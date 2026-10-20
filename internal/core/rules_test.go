package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/syncer"
)

func labelsOf(t *testing.T, e *env, accountID, subject string) (models.MessageSummary, bool) {
	t.Helper()
	page, err := e.s.ListMessages(models.ListQuery{AccountID: accountID, Search: subject, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page.Messages {
		if m.Subject == subject {
			return m, true
		}
	}
	return models.MessageSummary{}, false
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Rules label (or junk) new mail without AI; quiet labels stop notifications.
func TestLabelRulesAndQuietLabels(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	st := s.Settings()
	st.Labels = append(st.Labels, models.Label{ID: "gitlab", Name: "GitLab", Color: "#f97316", Source: "user"})
	st.LabelRules = []models.LabelRule{
		{ID: "r1", AccountID: demo.GmailAccountID, Field: "from", Contains: "GitLab", Action: "label", LabelID: "gitlab"},
		{ID: "r2", Field: "subject", Contains: "casino", Action: "junk"},
		{ID: "r3", AccountID: demo.OutlookAccountID, Field: "from", Contains: "gitlab", Action: "label", LabelID: "gitlab"},
	}
	st.QuietLabels = map[string][]string{demo.GmailAccountID: {"gitlab", "junk"}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	notified := len(e.ui.notified)

	e.demos[demo.GmailAccountID].Deliver("Pipeline failed", "noreply@gitlab.com")
	e.demos[demo.GmailAccountID].Deliver("Big casino bonus", "promo@example.com")
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	m, ok := labelsOf(t, e, demo.GmailAccountID, "Pipeline failed")
	if !ok || !has(m.Labels, "gitlab") {
		t.Fatalf("gitlab rule not applied: %+v", m.Labels)
	}
	spam, _ := s.store.FolderByKind(demo.GmailAccountID, models.FolderSpam)
	j, ok := labelsOf(t, e, demo.GmailAccountID, "Big casino bonus")
	if !ok || !has(j.FolderIDs, spam.ID) {
		t.Fatalf("junk rule didn't move the message: %+v", j.FolderIDs)
	}
	if len(e.ui.notified) != notified {
		t.Fatalf("quiet/junk mail notified: %v", e.ui.notified[notified:])
	}

	// Outlook: the label applies, but GitLab isn't quiet there (default list = Junk only).
	e.demos[demo.OutlookAccountID].Deliver("Merge request", "gitlab@example.com")
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	// The label had no mailbox: Outlook got its own copy, and its rule follows it.
	if o, ok := labelsOf(t, e, demo.OutlookAccountID, "Merge request"); !ok || !has(o.Labels, mailboxLabel(t, s, demo.OutlookAccountID, "GitLab")) {
		t.Fatalf("outlook rule not applied: %+v", o.Labels)
	}
	if len(e.ui.notified) != notified+1 {
		t.Fatalf("expected one notification for outlook, got %d", len(e.ui.notified)-notified)
	}

	// A label removed by hand isn't re-added by "Apply to existing mail".
	if err := s.RemoveLabel([]string{m.ID}, "gitlab"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyLabelRules(); err != nil {
		t.Fatal(err)
	}
	if m, _ := labelsOf(t, e, demo.GmailAccountID, "Pipeline failed"); has(m.Labels, "gitlab") {
		t.Fatal("rule re-added a label the user removed")
	}
}

func saveRules(t *testing.T, s *Service, rules []models.LabelRule, labels ...models.Label) {
	t.Helper()
	st := s.Settings()
	st.Labels = append(st.Labels, labels...)
	st.LabelRules = rules
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
}

// "Apply to existing mail" reports only what it changed, so running it twice
// says nothing changed the second time; rules for deleted labels do nothing.
func TestApplyLabelRulesCountsOnlyChanges(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	gitlab := models.Label{ID: "gitlab", Name: "GitLab", Color: "#f97316", Source: "user"}
	saveRules(t, s, []models.LabelRule{
		{ID: "r1", Field: "from", Contains: "gitlab", Action: "label", LabelID: "gitlab"},
		{ID: "r2", Field: "from", Contains: "gitlab", Action: "label", LabelID: "deleted-label"},
	}, gitlab)
	e.demos[demo.GmailAccountID].Deliver("Pipeline passed", "noreply@gitlab.com")
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ApplyLabelRules(); err != nil || n != 0 {
		t.Fatalf("second pass changed %d (%v), want 0", n, err)
	}
	e.demos[demo.OutlookAccountID].Deliver("Issue opened", "gitlab@example.com")
	// Ingest without rules: rules off, sync, rules on, then apply by hand.
	saveRules(t, s, nil)
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	saveRules(t, s, []models.LabelRule{{ID: "r1", AccountID: demo.OutlookAccountID, Field: "from", Contains: "gitlab", Action: "label",
		LabelID: mailboxLabel(t, s, demo.OutlookAccountID, "GitLab")}})
	if n, err := s.ApplyLabelRules(); err != nil || n != 1 {
		t.Fatalf("apply changed %d (%v), want 1", n, err)
	}
	if n, _ := s.ApplyLabelRules(); n != 0 {
		t.Fatalf("repeat apply changed %d, want 0", n)
	}
	var rows int
	if err := s.store.DB().QueryRow(`SELECT count(*) FROM message_labels WHERE label_id = 'deleted-label'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("deleted label assigned to %d messages (%v)", rows, err)
	}
}

// A Junk rule only acts on new mail: a message the user moved back to the
// inbox stays there on the next sync and on "Apply to existing mail". A
// Junk move the server refuses leaves the mail in the inbox, where it
// notifies like any other mail.
func TestJunkRuleEdgeCases(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	saveRules(t, s, []models.LabelRule{{ID: "r1", Field: "subject", Contains: "lottery", Action: "junk"}})
	acct := demo.GmailAccountID
	inbox, _ := s.store.FolderByKind(acct, models.FolderInbox)
	spam, _ := s.store.FolderByKind(acct, models.FolderSpam)

	e.demos[acct].Deliver("Lottery club meeting", "sam@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	m, ok := labelsOf(t, e, acct, "Lottery club meeting")
	if !ok || !has(m.FolderIDs, spam.ID) {
		t.Fatalf("not junked: %+v", m.FolderIDs)
	}
	if err := s.MoveToFolder([]string{m.ID}, inbox.ID); err != nil {
		t.Fatal(err)
	}
	e.demos[acct].Deliver("Unrelated", "alex.morgan@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	if m, _ := labelsOf(t, e, acct, "Lottery club meeting"); has(m.FolderIDs, spam.ID) || !has(m.FolderIDs, inbox.ID) {
		t.Fatalf("rule re-junked mail the user moved back: %+v", m.FolderIDs)
	}

	// Mail the provider already filed as spam isn't moved again.
	e.demos[acct].DeliverTo(models.FolderSpam, "Lottery winner", "promo@example.com")
	e.provs[acct].failMove = true // any move attempt would fail loudly below
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	if j, _ := labelsOf(t, e, acct, "Lottery winner"); !has(j.FolderIDs, spam.ID) {
		t.Fatalf("spam mail moved: %+v", j.FolderIDs)
	}

	notified := len(e.ui.notified)
	e.demos[acct].Deliver("Lottery results", "promo@example.com")
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	f, _ := labelsOf(t, e, acct, "Lottery results")
	if has(f.FolderIDs, spam.ID) || !has(f.FolderIDs, inbox.ID) {
		t.Fatalf("failed move not reverted: %+v", f.FolderIDs)
	}
	if len(e.ui.notified) != notified+1 {
		t.Fatalf("mail left in the inbox should notify once, got %d", len(e.ui.notified)-notified)
	}

	// "Apply to existing mail" never moves mail to Junk: that would undo the
	// user's own "not junk" moves (and the failed move above).
	e.provs[acct].failMove = false
	if n, err := s.ApplyLabelRules(); err != nil || n != 0 {
		t.Fatalf("apply with only a Junk rule changed %d (%v)", n, err)
	}
	for _, subj := range []string{"Lottery club meeting", "Lottery results"} {
		if m, _ := labelsOf(t, e, acct, subj); has(m.FolderIDs, spam.ID) {
			t.Fatalf("%q junked by Apply to existing mail", subj)
		}
	}
}

// A first sync can match more mail than one engine action allows
// (MaxBulk); rules move it, and push provider labels, in batches.
func TestJunkRuleManyMessages(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	acct := demo.OutlookAccountID
	work := mailboxLabel(t, s, acct, "Travel")
	saveRules(t, s, []models.LabelRule{
		{ID: "r1", AccountID: acct, Field: "from", Contains: "bulk@example.com", Action: "junk"},
		{ID: "r2", AccountID: acct, Field: "from", Contains: "bulk@example.com", Action: "label", LabelID: work},
	})
	n := syncer.MaxBulk + 1
	for i := 0; i < n; i++ {
		e.demos[acct].Deliver(fmt.Sprintf("Weekly deal %d", i), "bulk@example.com")
	}
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	var junked, total int
	if err := s.store.DB().QueryRow(`SELECT count(*), coalesce(sum((folder_kinds & 16) != 0), 0) FROM messages
		WHERE account_id = ? AND from_email = 'bulk@example.com'`, acct).Scan(&total, &junked); err != nil {
		t.Fatal(err)
	}
	if total != n || junked != n {
		t.Fatalf("%d of %d messages junked", junked, total)
	}
	// The label reaches Outlook for every message (pushes run in the background).
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := e.demos[acct].Search(context.Background(), mail.SearchQuery{From: []string{"bulk@example.com"}}, 2*n)
		if err != nil {
			t.Fatal(err)
		}
		pushed := 0
		for _, m := range res {
			if has(m.Labels, "Travel") {
				pushed++
			}
		}
		if pushed == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("label pushed to %d of %d messages", pushed, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Quiet labels for a mailbox replace the default list; a deleted label in
// the list is ignored.
func TestQuietLabelsPerMailbox(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	gitlab := models.Label{ID: "gitlab", Name: "GitLab", Color: "#f97316", Source: "user"}
	saveRules(t, s, []models.LabelRule{{ID: "r1", Field: "from", Contains: "gitlab", Action: "label", LabelID: "gitlab"}}, gitlab)
	st := s.Settings()
	st.QuietLabels = map[string][]string{demo.GmailAccountID: {"deleted-label"}, demo.OutlookAccountID: {mailboxLabel(t, s, demo.OutlookAccountID, "GitLab")}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	notified := len(e.ui.notified)
	e.demos[demo.GmailAccountID].Deliver("Build ok", "gitlab@example.com")
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	e.demos[demo.OutlookAccountID].Deliver("Build ok", "gitlab@example.com")
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	if got := len(e.ui.notified) - notified; got != 1 {
		t.Fatalf("want only the gmail mail to notify, got %d", got)
	}
}

// A mailbox with notifications off never notifies (whatever the labels);
// other mailboxes still do. Junk never notifies, even when the user's quiet
// list for the mailbox is empty.
func TestMutedMailbox(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	st := s.Settings()
	st.MutedMailboxes = []string{demo.GmailAccountID}
	st.QuietLabels = map[string][]string{demo.OutlookAccountID: {}}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	notified, events := len(e.ui.notified), e.ui.count(models.EventNewMail)
	e.demos[demo.GmailAccountID].Deliver("Lunch on Friday?", "sam@example.com")
	if err := s.SyncNow(demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	if len(e.ui.notified) != notified || e.ui.count(models.EventNewMail) != events {
		t.Fatalf("muted mailbox notified: %v", e.ui.notified[notified:])
	}
	if m, ok := labelsOf(t, e, demo.GmailAccountID, "Lunch on Friday?"); !ok || !m.Unread {
		t.Fatal("muted mail must still arrive (unread)")
	}
	e.demos[demo.OutlookAccountID].Deliver("Lunch on Friday?", "sam@example.com")
	if err := s.SyncNow(demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	if len(e.ui.notified) != notified+1 {
		t.Fatalf("other mailbox should notify once, got %d", len(e.ui.notified)-notified)
	}
	if !s.quietMessage(demo.OutlookAccountID, models.MessageSummary{Labels: []string{"junk"}}) {
		t.Fatal("junk-labeled mail notifies when the quiet list is empty")
	}
	if s.quietMessage(demo.OutlookAccountID, models.MessageSummary{Labels: []string{"work"}}) {
		t.Fatal("unlisted label is quiet")
	}
}

// Labeling by hand does what the label's "When added" says, like a rule:
// the built-in Junk label and a label set to Junk move the mail to Junk,
// read; a label without an action leaves it. The user's own moves aren't
// in the "sorted for you" review.
func TestManualLabelMovesMail(t *testing.T) {
	e := newEnv(t, "")
	s := e.s
	acct := demo.GmailAccountID
	saveRules(t, s, nil,
		models.Label{ID: "spammy", Name: "Spammy", Color: "#f97316", Source: "user", AccountIDs: []string{acct}, Then: "junk"},
		models.Label{ID: "plain", Name: "Plain", Color: "#22c55e", Source: "user", AccountIDs: []string{acct}})
	for _, subj := range []string{"By junk label", "By junk action", "No action"} {
		e.demos[acct].Deliver(subj, "someone@example.com")
	}
	if err := s.SyncNow(acct); err != nil {
		t.Fatal(err)
	}
	spam, _ := s.store.FolderByKind(acct, models.FolderSpam)
	for subj, label := range map[string]string{"By junk label": "junk", "By junk action": "spammy", "No action": "plain"} {
		m, ok := labelsOf(t, e, acct, subj)
		if !ok {
			t.Fatalf("%q not synced", subj)
		}
		if err := s.AddLabel([]string{m.ID}, label); err != nil {
			t.Fatal(err)
		}
		m, _ = labelsOf(t, e, acct, subj)
		if inSpam := has(m.FolderIDs, spam.ID); inSpam != (label != "plain") {
			t.Fatalf("%q with %s: in Junk %v (%v)", subj, label, inSpam, m.FolderIDs)
		}
		if label != "plain" && m.Unread {
			t.Fatalf("%q moved to Junk unread", subj)
		}
	}
	if moves, _ := s.AutoMoves(acct, models.FolderSpam, 24); len(moves) != 0 {
		t.Fatalf("manual moves in the review: %+v", moves)
	}
}
