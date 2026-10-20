package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
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

// A server search (or older page) fetched before the user moved a message
// must not move it back when its results are stored after the move; the
// next fetch applies server state again.
func TestIngestKeepsLocalMoves(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.GmailAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	m := te.inbox(t, acct, false)[0]
	trash, err := te.st.FolderByKind(acct, models.FolderTrash)
	if err != nil {
		t.Fatal(err)
	}
	search := func() []mail.Message {
		res, err := te.provs[acct].Search(ctx, mail.SearchQuery{Subject: []string{m.Subject}}, 50)
		if err != nil || len(res) == 0 {
			t.Fatalf("search %v %v", len(res), err)
		}
		return res
	}
	fetched := time.Now()
	stale := search() // still in the inbox on the server
	if err := te.e.MoveToKind([]string{m.ID}, models.FolderTrash); err != nil {
		t.Fatal(err)
	}
	if _, err := te.e.Ingest(acct, stale, fetched); err != nil {
		t.Fatal(err)
	}
	if s, _ := te.st.Summaries([]string{m.ID}); len(s) != 1 || len(s[0].FolderIDs) != 1 || s[0].FolderIDs[0] != trash.ID {
		t.Fatalf("stale search result undid the move: %+v", s)
	}
	// A fetch that started after the move stores what the server says (here:
	// someone moved it back to the inbox in another mail app).
	fetched = time.Now().Add(time.Millisecond)
	fresh := search()
	for i := range fresh {
		fresh[i].FolderRemoteIDs = []string{"INBOX"}
	}
	if _, err := te.e.Ingest(acct, fresh, fetched); err != nil {
		t.Fatal(err)
	}
	if s, _ := te.st.Summaries([]string{m.ID}); len(s) != 1 || len(s[0].FolderIDs) == 0 || s[0].FolderIDs[0] == trash.ID {
		t.Fatalf("later fetch ignored: %+v", s)
	}
}

// Label rules (BeforeAnnounce) run before the UI hears the sync finished,
// so the list never shows new mail that a rule is about to move to Junk.
func TestBeforeAnnounceRunsBeforeSyncFinished(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.GmailAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	var got []string
	finishedBefore := -1
	te.e.cfg.BeforeAnnounce = func(accountID string, ids []string) {
		got = ids
		finishedBefore = te.rec.count(models.EventSyncFinished)
	}
	finished := te.rec.count(models.EventSyncFinished)
	te.provs[acct].Deliver("Build passed", "ci@example.com")
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || finishedBefore != finished {
		t.Fatalf("rules saw %v; sync:finished events before them %d, want %d", got, finishedBefore, finished)
	}
}
