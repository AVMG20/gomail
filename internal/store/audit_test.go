package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

// An upsert that changes a message's thread and unread flag at once must
// not count the unread change twice (folder_threads.unread went to -1).
func TestRethreadAndUnreadInOneUpdate(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	a := threadMsg("A", "T1", 1, "Ann", "INBOX")
	a.Unread = true
	b := threadMsg("B", "T2", 2, "Bob", "INBOX")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{a, b}}, nil); err != nil {
		t.Fatal(err)
	}
	a.ThreadID, a.Unread = "T2", false
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{a}}, nil); err != nil {
		t.Fatal(err)
	}
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	var unread int
	if err := s.db.QueryRow(`SELECT unread FROM folder_threads WHERE folder_id = ? AND thread_key = 'T2'`, inbox.ID).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if unread != 0 {
		t.Fatalf("folder_threads(INBOX,T2).unread = %d, want 0", unread)
	}
	c := threadMsg("C", "T2", 3, "Cid", "INBOX")
	c.Unread = true
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{c}}, nil); err != nil {
		t.Fatal(err)
	}
	q := models.ListQuery{FolderID: inbox.ID, UnreadOnly: true}
	p, err := s.ListMessages(q, ListOptions{Conversations: true})
	if err != nil {
		t.Fatal(err)
	}
	slow, _ := s.slowConversationCount(q)
	if p.Total != len(p.Messages) || p.Total != slow {
		t.Fatalf("unread-only total %d, rows %d, recount %d", p.Total, len(p.Messages), slow)
	}
}

// A newer trashed reply must not hide the live conversation in views
// without a folder filter (search across all mail).
func TestTrashedReplyDoesNotHideLiveThread(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	live := threadMsg("A", "T1", 1, "Ann", "INBOX", "ALL")
	trashed := threadMsg("B", "T1", 2, "Bob", "TRASH")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{live, trashed}}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := s.ListMessages(models.ListQuery{}, ListOptions{Conversations: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, m := range p.Messages {
		got[m.RemoteID] = m.ThreadCount
	}
	if got["A"] != 1 || got["B"] != 1 || p.Total != 2 {
		t.Fatalf("rows %v total %d", got, p.Total)
	}
}

// Classification never re-adds a label the user removed by hand (provider
// or AI assignment alike); adding it by hand again lifts that.
func TestAIRespectsManualRemoval(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	m := threadMsg("A", "T1", 1, "Ann", "INBOX")
	m.Labels = []string{"Newsletter"}
	n := threadMsg("B", "T2", 2, "Bob", "INBOX")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{m, n}}, map[string]string{"newsletter": "news"}); err != nil {
		t.Fatal(err)
	}
	pol := AIPolicy{AddThreshold: 0.5, RemoveThreshold: 0.2}
	a, b := MessageID("a1", "A"), MessageID("a1", "B")
	if added, _, _ := s.ApplyAIScores(b, map[string]float64{"work": 0.9}, pol, "h1"); len(added) != 1 {
		t.Fatalf("ai label not added: %v", added)
	}
	if err := s.RemoveLabels([]string{a}, "news"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{b}, "work"); err != nil {
		t.Fatal(err)
	}
	if added, _, _ := s.ApplyAIScores(a, map[string]float64{"news": 0.99}, pol, "h2"); len(added) != 0 {
		t.Fatalf("removed provider label re-added by AI: %v", added)
	}
	if added, _, _ := s.ApplyAIScores(b, map[string]float64{"work": 0.99}, pol, "h2"); len(added) != 0 {
		t.Fatalf("removed AI label re-added: %v", added)
	}
	if err := s.AddLabels([]string{b}, "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLabels([]string{b}, "work"); err != nil {
		t.Fatal(err)
	}
	_ = s.AddLabels([]string{b}, "work")
	var n2 int
	s.db.QueryRow(`SELECT count(*) FROM label_rejections WHERE message_id = ?`, b).Scan(&n2)
	if n2 != 0 {
		t.Fatal("adding the label by hand kept the rejection")
	}
}

// A full listing (expired history, re-synced from scratch) drops cached
// memberships the listing no longer reports, within its window only.
func TestCompleteListingReconciles(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	old := threadMsg("old", "T1", -100, "Ann", "INBOX", "ALL") // before the window: kept
	gone := threadMsg("gone", "T2", 5, "Bob", "INBOX", "ALL")  // deleted elsewhere meanwhile
	kept := threadMsg("kept", "T3", 6, "Cid", "INBOX", "ALL")  // still listed
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{old, gone, kept}}, nil); err != nil {
		t.Fatal(err)
	}
	res := mail.SyncResult{Initial: true, Upserts: []mail.Message{kept},
		Complete: map[string]time.Time{"INBOX": base, "ALL": base}}
	if _, err := s.ApplySync("a1", res, nil); err != nil {
		t.Fatal(err)
	}
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	p, _ := s.ListMessages(models.ListQuery{FolderID: inbox.ID}, ListOptions{})
	var ids []string
	for _, m := range p.Messages {
		ids = append(ids, m.RemoteID)
	}
	if fmt.Sprint(ids) != "[kept old]" {
		t.Fatalf("inbox after full listing: %v", ids)
	}
	if _, _, err := s.Message(MessageID("a1", "gone")); err == nil {
		t.Fatal("message in no folder any more was kept")
	}
	// The windows seed the older-mail cursors.
	if c, _ := s.OlderCursor("a1", inbox.ID); !c.Equal(base) {
		t.Fatalf("inbox cursor %v", c)
	}
}

