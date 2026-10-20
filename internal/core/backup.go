package core

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/auth"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/store"
)

// Backup file format: gzip-compressed JSON (plain JSON is accepted on
// import too) with a format marker and a version. Version 1 holds:
//   - settings: the whole models.Settings, marshaled as is (new fields are
//     included automatically), without OAuth app credentials;
//   - extra: other stored preferences (dismissed provider labels);
//   - accounts: provider, email, name, color, signature, calendar switches,
//     plus the account's label marks (by remote message id) and calendar
//     choices (by remote calendar id). Never OAuth tokens;
//   - secrets (only when opted in): the OpenRouter key and the Google /
//     Microsoft OAuth app credentials.
//
// Mail itself is not included: it downloads again from the provider.
const (
	BackupFormat    = "gomail-backup"
	BackupVersion   = 1
	BackupExtension = ".gomailbackup"
	maxBackupSize   = 1 << 30
)

// restoredSyncError marks restored accounts until they are signed in.
const restoredSyncError = "Restored from a backup. Sign in to finish."

type backupFile struct {
	Format          string                     `json:"format"`
	Version         int                        `json:"version"`
	CreatedAt       time.Time                  `json:"createdAt"`
	AppVersion      string                     `json:"appVersion"`
	IncludesSecrets bool                       `json:"includesSecrets"`
	Settings        json.RawMessage            `json:"settings"`
	Extra           map[string]json.RawMessage `json:"extra,omitempty"`
	Accounts        []backupAccount            `json:"accounts"`
	Secrets         *backupSecrets             `json:"secrets,omitempty"`
}

type backupAccount struct {
	ID              string               `json:"id"`
	Provider        string               `json:"provider"`
	Email           string               `json:"email"`
	DisplayName     string               `json:"displayName"`
	Color           string               `json:"color"`
	Signature       string               `json:"signature"`
	CreatedAt       time.Time            `json:"createdAt"`
	CalendarEnabled bool                 `json:"calendarEnabled"`
	CalendarMuted   bool                 `json:"calendarMuted"`
	LabelMarks      []store.LabelMark    `json:"labelMarks"`
	Calendars       []store.CalendarPref `json:"calendars"`
}

type backupSecrets struct {
	OpenRouterKey      string `json:"openRouterKey,omitempty"`
	GoogleClientID     string `json:"googleClientId,omitempty"`
	GoogleClientSecret string `json:"googleClientSecret,omitempty"`
	MicrosoftClientID  string `json:"microsoftClientId,omitempty"`
	MicrosoftTenant    string `json:"microsoftTenant,omitempty"`
}

// backupExtraKeys are stored settings outside models.Settings that a
// backup carries.
var backupExtraKeys = []string{dismissedLabelsKey}

// oauthApp returns the OAuth app credentials of settings (backed up only
// with secrets).
func oauthApp(st models.Settings) backupSecrets {
	return backupSecrets{GoogleClientID: st.GoogleClientID, GoogleClientSecret: st.GoogleClientSecret,
		MicrosoftClientID: st.MicrosoftClientID, MicrosoftTenant: st.MicrosoftTenant}
}

func setOAuthApp(st *models.Settings, c backupSecrets) {
	st.GoogleClientID, st.GoogleClientSecret = c.GoogleClientID, c.GoogleClientSecret
	st.MicrosoftClientID, st.MicrosoftTenant = c.MicrosoftClientID, c.MicrosoftTenant
}

// BackupFileName is the suggested name of a backup made at t.
func BackupFileName(t time.Time) string {
	return "gomail-backup-" + t.Format("2006-01-02") + BackupExtension
}

// FileOpener is implemented by UIs that can show a native open dialog.
type FileOpener interface {
	// OpenFile asks for a file to open; "" means cancelled.
	OpenFile(title, filterName, pattern string) (string, error)
}

// ---- export ----

// ExportBackup asks where to save and writes a backup there.
func (s *Service) ExportBackup(includeSecrets bool) (models.BackupResult, error) {
	path, err := s.ui.SaveFile(BackupFileName(time.Now()))
	if err != nil {
		return models.BackupResult{}, err
	}
	if path == "" {
		return models.BackupResult{Cancelled: true}, nil
	}
	if filepath.Ext(path) == "" {
		path += BackupExtension
	}
	return s.ExportBackupFile(path, includeSecrets)
}

