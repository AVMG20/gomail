package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/devsync"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// Device sync: the state that makes two computers look the same, as
// documents of keyed values (see internal/devsync). Mailboxes are named by
// provider and address ("gmail:me@example.com"), never by local id, and
// labels by a sync id kept per computer (labelMap), because local ids
// differ between computers.
//
//   - "accounts": the mailboxes (name, color, signature, order, calendar
//     switches and calendar choices) and, while "Sync sign-ins and API
//     keys" is on, their sign-ins, the OpenRouter key and the OAuth app
//     credentials. Removing a mailbox removes it everywhere.
//   - "settings": the app settings in syncedSettings, and the per-mailbox
//     choices in mailboxSettings. Settings in deviceSettings stay on each
//     computer.
//   - "labels": every mailbox's labels, rules, quiet labels and deleted
//     imported labels, and the Junk label.
//   - "marks-<hash>" (one per mailbox): label decisions on messages
//     (labels added or removed by hand, Jev's labels and corrections,
//     "already labeled by Jev"), by remote message id.
//
// Mail itself, read/starred state and folders sync through the provider.

const (
	docAccounts    = "accounts"
	docSettings    = "settings"
	docLabels      = "labels"
	docMarksPrefix = "marks-"

	labelMapKey   = devsync.StatePrefix + "labelmap"
	junkSyncID    = "*junk"
	syncedAccount = "Added from your other computer. Sign in to finish."
)

// syncedSettings are the app settings (JSON names) that sync.
var syncedSettings = []string{
	"theme", "density", "showRemoteImages", "primaryColor", "radius", "notifications", "conversationView",
	"undoSendSeconds", "markReadMode", "readingPane", "replyQuote", "emailBackground",
	"calendarWeekStart", "calendarTimeFormat", "calendarDefaultView", "calendarDefaultDuration", "calendarShowDeclined",
	"calendarDefaultReminder", "calendarNotifications", "aiDailyBudgetUsd", "checkUpdates",
	"trustedLinkDomains", "trustedImageSenders",
	"openRouterModel", "textAIReasoning", "classifierModel", "eventAIModel", "eventAIReasoning",
	"importProviderLabels", "classifyThreshold", "classifyRemoveThreshold", "aiCorrectsProviderLabels",
	"classifyInstructions", "grammarSystemPrompt", "manualLabelActions",
}

// deviceSettings stay on each computer: they depend on its screen, disk,
// battery or desktop.
var deviceSettings = []string{
	"zoom", "launchAtLogin", "showInTray", "closeToTray", "syncDepthDays", "backfillPaused",
	"syncIntervalMinutes", "syncIntervals", "deviceSyncInterval",
}

// otherSettings sync elsewhere (labels, per mailbox, secrets) or not at
// all (read-only and migrated fields).
var otherSettings = []string{
	"labels", "labelRules", "quietLabels", // labels document
	"notJunkSenders", "mutedMailboxes", "hiddenFolders", "autoLabelAccounts", "aiCorrectsOff", "junkStaysPut", // per mailbox
	"googleClientId", "googleClientSecret", "microsoftClientId", "microsoftTenant", // secrets
	"openRouterKeySet", "labelsRev", "autoClassify", "darkenDesignedMail", // read-only or migrated
}

// mailboxSettings are the per-mailbox choices that sync ("m/<mailbox>/<name>").
var mailboxSettings = []string{"muted", "autoLabel", "aiCorrectsOff", "junkStaysPut", "notJunk", "hiddenFolders"}

// devSync is the service's device-sync state.
type devSync struct {
	engine *devsync.Engine

	mu       sync.Mutex
	labelMap map[string]string // "<mailbox>/<sync id>" -> local label id
	loaded   bool
	resigned map[string]bool // mailboxes signed in again: report their new sign-in
}

func (s *Service) initDeviceSync() {
	s.dsync.resigned = map[string]bool{}
	s.dsync.engine = devsync.New(devsync.Options{
		Source:   syncSource{s},
		State:    syncState{s.store},
		Secrets:  s.secrets,
		OnStatus: func(devsync.Status) { s.emit(models.EventDeviceSync, s.DeviceSyncStatus()) },
		Interval: func() time.Duration {
			if m := s.Settings().DeviceSyncInterval; m > 0 {
				return time.Duration(m) * time.Minute
			}
			return 0
		},
	})
}

// syncState keeps the engine's state in the settings table.
type syncState struct{ st *store.Store }

