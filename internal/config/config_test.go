package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/AVMG20/gomail/internal/models"
)

func TestNormalize(t *testing.T) {
	s := models.Settings{Theme: "neon", SyncIntervalMinutes: -1, PrimaryColor: "blue", Radius: 9, ClassifyThreshold: 3,
		Labels: []models.Label{{Name: " Work "}, {Name: "work"}, {Name: "Réunions & Co"}, {ID: "none", Name: "None"}, {Name: ""}}}
	Normalize(&s)
	if s.Theme != "system" || s.SyncIntervalMinutes != 5 || s.PrimaryColor != DefaultPrimaryColor || s.Radius != 0.5 || s.ClassifyThreshold != 0.5 {
		t.Fatalf("%+v", s)
	}
	if s.MicrosoftTenant != "common" || s.ClassifierModel != DefaultClassifierModel || s.OpenRouterModel == "" {
		t.Fatalf("%+v", s)
	}
	var ids []string
	for _, l := range s.Labels {
		ids = append(ids, l.ID+":"+l.Source)
		if !IsHexColor(l.Color) {
			t.Errorf("color %q", l.Color)
		}
	}
	want := "[work:user réunions-co:user none:user junk:system]"
	if got := fmtSlice(ids); got != want {
		t.Fatalf("labels %s, want %s", got, want)
	}
}

func fmtSlice(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}

func TestDataDirOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	t.Setenv(EnvDataDir, dir)
	t.Setenv(EnvDemo, "")
	got, err := DataDir()
	if err != nil || got != dir {
		t.Fatal(got, err)
	}
	t.Setenv(EnvDemo, "1")
	got, _ = DataDir()
	if got != filepath.Join(dir, "demo") {
		t.Fatal(got)
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gomail.log")
	r, err := openRotating(path)
	if err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 1<<20)
	for i := range line {
		line[i] = 'x'
	}
	for i := 0; i < 26; i++ { // 26 MB -> several rotations
		r.Write(line)
	}
	r.Close()
	for i := 1; i <= keepLogs; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Fatalf("missing %s.%d", path, i)
		}
	}
	if _, err := os.Stat(fmt.Sprintf("%s.%d", path, keepLogs+1)); err == nil {
		t.Fatal("too many old logs kept")
	}
	if fi, _ := os.Stat(path); fi.Size() > maxLogSize {
		t.Fatalf("log too big: %d", fi.Size())
	}
}

func TestEmailBackgroundSetting(t *testing.T) {
	if DefaultSettings().EmailBackground != "auto" {
		t.Fatal("default")
	}
	for in, want := range map[string]string{"": "auto", "auto": "auto", "light": "light", "dark": "dark", "bogus": "auto"} {
		s := DefaultSettings()
		s.EmailBackground = in
		Normalize(&s)
		if s.EmailBackground != want {
			t.Errorf("%q -> %q, want %q", in, s.EmailBackground, want)
		}
	}
	// The older "Dark designed mail" switch on top of Auto becomes "dark"; Always light stays light.
	for in, want := range map[string]string{"": "dark", "auto": "dark", "light": "light", "dark": "dark"} {
		s := DefaultSettings()
		s.EmailBackground, s.DarkenDesignedMail = in, true
		Normalize(&s)
		if s.EmailBackground != want || s.DarkenDesignedMail {
			t.Errorf("darkened %q -> %q (flag %v), want %q", in, s.EmailBackground, s.DarkenDesignedMail, want)
		}
	}
}

