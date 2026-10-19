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
			if state["olderdone:"+f.ID] == "1" || f.Total == 0 {
				continue
			}
			oldest, err := e.cfg.Store.OldestInFolder(f.ID)
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
		}
		slog.Debug("backfill page failed", "account", c.acct.ID, "folder", c.folder.Name, "err", err)
		return false
	}
	if len(msgs) == 0 {
		_ = e.cfg.Store.SetOlderDone(c.acct.ID, c.folder.ID, true)
		return true
	}
	if _, err := e.ingest(c.acct.ID, msgs, true); err != nil {
		slog.Warn("backfill store failed", "account", c.acct.ID, "err", err)
		return false
	}
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

// BackfillStep runs one backfill step now (tests).
func (e *Engine) BackfillStep(ctx context.Context) bool { return e.backfillStep(ctx) }
