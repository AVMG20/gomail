package core

import (
	"bytes"
	"compress/gzip"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
)

// newBlankService is a fresh install without accounts whose providers are
// the demo ones (same remote ids as newEnv's accounts).
func newBlankService(t *testing.T) *Service {
	t.Helper()
	s, err := New(Options{
		DataDir: t.TempDir(), Version: "test", UI: &fakeUI{}, Secrets: secrets.NewMemory(),
		ProviderFactory: func(acct models.Account) (provider.Provider, error) {
			d := demo.New(acct)
			d.Latency, d.NewMailChance = 0, 0
			return d, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func labelOf(t *testing.T, st *store.Store, id, label string) (string, bool) {
	t.Helper()
	sums, err := st.Summaries([]string{id})
	if err != nil || len(sums) != 1 {
		t.Fatalf("summary %s: %v %v", id, sums, err)
	}
	for _, d := range sums[0].LabelDetails {
		if d.LabelID == label {
			return d.Source, true
		}
	}
	return "", false
}

type seeded struct {
	manualID, removedID, removedLabel, aiID string
	manualRemote, removedRemote, aiRemote   string
}

// seedBackupData customizes a demo install: a new label, settings, keys,
// manual / removed / AI labels and calendar choices.
func seedBackupData(t *testing.T, s *Service) seeded {
	t.Helper()
	st := s.GetSettings()
	st.Theme = "dark"
	st.PrimaryColor = "#10b981"
	st.GoogleClientID = "client-id.apps.example.com"
	st.GoogleClientSecret = "client-secret-value"
	st.Labels = append(st.Labels, models.Label{Name: "Side Project", Color: "#123456", Description: "Mail about the side project."})
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOpenRouterKey("sk-or-test-key-123"); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveToken(s.secrets, demo.GmailAccountID, &oauth2.Token{AccessToken: "access-tok", RefreshToken: "refresh-tok-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountSignature(demo.GmailAccountID, "<p>Best, Alex Morgan</p>"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox, Limit: 100})
	if err != nil || len(page.Messages) < 3 {
		t.Fatalf("messages %d %v", len(page.Messages), err)
	}
	var out seeded
	for _, m := range page.Messages {
		for _, d := range m.LabelDetails {
			if out.removedID == "" && d.Source == "provider" {
				out.removedID, out.removedRemote, out.removedLabel = m.ID, m.RemoteID, d.LabelID
			}
		}
	}
	if out.removedID == "" {
		t.Fatal("no message with a provider label")
	}
	for _, m := range page.Messages {
		if m.ID == out.removedID {
			continue
		}
		if out.manualID == "" {
			out.manualID, out.manualRemote = m.ID, m.RemoteID
		} else if out.aiID == "" {
			out.aiID, out.aiRemote = m.ID, m.RemoteID
		}
	}
	if _, err := s.AddLabel([]string{out.manualID}, "side-project"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabel([]string{out.removedID}, out.removedLabel); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.ApplyAIScores(out.aiID, map[string]float64{"finance": 0.93}, store.AIPolicy{AddThreshold: 0.5}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceCalendars(demo.GmailAccountID, []calendar.Calendar{{RemoteID: "cal-team", Name: "Team"}, {RemoteID: "cal-home", Name: "Home"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetCalendarVisible(store.CalendarID(demo.GmailAccountID, "cal-team"), false); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetCalendarColor(store.CalendarID(demo.GmailAccountID, "cal-home"), "#ff0000"); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBackupRoundTrip(t *testing.T) {
	e := newEnv(t, "")
	seed := seedBackupData(t, e.s)

	var buf bytes.Buffer
	res, err := e.s.WriteBackup(&buf, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Accounts != 2 || res.LabelAssignments < 3 || res.IncludesSecrets {
		t.Fatalf("export result %+v", res)
	}
	text := gunzip(t, buf.Bytes())
	for _, secret := range []string{"sk-or-test-key-123", "client-secret-value", "client-id.apps.example.com", "refresh-tok-secret", "access-tok"} {
		if strings.Contains(text, secret) {
			t.Fatalf("backup without secrets contains %q", secret)
		}
	}
	if !strings.Contains(text, `"format":"gomail-backup"`) || !strings.Contains(text, seed.manualRemote) {
		t.Fatalf("unexpected backup content: %.300s", text)
	}

	// A fresh install.
	b := newBlankService(t)
	sum, err := b.ImportBackupFrom(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if sum.AccountsAdded != 2 || sum.AccountsMerged != 0 || sum.LabelAssignments != res.LabelAssignments || sum.AppliedNow != 0 ||
		sum.Calendars != 2 || sum.SecretsRestored || sum.Labels == 0 {
		t.Fatalf("summary %+v (export %+v)", sum, res)
	}
	st := b.GetSettings()
	if st.Theme != "dark" || st.PrimaryColor != "#10b981" || st.GoogleClientID != "" || st.GoogleClientSecret != "" || st.OpenRouterKeySet {
		t.Fatalf("settings %+v", st)
	}
	hasLabel := false
	for _, l := range st.Labels {
		hasLabel = hasLabel || (l.ID == "side-project" && l.Color == "#123456" && l.Description != "")
	}
	if !hasLabel {
		t.Fatalf("label not restored: %+v", st.Labels)
	}
	accts, _ := b.Accounts()
	if len(accts) != 2 {
		t.Fatalf("accounts %+v", accts)
	}
	for _, a := range accts {
		if !a.NeedsReauth || a.SyncError != restoredSyncError {
			t.Errorf("restored account should wait for a sign-in: %+v", a)
		}
		if a.ID == demo.GmailAccountID && (a.Email != "alex.demo@gmail.com" || !strings.Contains(a.Signature, "Alex Morgan") || a.Color != "#ea4335") {
			t.Errorf("gmail account %+v", a)
		}
	}
	if _, err := b.store.Account(demo.GmailAccountID); err != nil {
		t.Fatal("the account id should be kept")
	}
	if n, _ := b.store.PendingLabels(models.ProviderGmail, "alex.demo@gmail.com"); n == 0 {
		t.Fatal("label marks should wait for the messages")
	}

	// Signing in (ReauthAccount stores a token and clears the flag), then
	// the first sync applies the restored labels on ingest.
	if err := b.store.SetNeedsReauth(demo.GmailAccountID, false); err != nil {
		t.Fatal(err)
	}
	if err := b.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if src, ok := labelOf(t, b.store, seed.manualID, "side-project"); !ok || src != "manual" {
		t.Fatalf("manual label after sync: %q %v", src, ok)
	}
	if _, ok := labelOf(t, b.store, seed.removedID, seed.removedLabel); ok {
		t.Fatal("a provider label removed by hand came back")
	}
	if src, ok := labelOf(t, b.store, seed.aiID, "finance"); !ok || (src != "ai" && src != "provider") {
		t.Fatalf("AI label after sync: %q %v", src, ok)
	}
	// The removal holds on later syncs, too.
	if err := b.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if _, ok := labelOf(t, b.store, seed.removedID, seed.removedLabel); ok {
		t.Fatal("the provider label came back on the next sync")
	}
	// Calendar choices apply when the calendars sync.
	if err := b.store.ReplaceCalendars(demo.GmailAccountID, []calendar.Calendar{{RemoteID: "cal-team", Name: "Team", Color: "#00ff00"}, {RemoteID: "cal-home", Name: "Home", Color: "#00ff00"}}); err != nil {
		t.Fatal(err)
	}
	team, _ := b.store.Calendar(store.CalendarID(demo.GmailAccountID, "cal-team"))
	home, _ := b.store.Calendar(store.CalendarID(demo.GmailAccountID, "cal-home"))
	if team.Visible || !home.Visible || home.Color != "#ff0000" || team.Color != "#00ff00" {
		t.Fatalf("calendars team=%+v home=%+v", team.Calendar, home.Calendar)
	}

	// A backup of the restored install keeps the marks still pending.
	var again bytes.Buffer
	res2, err := b.WriteBackup(&again, false)
	if err != nil || res2.LabelAssignments < res.LabelAssignments-1 {
		t.Fatalf("re-export %+v %v (first %+v)", res2, err, res)
	}
}

func TestBackupSecretsOptIn(t *testing.T) {
	e := newEnv(t, "")
	seedBackupData(t, e.s)
	path := filepath.Join(t.TempDir(), BackupFileName(time.Now()))
	res, err := e.s.ExportBackupFile(path, true)
	if err != nil || !res.IncludesSecrets || res.Path != path {
		t.Fatalf("export %+v %v", res, err)
	}
	b := newBlankService(t)
	sum, err := b.ImportBackupFile(path)
	if err != nil || !sum.SecretsRestored {
		t.Fatalf("import %+v %v", sum, err)
	}
	st := b.GetSettings()
	if st.GoogleClientID != "client-id.apps.example.com" || st.GoogleClientSecret != "client-secret-value" || !st.OpenRouterKeySet {
		t.Fatalf("settings %+v", st)
	}
	if k, _ := b.openRouterKey(); k != "sk-or-test-key-123" {
		t.Fatalf("key %q", k)
	}
	// OAuth tokens are never exported, even with secrets.
	if _, err := auth.LoadToken(b.secrets, demo.GmailAccountID); err == nil {
		t.Fatal("token restored")
	}
	if !strings.HasPrefix(filepath.Base(path), "gomail-backup-") || filepath.Ext(path) != BackupExtension {
		t.Fatalf("name %s", path)
	}
}

func TestBackupMergeIntoExistingInstall(t *testing.T) {
	src := newEnv(t, "")
	seed := seedBackupData(t, src.s)
	var buf bytes.Buffer
	if _, err := src.s.WriteBackup(&buf, false); err != nil {
		t.Fatal(err)
	}

	dst := newEnv(t, "") // the same mailboxes, already synced, with a label of its own
	st := dst.s.GetSettings()
	st.Theme = "light"
	st.Labels = append(st.Labels, models.Label{Name: "Local Only", Color: "#654321"})
	if err := dst.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.s.AddLabel([]string{seed.aiID}, "local-only"); err != nil {
		t.Fatal(err)
	}
	if err := dst.s.UpdateAccount(demo.GmailAccountID, "My Gmail", "#000000"); err != nil {
		t.Fatal(err)
	}
	sum, err := dst.s.ImportBackupFrom(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if sum.AccountsMerged != 2 || sum.AccountsAdded != 0 || sum.AppliedNow == 0 {
		t.Fatalf("summary %+v", sum)
	}
	// Applied right away to messages already present.
	if src, ok := labelOf(t, dst.s.store, seed.manualID, "side-project"); !ok || src != "manual" {
		t.Fatalf("manual label: %q %v", src, ok)
	}
	if _, ok := labelOf(t, dst.s.store, seed.removedID, seed.removedLabel); ok {
		t.Fatal("removed label still there")
	}
	// Local data is kept: the label, its assignment and the account.
	if _, ok := labelOf(t, dst.s.store, seed.aiID, "local-only"); !ok {
		t.Fatal("local label assignment lost")
	}
	got := dst.s.GetSettings()
	ids := map[string]bool{}
	for _, l := range got.Labels {
		ids[l.ID] = true
	}
	if !ids["local-only"] || !ids["side-project"] || got.Theme != "dark" {
		t.Fatalf("merged settings theme=%s labels=%v", got.Theme, ids)
	}
	a, _ := dst.s.store.Account(demo.GmailAccountID)
	if a.DisplayName != "My Gmail" || a.NeedsReauth {
		t.Fatalf("existing account changed: %+v", a)
	}
}

func TestBackupRejectsIncompatibleFiles(t *testing.T) {
	s := newBlankService(t)
	for name, data := range map[string]string{
		"newer":     `{"format":"gomail-backup","version":99,"settings":{}}`,
		"zero":      `{"format":"gomail-backup","version":0}`,
		"foreign":   `{"format":"something-else","version":1}`,
		"garbage":   "not json at all",
		"truncated": "\x1f\x8b\x08\x00",
	} {
		if _, err := s.ImportBackupFrom(strings.NewReader(data)); err == nil {
			t.Errorf("%s: imported", name)
		} else if name == "newer" && !strings.Contains(err.Error(), "newer version of gomail") {
			t.Errorf("newer: %v", err)
		}
	}
	// A plain (not gzipped) current backup is fine; unknown settings are ignored.
	sum, err := s.ImportBackupFrom(strings.NewReader(`{"format":"gomail-backup","version":1,"settings":{"theme":"dark","someFutureSetting":true,"density":42},"accounts":[{"provider":"imap","email":"x@example.com"}]}`))
	if err != nil || sum.AccountsSkipped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
	if st := s.GetSettings(); st.Theme != "dark" || st.Density != "comfortable" {
		t.Fatalf("settings %+v", st)
	}
}
