package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/core"
	"github.com/AVMG20/gomail/internal/mailto"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncer"
)

// MailService is the Wails service bound to the frontend. Every exported
// method is callable from Vue via the generated bindings
// (frontend/bindings/github.com/AVMG20/gomail). It stays thin and delegates
// to internal/core.
type MailService struct {
	svc     *core.Service
	initErr error
	info    models.AppInfo

	app      *application.App
	window   *application.WebviewWindow
	tray     *application.SystemTray
	notifier *notifications.NotificationService

	trayMu    sync.Mutex
	trayTimer *time.Timer
	notified  map[string]models.NotificationOpen // notification id -> target

	startMinimized bool                // launched at login with --minimized
	pending        *models.ComposeOpen // mailto: received before the UI asked
	autostartOn    *bool               // last applied launch-at-login state
	trayVisible    bool                // Settings.showInTray
	closeToTray    bool                // Settings.closeToTray
	logCloser      io.Closer
	dataDir        string // set by main (preset); "" = resolve in ServiceStartup
	logFile        string

	closed bool // ServiceShutdown ran (guarded by trayMu)

	hooks *testHooks // nil in the app
}

// testHooks let end-to-end tests run the real service against fake servers.
type testHooks struct {
	options func(*core.Options)
	openURL func(string) error
	emit    func(event string, data any)
}

// NewMailService creates the service; the backend starts in ServiceStartup.
func NewMailService(notifier *notifications.NotificationService) *MailService {
	return &MailService{notifier: notifier}
}

func (m *MailService) attach(app *application.App, w *application.WebviewWindow) {
	m.app, m.window = app, w
}

// preset hands over the data directory and log file main already set up.
func (m *MailService) preset(st startup) {
	m.dataDir, m.logFile, m.logCloser = st.dir, st.logFile, st.logCloser
}

// ServiceStartup initialises the backend (Wails lifecycle hook).
func (m *MailService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if m.initErr != nil {
		return nil // e.g. another instance holds the data directory
	}
	dir, logFile := m.dataDir, m.logFile
	if dir == "" { // not started through main (tests)
		var err error
		if dir, err = config.DataDir(); err != nil {
			m.initErr = err
			return nil // let the UI show the error instead of failing to start
		}
		var closer io.Closer
		if logFile, closer, err = config.SetupLogging(dir); err == nil {
			m.logCloser = closer
		}
	}
	m.info = models.AppInfo{Version: version, DataDir: dir, LogFile: logFile, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Demo: config.DemoMode()}
	slog.Info("gomail starting", "version", version, "dataDir", dir, "demo", m.info.Demo)
	opts := core.Options{DataDir: dir, LogFile: logFile, Version: version, Demo: m.info.Demo, UI: desktopUI{m}}
	if m.hooks != nil && m.hooks.options != nil {
		m.hooks.options(&opts)
	}
	svc, err := core.New(opts)
	if err != nil {
		slog.Error("backend init failed", "err", err)
		m.initErr = err
		return nil
	}
	m.svc = svc
	if m.notifier != nil {
		if err := m.notifier.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
			slog.Warn("desktop notifications unavailable", "err", err)
			m.notifier = nil
		}
	}
	svc.Start()
	m.scheduleTrayUpdate()
	return nil
}

// ServiceShutdown stops background work (Wails lifecycle hook).
func (m *MailService) ServiceShutdown() error {
	m.trayMu.Lock()
	m.closed = true
	if m.trayTimer != nil {
		m.trayTimer.Stop()
		m.trayTimer = nil
	}
	m.trayMu.Unlock()
	if m.svc != nil {
		m.svc.Close()
	}
	if m.notifier != nil {
		_ = m.notifier.ServiceShutdown()
	}
	slog.Info("gomail stopped")
	if m.logCloser != nil {
		_ = m.logCloser.Close()
	}
	return nil
}

func (m *MailService) ready() (*core.Service, error) {
	if m.svc == nil {
		if m.initErr != nil {
			return nil, fmt.Errorf("gomail could not start: %w", m.initErr)
		}
		return nil, errors.New("gomail is still starting, please try again in a moment")
	}
	return m.svc, nil
}

