package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
)

type fakeUI struct {
	mu       sync.Mutex
	events   []string
	payloads []any
	opened   []string
	savePath string
	notified []string
}

func (f *fakeUI) Emit(event string, data any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	f.payloads = append(f.payloads, data)
}
func (f *fakeUI) OpenURL(u string) error {
	f.mu.Lock()
	f.opened = append(f.opened, u)
	f.mu.Unlock()
	return nil
}
func (f *fakeUI) SaveFile(string) (string, error) { return f.savePath, nil }
func (f *fakeUI) ApplySettings(models.Settings)   {}
func (f *fakeUI) Notify(title, body string, _ models.NotificationOpen) {
	f.mu.Lock()
	f.notified = append(f.notified, title)
	f.mu.Unlock()
}
func (f *fakeUI) count(event string) (n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e == event {
			n++
		}
	}
	return n
}

func (f *fakeUI) last(event string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.events) - 1; i >= 0; i-- {
		if f.events[i] == event {
			return f.payloads[i]
		}
	}
	return nil
}

// failingProvider wraps a provider and fails SetRead on demand.
type failingProvider struct {
	provider.Provider
	failRead bool
}

func (f *failingProvider) SetRead(ctx context.Context, ids []string, read bool) error {
	if f.failRead {
		return errors.New("server said no")
	}
	return f.Provider.SetRead(ctx, ids, read)
}

type env struct {
	s     *Service
	ui    *fakeUI
	provs map[string]*failingProvider
	demos map[string]*demo.Provider
}