func TestCalendarSettings(t *testing.T) {
	d := DefaultSettings()
	if d.CalendarWeekStart != "auto" || d.CalendarDefaultView != "week" || d.CalendarDefaultDuration != 30 || d.CalendarShowDeclined ||
		d.CalendarDefaultReminder != 10 || !d.CalendarNotifications {
		t.Fatalf("%+v", d)
	}
	s := DefaultSettings()
	s.CalendarWeekStart, s.CalendarDefaultView, s.CalendarDefaultDuration, s.CalendarDefaultReminder = "friday", "year", 1, -5
	Normalize(&s)
	if s.CalendarWeekStart != "auto" || s.CalendarDefaultView != "week" || s.CalendarDefaultDuration != 30 || s.CalendarDefaultReminder != 10 {
		t.Fatalf("%+v", s)
	}
	s.CalendarWeekStart, s.CalendarDefaultView, s.CalendarDefaultDuration, s.CalendarDefaultReminder = "monday", "agenda", 45, 0
	Normalize(&s)
	if s.CalendarWeekStart != "monday" || s.CalendarDefaultView != "agenda" || s.CalendarDefaultDuration != 45 || s.CalendarDefaultReminder != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestCalendarTimeFormat(t *testing.T) {
	s := DefaultSettings()
	if s.CalendarTimeFormat != "auto" {
		t.Fatalf("default %q", s.CalendarTimeFormat)
	}
	for in, want := range map[string]string{"12h": "12h", "24h": "24h", "": "auto", "13h": "auto"} {
		s.CalendarTimeFormat = in
		Normalize(&s)
		if s.CalendarTimeFormat != want {
			t.Errorf("%q -> %q, want %q", in, s.CalendarTimeFormat, want)
		}
	}
}

// Rule ids stay stable and unique; generated ids never take an id that a
// later rule already uses.
func TestNormalizeRules(t *testing.T) {
	got := normalizeRules([]models.LabelRule{
		{ID: "", Contains: " gitlab ", LabelID: "gitlab"},
		{ID: "rule_1", Field: "subject", Contains: "invoice", Action: "junk", LabelID: "x"},
		{ID: "rule_1", Contains: "dup", LabelID: "gitlab"},
		{ID: "keep", Contains: ""}, // not filled in yet: kept, never matches
	}, map[string]bool{"gitlab": true})
	if len(got) != 4 {
		t.Fatalf("rules %+v", got)
	}
	seen := map[string]bool{}
	for _, r := range got {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("duplicate or empty id in %+v", got)
		}
		seen[r.ID] = true
	}
	if got[1].ID != "rule_1" || got[3].ID != "keep" {
		t.Fatalf("ids not stable: %+v", got)
	}
	if got[0].Contains != "gitlab" || got[0].Field != "from" || got[0].Action != "label" {
		t.Fatalf("defaults %+v", got[0])
	}
	if got[1].LabelID != "" || got[1].Field != "subject" {
		t.Fatalf("junk rule %+v", got[1])
	}
}

func TestNormalizeQuietLabels(t *testing.T) {
	got := normalizeQuietLabels(map[string][]string{"": {"junk"}, "a": {"junk", " junk", "", "gitlab"}, "b": nil}, map[string]bool{"junk": true, "gitlab": true})
	if len(got) != 2 || fmtSlice(got["a"]) != "[junk gitlab]" || got["b"] == nil || len(got["b"]) != 0 {
		t.Fatalf("%v", got)
	}
	s := models.Settings{QuietLabels: got}
	if fmtSlice(QuietLabels(s, "b")) != "[]" || fmtSlice(QuietLabels(s, "c")) != "[junk]" {
		t.Fatalf("lookup %v %v", QuietLabels(s, "b"), QuietLabels(s, "c"))
	}
}

