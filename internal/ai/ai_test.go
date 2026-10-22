package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

func newTestClient(t *testing.T, h http.HandlerFunc, key string) *Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, func() (string, error) { return key, nil }, filepath.Join(t.TempDir(), "models.json"))
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestDecisionsRequestShapeAndScores(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/decisions" || r.Header.Get("Authorization") != "Bearer sk-test" || r.Header.Get("X-Title") != "gomail" || r.Header.Get("HTTP-Referer") == "" {
			t.Errorf("bad request %s %v", r.URL, r.Header)
		}
		json.NewDecoder(r.Body).Decode(&got)
		qs := got["questions"].(map[string]any)
		answers := map[string]any{}
		for k := range qs {
			switch k {
			case "label_finance":
				answers[k] = map[string]any{"type": "noul", "noul": 0.91}
			case "label_work_stuff":
				answers[k] = map[string]any{"type": "noul", "noul": 0.07}
			default:
				answers[k] = map[string]any{"type": "noul", "noul": 1.7} // out of range -> clamped
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "d1", "model": "typesafe/jev-1.13-20260901", "provider": "TypeSafe",
			"answers": answers, "usage": map[string]any{"input_tokens": 500, "output_tokens": 0, "cost": 0.0001}})
	}, "sk-test")

	labels := []LabelDef{{ID: "finance", Name: "Finance", Description: "Bills and receipts"}, {ID: "work stuff", Name: "Work"}, {ID: "x", Name: "X"}}
	email := Email{From: models.Address{Name: "Acme", Email: "billing@acme.com"}, Subject: "Invoice", HasAttachments: true,
		Body: "Your invoice is attached.\n\nOn Mon, Oct 5, 2026 at 10:00 Bob wrote:\n> old stuff\n> more", Date: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
	res, err := c.ScoreLabels(context.Background(), "", "", labels, email)
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != JevModel {
		t.Errorf("model %v", got["model"])
	}
	state := got["state"].(map[string]any)
	if state["from"] != "Acme <billing@acme.com>" || state["subject"] != "Invoice" || state["has_attachments"] != true || state["date"] != "2026-10-05T00:00:00Z" {
		t.Errorf("state %v", state)
	}
	if strings.Contains(state["body"].(string), "old stuff") {
		t.Errorf("quoted reply not stripped: %q", state["body"])
	}
	q := got["questions"].(map[string]any)["label_finance"].(map[string]any)
	if q["type"] != "noul" || !strings.Contains(q["instructions"].(string), `"Finance"`) {
		t.Errorf("question %v", q)
	}
	crit := q["criteria"].(map[string]any)
	if crit["true"] != "Bills and receipts" || crit["false"] == "" {
		t.Errorf("criteria %v", crit)
	}
	if res.Scores["finance"] != 0.91 || res.Scores["work stuff"] != 0.07 || res.Scores["x"] != 1 || res.Cost != 0.0001 {
		t.Fatalf("scores %+v", res)
	}
}

