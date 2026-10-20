package core

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// mailboxLabel returns the id of a mailbox's label called name.
func mailboxLabel(t *testing.T, s *Service, accountID, name string) string {
	t.Helper()
	l, ok := config.LabelNamed(s.Settings().Labels, accountID, name)
	if !ok {
		t.Fatalf("%s has no label %q", accountID, name)
	}
	return l.ID
}

// Every mailbox has its own labels: the demo mailboxes each get their own
// copies of the defaults, and their Gmail labels / Outlook categories of
// the same name are separate labels. Editing one never changes the other.
func TestEveryMailboxHasItsOwnLabels(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	for _, l := range s.Settings().Labels {
		if l.Source == config.LabelSourceSystem {
			if len(l.AccountIDs) != 0 {
				t.Errorf("Junk should belong to every mailbox: %v", l.AccountIDs)
			}
		} else if len(l.AccountIDs) != 1 {
			t.Errorf("label %s belongs to %v", l.ID, l.AccountIDs)
		}
	}
	for _, name := range []string{"Important", "Work", "Travel"} {
		g, o := mailboxLabel(t, s, demo.GmailAccountID, name), mailboxLabel(t, s, demo.OutlookAccountID, name)
		if g == o {
			t.Errorf("%s is shared: %s", name, g)
		}
	}
	gw, ow := mailboxLabel(t, s, demo.GmailAccountID, "Work"), mailboxLabel(t, s, demo.OutlookAccountID, "Work")
	before := labelByID(t, s, ow)
	st := s.GetSettings()
	for i := range st.Labels {
		if st.Labels[i].ID == gw {
			st.Labels[i].Name, st.Labels[i].Color, st.Labels[i].Description, st.Labels[i].Then = "Job", "#000000", "Edited.", config.LabelThenArchive
		}
	}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if l := labelByID(t, s, gw); l.Name != "Job" || l.Then != config.LabelThenArchive {
		t.Fatalf("gmail label %+v", l)
	}
	if l := labelByID(t, s, ow); l.Name != before.Name || l.Color != before.Color || l.Description != before.Description || l.Then != "" {
		t.Fatalf("outlook label changed with gmail's: %+v", l)
	}
	// The same name may exist once per mailbox.
	setLabels(t, s, models.Label{Name: "Job", AccountIDs: []string{demo.OutlookAccountID}}, models.Label{Name: "job", AccountIDs: []string{demo.GmailAccountID}})
	n := 0
	for _, l := range s.Settings().Labels {
		if strings.EqualFold(l.Name, "job") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d labels called Job, want one per mailbox", n)
	}
}

