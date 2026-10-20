package core

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// Jev auto-labels only the new mail of the mailboxes chosen for it.
// Removing a mailbox drops its per-mailbox settings.
func TestAutoLabelPerMailbox(t *testing.T) {
	srv := fakeOpenRouter(t)
	defer srv.Close()
	e := newEnv(t, srv.URL)
	s := e.s
	if err := s.SetOpenRouterKey("sk-or-test"); err != nil {
		t.Fatal(err)
	}
	st := s.GetSettings()
	st.AutoLabelAccounts = []string{demo.GmailAccountID, " " + demo.GmailAccountID}
	st.AICorrectsOff = []string{demo.OutlookAccountID}
	st.MutedMailboxes = []string{demo.OutlookAccountID}
	st.QuietLabels = map[string][]string{demo.OutlookAccountID: {"junk"}, demo.GmailAccountID: {"junk"}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSettings(); !slices.Equal(got.AutoLabelAccounts, []string{demo.GmailAccountID}) || !got.AutoClassify {
		t.Fatalf("autoLabelAccounts %v (autoClassify %v)", got.AutoLabelAccounts, got.AutoClassify)
	}

	classified := func(accountID, subject string) bool {
		t.Helper()
		res, _ := s.SearchMessages(models.SearchQuery{Query: subject, AccountID: accountID})
		if len(res) == 0 {
			t.Fatalf("%s: %q not found", accountID, subject)
		}
		in, _ := s.store.ClassifyInputs([]string{res[0].Message.ID})
		return len(in) == 1 && !in[0].ClassifiedAt.IsZero()
	}

	e.demos[demo.OutlookAccountID].Deliver("Quietbox invoice", "billing@example.com")
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	e.demos[demo.GmailAccountID].Deliver("Labelbox invoice", "billing@example.com")
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !classified(demo.GmailAccountID, "Labelbox invoice") {
		if time.Now().After(deadline) {
			t.Fatal("new mail of an auto-labeled mailbox was not labeled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if classified(demo.OutlookAccountID, "Quietbox invoice") {
		t.Fatal("new mail of a mailbox without auto-labeling was labeled")
	}

	if err := s.RemoveAccount(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	st = s.GetSettings()
	if len(st.AICorrectsOff)+len(st.MutedMailboxes) != 0 || len(st.QuietLabels) != 1 || !slices.Equal(st.AutoLabelAccounts, []string{demo.GmailAccountID}) {
		t.Fatalf("removed mailbox left settings behind: %v %v %v %v", st.AutoLabelAccounts, st.AICorrectsOff, st.MutedMailboxes, st.QuietLabels)
	}
}

// Settings from before auto-labeling was per mailbox: the old switch on
// means every mailbox, off none.
func TestAutoLabelMigration(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	accts, err := s.Accounts()
	if err != nil || len(accts) < 2 {
		t.Fatalf("accounts %v %v", accts, err)
	}
	for _, on := range []bool{true, false} {
		raw, err := s.store.RawSetting(settingsKey)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "autoLabelAccounts")
		fields["autoClassify"], _ = json.Marshal(on)
		old, _ := json.Marshal(fields)
		if err := s.store.PutRawSetting(settingsKey, old); err != nil {
			t.Fatal(err)
		}
		if err := s.loadSettings(); err != nil {
			t.Fatal(err)
		}
		want := 0
		if on {
			want = len(accts)
		}
		if got := s.Settings().AutoLabelAccounts; len(got) != want {
			t.Fatalf("old switch %v: autoLabelAccounts %v", on, got)
		}
		// Migrated once: loading again keeps the list.
		var stored models.Settings
		if err := s.store.GetSetting(settingsKey, &stored); err != nil || stored.AutoLabelAccounts == nil {
			t.Fatalf("migration not saved: %v %v", stored.AutoLabelAccounts, err)
		}
	}
}

// A new mailbox is auto-labeled when every mailbox so far is.
func TestAutoLabelNewMailbox(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	st := s.GetSettings()
	st.AutoLabelAccounts = []string{demo.GmailAccountID, demo.OutlookAccountID}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.store.InsertAccount(models.Account{ID: "a-new", Provider: models.ProviderGmail, Email: "new@example.com", Color: "#000000", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.labelsGainAccount("a-new")
	if got := s.Settings().AutoLabelAccounts; !slices.Contains(got, "a-new") {
		t.Fatalf("new mailbox not auto-labeled: %v", got)
	}
	st = s.GetSettings()
	st.AutoLabelAccounts = []string{demo.GmailAccountID}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.store.InsertAccount(models.Account{ID: "a-two", Provider: models.ProviderGmail, Email: "two@example.com", Color: "#000000", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.labelsGainAccount("a-two")
	if got := s.Settings().AutoLabelAccounts; slices.Contains(got, "a-two") {
		t.Fatalf("new mailbox auto-labeled while others aren't: %v", got)
	}
}

// Restoring a backup: a backup from before auto-labeling was per mailbox
// with the old switch on auto-labels its mailboxes; a newer one adds its
// mailboxes to those this install already auto-labels.
func TestAutoLabelBackup(t *testing.T) {
	b := newBlankService(t)
	legacy := `{"format":"gomail-backup","version":1,"settings":{"autoClassify":true},"accounts":[{"id":"old-g","provider":"gmail","email":"x@example.com"}]}`
	if _, err := b.ImportBackupFrom(strings.NewReader(legacy)); err != nil {
		t.Fatal(err)
	}
	if got := b.Settings().AutoLabelAccounts; !slices.Equal(got, []string{"old-g"}) {
		t.Fatalf("legacy backup: %v", got)
	}

	src := newEnv(t, "")
	st := src.s.GetSettings()
	st.AutoLabelAccounts = []string{demo.GmailAccountID}
	if err := src.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := src.s.WriteBackup(&buf, false); err != nil {
		t.Fatal(err)
	}
	dst := newEnv(t, "")
	st = dst.s.GetSettings()
	st.AutoLabelAccounts = []string{demo.OutlookAccountID}
	if err := dst.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.s.ImportBackupFrom(&buf); err != nil {
		t.Fatal(err)
	}
	if got := dst.s.Settings().AutoLabelAccounts; len(got) != 2 || !slices.Contains(got, demo.GmailAccountID) || !slices.Contains(got, demo.OutlookAccountID) {
		t.Fatalf("merged backup: %v", got)
	}
}
