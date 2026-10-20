package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
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

// Labels saved before labels were per mailbox keep working everywhere;
// provider labels belong to the mailboxes they were imported from.
func TestLabelMailboxMigration(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	all := []string{demo.GmailAccountID, demo.OutlookAccountID}

	for _, l := range s.Settings().Labels {
		switch l.Source {
		case config.LabelSourceSystem:
			if len(l.AccountIDs) != 0 {
				t.Errorf("Junk should belong to every mailbox: %v", l.AccountIDs)
			}
		default:
			// Defaults (user) and Travel/Family (in both demo mailboxes).
			if !sameSet(l.AccountIDs, all) {
				t.Errorf("label %s mailboxes %v", l.ID, l.AccountIDs)
			}
		}
	}

	// A new label without mailboxes gets every mailbox.
	setLabels(t, s, models.Label{Name: "Plain", Color: "#123456"})
	if l := labelByID(t, s, "plain"); !sameSet(l.AccountIDs, all) {
		t.Fatalf("plain %v", l.AccountIDs)
	}

	// Stored settings from an older version: no mailbox lists at all.
	st := s.Settings()
	var provider string
	for i, l := range st.Labels {
		if l.Source == config.LabelSourceProvider && provider == "" {
			provider = l.ID
		}
		st.Labels[i].AccountIDs = nil
	}
	if provider == "" {
		t.Fatal("no provider label imported")
	}
	// Pretend the provider label was only ever seen in Gmail.
	if _, err := s.store.DB().Exec(`DELETE FROM message_labels WHERE label_id = ? AND account_id = ?`, provider, demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetSetting(settingsKey, st); err != nil {
		t.Fatal(err)
	}
	if err := s.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if l := labelByID(t, s, "plain"); !sameSet(l.AccountIDs, all) {
		t.Fatalf("migrated user label %v", l.AccountIDs)
	}
	if l := labelByID(t, s, provider); !sameSet(l.AccountIDs, []string{demo.GmailAccountID}) {
		t.Fatalf("migrated provider label %s: %v", provider, l.AccountIDs)
	}
	var stored models.Settings
	if err := s.store.GetSetting(settingsKey, &stored); err != nil {
		t.Fatal(err)
	}
	for _, l := range stored.Labels {
		if l.Source != config.LabelSourceSystem && len(l.AccountIDs) == 0 {
			t.Fatalf("migration not saved: %+v", l)
		}
	}

	// The UI can't change a provider label's mailboxes.
	st = s.GetSettings()
	for i := range st.Labels {
		if st.Labels[i].ID == provider {
			st.Labels[i].AccountIDs = []string{demo.OutlookAccountID}
		}
	}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if l := labelByID(t, s, provider); !sameSet(l.AccountIDs, []string{demo.GmailAccountID}) {
		t.Fatalf("provider label mailboxes changed by the UI: %v", l.AccountIDs)
	}
}

func TestProviderLabelsBelongToTheirMailboxes(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	ids := s.resolveProviderLabels(demo.GmailAccountID, []mail.Label{{Name: "Receipts", Color: "#16a766"}})
	id := ids["receipts"]
	if l := labelByID(t, s, id); l.Source != config.LabelSourceProvider || !sameSet(l.AccountIDs, []string{demo.GmailAccountID}) {
		t.Fatalf("new provider label %+v", l)
	}
	// The same name in another mailbox is the same label.
	s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "receipts"}})
	if l := labelByID(t, s, id); !sameSet(l.AccountIDs, []string{demo.GmailAccountID, demo.OutlookAccountID}) {
		t.Fatalf("shared provider label %v", l.AccountIDs)
	}

	// Removing a mailbox: labels forget it; a provider label of only that
	// mailbox goes; user labels of only that mailbox fall back to every one.
	s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "Outlook Only"}})
	setLabels(t, s, models.Label{Name: "Outlook Mine", AccountIDs: []string{demo.OutlookAccountID}})
	if err := s.RemoveAccount(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	for _, l := range s.Settings().Labels {
		if slices.Contains(l.AccountIDs, demo.OutlookAccountID) {
			t.Errorf("label %s still lists the removed mailbox", l.ID)
		}
		if l.ID == "outlook-only" {
			t.Error("provider label of the removed mailbox kept")
		}
	}
	if l := labelByID(t, s, "outlook-mine"); !sameSet(l.AccountIDs, []string{demo.GmailAccountID}) {
		t.Fatalf("user label of the removed mailbox %v", l.AccountIDs)
	}

	// Adding a mailbox: labels of every mailbox ("All") include it.
	setLabels(t, s, models.Label{Name: "Gmail Only", AccountIDs: []string{"elsewhere"}})
	if err := s.store.InsertAccount(models.Account{ID: "a-new", Provider: models.ProviderGmail, Email: "sam@example.com", DisplayName: "Sam", Color: "#000000", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.labelsGainAccount("a-new")
	if l := labelByID(t, s, "outlook-mine"); !sameSet(l.AccountIDs, []string{demo.GmailAccountID, "a-new"}) {
		t.Fatalf("All label after adding a mailbox %v", l.AccountIDs)
	}
	if l := labelByID(t, s, "gmail-only"); slices.Contains(l.AccountIDs, "a-new") {
		t.Fatalf("scoped label gained the new mailbox %v", l.AccountIDs)
	}
}

// A label is only added by hand to mail of its mailboxes.
func TestAddLabelOutsideItsMailbox(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	setLabels(t, s, models.Label{Name: "Gmail Things", AccountIDs: []string{demo.GmailAccountID}})
	g, o := firstMessage(t, s, demo.GmailAccountID), firstMessage(t, s, demo.OutlookAccountID)
	if err := s.AddLabel([]string{o}, "gmail-things"); err == nil {
		t.Fatal("label added to mail of another mailbox")
	}
	if err := s.AddLabel([]string{g, o}, "gmail-things"); err != nil {
		t.Fatal(err)
	}
	if _, ok := labelOf(t, s.store, g, "gmail-things"); !ok {
		t.Fatal("gmail message not labeled")
	}
	if _, ok := labelOf(t, s.store, o, "gmail-things"); ok {
		t.Fatal("outlook message labeled")
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
	if keys := asked(o); slices.Contains(keys, "gmail_things") || !slices.Contains(keys, "work") || len(keys) == 0 {
		t.Fatalf("outlook message offered %v", keys)
	}
	if keys := asked(g); !slices.Contains(keys, "gmail_things") && !slices.Contains(keys, "gmail-things") {
		t.Fatalf("gmail message not offered its label: %v", keys)
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

// A backup's label mailboxes point at the accounts as they exist here.
func TestBackupMapsLabelMailboxes(t *testing.T) {
	src := newEnv(t, "")
	setLabels(t, src.s, models.Label{Name: "Outlook Stuff", AccountIDs: []string{demo.OutlookAccountID}},
		models.Label{Name: "Gmail Stuff", AccountIDs: []string{demo.GmailAccountID}})
	var buf bytes.Buffer
	if _, err := src.s.WriteBackup(&buf, false); err != nil {
		t.Fatal(err)
	}

	// This install has the Gmail mailbox under another id.
	b := newBlankService(t)
	if err := b.store.InsertAccount(models.Account{ID: "a-local", Provider: models.ProviderGmail, Email: "alex.demo@gmail.com", DisplayName: "Alex Morgan", Color: "#000000", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	b.labelsGainAccount("a-local")
	if _, err := b.ImportBackupFrom(&buf); err != nil {
		t.Fatal(err)
	}
	if l := labelByID(t, b, "gmail-stuff"); !sameSet(l.AccountIDs, []string{"a-local"}) {
		t.Fatalf("gmail label %v", l.AccountIDs)
	}
	if l := labelByID(t, b, "outlook-stuff"); !sameSet(l.AccountIDs, []string{demo.OutlookAccountID}) {
		t.Fatalf("outlook label %v", l.AccountIDs)
	}
	if l := labelByID(t, b, "work"); !sameSet(l.AccountIDs, []string{"a-local", demo.OutlookAccountID}) {
		t.Fatalf("label of every mailbox %v", l.AccountIDs)
	}
}
