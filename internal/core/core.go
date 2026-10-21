// Package core wires the store, secrets, OAuth, providers, sync engine and
// AI client together. The Wails-bound facade in package main delegates
// every call to a Service.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/provider/gmail"
	"github.com/AVMG20/gomail/internal/provider/outlook"
	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncer"
	"golang.org/x/oauth2"
)

// UI is what the service needs from the desktop shell.
type UI interface {
	Emit(event string, data any)
	OpenURL(url string) error
	// SaveFile asks for a destination path; "" means cancelled.
	SaveFile(defaultName string) (string, error)
	// Notify shows a desktop notification (best effort); clicking it should
	// emit notification:open with open.
	Notify(title, body string, open models.NotificationOpen)
	// ApplySettings lets the shell react to settings (tray, close-to-tray).
	ApplySettings(s models.Settings)
}

// Options configure a Service.
type Options struct {
	DataDir string
	LogFile string
	Version string
	Demo    bool
	UI      UI
	// Secrets overrides the secret store (tests). nil = keyring + file.
	Secrets secrets.Store
	// ProviderFactory overrides provider construction (tests).
	ProviderFactory func(acct models.Account) (provider.Provider, error)
	// OpenRouterBase overrides the OpenRouter URL (tests).
	OpenRouterBase string
	// Online overrides the connectivity probe (tests).
	Online func(ctx context.Context) bool
	// GmailBase, GraphBase and GoogleCalendarBase override the provider API
	// base URLs (tests against fake servers). "" = production.
	GmailBase          string
	GraphBase          string
	GoogleCalendarBase string
	// CalendarFactory overrides calendar provider construction (tests).
	CalendarFactory func(acct models.Account) (calendar.Provider, error)
}

// Service implements the application's use cases.
type Service struct {
	opts    Options
	store   *store.Store
	secrets secrets.Store
	ui      UI
	engine  *syncer.Engine
	ai      *ai.Client

	settingsMu sync.Mutex
	settings   models.Settings
	// labelsRev counts label changes made outside SaveSettings (sync
	// imports, mailboxes added or removed); labelAdded and labelDropped
	// hold the revision at which the backend added or dropped a label. See
	// SaveSettings.
	labelsRev    int64
	labelAdded   map[string]int64
	labelDropped map[string]int64

	provMu    sync.Mutex
	providers map[string]provider.Provider
	tokMu     sync.Mutex
	tokens    map[string]*auth.TokenSource
	cal       calState

	addMu     sync.Mutex
	addCancel context.CancelFunc

	repaired   bool
	firstQuery chan struct{}
	firstOnce  sync.Once

	jobs     jobManager
	drafts   draftSaver
	searches remoteSearches
	attCache *attCache

	ctx    context.Context
	cancel context.CancelFunc
	bg     sync.WaitGroup
}

const settingsKey = "settings"
const openRouterKeyName = "openrouter:key"