// ---- core.UI implementation ----
//
// desktopUI is a separate unexported type so these methods are not bound
// to the frontend.
type desktopUI struct{ m *MailService }

// Emit sends an event to the frontend.
func (u desktopUI) Emit(event string, data any) {
	m := u.m
	if m.app != nil {
		m.app.Event.Emit(event, data)
	}
	if m.hooks != nil && m.hooks.emit != nil {
		m.hooks.emit(event, data)
	}
	if event == models.EventAccountsChanged || event == models.EventMessagesChanged {
		m.scheduleTrayUpdate()
	}
}

// OpenURL opens the system browser.
func (u desktopUI) OpenURL(url string) error {
	m := u.m
	if m.hooks != nil && m.hooks.openURL != nil {
		return m.hooks.openURL(url)
	}
	if m.app == nil {
		return errors.New("the app is not ready")
	}
	return m.app.Browser.OpenURL(url)
}

// SaveFile shows a native save dialog.
func (u desktopUI) SaveFile(defaultName string) (string, error) {
	m := u.m
	if m.app == nil {
		return "", errors.New("the app is not ready")
	}
	d := m.app.Dialog.SaveFile().SetFilename(defaultName)
	if m.window != nil {
		d = d.AttachToWindow(m.window)
	}
	return d.PromptForSingleSelection()
}

// OpenFile shows a native open dialog (core.FileOpener).
func (u desktopUI) OpenFile(title, filterName, pattern string) (string, error) {
	m := u.m
	if m.app == nil {
		return "", errors.New("the app is not ready")
	}
	d := m.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).AddFilter(filterName, pattern).AddFilter("All files", "*")
	if m.window != nil {
		d = d.AttachToWindow(m.window)
	}
	return d.PromptForSingleSelection()
}

// Notify shows a desktop notification (errors are only logged).
func (u desktopUI) Notify(title, body string, open models.NotificationOpen) {
	m := u.m
	if m.notifier == nil {
		return
	}
	// New mail notifies whenever the window isn't the focused one (hidden, minimised,
	// or open behind another app); the list already shows it otherwise. Event
	// reminders always notify.
	if open.EventID == "" && m.window != nil && m.window.IsVisible() && m.window.IsFocused() {
		return
	}
	id := fmt.Sprintf("gomail-%d", time.Now().UnixNano())
	m.trayMu.Lock()
	if m.notified == nil || len(m.notified) > 100 {
		m.notified = map[string]models.NotificationOpen{}
	}
	m.notified[id] = open
	m.trayMu.Unlock()
	err := m.notifier.SendNotification(notifications.NotificationOptions{
		ID: id, Title: title, Body: body,
		Data: map[string]any{"messageId": open.MessageID, "accountId": open.AccountID, "eventId": open.EventID},
	})
	if err != nil {
		slog.Debug("notification failed", "err", err)
	}
}

// ensureNotificationPermission asks macOS for notification permission when
// notifications are on. Without it macOS drops every notification silently;
// after the first answer the call returns the stored choice without a prompt.
func (m *MailService) ensureNotificationPermission() {
	if m.notifier == nil || runtime.GOOS != "darwin" {
		return
	}
	s, err := m.ready()
	if err != nil || !s.Settings().Notifications {
		return
	}
	go func() {
		ok, err := m.notifier.RequestNotificationAuthorization()
		if err != nil || !ok {
			slog.Info("notifications not allowed by macOS", "err", err)
		}
	}()
}

// onNotification opens the app at the message of a clicked notification.
func (m *MailService) onNotification(r notifications.NotificationResult) {
	defer safe.Recover("notification click")
	if r.Error != nil {
		return
	}
	m.trayMu.Lock()
	open, ok := m.notified[r.Response.ID]
	m.trayMu.Unlock()
	if !ok {
		open.MessageID, _ = r.Response.UserInfo["messageId"].(string)
		open.AccountID, _ = r.Response.UserInfo["accountId"].(string)
		open.EventID, _ = r.Response.UserInfo["eventId"].(string)
	}
	m.showWindow()
	switch {
	case m.app == nil:
	case open.EventID != "":
		m.app.Event.Emit(models.EventCalendarOpen, models.CalendarOpen{EventID: open.EventID})
	case open.MessageID != "":
		m.app.Event.Emit(models.EventNotificationOpen, open)
	}
}

