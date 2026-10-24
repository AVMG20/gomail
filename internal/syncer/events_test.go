package syncer

import (
	"context"
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// An action announces changed mail once per account, and nothing at account
// level: the UI refreshes its counts from messages:changed.
func TestActionsEmitOnlyMessagesChanged(t *testing.T) {
	te := newTestEnv(t)
	if err := te.e.SyncNow(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	var unread, plain []models.MessageSummary // unread, and not starred
	for _, m := range te.inbox(t, demo.GmailAccountID, true) {
		if !m.Starred {
			plain = append(plain, m)
		}
		unread = append(unread, m)
	}
	if len(unread) == 0 || len(plain) == 0 {
		t.Fatal("not enough mail")
	}
	accountsBefore := te.rec.count(models.EventAccountsChanged)
	messagesBefore := te.rec.count(models.EventMessagesChanged)
	if err := te.e.MarkRead([]string{unread[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := te.e.SetStarred([]string{plain[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := te.e.MoveToKind([]string{plain[0].ID}, models.FolderArchive); err != nil {
		t.Fatal(err)
	}
	if n := te.rec.count(models.EventMessagesChanged) - messagesBefore; n != 3 {
		t.Fatalf("messages:changed %d times for 3 actions", n)
	}
	if ev, ok := te.rec.last(models.EventMessagesChanged).(models.AccountEvent); !ok || ev.AccountID != demo.GmailAccountID {
		t.Fatalf("messages:changed for %+v", te.rec.last(models.EventMessagesChanged))
	}
	if n := te.rec.count(models.EventAccountsChanged) - accountsBefore; n != 0 {
		t.Fatalf("accounts:changed %d times after actions", n)
	}
}