func newEnv(t *testing.T, openRouter string) *env {
	t.Helper()
	e := &env{ui: &fakeUI{savePath: filepath.Join(t.TempDir(), "out.bin")}, provs: map[string]*failingProvider{}, demos: map[string]*demo.Provider{}}
	var mu sync.Mutex
	s, err := New(Options{
		DataDir: t.TempDir(), Version: "test", Demo: true, UI: e.ui, Secrets: secrets.NewMemory(), OpenRouterBase: openRouter,
		ProviderFactory: func(acct models.Account) (provider.Provider, error) {
			mu.Lock()
			defer mu.Unlock()
			d := demo.New(acct)
			d.Latency, d.NewMailChance = 0, 0
			e.demos[acct.ID] = d
			fp := &failingProvider{Provider: d}
			e.provs[acct.ID] = fp
			return fp, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	e.s = s
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDemoSyncAndBrowse(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	accts, err := s.Accounts()
	if err != nil || len(accts) != 2 {
		t.Fatalf("accounts %v %v", accts, err)
	}
	for _, a := range accts {
		if a.UnreadCount == 0 || a.LastSyncAt == nil || a.SyncError != "" {
			t.Errorf("account %+v", a)
		}
	}
	page, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total < 60 || len(page.Messages) != 20 || page.NextCursor == "" {
		t.Fatalf("unified inbox total=%d len=%d", page.Total, len(page.Messages))
	}
	// Provider labels merged by name with the default labels.
	st := s.GetSettings()
	names := map[string]string{}
	for _, l := range st.Labels {
		names[l.Name] = l.Source
	}
	if names["Work"] != "user" || names["Family"] != "provider" || names["Travel"] != "provider" || names["Junk"] != "system" {
		t.Fatalf("labels %v", names)
	}
	counts, _ := s.LabelCounts("")
	byLabel := map[string]models.LabelCount{}
	for _, c := range counts {
		byLabel[c.LabelID] = c
	}
	if byLabel["work"].Total == 0 || byLabel["junk"].Total == 0 {
		t.Fatalf("label counts %v", counts)
	}
	// Newsletter with an inline image: cid rewritten and sanitized.
	res, err := s.SearchMessages(models.SearchQuery{Query: "morning brew"})
	if err != nil || len(res) == 0 {
		t.Fatalf("search %v %v", res, err)
	}
	m, err := s.Message(res[0].Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.BodyHTML, "data:image/png;base64,") || strings.Contains(m.BodyHTML, "cid:") || m.BodyText == "" {
		t.Fatalf("body %q", m.BodyHTML[:min(300, len(m.BodyHTML))])
	}
	// Threads.
	res, _ = s.SearchMessages(models.SearchQuery{Query: "subject:roadmap from:priya"})
	if len(res) == 0 {
		t.Fatal("thread root not found")
	}
	thread, err := s.Thread(res[0].Message.ID)
	if err != nil || len(thread) < 3 {
		t.Fatalf("thread %d %v", len(thread), err)
	}
	for i := 1; i < len(thread); i++ {
		if thread[i].Date.Before(thread[i-1].Date) || thread[i].BodyHTML == "" {
			t.Fatal("thread order/bodies")
		}
	}
	// Attachments.
	var withAtt models.Message
	for _, msg := range thread {
		if len(msg.Attachments) > 0 {
			withAtt = msg
		}
	}
	if withAtt.ID == "" {
		t.Fatal("no attachment in thread")
	}
	if err := s.SaveAttachment(withAtt.ID, withAtt.Attachments[0].ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(e.ui.savePath); !strings.HasPrefix(string(data), "%PDF") {
		t.Fatalf("saved %q", data)
	}
	if err := s.OpenExternalURL("javascript:alert(1)"); err == nil {
		t.Fatal("javascript url allowed")
	}
	if err := s.OpenExternalURL("https://example.com"); err != nil || len(e.ui.opened) != 1 {
		t.Fatal(err)
	}
}

func TestOptimisticActionsAndRevert(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	page, _ := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox, UnreadOnly: true})
	if len(page.Messages) < 2 {
		t.Fatal("need unread messages")
	}
	ids := []string{page.Messages[0].ID, page.Messages[1].ID}
	before, _ := s.store.Account(demo.GmailAccountID)

	e.provs[demo.GmailAccountID].failRead = true
	err := s.MarkRead(ids, true)
	if err == nil || !strings.Contains(err.Error(), "server said no") || !strings.Contains(err.Error(), "alex.demo@gmail.com") {
		t.Fatalf("expected readable error, got %v", err)
	}
	after, _ := s.store.Account(demo.GmailAccountID)
	if after.UnreadCount != before.UnreadCount {
		t.Fatalf("not reverted: %d -> %d", before.UnreadCount, after.UnreadCount)
	}
	e.provs[demo.GmailAccountID].failRead = false
	if err := s.MarkRead(ids, true); err != nil {
		t.Fatal(err)
	}
	after, _ = s.store.Account(demo.GmailAccountID)
	if after.UnreadCount != before.UnreadCount-2 {
		t.Fatalf("unread %d -> %d", before.UnreadCount, after.UnreadCount)
	}
	// The provider got it: a resync keeps them read.
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	sums, _ := s.store.Summaries(ids)
	for _, m := range sums {
		if m.Unread {
			t.Fatal("unread after resync")
		}
	}
	// Archive, trash, spam (junk label) and back.
	if err := s.Archive(ids[:1]); err != nil {
		t.Fatal(err)
	}
	inbox, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderInbox)
	m, _, _ := s.store.Message(ids[0])
	for _, f := range m.FolderIDs {
		if f == inbox.ID {
			t.Fatal("still in inbox after archive")
		}
	}
	if err := s.Trash(ids[:1]); err != nil {
		t.Fatal(err)
	}
	trash, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderTrash)
	m, _, _ = s.store.Message(ids[0])
	if len(m.FolderIDs) != 1 || m.FolderIDs[0] != trash.ID {
		t.Fatalf("trash folders %v", m.FolderIDs)
	}
	spam, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderSpam)
	if err := s.MoveToFolder(ids[:1], spam.ID); err != nil {
		t.Fatal(err)
	}
	m, _, _ = s.store.Message(ids[0])
	if !containsStr(m.Labels, "junk") {
		t.Fatalf("junk label missing %v", m.Labels)
	}
	if err := s.MoveToFolder(ids[:1], inbox.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	m, _, _ = s.store.Message(ids[0])
	if containsStr(m.Labels, "junk") || !containsStr(m.FolderIDs, inbox.ID) {
		t.Fatalf("after moving back: labels %v folders %v", m.Labels, m.FolderIDs)
	}
	// Cross-account move is refused.
	outlookInbox, _ := s.store.FolderByKind(demo.OutlookAccountID, models.FolderInbox)
	if err := s.MoveToFolder(ids[:1], outlookInbox.ID); err == nil {
		t.Fatal("cross-account move allowed")
	}
	// Stars.
	if err := s.SetStarred(ids, true); err != nil {
		t.Fatal(err)
	}
	starred, _ := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderStarred})
	n := 0
	for _, x := range starred.Messages {
		if x.ID == ids[0] || x.ID == ids[1] {
			n++
		}
	}
	if n != 2 {
		t.Fatal("starred view")
	}
	if e.ui.count(models.EventMessagesChanged) == 0 {
		t.Fatal("no events")
	}
}