// ApplySettings shows/hides the tray icon, enables close-to-tray and
// registers/unregisters launch at login.
func (u desktopUI) ApplySettings(st models.Settings) {
	m := u.m
	m.trayMu.Lock()
	m.trayVisible, m.closeToTray = st.ShowInTray, st.CloseToTray
	minimized := m.startMinimized
	m.startMinimized = false
	m.trayMu.Unlock()
	m.applyTray()
	// Launched at login but there's no tray to restore from: show the window.
	if minimized && !(st.ShowInTray && trayAvailable()) {
		m.showWindow()
	}
	m.applyAutostart(st.LaunchAtLogin)
}

func (m *MailService) applyAutostart(want bool) {
	if m.app == nil || m.app.Autostart == nil || config.CustomProfile() {
		return // profiles (GOMAIL_DATA_DIR / demo) never register at login
	}
	m.trayMu.Lock()
	same := m.autostartOn != nil && *m.autostartOn == want
	m.autostartOn = &want
	m.trayMu.Unlock()
	if same {
		return
	}
	enabled, _ := m.app.Autostart.IsEnabled()
	var err error
	switch {
	case want && !enabled:
		err = m.app.Autostart.EnableWithOptions(application.AutostartOptions{Identifier: autostartID(), Arguments: []string{minimizedArg}})
	case !want && enabled:
		err = m.app.Autostart.Disable()
	}
	if err != nil {
		slog.Warn("updating launch at login failed", "err", err)
	}
}

// autostartID names the login item: a launchd label on macOS, the HKCU Run
// value on Windows and the autostart .desktop file name on Linux.
func autostartID() string {
	if runtime.GOOS == "darwin" {
		return "com.avmg20.gomail"
	}
	return "gomail"
}

// handleArgs looks for a mailto: link (command line, second instance or
// macOS open-URL event) and asks the UI to compose it.
func (m *MailService) handleArgs(args []string) {
	c, ok := mailto.FromArgs(args)
	if !ok {
		return
	}
	m.trayMu.Lock()
	m.pending = &c
	m.trayMu.Unlock()
	if m.app != nil {
		m.app.Event.Emit(models.EventComposeOpen, c)
	}
}

// GetPendingCompose returns (once) a mailto: link that opened the app
// before the UI was ready, or nil.
func (m *MailService) GetPendingCompose() *models.ComposeOpen {
	m.trayMu.Lock()
	defer m.trayMu.Unlock()
	c := m.pending
	m.pending = nil
	return c
}

// CheckForUpdate checks GitHub for a newer release now.
func (m *MailService) CheckForUpdate() (models.UpdateInfo, error) {
	s, err := m.ready()
	if err != nil {
		return models.UpdateInfo{Current: version}, err
	}
	return uiResult(s.CheckForUpdate())
}

// applyTray syncs the tray icon visibility with the settings.
func (m *MailService) applyTray() {
	m.trayMu.Lock()
	visible := m.trayVisible
	m.trayMu.Unlock()
	if m.tray == nil {
		return
	}
	if visible {
		m.tray.Show()
	} else {
		m.tray.Hide()
	}
	m.scheduleTrayUpdate()
}

// hideOnClose reports whether closing the window should hide it to the
// tray: only when enabled and a tray host actually exists (some Linux
// desktops have none, and a hidden window would be unreachable).
func (m *MailService) hideOnClose() bool {
	m.trayMu.Lock()
	ok := m.trayVisible && m.closeToTray
	m.trayMu.Unlock()
	return ok && trayAvailable()
}

func (m *MailService) showWindow() {
	if m.window != nil {
		m.window.Show()
		m.window.Focus()
	}
}