// Rules and quiet lists that point at a deleted label let go of it: the
// rule stays (so the user can pick another label) but never matches, and a
// new label that later gets the same id isn't applied by surprise.
func TestNormalizeDropsDeletedLabels(t *testing.T) {
	s := DefaultSettings()
	s.Labels = append(s.Labels, models.Label{ID: "gitlab", Name: "GitLab"})
	s.LabelRules = []models.LabelRule{
		{ID: "a", Contains: "gitlab", LabelID: "gitlab"},
		{ID: "b", Contains: "ci", LabelID: "deleted"},
		{ID: "c", Contains: "casino", LabelID: JunkLabelID}, // "add Junk" = move to Junk
	}
	s.QuietLabels = map[string][]string{"acct": {"deleted", "gitlab", JunkLabelID}}
	s.MutedMailboxes = []string{" acct ", "", "acct", "other"}
	s.AutoLabelAccounts = []string{"acct", " acct", ""}
	Normalize(&s)
	if r := s.LabelRules; r[0].LabelID != "gitlab" || r[1].LabelID != "" || r[1].Action != "label" || r[2].Action != "junk" || r[2].LabelID != "" {
		t.Fatalf("rules %+v", r)
	}
	if got := fmtSlice(s.QuietLabels["acct"]); got != "[gitlab junk]" {
		t.Fatalf("quiet %s", got)
	}
	if got := fmtSlice(s.MutedMailboxes); got != "[acct other]" {
		t.Fatalf("muted %s", got)
	}
	if got := fmtSlice(s.AutoLabelAccounts); got != "[acct]" || !s.AutoClassify {
		t.Fatalf("autoLabelAccounts %s (autoClassify %v)", got, s.AutoClassify)
	}
	if !AutoClassifies(s, "acct") || AutoClassifies(s, "other") {
		t.Fatal("AutoClassifies ignores the per-mailbox list")
	}
	s.AICorrectsProviderLabels = true
	s.AICorrectsOff = []string{"other"}
	if !AICorrects(s, "acct") || AICorrects(s, "other") {
		t.Fatal("AICorrects ignores the per-mailbox list")
	}
	s.AICorrectsProviderLabels = false
	if AICorrects(s, "acct") {
		t.Fatal("AICorrectsProviderLabels off still corrects")
	}
}

func TestLabelScope(t *testing.T) {
	labels := NormalizeLabels([]models.Label{
		{Name: "Old"},
		{Name: "Scoped", AccountIDs: []string{" a1 ", "a1", ""}},
		{ID: JunkLabelID, Name: "Junk", AccountIDs: []string{"a1"}},
	})
	if got := labels[1].AccountIDs; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("scoped label ids not cleaned: %q", got)
	}
	if len(labels[2].AccountIDs) != 0 {
		t.Fatalf("Junk should belong to every mailbox: %q", labels[2].AccountIDs)
	}
	names := func(ls []models.Label) string {
		out := ""
		for _, l := range ls {
			out += l.Name + ","
		}
		return out
	}
	if got := names(LabelsForAccount(labels, "a2")); got != "Old,Junk," {
		t.Fatalf("a2 labels %s", got)
	}
	if got := names(LabelsForAccount(labels, "")); got != "Old,Scoped,Junk," {
		t.Fatalf("unified labels %s", got)
	}
	if l, ok := LabelNamed(labels, "a1", "scoped"); !ok || l.ID != "scoped" {
		t.Fatalf("LabelNamed %+v %v", l, ok)
	}
	if _, ok := LabelNamed(labels, "a2", "Scoped"); ok {
		t.Fatal("LabelNamed found another mailbox's label")
	}
	if l, ok := LabelNamed(labels, "a2", "JUNK"); !ok || l.ID != JunkLabelID {
		t.Fatal("Junk belongs to every mailbox")
	}
}

// Names are unique within a mailbox only; nothing else takes Junk's name
// or id; unknown actions are dropped and Junk has none.
func TestNormalizeLabelsPerMailbox(t *testing.T) {
	labels := NormalizeLabels([]models.Label{
		{ID: "work", Name: "Work", AccountIDs: []string{"a1"}, Then: "archive"},
		{ID: "work", Name: "work", AccountIDs: []string{"a2"}, Then: "explode"},
		{Name: "WORK", AccountIDs: []string{"a1"}},
		{Name: "junk", AccountIDs: []string{"a1"}},
		{ID: JunkLabelID, Name: "Junk", Then: "trash"},
	})
	got := ""
	for _, l := range labels {
		got += l.ID + "/" + l.Then + ","
	}
	if got != "work/archive,work-2/,junk/," {
		t.Fatalf("labels %s", got)
	}
	if id := NewLabelID(labels, "Work"); id != "work-3" {
		t.Fatalf("NewLabelID %s", id)
	}
	defs := MailboxDefaultLabels(labels, "a3")
	if len(defs) == 0 || defs[0].ID != "important" || len(defs[0].AccountIDs) != 1 || defs[0].AccountIDs[0] != "a3" {
		t.Fatalf("defaults %+v", defs)
	}
	for _, l := range defs {
		if l.Source != LabelSourceUser || l.ID == "work" {
			t.Fatalf("default copy %+v", l)
		}
	}
}