// New opens the database and prepares the service. Call Start to begin
// background syncing.
func New(opts Options) (*Service, error) {
	if opts.UI == nil {
		return nil, errors.New("core: UI is required")
	}
	st, repaired, err := openStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	sec := opts.Secrets
	if sec == nil {
		if opts.Demo {
			sec, err = secrets.NewFileStore(opts.DataDir)
		} else {
			sec, err = secrets.New(opts.DataDir)
		}
		if err != nil {
			st.Close()
			return nil, err
		}
	}
	online := opts.Online
	if online == nil && !opts.Demo && opts.ProviderFactory == nil {
		online = syncer.ProbeHosts("gmail.googleapis.com:443", "graph.microsoft.com:443", "www.google.com:443")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{opts: opts, store: st, secrets: sec, ui: opts.UI, providers: map[string]provider.Provider{},
		tokens: map[string]*auth.TokenSource{}, ctx: ctx, cancel: cancel}
	s.cal.init()
	s.jobs.init()
	s.repaired, s.firstQuery = repaired, make(chan struct{})
	s.drafts.entries = map[string]*draftEntry{}
	s.searches.recent, s.searches.busy = map[string]time.Time{}, map[string]bool{}
	s.attCache = newAttCache(filepath.Join(opts.DataDir, "attachments"), defaultAttCacheLimit)
	if err := s.loadSettings(); err != nil {
		st.Close()
		return nil, err
	}
	s.ai = ai.New(opts.OpenRouterBase, s.openRouterKey, ai.CacheFile(opts.DataDir))
	s.ai.OnUsage = s.recordUsage
	s.engine = syncer.New(syncer.Config{
		Store:         st,
		Emit:          notifyingEmitter{s},
		Provider:      s.provider,
		ResolveLabels: s.resolveProviderLabels,
		Interval: func(accountID string) time.Duration {
			return time.Duration(config.SyncInterval(s.Settings(), accountID)) * time.Minute
		},
		OnNewMessages:  s.onNewMessages,
		BeforeAnnounce: s.applyRules,
		Quiet:          s.quietMessage,
		Online:         online,
		Backfill: func() (int, bool) {
			st := s.Settings()
			return st.SyncDepthDays, st.BackfillPaused
		},
		Calendar: func(ctx context.Context, accountID string) { s.syncCalendarsQuiet(ctx, accountID) },
	})
	s.ui.ApplySettings(s.Settings())
	if opts.Demo {
		if err := s.seedDemo(); err != nil {
			st.Close()
			return nil, err
		}
		s.updateLabels(func(st *models.Settings) bool { // each demo mailbox gets its own labels once
			changed := false
			for _, a := range demo.Accounts() {
				changed = s.ownLabels(st, a.ID) || changed
			}
			return changed
		})
	}
	return s, nil
}

// Start begins background work.
//
// Syncing starts after the UI's first ListMessages (or 3 s), so the first
// paint never competes with network and database writes; the integrity
// check and maintenance run later still.
func (s *Service) Start() {
	if s.repaired {
		s.emit(models.EventDatabaseStatus, models.DatabaseStatus{OK: true,
			Message: "The local mail cache was damaged and has been rebuilt. Your mail is being downloaded again."})
	}
	s.goBG(func() {
		select {
		case <-s.firstQuery:
		case <-time.After(startDelay):
		case <-s.ctx.Done():
			return
		}
		s.engine.Start()
	})
	s.goBG(s.maintenanceLoop)
	s.goBG(s.updateLoop)
	s.goBG(s.reminderLoop)
}

// startDelay bounds how long syncing waits for the first UI query.
var startDelay = 3 * time.Second

// markFirstQuery releases the deferred start.
func (s *Service) markFirstQuery() { s.firstOnce.Do(func() { close(s.firstQuery) }) }

const repairMarker = ".repair-database"

// openStore opens the database, rebuilding it when it is damaged or was
// flagged by a failed integrity check in the previous session.
func openStore(dir string) (*store.Store, bool, error) {
	path := filepath.Join(dir, "gomail.db")
	marker := filepath.Join(dir, repairMarker)
	repaired := false
	if _, err := os.Stat(marker); err == nil {
		if err := store.Recover(path); err != nil {
			return nil, false, err
		}
		_ = os.Remove(marker)
		repaired = true
	}
	st, err := store.Open(path)
	if store.IsCorrupt(err) {
		slog.Error("database is damaged, rebuilding", "err", err)
		if rerr := store.Recover(path); rerr != nil {
			return nil, false, rerr
		}
		repaired = true
		st, err = store.Open(path)
	}
	return st, repaired, err
}

// Maintenance cadence.
var (
	integrityDelay   = 15 * time.Second
	maintenanceEvery = 6 * time.Hour
)

func (s *Service) maintenanceLoop() {
	select {
	case <-time.After(integrityDelay):
	case <-s.ctx.Done():
		return
	}
	if err := s.store.QuickCheck(); err != nil {
		slog.Error("database integrity check failed", "err", err)
		_ = os.WriteFile(filepath.Join(s.opts.DataDir, repairMarker), []byte(err.Error()), 0o600)
		s.emit(models.EventDatabaseStatus, models.DatabaseStatus{OK: false,
			Message: "The local mail cache is damaged. It will be rebuilt the next time gomail starts; your accounts and settings are kept."})
	}
	tick := time.NewTicker(maintenanceEvery)
	defer tick.Stop()
	for {
		if err := s.store.Maintain(); err != nil {
			slog.Warn("database maintenance failed", "err", err)
		}
		select {
		case <-tick.C:
		case <-s.ctx.Done():
			return
		}
	}
}

// Close stops background work and closes the database.
func (s *Service) Close() {
	// Write coalesced draft autosaves before the network context goes away.
	s.flushPendingDrafts(5 * time.Second)
	s.cancel()
	s.CancelAddAccount()
	s.jobs.cancelAll()
	s.engine.Stop()
	done := make(chan struct{})
	go func() { s.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	if err := s.store.Close(); err != nil {
		slog.Warn("closing database", "err", err)
	}
}

// Store exposes the store (tests).
func (s *Service) Store() *store.Store { return s.store }

// Info returns build information.
func (s *Service) Info() models.AppInfo {
	return models.AppInfo{Version: s.opts.Version, DataDir: s.opts.DataDir, LogFile: s.opts.LogFile, Demo: s.opts.Demo}
}

func (s *Service) goBG(f func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer safe.Recover("background task")
		f()
	}()
}

func (s *Service) emit(event string, data any) { s.ui.Emit(event, data) }

// notifyingEmitter forwards engine events to the UI and shows a desktop
// notification for new mail when enabled.
type notifyingEmitter struct{ s *Service }

func (n notifyingEmitter) Emit(event string, data any) {
	n.s.ui.Emit(event, data)
	if ns, ok := data.(models.NetworkStatus); ok && event == models.EventNetworkStatus && ns.Online {
		n.s.retryDrafts() // autosaves that failed while offline
	}
	ev, ok := data.(models.NewMailEvent)
	if event != models.EventNewMail || !ok || !n.s.Settings().Notifications {
		return
	}
	title, body := NotificationText(ev, n.s.accountName(ev.AccountID))
	n.s.ui.Notify(title, body, models.NotificationOpen{MessageID: ev.Latest.ID, AccountID: ev.AccountID})
}

// ---- providers ----

func (s *Service) seedDemo() error {
	// The demo provider generates fresh mail on every launch, so local rows
	// from a previous run would point at messages that no longer exist.
	// Reset each demo account's data but keep its customizations.
	for _, a := range demo.Accounts() {
		existing, err := s.store.Account(a.ID)
		switch {
		case err == nil:
			if err := s.store.DeleteAccount(a.ID); err != nil {
				return err
			}
			a = existing
			a.CalendarConsent = true // demo accounts always have calendar access
		case !errors.Is(err, store.ErrNotFound):
			return err
		default:
			a.CreatedAt = time.Now()
		}
		if err := s.store.InsertAccount(a); err != nil {
			return err
		}
		if a.Signature != "" {
			if err := s.store.UpdateSignature(a.ID, a.Signature); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) provider(accountID string) (provider.Provider, error) {
	s.provMu.Lock()
	defer s.provMu.Unlock()
	if p, ok := s.providers[accountID]; ok {
		return p, nil
	}
	acct, err := s.store.Account(accountID)
	if err != nil {
		return nil, errors.New("account not found")
	}
	var p provider.Provider
	switch {
	case s.opts.ProviderFactory != nil:
		p, err = s.opts.ProviderFactory(acct)
	case s.opts.Demo && strings.HasPrefix(acct.ID, "demo-"):
		p = demo.New(acct)
	default:
		p, err = s.realProvider(acct)
	}
	if err != nil {
		return nil, err
	}
	s.providers[accountID] = p
	return p, nil
}

func (s *Service) realProvider(acct models.Account) (provider.Provider, error) {
	ts, err := s.tokenSource(acct)
	if err != nil {
		return nil, err
	}
	return s.newProvider(acct.Provider, ts)
}

// tokenSource returns the account's shared token source, so mail and
// calendar clients refresh (and persist rotated tokens) through one place.
func (s *Service) tokenSource(acct models.Account) (*auth.TokenSource, error) {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()
	if ts, ok := s.tokens[acct.ID]; ok {
		return ts, nil
	}
	tok, err := auth.LoadToken(s.secrets, acct.ID)
	if err != nil {
		return nil, err
	}
	cfg, err := auth.Config(acct.Provider, auth.Resolve(s.Settings()), "")
	if err != nil {
		return nil, err
	}
	id := acct.ID
	ts := auth.NewTokenSource(cfg, tok, acct.Email, func(t *oauth2.Token) error {
		if _, err := s.store.Account(id); errors.Is(err, store.ErrNotFound) {
			return nil // removed meanwhile: don't write its sign-in back
		}
		return auth.SaveToken(s.secrets, id, t)
	})
	s.tokens[acct.ID] = ts
	return ts, nil
}

// forgetClients drops cached providers and tokens of an account ("" = all
// real accounts), e.g. after signing in again.
func (s *Service) forgetClients(accountID string) {
	keep := func(id string) bool { return s.opts.Demo && strings.HasPrefix(id, "demo-") }
	s.provMu.Lock()
	for id := range s.providers {
		if (accountID == "" || id == accountID) && !keep(id) {
			delete(s.providers, id)
		}
	}
	s.provMu.Unlock()
	s.cal.mu.Lock()
	for id := range s.cal.providers {
		if (accountID == "" || id == accountID) && !keep(id) {
			delete(s.cal.providers, id)
		}
	}
	s.cal.mu.Unlock()
	s.tokMu.Lock()
	for id := range s.tokens {
		if accountID == "" || id == accountID {
			delete(s.tokens, id)
		}
	}
	s.tokMu.Unlock()
}

func (s *Service) newProvider(kind string, ts interface {
	AccessToken(context.Context) (string, error)
	Invalidate()
}) (provider.Provider, error) {
	switch kind {
	case models.ProviderGmail:
		return gmail.New(ts, s.opts.GmailBase), nil
	case models.ProviderOutlook:
		return outlook.New(ts, s.opts.GraphBase), nil
	}
	return nil, fmt.Errorf("unsupported provider %q", kind)
}

func (s *Service) resetProviders() { s.forgetClients("") }

// ---- accounts ----

var accountPalette = []string{"#2563eb", "#16a34a", "#db2777", "#ea580c", "#7c3aed", "#0891b2", "#ca8a04", "#dc2626", "#4f46e5", "#059669"}

func (s *Service) pickColor() string {
	accts, _ := s.store.Accounts()
	used := map[string]bool{}
	for _, a := range accts {
		used[strings.ToLower(a.Color)] = true
	}
	for _, c := range accountPalette {
		if !used[c] {
			return c
		}
	}
	return accountPalette[len(accts)%len(accountPalette)]
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Accounts lists accounts with live sync state.
func (s *Service) Accounts() ([]models.Account, error) {
	accts, err := s.store.Accounts()
	if err != nil {
		return nil, err
	}
	for i := range accts {
		accts[i].Syncing = s.engine.Syncing(accts[i].ID)
	}
	return accts, nil
}

// signIn runs the OAuth flow and reads the mailbox profile.
func (s *Service) signIn(kind string) (provider.Profile, *oauth2.Token, error) {
	creds := auth.Resolve(s.Settings())
	if _, err := auth.Config(kind, creds, ""); err != nil {
		return provider.Profile{}, nil, errors.New(strings.TrimPrefix(err.Error(), auth.ErrNotConfigured.Error()+": "))
	}
	s.addMu.Lock()
	if s.addCancel != nil {
		s.addCancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.addCancel = cancel
	s.addMu.Unlock()
	defer cancel()

	tok, err := auth.Login(ctx, kind, creds, s.ui.OpenURL)
	if err != nil {
		return provider.Profile{}, nil, err
	}
	cfg, _ := auth.Config(kind, creds, "")
	current := tok
	ts := auth.NewTokenSource(cfg, tok, "the new account", func(t *oauth2.Token) error { current = t; return nil })
	p, err := s.newProvider(kind, ts)
	if err != nil {
		return provider.Profile{}, nil, err
	}
	pctx, pcancel := context.WithTimeout(ctx, 30*time.Second)
	defer pcancel()
	prof, err := p.Profile(pctx)
	if err != nil {
		return provider.Profile{}, nil, fmt.Errorf("signed in, but reading the mailbox failed: %w", err)
	}
	id := auth.IdentityFromToken(tok)
	if prof.Email == "" {
		prof.Email = id.Email
	}
	if prof.Name == "" {
		prof.Name = id.Name
	}
	if prof.Email == "" {
		return provider.Profile{}, nil, errors.New("signed in, but the provider did not return an email address")
	}
	return prof, current, nil
}

// AddAccount runs the OAuth flow and stores the account. Signing in again
// with an existing account refreshes its tokens.
func (s *Service) AddAccount(kind string) (models.Account, error) {
	if kind != models.ProviderGmail && kind != models.ProviderOutlook {
		return models.Account{}, fmt.Errorf("unknown provider %q", kind)
	}
	prof, tok, err := s.signIn(kind)
	if err != nil {
		return models.Account{}, err
	}
	acct, err := s.store.FindAccount(kind, prof.Email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return models.Account{}, err
	}
	if errors.Is(err, store.ErrNotFound) {
		granted, _ := auth.CalendarGranted(kind, tok)
		acct = models.Account{ID: randomID("a"), Provider: kind, Email: strings.ToLower(prof.Email), DisplayName: prof.Name,
			Color: s.pickColor(), CreatedAt: time.Now(), CalendarConsent: granted, CalendarEnabled: true}
		if acct.DisplayName == "" {
			acct.DisplayName = acct.Email
		}
		if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
			return models.Account{}, fmt.Errorf("could not store the sign-in securely: %w", err)
		}
		if err := s.store.InsertAccount(acct); err != nil {
			_ = s.secrets.Delete(auth.TokenKey(acct.ID))
			return models.Account{}, err
		}
		slog.Info("account added", "provider", kind, "account", acct.ID)
		s.labelsGainAccount(acct.ID)
	} else if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
		return models.Account{}, fmt.Errorf("could not store the sign-in securely: %w", err)
	} else {
		s.recordCalendarConsent(acct.ID, kind, tok)
	}
	return s.afterSignIn(acct.ID)
}

// ReauthAccount signs in again for an account whose sign-in expired or was
// revoked, keeping all local data. The same mailbox must be chosen.
func (s *Service) ReauthAccount(accountID string) (models.Account, error) {
	acct, err := s.store.Account(accountID)
	if err != nil {
		return acct, errors.New("account not found")
	}
	if s.opts.Demo && strings.HasPrefix(acct.ID, "demo-") {
		_ = s.store.SetNeedsReauth(acct.ID, false)
		return s.afterSignIn(acct.ID)
	}
	prof, tok, err := s.signIn(acct.Provider)
	if err != nil {
		return acct, err
	}
	if !strings.EqualFold(prof.Email, acct.Email) {
		return acct, fmt.Errorf("you signed in as %s, but this account is %s. Sign in with %s, or add %s as a new account",
			prof.Email, acct.Email, acct.Email, prof.Email)
	}
	if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
		return acct, fmt.Errorf("could not store the sign-in securely: %w", err)
	}
	slog.Info("account re-authenticated", "account", acct.ID)
	s.recordCalendarConsent(acct.ID, acct.Provider, tok)
	return s.afterSignIn(acct.ID)
}

func (s *Service) afterSignIn(accountID string) (models.Account, error) {
	s.forgetClients(accountID)
	_ = s.store.SetNeedsReauth(accountID, false)
	_ = s.store.SetSyncResult(accountID, time.Time{}, "")
	s.engine.ResetBackoff(accountID)
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	s.goBG(func() { _ = s.engine.SyncNow(s.ctx, accountID) })
	return s.store.Account(accountID)
}

// CancelAddAccount aborts a running AddAccount.
func (s *Service) CancelAddAccount() {
	s.addMu.Lock()
	defer s.addMu.Unlock()
	if s.addCancel != nil {
		s.addCancel()
		s.addCancel = nil
	}
}

// RemoveAccount deletes the account, its tokens and all local data.
func (s *Service) RemoveAccount(accountID string) error {
	if _, err := s.store.Account(accountID); err != nil {
		return errors.New("account not found")
	}
	if err := s.secrets.Delete(auth.TokenKey(accountID)); err != nil {
		slog.Warn("deleting token", "err", err)
	}
	if err := s.store.DeleteAccount(accountID); err != nil {
		return err
	}
	s.provMu.Lock()
	delete(s.providers, accountID)
	s.provMu.Unlock()
	s.forgetClients(accountID)
	s.engine.Forget(accountID)
	s.attCache.dropAccount(accountID)
	s.labelsLoseAccount(accountID)
	// A refresh that was in flight may have saved the token again.
	_ = s.secrets.Delete(auth.TokenKey(accountID))
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	s.emit(models.EventMessagesChanged, models.AccountEvent{AccountID: accountID})
	return nil
}

// UpdateAccount changes display name and color.
func (s *Service) UpdateAccount(accountID, displayName, color string) error {
	acct, err := s.store.Account(accountID)
	if err != nil {
		return errors.New("account not found")
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = acct.Email
	}
	color = strings.TrimSpace(color)
	if color == "" {
		color = acct.Color
	}
	if !config.IsHexColor(color) {
		return errors.New("the color must be a hex value like #2563eb")
	}
	if err := s.store.UpdateAccount(accountID, displayName, color); err != nil {
		return err
	}
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	return nil
}

// ReorderAccounts sets the order of the mailboxes (ids in the new order).
func (s *Service) ReorderAccounts(ids []string) error {
	if err := s.store.ReorderAccounts(ids); err != nil {
		return err
	}
	s.emit(models.EventAccountsChanged, models.AccountEvent{})
	return nil
}

// SyncNow syncs an account ("" = all) and waits for it.
func (s *Service) SyncNow(accountID string) error {
	return s.engine.SyncNow(s.ctx, accountID)
}

// UpdateAccountSignature stores a sanitized HTML signature.
func (s *Service) UpdateAccountSignature(accountID, html string) error {
	clean := ""
	if strings.TrimSpace(html) != "" {
		clean = mailmime.SanitizeHTML(html)
	}
	if len(clean) > 64<<10 {
		return errors.New("the signature is too long (max 64 KB)")
	}
	if err := s.store.UpdateSignature(accountID, clean); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("account not found")
		}
		return err
	}
	s.emit(models.EventAccountsChanged, models.AccountEvent{AccountID: accountID})
	return nil
}

