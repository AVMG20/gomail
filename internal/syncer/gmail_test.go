package syncer

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/provider/gmail"
	"github.com/AVMG20/gomail/internal/store"
)

// gmailEnv syncs a real Gmail client against the fake Gmail server.
type gmailEnv struct {
	st   *store.Store
	e    *Engine
	rec  *recorder
	fake *fakes.Gmail
	c    *gmail.Client
}

const gmailAcct = "g1"

func newGmailEnv(t *testing.T) *gmailEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ge := &gmailEnv{st: st, rec: &recorder{}, fake: fakes.NewGmail("me@gmail.example", nil)}
	if err := st.InsertAccount(models.Account{ID: gmailAcct, Provider: models.ProviderGmail, Email: "me@gmail.example", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ge.c = gmail.New(httpx.StaticToken("tok"), ge.fake.URL())
	ge.c.SetSleep(func(context.Context, time.Duration) error { return nil })
	ge.e = New(Config{
		Store:    st,
		Emit:     ge.rec,
		Provider: func(string) (provider.Provider, error) { return ge.c, nil },
		Interval: func(string) time.Duration { return time.Hour },
	})
	t.Cleanup(func() { ge.e.Stop(); st.Close(); ge.fake.Close() })
	return ge
}

// round runs a periodic (or manual) sync round and returns how many HTTP
// requests it made and how many accounts:changed events it emitted.
func (ge *gmailEnv) round(t *testing.T, manual bool) (requests, accountEvents int) {
	t.Helper()
	before, _ := ge.fake.Requests()
	events := ge.rec.count(models.EventAccountsChanged)
	if err := ge.e.syncAccount(context.Background(), gmailAcct, manual); err != nil {
		t.Fatal(err)
	}
	after, _ := ge.fake.Requests()
	return after - before, ge.rec.count(models.EventAccountsChanged) - events
}

func (ge *gmailEnv) inboxUnread(t *testing.T) int {
	t.Helper()
	f, err := ge.st.FolderByKind(gmailAcct, models.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	return f.Unread
}

// A periodic Gmail round that finds no changes costs one request
// (history.list); folders and counts are fetched again when mail changed,
// on manual rounds and every folderRefreshEvery. Mail is fetched in
// batches, and label changes need no fetch at all.
func TestGmailRoundRequests(t *testing.T) {
	ge := newGmailEnv(t)
	ctx := context.Background()
	now := time.Now()
	var unread []string
	for i := range 400 {
		labels := []string{"INBOX"}
		switch i % 4 {
		case 1:
			labels = append(labels, "UNREAD")
		case 2:
			labels = []string{"SENT"}
		case 3:
			labels = nil // archived
		}
		id := ge.fake.Deliver(fakes.GmailMessage{LabelIDs: labels, From: fmt.Sprintf("s%d@example.com", i),
			Subject: fmt.Sprintf("Message %d", i), Date: now.Add(-time.Duration(i) * time.Minute)})
		if i%4 == 1 {
			unread = append(unread, id)
		}
	}
	first, _ := ge.round(t, true)
	if first > 30 {
		t.Fatalf("first sync made %d requests", first)
	}
	if n, _ := ge.round(t, false); n != 1 {
		t.Fatalf("idle round: %d requests", n)
	}
	// Read elsewhere: applied from history, then the counts are refreshed.
	if err := ge.c.SetRead(ctx, unread[:10], true); err != nil {
		t.Fatal(err)
	}
	before := ge.inboxUnread(t)
	if n, _ := ge.round(t, false); n != 3 {
		t.Fatalf("label change round: %d requests (want history + 2 for folders)", n)
	}
	if got := ge.inboxUnread(t); got != before-10 {
		t.Fatalf("inbox unread %d, want %d", got, before-10)
	}
	// New mail: one batch for it, then the counts.
	ge.fake.Deliver(fakes.GmailMessage{LabelIDs: []string{"INBOX", "UNREAD"}, From: "new@example.com", Subject: "New"})
	if n, _ := ge.round(t, false); n != 4 {
		t.Fatalf("new mail round: %d requests", n)
	}
	if got := ge.inboxUnread(t); got != before-9 {
		t.Fatalf("inbox unread %d after new mail, want %d", got, before-9)
	}
	// "Sync now" and a stale folder list refresh the counts.
	if n, _ := ge.round(t, true); n != 3 {
		t.Fatalf("manual idle round: %d requests", n)
	}
	st := ge.e.state(gmailAcct)
	st.foldersAt = time.Now().Add(-folderRefreshEvery)
	if n, _ := ge.round(t, false); n != 3 {
		t.Fatalf("idle round with stale folders: %d requests", n)
	}
	if n, _ := ge.round(t, false); n != 1 {
		t.Fatalf("idle round after the refresh: %d requests", n)
	}
}

// Providers without lazy folders (Outlook: delta per folder) still get
// their folders before every round.
func TestFoldersEveryRoundWithoutLazyFolders(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.OutlookAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := te.e.syncAccount(ctx, acct, false); err != nil {
			t.Fatal(err)
		}
	}
	p := te.provs[acct]
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls["folders"] != 3 {
		t.Fatalf("folders fetched %d times in 3 rounds", p.calls["folders"])
	}
}