// Settings from before labels were per mailbox: on startup, every mailbox
// gets its own copy of the labels it shared, and its assignments,
// corrections, rules, quiet labels and AI fingerprints follow the copy.
func TestLabelMigrationSplitsSharedLabels(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	G, O := demo.GmailAccountID, demo.OutlookAccountID
	if err := s.store.DeleteUnknownLabels(nil); err != nil { // a clean slate
		t.Fatal(err)
	}
	legacy := s.Settings()
	legacy.Labels = []models.Label{
		{ID: "news", Name: "News", Color: "#111111", Description: "Newsletters.", Source: config.LabelSourceUser, Then: config.LabelThenArchive}, // every mailbox
		{ID: "travel", Name: "Travel", Color: "#222222", Source: config.LabelSourceProvider, AccountIDs: []string{G, O}},
		{ID: "mine", Name: "Mine", Color: "#333333", Source: config.LabelSourceUser, AccountIDs: []string{G}},
		config.JunkLabel(),
	}
	legacy.LabelRules = []models.LabelRule{{ID: "rule_1", Field: "from", Contains: "digest", Action: "label", LabelID: "news"}}
	legacy.QuietLabels = map[string][]string{O: {"news", "junk"}}

	page, err := s.ListMessages(models.ListQuery{AccountID: O, FolderKind: models.FolderInbox, Limit: 3})
	if err != nil || len(page.Messages) < 3 {
		t.Fatalf("outlook messages %v", err)
	}
	g := firstMessage(t, s, G)
	o, rejected, guessed := page.Messages[0].ID, page.Messages[1].ID, page.Messages[2].ID
	for _, label := range []string{"news", "travel"} {
		if err := s.store.AddLabels([]string{g, o}, label); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.RemoveLabels([]string{rejected}, "news"); err != nil {
		t.Fatal(err)
	}
	// An AI-labeled message, fingerprinted with the old (shared) label set.
	var was []ai.LabelDef
	for _, l := range config.LabelsForAccount(legacy.Labels, O) {
		was = append(was, ai.LabelDef{ID: l.ID, Name: l.Name, Description: l.Description})
	}
	if _, _, err := s.store.ApplyAIScores(guessed, map[string]float64{"news": 0.9, "travel": 0.8}, store.AIPolicy{AddThreshold: 0.5},
		labelsetHashOf(legacy, was)+":msg"); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{settingsKey: legacy, labelsVersionKey: 0, dismissedLabelsKey: []string{"old category"}} {
		if err := s.store.SetSetting(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.loadSettings(); err != nil {
		t.Fatal(err)
	}

	news, travel := mailboxLabel(t, s, O, "News"), mailboxLabel(t, s, O, "Travel")
	if mailboxLabel(t, s, G, "News") != "news" || mailboxLabel(t, s, G, "Travel") != "travel" || news == "news" || travel == "travel" {
		t.Fatalf("the first mailbox keeps the id, the others get copies: %s %s", news, travel)
	}
	if l := labelByID(t, s, news); l.Color != "#111111" || l.Description != "Newsletters." || l.Then != config.LabelThenArchive || l.Source != config.LabelSourceUser {
		t.Fatalf("copy %+v", l)
	}
	if l := labelByID(t, s, travel); l.Source != config.LabelSourceProvider || l.Color != "#222222" {
		t.Fatalf("provider copy %+v", l)
	}
	if _, ok := config.LabelNamed(s.Settings().Labels, O, "Mine"); ok {
		t.Fatal("a label of one mailbox was copied")
	}
	for _, c := range []struct {
		id, label string
		want      bool
	}{{g, "news", true}, {g, "travel", true}, {o, news, true}, {o, travel, true}, {o, "news", false}, {o, "travel", false}, {guessed, news, true}} {
		if _, ok := labelOf(t, s.store, c.id, c.label); ok != c.want {
			t.Errorf("message %s label %s: %v, want %v", c.id, c.label, ok, c.want)
		}
	}
	var n int
	_ = s.store.DB().QueryRow(`SELECT count(*) FROM label_rejections WHERE message_id = ? AND label_id = ?`, rejected, news).Scan(&n)
	if n != 1 {
		t.Fatal("the rejection didn't follow the copy")
	}
	// The classifier isn't offered imported labels without a description
	// any more: its guesses of them go.
	_ = s.store.DB().QueryRow(`SELECT count(*) FROM message_labels WHERE source = 'ai' AND label_id IN ('travel', ?)`, travel).Scan(&n)
	if n != 0 {
		t.Fatal("AI guesses of an imported label without description kept")
	}
	in, err := s.store.ClassifyInputs([]string{guessed})
	if err != nil || len(in) != 1 {
		t.Fatalf("classify input %v", err)
	}
	if !strings.HasPrefix(in[0].AIHash, s.labelsetHash(O)+":") || in[0].AIScores[news] != 0.9 {
		t.Fatalf("AI fingerprint not kept: %s %v", in[0].AIHash, in[0].AIScores)
	}
	st := s.Settings()
	rules := ""
	for _, r := range st.LabelRules {
		rules += r.AccountID + ":" + r.LabelID + ","
	}
	if rules != G+":news,"+O+":"+news+"," {
		t.Fatalf("rules %s", rules)
	}
	if q := st.QuietLabels[O]; !slices.Equal(q, []string{news, "junk"}) {
		t.Fatalf("quiet %v", q)
	}
	if d := s.dismissedLabels(); !slices.Contains(d[G], "old category") || !slices.Contains(d[O], "old category") {
		t.Fatalf("dismissed %v", d)
	}
	var stored models.Settings
	if err := s.store.GetSetting(settingsKey, &stored); err != nil || len(stored.Labels) != len(st.Labels) {
		t.Fatalf("migration not saved: %v", err)
	}

	// Once is enough: a second start changes nothing.
	if err := s.loadSettings(); err != nil {
		t.Fatal(err)
	}
	again := s.Settings()
	if len(again.Labels) != len(st.Labels) || len(again.LabelRules) != len(st.LabelRules) {
		t.Fatalf("second run changed the labels: %+v", again.Labels)
	}
	if _, ok := labelOf(t, s.store, o, news); !ok {
		t.Fatal("second run moved assignments")
	}
}

func TestProviderLabelsBelongToTheirMailboxes(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	g := s.resolveProviderLabels(demo.GmailAccountID, []mail.Label{{Name: "Receipts", Color: "#16a766"}})["receipts"]
	if l := labelByID(t, s, g); l.Source != config.LabelSourceProvider || !sameSet(l.AccountIDs, []string{demo.GmailAccountID}) {
		t.Fatalf("new provider label %+v", l)
	}
	// The same name in another mailbox is another label.
	o := s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "receipts"}})["receipts"]
	if o == g || !sameSet(labelByID(t, s, o).AccountIDs, []string{demo.OutlookAccountID}) {
		t.Fatalf("outlook receipts %s (gmail %s)", o, g)
	}
	// A provider label the user deleted is only gone from its mailbox.
	st := s.GetSettings()
	st.Labels = slices.DeleteFunc(st.Labels, func(l models.Label) bool { return l.ID == g })
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if ids := s.resolveProviderLabels(demo.GmailAccountID, []mail.Label{{Name: "Receipts"}}); ids["receipts"] != "" {
		t.Fatal("deleted provider label imported again")
	}
	if ids := s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "Receipts"}}); ids["receipts"] != o {
		t.Fatal("deleting a label in one mailbox dismissed it in another")
	}

	// Removing a mailbox: its labels, rules and quiet labels go.
	setLabels(t, s, models.Label{Name: "Outlook Mine", AccountIDs: []string{demo.OutlookAccountID}})
	st = s.GetSettings()
	st.LabelRules = []models.LabelRule{{ID: "r1", AccountID: demo.OutlookAccountID, Contains: "x", LabelID: "outlook-mine"}}
	st.QuietLabels = map[string][]string{demo.OutlookAccountID: {"outlook-mine"}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAccount(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	st = s.Settings()
	for _, l := range st.Labels {
		if slices.Contains(l.AccountIDs, demo.OutlookAccountID) {
			t.Errorf("label %s of the removed mailbox kept", l.ID)
		}
	}
	if len(st.LabelRules) != 0 || st.QuietLabels[demo.OutlookAccountID] != nil {
		t.Fatalf("rules %v quiet %v", st.LabelRules, st.QuietLabels)
	}

	// Adding a mailbox: it gets its own copies of the default labels.
	if err := s.store.InsertAccount(models.Account{ID: "a-new", Provider: models.ProviderGmail, Email: "sam@example.com", DisplayName: "Sam", Color: "#000000", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.labelsGainAccount("a-new")
	own := config.LabelsForAccount(s.Settings().Labels, "a-new")
	if len(own) != len(config.DefaultLabels()) {
		t.Fatalf("new mailbox labels %+v", own)
	}
	for _, l := range own {
		if l.Source == config.LabelSourceUser && (len(l.AccountIDs) != 1 || l.ID == mailboxLabel(t, s, demo.GmailAccountID, l.Name)) {
			t.Fatalf("new mailbox shares %+v", l)
		}
	}
	s.labelsGainAccount("a-new") // twice: no second set
	if n := len(config.LabelsForAccount(s.Settings().Labels, "a-new")); n != len(own) {
		t.Fatalf("labels added twice: %d", n)
	}
}

// Adding a label to mail of several mailboxes gives each message its own
// mailbox's label of that name; mail of a mailbox without one is skipped.
func TestAddLabelAcrossMailboxes(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	setLabels(t, s, models.Label{Name: "Gmail Things", AccountIDs: []string{demo.GmailAccountID}})
	g, o := firstMessage(t, s, demo.GmailAccountID), firstMessage(t, s, demo.OutlookAccountID)
	if _, err := s.AddLabel([]string{o}, "gmail-things"); err == nil {
		t.Fatal("label added to mail of another mailbox")
	}
	if _, err := s.AddLabel([]string{g, o}, "gmail-things"); err != nil {
		t.Fatal(err)
	}
	if _, ok := labelOf(t, s.store, g, "gmail-things"); !ok {
		t.Fatal("gmail message not labeled")
	}
	if _, ok := labelOf(t, s.store, o, "gmail-things"); ok {
		t.Fatal("outlook message labeled")
	}
	gp, op := mailboxLabel(t, s, demo.GmailAccountID, "Personal"), mailboxLabel(t, s, demo.OutlookAccountID, "Personal")
	if _, err := s.AddLabel([]string{g, o}, gp); err != nil {
		t.Fatal(err)
	}
	if _, ok := labelOf(t, s.store, o, op); !ok {
		t.Fatal("outlook message didn't get its own Personal label")
	}
	if _, ok := labelOf(t, s.store, o, gp); ok {
		t.Fatal("outlook message got gmail's label")
	}
	// "label:" search finds the label of every mailbox by name.
	res, err := s.SearchMessages(models.SearchQuery{Query: "label:personal"})
	if err != nil || len(res) != 2 {
		t.Fatalf("label search %d %v", len(res), err)
	}
	if err := s.RemoveLabel([]string{g, o}, op); err != nil {
		t.Fatal(err)
	}
	if _, ok := labelOf(t, s.store, g, gp); ok {
		t.Fatal("removing by name skipped the gmail message")
	}
}

// A backup's labels go to its mailboxes as they exist here, merged by name
// with the labels those mailboxes have.
func TestBackupMapsLabelMailboxes(t *testing.T) {
	src := newEnv(t, "")
	setLabels(t, src.s, models.Label{Name: "Outlook Stuff", AccountIDs: []string{demo.OutlookAccountID}},
		models.Label{Name: "Gmail Stuff", AccountIDs: []string{demo.GmailAccountID}, Then: config.LabelThenTrash})
	st := src.s.GetSettings()
	for i, l := range st.Labels {
		if l.Name == "Work" && slices.Contains(l.AccountIDs, demo.GmailAccountID) {
			st.Labels[i].Color = "#abcdef"
		}
	}
	if err := src.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
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
	if l := labelByID(t, b, "gmail-stuff"); !sameSet(l.AccountIDs, []string{"a-local"}) || l.Then != config.LabelThenTrash {
		t.Fatalf("gmail label %+v", l)
	}
	if l := labelByID(t, b, "outlook-stuff"); !sameSet(l.AccountIDs, []string{demo.OutlookAccountID}) {
		t.Fatalf("outlook label %v", l.AccountIDs)
	}
	works := 0
	for _, l := range b.Settings().Labels {
		if l.Name == "Work" {
			works++
			if len(l.AccountIDs) != 1 {
				t.Fatalf("shared label %+v", l)
			}
		}
	}
	if works != 2 {
		t.Fatalf("%d Work labels, want one per mailbox", works)
	}
	if l := labelByID(t, b, mailboxLabel(t, b, "a-local", "Work")); l.Color != "#abcdef" {
		t.Fatalf("merged by name: the backup's color should win: %+v", l)
	}
}

// A backup from before labels were per mailbox: its shared labels become
// one per mailbox, and its label marks and rules follow each copy.
func TestImportLegacyBackupSplitsLabels(t *testing.T) {
	b := newBlankService(t)
	data := `{"format":"gomail-backup","version":1,"settings":{
		"labels":[{"id":"news","name":"News","color":"#111111","description":"Newsletters.","source":"user","accountIds":[]}],
		"labelRules":[{"id":"r1","accountId":"","field":"from","contains":"digest","action":"label","labelId":"news"}]},
		"extra":{"dismissedProviderLabels":["old category"]},
		"accounts":[
			{"id":"g1","provider":"gmail","email":"alex.demo@gmail.com","labelMarks":[{"remoteId":"r-1","labelId":"news","kind":"manual"}]},
			{"id":"o1","provider":"outlook","email":"alex.demo@outlook.com","labelMarks":[{"remoteId":"r-2","labelId":"news","kind":"manual"}]}]}`
	if _, err := b.ImportBackupFrom(strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	g, o := mailboxLabel(t, b, "g1", "News"), mailboxLabel(t, b, "o1", "News")
	if g == o {
		t.Fatalf("News shared: %s", g)
	}
	if l := labelByID(t, b, o); l.Color != "#111111" || l.Description != "Newsletters." {
		t.Fatalf("copy %+v", l)
	}
	for email, want := range map[string]string{"alex.demo@gmail.com": g, "alex.demo@outlook.com": o} {
		var got string
		if err := b.store.DB().QueryRow(`SELECT label_id FROM pending_labels WHERE email = ? AND kind = 'manual'`, email).Scan(&got); err != nil || got != want {
			t.Fatalf("%s mark %q (%v), want %q", email, got, err, want)
		}
	}
	rules := ""
	for _, r := range b.Settings().LabelRules {
		rules += r.AccountID + ":" + r.LabelID + ","
	}
	if rules != "g1:"+g+",o1:"+o+"," {
		t.Fatalf("rules %s", rules)
	}
	if d := b.dismissedLabels(); !slices.Contains(d["g1"], "old category") || !slices.Contains(d["o1"], "old category") {
		t.Fatalf("dismissed %v", d)
	}
}
