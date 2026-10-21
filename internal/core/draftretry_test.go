package core

import (
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

func (p *draftProbe) failSaves(err error) {
	p.mu.Lock()
	p.saveErr = err
	p.mu.Unlock()
}

func (p *draftProbe) lastSaved() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.saved) == 0 {
		return ""
	}
	return p.saved[len(p.saved)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A coalesced autosave that fails (offline) is kept and retried with
// backoff, instead of being dropped after a log line.
func TestFailedAutosaveIsRetried(t *testing.T) {
	oldC, oldB := draftCoalesce, draftRetryBase
	draftCoalesce, draftRetryBase = 100*time.Millisecond, 50*time.Millisecond
	defer func() { draftCoalesce, draftRetryBase = oldC, oldB }()
	s, probes := newProbeEnv(t)
	acct := demo.GmailAccountID
	p := probes[acct]
	h, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Retry v1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p.failSaves(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")})
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Retry v2"}, h); err != nil { // coalesced
		t.Fatal(err)
	}
	waitFor(t, "the failed autosave was not retried", func() bool {
		s.drafts.mu.Lock()
		defer s.drafts.mu.Unlock()
		e := s.drafts.entries[h]
		return e != nil && e.failures >= 2 && e.pending != nil
	})
	p.failSaves(nil)
	waitFor(t, "the autosave never reached the server", func() bool { return p.lastSaved() == "Retry v2" })
	if n := draftsWith(t, s, acct, "Retry"); n != 1 {
		t.Fatalf("%d drafts, want 1", n)
	}
}

// When the network comes back, a failed autosave is saved right away; a
// newer save made meanwhile is never overwritten by the older content.
func TestFailedAutosaveRetriesWhenOnline(t *testing.T) {
	oldC, oldB := draftCoalesce, draftRetryBase
	draftCoalesce, draftRetryBase = 100*time.Millisecond, time.Hour
	defer func() { draftCoalesce, draftRetryBase = oldC, oldB }()
	s, probes := newProbeEnv(t)
	acct := demo.OutlookAccountID
	p := probes[acct]
	h, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Online v1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	failing := func() bool {
		s.drafts.mu.Lock()
		defer s.drafts.mu.Unlock()
		e := s.drafts.entries[h]
		return e != nil && e.failures == 1 && e.pending != nil
	}
	p.failSaves(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")})
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Online v2"}, h); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the autosave did not fail", failing)
	p.failSaves(nil)
	notifyingEmitter{s}.Emit(models.EventNetworkStatus, models.NetworkStatus{Online: true})
	waitFor(t, "not saved when the network came back", func() bool { return p.lastSaved() == "Online v2" })

	// Fails again; then a direct save of newer content succeeds.
	p.failSaves(errors.New("network is unreachable"))
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Online v3"}, h); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the autosave did not fail", failing)
	p.failSaves(nil)
	time.Sleep(draftCoalesce)
	if _, err := s.SaveDraft(models.ComposeRequest{AccountID: acct, Subject: "Online v4"}, h); err != nil {
		t.Fatal(err)
	}
	notifyingEmitter{s}.Emit(models.EventNetworkStatus, models.NetworkStatus{Online: true})
	time.Sleep(100 * time.Millisecond)
	p.mu.Lock()
	saved := slices.Clone(p.saved)
	p.mu.Unlock()
	if saved[len(saved)-1] != "Online v4" || slices.Contains(saved, "Online v3") {
		t.Fatalf("saves %v: the older content must not land after v4", saved)
	}
}
