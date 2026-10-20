package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/provider"
)

// When a provider applied an action to some messages only, only the
// others are reverted locally.
func TestPartialFailureRevertsOnlyFailed(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	unread := te.inbox(t, demo.GmailAccountID, true)
	if len(unread) < 2 {
		t.Fatal("need two unread messages")
	}
	a, b := unread[0], unread[1]
	te.provs[demo.GmailAccountID].set("read", &provider.PartialError{Failed: []string{b.RemoteID}, Err: errors.New("server said no")})
	if err := te.e.MarkRead([]string{a.ID, b.ID}, true); err == nil {
		t.Fatal("expected an error")
	}
	sums, _ := te.st.Summaries([]string{a.ID, b.ID})
	state := map[string]bool{}
	for _, m := range sums {
		state[m.ID] = m.Unread
	}
	if state[a.ID] || !state[b.ID] {
		t.Fatalf("unread after partial failure: applied=%v failed=%v", state[a.ID], state[b.ID])
	}
}

// A sync whose snapshot predates a local change (push in flight or just
// finished) keeps the local read/star state.
func TestSyncKeepsFreshLocalChanges(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	m := te.inbox(t, demo.GmailAccountID, true)[0]
	if _, err := te.st.SetUnread([]string{m.ID}, false); err != nil {
		t.Fatal(err)
	}
	folders, _, _ := te.provs[demo.GmailAccountID].Folders(context.Background())
	snapshot := func() []mail.Message {
		return []mail.Message{{RemoteID: m.RemoteID, Unread: true, FolderRemoteIDs: []string{"INBOX"}}}
	}
	started := time.Now()
	done := te.e.local.begin([]string{m.ID}) // push in flight
	ups := snapshot()
	te.e.keepLocalChanges(demo.GmailAccountID, folders, ups, started)
	if ups[0].Unread {
		t.Fatal("in-flight local change overwritten by the snapshot")
	}
	done()
	ups = snapshot()
	te.e.keepLocalChanges(demo.GmailAccountID, folders, ups, started)
	if ups[0].Unread {
		t.Fatal("change pushed during the sync overwritten by the snapshot")
	}
	ups = snapshot()
	te.e.keepLocalChanges(demo.GmailAccountID, folders, ups, time.Now().Add(time.Second))
	if !ups[0].Unread {
		t.Fatal("a later sync must apply the server state")
	}
}

// A folder whose older-mail page keeps failing (not a network problem) is
// paused instead of being retried every few seconds.
func TestBackfillBacksOffFailingFolder(t *testing.T) {
	te := newTestEnv(t)
	for _, a := range []string{demo.GmailAccountID, demo.OutlookAccountID} {
		if err := te.e.SyncNow(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		te.provs[a].set("older", errors.New("invalid query"))
	}
	for i := 0; i < 40; i++ {
		te.e.BackfillStep(context.Background())
	}
	calls := 0
	for _, a := range []string{demo.GmailAccountID, demo.OutlookAccountID} {
		f := te.provs[a]
		f.mu.Lock()
		calls += f.calls["older"]
		f.mu.Unlock()
	}
	folders := 0
	for _, a := range []string{demo.GmailAccountID, demo.OutlookAccountID} {
		acct, _ := te.st.Account(a)
		fs, _ := te.st.Folders(a)
		folders += len(backfillFolders(acct, fs))
	}
	if calls == 0 || calls > folders {
		t.Fatalf("%d failing older-mail requests for %d folders", calls, folders)
	}
}
