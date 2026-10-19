package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/core"
	"github.com/AVMG20/gomail/internal/mailto"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/safe"
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
	logCloser      interface{ Close() error }

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

// ServiceStartup initialises the backend (Wails lifecycle hook).
func (m *MailService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	dir, err := config.DataDir()
	if err != nil {
		m.initErr = err
		return nil // let the UI show the error instead of failing to start
	}
	logFile, closer, err := config.SetupLogging(dir)
	if err == nil {
		m.logCloser = closer
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

// Notify shows a desktop notification (errors are only logged).
func (u desktopUI) Notify(title, body string, open models.NotificationOpen) {
	m := u.m
	if m.notifier == nil {
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
		Data: map[string]any{"messageId": open.MessageID, "accountId": open.AccountID},
	})
	if err != nil {
		slog.Debug("notification failed", "err", err)
	}
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
	}
	m.showWindow()
	if open.MessageID != "" && m.app != nil {
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
	return s.CheckForUpdate()
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
	if m.trayTimer != nil {
		return
	}
	m.trayTimer = time.AfterFunc(700*time.Millisecond, func() {
		defer safe.Recover("tray update")
		m.trayMu.Lock()
		m.trayTimer = nil
		m.trayMu.Unlock()
		if m.svc == nil || m.tray == nil {
			return
		}
		accts, err := m.svc.Accounts()
		if err != nil {
			return
		}
		unread := 0
		for _, a := range accts {
			unread += a.UnreadCount
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
	return s.Accounts()
}

// AddAccount runs the OAuth flow for provider ("gmail" | "outlook") in the
// system browser and blocks until it completes, is cancelled, or times out.
func (m *MailService) AddAccount(provider string) (models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return models.Account{}, err
	}
	return s.AddAccount(provider)
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
	return s.RemoveAccount(accountID)
}

// UpdateAccount changes an account's display name and color.
func (m *MailService) UpdateAccount(accountID, displayName, color string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.UpdateAccount(accountID, displayName, color)
}

// SyncNow syncs one account ("" = all) and resolves when done.
func (m *MailService) SyncNow(accountID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.SyncNow(accountID)
}

// ---- Folders & messages ----

// GetFolders lists folders of an account ("" = all accounts).
func (m *MailService) GetFolders(accountID string) ([]models.Folder, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.Folders(accountID)
}

// ListMessages returns one page of messages for the list view.
func (m *MailService) ListMessages(q models.ListQuery) (models.MessagePage, error) {
	s, err := m.ready()
	if err != nil {
		return models.MessagePage{Messages: []models.MessageSummary{}}, err
	}
	return s.ListMessages(q)
}

// SearchMessages runs a ranked, typo-tolerant search (command palette).
func (m *MailService) SearchMessages(q models.SearchQuery) ([]models.SearchResult, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.SearchMessages(q)
}

// ResolveQueryIDs returns the ids of all messages matching q (for "select
// all"), newest first, capped at max (<= 5000).
func (m *MailService) ResolveQueryIDs(q models.ListQuery, max int) ([]string, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.ResolveQueryIDs(q, max)
}

// GetMessage returns a message with its (sanitized) body.
func (m *MailService) GetMessage(id string) (models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return models.Message{}, err
	}
	return s.Message(id)
}

// GetThread returns the messages of a conversation, oldest first.
func (m *MailService) GetThread(id string) ([]models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.Thread(id)
}

func (m *MailService) MarkRead(ids []string, read bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.MarkRead(ids, read)
}

func (m *MailService) SetStarred(ids []string, starred bool) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.SetStarred(ids, starred)
}

func (m *MailService) Archive(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.Archive(ids)
}

func (m *MailService) Trash(ids []string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.Trash(ids)
}

func (m *MailService) MoveToFolder(ids []string, folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.MoveToFolder(ids, folderID)
}

// SendMessage sends a new message, reply, reply-all or forward.
func (m *MailService) SendMessage(req models.ComposeRequest) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.Send(req)
}

// SaveAttachment asks for a location and saves the attachment there.
func (m *MailService) SaveAttachment(messageID, attachmentID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.SaveAttachment(messageID, attachmentID)
}

// OpenExternalURL opens an http(s)/mailto link in the system browser.
func (m *MailService) OpenExternalURL(url string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.OpenExternalURL(url)
}

// GetMessageWithRemoteContent returns a message with remote images and
// CSS kept (the "Show images" button).
func (m *MailService) GetMessageWithRemoteContent(id string) (models.Message, error) {
	s, err := m.ready()
	if err != nil {
		return models.Message{}, err
	}
	return s.MessageWithRemoteContent(id)
}

// LoadOlder fetches the next page of older mail from the server for the
// view q (use when ListMessages reports hasMoreRemote). It returns how
// many messages were added; messages:changed follows.
func (m *MailService) LoadOlder(q models.ListQuery) (int, error) {
	s, err := m.ready()
	if err != nil {
		return 0, err
	}
	return s.LoadOlder(q)
}