// ExportBackupFile writes a backup to path (atomically: a failed write
// never leaves a half-written file behind).
func (s *Service) ExportBackupFile(path string, includeSecrets bool) (models.BackupResult, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gomail-backup-*")
	if err != nil {
		return models.BackupResult{}, fmt.Errorf("could not write the backup: %w", err)
	}
	defer os.Remove(tmp.Name())
	res, err := s.WriteBackup(tmp, includeSecrets)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// Secrets or not, a backup holds personal data: owner-only.
		_ = os.Chmod(tmp.Name(), 0o600)
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return models.BackupResult{}, fmt.Errorf("could not write the backup: %w", err)
	}
	res.Path = path
	slog.Info("backup written", "accounts", res.Accounts, "labelMarks", res.LabelAssignments, "secrets", includeSecrets)
	return res, nil
}

// WriteBackup writes a gzip-compressed backup to w.
func (s *Service) WriteBackup(w io.Writer, includeSecrets bool) (models.BackupResult, error) {
	f, res, err := s.buildBackup(includeSecrets)
	if err != nil {
		return res, err
	}
	zw := gzip.NewWriter(w)
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return res, err
	}
	return res, zw.Close()
}

func (s *Service) buildBackup(includeSecrets bool) (backupFile, models.BackupResult, error) {
	st := s.Settings()
	creds := oauthApp(st)
	setOAuthApp(&st, backupSecrets{})
	st.OpenRouterKeySet = false
	raw, err := json.Marshal(st)
	if err != nil {
		return backupFile{}, models.BackupResult{}, err
	}
	f := backupFile{Format: BackupFormat, Version: BackupVersion, CreatedAt: time.Now().UTC(), AppVersion: s.opts.Version,
		Settings: raw, Extra: map[string]json.RawMessage{}, Accounts: []backupAccount{}}
	res := models.BackupResult{Labels: len(st.Labels), IncludesSecrets: includeSecrets}
	for _, k := range backupExtraKeys {
		if v, err := s.store.RawSetting(k); err == nil && json.Valid(v) {
			f.Extra[k] = v
		}
	}
	if includeSecrets {
		sec := creds
		key, err := s.openRouterKey()
		if err != nil {
			return f, res, fmt.Errorf("could not read the OpenRouter key: %w", err)
		}
		sec.OpenRouterKey = key
		f.Secrets = &sec
		f.IncludesSecrets = true
	}
	accts, err := s.store.Accounts()
	if err != nil {
		return f, res, err
	}
	for _, a := range accts {
		marks, err := s.store.ExportLabelMarks(a.ID)
		if err != nil {
			return f, res, err
		}
		cals, err := s.store.ExportCalendarPrefs(a.ID)
		if err != nil {
			return f, res, err
		}
		f.Accounts = append(f.Accounts, backupAccount{ID: a.ID, Provider: a.Provider, Email: a.Email, DisplayName: a.DisplayName,
			Color: a.Color, Signature: a.Signature, CreatedAt: a.CreatedAt, CalendarEnabled: a.CalendarEnabled,
			CalendarMuted: a.CalendarMuted, LabelMarks: marks, Calendars: cals})
		res.LabelAssignments += len(marks)
	}
	res.Accounts = len(f.Accounts)
	return f, res, nil
}

// ---- import ----

// ImportBackup asks for a backup file and imports it.
func (s *Service) ImportBackup() (models.RestoreSummary, error) {
	op, ok := s.ui.(FileOpener)
	if !ok {
		return models.RestoreSummary{}, errors.New("opening files is not supported here")
	}
	path, err := op.OpenFile("Import a gomail backup", "gomail backup", "*"+BackupExtension)
	if err != nil {
		return models.RestoreSummary{}, err
	}
	if path == "" {
		return models.RestoreSummary{Cancelled: true}, nil
	}
	return s.ImportBackupFile(path)
}

// ImportBackupFile imports the backup at path.
func (s *Service) ImportBackupFile(path string) (models.RestoreSummary, error) {
	fh, err := os.Open(path)
	if err != nil {
		return models.RestoreSummary{}, fmt.Errorf("could not open the backup: %w", err)
	}
	defer fh.Close()
	sum, err := s.ImportBackupFrom(fh)
	sum.Path = path
	return sum, err
}

