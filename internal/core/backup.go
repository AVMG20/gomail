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
	"maps"
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
//   - extra: other stored preferences (deleted provider labels per
//     mailbox);
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
var backupExtraKeys = []string{dismissedMailboxLabelsKey}

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
//     (settings the backup doesn't know keep their value); OAuth app
//     credentials are only changed by a backup that includes secrets;
//   - labels: each backup mailbox's labels are merged into the labels of
//     that mailbox here by name, the backup's color, description and
//     action winning; its rules and quiet labels replace that mailbox's
//     (see restoreLabels);
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
	before := s.Settings()
	if err := s.restoreSettings(f, &sum); err != nil {
		return sum, err
	}
	idMap := map[string]string{} // backup account id -> id here
	local := map[string]models.Account{}
	for _, ba := range f.Accounts {
		acct, ok, err := s.restoreAccount(ba, idMap, &sum)
		if err != nil {
			return sum, err
		}
		if ok {
			local[ba.ID] = acct
		}
	}
	labelMap := s.restoreLabels(f, idMap)
	known := map[string]bool{}
	for _, l := range s.Settings().Labels {
		known[l.ID] = true
	}
	for _, ba := range f.Accounts {
		if acct, ok := local[ba.ID]; ok {
			if err := s.restoreMarks(ba, acct, labelMap[ba.ID], known, &sum); err != nil {
				return sum, err
			}
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
		// Labels, rules and quiet labels belong to mailboxes: they are
		// merged once the accounts are restored (restoreLabels).
		merged.Labels, merged.LabelRules, merged.QuietLabels = cur.Labels, cur.LabelRules, cur.QuietLabels
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

// restoreLabels merges the backup's labels into the labels of its
// mailboxes here (restored or merged accounts): a label with the same
// name in that mailbox takes the backup's color, description and action;
// others are added (keeping their id when it is free). Labels of a backup
// from before labels were per mailbox are first split per mailbox, as on
// startup. The backup's rules and quiet labels replace those of its
// mailboxes, and its deleted provider labels stay deleted. It returns
// backup account id -> backup label id -> label id here, for the label
// marks.
func (s *Service) restoreLabels(f backupFile, idMap map[string]string) map[string]map[string]string {
	var bs struct {
		Labels      []models.Label      `json:"labels"`
		LabelRules  []models.LabelRule  `json:"labelRules"`
		QuietLabels map[string][]string `json:"quietLabels"`
	}
	if len(f.Settings) > 0 {
		_ = json.Unmarshal(f.Settings, &bs)
	}
	var backupIDs []string
	for _, ba := range f.Accounts {
		backupIDs = append(backupIDs, ba.ID)
	}
	b := models.Settings{Labels: config.NormalizeLabels(bs.Labels), LabelRules: bs.LabelRules, QuietLabels: bs.QuietLabels}
	moves, _ := config.SplitLabels(&b, backupIDs)
	out := map[string]map[string]string{}
	set := func(acct, from, to string) {
		if out[acct] == nil {
			out[acct] = map[string]string{}
		}
		out[acct][from] = to
	}
	split := map[string]map[string]string{}
	for _, m := range moves {
		if split[m.AccountID] == nil {
			split[m.AccountID] = map[string]string{}
		}
		split[m.AccountID][m.From] = m.To
	}
	s.updateLabels(func(st *models.Settings) bool {
		local := s.accountIDs()
		changed := false
		for _, l := range b.Labels {
			if l.Source == config.LabelSourceSystem {
				i := slices.IndexFunc(st.Labels, func(x models.Label) bool { return x.ID == l.ID })
				if i >= 0 && (st.Labels[i].Color != l.Color || st.Labels[i].Description != l.Description || st.Labels[i].Name != l.Name) {
					st.Labels[i].Name, st.Labels[i].Color, st.Labels[i].Description = l.Name, l.Color, l.Description
					changed = true
				}
				for _, ba := range backupIDs {
					set(ba, l.ID, l.ID)
				}
				continue
			}
			if len(l.AccountIDs) == 0 { // a backup without mailboxes: every mailbox here
				for _, a := range local {
					mergeLabel(st, l, a)
					changed = true
				}
				continue
			}
			if a, ok := idMap[l.AccountIDs[0]]; ok {
				set(l.AccountIDs[0], l.ID, mergeLabel(st, l, a))
				changed = true
			}
		}
		// Marks name the labels as they were before the split.
		for ba, m := range split {
			for from, to := range m {
				if id, ok := out[ba][to]; ok {
					set(ba, from, id)
				}
			}
		}
		if bs.LabelRules != nil {
			covered := map[string]bool{}
			for _, ba := range backupIDs {
				if a, ok := idMap[ba]; ok {
					covered[a] = true
				}
			}
			rules := slices.DeleteFunc(slices.Clone(st.LabelRules), func(r models.LabelRule) bool { return covered[r.AccountID] })
			for _, r := range b.LabelRules {
				a, ok := idMap[r.AccountID]
				if !ok {
					continue
				}
				if r.LabelID != "" {
					r.LabelID = out[r.AccountID][r.LabelID]
				}
				r.AccountID = a
				rules = append(rules, r)
			}
			st.LabelRules = rules
			changed = true
		}
		if len(b.QuietLabels) > 0 {
			quiet := maps.Clone(st.QuietLabels)
			if quiet == nil {
				quiet = map[string][]string{}
			}
			for ba, ids := range b.QuietLabels {
				a, ok := idMap[ba]
				if !ok {
					continue
				}
				list := []string{}
				for _, id := range ids {
					if to, ok := out[ba][id]; ok {
						list = append(list, to)
					}
				}
				quiet[a] = list
			}
			st.QuietLabels = quiet
			changed = true
		}
		if changed {
			config.Normalize(st)
		}
		return changed
	})
	s.restoreDismissed(f, idMap)
	return out
}

// mergeLabel merges a backup label into the labels of mailbox accountID
// and returns the id of the label there.
func mergeLabel(st *models.Settings, l models.Label, accountID string) string {
	if cur, ok := config.LabelNamed(st.Labels, accountID, l.Name); ok {
		i := slices.IndexFunc(st.Labels, func(x models.Label) bool { return x.ID == cur.ID })
		st.Labels[i].Color, st.Labels[i].Then = l.Color, l.Then
		if l.Description != "" {
			st.Labels[i].Description = l.Description
		}
		return cur.ID
	}
	l.AccountIDs = []string{accountID}
	if slices.ContainsFunc(st.Labels, func(x models.Label) bool { return x.ID == l.ID }) {
		l.ID = config.NewLabelID(st.Labels, l.Name)
	}
	st.Labels = append(st.Labels, l)
	return l.ID
}

// restoreDismissed keeps the provider labels deleted in the backup's
// mailboxes deleted (a backup from before labels were per mailbox lists
// them for every mailbox).
func (s *Service) restoreDismissed(f backupFile, idMap map[string]string) {
	theirs := map[string][]string{}
	if raw, ok := f.Extra[dismissedMailboxLabelsKey]; ok {
		_ = json.Unmarshal(raw, &theirs)
	}
	if raw, ok := f.Extra[dismissedLabelsKey]; ok {
		var names []string
		if json.Unmarshal(raw, &names) == nil {
			for _, ba := range f.Accounts {
				theirs[ba.ID] = append(theirs[ba.ID], names...)
			}
		}
	}
	if len(theirs) == 0 {
		return
	}
	ours := s.dismissedLabels()
	for ba, names := range theirs {
		a, ok := idMap[ba]
		if !ok {
			continue
		}
		for _, n := range names {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !slices.Contains(ours[a], n) {
				ours[a] = append(ours[a], n)
			}
		}
	}
	_ = s.store.SetSetting(dismissedMailboxLabelsKey, ours)
}

// remapRestoredAccounts points the restored per-mailbox lists at the
// accounts as they exist here: a mailbox merged into an existing account
// (or restored under a new id) has another id than in the backup. The
// lists keep the mailboxes this install had on them (before). A backup
// from before auto-labeling was per mailbox auto-labels its mailboxes
// when its old switch was on.
func (s *Service) remapRestoredAccounts(f backupFile, before models.Settings, idMap map[string]string) {
	var bs struct {
		AutoClassify      bool     `json:"autoClassify"`
		AutoLabelAccounts []string `json:"autoLabelAccounts"`
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
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		if len(f.Settings) > 0 && bs.AutoLabelAccounts == nil && bs.AutoClassify {
			for _, ba := range f.Accounts {
				st.AutoLabelAccounts = append(st.AutoLabelAccounts, ba.ID)
				changed = true
			}
		}
		for _, l := range []struct {
			cur *[]string
			had []string
		}{
			{&st.MutedMailboxes, before.MutedMailboxes},
			{&st.AutoLabelAccounts, before.AutoLabelAccounts},
			{&st.AICorrectsOff, before.AICorrectsOff},
		} {
			next := []string{}
			for _, a := range append(slices.Clone(*l.cur), l.had...) {
				if a = mapID(a); !slices.Contains(next, a) {
					next = append(next, a)
				}
			}
			if !slices.Equal(next, *l.cur) {
				*l.cur = next
				changed = true
			}
		}
		return changed
	})
}

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// restoreAccount finds or recreates a backup's account (and restores its
// calendar choices). ok is false for an account that was skipped.
func (s *Service) restoreAccount(ba backupAccount, idMap map[string]string, sum *models.RestoreSummary) (acct models.Account, ok bool, err error) {
	email := strings.ToLower(strings.TrimSpace(ba.Email))
	if (ba.Provider != models.ProviderGmail && ba.Provider != models.ProviderOutlook) || !strings.Contains(email, "@") {
		sum.AccountsSkipped++
		return acct, false, nil
	}
	signature := ""
	if strings.TrimSpace(ba.Signature) != "" {
		if signature = mailmime.SanitizeHTML(ba.Signature); len(signature) > 64<<10 {
			signature = ""
		}
	}
	acct, err = s.store.FindAccount(ba.Provider, email)
	switch {
	case err == nil:
		sum.AccountsMerged++
		idMap[ba.ID] = acct.ID
		if acct.Signature == "" && signature != "" {
			if err := s.store.UpdateSignature(acct.ID, signature); err != nil {
				return acct, false, err
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
			return acct, false, err
		}
		if signature != "" {
			if err := s.store.UpdateSignature(id, signature); err != nil {
				return acct, false, err
			}
		}
		// A sign-in can survive in the OS keychain when only the data folder
		// was lost (same device, same account id): use it.
		if _, err := auth.LoadToken(s.secrets, id); err == nil {
			sum.AccountsReady++
		} else {
			if err := s.store.SetNeedsReauth(id, true); err != nil {
				return acct, false, err
			}
			_ = s.store.SetSyncResult(id, time.Time{}, restoredSyncError)
			sum.AccountsAdded++
		}
		slog.Info("account restored from backup", "provider", ba.Provider, "account", id)
		idMap[ba.ID] = id
		s.labelsGainAccount(id)
	default:
		return acct, false, err
	}
	prefs := slices.Clone(ba.Calendars)
	for i := range prefs {
		if prefs[i].Color != "" && !config.IsHexColor(prefs[i].Color) {
			prefs[i].Color = ""
		}
	}
	cals, err := s.store.AddPendingCalendars(acct.Provider, acct.Email, prefs)
	if err != nil {
		return acct, false, err
	}
	sum.Calendars += cals
	return acct, true, nil
}

// restoreMarks queues a backup account's label marks for acct, with the
// label ids as they are here (labels maps the backup's ids), and applies
// them to the messages already present.
func (s *Service) restoreMarks(ba backupAccount, acct models.Account, labels map[string]string, known map[string]bool, sum *models.RestoreSummary) error {
	marks := make([]store.LabelMark, 0, len(ba.LabelMarks))
	for _, m := range ba.LabelMarks {
		if m.Kind != store.MarkClassified {
			if id, ok := labels[m.LabelID]; ok {
				m.LabelID = id
			}
			if !known[m.LabelID] {
				continue
			}
		}
		marks = append(marks, m)
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
	return nil
}
