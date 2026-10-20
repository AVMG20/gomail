package core

import (
	"slices"
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

// Removing a mailbox also removes its rules, quiet list and notification
// mute, and rules of other mailboxes forget a provider label that went
// with it (so a later label with the same id isn't applied by surprise).
func TestRemoveAccountForgetsItsRulesQuietListAndMute(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	ids := s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "Outlook Only"}})
	outlookOnly := ids["outlook only"]
	st := s.GetSettings()
	st.LabelRules = []models.LabelRule{
		{ID: "r1", AccountID: demo.OutlookAccountID, Field: "from", Contains: "ci@example.com", Action: "junk"},
		{ID: "r2", AccountID: demo.GmailAccountID, Field: "from", Contains: "ci@example.com", Action: "label", LabelID: "work"},
		{ID: "r3", Field: "subject", Contains: "build", Action: "label", LabelID: outlookOnly},
	}
	st.QuietLabels = map[string][]string{demo.OutlookAccountID: {"work"}, demo.GmailAccountID: {"personal"}}
	st.MutedMailboxes = []string{demo.OutlookAccountID, demo.GmailAccountID}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAccount(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	got := s.GetSettings()
	for _, r := range got.LabelRules {
		if r.AccountID == demo.OutlookAccountID {
			t.Errorf("rule %s of the removed mailbox kept", r.ID)
		}
		if r.LabelID == outlookOnly {
			t.Errorf("rule %s still points at the dropped label %q", r.ID, outlookOnly)
		}
	}
	if !slices.ContainsFunc(got.LabelRules, func(r models.LabelRule) bool { return r.ID == "r2" && r.LabelID == "work" }) {
		t.Errorf("rule of the remaining mailbox changed: %+v", got.LabelRules)
	}
	if _, ok := got.QuietLabels[demo.OutlookAccountID]; ok {
		t.Errorf("quiet list of the removed mailbox kept: %v", got.QuietLabels)
	}
	if !slices.Equal(got.QuietLabels[demo.GmailAccountID], []string{"personal"}) {
		t.Errorf("quiet list of the remaining mailbox changed: %v", got.QuietLabels)
	}
	if !slices.Equal(got.MutedMailboxes, []string{demo.GmailAccountID}) {
		t.Errorf("muted mailboxes %v", got.MutedMailboxes)
	}
}

// A settings save from a UI that loaded the settings before sync imported
// a provider label (labels:changed arrived while a save was pending, so the
// UI's copy never got it) must not delete that label, its assignments, and
// dismiss it for good.
func TestStaleSettingsSaveKeepsLabelsImportedMeanwhile(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	snapshot := s.GetSettings() // what the settings window holds

	ids := s.resolveProviderLabels(demo.GmailAccountID, []mail.Label{{Name: "Receipts"}})
	receipts := ids["receipts"]
	msg := firstMessage(t, s, demo.GmailAccountID)
	if err := s.store.AddLabels([]string{msg}, receipts); err != nil {
		t.Fatal(err)
	}

	snapshot.Theme = "dark" // the user changes something unrelated
	if err := s.SaveSettings(snapshot); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(s.Settings().Labels, func(l models.Label) bool { return l.ID == receipts }) {
		t.Fatal("provider label imported meanwhile was deleted by a stale save")
	}
	if _, ok := labelOf(t, s.store, msg, receipts); !ok {
		t.Fatal("the label's assignments were deleted")
	}
	var dismissed []string
	_ = s.store.GetSetting(dismissedLabelsKey, &dismissed)
	if slices.Contains(dismissed, "receipts") {
		t.Fatal("label dismissed: it would never be imported again")
	}
	if s.Settings().Theme != "dark" {
		t.Fatal("the rest of the save was lost")
	}

	// A UI that has seen the label can delete it.
	fresh := s.GetSettings()
	fresh.Labels = slices.DeleteFunc(fresh.Labels, func(l models.Label) bool { return l.ID == receipts })
	if err := s.SaveSettings(fresh); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(s.Settings().Labels, func(l models.Label) bool { return l.ID == receipts }) {
		t.Fatal("deleting a label the UI knew about didn't stick")
	}
}

// A stale settings save doesn't bring back a provider label that was
// dropped with its mailbox after the UI read the settings.
func TestStaleSettingsSaveDoesNotResurrectDroppedLabels(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	ids := s.resolveProviderLabels(demo.OutlookAccountID, []mail.Label{{Name: "Outlook Only"}})
	outlookOnly := ids["outlook only"]
	snapshot := s.GetSettings()
	if !slices.ContainsFunc(snapshot.Labels, func(l models.Label) bool { return l.ID == outlookOnly }) {
		t.Fatal("setup: label missing")
	}
	if err := s.RemoveAccount(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	snapshot.Theme = "dark"
	if err := s.SaveSettings(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, l := range s.Settings().Labels {
		if l.ID == outlookOnly {
			t.Fatalf("label of the removed mailbox came back: %+v", l)
		}
	}
	if s.Settings().Theme != "dark" {
		t.Fatal("the rest of the save was lost")
	}
}