// Shared labels become one copy per mailbox: the first mailbox keeps the
// id; rules for every mailbox become one per mailbox; rules and quiet
// lists follow the copies. A second run changes nothing.
func TestSplitLabels(t *testing.T) {
	s := models.Settings{
		Labels: NormalizeLabels([]models.Label{
			{ID: "news", Name: "News", Color: "#111111", Description: "Newsletters.", Then: "archive", AccountIDs: []string{"a1", "a2"}},
			{ID: "old", Name: "Old"},
			{ID: "mine", Name: "Mine", AccountIDs: []string{"a2"}},
			{ID: "rood", Name: "Categorie Rood", Source: LabelSourceProvider, AccountIDs: []string{"a2", "a1"}},
		}),
		LabelRules: []models.LabelRule{
			{ID: "rule_1", Contains: "digest", LabelID: "news"},
			{ID: "rule_2", AccountID: "a2", Contains: "x", LabelID: "news"},
			{ID: "rule_3", Contains: "me", LabelID: "mine"},
			{ID: "rule_4", Contains: "casino", Action: "junk"},
		},
		QuietLabels: map[string][]string{"a1": {"news"}, "a2": {"news", "rood", JunkLabelID}},
	}
	moves, changed := SplitLabels(&s, []string{"a1", "a2"})
	if !changed {
		t.Fatal("nothing split")
	}
	got := ""
	for _, l := range s.Labels {
		got += fmt.Sprintf("%s:%s:%v:%s|", l.ID, l.Name, l.AccountIDs, l.Then)
	}
	want := "news:News:[a1]:archive|news-2:News:[a2]:archive|old:Old:[a1]:|old-2:Old:[a2]:|mine:Mine:[a2]:|rood:Categorie Rood:[a2]:|rood-2:Categorie Rood:[a1]:|junk:Junk:[]:|"
	if got != want {
		t.Fatalf("labels\n got %s\nwant %s", got, want)
	}
	if s.Labels[1].Color != "#111111" || s.Labels[1].Description != "Newsletters." || s.Labels[6].Source != LabelSourceProvider {
		t.Fatalf("copy lost its details: %+v %+v", s.Labels[1], s.Labels[6])
	}
	if fmt.Sprint(moves) != "[{a2 news news-2} {a2 old old-2} {a1 rood rood-2}]" {
		t.Fatalf("moves %v", moves)
	}
	rules := ""
	for _, r := range s.LabelRules {
		rules += fmt.Sprintf("%s:%s:%s:%s|", r.ID, r.AccountID, r.Contains, r.LabelID)
	}
	if rules != "rule_1:a1:digest:news|rule_5:a2:digest:news-2|rule_2:a2:x:news-2|rule_3:a2:me:mine|rule_4:a1:casino:|rule_6:a2:casino:|" {
		t.Fatalf("rules %s", rules)
	}
	if fmtSlice(s.QuietLabels["a1"]) != "[news]" || fmtSlice(s.QuietLabels["a2"]) != "[news-2 rood junk]" {
		t.Fatalf("quiet %v", s.QuietLabels)
	}
	Normalize(&s)
	if moves, changed := SplitLabels(&s, []string{"a1", "a2"}); changed || moves != nil {
		t.Fatalf("second run changed something: %v", moves)
	}

	// Without accounts, labels without a mailbox wait for the first one.
	empty := models.Settings{Labels: []models.Label{{ID: "old", Name: "Old"}}, LabelRules: []models.LabelRule{{ID: "r", Contains: "x"}}}
	if _, changed := SplitLabels(&empty, nil); changed || len(empty.Labels[0].AccountIDs) != 0 || empty.LabelRules[0].AccountID != "" {
		t.Fatalf("split without accounts: %+v", empty)
	}
}