// GetAttachmentData returns attachment bytes (max 15 MB) for previews.
func (m *MailService) GetAttachmentData(messageID, attachmentID string) (models.AttachmentData, error) {
	s, err := m.ready()
	if err != nil {
		return models.AttachmentData{}, err
	}
	return s.AttachmentData(messageID, attachmentID)
}

// SaveDraft creates (draftID "") or updates a draft and returns its draft
// id. Saves within 3 s are coalesced. draftID may also be the id of a
// message in a Drafts folder (to keep editing an existing draft).
func (m *MailService) SaveDraft(req models.ComposeRequest, draftID string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return s.SaveDraft(req, draftID)
}

// DeleteDraft discards a draft (draft id or draft message id).
func (m *MailService) DeleteDraft(id string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.DeleteDraft(id)
}

// Unsubscribe uses the message's List-Unsubscribe header (one-click POST,
// unsubscribe email, or opening the link in the browser).
func (m *MailService) Unsubscribe(messageID string) (models.UnsubscribeResult, error) {
	s, err := m.ready()
	if err != nil {
		return models.UnsubscribeResult{}, err
	}
	return s.Unsubscribe(messageID)
}

// ReauthAccount signs in again for an account with needsReauth, keeping
// its local data.
func (m *MailService) ReauthAccount(accountID string) (models.Account, error) {
	s, err := m.ready()
	if err != nil {
		return models.Account{}, err
	}
	return s.ReauthAccount(accountID)
}

// UpdateAccountSignature sets an account's HTML signature (sanitized).
func (m *MailService) UpdateAccountSignature(accountID, html string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.UpdateAccountSignature(accountID, html)
}

// SearchContacts suggests recipients ranked by frequency and recency.
func (m *MailService) SearchContacts(query string, limit int) ([]models.Contact, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.SearchContacts(query, limit)
}

// MarkFolderRead marks every message of a folder read.
func (m *MailService) MarkFolderRead(folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.MarkFolderRead(folderID)
}

// EmptyFolder permanently deletes everything in a Trash or Spam folder.
func (m *MailService) EmptyFolder(folderID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.EmptyFolder(folderID)
}

// ---- Labels ----

// AddLabel assigns a label manually.
func (m *MailService) AddLabel(ids []string, labelID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.AddLabel(ids, labelID)
}

// RemoveLabel removes a label from messages.
func (m *MailService) RemoveLabel(ids []string, labelID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.RemoveLabel(ids, labelID)
}

// GetLabelCounts returns total/unread counts per label (Trash excluded).
func (m *MailService) GetLabelCounts(accountID string) ([]models.LabelCount, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.LabelCounts(accountID)
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
	return s.SaveSettings(st)
}

// SetOpenRouterKey stores the key securely ("" removes it). The key is
// never returned to the frontend.
func (m *MailService) SetOpenRouterKey(key string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.SetOpenRouterKey(key)
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
	return s.TestOpenRouter()
}

// ListAIModels returns all OpenRouter models (cached for an hour).
func (m *MailService) ListAIModels() ([]models.AIModel, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.ListAIModels(false)
}

// RefreshAIModels refetches the OpenRouter model list.
func (m *MailService) RefreshAIModels() ([]models.AIModel, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.ListAIModels(true)
}

// GetAIUsage returns OpenRouter spending recorded today and this month,
// and whether the daily budget paused automatic labeling.
func (m *MailService) GetAIUsage() (models.AIUsage, error) {
	s, err := m.ready()
	if err != nil {
		return models.AIUsage{}, err
	}
	return s.AIUsage()
}

// ClassifyMessages labels the given messages (max 200) and returns the
// per-message results.
func (m *MailService) ClassifyMessages(ids []string) ([]models.ClassificationResult, error) {
	s, err := m.ready()
	if err != nil {
		return nil, err
	}
	return s.ClassifyMessages(ids)
}

// ClassifyQuery starts a background job labeling every message matching q.
// Trash and Drafts are skipped unless includeAllFolders.
func (m *MailService) ClassifyQuery(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyJob, error) {
	s, err := m.ready()
	if err != nil {
		return models.ClassifyJob{}, err
	}
	return s.ClassifyQuery(q, onlyUnlabeled, includeAllFolders)
}

// CancelClassify stops a labeling job ("auto" stops automatic labeling).
func (m *MailService) CancelClassify(jobID string) error {
	s, err := m.ready()
	if err != nil {
		return err
	}
	return s.CancelClassify(jobID)
}

// EstimateClassify estimates how many messages ClassifyQuery would label
// and what it would cost.
func (m *MailService) EstimateClassify(q models.ListQuery, onlyUnlabeled, includeAllFolders bool) (models.ClassifyEstimate, error) {
	s, err := m.ready()
	if err != nil {
		return models.ClassifyEstimate{}, err
	}
	return s.EstimateClassify(q, onlyUnlabeled, includeAllFolders)
}

// SummarizeMessage returns short TL;DR bullets for a message.
func (m *MailService) SummarizeMessage(id string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return s.SummarizeMessage(id)
}

// ImproveGrammar returns text with spelling and grammar corrected (meaning,
// tone, language and formatting/HTML kept).
func (m *MailService) ImproveGrammar(text string) (string, error) {
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	return s.ImproveGrammar(text)
}
