package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

var benchWords = strings.Fields("invoice payment meeting project roadmap lunch report weekly update security alert travel booking order shipped review")

func benchMessages(n, offset int) []mail.Message {
	out := make([]mail.Message, 0, n)
	for j := 0; j < n; j++ {
		i := offset + j
		subj := fmt.Sprintf("%s %s %d", benchWords[i%len(benchWords)], benchWords[(i/7)%len(benchWords)], i/3)
		m := mail.Message{RemoteID: fmt.Sprint(i), ThreadID: fmt.Sprint("t", i/3), Subject: subj, Snippet: subj + " details",
			From: models.Address{Name: fmt.Sprintf("Sender %d", i%300), Email: fmt.Sprintf("s%d@example.com", i%300)},
			To:   []models.Address{{Name: "Me", Email: "me@example.com"}},
			Date: time.Now().Add(-time.Duration(i) * time.Minute), FolderRemoteIDs: []string{"INBOX", "ALL"}, Unread: i%4 == 0}
		if i%3 == 0 {
			m.Labels = []string{[]string{"Work", "Finance", "Travel"}[i%9/3]}
		}
		out = append(out, m)
	}
	return out
}

var (
	bigOnce  sync.Once
	bigStore *Store
)

// bigDB is a shared 50k-message mailbox (built once per test binary run).
func bigDB(b *testing.B) *Store {
	bigOnce.Do(func() {
		s, err := Open(filepath.Join(b.TempDir(), "big.db"))
		if err != nil {
			b.Fatal(err)
		}
		s.InsertAccount(models.Account{ID: "a", Provider: "gmail", Email: "me@example.com"})
		s.ReplaceFolders("a", []mail.Folder{{RemoteID: "INBOX", Kind: models.FolderInbox, Name: "Inbox", Total: 60000},
			{RemoteID: "ALL", Kind: models.FolderArchive, Name: "All"}, {RemoteID: "TRASH", Kind: models.FolderTrash, Name: "Trash"}})
		ids := map[string]string{"work": "work", "finance": "finance", "travel": "travel"}
		for off := 0; off < 50000; off += 5000 {
			if _, err := s.ApplySync("a", mail.SyncResult{Upserts: benchMessages(5000, off)}, ids); err != nil {
				b.Fatal(err)
			}
		}
		bigStore = s
	})
	return bigStore
}

func BenchmarkApplySync5k(b *testing.B) {
	msgs := benchMessages(5000, 0)
	ids := map[string]string{"work": "work", "finance": "finance", "travel": "travel"}
	for b.Loop() {
		b.StopTimer()
		s, _ := Open(filepath.Join(b.TempDir(), "s.db"))
		s.InsertAccount(models.Account{ID: "a", Provider: "gmail", Email: "me@example.com"})
		s.ReplaceFolders("a", []mail.Folder{{RemoteID: "INBOX", Kind: models.FolderInbox, Name: "Inbox"}, {RemoteID: "ALL", Kind: models.FolderArchive, Name: "All"}})
		b.StartTimer()
		if _, err := s.ApplySync("a", mail.SyncResult{Upserts: msgs}, ids); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		s.Close()
		b.StartTimer()
	}
}

func BenchmarkListConversations50k(b *testing.B) {
	s := bigDB(b)
	inbox, _ := s.FolderByKind("a", models.FolderInbox)
	for b.Loop() {
		p, err := s.ListMessages(models.ListQuery{FolderID: inbox.ID}, ListOptions{Conversations: true})
		if err != nil || len(p.Messages) != 50 {
			b.Fatal(err, len(p.Messages))
		}
	}
}

func BenchmarkListConversationsUnread50k(b *testing.B) {
	s := bigDB(b)
	for b.Loop() {
		if _, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox, UnreadOnly: true}, ListOptions{Conversations: true}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMessages50k(b *testing.B) {
	s := bigDB(b)
	for b.Loop() {
		if _, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox}, ListOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLabelCounts50k(b *testing.B) {
	s := bigDB(b)
	for b.Loop() {
		c, err := s.LabelCounts("")
		if err != nil || len(c) != 3 {
			b.Fatal(err, c)
		}
	}
}

func BenchmarkSearch50k(b *testing.B) {
	s := bigDB(b)
	for b.Loop() {
		for _, q := range []string{"invo", "roadmap lunch", "invioce", "from:sender is:unread"} {
			if _, err := s.Search(models.SearchQuery{Query: q}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
