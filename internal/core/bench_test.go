package core

import (
	"testing"

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