// Rules and quiet lists only keep labels of their own mailbox.
func TestNormalizeKeepsMailboxLabels(t *testing.T) {
	s := DefaultSettings()
	s.Labels = append(s.Labels, models.Label{ID: "a1-only", Name: "A1", AccountIDs: []string{"a1"}})
	s.LabelRules = []models.LabelRule{{ID: "r1", AccountID: "a2", Contains: "x", LabelID: "a1-only"}, {ID: "r2", AccountID: "a1", Contains: "x", LabelID: "a1-only"}}
	s.QuietLabels = map[string][]string{"a2": {"a1-only", JunkLabelID}, "a1": {"a1-only"}}
	Normalize(&s)
	if s.LabelRules[0].LabelID != "" || s.LabelRules[1].LabelID != "a1-only" {
		t.Fatalf("rules %+v", s.LabelRules)
	}
	if fmtSlice(s.QuietLabels["a2"]) != "[junk]" || fmtSlice(s.QuietLabels["a1"]) != "[a1-only]" {
		t.Fatalf("quiet %v", s.QuietLabels)
	}
}

// The old app-wide auto-label switch becomes the per-mailbox list once:
// on means every mailbox, off none.
func TestMigrateAutoLabel(t *testing.T) {
	on := models.Settings{AutoClassify: true}
	if !MigrateAutoLabel(&on, []string{"a", "b"}) || fmtSlice(on.AutoLabelAccounts) != "[a b]" {
		t.Fatalf("on: %v", on.AutoLabelAccounts)
	}
	if MigrateAutoLabel(&on, []string{"a", "b", "c"}) || len(on.AutoLabelAccounts) != 2 {
		t.Fatal("migrated twice")
	}
	off := models.Settings{}
	if !MigrateAutoLabel(&off, []string{"a"}) || off.AutoLabelAccounts == nil || len(off.AutoLabelAccounts) != 0 {
		t.Fatalf("off: %v", off.AutoLabelAccounts)
	}
	Normalize(&off)
	if MigrateAutoLabel(&off, []string{"a"}) {
		t.Fatal("normalized settings migrated again")
	}
}

func TestSyncIntervalPerMailbox(t *testing.T) {
	s := models.Settings{SyncIntervalMinutes: 1, SyncIntervals: map[string]int{"a": 15, "b": 0, " ": 5, "c": 99999}}
	Normalize(&s)
	if len(s.SyncIntervals) != 1 || SyncInterval(s, "a") != 15 {
		t.Fatalf("intervals %+v", s.SyncIntervals)
	}
	// Mailboxes without their own choice check every 5 minutes (not the old global setting).
	if SyncInterval(s, "b") != DefaultSyncInterval || DefaultSyncInterval != 5 {
		t.Fatalf("default %d", SyncInterval(s, "b"))
	}
}

func TestAIReasoningSettings(t *testing.T) {
	// Settings saved before these existed (empty) get the defaults.
	var s models.Settings
	Normalize(&s)
	if s.TextAIReasoning != "default" || s.EventAIReasoning != "low" || s.EventAIModel != "" {
		t.Fatalf("defaults %q %q %q", s.TextAIReasoning, s.EventAIReasoning, s.EventAIModel)
	}
	s.TextAIReasoning, s.EventAIReasoning, s.EventAIModel = "high", "bogus", "  x/y  "
	Normalize(&s)
	if s.TextAIReasoning != "high" || s.EventAIReasoning != "low" || s.EventAIModel != "x/y" {
		t.Fatalf("normalized %q %q %q", s.TextAIReasoning, s.EventAIReasoning, s.EventAIModel)
	}
}
