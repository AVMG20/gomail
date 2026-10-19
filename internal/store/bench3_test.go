package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func TestTimingSQL(t *testing.T) {
	s, _ := Open(t.TempDir() + "/bench.db")
	defer s.Close()
	s.InsertAccount(models.Account{ID: "a", Provider: "gmail", Email: "a@x"})
	s.ReplaceFolders("a", []mail.Folder{{RemoteID: "INBOX", Kind: models.FolderInbox, Name: "Inbox"}})
	words := strings.Fields("invoice payment meeting project roadmap lunch report weekly update security alert travel booking order shipped review")
	for round := 0; round < 10; round++ {
		var batch []mail.Message
		for j := 0; j < 5000; j++ {
			i := round*5000 + j
			subj := fmt.Sprintf("%s %s %d", words[i%len(words)], words[(i/7)%len(words)], i)
			batch = append(batch, mail.Message{RemoteID: fmt.Sprint(i), Subject: subj, Snippet: subj + " details",
				From: models.Address{Name: fmt.Sprintf("Sender %d", i%300), Email: fmt.Sprintf("s%d@example.com", i%300)},
				Date: time.Now().Add(-time.Duration(i) * time.Minute), FolderRemoteIDs: []string{"INBOX"}, Unread: i%3 == 0})
		}
		s.ApplySync("a", mail.SyncResult{Upserts: batch}, nil)
	}
	for _, q := range []string{
		`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '"invo"*'`,
		`SELECT count(*) FROM (SELECT rowid, rank FROM messages_fts WHERE messages_fts MATCH '"invo"*' ORDER BY rank LIMIT 20)`,
		`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'subject : "invo"*'`,
		`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '"sender" "12"*'`,
		`SELECT count(*) FROM (SELECT rowid, rank FROM messages_fts WHERE messages_fts MATCH '"sender" "12"*' ORDER BY rank LIMIT 20)`,
		`SELECT count(*) FROM messages m WHERE EXISTS (SELECT 1 FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE mf.message_id = m.id AND f.kind IN ('inbox'))`,
		`SELECT count(*) FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE f.kind = 'inbox'`,
		`SELECT count(*) FROM messages m WHERE m.id IN (SELECT mf.message_id FROM message_folders mf WHERE mf.folder_id IN (SELECT id FROM folders WHERE kind = 'inbox'))`,
		`SELECT count(*) FROM messages m WHERE m.unread = 1 AND m.id IN (SELECT mf.message_id FROM message_folders mf WHERE mf.folder_id IN (SELECT id FROM folders WHERE kind = 'inbox'))`,
		`SELECT count(*) FROM (SELECT m.id FROM messages m WHERE EXISTS (SELECT 1 FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE mf.message_id = m.id AND f.kind IN ('inbox')) ORDER BY m.date DESC, m.id DESC LIMIT 51)`,
		`SELECT count(*) FROM messages`,
	} {
		st := time.Now()
		var n int
		err := s.db.QueryRow(q).Scan(&n)
		t.Logf("%v n=%d err=%v :: %.90s", time.Since(st), n, err, q)
	}
}
