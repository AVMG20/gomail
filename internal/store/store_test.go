package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedAccount(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.InsertAccount(models.Account{ID: id, Provider: models.ProviderGmail, Email: id + "@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceFolders(id, []mail.Folder{
		{RemoteID: "INBOX", Name: "Inbox", Kind: models.FolderInbox, Unread: 3, Total: 10},
		{RemoteID: "STARRED", Name: "Starred", Kind: models.FolderStarred},
		{RemoteID: "SPAM", Name: "Spam", Kind: models.FolderSpam},
		{RemoteID: "TRASH", Name: "Trash", Kind: models.FolderTrash},
		{RemoteID: "ALL", Name: "All Mail", Kind: models.FolderArchive},
	}); err != nil {
		t.Fatal(err)
	}
}

var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func msg(i int, subject, from string, folders ...string) mail.Message {
	return mail.Message{
		RemoteID:        fmt.Sprintf("r%03d", i),
		ThreadID:        fmt.Sprintf("t%d", i%5),
		FolderRemoteIDs: folders,
		From:            models.Address{Name: from, Email: strings.ToLower(strings.ReplaceAll(from, " ", ".")) + "@example.com"},
		To:              []models.Address{{Name: "Me", Email: "me@example.com"}},
		Subject:         subject,
		Snippet:         "snippet for " + subject,
		Date:            base.Add(time.Duration(i) * time.Hour),
		Unread:          i%2 == 0,
	}
}

func TestApplySyncAndPagination(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	var ups []mail.Message
	for i := 0; i < 25; i++ {
		ups = append(ups, msg(i, fmt.Sprintf("Message %d", i), "Alice Smith", "INBOX", "ALL"))
	}
	res, err := s.ApplySync("a1", mail.SyncResult{Upserts: ups, State: map[string]string{"historyId": "42"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New) != 25 {
		t.Fatalf("new = %d", len(res.New))
	}
	// Re-applying is idempotent.
	res, err = s.ApplySync("a1", mail.SyncResult{Upserts: ups[:3]}, nil)
	if err != nil || len(res.New) != 0 {
		t.Fatalf("reapply: %v %d", err, len(res.New))
	}
	st, _ := s.SyncState("a1")
	if st["historyId"] != "42" {
		t.Fatalf("state %v", st)
	}

	var all []models.MessageSummary
	cursor := ""
	for pages := 0; ; pages++ {
		p, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox, Limit: 10, Cursor: cursor}, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 25 {
			t.Fatalf("total = %d", p.Total)
		}
		all = append(all, p.Messages...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
		if pages > 5 {
			t.Fatal("too many pages")
		}
	}
	if len(all) != 25 {
		t.Fatalf("got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Date.After(all[i-1].Date) {
			t.Fatal("not sorted desc")
		}
	}
	if all[0].Subject != "Message 24" || len(all[0].FolderIDs) != 2 {
		t.Fatalf("first = %+v", all[0])
	}

	p, _ := s.ListMessages(models.ListQuery{UnreadOnly: true, AccountID: "a1"}, ListOptions{})
	if p.Total != 13 {
		t.Fatalf("unread total %d", p.Total)
	}

	// Deleting and removing from folders.
	_, err = s.ApplySync("a1", mail.SyncResult{Deleted: []string{"r000"}, Removed: map[string][]string{"INBOX": {"r001"}, "ALL": {"r001"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ = s.ListMessages(models.ListQuery{AccountID: "a1"}, ListOptions{})
	if p.Total != 23 {
		t.Fatalf("after delete total %d", p.Total)
	}
}

func TestUnreadCountersAndMove(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	_, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{msg(0, "hello", "Bob", "INBOX", "ALL")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := MessageID("a1", "r000")
	changed, err := s.SetUnread([]string{id}, false)
	if err != nil || len(changed) != 1 {
		t.Fatal(err, changed)
	}
	if changed, _ := s.SetUnread([]string{id}, false); len(changed) != 0 {
		t.Fatal("no-op change reported")
	}
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	if inbox.Unread != 2 {
		t.Fatalf("inbox unread %d", inbox.Unread)
	}
	acct, _ := s.Account("a1")
	if acct.UnreadCount != 2 {
		t.Fatalf("account unread %d", acct.UnreadCount)
	}
	spam, _ := s.FolderByKind("a1", models.FolderSpam)
	if err := s.SetMessageFolders(map[string][]string{id: {spam.ID}}); err != nil {
		t.Fatal(err)
	}
	m, _, _ := s.Message(id)
	if len(m.Labels) != 1 || m.Labels[0] != "junk" || m.LabelDetails[0].Source != "system" {
		t.Fatalf("junk label missing: %+v", m.LabelDetails)
	}
	inbox, _ = s.FolderByKind("a1", models.FolderInbox)
	if inbox.Total != 9 {
		t.Fatalf("inbox total %d", inbox.Total)
	}
	if err := s.SetMessageFolders(map[string][]string{id: {inbox.ID}}); err != nil {
		t.Fatal(err)
	}
	m, _, _ = s.Message(id)
	if len(m.Labels) != 0 {
		t.Fatalf("junk label should be gone: %v", m.Labels)
	}
	// starred folder counts from flag
	if _, err := s.SetStarred([]string{id}, true); err != nil {
		t.Fatal(err)
	}
	st, _ := s.FolderByKind("a1", models.FolderStarred)
	if st.Total != 1 {
		t.Fatalf("starred total %d", st.Total)
	}
	p, _ := s.ListMessages(models.ListQuery{FolderID: st.ID}, ListOptions{})
	if p.Total != 1 {
		t.Fatalf("starred list %d", p.Total)
	}
}

func TestLabels(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m1 := msg(1, "Invoice 42", "Acme Billing", "INBOX", "ALL")
	m1.Labels = []string{"Finance", "Unknown"}
	m2 := msg(2, "Lunch?", "Carol", "INBOX", "ALL")
	ids := map[string]string{"finance": "finance"}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m1, m2}}, ids); err != nil {
		t.Fatal(err)
	}
	id1, id2 := MessageID("a1", "r001"), MessageID("a1", "r002")
	m, _, _ := s.Message(id1)
	if len(m.LabelDetails) != 1 || m.LabelDetails[0].Source != "provider" {
		t.Fatalf("provider label: %+v", m.LabelDetails)
	}
	if err := s.AddLabels([]string{id2}, "personal"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.ListMessages(models.ListQuery{Labels: []string{"personal", "finance"}}, ListOptions{})
	if p.Total != 2 {
		t.Fatalf("labels any %d", p.Total)
	}
	p, _ = s.ListMessages(models.ListQuery{Unlabeled: true}, ListOptions{})
	if p.Total != 0 {
		t.Fatalf("unlabeled %d", p.Total)
	}

	// AI: low P(finance) removes the provider label and records an override.
	pol := AIPolicy{AddThreshold: 0.5, RemoveThreshold: 0.15, CorrectProvider: true}
	added, removed, err := s.ApplyAIScores(id1, map[string]float64{"finance": 0.05, "work": 0.9}, pol, "")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(added, removed) != "[work] [finance]" {
		t.Fatalf("added %v removed %v", added, removed)
	}
	// Next sync still reports Finance: the override keeps it removed.
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m1}}, ids); err != nil {
		t.Fatal(err)
	}
	m, _, _ = s.Message(id1)
	if fmt.Sprint(m.Labels) != "[work]" {
		t.Fatalf("labels after resync %v", m.Labels)
	}
	// Provider drops then re-adds the label: it applies again.
	m1b := m1
	m1b.Labels = nil
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m1b}}, ids)
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m1}}, ids)
	m, _, _ = s.Message(id1)
	if fmt.Sprint(m.Labels) != "[finance work]" {
		t.Fatalf("labels after re-add %v", m.Labels)
	}
	// Re-classification replaces AI labels but never manual ones.
	added, removed, _ = s.ApplyAIScores(id1, map[string]float64{"work": 0.2}, pol, "")
	if len(added) != 0 || fmt.Sprint(removed) != "[work]" {
		t.Fatalf("reclassify %v %v", added, removed)
	}
	_, removed, _ = s.ApplyAIScores(id2, map[string]float64{"personal": 0.0}, pol, "")
	if len(removed) != 0 {
		t.Fatal("manual label removed by AI")
	}
	// Hiding provider labels.
	s.SetHideProviderLabels(true)
	m, _, _ = s.Message(id1)
	if len(m.Labels) != 0 {
		t.Fatalf("hidden provider labels visible: %v", m.Labels)
	}
	s.SetHideProviderLabels(false)

	counts, err := s.LabelCounts("")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(counts) != "[{finance 1 0} {personal 1 1}]" {
		t.Fatalf("counts %v", counts)
	}
	if err := s.DeleteUnknownLabels([]string{"finance"}); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.LabelCounts("")
	if len(counts) != 1 {
		t.Fatalf("counts after delete %v", counts)
	}
}

