package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

// TestMigrateFromV2 upgrades a database created by an older build.
func TestMigrateFromV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range migrations[:2] {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	for _, q := range []string{
		`PRAGMA user_version = 2`,
		`INSERT INTO accounts(id, provider, email, created_at) VALUES('a', 'gmail', 'me@x.com', 1)`,
		`INSERT INTO folders(id, account_id, remote_id, name, kind) VALUES('f', 'a', 'INBOX', 'Inbox', 'inbox')`,
		`INSERT INTO messages(id, account_id, remote_id, from_name, from_email, to_json, subject, date, unread, folder_kinds)
			VALUES('m1', 'a', 'r1', 'Ann', 'Ann@x.com', '[{"name":"Bob","email":"bob@x.com"}]', 'hi', 5, 1, 1)`,
		`INSERT INTO message_folders(folder_id, message_id) VALUES('f', 'm1')`,
		`INSERT INTO message_labels(message_id, label_id, source) VALUES('m1', 'work', 'manual')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	counts, err := s.LabelCounts("a")
	if err != nil || len(counts) != 1 || counts[0].Total != 1 || counts[0].Unread != 1 {
		t.Fatalf("counts %+v %v", counts, err)
	}
	cs, err := s.SearchContacts("b", 5, nil)
	if err != nil || len(cs) != 1 || cs[0].Email != "bob@x.com" {
		t.Fatalf("contacts %+v %v", cs, err)
	}
	cs, _ = s.SearchContacts("ann", 5, nil)
	if len(cs) != 1 || cs[0].Email != "ann@x.com" {
		t.Fatalf("contacts %+v", cs)
	}
	if _, err := s.Account("a"); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateFromEveryVersion upgrades databases created at every earlier
// schema version and checks the maintained counters against a recount.
func TestMigrateFromEveryVersion(t *testing.T) {
	for v := 1; v < len(migrations); v++ {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			for i, m := range migrations[:v] {
				if _, err := db.Exec(m); err != nil {
					t.Fatalf("migration %d: %v", i+1, err)
				}
			}
			stmts := []string{
				fmt.Sprintf(`PRAGMA user_version = %d`, v),
				`INSERT INTO accounts(id, provider, email, created_at) VALUES('a', 'gmail', 'me@x.com', 1)`,
				`INSERT INTO folders(id, account_id, remote_id, name, kind) VALUES('fi', 'a', 'INBOX', 'Inbox', 'inbox')`,
				`INSERT INTO folders(id, account_id, remote_id, name, kind) VALUES('ft', 'a', 'TRASH', 'Trash', 'trash')`,
			}
			for i := 0; i < 12; i++ {
				folder, kinds := "fi", 1
				if i%4 == 3 {
					folder, kinds = "ft", 32
				}
				stmts = append(stmts,
					fmt.Sprintf(`INSERT INTO messages(id, account_id, remote_id, thread_id, from_email, subject, date, unread, folder_kinds)
						VALUES('m%d', 'a', 'r%d', 't%d', 'x%d@x.com', 'subject %d', %d, %d, %d)`, i, i, i/3, i, i, 1000+i, i%2, kinds),
					fmt.Sprintf(`INSERT INTO message_folders(folder_id, message_id) VALUES('%s', 'm%d')`, folder, i),
					fmt.Sprintf(`INSERT INTO message_labels(message_id, label_id, source) VALUES('m%d', 'work', 'manual')`, i))
			}
			for _, q := range stmts {
				if _, err := db.Exec(q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			db.Close()
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var version int
			s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
			if version != len(migrations) {
				t.Fatalf("version %d", version)
			}
			// Label counts leave out trash; unread follows the messages.
			counts, err := s.LabelCounts("a")
			if err != nil || len(counts) != 1 || counts[0].Total != 9 || counts[0].Unread != 3 {
				t.Fatalf("label counts %+v %v", counts, err)
			}
			for _, unread := range []bool{false, true} {
				q := models.ListQuery{FolderID: "fi", UnreadOnly: unread}
				fast, ok, err := s.fastConversationCount(q, ListOptions{Conversations: true})
				slow, err2 := s.slowConversationCount(q)
				if err != nil || err2 != nil || !ok || fast != slow {
					t.Fatalf("unread=%v fast=%d slow=%d %v %v", unread, fast, slow, err, err2)
				}
			}
			// The upgraded database keeps working.
			if _, err := s.ApplySync("a", mail.SyncResult{Upserts: []mail.Message{{RemoteID: "new", ThreadID: "t0", Subject: "new",
				Date: time.Now(), FolderRemoteIDs: []string{"INBOX"}, Unread: true}}}, nil); err != nil {
				t.Fatal(err)
			}
			if res, err := s.Search(models.SearchQuery{Query: "subject"}); err != nil || len(res) == 0 {
				t.Fatalf("search after upgrade: %d %v", len(res), err)
			}
			// Calendars work on the upgraded database.
			if a, _ := s.Account("a"); !a.CalendarEnabled || a.CalendarConsent {
				t.Fatalf("calendar defaults after upgrade %+v", a)
			}
			if err := s.ReplaceCalendars("a", []calendar.Calendar{{RemoteID: "c", Name: "C", CanEdit: true}}); err != nil {
				t.Fatal(err)
			}
			row, _ := s.Calendar(CalendarID("a", "c"))
			if _, err := s.ApplyEvents(row, EventChanges{Upserts: []calendar.Event{{RemoteID: "e", Title: "E", Start: time.Now(), End: time.Now().Add(time.Hour), ETag: "x"}}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
