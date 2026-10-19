// Package syncer runs background synchronisation per account and applies
// user actions optimistically: the local cache is updated (and the UI
// notified) first, then the change is pushed to the provider and reverted
// if that fails.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/store"
)

// Emitter delivers events to the UI.
type Emitter interface {
	Emit(event string, data any)
}

// Config wires the engine to the rest of the app.
type Config struct {
	Store *store.Store
	Emit  Emitter
	// Provider returns the (cached) provider for an account.
	Provider func(accountID string) (provider.Provider, error)
	// ResolveLabels makes sure provider labels exist locally and returns
	// lower-cased provider label name -> local label id.
	ResolveLabels func(accountID string, labels []mail.Label) map[string]string
	// Interval returns the periodic sync interval.
	Interval func() time.Duration
	// OnNewMessages is called after a sync stored new messages.
	OnNewMessages func(accountID string, ids []string, initial bool)
	Limits        provider.Limits
}

type acctState struct {
	mu          sync.Mutex // held while syncing
	syncing     bool
	lastAttempt time.Time
	labels      map[string]bool // provider label names (lower) seen in the last sync
}

// Engine is the sync engine.
type Engine struct {
	cfg Config

	mu       sync.Mutex
	accounts map[string]*acctState
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	wake     chan struct{}
}

// New creates an engine; call Start to begin periodic syncing.
func New(cfg Config) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{cfg: cfg, accounts: map[string]*acctState{}, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
}

func (e *Engine) state(id string) *acctState {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.accounts[id]
	if !ok {
		st = &acctState{}
		e.accounts[id] = st
	}
	return st
}

// Forget drops in-memory state for a removed account.
func (e *Engine) Forget(accountID string) {
	e.mu.Lock()
	delete(e.accounts, accountID)
	e.mu.Unlock()
}

// Syncing reports whether an account is syncing now.
func (e *Engine) Syncing(accountID string) bool {
	st := e.state(accountID)
	e.mu.Lock()
	defer e.mu.Unlock()
	return st.syncing
}

// HasProviderLabel reports whether the account's provider had a label with
// this name at the last sync.
func (e *Engine) HasProviderLabel(accountID, name string) bool {
	st := e.state(accountID)
	e.mu.Lock()
	defer e.mu.Unlock()
	return st.labels[strings.ToLower(name)]
}

// Start begins periodic background syncing (an immediate first round, then
// every Interval).
func (e *Engine) Start() {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		e.syncDue(true)
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-tick.C:
				e.syncDue(false)
			case <-e.wake:
				e.syncDue(false)
			}
		}
	}()
}

// Wake re-evaluates due syncs (e.g. after the interval setting changed).
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Stop cancels running syncs and waits for them (bounded).
func (e *Engine) Stop() {
	e.cancel()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("sync engine did not stop in time")
	}
}

func (e *Engine) syncDue(all bool) {
	accts, err := e.cfg.Store.Accounts()
	if err != nil {
		slog.Error("list accounts", "err", err)
		return
	}
	interval := 5 * time.Minute
	if e.cfg.Interval != nil {
		interval = e.cfg.Interval()
	}
	for _, a := range accts {
		st := e.state(a.ID)
		e.mu.Lock()
		due := all || time.Since(st.lastAttempt) >= interval
		busy := st.syncing
		e.mu.Unlock()
		if due && !busy {
			id := a.ID
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				_ = e.syncAccount(e.ctx, id)
			}()
		}
	}
}

// SyncNow syncs one account ("" = all) and waits for completion. When a
// sync of the account is already running it waits for that one instead.
func (e *Engine) SyncNow(ctx context.Context, accountID string) error {
	if accountID == "" {
		accts, err := e.cfg.Store.Accounts()
		if err != nil {
			return err
		}
		var errs []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, a := range accts {
			wg.Add(1)
			go func(a models.Account) {
				defer wg.Done()
				if err := e.SyncNow(ctx, a.ID); err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
				}
			}(a)
		}
		wg.Wait()
		if len(errs) > 0 {
			sort.Strings(errs)
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}
	if _, err := e.cfg.Store.Account(accountID); err != nil {
		return errors.New("account not found")
	}
	e.wg.Add(1)
	defer e.wg.Done()
	ctx, cancel := mergeCancel(ctx, e.ctx)
	defer cancel()
	return e.syncAccount(ctx, accountID)
}

// mergeCancel returns a context cancelled when either parent is.
func mergeCancel(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	stop := context.AfterFunc(b, cancel)
	return ctx, func() { stop(); cancel() }
}

