package syncer

import (
	"context"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// While the server empties Trash, the emptied messages stay hidden (label
// counts, label views, search) and server state fetched before the empty
// doesn't put them back in Trash.
func TestEmptyTrashKeepsMessagesHidden(t *testing.T) {
	te := newTestEnv(t)
	ctx := context.Background()
	acct := demo.OutlookAccountID
	if err := te.e.SyncNow(ctx, acct); err != nil {
		t.Fatal(err)
	}
	trash, err := te.st.FolderByKind(acct, models.FolderTrash)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := te.st.FolderMessageIDs(trash.ID, false)
	if len(ids) == 0 {
		t.Fatal("demo trash is empty")
	}
	if err := te.st.AddLabels(ids, "kept"); err != nil {
		t.Fatal(err)
	}
	// Flagged unread mail in Trash: the Flagged view lists it until it's emptied.
	if _, err := te.st.SetStarred(ids, true); err != nil {
		t.Fatal(err)
	}
	if _, err := te.st.SetUnread(ids, true); err != nil {
		t.Fatal(err)
	}
	starredCounts := func() (unread, total int) {
		f, err := te.st.FolderByKind(acct, models.FolderStarred)
		if err != nil {
			t.Fatal(err)
		}
		return f.Unread, f.Total
	}
	if u, n := starredCounts(); u < len(ids) || n < len(ids) {
		t.Fatalf("starred counts %d/%d before emptying %d flagged", u, n, len(ids))
	}
	starredBefore, _ := starredCounts()
	sums, _ := te.st.Summaries(ids)
	var stale []mail.Message
	for _, m := range sums {
		stale = append(stale, mail.Message{RemoteID: m.RemoteID, ThreadID: m.ThreadID, Subject: m.Subject, From: m.From,
			Date: m.Date, FolderRemoteIDs: []string{trash.RemoteID}})
	}
	keptCount := func() int {
		counts, err := te.st.LabelCounts(acct)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range counts {
			if c.LabelID == "kept" {
				return c.Total
			}
		}
		return 0
	}
	if n := keptCount(); n != 0 {
		t.Fatalf("trashed messages counted: %d", n)
	}
	fetched := time.Now().Add(-time.Second) // a sync that started before the empty
	checked := false
	te.provs[acct].onEmpty = func() {
		checked = true
		if n := keptCount(); n != 0 {
			t.Errorf("emptied messages counted while the server empties: %d", n)
		}
		if u, _ := starredCounts(); u != starredBefore-len(ids) {
			t.Errorf("emptied flagged mail still counted in Flagged: %d unread, want %d", u, starredBefore-len(ids))
		}
		page, err := te.st.ListMessages(models.ListQuery{Labels: []string{"kept"}}, store.ListOptions{})
		if err != nil || page.Total != 0 {
			t.Errorf("emptied messages in the label view: %d %v", page.Total, err)
		}
		res, err := te.st.Search(models.SearchQuery{Query: sums[0].Subject, AccountID: acct})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Message.ID == sums[0].ID {
				t.Errorf("emptied message found by search")
			}
		}
		if _, err := te.e.Ingest(acct, stale, fetched); err != nil {
			t.Fatal(err)
		}
		if again, _ := te.st.FolderMessageIDs(trash.ID, false); len(again) != 0 {
			t.Errorf("stale server state put %d messages back in Trash", len(again))
		}
		if u, _ := starredCounts(); u != starredBefore-len(ids) {
			t.Errorf("a sync counted emptied flagged mail again: %d unread", u)
		}
	}
	if err := te.e.EmptyFolder(trash.ID); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("provider not called")
	}
	if left, _ := te.st.Summaries(ids); len(left) != 0 {
		t.Fatalf("%d emptied messages still cached", len(left))
	}
}