// scheduleTrayUpdate refreshes the tray unread count (debounced).
func (m *MailService) scheduleTrayUpdate() {
	m.trayMu.Lock()
	defer m.trayMu.Unlock()
	if m.trayTimer != nil || m.closed {
		return
	}
	m.trayTimer = time.AfterFunc(700*time.Millisecond, func() {
		defer safe.Recover("tray update")
		m.trayMu.Lock()
		m.trayTimer = nil
		closed := m.closed
		m.trayMu.Unlock()
		if closed || m.svc == nil || m.tray == nil {
			return
		}
		accts, err := m.svc.Accounts()
		if err != nil {
			return
		}
		muted := m.svc.Settings().MutedMailboxes
		unread := 0
		for _, a := range accts {
			if !slices.Contains(muted, a.ID) {
				unread += a.UnreadCount
			}
		}
		tip := "gomail — no unread mail"
		label := ""
		if unread > 0 {
			tip = fmt.Sprintf("gomail — %d unread", unread)
			label = fmt.Sprint(unread)
		}
		m.tray.SetTooltip(tip)
		if runtime.GOOS == "darwin" {
			m.tray.SetLabel(label)
		}
	})
}

// ---- Accounts ----

// GetAccounts lists the configured accounts.
func (m *MailService) GetAccounts() ([]models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.Accounts())
}

// AddAccount runs the OAuth flow for provider ("gmail" | "outlook") in the
// system browser and blocks until it completes, is cancelled, or times out.
func (m *MailService) AddAccount(provider string) (models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return models.Account{}, err
	}
	return uiResult(s.AddAccount(provider))
}

// CancelAddAccount aborts a running AddAccount.
func (m *MailService) CancelAddAccount() {
	if m.svc != nil {
		m.svc.CancelAddAccount()
	}
}

// RemoveAccount deletes an account, its tokens and all local data.
func (m *MailService) RemoveAccount(accountID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.RemoveAccount(accountID))
}

// UpdateAccount changes an account's display name and color.
func (m *MailService) UpdateAccount(accountID, displayName, color string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.UpdateAccount(accountID, displayName, color))
}

// AutoMoves lists what automations (Jev, rules, label actions) moved into a
// folder kind (spam, archive, trash) in the last hours ("" = every mailbox).
func (m *MailService) AutoMoves(accountID, kind string, hours int) ([]models.AutoMove, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	moves, err := s.AutoMoves(accountID, kind, hours)
	return moves, uiError(err)
}

// ReorderAccounts sets the order of the mailboxes (account ids in the new order).
func (m *MailService) ReorderAccounts(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.ReorderAccounts(ids))
}

// SyncNow syncs one account ("" = all) and resolves when done.
func (m *MailService) SyncNow(accountID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SyncNow(accountID))
}

// ---- Folders & messages ----

// GetFolders lists folders of an account ("" = all accounts).
func (m *MailService) GetFolders(accountID string) ([]models.Folder, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.Folders(accountID))
}

// ListMessages returns one page of messages for the list view.
func (m *MailService) ListMessages(q models.ListQuery) (models.MessagePage, error) {
	s, err := m.ready()
	if err != nil {
		return models.MessagePage{Messages: []models.MessageSummary{}}, err
	}
	return uiResult(s.ListMessages(q))
}

// SearchMessages runs a ranked, typo-tolerant search (command palette).
func (m *MailService) SearchMessages(q models.SearchQuery) ([]models.SearchResult, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.SearchMessages(q))
}

// ResolveQueryIDs returns the ids of all messages matching q (for "select
// all"), newest first, capped at max (<= 5000).
func (m *MailService) ResolveQueryIDs(q models.ListQuery, max int) ([]string, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ResolveQueryIDs(q, max))
}

// GetMessage returns a message with its (sanitized) body.
func (m *MailService) GetMessage(id string) (models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return models.Message{}, err
	}
	return uiResult(s.Message(id))
}

// GetThread returns the messages of a conversation, oldest first.
func (m *MailService) GetThread(id string) ([]models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.Thread(id))
}

func (m *MailService) MarkRead(ids []string, read bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.MarkRead(ids, read))
}

func (m *MailService) SetStarred(ids []string, starred bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetStarred(ids, starred))
}

func (m *MailService) Archive(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.Archive(ids))
}

func (m *MailService) Trash(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.Trash(ids))
}

// MoveToJunk moves messages to their own account's Junk/Spam folder.
func (m *MailService) MoveToJunk(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.MoveToJunk(ids))
}