// HumanError turns provider/auth errors into messages for the UI.
func HumanError(acct models.Account, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, auth.ErrReauthRequired) {
		return fmt.Sprintf("Sign-in for %s expired. Add the account again to reconnect.", acct.Email)
	}
	if errors.Is(err, context.Canceled) {
		return "Sync was cancelled."
	}
	return err.Error()
}

func (e *Engine) syncAccount(ctx context.Context, accountID string) error {
	st := e.state(accountID)
	st.mu.Lock() // one sync at a time per account
	defer st.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	acct, err := e.cfg.Store.Account(accountID)
	if err != nil {
		return nil // removed meanwhile
	}
	e.mu.Lock()
	st.syncing, st.lastAttempt = true, time.Now()
	e.mu.Unlock()
	e.cfg.Emit.Emit(models.EventSyncStarted, models.SyncEvent{AccountID: accountID})
	e.cfg.Emit.Emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})

	newIDs, initial, err := e.doSync(ctx, acct)

	e.mu.Lock()
	st.syncing = false
	e.mu.Unlock()
	msg := HumanError(acct, err)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("sync failed", "account", acct.Email, "err", err)
		_ = e.cfg.Store.SetSyncResult(accountID, time.Now(), msg)
	} else if err == nil {
		_ = e.cfg.Store.SetSyncResult(accountID, time.Now(), "")
	}
	e.cfg.Emit.Emit(models.EventMessagesChanged, models.AccountEvent{AccountID: accountID})
	e.cfg.Emit.Emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	e.cfg.Emit.Emit(models.EventSyncFinished, models.SyncEvent{AccountID: accountID, NewMessages: len(newIDs), Error: msg})
	if err == nil && len(newIDs) > 0 {
		if !initial {
			e.announce(accountID, newIDs)
		}
		if e.cfg.OnNewMessages != nil {
			e.cfg.OnNewMessages(accountID, newIDs, initial)
		}
	}
	if err != nil {
		return errors.New(msg)
	}
	return nil
}

func (e *Engine) doSync(ctx context.Context, acct models.Account) ([]string, bool, error) {
	p, err := e.cfg.Provider(acct.ID)
	if err != nil {
		return nil, false, err
	}
	folders, labels, err := p.Folders(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := e.cfg.Store.ReplaceFolders(acct.ID, folders); err != nil {
		return nil, false, err
	}
	state, err := e.cfg.Store.SyncState(acct.ID)
	if err != nil {
		return nil, false, err
	}
	res, err := p.Sync(ctx, provider.SyncRequest{State: state, Folders: folders, Limits: e.cfg.Limits})
	if err != nil {
		return nil, false, err
	}
	// Labels referenced by messages but missing from the label list (e.g.
	// Outlook categories not in the master list) are imported too.
	known := map[string]bool{}
	for _, l := range labels {
		known[strings.ToLower(l.Name)] = true
	}
	for _, m := range res.Upserts {
		for _, n := range m.Labels {
			if !known[strings.ToLower(n)] {
				known[strings.ToLower(n)] = true
				labels = append(labels, mail.Label{RemoteID: n, Name: n})
			}
		}
	}
	names := map[string]bool{}
	for _, l := range labels {
		names[strings.ToLower(l.Name)] = true
	}
	st := e.state(acct.ID)
	e.mu.Lock()
	st.labels = names
	e.mu.Unlock()
	var labelIDs map[string]string
	if e.cfg.ResolveLabels != nil {
		labelIDs = e.cfg.ResolveLabels(acct.ID, labels)
	}
	applied, err := e.cfg.Store.ApplySync(acct.ID, res, labelIDs)
	if err != nil {
		return nil, false, fmt.Errorf("saving synced mail: %w", err)
	}
	return applied.New, res.Initial, nil
}

// announce emits mail:new for new unread inbox messages.
func (e *Engine) announce(accountID string, ids []string) {
	sums, err := e.cfg.Store.Summaries(ids)
	if err != nil {
		return
	}
	inbox, err := e.cfg.Store.FolderByKind(accountID, models.FolderInbox)
	if err != nil {
		return
	}
	var fresh []models.MessageSummary
	for _, m := range sums {
		if m.Unread && contains(m.FolderIDs, inbox.ID) && time.Since(m.Date) < 7*24*time.Hour {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Date.After(fresh[j].Date) })
	e.cfg.Emit.Emit(models.EventNewMail, models.NewMailEvent{AccountID: accountID, Count: len(fresh), Latest: fresh[0]})
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