// readBackup decodes and validates a backup (gzip or plain JSON).
func readBackup(r io.Reader) (backupFile, error) {
	var f backupFile
	br := bufio.NewReader(r)
	var src io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return f, errors.New("this backup file is damaged")
		}
		defer zr.Close()
		src = zr
	}
	data, err := io.ReadAll(io.LimitReader(src, maxBackupSize+1))
	if err != nil {
		return f, errors.New("this backup file is damaged or incomplete")
	}
	if len(data) > maxBackupSize {
		return f, errors.New("this backup file is too large")
	}
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if json.Unmarshal(data, &head) != nil || head.Format != BackupFormat {
		return f, errors.New("this is not a gomail backup file (or it is damaged)")
	}
	if head.Version > BackupVersion {
		return f, fmt.Errorf("this backup was made by a newer version of gomail (backup format %d; this version reads up to %d). Update gomail and try again",
			head.Version, BackupVersion)
	}
	if head.Version < 1 {
		return f, fmt.Errorf("unsupported backup format version %d", head.Version)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("this backup file is damaged: %w", err)
	}
	return f, nil
}

// ImportBackupFrom merges a backup into this install. Nothing is deleted:
//   - settings: every setting in the backup replaces the current one
//     (settings the backup doesn't know keep their value); labels are
//     merged by id, the backup's version winning; OAuth app credentials
//     are only changed by a backup that includes secrets;
//   - accounts: missing ones are recreated, keeping their id, and need a
//     sign-in (ReauthAccount) unless a sign-in for that id is still stored
//     on this device. An account that already exists (same provider and
//     email) is kept as it is, only gaining the backup's labels and
//     calendar choices (and its signature if it has none);
//   - label marks wait for their messages to sync and are applied right
//     away to messages already present.
func (s *Service) ImportBackupFrom(r io.Reader) (models.RestoreSummary, error) {
	f, err := readBackup(r)
	if err != nil {
		return models.RestoreSummary{}, err
	}
	sum := models.RestoreSummary{CreatedAt: f.CreatedAt.UTC().Format(time.RFC3339)}
	before := s.Settings().Labels
	if err := s.restoreSettings(f, &sum); err != nil {
		return sum, err
	}
	known := map[string]bool{}
	for _, l := range s.Settings().Labels {
		known[l.ID] = true
	}
	idMap := map[string]string{} // backup account id -> id here
	for _, ba := range f.Accounts {
		if err := s.restoreAccount(ba, known, idMap, &sum); err != nil {
			return sum, err
		}
	}
	s.remapRestoredAccounts(f, before, idMap)
	slog.Info("backup imported", "added", sum.AccountsAdded, "ready", sum.AccountsReady, "merged", sum.AccountsMerged,
		"labelMarks", sum.LabelAssignments, "appliedNow", sum.AppliedNow)
	s.emit(models.EventAccountsChanged, models.AccountEvent{})
	s.emit(models.EventLabelsChanged, models.AccountEvent{})
	s.emit(models.EventMessagesChanged, models.AccountEvent{})
	s.emit(models.EventCalendarChanged, models.CalendarChanged{})
	if sum.AccountsReady > 0 {
		s.engine.Wake()
	}
	return sum, nil
}

