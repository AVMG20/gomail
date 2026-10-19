package store

import (
	"database/sql"
	"path/filepath"
	"testing"
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