// JunkSender sends a sender's mail in one mailbox to Junk from now on (a
// junk rule) and moves the given messages plus the sender's inbox mail
// there, marked read. scope is "address" or "domain".
func (m *MailService) JunkSender(accountID, address, scope string, ids []string) (core.JunkSenderResult, error) {
	s, err := m.ready()
	if err != nil {
		return core.JunkSenderResult{}, err
	}
	return uiResult(s.JunkSender(accountID, address, scope, ids))
}

// NotJunk moves messages back to their inbox and returns the junk rules
// that still match their senders.
func (m *MailService) NotJunk(ids []string) (core.NotJunkResult, error) {
	s, err := m.ready()
	if err != nil {
		return core.NotJunkResult{}, err
	}
	return uiResult(s.NotJunk(ids))
}

// NotJunkSender moves all of a sender's mail in a mailbox's Junk to the
// inbox (offered after Not junk) and returns how many moved.
func (m *MailService) NotJunkSender(accountID, email string) (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return uiResult(s.NotJunkSender(accountID, email))
}

// LabelSender labels a sender's mail in one mailbox from now on (a label
// rule) and labels the sender's mail already there. scope is "address" or
// "domain".
func (m *MailService) LabelSender(accountID, address, scope, labelID string) (core.LabelSenderResult, error) {
	s, err := m.ready()
	if err != nil {
		return core.LabelSenderResult{}, err
	}
	return uiResult(s.LabelSender(accountID, address, scope, labelID))
}

// RemoveRules deletes label/junk rules by id ("Stop junking", Undo).
func (m *MailService) RemoveRules(ruleIDs []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.RemoveRules(ruleIDs))
}

// TopSenders lists who fills a mailbox's folder kind ("" = inbox), the most
// mail first, per address or per domain (groupBy "domain"); accountID ""
// covers every mailbox. For "Clean up by sender".
func (m *MailService) TopSenders(accountID, folderKind, groupBy string, limit int) ([]models.SenderSummary, error) {
	s, err := m.ready()
	if err != nil {
		return []models.SenderSummary{}, err
	}
	return uiResult(s.TopSenders(accountID, folderKind, groupBy, limit))
}

// SenderMessageIDs lists one sender's mail (an address, or "@domain") in a
// mailbox's folder kind ("" = inbox), for "Archive all" in Clean up.
func (m *MailService) SenderMessageIDs(accountID, folderKind, sender string) ([]string, error) {
	s, err := m.ready()
	if err != nil {
		return []string{}, err
	}
	return uiResult(s.SenderMessageIDs(accountID, folderKind, sender))
}

func (m *MailService) MoveToFolder(ids []string, folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.MoveToFolder(ids, folderID))
}

// SendMessage sends a new message, reply, reply-all or forward.
func (m *MailService) SendMessage(req models.ComposeRequest) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.Send(req))
}

// SaveAttachment asks for a location and saves the attachment there.
func (m *MailService) SaveAttachment(messageID, attachmentID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SaveAttachment(messageID, attachmentID))
}

// OpenExternalURL opens an http(s)/mailto link in the system browser.
func (m *MailService) OpenExternalURL(url string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.OpenExternalURL(url))
}

// GetMessageWithRemoteContent returns a message with remote images and
// CSS kept (the "Show images" button).
func (m *MailService) GetMessageWithRemoteContent(id string) (models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return models.Message{}, err
	}
	return uiResult(s.MessageWithRemoteContent(id))
}

// LoadOlder fetches the next page of older mail from the server for the
// view q (use when ListMessages reports hasMoreRemote). It returns how
// many messages were added; messages:changed follows.
func (m *MailService) LoadOlder(q models.ListQuery) (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return uiResult(s.LoadOlder(q))
}

// GetAttachmentData returns attachment bytes (max 15 MB) for previews.
func (m *MailService) GetAttachmentData(messageID, attachmentID string) (models.AttachmentData, error) {
	s, err := m.ready()
	if err != nil {
		return models.AttachmentData{}, err
	}
	return uiResult(s.AttachmentData(messageID, attachmentID))
}

// SaveDraft creates (draftID "") or updates a draft and returns its draft
// id. Saves within 3 s are coalesced. draftID may also be the id of a
// message in a Drafts folder (to keep editing an existing draft).
func (m *MailService) SaveDraft(req models.ComposeRequest, draftID string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return uiResult(s.SaveDraft(req, draftID))
}