func (s *Service) restoreSettings(f backupFile, sum *models.RestoreSummary) error {
	cur := s.Settings()
	merged := cur
	if len(f.Settings) > 0 && !bytes.Equal(bytes.TrimSpace(f.Settings), []byte("null")) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(f.Settings, &fields); err != nil {
			return errors.New("the settings in this backup are damaged")
		}
		for k, v := range fields {
			if strings.HasSuffix(strings.ToLower(k), "rules") {
				var list []json.RawMessage
				if json.Unmarshal(v, &list) == nil {
					sum.Rules += len(list)
				}
			}
		}
		var err error
		if merged, err = overlaySettings(cur, fields); err != nil {
			return err
		}
		sum.Labels = len(merged.Labels)
		merged.Labels = mergeLabels(merged.Labels, cur.Labels)
	}
	// OAuth app credentials only come from a backup with secrets.
	setOAuthApp(&merged, oauthApp(cur))
	if sec := f.Secrets; sec != nil {
		if sec.GoogleClientID != "" || sec.MicrosoftClientID != "" {
			merged.GoogleClientID, merged.GoogleClientSecret = sec.GoogleClientID, sec.GoogleClientSecret
			merged.MicrosoftClientID = sec.MicrosoftClientID
			if sec.MicrosoftTenant != "" {
				merged.MicrosoftTenant = sec.MicrosoftTenant
			}
			sum.SecretsRestored = true
		}
		if strings.TrimSpace(sec.OpenRouterKey) != "" {
			if err := s.SetOpenRouterKey(sec.OpenRouterKey); err != nil {
				return fmt.Errorf("could not store the OpenRouter key: %w", err)
			}
			sum.SecretsRestored = true
		}
	}
	// Provider labels the user deleted stay deleted.
	if raw, ok := f.Extra[dismissedLabelsKey]; ok {
		var theirs, ours []string
		if json.Unmarshal(raw, &theirs) == nil {
			_ = s.store.GetSetting(dismissedLabelsKey, &ours)
			for _, n := range theirs {
				if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !slices.Contains(ours, n) {
					ours = append(ours, n)
				}
			}
			if err := s.store.SetSetting(dismissedLabelsKey, ours); err != nil {
				return err
			}
		}
	}
	return s.SaveSettings(merged)
}

// overlaySettings replaces the top-level settings fields of cur that the
// backup has. A field whose value doesn't fit this version (e.g. a type
// changed) keeps the current value.
func overlaySettings(cur models.Settings, fields map[string]json.RawMessage) (models.Settings, error) {
	base, err := json.Marshal(cur)
	if err != nil {
		return cur, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return cur, err
	}
	for k, v := range fields {
		if _, ok := m[k]; !ok {
			continue // unknown here (newer gomail): ignored
		}
		one, _ := json.Marshal(map[string]json.RawMessage{k: v})
		var probe models.Settings
		if json.Unmarshal(one, &probe) != nil {
			slog.Warn("backup: setting skipped (incompatible value)", "setting", k)
			continue
		}
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		return cur, err
	}
	var out models.Settings // decoded fresh: no leftovers from cur's slices
	if err := json.Unmarshal(data, &out); err != nil {
		return cur, err
	}
	return out, nil
}

