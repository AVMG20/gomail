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
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/httpx"
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
	// Online probes connectivity; nil means "assume online".
	Online func(ctx context.Context) bool
	Limits provider.Limits
}

// Backoff bounds for repeated sync failures.
const (
	backoffBase = 30 * time.Second
	backoffMax  = 30 * time.Minute
	tickEvery   = 30 * time.Second
)

type acctState struct {
	mu          sync.Mutex // held while syncing
	syncing     bool
	lastAttempt time.Time
	failures    int             // consecutive failed syncs
	labels      map[string]bool // provider label names (lower) seen in the last sync
}

// Engine is the sync engine.
type Engine struct {
	cfg Config

	mu       sync.Mutex
	accounts map[string]*acctState
	offline  bool
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

// ResetBackoff clears failure backoff (e.g. after re-authentication).
func (e *Engine) ResetBackoff(accountID string) {
	st := e.state(accountID)
	e.mu.Lock()
	st.failures = 0
	e.mu.Unlock()
}

// Syncing reports whether an account is syncing now.
func (e *Engine) Syncing(accountID string) bool {
	st := e.state(accountID)
	e.mu.Lock()
	defer e.mu.Unlock()
	return st.syncing
}

// Offline reports whether the engine believes the network is down.
func (e *Engine) Offline() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.offline
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
		tick := time.NewTicker(tickEvery)
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

// backoff returns the wait after n consecutive failures.
func backoff(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := backoffBase << min(n-1, 10)
	return min(d, backoffMax)
}

func (e *Engine) setOnline(online bool) {
	e.mu.Lock()
	changed := e.offline == online
	e.offline = !online
	e.mu.Unlock()
	if changed {
		slog.Info("network status changed", "online", online)
		e.cfg.Emit.Emit(models.EventNetworkStatus, models.NetworkStatus{Online: online})
	}
}

func (e *Engine) syncDue(all bool) {
	if e.Offline() {
		if e.cfg.Online == nil || !e.cfg.Online(e.ctx) {
			return
		}
		e.setOnline(true)
		all = true // catch up right away
	}
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
		if a.NeedsReauth {
			continue // waits for ReauthAccount
		}
		st := e.state(a.ID)
		e.mu.Lock()
		wait := max(interval, backoff(st.failures))
		due := (all && st.failures == 0) || time.Since(st.lastAttempt) >= wait
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
			return errors.New(dedupe(errs))
		}
		return nil
	}
	if _, err := e.cfg.Store.Account(accountID); err != nil {
		return errors.New("account not found")
	}
	if e.Offline() && e.cfg.Online != nil {
		if !e.cfg.Online(ctx) {
			return errors.New(offlineMessage)
		}
		e.setOnline(true)
	}
	e.wg.Add(1)
	defer e.wg.Done()
	ctx, cancel := mergeCancel(ctx, e.ctx)
	defer cancel()
	return e.syncAccount(ctx, accountID)
}

func dedupe(errs []string) string {
	var out []string
	for i, e := range errs {
		if i == 0 || e != errs[i-1] {
			out = append(out, e)
		}
	}
	return strings.Join(out, "; ")
}

// mergeCancel returns a context cancelled when either parent is.
func mergeCancel(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	stop := context.AfterFunc(b, cancel)
	return ctx, func() { stop(); cancel() }
}

const offlineMessage = "You're offline. gomail will sync again when the connection is back."

// IsNetworkError reports connectivity failures (DNS, refused, timeouts).
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	var ue *url.Error
	var de *net.DNSError
	var oe *net.OpError
	if errors.As(err, &de) || errors.As(err, &oe) {
		return true
	}
	if errors.As(err, &ue) && !errors.Is(err, context.Canceled) {
		return true
	}
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "could not reach ") || strings.Contains(msg, "no such host") || strings.Contains(msg, "network is unreachable")
}

// IsReauthError reports a revoked or expired sign-in.
func IsReauthError(err error) bool {
	return errors.Is(err, auth.ErrReauthRequired) || httpx.StatusOf(err) == 401
}

