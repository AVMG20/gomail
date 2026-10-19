package core

import (
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/secrets"
)

// Demo providers generate fresh mail on every launch, so a restart must not
// keep rows that point at messages the new provider instance doesn't have.
func TestDemoDataResetsOnRestart(t *testing.T) {
	dir := t.TempDir()
	open := func() *Service {
		s, err := New(Options{DataDir: dir, Demo: true, UI: &fakeUI{}, Secrets: secrets.NewMemory(),
			ProviderFactory: func(acct models.Account) (provider.Provider, error) {
				d := demo.New(acct)
				d.Latency, d.NewMailChance = 0, 0
				return d, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	s := open()
	if err := s.SyncNow(""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpdateSignature(demo.GmailAccountID, "<p>Cheers</p>"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox})
	if err != nil || len(page.Messages) == 0 {
		t.Fatalf("first run: %d messages, err %v", len(page.Messages), err)
	}
	s.Close()

	s = open()
	defer s.Close()
	page, err = s.ListMessages(models.ListQuery{AccountID: demo.GmailAccountID, FolderKind: models.FolderInbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("stale demo messages survived a restart: %d", len(page.Messages))
	}
	a, err := s.store.Account(demo.GmailAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Signature != "<p>Cheers</p>" {
		t.Fatalf("signature not kept: %q", a.Signature)
	}
}
