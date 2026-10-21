package syncer

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

func (te *testEnv) setOnline(on bool) {
	te.mu.Lock()
	te.online = on
	te.mu.Unlock()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (te *testEnv) folderIDs(t *testing.T, id string) []string {
	t.Helper()
	sums, err := te.st.Summaries([]string{id})
	if err != nil || len(sums) != 1 {
		t.Fatalf("summaries %v %v", sums, err)
	}
	return sums[0].FolderIDs
}

// Offline, an archive keeps its local change without an error (the UI says
// changes sync when you reconnect) and reaches the server once the network
// is back, in the order the changes were made.
func TestOfflineActionsAreQueued(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.GmailAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	p := te.provs[acct]
	unread := te.inbox(t, acct, true)
	m, m2 := unread[0], unread[1]
	archive, _ := te.st.FolderByKind(acct, models.FolderArchive)
	inbox, _ := te.st.FolderByKind(acct, models.FolderInbox)
	var mu sync.Mutex
	var moved []string
	p.moveHook = func(target mail.Folder) error {
		mu.Lock()
		moved = append(moved, target.Kind)
		mu.Unlock()
		return nil
	}
	movedKinds := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(moved)
	}

	te.setOnline(false)
	p.set("move", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")})
	if err := te.e.MoveToKind([]string{m.ID}, models.FolderArchive); err != nil {
		t.Fatalf("offline archive: %v", err)
	}
	if !te.e.Offline() {
		t.Fatal("a network failure while the probe fails must mark the engine offline")
	}
	if got := te.folderIDs(t, m.ID); !slices.Equal(got, []string{archive.ID}) {
		t.Fatalf("folders %v, want the archive (%s)", got, archive.ID)
	}
	// Further changes while offline aren't even tried; they queue in order.
	reads := p.calls["read"]
	if err := te.e.MarkRead([]string{m.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := te.e.MoveToKind([]string{m.ID}, models.FolderTrash); err != nil {
		t.Fatal(err)
	}
	if p.calls["read"] != reads || len(movedKinds()) != 0 {
		t.Fatal("pushed while offline")
	}
	// A sync whose snapshot predates the changes can't undo them meanwhile.
	if !te.e.local.fresh(m.ID, changeFolders, time.Now()) {
		t.Fatal("queued change not guarded against syncs")
	}

	p.set("move", nil)
	te.setOnline(true)
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "queued moves not replayed", func() bool { return len(movedKinds()) == 2 })
	if got := movedKinds(); !slices.Equal(got, []string{models.FolderArchive, models.FolderTrash}) {
		t.Fatalf("replayed moves %v", got)
	}
	waitUntil(t, "queued read not replayed", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.calls["read"] == reads+1
	})
	te.e.wg.Wait()
	trash, _ := te.st.FolderByKind(acct, models.FolderTrash)
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if got := te.folderIDs(t, m.ID); !slices.Equal(got, []string{trash.ID}) {
		t.Fatalf("after the replay the server has %v, want the trash", got)
	}

	// A queued change the server then refuses is reverted and reported.
	te.setOnline(false)
	p.set("move", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable")})
	if err := te.e.MoveToKind([]string{m2.ID}, models.FolderArchive); err != nil {
		t.Fatal(err)
	}
	p.set("move", &httpx.Error{Service: "Gmail", Status: 400, Code: "invalidArgument", Message: "Invalid label"})
	te.setOnline(true)
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "refused queued change not reverted", func() bool { return slices.Contains(te.folderIDs(t, m2.ID), inbox.ID) })
	waitUntil(t, "refused queued change not reported", func() bool {
		te.rec.mu.Lock()
		defer te.rec.mu.Unlock()
		for i, ev := range te.rec.events {
			if se, ok := te.rec.data[i].(models.SyncEvent); ok && ev == models.EventSyncFinished && strings.Contains(se.Error, "Couldn't move messages") {
				return true
			}
		}
		return false
	})
}

// New mail is announced after OnNewMessages settled (Jev labeled and moved
// it), or after announceWait at the latest.
func TestAnnounceWaitsForNewMailWork(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.GmailAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	settled := make(chan struct{})
	te.e.cfg.OnNewMessages = func(string, []string, bool) <-chan struct{} { return settled }
	before := te.rec.count(models.EventNewMail)
	te.provs[acct].Deliver("Weekly report", "sam@example.com")
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if te.rec.count(models.EventNewMail) != before {
		t.Fatal("announced before the new mail was labeled")
	}
	close(settled)
	waitUntil(t, "not announced after labeling", func() bool { return te.rec.count(models.EventNewMail) == before+1 })

	old := announceWait
	announceWait = 50 * time.Millisecond
	defer func() { announceWait = old }()
	te.e.cfg.OnNewMessages = func(string, []string, bool) <-chan struct{} { return make(chan struct{}) } // never settles
	te.provs[acct].Deliver("Second report", "sam@example.com")
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "a stuck labeler lost the notification", func() bool { return te.rec.count(models.EventNewMail) == before+2 })
}