func (k syncState) Load(key string, v any) (bool, error) {
	err := k.st.GetSetting(key, v)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
func (k syncState) Save(key string, v any) error     { return k.st.SetSetting(key, v) }
func (k syncState) DeletePrefix(prefix string) error { return k.st.DeleteSettingsPrefix(prefix) }

// deviceSyncOnline syncs right away when the network comes back.
func (s *Service) deviceSyncOnline(event string, data any) {
	if ns, ok := data.(models.NetworkStatus); ok && event == models.EventNetworkStatus && ns.Online && s.dsync.engine != nil {
		s.dsync.engine.Kick()
	}
}

// deviceSyncSignedIn reports a mailbox's new sign-in to the other computers.
func (s *Service) deviceSyncSignedIn(accountID string) {
	acct, err := s.store.Account(accountID)
	if err != nil {
		return
	}
	s.dsync.mu.Lock()
	s.dsync.resigned[mailboxKey(acct)] = true
	s.dsync.mu.Unlock()
}

// ---- service API ----

// DeviceSyncStatus describes the device-sync connection.
func (s *Service) DeviceSyncStatus() models.DeviceSyncStatus {
	out := models.DeviceSyncStatus{Available: !s.opts.Demo, DefaultServerURL: devsync.DefaultServer}
	if s.dsync.engine == nil {
		return out
	}
	st := s.dsync.engine.Status()
	out.Connected, out.ServerURL, out.IncludeSecrets, out.Syncing, out.Error =
		st.Connected, st.Server, st.Secrets, st.Syncing, st.Error
	if !st.LastSync.IsZero() {
		t := st.LastSync
		out.LastSyncAt = &t
	}
	return out
}

func (s *Service) deviceSyncAvailable() error {
	if s.opts.Demo {
		return errors.New("sync between computers isn't available with demo data")
	}
	return nil
}

// ConnectDeviceSync turns on sync, with a new sync code or another
// computer's. The first round runs in the background (devicesync:status
// reports it).
func (s *Service) ConnectDeviceSync(req models.DeviceSyncConnect) (models.DeviceSyncStatus, error) {
	if err := s.deviceSyncAvailable(); err != nil {
		return s.DeviceSyncStatus(), err
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()
	s.dsync.mu.Lock()
	s.dsync.labelMap, s.dsync.loaded = nil, false
	s.dsync.mu.Unlock()
	err := s.dsync.engine.Connect(ctx, devsync.ConnectParams{Server: req.ServerURL, Code: req.Code, Create: req.Create, Secrets: req.IncludeSecrets})
	return s.DeviceSyncStatus(), err
}

// DeviceSyncCode returns the sync code, to set up another computer.
func (s *Service) DeviceSyncCode() (string, error) { return s.dsync.engine.Code() }

// DisconnectDeviceSync stops syncing this computer. Its data stays, and so
// does the server's copy.
func (s *Service) DisconnectDeviceSync() error {
	err := s.dsync.engine.Disconnect()
	s.dsync.mu.Lock()
	s.dsync.labelMap, s.dsync.loaded = nil, false
	s.dsync.mu.Unlock()
	return err
}

// SyncDevicesNow runs a sync round and waits for it.
func (s *Service) SyncDevicesNow() (models.DeviceSyncStatus, error) {
	if err := s.deviceSyncAvailable(); err != nil {
		return s.DeviceSyncStatus(), err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	err := s.dsync.engine.SyncNow(ctx)
	return s.DeviceSyncStatus(), err
}

// SetDeviceSyncSecrets turns syncing of sign-ins and API keys on or off for
// every computer of the sync account. Off removes them from the server.
func (s *Service) SetDeviceSyncSecrets(on bool) error {
	return s.dsync.engine.SetSecrets(on)
}

// ---- helpers ----

// mailboxKey names a mailbox the same way on every computer.
func mailboxKey(a models.Account) string {
	return a.Provider + ":" + strings.ToLower(strings.TrimSpace(a.Email))
}

func marksDoc(mailbox string) string {
	h := sha256.Sum256([]byte(mailbox))
	return docMarksPrefix + hex.EncodeToString(h[:8])
}

// mailboxes returns the local accounts by mailbox key (and in order).
func (s *Service) mailboxes() (map[string]models.Account, []models.Account) {
	accts, err := s.store.Accounts()
	if err != nil {
		return map[string]models.Account{}, nil
	}
	m := make(map[string]models.Account, len(accts))
	for _, a := range accts {
		m[mailboxKey(a)] = a
	}
	return m, accts
}

// splitKey splits "<prefix>/<mailbox>/<rest>" (rest may contain "/").
func splitKey(key string) (prefix, mailbox, rest string) {
	parts := strings.SplitN(key, "/", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], parts[1], ""
	}
	return key, "", ""
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// syncSource implements devsync.Source.
type syncSource struct{ s *Service }

func (src syncSource) Docs() []string {
	docs := []string{docAccounts, docSettings, docLabels}
	_, accts := src.s.mailboxes()
	for _, a := range accts {
		docs = append(docs, marksDoc(mailboxKey(a)))
	}
	return docs
}

func (src syncSource) Snapshot(doc string, synced func(string) (json.RawMessage, bool)) (map[string]json.RawMessage, error) {
	s := src.s
	switch {
	case doc == docAccounts:
		return s.snapshotAccounts(synced)
	case doc == docSettings:
		return s.snapshotSettings(synced)
	case doc == docLabels:
		return s.snapshotLabels()
	case strings.HasPrefix(doc, docMarksPrefix):
		return s.snapshotMarks(doc)
	}
	return nil, nil
}

func (src syncSource) Owns(doc, key string) bool {
	s := src.s
	switch doc {
	case docAccounts:
		prefix, mailbox, _ := splitKey(key)
		switch prefix {
		case "a", "s": // a removed mailbox; a secret no longer synced
			return true
		case "t", "c":
			local, _ := s.mailboxes()
			_, ok := local[mailbox]
			return ok
		}
	case docLabels:
		_, mailbox, _ := splitKey(key)
		local, _ := s.mailboxes()
		_, ok := local[mailbox]
		return ok
	}
	// Settings are never deleted; label decisions of mail this computer
	// doesn't have (older than it keeps, or gone) stay.
	return false
}

func (src syncSource) Apply(doc string, changes map[string]json.RawMessage, _ func(string) (json.RawMessage, bool)) ([]string, error) {
	s := src.s
	var apply func(map[string]json.RawMessage) ([]string, error)
	switch {
	case doc == docAccounts:
		apply = s.applyAccounts
	case doc == docSettings:
		apply = s.applySettings
	case doc == docLabels:
		apply = s.applyLabels
	case strings.HasPrefix(doc, docMarksPrefix):
		return s.applyMarks(doc, changes)
	default:
		return nil, nil
	}
	notApplied, err := apply(changes)
	// The UI re-reads every setting (its copy is stale now).
	s.emit(models.EventSettingsChanged, struct{}{})
	return notApplied, err
}

// sortedKeys returns the keys of changes, sorted.
func sortedKeys(changes map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ---- accounts ----

type syncAccount struct {
	Provider        string `json:"provider"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Color           string `json:"color"`
	Signature       string `json:"signature"`
	Position        int    `json:"position"`
	CalendarEnabled bool   `json:"calendarEnabled"`
	CalendarMuted   bool   `json:"calendarMuted"`
}

type syncToken struct {
	Token           *oauth2.Token `json:"token"`
	CalendarConsent bool          `json:"calendarConsent"`
}

type syncOAuthApp struct {
	GoogleClientID     string `json:"googleClientId"`
	GoogleClientSecret string `json:"googleClientSecret"`
	MicrosoftClientID  string `json:"microsoftClientId"`
	MicrosoftTenant    string `json:"microsoftTenant"`
}

func (s *Service) snapshotAccounts(synced func(string) (json.RawMessage, bool)) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	secrets := s.dsync.engine.SecretsEnabled()
	out["secrets"] = rawJSON(map[string]bool{"on": secrets})
	_, accts := s.mailboxes()
	for i, a := range accts {
		k := mailboxKey(a)
		out["a/"+k] = rawJSON(syncAccount{Provider: a.Provider, Email: strings.ToLower(a.Email), Name: a.DisplayName, Color: a.Color,
			Signature: a.Signature, Position: i, CalendarEnabled: a.CalendarEnabled, CalendarMuted: a.CalendarMuted})
		prefs, err := s.store.ExportCalendarPrefs(a.ID)
		if err != nil {
			return nil, err
		}
		out["c/"+k] = rawJSON(prefs)
		if !secrets || a.NeedsReauth {
			continue
		}
		tok, err := auth.LoadToken(s.secrets, a.ID)
		if err != nil || tok.RefreshToken == "" {
			continue
		}
		// A sign-in that works here is reported once (and again after
		// signing in anew): refreshing rotates it, which needn't sync.
		s.dsync.mu.Lock()
		resigned := s.dsync.resigned[k]
		delete(s.dsync.resigned, k)
		s.dsync.mu.Unlock()
		if prev, ok := synced("t/" + k); ok && !resigned {
			out["t/"+k] = prev
			continue
		}
		out["t/"+k] = rawJSON(syncToken{Token: &oauth2.Token{RefreshToken: tok.RefreshToken, TokenType: tok.TokenType},
			CalendarConsent: a.CalendarConsent})
	}
	if !secrets {
		return out, nil
	}
	if key, err := s.openRouterKey(); err == nil && key != "" {
		out["s/openrouter"] = rawJSON(key)
	}
	st := s.Settings()
	if st.GoogleClientID != "" || st.MicrosoftClientID != "" {
		out["s/oauth"] = rawJSON(syncOAuthApp{st.GoogleClientID, st.GoogleClientSecret, st.MicrosoftClientID, st.MicrosoftTenant})
	}
	return out, nil
}

func (s *Service) applyAccounts(changes map[string]json.RawMessage) ([]string, error) {
	var notApplied []string
	// The secrets switch and OAuth app first: sign-ins need them.
	if v, ok := changes["secrets"]; ok && v != nil {
		var sw struct {
			On bool `json:"on"`
		}
		if json.Unmarshal(v, &sw) == nil {
			if err := s.dsync.engine.ApplySecrets(sw.On); err != nil {
				return nil, err
			}
		}
	}
	if v := changes["s/oauth"]; v != nil {
		var app syncOAuthApp
		if json.Unmarshal(v, &app) == nil {
			st := s.Settings()
			st.GoogleClientID, st.GoogleClientSecret = app.GoogleClientID, app.GoogleClientSecret
			st.MicrosoftClientID, st.MicrosoftTenant = app.MicrosoftClientID, app.MicrosoftTenant
			if err := s.SaveSettings(st); err != nil {
				return nil, err
			}
		}
	}
	if v := changes["s/openrouter"]; v != nil {
		var key string
		if json.Unmarshal(v, &key) == nil && strings.TrimSpace(key) != "" {
			if err := s.SetOpenRouterKey(key); err != nil {
				notApplied = append(notApplied, "s/openrouter")
			}
		}
	}
	// A removed secret isn't deleted here: this computer may still need it.

	keys := sortedKeys(changes)
	positions := map[string]int{}
	changedAccounts := false
	for _, key := range keys {
		prefix, mailbox, _ := splitKey(key)
		if prefix != "a" {
			continue
		}
		v := changes[key]
		local, _ := s.mailboxes()
		acct, have := local[mailbox]
		if v == nil {
			if have {
				slog.Info("device sync: mailbox removed on another computer", "account", acct.ID)
				if err := s.RemoveAccount(acct.ID); err != nil {
					notApplied = append(notApplied, key)
				}
			}
			continue
		}
		var sa syncAccount
		if json.Unmarshal(v, &sa) != nil || mailboxKey(models.Account{Provider: sa.Provider, Email: sa.Email}) != mailbox {
			continue // damaged: leave it
		}
		var err error
		if have {
			err = s.updateSyncedAccount(acct, sa)
		} else {
			err = s.addSyncedAccount(sa)
		}
		if err != nil {
			slog.Warn("device sync: applying a mailbox failed", "err", err)
			notApplied = append(notApplied, key)
			continue
		}
		positions[mailbox] = sa.Position
		changedAccounts = true
	}
	if len(positions) > 0 {
		s.reorderSynced(positions)
	}
	local, _ := s.mailboxes()
	for _, key := range keys {
		prefix, mailbox, _ := splitKey(key)
		v := changes[key]
		if v == nil || (prefix != "t" && prefix != "c") {
			continue
		}
		acct, ok := local[mailbox]
		if !ok {
			notApplied = append(notApplied, key)
			continue
		}
		var err error
		if prefix == "t" {
			err = s.adoptSyncedSignIn(acct, v)
		} else {
			var prefs []store.CalendarPref
			if json.Unmarshal(v, &prefs) == nil {
				for i := range prefs {
					if prefs[i].Color != "" && !config.IsHexColor(prefs[i].Color) {
						prefs[i].Color = ""
					}
				}
				if err = s.store.SetCalendarPrefs(acct.ID, prefs); err == nil {
					s.emitCalendar(acct.ID)
				}
			}
		}
		if err != nil {
			slog.Warn("device sync: applying a mailbox setting failed", "key", prefix, "err", err)
			notApplied = append(notApplied, key)
		}
	}
	if changedAccounts {
		s.emit(models.EventAccountsChanged, models.AccountEvent{})
	}
	return notApplied, nil
}

// addSyncedAccount adds a mailbox another computer has. It needs a sign-in
// unless one arrives with it. Its labels come with the labels document.
func (s *Service) addSyncedAccount(sa syncAccount) error {
	if sa.Provider != models.ProviderGmail && sa.Provider != models.ProviderOutlook || !strings.Contains(sa.Email, "@") {
		return errors.New("invalid mailbox")
	}
	color := strings.TrimSpace(sa.Color)
	if !config.IsHexColor(color) {
		color = s.pickColor()
	}
	name := strings.TrimSpace(sa.Name)
	if name == "" {
		name = sa.Email
	}
	acct := models.Account{ID: randomID("a"), Provider: sa.Provider, Email: strings.ToLower(strings.TrimSpace(sa.Email)), DisplayName: name,
		Color: color, CreatedAt: time.Now(), CalendarEnabled: sa.CalendarEnabled, CalendarMuted: sa.CalendarMuted}
	if err := s.store.InsertAccount(acct); err != nil {
		return err
	}
	if sig := syncedSignature(sa.Signature); sig != "" {
		if err := s.store.UpdateSignature(acct.ID, sig); err != nil {
			return err
		}
	}
	_ = s.store.SetNeedsReauth(acct.ID, true)
	_ = s.store.SetSyncResult(acct.ID, time.Time{}, syncedAccount)
	slog.Info("device sync: mailbox added from another computer", "provider", acct.Provider, "account", acct.ID)
	return nil
}

func syncedSignature(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	if sig := mailmime.SanitizeHTML(html); len(sig) <= 64<<10 {
		return sig
	}
	return ""
}

func (s *Service) updateSyncedAccount(acct models.Account, sa syncAccount) error {
	if sa.Name != acct.DisplayName || (sa.Color != acct.Color && config.IsHexColor(sa.Color)) {
		color := acct.Color
		if config.IsHexColor(sa.Color) {
			color = sa.Color
		}
		name := strings.TrimSpace(sa.Name)
		if name == "" {
			name = acct.Email
		}
		if err := s.store.UpdateAccount(acct.ID, name, color); err != nil {
			return err
		}
	}
	if sig := syncedSignature(sa.Signature); sig != acct.Signature {
		if err := s.store.UpdateSignature(acct.ID, sig); err != nil {
			return err
		}
	}
	if sa.CalendarEnabled != acct.CalendarEnabled {
		if err := s.SetAccountCalendarEnabled(acct.ID, sa.CalendarEnabled); err != nil {
			return err
		}
	}
	if sa.CalendarMuted != acct.CalendarMuted {
		if err := s.SetAccountCalendarMuted(acct.ID, sa.CalendarMuted); err != nil {
			return err
		}
	}
	return nil
}

// reorderSynced orders the mailboxes as the other computer does.
func (s *Service) reorderSynced(positions map[string]int) {
	_, accts := s.mailboxes()
	order := slices.Clone(accts)
	slices.SortStableFunc(order, func(a, b models.Account) int {
		pa, oka := positions[mailboxKey(a)]
		pb, okb := positions[mailboxKey(b)]
		if !oka {
			pa = math.MaxInt
		}
		if !okb {
			pb = math.MaxInt
		}
		return pa - pb
	})
	if slices.EqualFunc(order, accts, func(a, b models.Account) bool { return a.ID == b.ID }) {
		return
	}
	ids := make([]string, len(order))
	for i, a := range order {
		ids[i] = a.ID
	}
	if err := s.store.ReorderAccounts(ids); err != nil {
		slog.Warn("device sync: reordering mailboxes failed", "err", err)
	}
}

// adoptSyncedSignIn uses another computer's sign-in for a mailbox that has
// none that works here; a working one is kept.
func (s *Service) adoptSyncedSignIn(acct models.Account, v json.RawMessage) error {
	var st syncToken
	if json.Unmarshal(v, &st) != nil || st.Token == nil || st.Token.RefreshToken == "" {
		return nil
	}
	if _, err := auth.LoadToken(s.secrets, acct.ID); err == nil && !acct.NeedsReauth {
		return nil
	}
	tok := &oauth2.Token{RefreshToken: st.Token.RefreshToken, TokenType: st.Token.TokenType}
	if err := auth.SaveToken(s.secrets, acct.ID, tok); err != nil {
		return err
	}
	_ = s.store.SetCalendarConsent(acct.ID, st.CalendarConsent)
	slog.Info("device sync: using the sign-in of another computer", "account", acct.ID)
	_, err := s.afterSignIn(acct.ID)
	return err
}

// ---- settings ----

func (s *Service) snapshotSettings(synced func(string) (json.RawMessage, bool)) (map[string]json.RawMessage, error) {
	st := s.Settings()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON(st), &fields); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for _, f := range syncedSettings {
		if v, ok := fields[f]; ok {
			out["g/"+f] = v
		}
	}
	_, accts := s.mailboxes()
	for _, a := range accts {
		k := mailboxKey(a)
		notJunk := slices.Clone(st.NotJunkSenders[a.ID])
		slices.Sort(notJunk)
		if notJunk == nil {
			notJunk = []string{}
		}
		out["m/"+k+"/muted"] = rawJSON(slices.Contains(st.MutedMailboxes, a.ID))
		out["m/"+k+"/autoLabel"] = rawJSON(slices.Contains(st.AutoLabelAccounts, a.ID))
		out["m/"+k+"/aiCorrectsOff"] = rawJSON(slices.Contains(st.AICorrectsOff, a.ID))
		out["m/"+k+"/junkStaysPut"] = rawJSON(slices.Contains(st.JunkStaysPut, a.ID))
		out["m/"+k+"/notJunk"] = rawJSON(notJunk)
		out["m/"+k+"/hiddenFolders"] = rawJSON(s.hiddenRemoteFolders(st, a, synced))
	}
	return out, nil
}

// hiddenRemoteFolders lists the remote ids of a mailbox's hidden folders.
// Folders that haven't synced here yet are known by their id (it derives
// from the remote id), so a synced choice for them is kept.
func (s *Service) hiddenRemoteFolders(st models.Settings, a models.Account, synced func(string) (json.RawMessage, bool)) []string {
	hidden := map[string]bool{}
	for _, id := range st.HiddenFolders {
		hidden[id] = true
	}
	out := []string{}
	if folders, err := s.store.Folders(a.ID); err == nil {
		for _, f := range folders {
			if hidden[f.ID] {
				out = append(out, f.RemoteID)
			}
		}
	}
	if prev, ok := synced("m/" + mailboxKey(a) + "/hiddenFolders"); ok {
		var ids []string
		_ = json.Unmarshal(prev, &ids)
		for _, rid := range ids {
			if hidden[store.FolderID(a.ID, rid)] && !slices.Contains(out, rid) {
				out = append(out, rid)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (s *Service) applySettings(changes map[string]json.RawMessage) ([]string, error) {
	cur := s.Settings()
	fields := map[string]json.RawMessage{}
	local, _ := s.mailboxes()
	var notApplied []string
	next := cur
	next.MutedMailboxes = slices.Clone(cur.MutedMailboxes)
	next.AutoLabelAccounts = slices.Clone(cur.AutoLabelAccounts)
	next.AICorrectsOff = slices.Clone(cur.AICorrectsOff)
	next.JunkStaysPut = slices.Clone(cur.JunkStaysPut)
	next.HiddenFolders = slices.Clone(cur.HiddenFolders)
	next.NotJunkSenders = map[string][]string{}
	for a, l := range cur.NotJunkSenders {
		next.NotJunkSenders[a] = slices.Clone(l)
	}
	type mailboxChange struct {
		acct  models.Account
		name  string
		value json.RawMessage
	}
	var perMailbox []mailboxChange
	for _, key := range sortedKeys(changes) {
		v := changes[key]
		if v == nil {
			continue
		}
		prefix, rest, name := splitKey(key)
		switch prefix {
		case "g":
			if slices.Contains(syncedSettings, rest) {
				fields[rest] = v
			}
		case "m":
			acct, ok := local[rest]
			if !ok {
				notApplied = append(notApplied, key)
				continue
			}
			perMailbox = append(perMailbox, mailboxChange{acct, name, v})
		}
	}
	if len(fields) > 0 {
		overlaid, err := overlaySettings(cur, fields)
		if err != nil {
			return nil, err
		}
		for _, f := range syncedSettings {
			if _, ok := fields[f]; ok {
				copySetting(&next, overlaid, f)
			}
		}
	}
	for _, c := range perMailbox {
		id := c.acct.ID
		var on bool
		switch c.name {
		case "muted", "autoLabel", "aiCorrectsOff", "junkStaysPut":
			if json.Unmarshal(c.value, &on) != nil {
				continue
			}
			list := map[string]*[]string{"muted": &next.MutedMailboxes, "autoLabel": &next.AutoLabelAccounts,
				"aiCorrectsOff": &next.AICorrectsOff, "junkStaysPut": &next.JunkStaysPut}[c.name]
			*list = setMember(*list, id, on)
		case "notJunk":
			var senders []string
			if json.Unmarshal(c.value, &senders) != nil {
				continue
			}
			if len(senders) == 0 {
				delete(next.NotJunkSenders, id)
			} else {
				next.NotJunkSenders[id] = senders
			}
		case "hiddenFolders":
			var remote []string
			if json.Unmarshal(c.value, &remote) != nil {
				continue
			}
			mine := map[string]bool{}
			if folders, err := s.store.Folders(id); err == nil {
				for _, f := range folders {
					mine[f.ID] = true
				}
			}
			for _, rid := range remote {
				mine[store.FolderID(id, rid)] = true
			}
			hidden := slices.DeleteFunc(next.HiddenFolders, func(f string) bool { return mine[f] })
			for _, rid := range remote {
				hidden = append(hidden, store.FolderID(id, rid))
			}
			next.HiddenFolders = hidden
		}
	}
	next.LabelsRev = 0
	if err := s.SaveSettings(next); err != nil {
		return nil, err
	}
	return notApplied, nil
}

// copySetting copies the setting with JSON name field from src to dst.
func copySetting(dst *models.Settings, src models.Settings, field string) {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(rawJSON(src), &m)
	if v, ok := m[field]; ok {
		_ = json.Unmarshal(rawJSON(map[string]json.RawMessage{field: v}), dst)
	}
}

func setMember(list []string, id string, on bool) []string {
	i := slices.Index(list, id)
	switch {
	case on && i < 0:
		return append(list, id)
	case !on && i >= 0:
		return slices.Delete(list, i, i+1)
	}
	return list
}

// ---- labels ----

type syncLabel struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Source      string `json:"source,omitempty"`
	Then        string `json:"then,omitempty"`
}

type syncRule struct {
	Field    string `json:"field"`
	Contains string `json:"contains"`
	Action   string `json:"action"`
	Label    string `json:"label,omitempty"` // sync id
}

func ruleKey(mailbox string, r models.LabelRule) string {
	return "r/" + mailbox + "/" + r.Field + "/" + r.Action + "/" + strings.ToLower(strings.TrimSpace(r.Contains))
}

// loadLabelMap reads the label map (dsync.mu held).
func (s *Service) loadLabelMapLocked() {
	if s.dsync.loaded {
		return
	}
	m := map[string]string{}
	_ = s.store.GetSetting(labelMapKey, &m)
	if m == nil {
		m = map[string]string{}
	}
	s.dsync.labelMap, s.dsync.loaded = m, true
}

// labelRefs returns local label id -> sync id for a mailbox, giving labels
// without one a sync id (from the name, unique in the mailbox), and
// forgetting labels that are gone.
func (s *Service) labelRefs(st models.Settings) map[string]map[string]string {
	local, _ := s.mailboxes()
	s.dsync.mu.Lock()
	defer s.dsync.mu.Unlock()
	s.loadLabelMapLocked()
	exists := map[string]bool{}
	for _, l := range st.Labels {
		exists[l.ID] = true
	}
	changed := false
	refs := map[string]map[string]string{} // mailbox -> local id -> sync id
	taken := map[string]bool{}             // "<mailbox>/<sync id>"
	for key, id := range s.dsync.labelMap {
		mailbox, syncID, _ := strings.Cut(key, "/")
		if !exists[id] {
			delete(s.dsync.labelMap, key)
			changed = true
			continue
		}
		taken[key] = true
		if refs[mailbox] == nil {
			refs[mailbox] = map[string]string{}
		}
		if cur, ok := refs[mailbox][id]; !ok || syncID < cur {
			refs[mailbox][id] = syncID
		}
	}
	accountKey := map[string]string{}
	for k, a := range local {
		accountKey[a.ID] = k
	}
	for _, l := range st.Labels {
		if l.Source == config.LabelSourceSystem || len(l.AccountIDs) != 1 {
			continue
		}
		mailbox, ok := accountKey[l.AccountIDs[0]]
		if !ok || refs[mailbox][l.ID] != "" {
			continue
		}
		base := config.Slug(l.Name)
		if base == "" {
			base = "label"
		}
		syncID := base
		for n := 2; taken[mailbox+"/"+syncID]; n++ {
			syncID = base + "-" + strconv.Itoa(n)
		}
		taken[mailbox+"/"+syncID] = true
		s.dsync.labelMap[mailbox+"/"+syncID] = l.ID
		if refs[mailbox] == nil {
			refs[mailbox] = map[string]string{}
		}
		refs[mailbox][l.ID] = syncID
		changed = true
	}
	if changed {
		_ = s.store.SetSetting(labelMapKey, s.dsync.labelMap)
	}
	return refs
}

// localLabel resolves a mailbox's sync id to a local label id.
func (s *Service) localLabel(mailbox, syncID string) (string, bool) {
	if syncID == junkSyncID {
		return config.JunkLabelID, true
	}
	s.dsync.mu.Lock()
	defer s.dsync.mu.Unlock()
	s.loadLabelMapLocked()
	id, ok := s.dsync.labelMap[mailbox+"/"+syncID]
	return id, ok
}

func syncRef(refs map[string]string, labelID string) string {
	if labelID == config.JunkLabelID {
		return junkSyncID
	}
	return refs[labelID]
}

func (s *Service) snapshotLabels() (map[string]json.RawMessage, error) {
	st := s.Settings()
	local, _ := s.mailboxes()
	refs := s.labelRefs(st)
	out := map[string]json.RawMessage{}
	for _, l := range st.Labels {
		if l.Source == config.LabelSourceSystem {
			out["l/*/junk"] = rawJSON(syncLabel{Name: l.Name, Description: l.Description, Color: strings.ToLower(l.Color)})
			continue
		}
		if len(l.AccountIDs) != 1 {
			continue
		}
		for mailbox, a := range local {
			if a.ID != l.AccountIDs[0] {
				continue
			}
			v := rawJSON(syncLabel{Name: l.Name, Description: l.Description, Color: strings.ToLower(l.Color), Source: l.Source, Then: l.Then})
			// Every sync id of the label (two computers may have named the
			// same label differently before they synced).
			s.dsync.mu.Lock()
			for key, id := range s.dsync.labelMap {
				if id == l.ID && strings.HasPrefix(key, mailbox+"/") {
					out["l/"+key] = v
				}
			}
			s.dsync.mu.Unlock()
		}
	}
	dismissed := s.dismissedLabels()
	for mailbox, a := range local {
		names := slices.Clone(dismissed[a.ID])
		slices.Sort(names)
		if names == nil {
			names = []string{}
		}
		out["x/"+mailbox] = rawJSON(names)
		if ids, ok := st.QuietLabels[a.ID]; ok {
			quiet := []string{}
			for _, id := range ids {
				if ref := syncRef(refs[mailbox], id); ref != "" {
					quiet = append(quiet, ref)
				}
			}
			slices.Sort(quiet)
			out["q/"+mailbox] = rawJSON(quiet)
		} else {
			out["q/"+mailbox] = json.RawMessage("null")
		}
	}
	for _, r := range st.LabelRules {
		var mailbox string
		for k, a := range local {
			if a.ID == r.AccountID {
				mailbox = k
			}
		}
		if mailbox == "" || strings.TrimSpace(r.Contains) == "" {
			continue
		}
		key := ruleKey(mailbox, r)
		if _, dup := out[key]; dup {
			continue
		}
		out[key] = rawJSON(syncRule{Field: r.Field, Contains: strings.TrimSpace(r.Contains), Action: r.Action, Label: syncRef(refs[mailbox], r.LabelID)})
	}
	return out, nil
}

func (s *Service) applyLabels(changes map[string]json.RawMessage) ([]string, error) {
	st := s.Settings()
	st.Labels = slices.Clone(st.Labels)
	st.LabelRules = slices.Clone(st.LabelRules)
	quiet := map[string][]string{}
	for a, l := range st.QuietLabels {
		quiet[a] = slices.Clone(l)
	}
	st.QuietLabels = quiet
	s.labelRefs(st) // map the labels this computer has
	local, _ := s.mailboxes()
	var notApplied []string
	keys := sortedKeys(changes)
	mapChanged := false
	dismissed := map[string][]string{}
	sources := map[string]string{} // label id -> source as synced

	// Labels first: rules and quiet lists refer to them.
	for _, key := range keys {
		prefix, mailbox, syncID := splitKey(key)
		if prefix != "l" {
			continue
		}
		v := changes[key]
		var sl syncLabel
		if v != nil && json.Unmarshal(v, &sl) != nil {
			continue
		}
		if mailbox == "*" {
			if v != nil && syncID == "junk" {
				if i := slices.IndexFunc(st.Labels, func(l models.Label) bool { return l.ID == config.JunkLabelID }); i >= 0 {
					st.Labels[i].Name, st.Labels[i].Description = sl.Name, sl.Description
					if config.IsHexColor(sl.Color) {
						st.Labels[i].Color = sl.Color
					}
				}
			}
			continue
		}
		acct, ok := local[mailbox]
		if !ok {
			notApplied = append(notApplied, key)
			continue
		}
		id, mapped := s.localLabel(mailbox, syncID)
		i := slices.IndexFunc(st.Labels, func(l models.Label) bool { return mapped && l.ID == id })
		if v == nil {
			if i < 0 {
				continue
			}
			s.dsync.mu.Lock()
			delete(s.dsync.labelMap, mailbox+"/"+syncID)
			alias := false
			for _, other := range s.dsync.labelMap {
				alias = alias || other == id
			}
			s.dsync.mu.Unlock()
			mapChanged = true
			if !alias {
				st.Labels = slices.Delete(st.Labels, i, i+1)
			}
			continue
		}
		if strings.TrimSpace(sl.Name) == "" {
			continue
		}
		if i < 0 {
			// Not mapped yet: the label of that name, or a new one.
			if l, ok := config.LabelNamed(st.Labels, acct.ID, sl.Name); ok && l.Source != config.LabelSourceSystem {
				i = slices.IndexFunc(st.Labels, func(x models.Label) bool { return x.ID == l.ID })
			} else {
				source := config.LabelSourceUser
				if sl.Source == config.LabelSourceProvider {
					source = config.LabelSourceProvider
				}
				st.Labels = append(st.Labels, models.Label{ID: config.NewLabelID(st.Labels, sl.Name), Source: source, AccountIDs: []string{acct.ID}})
				i = len(st.Labels) - 1
			}
			s.dsync.mu.Lock()
			s.dsync.labelMap[mailbox+"/"+syncID] = st.Labels[i].ID
			s.dsync.mu.Unlock()
			mapChanged = true
		}
		st.Labels[i].Name, st.Labels[i].Description, st.Labels[i].Then = sl.Name, sl.Description, sl.Then
		if config.IsHexColor(sl.Color) {
			st.Labels[i].Color = sl.Color
		}
		if sl.Source == config.LabelSourceUser || sl.Source == config.LabelSourceProvider {
			sources[st.Labels[i].ID] = sl.Source
		}
	}
	if mapChanged {
		s.dsync.mu.Lock()
		_ = s.store.SetSetting(labelMapKey, s.dsync.labelMap)
		s.dsync.mu.Unlock()
	}
	resolve := func(mailbox, ref string) string {
		id, _ := s.localLabel(mailbox, ref)
		return id
	}
	for _, key := range keys {
		prefix, mailbox, rest := splitKey(key)
		if prefix != "r" && prefix != "q" && prefix != "x" {
			continue
		}
		acct, ok := local[mailbox]
		if !ok {
			notApplied = append(notApplied, key)
			continue
		}
		v := changes[key]
		switch prefix {
		case "r":
			match := func(r models.LabelRule) bool { return r.AccountID == acct.ID && ruleKey(mailbox, r) == key }
			if v == nil {
				st.LabelRules = slices.DeleteFunc(st.LabelRules, match)
				continue
			}
			var sr syncRule
			if json.Unmarshal(v, &sr) != nil || rest == "" {
				continue
			}
			labelID := resolve(mailbox, sr.Label)
			if i := slices.IndexFunc(st.LabelRules, match); i >= 0 {
				st.LabelRules[i].LabelID, st.LabelRules[i].Contains = labelID, sr.Contains
			} else {
				st.LabelRules = append(st.LabelRules, models.LabelRule{AccountID: acct.ID, Field: sr.Field, Contains: sr.Contains, Action: sr.Action, LabelID: labelID})
			}
		case "q":
			var refs []string
			if v == nil || json.Unmarshal(v, &refs) != nil || refs == nil {
				delete(st.QuietLabels, acct.ID)
				continue
			}
			ids := []string{}
			for _, ref := range refs {
				if id := resolve(mailbox, ref); id != "" {
					ids = append(ids, id)
				}
			}
			st.QuietLabels[acct.ID] = ids
		case "x":
			var names []string
			if v != nil && json.Unmarshal(v, &names) == nil {
				dismissed[acct.ID] = names
			}
		}
	}
	st.LabelsRev = 0
	if err := s.SaveSettings(st); err != nil {
		return nil, err
	}
	// SaveSettings keeps an imported label imported; another computer may
	// have made it before this one imported it (or the other way round).
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		for i, l := range st.Labels {
			if src, ok := sources[l.ID]; ok && l.Source != src && l.Source != config.LabelSourceSystem {
				st.Labels[i].Source = src
				changed = true
			}
		}
		return changed
	})
	if len(dismissed) > 0 {
		d := s.dismissedLabels()
		for a, names := range dismissed {
			d[a] = names
		}
		_ = s.store.SetSetting(dismissedMailboxLabelsKey, d)
	}
	return notApplied, nil
}

// ---- label decisions ----

type syncMark struct {
	L string  `json:"l,omitempty"` // label sync id ("" for "classified")
	K string  `json:"k"`           // kind (store.Mark*)
	C float64 `json:"c,omitempty"` // AI confidence
}

// marksAccount finds the local mailbox of a marks document.
func (s *Service) marksAccount(doc string) (models.Account, string, bool) {
	local, _ := s.mailboxes()
	for k, a := range local {
		if marksDoc(k) == doc {
			return a, k, true
		}
	}
	return models.Account{}, "", false
}

func (s *Service) snapshotMarks(doc string) (map[string]json.RawMessage, error) {
	acct, mailbox, ok := s.marksAccount(doc)
	if !ok {
		return map[string]json.RawMessage{}, nil
	}
	refs := s.labelRefs(s.Settings())[mailbox]
	marks, err := s.store.ExportLabelMarks(acct.ID)
	if err != nil {
		return nil, err
	}
	byMsg := map[string][]syncMark{}
	for _, m := range marks {
		sm := syncMark{K: m.Kind}
		if m.Kind != store.MarkClassified {
			if sm.L = syncRef(refs, m.LabelID); sm.L == "" {
				continue // a label that is gone
			}
		}
		if m.Kind == store.MarkAI {
			sm.C = math.Round(m.Confidence*1000) / 1000
		}
		if !slices.Contains(byMsg[m.RemoteID], sm) {
			byMsg[m.RemoteID] = append(byMsg[m.RemoteID], sm)
		}
	}
	out := make(map[string]json.RawMessage, len(byMsg))
	for rid, list := range byMsg {
		slices.SortFunc(list, func(a, b syncMark) int {
			if c := strings.Compare(a.K, b.K); c != 0 {
				return c
			}
			return strings.Compare(a.L, b.L)
		})
		out[rid] = rawJSON(list)
	}
	return out, nil
}

func (s *Service) applyMarks(doc string, changes map[string]json.RawMessage) ([]string, error) {
	acct, mailbox, ok := s.marksAccount(doc)
	if !ok {
		return sortedKeys(changes), nil
	}
	set := map[string][]store.LabelMark{}
	for rid, v := range changes {
		if v == nil {
			continue
		}
		var list []syncMark
		if json.Unmarshal(v, &list) != nil {
			continue
		}
		marks := []store.LabelMark{}
		for _, m := range list {
			lm := store.LabelMark{RemoteID: rid, Kind: m.K, Confidence: m.C}
			if m.K != store.MarkClassified {
				id, ok := s.localLabel(mailbox, m.L)
				if !ok {
					continue // a label deleted meanwhile
				}
				lm.LabelID = id
			}
			marks = append(marks, lm)
		}
		set[rid] = marks
	}
	if len(set) == 0 {
		return nil, nil
	}
	if err := s.store.SetLabelMarks(acct.ID, set); err != nil {
		return nil, err
	}
	s.emit(models.EventLabelsChanged, models.AccountEvent{AccountID: acct.ID})
	s.emit(models.EventMessagesChanged, models.AccountEvent{AccountID: acct.ID})
	return nil, nil
}