// HumanError turns provider/auth errors into messages for the UI.
func HumanError(acct models.Account, err error) string {
	if err == nil {
		return ""
	}
	service := map[string]string{models.ProviderGmail: "Gmail", models.ProviderOutlook: "Outlook"}[acct.Provider]
	if service == "" {
		service = "the mail server"
	}
	switch {
	case IsReauthError(err):
		return fmt.Sprintf("The sign-in for %s has expired or was revoked. Sign in again to keep syncing.", acct.Email)
	case errors.Is(err, context.Canceled):
		return "Sync was cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("%s took too long to respond. gomail will try again.", service)
	case IsNetworkError(err):
		return fmt.Sprintf("Can't reach %s. Check your internet connection.", service)
	}
	switch s := httpx.StatusOf(err); {
	case s == 429:
		return fmt.Sprintf("%s is limiting requests right now. gomail will try again later.", service)
	case s >= 500:
		return fmt.Sprintf("%s is having problems (HTTP %d). gomail will try again later.", service, s)
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
	switch {
	case err == nil:
		e.mu.Lock()
		st.failures = 0
		e.mu.Unlock()
		e.setOnline(true)
		_ = e.cfg.Store.SetSyncResult(accountID, time.Now(), "")
		if acct.NeedsReauth {
			_ = e.cfg.Store.SetNeedsReauth(accountID, false)
		}
	case errors.Is(err, context.Canceled):
	case IsNetworkError(err) && (e.cfg.Online == nil || !e.cfg.Online(ctx)):
		// Offline: not the account's fault; resume when the network is back.
		e.setOnline(false)
		msg = offlineMessage
	default:
		e.mu.Lock()
		st.failures++
		fails := st.failures
		e.mu.Unlock()
		slog.Warn("sync failed", "account", acct.ID, "failures", fails, "err", err)
		_ = e.cfg.Store.SetSyncResult(accountID, time.Now(), msg)
		if IsReauthError(err) {
			_ = e.cfg.Store.SetNeedsReauth(accountID, true)
		}
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

// progressReporter throttles sync:progress events.
func (e *Engine) progressReporter(accountID string) func(phase string, done, total int) {
	var mu sync.Mutex
	var last time.Time
	var lastPhase string
	return func(phase string, done, total int) {
		mu.Lock()
		defer mu.Unlock()
		if phase == lastPhase && done < total && time.Since(last) < 200*time.Millisecond {
			return
		}
		last, lastPhase = time.Now(), phase
		e.cfg.Emit.Emit(models.EventSyncProgress, models.SyncProgress{AccountID: accountID, Phase: phase, Done: done, Total: total})
	}
}

func (e *Engine) doSync(ctx context.Context, acct models.Account) ([]string, bool, error) {
	p, err := e.cfg.Provider(acct.ID)
	if err != nil {
		return nil, false, err
	}
	state, err := e.cfg.Store.SyncState(acct.ID)
	if err != nil {
		return nil, false, err
	}
	var report func(string, int, int)
	if len(state) == 0 || acct.LastSyncAt == nil {
		report = e.progressReporter(acct.ID)
		report("folders", 0, 1)
	}
	folders, labels, err := p.Folders(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := e.cfg.Store.ReplaceFolders(acct.ID, folders); err != nil {
		return nil, false, err
	}
	res, err := p.Sync(ctx, provider.SyncRequest{State: state, Folders: folders, Limits: e.cfg.Limits, Progress: report})
	if err != nil {
		return nil, false, err
	}
	if report != nil {
		report("saving", 0, len(res.Upserts))
	}
	labelIDs := e.labelIDs(acct.ID, labels, res.Upserts, true)
	applied, err := e.cfg.Store.ApplySync(acct.ID, res, labelIDs)
	if err != nil {
		return nil, false, fmt.Errorf("saving synced mail: %w", err)
	}
	if report != nil {
		report("saving", len(res.Upserts), len(res.Upserts))
	}
	return applied.New, res.Initial, nil
}

// labelIDs resolves provider labels (including names only seen on
// messages, e.g. Outlook categories missing from the master list).
func (e *Engine) labelIDs(accountID string, labels []mail.Label, msgs []mail.Message, remember bool) map[string]string {
	known := map[string]bool{}
	for _, l := range labels {
		known[strings.ToLower(l.Name)] = true
	}
	for _, m := range msgs {
		for _, n := range m.Labels {
			if !known[strings.ToLower(n)] {
				known[strings.ToLower(n)] = true
				labels = append(labels, mail.Label{RemoteID: n, Name: n})
			}
		}
	}
	if remember {
		st := e.state(accountID)
		e.mu.Lock()
		st.labels = known
		e.mu.Unlock()
	}
	if e.cfg.ResolveLabels == nil {
		return nil
	}
	return e.cfg.ResolveLabels(accountID, labels)
}

// Ingest stores messages fetched outside a sync round (older pages, server
// search results) without announcing them as new mail. It returns how many
// were not cached before.
func (e *Engine) Ingest(accountID string, msgs []mail.Message) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	labelIDs := e.labelIDs(accountID, nil, msgs, false)
	applied, err := e.cfg.Store.ApplySync(accountID, mail.SyncResult{Upserts: msgs}, labelIDs)
	if err != nil {
		return 0, err
	}
	if len(applied.New) > 0 {
		e.cfg.Emit.Emit(models.EventMessagesChanged, models.AccountEvent{AccountID: accountID})
	}
	return len(applied.New), nil
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

// ProbeHosts is the default connectivity probe: a TCP connection to any of
// the given host:port addresses.
func ProbeHosts(hosts ...string) func(ctx context.Context) bool {
	return func(ctx context.Context) bool {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		var d net.Dialer
		for _, h := range hosts {
			if c, err := d.DialContext(ctx, "tcp", h); err == nil {
				c.Close()
				return true
			}
		}
		return false
	}
}