func TestDeleteAccountCascades(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	seedAccount(t, s, "a2")
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{msg(1, "one", "X", "INBOX")}}, nil)
	s.ApplySync("a2", mail.SyncResult{Upserts: []mail.Message{msg(1, "one", "X", "INBOX")}}, nil)
	if err := s.DeleteAccount("a1"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.ListMessages(models.ListQuery{}, ListOptions{})
	if p.Total != 1 || p.Messages[0].AccountID != "a2" {
		t.Fatalf("%+v", p)
	}
	res, err := s.Search(models.SearchQuery{Query: "one"})
	if err != nil || len(res) != 1 {
		t.Fatalf("search after delete: %v %d", err, len(res))
	}
}

// TestFullResyncKeepsLabels mirrors Gmail's history 404 fallback: a full
// (Initial) round re-upserts messages; manual labels and AI overrides of
// provider labels must survive.
func TestFullResyncKeepsLabels(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := msg(1, "Invoice", "Acme", "INBOX", "ALL")
	m.Labels = []string{"Finance"}
	ids := map[string]string{"finance": "finance"}
	s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}, State: map[string]string{"historyId": "5"}}, ids)
	id := MessageID("a1", m.RemoteID)
	s.AddLabels([]string{id}, "work")
	s.ApplyAIScores(id, map[string]float64{"finance": 0.01}, AIPolicy{AddThreshold: 0.5, RemoveThreshold: 0.15, CorrectProvider: true}, "h")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m}, State: map[string]string{"historyId": "99"}, Initial: true}, ids); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Message(id)
	if fmt.Sprint(got.Labels) != "[work]" {
		t.Fatalf("labels after full resync: %v", got.Labels)
	}
	if st, _ := s.SyncState("a1"); st["historyId"] != "99" {
		t.Fatal(st)
	}
}