// SearchContacts suggests recipients (prefix match on name or email).
func (s *Service) SearchContacts(query string, limit int) ([]models.Contact, error) {
	accts, err := s.store.Accounts()
	if err != nil {
		return nil, err
	}
	own := make([]string, 0, len(accts))
	for _, a := range accts {
		own = append(own, a.Email)
	}
	return s.store.SearchContacts(query, limit, own)
}

// MarkFolderRead marks every message of a folder read.
func (s *Service) MarkFolderRead(folderID string) error { return s.engine.MarkFolderRead(folderID) }

// EmptyFolder permanently deletes the contents of Trash or Spam.
func (s *Service) EmptyFolder(folderID string) error { return s.engine.EmptyFolder(folderID) }

// DeleteFolder deletes a custom folder on the server; its mail goes to Trash.
func (s *Service) DeleteFolder(folderID string) error { return s.engine.DeleteFolder(folderID) }

func (s *Service) accountName(id string) string {
	a, err := s.store.Account(id)
	if err != nil {
		return ""
	}
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Email
}

// NotificationText groups new mail: one message shows sender and subject;
// several show "5 new messages · Work" and the senders.
func NotificationText(ev models.NewMailEvent, account string) (title, body string) {
	from := ev.Latest.From.Name
	if from == "" {
		from = ev.Latest.From.Email
	}
	if ev.Count <= 1 {
		return from, ev.Latest.Subject
	}
	title = fmt.Sprintf("%d new messages", ev.Count)
	if account != "" {
		title += " · " + account
	}
	senders := ev.Senders
	if len(senders) == 0 {
		senders = []string{from}
	}
	switch {
	case len(senders) == 1:
		body = senders[0] + ": " + ev.Latest.Subject
	case len(senders) == 2:
		body = senders[0] + " and " + senders[1]
	case len(senders) == 3:
		body = senders[0] + ", " + senders[1] + " and " + senders[2]
	default:
		body = fmt.Sprintf("%s, %s and %d others", senders[0], senders[1], len(senders)-2)
	}
	return title, body
}