// DeleteDraft discards a draft (draft id or draft message id).
func (m *MailService) DeleteDraft(id string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.DeleteDraft(id))
}

// Unsubscribe uses the message's List-Unsubscribe header (one-click POST,
// unsubscribe email, or opening the link in the browser).
func (m *MailService) Unsubscribe(messageID string) (models.UnsubscribeResult, error) {
	s, err := m.ready()
	if err != nil {
		return models.UnsubscribeResult{}, err
	}
	return uiResult(s.Unsubscribe(messageID))
}

// ReauthAccount signs in again for an account with needsReauth, keeping
// its local data.
func (m *MailService) ReauthAccount(accountID string) (models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return models.Account{}, err
	}
	return uiResult(s.ReauthAccount(accountID))
}

// UpdateAccountSignature sets an account's HTML signature (sanitized).
func (m *MailService) UpdateAccountSignature(accountID, html string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.UpdateAccountSignature(accountID, html))
}

// ImportAccountSignature copies the account's signature from its web mail
// (Gmail) and returns the stored HTML.
func (m *MailService) ImportAccountSignature(accountID string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	sig, err := s.ImportAccountSignature(accountID)
	return sig, uiError(err)
}

// SearchContacts suggests recipients ranked by frequency and recency.
func (m *MailService) SearchContacts(query string, limit int) ([]models.Contact, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.SearchContacts(query, limit))
}

// MarkFolderRead marks every message of a folder read.
func (m *MailService) MarkFolderRead(folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.MarkFolderRead(folderID))
}

// EmptyFolder permanently deletes everything in a Trash or Spam folder.
func (m *MailService) EmptyFolder(folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.EmptyFolder(folderID))
}

// DeleteFolder deletes one of the user's own folders on the server; its
// mail goes to Trash.
func (m *MailService) DeleteFolder(folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.DeleteFolder(folderID))
}

// ---- Labels ----

// AddLabel assigns a label manually and returns how many messages its
// "When added" moved (only with the manual label actions setting).
func (m *MailService) AddLabel(ids []string, labelID string) (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return uiResult(s.AddLabel(ids, labelID))
}

// ApplyLabelAction does a label's "When added" once for the mail that
// already has it, and returns how many messages moved.
func (m *MailService) ApplyLabelAction(labelID string) (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return uiResult(s.ApplyLabelAction(labelID))
}

// RestoreLabel puts a label back without its "When added" (Undo).
func (m *MailService) RestoreLabel(ids []string, labelID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.RestoreLabel(ids, labelID))
}

// RemoveLabel removes a label from messages.
func (m *MailService) RemoveLabel(ids []string, labelID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.RemoveLabel(ids, labelID))
}

// GetLabelCounts returns total/unread counts per label (Trash excluded).
func (m *MailService) GetLabelCounts(accountID string) ([]models.LabelCount, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.LabelCounts(accountID))
}

// GetViewLabelCounts returns total/unread counts per label within the box
// a list query shows (account + folder or folder kind), for the sidebar.
func (m *MailService) GetViewLabelCounts(q models.ListQuery) ([]models.LabelCount, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ViewLabelCounts(q))
}

// ---- Settings ----

func (m *MailService) GetSettings() (models.Settings, error) {
	s, err := m.ready()
	if err != nil {
		return config.DefaultSettings(), err
	}
	return s.GetSettings(), nil
}

func (m *MailService) SaveSettings(st models.Settings) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	before := s.Settings().Notifications
	if err := s.SaveSettings(st); err != nil {
		return uiError(err)
	}
	if st.Notifications && !before {
		m.ensureNotificationPermission()
	}
	m.scheduleTrayUpdate() // muted mailboxes don't count in the tray
	return nil
}

// SetOpenRouterKey stores the key securely ("" removes it). The key is
// never returned to the frontend.
func (m *MailService) SetOpenRouterKey(key string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetOpenRouterKey(key))
}