// mergeLabels returns the backup's labels followed by the current labels
// it doesn't have (by id or name).
func mergeLabels(backup, current []models.Label) []models.Label {
	out := slices.Clone(backup)
	ids, names := map[string]bool{}, map[string]bool{}
	for _, l := range backup {
		ids[l.ID] = true
		names[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}
	for _, l := range current {
		if !ids[l.ID] && !names[strings.ToLower(strings.TrimSpace(l.Name))] {
			out = append(out, l)
		}
	}
	return out
}

// remapRestoredAccounts points the restored settings at the accounts as
// they exist here: a mailbox merged into an existing account (or restored
// under a new id) has another id than in the backup. Labels from the
// backup belong to the mailboxes they belonged to there (plus, for a label
// this install already had, its mailboxes here); a label of no mailbox
// here (or from a backup made before labels were per mailbox) belongs to
// every mailbox. Rules, quiet labels and muted mailboxes follow the ids.
func (s *Service) remapRestoredAccounts(f backupFile, before []models.Label, idMap map[string]string) {
	var bs struct {
		Labels []models.Label `json:"labels"`
	}
	if len(f.Settings) > 0 {
		_ = json.Unmarshal(f.Settings, &bs)
	}
	mapID := func(id string) string {
		if m, ok := idMap[id]; ok {
			return m
		}
		return id
	}
	local := s.accountIDs()
	scope := map[string][]string{}
	inBackup := map[string]bool{}
	for _, l := range bs.Labels {
		inBackup[l.ID] = true
		for _, a := range l.AccountIDs {
			scope[l.ID] = append(scope[l.ID], mapID(a))
		}
	}
	for _, l := range before {
		if inBackup[l.ID] && len(scope[l.ID]) > 0 {
			scope[l.ID] = append(scope[l.ID], l.AccountIDs...)
		}
	}
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		for i, l := range st.Labels {
			if !inBackup[l.ID] || l.Source == config.LabelSourceSystem {
				continue
			}
			ids := []string{}
			for _, a := range scope[l.ID] {
				if slices.Contains(local, a) && !slices.Contains(ids, a) {
					ids = append(ids, a)
				}
			}
			if !slices.Equal(ids, l.AccountIDs) {
				st.Labels[i].AccountIDs = ids
				changed = true
			}
		}
		for i, r := range st.LabelRules {
			if r.AccountID != "" && mapID(r.AccountID) != r.AccountID {
				st.LabelRules[i].AccountID = mapID(r.AccountID)
				changed = true
			}
		}
		if len(st.QuietLabels) > 0 {
			quiet := make(map[string][]string, len(st.QuietLabels))
			for a, list := range st.QuietLabels {
				if mapID(a) != a {
					changed = true
				}
				quiet[mapID(a)] = list
			}
			st.QuietLabels = quiet
		}
		for i, a := range st.MutedMailboxes {
			if mapID(a) != a {
				st.MutedMailboxes[i] = mapID(a)
				changed = true
			}
		}
		return s.scopeLabels(st.Labels) || changed
	})
}

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *Service) restoreAccount(ba backupAccount, knownLabels map[string]bool, idMap map[string]string, sum *models.RestoreSummary) error {
	email := strings.ToLower(strings.TrimSpace(ba.Email))
	if (ba.Provider != models.ProviderGmail && ba.Provider != models.ProviderOutlook) || !strings.Contains(email, "@") {
		sum.AccountsSkipped++
		return nil
	}
	signature := ""
	if strings.TrimSpace(ba.Signature) != "" {
		if signature = mailmime.SanitizeHTML(ba.Signature); len(signature) > 64<<10 {
			signature = ""
		}
	}
	acct, err := s.store.FindAccount(ba.Provider, email)
	switch {
	case err == nil:
		sum.AccountsMerged++
		idMap[ba.ID] = acct.ID
		if acct.Signature == "" && signature != "" {
			if err := s.store.UpdateSignature(acct.ID, signature); err != nil {
				return err
			}
		}
	case errors.Is(err, store.ErrNotFound):
		id := ba.ID
		if !accountIDPattern.MatchString(id) {
			id = randomID("a")
		} else if _, err := s.store.Account(id); err == nil {
			id = randomID("a") // the id is taken by another account here
		}
		color := strings.TrimSpace(ba.Color)
		if !config.IsHexColor(color) {
			color = s.pickColor()
		}
		name := strings.TrimSpace(ba.DisplayName)
		if name == "" {
			name = email
		}
		created := ba.CreatedAt
		if created.IsZero() {
			created = time.Now()
		}
		acct = models.Account{ID: id, Provider: ba.Provider, Email: email, DisplayName: name, Color: color, CreatedAt: created,
			CalendarEnabled: ba.CalendarEnabled, CalendarMuted: ba.CalendarMuted}
		if err := s.store.InsertAccount(acct); err != nil {
			return err
		}
		if signature != "" {
			if err := s.store.UpdateSignature(id, signature); err != nil {
				return err
			}
		}
		// A sign-in can survive in the OS keychain when only the data folder
		// was lost (same device, same account id): use it.
		if _, err := auth.LoadToken(s.secrets, id); err == nil {
			sum.AccountsReady++
		} else {
			if err := s.store.SetNeedsReauth(id, true); err != nil {
				return err
			}
			_ = s.store.SetSyncResult(id, time.Time{}, restoredSyncError)
			sum.AccountsAdded++
		}
		slog.Info("account restored from backup", "provider", ba.Provider, "account", id)
		idMap[ba.ID] = id
		s.labelsGainAccount(id)
	default:
		return err
	}

	marks := make([]store.LabelMark, 0, len(ba.LabelMarks))
	for _, m := range ba.LabelMarks {
		if m.Kind == store.MarkClassified || knownLabels[m.LabelID] {
			marks = append(marks, m)
		}
	}
	n, err := s.store.AddPendingLabels(acct.Provider, acct.Email, marks)
	if err != nil {
		return err
	}
	sum.LabelAssignments += n
	applied, err := s.store.ApplyPendingLabels(acct.ID)
	if err != nil {
		return err
	}
	sum.AppliedNow += applied
	prefs := slices.Clone(ba.Calendars)
	for i := range prefs {
		if prefs[i].Color != "" && !config.IsHexColor(prefs[i].Color) {
			prefs[i].Color = ""
		}
	}
	cals, err := s.store.AddPendingCalendars(acct.Provider, acct.Email, prefs)
	if err != nil {
		return err
	}
	sum.Calendars += cals
	return nil
}
