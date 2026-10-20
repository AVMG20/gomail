package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

func labelByID(t *testing.T, s *Service, id string) models.Label {
	t.Helper()
	for _, l := range s.Settings().Labels {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("label %q not found", id)
	return models.Label{}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func firstMessage(t *testing.T, s *Service, accountID string) string {
	t.Helper()
	page, err := s.ListMessages(models.ListQuery{AccountID: accountID, FolderKind: models.FolderInbox, Limit: 1})
	if err != nil || len(page.Messages) == 0 {
		t.Fatalf("no inbox message for %s: %v", accountID, err)
	}
	return page.Messages[0].ID
}

func setLabels(t *testing.T, s *Service, extra ...models.Label) {
	t.Helper()
	st := s.GetSettings()
	st.Labels = append(st.Labels, extra...)
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
}

// Label rules only add a label to mail of a mailbox the label belongs to.
func TestRulesSkipLabelsOutsideTheirMailbox(t *testing.T) {
	labels := []models.Label{{ID: "g", Name: "G", AccountIDs: []string{"a1"}}, {ID: "x", Name: "X"}}
	rules := []models.LabelRule{
		{ID: "r1", Field: "from", Contains: "ci", Action: "label", LabelID: "g"},
		{ID: "r2", Field: "from", Contains: "ci", Action: "label", LabelID: "x"},
		{ID: "r3", Field: "from", Contains: "ci", Action: "label", LabelID: "gone"},
	}
	cands := []store.RuleCandidate{{ID: "m1", AccountID: "a1", FromEmail: "ci@example.com"}, {ID: "m2", AccountID: "a2", FromEmail: "ci@example.com"}}
	act := planRules(rules, labels, cands, true)
	if !slices.Equal(act.labels["g"], []string{"m1"}) || !slices.Equal(act.labels["x"], []string{"m1", "m2"}) || len(act.labels["gone"]) != 0 {
		t.Fatalf("plan %+v", act.labels)
	}

	e := newEnv(t, "")
	s := e.s
	st := s.GetSettings()
	st.Labels = append(st.Labels, models.Label{Name: "Builds", AccountIDs: []string{demo.GmailAccountID}})
	st.LabelRules = []models.LabelRule{{ID: "r1", Field: "from", Contains: "builds@ci.example", Action: "label", LabelID: "builds"}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	e.demos[demo.GmailAccountID].Deliver("Build 41 passed", "builds@ci.example")
	e.demos[demo.OutlookAccountID].Deliver("Build 42 passed", "builds@ci.example")
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if m, ok := labelsOf(t, e, demo.GmailAccountID, "Build 41 passed"); !ok || !has(m.Labels, "builds") {
		t.Fatalf("gmail mail not labeled: %+v", m.Labels)
	}
	if m, ok := labelsOf(t, e, demo.OutlookAccountID, "Build 42 passed"); !ok || has(m.Labels, "builds") {
		t.Fatalf("outlook mail labeled with a gmail label: %v %+v", ok, m.Labels)
	}
	if _, err := s.ApplyLabelRules(); err != nil {
		t.Fatal(err)
	}
	if m, _ := labelsOf(t, e, demo.OutlookAccountID, "Build 42 passed"); has(m.Labels, "builds") {
		t.Fatal("apply to existing mail labeled the other mailbox")
	}
}

// questionServer is a fake OpenRouter that records the labels asked about.
type questionServer struct {
	*httptest.Server
	mu    sync.Mutex
	asked [][]string
}

func newQuestionServer(t *testing.T) *questionServer {
	q := &questionServer{}
	q.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/decisions" {
			w.WriteHeader(404)
			return
		}
		var req struct {
			Questions map[string]any `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		answers := map[string]any{}
		var keys []string
		for k := range req.Questions {
			keys = append(keys, strings.TrimPrefix(k, "label_"))
			answers[k] = map[string]any{"type": "noul", "noul": 0.05}
		}
		slices.Sort(keys)
		q.mu.Lock()
		q.asked = append(q.asked, keys)
		q.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers, "usage": map[string]any{"cost": 0.00001}})
	}))
	t.Cleanup(q.Close)
	return q
}

func (q *questionServer) take() [][]string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.asked
	q.asked = nil
	return out
}

// The classifier is only offered the labels of the message's mailbox, and
// the "already classified" fingerprint is per mailbox.
func TestClassifierOffersMailboxLabels(t *testing.T) {
	srv := newQuestionServer(t)
	e := newEnv(t, srv.URL)
	s := e.s
	if err := s.SetOpenRouterKey("sk-or-test"); err != nil {
		t.Fatal(err)
	}
	setLabels(t, s, models.Label{Name: "Gmail Things", Description: "Only in Gmail.", AccountIDs: []string{demo.GmailAccountID}})
	g, o := firstMessage(t, s, demo.GmailAccountID), firstMessage(t, s, demo.OutlookAccountID)

	asked := func(id string) []string {
		t.Helper()
		srv.take()
		if _, err := s.ClassifyMessages([]string{id}); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, k := range srv.take() {
			keys = append(keys, k...)
		}
		return keys
	}
	work := strings.ReplaceAll(mailboxLabel(t, s, demo.OutlookAccountID, "Work"), "-", "_")
	if keys := asked(o); slices.Contains(keys, "gmail_things") || !slices.Contains(keys, work) || len(keys) == 0 {
		t.Fatalf("outlook message offered %v", keys)
	}
	if keys := asked(g); !slices.Contains(keys, "gmail_things") && !slices.Contains(keys, "gmail-things") {
		t.Fatalf("gmail message not offered its label: %v", keys)
	}
	// Imported labels need a description first.
	travel := mailboxLabel(t, s, demo.GmailAccountID, "Travel")
	if keys := asked(g); slices.Contains(keys, strings.ReplaceAll(travel, "-", "_")) {
		t.Fatalf("imported label without description offered: %v", keys)
	}

	// Already classified: skipped. Changing the Gmail-only label re-runs
	// Gmail mail only.
	st := s.GetSettings()
	for i := range st.Labels {
		if st.Labels[i].ID == "gmail-things" {
			st.Labels[i].Description = "Only in Gmail, edited."
		}
	}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.ClassifyMessages([]string{o}); !r[0].Skipped {
		t.Fatal("outlook message re-classified after a Gmail label changed")
	}
	if r, _ := s.ClassifyMessages([]string{g}); r[0].Skipped {
		t.Fatal("gmail message not re-classified after its label changed")
	}
	est, err := s.EstimateClassify(models.ListQuery{FolderKind: models.FolderInbox}, false, false)
	if err != nil || est.Unchanged != 2 {
		t.Fatalf("estimate %+v %v", est, err)
	}
}
