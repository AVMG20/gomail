package syncer

import (
	"context"
	"testing"

	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/models"
)

// The calendar round that follows a mail sync must not hold up the mail
// sync's result: it runs once the account is no longer "syncing" and its
// sync:finished (and new-mail handling) went out.
func TestCalendarSyncRunsAfterMailSyncFinished(t *testing.T) {
	te := newTestEnv(t)
	acct := demo.GmailAccountID
	var ran, finishedFirst, idle bool
	te.e.cfg.Calendar = func(ctx context.Context, accountID string) {
		ran = true
		finishedFirst = te.rec.count(models.EventSyncFinished) > 0
		idle = !te.e.Syncing(accountID)
	}
	if err := te.e.SyncNow(context.Background(), acct); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("calendar sync didn't run")
	}
	if !finishedFirst || !idle {
		t.Fatalf("calendar sync ran inside the mail sync (finished first %v, idle %v)", finishedFirst, idle)
	}
}
