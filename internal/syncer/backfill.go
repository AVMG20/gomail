package syncer

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/safe"
)

// Backfill tuning: one small page per step keeps write transactions short
// and leaves the database free for the UI.
var (
	backfillEvery = 3 * time.Second
	backfillDelay = 30 * time.Second // after start-up, so it never competes with first paint
	backfillPage  = 100
)

type backfillState struct {
	mu         sync.Mutex
	next       int                  // round-robin cursor over candidates
	lastNotify map[string]time.Time // per account, throttles messages:changed
	failures   map[string]int       // per folder id: consecutive failed pages
	retryAt    map[string]time.Time // per folder id: skipped until then
}

// backfillRetry is the pause after n consecutive failures of one folder.
func backfillRetry(n int) time.Duration {
	d := time.Minute << min(n-1, 6) // 1 min doubling, capped at ~1 h
	return min(d, time.Hour)
}

func (b *backfillState) failed(folderID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures == nil {
		b.failures, b.retryAt = map[string]int{}, map[string]time.Time{}
	}
	b.failures[folderID]++
	b.retryAt[folderID] = time.Now().Add(backfillRetry(b.failures[folderID]))
}

func (b *backfillState) succeeded(folderID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, folderID)
	delete(b.retryAt, folderID)
}

func (b *backfillState) waiting(folderID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.retryAt[folderID])
}

// backfillFolders are the folders worth backfilling: Gmail's All Mail
// covers everything; elsewhere every real folder except trash, spam and
// drafts.
func backfillFolders(acct models.Account, folders []models.Folder) []models.Folder {
	var out []models.Folder
	if acct.Provider == models.ProviderGmail {
		for _, f := range folders {
			if f.Kind == models.FolderArchive {
				return []models.Folder{f}
			}
		}
	}
	for _, f := range folders {
		switch f.Kind {
		case models.FolderInbox, models.FolderArchive, models.FolderSent, models.FolderCustom:
			out = append(out, f)
		}
	}
	return out
}

func (e *Engine) startBackfill() {
	if e.cfg.Backfill == nil {
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer safe.Recover("backfill")
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(backfillDelay):
		}
		tick := time.NewTicker(backfillEvery)
		defer tick.Stop()
		for {
			e.backfillStep(e.ctx)
			select {
			case <-e.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// backfillStep fetches one page of older mail for the next folder that
// hasn't reached the configured depth. It reports whether it did work.
func (e *Engine) backfillStep(ctx context.Context) bool {
	depth, paused := e.cfg.Backfill()
	if paused || e.Offline() {
		return false
	}
	accts, err := e.cfg.Store.Accounts()
	if err != nil {
		return false
	}
	cutoff := time.Time{}
	if depth > 0 {
		cutoff = time.Now().AddDate(0, 0, -depth)
	}
	type cand struct {
		acct   models.Account
		folder models.Folder
		oldest time.Time
	}
	var cands []cand
	for _, a := range accts {
		if a.NeedsReauth || a.LastSyncAt == nil || e.Syncing(a.ID) {
			continue
		}
		folders, err := e.cfg.Store.Folders(a.ID)
		if err != nil {
			continue
		}
		state, _ := e.cfg.Store.SyncState(a.ID)
		for _, f := range backfillFolders(a, folders) {
			if state["olderdone:"+f.ID] == "1" || f.Total == 0 || e.backfill.waiting(f.ID) {
				continue
			}
			oldest, err := e.cfg.Store.OlderCursor(a.ID, f.ID)
			if err != nil || oldest.IsZero() || (!cutoff.IsZero() && oldest.Before(cutoff)) {
				continue
			}
			if n, err := e.cfg.Store.LocalFolderCount(f.ID); err == nil && n >= f.Total {
				continue
			}
			cands = append(cands, cand{a, f, oldest})
		}
	}
	if len(cands) == 0 {
		return false
	}
	e.backfill.mu.Lock()
	c := cands[e.backfill.next%len(cands)]
	e.backfill.next++
	e.backfill.mu.Unlock()

	p, err := e.cfg.Provider(c.acct.ID)
	if err != nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	msgs, err := p.ListOlder(pctx, mail.Folder{RemoteID: c.folder.RemoteID, Name: c.folder.Name, Kind: c.folder.Kind}, c.oldest, backfillPage)
	if err != nil {
		if IsNetworkError(err) {
			e.setOnline(false)
		} else {
			// Don't retry a failing folder every few seconds.
			e.backfill.failed(c.folder.ID)
		}
		slog.Debug("backfill page failed", "account", c.acct.ID, "folder", c.folder.Name, "err", err)
		return false
	}
	e.backfill.succeeded(c.folder.ID)
	if len(msgs) == 0 {
		_ = e.cfg.Store.SetOlderDone(c.acct.ID, c.folder.ID, true)
		return true
	}
	if _, err := e.ingest(c.acct.ID, msgs, true); err != nil {
		slog.Warn("backfill store failed", "account", c.acct.ID, "err", err)
		return false
	}
	_ = e.cfg.Store.AdvanceOlderCursor(c.acct.ID, c.folder.ID, messageDates(msgs))
	oldest := c.oldest
	for _, m := range msgs {
		if m.Date.Before(oldest) {
			oldest = m.Date
		}
	}
	days := int(time.Since(oldest).Hours() / 24)
	e.cfg.Emit.Emit(models.EventSyncProgress, models.SyncProgress{AccountID: c.acct.ID, Phase: "backfill", Done: min(days, max(depth, days)), Total: depth})
	e.backfill.mu.Lock()
	if e.backfill.lastNotify == nil {
		e.backfill.lastNotify = map[string]time.Time{}
	}
	notify := time.Since(e.backfill.lastNotify[c.acct.ID]) > 10*time.Second
	if notify {
		e.backfill.lastNotify[c.acct.ID] = time.Now()
	}
	e.backfill.mu.Unlock()
	if notify {
		e.cfg.Emit.Emit(models.EventMessagesChanged, models.AccountEvent{AccountID: c.acct.ID})
	}
	return true
}

func messageDates(msgs []mail.Message) []time.Time {
	out := make([]time.Time, len(msgs))
	for i, m := range msgs {
		out[i] = m.Date
	}
	return out
}

// BackfillStep runs one backfill step now (tests).
func (e *Engine) BackfillStep(ctx context.Context) bool { return e.backfillStep(ctx) }
