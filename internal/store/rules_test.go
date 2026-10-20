package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AVMG20/gomail/internal/mail"
)

// A rule and AI auto-labeling can reach the same message in either order;
// the rule's label must survive a later re-classification that scores it low.
func TestRuleLabelWinsOverAI(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{
		msg(1, "Pipeline failed", "Sam", "INBOX", "ALL"),
		msg(2, "Pipeline passed", "Sam", "INBOX", "ALL"),
	}}, nil); err != nil {
		t.Fatal(err)
	}
	a, b := MessageID("a1", "r001"), MessageID("a1", "r002")
	pol := AIPolicy{AddThreshold: 0.5, RemoveThreshold: 0.2}
	// AI first, then the rule.
	if added, _, _ := s.ApplyAIScores(a, map[string]float64{"gitlab": 0.9}, pol, "h1"); len(added) != 1 {
		t.Fatalf("ai label not added: %v", added)
	}
	// The rule second on a, first on b.
	added, err := s.ApplyRuleLabel([]string{a, b}, "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(added) != fmt.Sprint([]string{b}) {
		t.Fatalf("rule reported %v as added, want only %s (a already showed the label)", added, b)
	}
	for _, id := range []string{a, b} {
		if _, removed, _ := s.ApplyAIScores(id, map[string]float64{"gitlab": 0.01}, pol, "h2"); len(removed) != 0 {
			t.Fatalf("re-classification removed the rule's label from %s", id)
		}
	}
	if again, _ := s.ApplyRuleLabel([]string{a, b}, "gitlab"); len(again) != 0 {
		t.Fatalf("second run added %v", again)
	}
}

// Rules on new mail look the new messages up by id (a first sync stores
// thousands of messages; scanning the whole table for each would be slow).
func TestRuleCandidatesUsesIDs(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	var ups []mail.Message
	for i := 0; i < 30; i++ {
		folder := "INBOX"
		switch i % 3 {
		case 1:
			folder = "SPAM"
		case 2:
			folder = "TRASH"
		}
		ups = append(ups, msg(i, fmt.Sprintf("Message %d", i), "Sam", folder))
	}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: ups}, nil); err != nil {
		t.Fatal(err)
	}
	ids := []string{MessageID("a1", "r000"), MessageID("a1", "r001"), MessageID("a1", "r002"), "missing"}
	got, err := s.RuleCandidates(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].InSpam == got[1].InSpam {
		t.Fatalf("candidates %+v (want the inbox and the spam message, not trash)", got)
	}
	all, _ := s.AllRuleCandidates()
	if len(all) != 20 {
		t.Fatalf("all candidates %d, want 20", len(all))
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+ruleCandidatesQuery+` AND id IN (?, ?)`,
		ruleCandidatesArgs(ids[:2])...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if p := strings.Join(plan, "; "); !strings.Contains(p, "SEARCH") || strings.Contains(p, "SCAN") {
		t.Fatalf("RuleCandidates scans the table: %s", p)
	}
}