func TestDecisionsChunkingAndErrors(t *testing.T) {
	var calls, throttled int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&throttled, 1) == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":429,"message":"slow down"}}`))
			return
		}
		atomic.AddInt32(&calls, 1)
		var req DecisionRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Questions) > MaxLabelsPerRequest {
			t.Errorf("too many questions: %d", len(req.Questions))
		}
		answers := map[string]Answer{}
		for k := range req.Questions {
			p := 0.5
			answers[k] = Answer{Type: "noul", Noul: &p}
		}
		json.NewEncoder(w).Encode(DecisionResponse{Answers: answers})
	}, "sk")
	var labels []LabelDef
	for i := 0; i < 45; i++ {
		labels = append(labels, LabelDef{ID: "l" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Name: "L"})
	}
	res, err := c.ScoreLabels(context.Background(), JevLatestModel, "", labels, Email{Subject: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(res.Scores) != 45 {
		t.Fatalf("calls %d scores %d", calls, len(res.Scores))
	}

	for status, want := range map[int]string{401: "rejected the API key", 402: "insufficient credits", 413: "too large"} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":{"code":` + http.StatusText(status) + `,"message":"nope"}}`))
		}, "sk")
		_, err := c.ScoreLabels(context.Background(), JevModel, "", labels[:1], Email{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
	// Missing answers are an error; non-decision models are rejected.
	c = newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"answers":{}}`)) }, "sk")
	if _, err := c.ScoreLabels(context.Background(), JevModel, "", labels[:1], Email{}); err == nil {
		t.Error("expected missing answer error")
	}
	if _, err := c.ScoreLabels(context.Background(), "openai/gpt-4o-mini", "", labels[:1], Email{}); err == nil {
		t.Error("expected non-decision model error")
	}
	noKey := New("http://unused", func() (string, error) { return "", nil }, "")
	if _, err := noKey.ScoreLabels(context.Background(), JevModel, "", labels[:1], Email{}); err != ErrNoKey {
		t.Errorf("no key: %v", err)
	}
}

func TestAnswerParsing(t *testing.T) {
	var a Answer
	json.Unmarshal([]byte(`{"type":"choice","choice":"payments","confidence":0.9,"probabilities":{"payments":0.78,"frontend":0.22}}`), &a)
	if a.Choice != "payments" || a.Probabilities["frontend"] != 0.22 {
		t.Fatal(a)
	}
	if _, ok := a.YesProbability(); ok {
		t.Error("choice without true/false is not a yes probability")
	}
	json.Unmarshal([]byte(`{"type":"choice","choice":"false","confidence":0.8}`), &a)
	a.Probabilities = nil
	if p, ok := a.YesProbability(); !ok || p < 0.19 || p > 0.21 {
		t.Errorf("choice false: %v %v", p, ok)
	}
	var s Answer
	json.Unmarshal([]byte(`{"type":"score","score":2,"confidence":0.7,"probabilities":{"0":0.1,"1":0.2,"2":0.7},"legend":["low","mid","high"]}`), &s)
	if s.Score == nil || *s.Score != 2 || len(s.Legend) == 0 {
		t.Errorf("score %+v", s)
	}
}

func TestListModelsPricingAndCache(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "" {
			t.Error("models listed with a key header although none is set")
		}
		w.Write([]byte(`{"data":[
			{"id":"z/model","name":"Zeta","context_length":8000,"pricing":{"prompt":"0.0000015","completion":"0.000002"}},
			{"id":"a/auto","name":"Auto Router","pricing":{"prompt":"-1","completion":"-1"}},
			{"id":"b/free","name":"beta free","pricing":{"prompt":"0","completion":"0"}},
			{"id":"typesafe/jev-1.13","name":"Jev","pricing":{"prompt":"0.0000001"}}
		]}`))
	}, "")
	list, err := c.ListModels(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byID := map[string]models.AIModel{}
	for _, m := range list {
		names = append(names, m.Name)
		byID[m.ID] = m
	}
	if strings.Join(names, ",") != "Auto Router,beta free,Jev,TypeSafe: Jev (latest),Zeta" {
		t.Fatalf("order %v", names)
	}
	if z := byID["z/model"]; z.PromptPrice != 1.5 || z.CompletionPrice != 2 || z.PriceUnknown || z.ContextLength != 8000 || z.Kind != "chat" {
		t.Errorf("zeta %+v", z)
	}
	if a := byID["a/auto"]; !a.PriceUnknown || a.PromptPrice != 0 {
		t.Errorf("auto %+v", a)
	}
	if f := byID["b/free"]; f.PriceUnknown {
		t.Errorf("free %+v", f)
	}
	if j := byID[JevModel]; j.Kind != "decision" || j.PromptPrice != 0.1 || j.PriceUnknown {
		t.Errorf("jev %+v", j)
	}
	if _, ok := byID[JevLatestModel]; !ok {
		t.Error("jev latest not synthesized")
	}
	c.ListModels(context.Background(), false)
	if calls != 1 {
		t.Errorf("not cached: %d", calls)
	}
	c.ListModels(context.Background(), true)
	if calls != 2 {
		t.Errorf("force: %d", calls)
	}
	// A new client with the same cache file loads from disk.
	c2 := New("http://127.0.0.1:1", func() (string, error) { return "", nil }, c.models.file)
	l2, err := c2.ListModels(context.Background(), false)
	if err != nil || len(l2) != len(list) {
		t.Errorf("disk cache: %v %d", err, len(l2))
	}
}

func TestChatAndGrammarCleanup(t *testing.T) {
	var last map[string]any
	reply := "```\nHello, world!\n```"
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&last)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": reply}}}})
	}, "sk")
	out, err := c.ImproveGrammar(context.Background(), "openai/gpt-4o-mini", "", "", "helo world\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello, world!\n" {
		t.Fatalf("%q", out)
	}
	msgs := last["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != DefaultGrammarPrompt || last["temperature"].(float64) != 0 {
		t.Errorf("request %v", last)
	}
	reply = `"Fixed text."`
	out, _ = c.ImproveGrammar(context.Background(), "m", "", "custom prompt", "fixd text.")
	if out != "Fixed text." {
		t.Fatalf("quotes: %q", out)
	}
	if msgs := last["messages"].([]any); msgs[0].(map[string]any)["content"] != "custom prompt" {
		t.Error("custom prompt not used")
	}
	reply = "<p>Hello <b>there</b></p>"
	out, _ = c.ImproveGrammar(context.Background(), "m", "", "", "<p>Helo <b>ther</b></p>")
	if out != "<p>Hello <b>there</b></p>" {
		t.Fatalf("html: %q", out)
	}
	if _, err := c.ImproveGrammar(context.Background(), JevModel, "", "", "x"); err == nil {
		t.Error("jev cannot chat")
	}
}

func TestCleanBody(t *testing.T) {
	in := "Hi Bob,\n\nSee   the plan below.\n\nThanks\n-- \nAlice\nCEO\n"
	if got := CleanBody(in, 100); got != "Hi Bob,\n\nSee the plan below.\n\nThanks" {
		t.Fatalf("%q", got)
	}
	if got := CleanBody(strings.Repeat("a", 50), 10); got != "aaaaaaaaaa…" {
		t.Fatalf("%q", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	labels := make([]LabelDef, 25)
	tok, req := EstimateTokens(4000, labels, "")
	if req != 2 || tok < 2000 {
		t.Fatalf("%d %d", tok, req)
	}
}