// The older-mail cursor only moves down, and falls back to the oldest
// cached message.
func TestOlderCursor(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: []mail.Message{threadMsg("x", "T", 10, "Ann", "ALL")}}, nil); err != nil {
		t.Fatal(err)
	}
	all, _ := s.FolderByKind("a1", models.FolderArchive)
	if c, _ := s.OlderCursor("a1", all.ID); !c.Equal(base.Add(10 * time.Hour)) {
		t.Fatalf("fallback cursor %v", c)
	}
	// A full listing whose window ends later than the cached mail.
	if _, err := s.ApplySync("a1", mail.SyncResult{Initial: true, Complete: map[string]time.Time{"ALL": base.Add(20 * time.Hour)}}, nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.OlderCursor("a1", all.ID); !c.Equal(base.Add(20 * time.Hour)) {
		t.Fatalf("seeded cursor %v", c)
	}
	_ = s.AdvanceOlderCursor("a1", all.ID, []time.Time{base.Add(15 * time.Hour), base.Add(12 * time.Hour)})
	_ = s.AdvanceOlderCursor("a1", all.ID, []time.Time{base.Add(30 * time.Hour)}) // never moves up
	if c, _ := s.OlderCursor("a1", all.ID); !c.Equal(base.Add(12 * time.Hour)) {
		t.Fatalf("advanced cursor %v", c)
	}
}

// A panic inside a transaction must not leave it open.
func TestTxPanicRollsBack(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	func() {
		defer func() { _ = recover() }()
		_ = s.tx(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE accounts SET display_name = 'x'`); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE accounts SET display_name = 'y'`)
		return err
	}); err != nil {
		t.Fatalf("write after a panicking transaction: %v", err)
	}
}

// Upgrading a v8 database repairs folder_threads counters corrupted by the
// old unread trigger and adds label_rejections.
func TestMigrateFromV8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v8.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for i := range migrations[:8] {
		tx, _ := db.Begin()
		if err := applyMigration(context.Background(), tx, i); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		tx.Commit()
	}
	for _, q := range []string{
		`PRAGMA user_version = 8`,
		`INSERT INTO accounts(id, provider, email, created_at) VALUES('a', 'gmail', 'me@x.com', 1)`,
		`INSERT INTO folders(id, account_id, remote_id, name, kind) VALUES('fi', 'a', 'INBOX', 'Inbox', 'inbox')`,
		`INSERT INTO messages(id, account_id, remote_id, thread_id, from_email, subject, date, unread, folder_kinds)
			VALUES('m1', 'a', 'r1', 'T1', 'x@x.com', 's', 1, 1, 1), ('m2', 'a', 'r2', 'T2', 'y@x.com', 's', 2, 0, 1)`,
		`INSERT INTO message_folders(folder_id, message_id) VALUES('fi', 'm1'), ('fi', 'm2')`,
		// The old trigger pair miscounts this.
		`UPDATE messages SET thread_id = 'T2', unread = 0 WHERE id = 'm1'`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var before int
	db.QueryRow(`SELECT unread FROM folder_threads WHERE thread_key = 'T2'`).Scan(&before)
	if before != -1 {
		t.Fatalf("v8 setup: expected the old miscount, got %d", before)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, unread, msgs int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	s.db.QueryRow(`SELECT unread, msgs FROM folder_threads WHERE thread_key = 'T2'`).Scan(&unread, &msgs)
	if version != len(migrations) || unread != 0 || msgs != 2 {
		t.Fatalf("version %d unread %d msgs %d", version, unread, msgs)
	}
	if err := s.RemoveLabels([]string{"m1"}, "work"); err != nil {
		t.Fatalf("label_rejections missing: %v", err)
	}
}

// "No label" combined with labels means either (like the filter popover).
func TestUnlabeledOrLabels(t *testing.T) {
	s := openTest(t)
	seedAccount(t, s, "a1")
	ms := []mail.Message{threadMsg("A", "T1", 1, "Ann", "INBOX"), threadMsg("B", "T2", 2, "Bob", "INBOX"), threadMsg("C", "T3", 3, "Cid", "INBOX")}
	if _, err := s.ApplySync("a1", mail.SyncResult{Upserts: ms}, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.AddLabels([]string{MessageID("a1", "A")}, "work")
	_ = s.AddLabels([]string{MessageID("a1", "C")}, "personal")
	inbox, _ := s.FolderByKind("a1", models.FolderInbox)
	got := func(q models.ListQuery) string {
		q.FolderID = inbox.ID
		p, err := s.ListMessages(q, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, m := range p.Messages {
			ids = append(ids, m.RemoteID)
		}
		return fmt.Sprint(ids, p.Total)
	}
	if g := got(models.ListQuery{Labels: []string{"work"}, Unlabeled: true}); g != "[B A] 2" {
		t.Fatalf("work or unlabeled: %s", g)
	}
	if g := got(models.ListQuery{Unlabeled: true}); g != "[B] 1" {
		t.Fatalf("unlabeled: %s", g)
	}
	if g := got(models.ListQuery{Labels: []string{"work", "personal"}}); g != "[C A] 2" {
		t.Fatalf("labels: %s", g)
	}
	if g := got(models.ListQuery{ExcludeLabels: []string{"work"}}); g != "[C B] 2" {
		t.Fatalf("hide work: %s", g)
	}
	if g := got(models.ListQuery{Unlabeled: true, Labels: []string{"personal"}, ExcludeLabels: []string{"work"}}); g != "[C B] 2" {
		t.Fatalf("hide work with show filter: %s", g)
	}
	// Conversation view must not answer from the unfiltered folder counters.
	p, err := s.ListMessages(models.ListQuery{FolderID: inbox.ID, ExcludeLabels: []string{"work", "personal"}}, ListOptions{Conversations: true})
	if err != nil || p.Total != 1 || len(p.Messages) != 1 {
		t.Fatalf("conversations hiding both: total %d, %d rows, %v", p.Total, len(p.Messages), err)
	}
}
