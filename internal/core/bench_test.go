package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/mail"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
)

// BenchmarkInitialSync5k measures the first sync of a 5,000-message
// account end to end (provider -> store, labels, junk, vocabulary, FTS).
func BenchmarkInitialSync5k(b *testing.B) {
	acct := demo.Accounts()[0]
	for b.Loop() {
		b.StopTimer()
		d := demo.NewSized(acct, 5000)
		d.Latency, d.NewMailChance = 0, 0
		s, err := New(Options{DataDir: b.TempDir(), Demo: true, UI: &fakeUI{}, Secrets: secrets.NewMemory(),
			ProviderFactory: func(a models.Account) (provider.Provider, error) {
				if a.ID == acct.ID {
					return d, nil
				}
				o := demo.New(a)
				o.Latency = 0
				return o, nil
			}})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := s.SyncNow(acct.ID); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		page, _ := s.ListMessages(models.ListQuery{AccountID: acct.ID})
		if page.Total < 1000 {
			b.Fatalf("only %d conversations", page.Total)
		}
		s.Close()
		b.StartTimer()
	}
}

// BenchmarkListAndCounts measures the hot paths after a sync.
func BenchmarkListAndCounts(b *testing.B) {
	acct := demo.Accounts()[0]
	d := demo.NewSized(acct, 5000)
	d.Latency = 0
	s, err := New(Options{DataDir: b.TempDir(), Demo: true, UI: &fakeUI{}, Secrets: secrets.NewMemory(),
		ProviderFactory: func(a models.Account) (provider.Provider, error) { return d, nil }})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err := s.SyncNow(acct.ID); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox}); err != nil {
			b.Fatal(err)
		}
		if _, err := s.LabelCounts(""); err != nil {
			b.Fatal(err)
		}
		s.store.SetUnread(nil, false) // no-op write: defeats the count cache
	}
}

// BenchmarkColdStart50k measures opening an existing 50k-message database
// and answering the first-paint calls (accounts, folders, label counts and
// the first conversation page). Seeding happens once outside the timer.
func BenchmarkColdStart50k(b *testing.B) {
	dir := b.TempDir()
	opts := Options{DataDir: dir, UI: &fakeUI{}, Secrets: secrets.NewMemory(),
		ProviderFactory: func(models.Account) (provider.Provider, error) { return nil, errors.New("offline") }}
	s, err := New(opts)
	if err != nil {
		b.Fatal(err)
	}
	s.store.InsertAccount(models.Account{ID: "a", Provider: models.ProviderGmail, Email: "me@example.com"})
	s.store.ReplaceFolders("a", []mail.Folder{{RemoteID: "INBOX", Name: "Inbox", Kind: models.FolderInbox, Total: 50000},
		{RemoteID: "ALL", Name: "All Mail", Kind: models.FolderArchive}})
	ids := map[string]string{"work": "work", "finance": "finance"}
	for off := 0; off < 50000; off += 2500 {
		var batch []mail.Message
		for i := off; i < off+2500; i++ {
			m := mail.Message{RemoteID: fmt.Sprint(i), ThreadID: fmt.Sprint("t", i/3), Subject: fmt.Sprintf("Subject %d about things", i/3),
				Snippet: "snippet text", From: models.Address{Name: fmt.Sprintf("Sender %d", i%300), Email: fmt.Sprintf("s%d@example.com", i%300)},
				Date: time.Now().Add(-time.Duration(i) * time.Minute), FolderRemoteIDs: []string{"INBOX", "ALL"}, Unread: i%4 == 0}
			if i%3 == 0 {
				m.Labels = []string{"Work"}
			}
			batch = append(batch, m)
		}
		if _, err := s.store.ApplySync("a", mail.SyncResult{Upserts: batch}, ids); err != nil {
			b.Fatal(err)
		}
	}
	s.Close()
	for b.Loop() {
		start := time.Now()
		s, err := New(opts)
		if err != nil {
			b.Fatal(err)
		}
		opened := time.Since(start)
		s.Accounts()
		s.Folders("")
		s.LabelCounts("")
		p, err := s.ListMessages(models.ListQuery{FolderKind: models.FolderInbox})
		if err != nil || p.Total != 16667 {
			b.Fatal(err, p.Total)
		}
		b.ReportMetric(float64(opened.Microseconds())/1000, "open-ms")
		b.ReportMetric(float64(time.Since(start).Microseconds())/1000, "first-page-ms")
		s.Close()
	}
}
