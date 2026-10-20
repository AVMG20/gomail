package syncer

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

// outlookRound prepares an Outlook-style delta round for message m: the
// server reports it removed from the Inbox and present in the Archive.
func outlookRound(t *testing.T, te *testEnv, m models.MessageSummary, unread bool) ([]mail.Folder, mail.SyncResult, models.Folder, models.Folder) {
	t.Helper()
	acct := demo.OutlookAccountID
	inbox, err := te.st.FolderByKind(acct, models.FolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := te.st.FolderByKind(acct, models.FolderArchive)
	if err != nil {
		t.Fatal(err)
	}
	folders, _, err := te.provs[acct].Folders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := mail.SyncResult{
		Upserts: []mail.Message{{RemoteID: m.RemoteID, ThreadID: m.ThreadID, Subject: m.Subject, From: m.From, Date: m.Date,
			Unread: unread, FolderRemoteIDs: []string{archive.RemoteID}}},
		Removed: map[string][]string{inbox.RemoteID: {m.RemoteID}},
	}
	return folders, res, inbox, archive
}

func labeledMessage(t *testing.T, te *testEnv) models.MessageSummary {
	t.Helper()
	if err := te.e.SyncNow(context.Background(), demo.OutlookAccountID); err != nil {
		t.Fatal(err)
	}
	m := te.inbox(t, demo.OutlookAccountID, true)[0]
	if err := te.st.AddLabels([]string{m.ID}, "work"); err != nil {
		t.Fatal(err)
	}
	return m
}

// A message moved on the server (Inbox -> Archive) while only its read
// state changed locally must follow the server's move. Keeping the local
// folders and then applying the Inbox removal left it in no folder, so it
// was deleted locally, with its labels.
func TestServerMoveDuringLocalReadChange(t *testing.T) {
	te := newTestEnv(t)
	m := labeledMessage(t, te)
	folders, res, inbox, archive := outlookRound(t, te, m, true)

	started := time.Now()
	if _, err := te.st.SetUnread([]string{m.ID}, false); err != nil {
		t.Fatal(err)
	}
	done := te.e.local.begin(changeRead, []string{m.ID}) // mark-read push in flight
	defer done()
	if _, err := te.e.applyFetched(demo.OutlookAccountID, folders, res, nil, started); err != nil {
		t.Fatal(err)
	}
	sums, err := te.st.Summaries([]string{m.ID})
	if err != nil || len(sums) != 1 {
		t.Fatalf("message deleted locally after a server-side move: %v %v", sums, err)
	}
	got := sums[0]
	if got.Unread {
		t.Error("in-flight read change overwritten")
	}
	if !slices.Contains(got.FolderIDs, archive.ID) || slices.Contains(got.FolderIDs, inbox.ID) {
		t.Errorf("folders %v, want the archive (%s) only", got.FolderIDs, archive.ID)
	}
	if !slices.Contains(got.Labels, "work") {
		t.Errorf("labels lost: %v", got.Labels)
	}
}

// Moving a message back locally (Archive -> Inbox) while a sync reports the
// earlier archive must not apply that Inbox removal on top of the local move.
func TestLocalMoveKeepsFolderAgainstStaleRemoval(t *testing.T) {
	te := newTestEnv(t)
	m := labeledMessage(t, te)
	folders, res, inbox, _ := outlookRound(t, te, m, true)

	started := time.Now()
	done := te.e.local.begin(changeFolders, []string{m.ID}) // move back to the Inbox in flight
	defer done()
	if _, err := te.e.applyFetched(demo.OutlookAccountID, folders, res, nil, started); err != nil {
		t.Fatal(err)
	}
	sums, err := te.st.Summaries([]string{m.ID})
	if err != nil || len(sums) != 1 {
		t.Fatalf("message deleted locally: %v %v", sums, err)
	}
	if !slices.Equal(sums[0].FolderIDs, []string{inbox.ID}) {
		t.Fatalf("folders %v, want the inbox (%s)", sums[0].FolderIDs, inbox.ID)
	}
}

// Marking a whole folder read protects the read state from a sync that
// runs while the server is still working through it.
func TestMarkFolderReadIsProtectedWhileInFlight(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), demo.GmailAccountID); err != nil {
		t.Fatal(err)
	}
	m := te.inbox(t, demo.GmailAccountID, true)[0]
	inbox, _ := te.st.FolderByKind(demo.GmailAccountID, models.FolderInbox)
	gate := make(chan struct{})
	te.provs[demo.GmailAccountID].markFolderReadHook = func() { <-gate }
	errc := make(chan error, 1)
	go func() { errc <- te.e.MarkFolderRead(inbox.ID) }()
	deadline := time.Now().Add(5 * time.Second)
	for !te.e.local.fresh(m.ID, changeRead, time.Now().Add(-time.Hour)) {
		if time.Now().After(deadline) {
			close(gate)
			t.Fatal("folder read never marked as a local change")
		}
		time.Sleep(time.Millisecond)
	}
	ups := []mail.Message{{RemoteID: m.RemoteID, Unread: true, FolderRemoteIDs: []string{"INBOX"}}}
	te.e.keepLocalChanges(demo.GmailAccountID, nil, ups, time.Now())
	close(gate)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if ups[0].Unread {
		t.Fatal("sync during Mark folder as read would mark the message unread again")
	}
}

