package store

import (
	"fmt"
	"testing"

	"github.com/AVMG20/gomail/internal/mail"
)

// A provider label the user confirmed by hand (upgrading it to manual) and
// then removed stays removed when the provider still reports it.
func TestRemovedManualProviderLabelStaysRemoved(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := msg(1, "Report", "Boss", "INBOX", "ALL")
	m.Labels = []string{"Work"}
	ids := map[string]string{"work": "work"}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, ids); err != nil {
		t.Fatal(err)
	}
	id := MessageID("a1", m.RemoteID)
	if err := s.AddLabels([]string{id}, "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{id}, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, ids); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Message(id)
	if len(got.Labels) != 0 {
		t.Fatalf("removed label came back after sync: %v", got.LabelDetails)
	}
	// The provider dropping and re-adding the label applies again.
	m2 := m
	m2.Labels = nil
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m2}}, ids)
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, ids)
	got, _, _ = s.Message(id)
	if fmt.Sprint(got.Labels) != "[work]" {
		t.Fatalf("provider re-add ignored: %v", got.Labels)
	}
}

// Junk confirmed by hand and then removed doesn't come back while the
// message stays in Spam.
func TestRemovedManualJunkStaysRemoved(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := msg(1, "Win a prize", "Spammer", "SPAM")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, nil); err != nil {
		t.Fatal(err)
	}
	id := MessageID("a1", m.RemoteID)
	if got, _, _ := s.Message(id); fmt.Sprint(got.Labels) != "[junk]" {
		t.Fatalf("spam not junk: %v", got.Labels)
	}
	if err := s.AddLabels([]string{id}, "junk"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{id}, "junk"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Message(id); len(got.Labels) != 0 {
		t.Fatalf("removed junk came back after sync: %v", got.LabelDetails)
	}
}

// A message that leaves every synced folder (e.g. an old message moved in
// another Outlook client) is dropped locally, but the user's label
// decisions come back with it.
func TestOrphanKeepsLabelMarks(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := msg(1, "Old thread", "Ann", "INBOX")
	m.Labels = []string{"Finance"}
	ids := map[string]string{"finance": "finance"}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, ids); err != nil {
		t.Fatal(err)
	}
	id := MessageID("a1", m.RemoteID)
	if err := s.AddLabels([]string{id}, "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLabels([]string{id}, "personal"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{id}, "personal"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{id}, "finance"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySync("a1", mail.SyncResult{Removed: map[string][]string{"INBOX": {m.RemoteID}}}, ids); err != nil {
		t.Fatal(err)
	}
	if sums, _ := s.Summaries([]string{id}); len(sums) != 0 {
		t.Fatal("orphan not dropped")
	}
	// It turns up again in another folder, still carrying the provider label.
	m.FolderRemoteIDs = []string{"ALL"}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}}, ids); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Message(id)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got.LabelDetails) != "[{work manual 1}]" {
		t.Fatalf("labels after the message came back: %v", got.LabelDetails)
	}
	_, _, err = s.ApplyAIScores(id, map[string]float64{"personal": 0.99}, AIPolicy{AddThreshold: 0.5}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Message(id); fmt.Sprint(got.Labels) != "[work]" {
		t.Fatalf("rejection lost: %v", got.Labels)
	}
	if n, _ := s.PendingLabels("", ""); n != 0 {
		t.Fatalf("%d pending marks left", n)
	}
}