// ExportBackup asks where to save and writes a backup of settings,
// accounts (without sign-ins) and local label decisions. includeSecrets
// adds the OpenRouter key and OAuth app credentials.
func (m *MailService) ExportBackup(includeSecrets bool) (models.BackupResult, error) {
	s, err := m.ready()
	if err != nil {
		return models.BackupResult{}, err
	}
	return uiResult(s.ExportBackup(includeSecrets))
}

// ImportBackup asks for a backup file and merges it into this install.
// Restored accounts need a sign-in (ReauthAccount).
func (m *MailService) ImportBackup() (models.RestoreSummary, error) {
	s, err := m.ready()
	if err != nil {
		return models.RestoreSummary{}, err
	}
	return uiResult(s.ImportBackup())
}

// GetDefaultPrompts returns the built-in grammar prompt and classifier
// instructions (for "Reset to default").
func (m *MailService) GetDefaultPrompts() models.DefaultPrompts { return core.DefaultPrompts() }

// GetDefaultGrammarPrompt returns the built-in grammar system prompt.
func (m *MailService) GetDefaultGrammarPrompt() string { return core.DefaultPrompts().Grammar }

// GetAppInfo returns version, data directory and log file location.
func (m *MailService) GetAppInfo() models.AppInfo {
	info := m.info
	if info.Version == "" {
		info.Version = version
	}
	if info.Platform == "" {
		info.Platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	info.OS, info.Arch = runtime.GOOS, runtime.GOARCH
	return info
}

// ---- AI (OpenRouter) ----

// TestOpenRouter validates the stored OpenRouter key.
func (m *MailService) TestOpenRouter() error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.TestOpenRouter())
}

// ListAIModels returns all OpenRouter models (cached for an hour).
func (m *MailService) ListAIModels() ([]models.AIModel, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ListAIModels(false))
}

// RefreshAIModels refetches the OpenRouter model list.
func (m *MailService) RefreshAIModels() ([]models.AIModel, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ListAIModels(true))
}

// GetAIUsage returns OpenRouter spending recorded today and this month,
// and whether the daily budget paused automatic labeling.
func (m *MailService) GetAIUsage() (models.AIUsage, error) {
	s, err := m.ready()
	if err != nil {
		return models.AIUsage{}, err
	}
	return uiResult(s.AIUsage())
}

// ClassifyMessages labels the given messages (max 200) and returns the
// per-message results.
func (m *MailService) ClassifyMessages(ids []string) ([]models.ClassificationResult, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ClassifyMessages(ids))
}

// ClassifyQuery starts a background job labeling every message matching q.
// Trash and Drafts are skipped unless includeAllFolders.
func (m *MailService) ClassifyQuery(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyJob, error) {
	s, err := m.ready()
	if err != nil {
		return models.ClassifyJob{}, err
	}
	return uiResult(s.ClassifyQuery(q, onlyUnlabeled, includeAllFolders))
}

// CancelClassify stops a labeling job ("auto" stops automatic labeling).
func (m *MailService) CancelClassify(jobID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.CancelClassify(jobID))
}

// EstimateClassify estimates how many messages ClassifyQuery would label
// and what it would cost.
func (m *MailService) EstimateClassify(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyEstimate, error) {
	s, err := m.ready()
	if err != nil {
		return models.ClassifyEstimate{}, err
	}
	return uiResult(s.EstimateClassify(q, onlyUnlabeled, includeAllFolders))
}

// ParseEventText turns a natural-language description into event fields.
func (m *MailService) ParseEventText(text, timeZone string) (models.EventDraft, error) {
	s, err := m.ready()
	if err != nil {
		return models.EventDraft{}, err
	}
	return uiResult(s.ParseEventText(text, timeZone))
}

// ApplyLabelRules runs the label rules over mail already downloaded.
func (m *MailService) ApplyLabelRules() (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return uiResult(s.ApplyLabelRules())
}

// SummarizeMessage returns short TL;DR bullets for a message.
func (m *MailService) SummarizeMessage(id string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return uiResult(s.SummarizeMessage(id))
}

// ImproveGrammar returns text with spelling and grammar corrected (meaning,
// tone, language and formatting/HTML kept).
func (m *MailService) ImproveGrammar(text string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return uiResult(s.ImproveGrammar(text))
}

