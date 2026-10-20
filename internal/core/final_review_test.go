package core

import (
	"context"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// A save that arrives while a coalesced flush is still in flight (and that
// flush recreates a draft deleted elsewhere) must land on the recreated
// draft, after the earlier content: one draft, the latest text.
func TestOverlappingDraftFlushesStayOrdered(t *testing.T) {
	old := draftCoalesce
	draftCoalesce = 100 * time.Millisecond
	defer func() { draftCoalesce = old }()
	s, probes := newProbeEnv(t)
	acct := demo.GmailAccountID
	h, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Overlap v1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	dh, _ := parseDraftHandle(h)
	if err := probes[acct].Provider.DeleteDraft(context.Background(), dh.remoteID); err != nil { // deleted on another device
		t.Fatal(err)
	}
	p := probes[acct]
	p.mu.Lock()
	p.gate, p.started = make(chan struct{}), make(chan struct{}, 1)
	gate := p.gate
	p.mu.Unlock()
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Overlap v2"}, h); err != nil { // coalesced
		t.Fatal(err)
	}
	<-p.started // the first flush is in flight (and will recreate the draft)
	p.mu.Lock()
	p.gate = nil
	p.mu.Unlock()
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Overlap v3"}, h); err != nil { // coalesced again
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the second flush is due while the first is still running
	close(gate)
	time.Sleep(300 * time.Millisecond)
	if n := draftsWith(t, s, acct, "Overlap"); n != 1 {
		t.Fatalf("%d drafts, want 1", n)
	}
	if n := draftsWith(t, s, acct, "Overlap v3"); n != 1 {
		t.Fatal("the latest save was lost")
	}
}

// A refreshed sign-in is stored even when the account lookup that guards
// against removed accounts fails for another reason (a busy or closing
// database): only a removed account skips the save.
func TestRefreshedTokenSavedWhenAccountLookupFails(t *testing.T) {
	e := newRealEnv(t)
	s := e.s
	g, err := s.AddAccount(models.ProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.LoadToken(s.secrets, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	tok.Expiry = time.Now().Add(-time.Hour) // force a refresh
	if err := auth.SaveToken(s.secrets, g.ID, tok); err != nil {
		t.Fatal(err)
	}
	s.forgetClients(g.ID)
	ts, err := s.tokenSource(g)
	if err != nil {
		t.Fatal(err)
	}
	db := s.store.DB()
	if _, err := db.Exec(`ALTER TABLE accounts RENAME TO accounts_hidden`); err != nil {
		t.Fatal(err)
	}
	access, err := ts.AccessToken(context.Background())
	if _, rerr := db.Exec(`ALTER TABLE accounts_hidden RENAME TO accounts`); rerr != nil {
		t.Fatal(rerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	saved, err := auth.LoadToken(s.secrets, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != access {
		t.Fatal("the refreshed token was not stored")
	}
}