func TestLabelsSendAndNewMail(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	page, _ := s.ListMessages(models.ListQuery{AccountID: demo.OutlookAccountID, FolderKind: models.FolderInbox})
	id := page.Messages[0].ID
	if err := s.AddLabel([]string{id}, "important"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLabel([]string{id}, "nope"); err == nil {
		t.Fatal("unknown label accepted")
	}
	p, _ := s.ListMessages(models.ListQuery{Labels: []string{"important"}})
	if p.Total != 1 {
		t.Fatalf("label filter %d", p.Total)
	}
	// Deleting a label in Settings removes its assignments.
	st := s.GetSettings()
	var kept []models.Label
	for _, l := range st.Labels {
		if l.ID != "important" {
			kept = append(kept, l)
		}
	}
	st.Labels = kept
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	p, _ = s.ListMessages(models.ListQuery{Labels: []string{"important"}})
	if p.Total != 0 {
		t.Fatal("assignments survived label deletion")
	}
	// Reply and see it in Sent after sync.
	err := s.Send(models.ComposeRequest{AccountID: demo.OutlookAccountID, To: []models.Address{{Email: "ana@example.com"}},
		BodyHTML: "<p>Zebracorn appreciation!</p>", ReplyToID: id, Mode: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(models.ComposeRequest{AccountID: demo.OutlookAccountID, To: []models.Address{{Email: "not an address"}}, BodyText: "x"}); err == nil {
		t.Fatal("invalid address accepted")
	}
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	res, _ := s.SearchMessages(models.SearchQuery{Query: "in:sent zebracorn"})
	if len(res) == 0 || !strings.HasPrefix(res[0].Message.Subject, "Re: ") {
		t.Fatalf("sent reply not found: %+v", res)
	}
	orig, _, _ := s.store.Message(id)
	if res[0].Message.ThreadID != orig.ThreadID {
		t.Fatal("reply not threaded")
	}
	// New mail: event + notification.
	e.demos[demo.OutlookAccountID].Deliver("Fresh news", "news@example.com")
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	ev, ok := e.ui.last(models.EventNewMail).(models.NewMailEvent)
	if !ok || ev.Count != 1 || ev.Latest.Subject != "Fresh news" || len(e.ui.notified) != 1 {
		t.Fatalf("new mail event %+v %v", ev, e.ui.notified)
	}
}

// fakeOpenRouter answers decisions with P(yes)=0.9 for "finance" and 0.05
// for everything else.
func fakeOpenRouter(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha/decisions":
			var req struct {
				Questions map[string]any `json:"questions"`
				State     map[string]any `json:"state"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			answers := map[string]any{}
			for k := range req.Questions {
				p := 0.05
				if k == "label_finance" && strings.Contains(strings.ToLower(req.State["subject"].(string)+req.State["body"].(string)), "invoice") {
					p = 0.9
				}
				answers[k] = map[string]any{"type": "noul", "noul": p}
			}
			json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]any{"cost": 0.00002}})
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"typesafe/jev-1.13","name":"Jev","pricing":{"prompt":"0.0000002","completion":"0"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestClassification(t *testing.T) {
	srv := fakeOpenRouter(t)
	defer srv.Close()
	e := newEnv(t, srv.URL)
	s := e.s
	res, _ := s.SearchMessages(models.SearchQuery{Query: "invoice for september", AccountID: demo.GmailAccountID})
	if len(res) == 0 {
		t.Fatal("invoice not found")
	}
	if _, err := s.ClassifyMessages([]string{res[0].Message.ID}); err == nil || !strings.Contains(err.Error(), "OpenRouter API key") {
		t.Fatalf("expected key error, got %v", err)
	}
	if err := s.SetOpenRouterKey("sk-or-test"); err != nil {
		t.Fatal(err)
	}
	if !s.GetSettings().OpenRouterKeySet {
		t.Fatal("key not set")
	}
	id := res[0].Message.ID
	out, err := s.ClassifyMessages([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	r := out[0]
	if r.Scores["finance"] != 0.9 || r.Error != "" {
		t.Fatalf("result %+v", r)
	}
	// The demo invoice already carries the provider "Finance" label, so it
	// is kept (not re-added).
	m, _, _ := s.store.Message(id)
	if !containsStr(m.Labels, "finance") {
		t.Fatalf("labels %v", m.Labels)
	}

	est, err := s.EstimateClassify(models.ListQuery{AccountID: demo.GmailAccountID}, false, false)
	if err != nil || est.Count == 0 || est.Unchanged != 1 || est.Requests != est.Count-1 || !est.PriceKnown || est.EstimatedCostUSD <= 0 {
		t.Fatalf("estimate %+v %v", est, err)
	}
	job, err := s.ClassifyQuery(models.ListQuery{AccountID: demo.GmailAccountID}, false, false)
	if err != nil || job.Total != est.Count {
		t.Fatalf("job %+v %v", job, err)
	}
	if _, err := s.ClassifyQuery(models.ListQuery{}, false, false); err == nil {
		t.Log("second job started before first finished (it may have completed already)")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if p, ok := e.ui.last(models.EventClassifyFinished).(models.ClassifyProgress); ok && p.JobID == job.JobID {
			if p.Done != job.Total || p.Failed != 0 || p.Cost <= 0 {
				t.Fatalf("final %+v", p)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Trash/drafts were excluded.
	trash, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderTrash)
	tp, _ := s.ListMessages(models.ListQuery{FolderID: trash.ID})
	for _, m := range tp.Messages {
		in, _ := s.store.ClassifyInputs([]string{m.ID})
		if !in[0].ClassifiedAt.IsZero() {
			t.Fatal("trash message was classified")
		}
	}
	if err := s.CancelClassify("nope"); err == nil {
		t.Fatal("cancel unknown job")
	}
	// Unchanged messages are not sent again.
	again, err := s.ClassifyMessages([]string{id})
	if err != nil || !again[0].Skipped || again[0].Cost != 0 || again[0].Scores["finance"] != 0.9 {
		t.Fatalf("re-run %+v %v", again, err)
	}
	// Editing a label definition invalidates the fingerprint.
	st := s.GetSettings()
	st.Labels[0].Description += " (edited)"
	s.SaveSettings(st)
	again, _ = s.ClassifyMessages([]string{id})
	if again[0].Skipped {
		t.Fatal("label change did not trigger re-classification")
	}
	usage, err := s.AIUsage()
	if err != nil || usage.TodayUSD <= 0 || usage.RequestsToday < job.Total || usage.MonthUSD < usage.TodayUSD || usage.DailyBudgetUSD != 1 {
		t.Fatalf("usage %+v %v", usage, err)
	}
	// Budget reached: automatic labeling pauses with an event.
	st = s.GetSettings()
	st.AIDailyBudgetUSD = 0.0001
	st.AutoClassify = true
	s.SaveSettings(st)
	e.demos[demo.GmailAccountID].Deliver("Invoice for budget test", "billing@acme.example")
	s.SyncNow(demo.GmailAccountID)
	if ev, ok := e.ui.last(models.EventAIBudget).(models.AIUsage); !ok || !ev.BudgetReached {
		t.Fatalf("no budget event: %+v", ev)
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