// uiError makes errors that reach the UI readable: database conditions get
// plain explanations instead of SQLite messages; everything else (already
// worded for users by core) passes through.
func uiError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return errors.New("the operation was cancelled")
	case store.IsDiskFull(err):
		return errors.New(syncer.DiskFullMessage)
	case store.IsBusy(err):
		return errors.New("gomail is busy saving mail; please try again in a moment")
	case store.IsCorrupt(err) && strings.Contains(strings.ToLower(err.Error()), "database"):
		return errors.New("the local mail cache is damaged; restart gomail to rebuild it (your accounts and settings are kept)")
	case strings.HasPrefix(err.Error(), "sql: "):
		slog.Error("database error", "err", err)
		return errors.New("something went wrong reading the local mail cache; please try again")
	}
	return err
}

// uiResult applies uiError to a (value, error) result.
func uiResult[T any](v T, err error) (T, error) { return v, uiError(err) }

// ---- Calendar ----

// ListCalendars lists the calendars of accounts with calendar access.
func (m *MailService) ListCalendars() ([]models.Calendar, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ListCalendars())
}

// SetCalendarVisible shows or hides one calendar in the unified view.
func (m *MailService) SetCalendarVisible(calendarID string, visible bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetCalendarVisible(calendarID, visible))
}

// SetCalendarColor changes the color shown for a calendar ("" = the provider's).
func (m *MailService) SetCalendarColor(calendarID, color string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetCalendarColor(calendarID, color))
}

// SetAccountCalendarEnabled disables or re-enables an account's calendar
// (disabled: no sync, no reminders, events hidden).
func (m *MailService) SetAccountCalendarEnabled(accountID string, enabled bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetAccountCalendarEnabled(accountID, enabled))
}

// SetAccountCalendarMuted silences an account's event reminders.
func (m *MailService) SetAccountCalendarMuted(accountID string, muted bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SetAccountCalendarMuted(accountID, muted))
}

// EnableCalendar signs in again to grant calendar access (mail is kept).
func (m *MailService) EnableCalendar(accountID string) (models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return models.Account{}, err
	}
	return uiResult(s.EnableCalendar(accountID))
}

// ListEvents returns events overlapping [start, end) (max 400 days) of the
// visible calendars of enabled accounts.
func (m *MailService) ListEvents(start, end time.Time) ([]models.CalendarEvent, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return uiResult(s.ListEvents(start, end))
}

// GetEvent returns one event.
func (m *MailService) GetEvent(id string) (models.CalendarEvent, error) {
	s, err := m.ready()
	if err != nil {
		return models.CalendarEvent{}, err
	}
	return uiResult(s.GetEvent(id))
}

// CreateEvent creates an event (invitations are sent to attendees).
func (m *MailService) CreateEvent(in models.EventInput) (models.CalendarEvent, error) {
	s, err := m.ready()
	if err != nil {
		return models.CalendarEvent{}, err
	}
	return uiResult(s.CreateEvent(in))
}

// UpdateEvent changes an event; scope is "this" or "series".
func (m *MailService) UpdateEvent(id string, in models.EventInput, scope string) (models.CalendarEvent, error) {
	s, err := m.ready()
	if err != nil {
		return models.CalendarEvent{}, err
	}
	return uiResult(s.UpdateEvent(id, in, scope))
}

// DeleteEvent deletes an event; scope is "this" or "series".
func (m *MailService) DeleteEvent(id, scope string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.DeleteEvent(id, scope))
}

// RespondToEvent answers an invitation: "accepted" | "tentative" | "declined".
func (m *MailService) RespondToEvent(id, response string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.RespondToEvent(id, response))
}

// SyncCalendars syncs the calendars of an account ("" = all) now.
func (m *MailService) SyncCalendars(accountID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return uiError(s.SyncCalendars(accountID))
}

// MoveEventCheck tells what moving an event to calendarID would do (cross
// account, attendees re-invited, whole series) so the UI can confirm.
func (m *MailService) MoveEventCheck(id, calendarID string) (models.MoveCheck, error) {
	s, err := m.ready()
	if err != nil {
		return models.MoveCheck{}, err
	}
	return uiResult(s.MoveEventCheck(id, calendarID))
}
