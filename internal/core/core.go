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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/demo"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/provider/gmail"
	"github.com/AVMG20/gomail/internal/provider/outlook"
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
	// Notify shows a desktop notification (best effort).
	Notify(title, body string)
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

	provMu    sync.Mutex
	providers map[string]provider.Provider

	addMu     sync.Mutex
	addCancel context.CancelFunc

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
	st, err := store.Open(filepath.Join(opts.DataDir, "gomail.db"))
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
	s := &Service{opts: opts, store: st, secrets: sec, ui: opts.UI, providers: map[string]provider.Provider{}, ctx: ctx, cancel: cancel}
	s.jobs.init()
	s.drafts.entries = map[string]*draftEntry{}
	s.searches.recent, s.searches.busy = map[string]time.Time{}, map[string]bool{}
	s.attCache = newAttCache(filepath.Join(opts.DataDir, "attachments"), defaultAttCacheLimit)
	if err := s.loadSettings(); err != nil {
		st.Close()
		return nil, err
	}
	s.ai = ai.New(opts.OpenRouterBase, s.openRouterKey, ai.CacheFile(opts.DataDir))
	s.engine = syncer.New(syncer.Config{
		Store:         st,
		Emit:          notifyingEmitter{s},
		Provider:      s.provider,
		ResolveLabels: s.resolveProviderLabels,
		Interval: func() time.Duration {
			return time.Duration(s.Settings().SyncIntervalMinutes) * time.Minute
		},
		OnNewMessages: s.onNewMessages,
		Online:        online,
	})
	s.ui.ApplySettings(s.Settings())
	if opts.Demo {
		if err := s.seedDemo(); err != nil {
			st.Close()
			return nil, err
		}
	}
	return s, nil
}

// Start begins background work.
func (s *Service) Start() { s.engine.Start() }

// Close stops background work and closes the database.
func (s *Service) Close() {
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
		f()
	}()
}

func (s *Service) emit(event string, data any) { s.ui.Emit(event, data) }

// notifyingEmitter forwards engine events to the UI and shows a desktop
// notification for new mail when enabled.
type notifyingEmitter struct{ s *Service }

func (n notifyingEmitter) Emit(event string, data any) {
	n.s.ui.Emit(event, data)
	ev, ok := data.(models.NewMailEvent)
	if event != models.EventNewMail || !ok || !n.s.Settings().Notifications {
		return
	}
	from := ev.Latest.From.Name
	if from == "" {
		from = ev.Latest.From.Email
	}
	title, body := from, ev.Latest.Subject
	if ev.Count > 1 {
		title = fmt.Sprintf("%d new messages", ev.Count)
		body = from + ": " + ev.Latest.Subject
	}
	n.s.ui.Notify(title, body)
}

// ---- providers ----

func (s *Service) seedDemo() error {
	for _, a := range demo.Accounts() {
		if _, err := s.store.Account(a.ID); errors.Is(err, store.ErrNotFound) {
			a.CreatedAt = time.Now()
			if err := s.store.InsertAccount(a); err != nil {
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
	tok, err := auth.LoadToken(s.secrets, acct.ID)
	if err != nil {
		return nil, err
	}
	cfg, err := auth.Config(acct.Provider, auth.Resolve(s.Settings()), "")
	if err != nil {
		return nil, err
	}
	ts := auth.NewTokenSource(cfg, tok, acct.Email, func(t *oauth2.Token) error { return auth.SaveToken(s.secrets, acct.ID, t) })
	return newProvider(acct.Provider, ts)
}

func newProvider(kind string, ts interface {
	AccessToken(context.Context) (string, error)
	Invalidate()
}) (provider.Provider, error) {
	switch kind {
	case models.ProviderGmail:
		return gmail.New(ts, ""), nil
	case models.ProviderOutlook:
		return outlook.New(ts, ""), nil
	}
	return nil, fmt.Errorf("unsupported provider %q", kind)
}

func (s *Service) resetProviders() {
	s.provMu.Lock()
	defer s.provMu.Unlock()
	for id := range s.providers {
		if !(s.opts.Demo && strings.HasPrefix(id, "demo-")) {
			delete(s.providers, id)
		}
	}
}

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
	p, err := newProvider(kind, ts)
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
		acct = models.Account{ID: randomID("a"), Provider: kind, Email: strings.ToLower(prof.Email), DisplayName: prof.Name,
			Color: s.pickColor(), CreatedAt: time.Now()}
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
	} else if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
		return models.Account{}, fmt.Errorf("could not store the sign-in securely: %w", err)
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
	return s.afterSignIn(acct.ID)
}

func (s *Service) afterSignIn(accountID string) (models.Account, error) {
	s.provMu.Lock()
	if !(s.opts.Demo && strings.HasPrefix(accountID, "demo-")) {
		delete(s.providers, accountID)
	}
	s.provMu.Unlock()
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
	s.engine.Forget(accountID)
	s.attCache.dropAccount(accountID)
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