// A full listing of a folder fetched before a local move into that folder
// doesn't list the message; it must not take the message out of it again
// (which left it in no folder, so it was deleted with its labels).
func TestCompleteListingKeepsLocalMoveIn(t *testing.T) {
	te := newTestEnv(t)
	m := labeledMessage(t, te)
	folders, _, _, archive := outlookRound(t, te, m, true)

	started := time.Now()
	done := te.e.local.begin(changeFolders, []string{m.ID})
	if err := te.st.SetMessageFolders(map[string][]string{m.ID: {archive.ID}}); err != nil {
		t.Fatal(err)
	}
	done()
	res := mail.SyncResult{Complete: map[string]time.Time{archive.RemoteID: {}}}
	if _, err := te.e.applyFetched(demo.OutlookAccountID, folders, res, nil, started); err != nil {
		t.Fatal(err)
	}
	sums, err := te.st.Summaries([]string{m.ID})
	if err != nil || len(sums) != 1 {
		t.Fatalf("locally moved message deleted by a stale complete listing: %v %v", sums, err)
	}
	if !slices.Equal(sums[0].FolderIDs, []string{archive.ID}) || !slices.Contains(sums[0].Labels, "work") {
		t.Fatalf("got folders %v labels %v", sums[0].FolderIDs, sums[0].Labels)
	}
}

// A star change in flight keeps the star but not the read state or the
// folders: those follow the server.
func TestStarChangeKeepsOnlyTheStar(t *testing.T) {
	te := newTestEnv(t)
	m := labeledMessage(t, te)
	folders, res, _, archive := outlookRound(t, te, m, false)
	res.Upserts[0].Starred = !m.Starred

	started := time.Now()
	if _, err := te.st.SetStarred([]string{m.ID}, m.Starred); err != nil {
		t.Fatal(err)
	}
	done := te.e.local.begin(changeStar, []string{m.ID})
	defer done()
	if _, err := te.e.applyFetched(demo.OutlookAccountID, folders, res, nil, started); err != nil {
		t.Fatal(err)
	}
	sums, _ := te.st.Summaries([]string{m.ID})
	if len(sums) != 1 {
		t.Fatal("message deleted")
	}
	if sums[0].Starred != m.Starred || sums[0].Unread || !slices.Equal(sums[0].FolderIDs, []string{archive.ID}) {
		t.Fatalf("got starred=%v unread=%v folders=%v", sums[0].Starred, sums[0].Unread, sums[0].FolderIDs)
	}
}
