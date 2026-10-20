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
	for in, want := range map[string]string{"": "auto", "auto": "auto", "light": "light", "dark": "auto"} {
		s := DefaultSettings()
		s.EmailBackground = in
		Normalize(&s)
		if s.EmailBackground != want {
			t.Errorf("%q -> %q, want %q", in, s.EmailBackground, want)
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
	if ScopeLabels(labels, nil) {
		t.Fatal("without accounts nothing changes")
	}
	if !ScopeLabels(labels, []string{"a1", "a2"}) || fmt.Sprint(labels[0].AccountIDs) != "[a1 a2]" || fmt.Sprint(labels[1].AccountIDs) != "[a1]" || len(labels[2].AccountIDs) != 0 {
		t.Fatalf("scoped %+v", labels)
	}
	if ScopeLabels(labels, []string{"a1", "a2"}) {
		t.Fatal("scoping twice changed something")
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
}
