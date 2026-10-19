package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func TestTimingTmp(t *testing.T) {
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
		st := time.Now()
		s.ApplySync("a", mail.SyncResult{Upserts: batch}, nil)
		if round == 9 {
			t.Logf("round %d: %v", round, time.Since(st))
		}
	}
	for _, q := range []string{"invo", "roadmap lunch", "invioce", "from:sender is:unread", "sender 12", "zzzz", "s"} {
		st := time.Now()
		rs, err := s.Search(models.SearchQuery{Query: q})
		t.Logf("%q: %d results %v %v", q, len(rs), time.Since(st), err)
	}
	for _, q := range []models.ListQuery{{FolderKind: "inbox"}, {FolderKind: "inbox", UnreadOnly: true}, {FolderKind: "inbox", Search: "invoice"}, {}} {
		st := time.Now()
		p, _ := s.ListMessages(q)
		t.Logf("list %+v: %d %v", q, p.Total, time.Since(st))
	}
}
