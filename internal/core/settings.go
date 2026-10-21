package core

import (
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
)

const (
	// dismissedLabelsKey held the deleted provider labels of every mailbox
	// (lower-cased names) before labels were per mailbox; migrateLabels
	// moves it to dismissedMailboxLabelsKey.
	dismissedLabelsKey = "dismissedProviderLabels"
	// dismissedMailboxLabelsKey maps an account id to the lower-cased names
	// of the provider labels the user deleted there: sync doesn't import
	// them again.
	dismissedMailboxLabelsKey = "dismissedMailboxLabels"
	// labelsVersionKey records the one-time label migrations that ran
	// (labelsVersion: labels belong to one mailbox each).
	labelsVersionKey = "labelsVersion"
	labelsVersion    = 1
)

func (s *Service) loadSettings() error {
	st := config.DefaultSettings()
	if err := s.store.GetSetting(settingsKey, &st); err != nil && !errors.Is(err, store.ErrNotFound) {
		var syn *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if !errors.As(err, &syn) && !errors.As(err, &typ) {
			return err
		}
		// Damaged settings must not keep gomail from starting: keep every
		// field that still decodes, default the rest, and save a copy.
		st = s.salvageSettings()
	}
	migrated := config.MigrateAutoLabel(&st, s.accountIDs())
	config.Normalize(&st)
	// Labels shared by mailboxes (older versions), or the old auto-label switch.
	if s.migrateLabels(&st) || migrated {
		_ = s.persist(st)
	}
	s.settingsMu.Lock()
	if s.labelsRev == 0 {
		s.labelsRev = 1 // 0 in a save means "not read through GetSettings"
	}
	st.LabelsRev = 0
	s.settings = st
	s.settingsMu.Unlock()
	s.store.SetLabels(st.Labels)
	s.store.SetHideProviderLabels(!st.ImportProviderLabels)
	return nil
}

// salvageSettings decodes the stored settings field by field.
func (s *Service) salvageSettings() models.Settings {
	st := config.DefaultSettings()
	data, err := s.store.RawSetting(settingsKey)
	if err != nil {
		return st
	}
	_ = s.store.PutRawSetting(settingsKey+".damaged", data)
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		slog.Error("settings are damaged; using defaults (a copy was kept as settings.damaged)")
		return st
	}
	bad := 0
	for k, v := range fields {
		one, _ := json.Marshal(map[string]json.RawMessage{k: v})
		if json.Unmarshal(one, &st) != nil {
			bad++
		}
	}
	slog.Warn("some settings were damaged and reset to defaults", "fields", bad)
	return st
}

// Settings returns a copy of the current settings (without key status).
func (s *Service) Settings() models.Settings {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	st := s.settings
	st.Labels = slices.Clone(st.Labels)
	return st
}

// GetSettings returns the settings for the UI.
func (s *Service) GetSettings() models.Settings {
	st := s.Settings()
	s.settingsMu.Lock()
	st.LabelsRev = s.labelsRev
	s.settingsMu.Unlock()
	key, _ := s.openRouterKey()
	st.OpenRouterKeySet = key != ""
	return st
}

func (s *Service) persist(st models.Settings) error {
	st.OpenRouterKeySet, st.LabelsRev = false, 0
	return s.store.SetSetting(settingsKey, st)
}

// SaveSettings validates and stores settings. Labels removed from the list
// lose their assignments; removed provider labels are not re-imported.
//
// in.LabelsRev tells which label list the caller read (GetSettings): a
// label the backend added after that (a sync imported it while the
// settings window had its copy) can't have been deleted by the user and is
// kept. Callers that didn't read the settings through GetSettings (0) are
// trusted; the labels they add count as added by the backend.
func (s *Service) SaveSettings(in models.Settings) error {
	config.Normalize(&in)
	s.settingsMu.Lock()
	old := s.settings
	readAt := in.LabelsRev
	in.LabelsRev = 0
	if readAt > 0 && readAt < s.labelsRev {
		ids, names := map[string]bool{}, map[string]bool{}
		for _, l := range in.Labels {
			ids[l.ID] = true
			names[labelKey(l)] = true
		}
		for _, l := range old.Labels {
			if s.labelAdded[l.ID] > readAt && !ids[l.ID] && !names[labelKey(l)] {
				in.Labels = append(in.Labels, l)
			}
		}
		// Labels the backend dropped since (their mailbox was removed)
		// don't come back from the stale copy.
		in.Labels = slices.DeleteFunc(in.Labels, func(l models.Label) bool {
			return s.labelDropped[l.ID] > readAt && !slices.ContainsFunc(old.Labels, func(o models.Label) bool { return o.ID == l.ID })
		})
	}
	// Keep provider/system metadata the UI may not round-trip.
	prev := map[string]models.Label{}
	for _, l := range old.Labels {
		prev[l.ID] = l
	}
	for i, l := range in.Labels {
		p, ok := prev[l.ID]
		if !ok {
			continue
		}
		if l.Source == config.LabelSourceUser && p.Source != config.LabelSourceUser {
			in.Labels[i].Source = p.Source
		}
		if p.Source == config.LabelSourceProvider {
			// A provider label belongs to the mailboxes it was imported
			// from; the UI doesn't edit that (and may hold a stale copy).
			in.Labels[i].AccountIDs = slices.Clone(p.AccountIDs)
		}
	}
	in.Labels = config.NormalizeLabels(in.Labels)
	s.splitLabels(&in, false) // a label of several mailboxes becomes one per mailbox
	if err := s.persist(in); err != nil {
		s.settingsMu.Unlock()
		return err
	}
	if readAt == 0 {
		s.labelsChangedLocked(old.Labels, in.Labels)
	}
	s.settings = in
	s.settingsMu.Unlock()

	// Forget assignments of deleted labels; remember deleted provider labels.
	valid := make([]string, 0, len(in.Labels))
	kept := map[string]bool{}
	for _, l := range in.Labels {
		valid = append(valid, l.ID)
		kept[l.ID] = true
	}
	dismissed := s.dismissedLabels()
	changed := false
	for _, l := range old.Labels {
		if !kept[l.ID] && l.Source == config.LabelSourceProvider {
			for _, a := range l.AccountIDs {
				if n := strings.ToLower(l.Name); !slices.Contains(dismissed[a], n) {
					dismissed[a] = append(dismissed[a], n)
					changed = true
				}
			}
		}
	}
	for _, l := range in.Labels {
		for _, a := range l.AccountIDs {
			if i := slices.Index(dismissed[a], strings.ToLower(l.Name)); i >= 0 {
				dismissed[a] = slices.Delete(dismissed[a], i, i+1)
				changed = true
			}
		}
	}
	if changed {
		_ = s.store.SetSetting(dismissedMailboxLabelsKey, dismissed)
	}
	if err := s.store.DeleteUnknownLabels(valid); err != nil {
		return err
	}
	s.store.SetLabels(in.Labels)
	s.store.SetHideProviderLabels(!in.ImportProviderLabels)

	if old.GoogleClientID != in.GoogleClientID || old.GoogleClientSecret != in.GoogleClientSecret ||
		old.MicrosoftClientID != in.MicrosoftClientID || old.MicrosoftTenant != in.MicrosoftTenant {
		s.resetProviders()
	}
	if old.SyncIntervalMinutes != in.SyncIntervalMinutes {
		s.engine.Wake()
	}
	s.ui.ApplySettings(in)
	s.emit(models.EventLabelsChanged, models.AccountEvent{})
	s.emit(models.EventMessagesChanged, models.AccountEvent{})
	return nil
}

// resolveProviderLabels finds the labels of a mailbox for its Gmail labels
// or Outlook categories by case-insensitive name, adding the missing ones
// to that mailbox, and returns lower-cased name -> label id. Mailboxes
// never share labels: the same name in another mailbox is another label.
func (s *Service) resolveProviderLabels(accountID string, labels []mail.Label) map[string]string {
	dismissed := s.dismissedLabels()[accountID]
	s.settingsMu.Lock()
	st := s.settings
	st.Labels = slices.Clone(st.Labels)
	out := map[string]string{}
	changed := false
	for _, pl := range labels {
		name := strings.TrimSpace(pl.Name)
		lower := strings.ToLower(name)
		if name == "" {
			continue
		}
		if l, ok := config.LabelNamed(st.Labels, accountID, name); ok {
			out[lower] = l.ID
			i := slices.IndexFunc(st.Labels, func(x models.Label) bool { return x.ID == l.ID })
			if l.Source == config.LabelSourceProvider && pl.Color != "" && l.Color != pl.Color && config.IsHexColor(pl.Color) {
				st.Labels[i].Color = pl.Color
				changed = true
			}
			continue
		}
		if slices.Contains(dismissed, lower) {
			continue
		}
		id := config.NewLabelID(st.Labels, name)
		st.Labels = config.NormalizeLabels(append(st.Labels, models.Label{ID: id, Name: name, Color: pl.Color, Source: config.LabelSourceProvider, AccountIDs: []string{accountID}}))
		if slices.ContainsFunc(st.Labels, func(l models.Label) bool { return l.ID == id }) {
			out[lower] = id
			changed = true
		}
	}
	if changed {
		if err := s.persist(st); err == nil {
			s.labelsChangedLocked(s.settings.Labels, st.Labels)
			s.settings = st
		}
	}
	s.settingsMu.Unlock()
	if changed {
		s.store.SetLabels(st.Labels)
		s.emit(models.EventLabelsChanged, models.AccountEvent{AccountID: accountID})
	}
	return out
}

// labelKey identifies a label by mailbox and lower-cased name (names are
// unique within a mailbox).
func labelKey(l models.Label) string {
	return strings.Join(l.AccountIDs, ",") + "\x00" + strings.ToLower(strings.TrimSpace(l.Name))
}

// dismissedLabels returns account id -> lower-cased names of the provider
// labels the user deleted in that mailbox.
func (s *Service) dismissedLabels() map[string][]string {
	m := map[string][]string{}
	_ = s.store.GetSetting(dismissedMailboxLabelsKey, &m)
	if m == nil {
		m = map[string][]string{}
	}
	return m
}

// labelsChangedLocked records the labels of next that aren't in prev as
// added by the backend, and those of prev missing from next as dropped by
// it (settingsMu held; see SaveSettings).
func (s *Service) labelsChangedLocked(prev, next []models.Label) {
	if s.labelAdded == nil {
		s.labelAdded, s.labelDropped = map[string]int64{}, map[string]int64{}
	}
	had := make(map[string]bool, len(prev))
	for _, l := range prev {
		had[l.ID] = true
	}
	has := make(map[string]bool, len(next))
	for _, l := range next {
		has[l.ID] = true
		if !had[l.ID] {
			s.labelsRev++
			s.labelAdded[l.ID] = s.labelsRev
		}
	}
	for _, l := range prev {
		if !has[l.ID] {
			s.labelsRev++
			s.labelDropped[l.ID] = s.labelsRev
		}
	}
}

// ---- labels per mailbox ----

// accountIDs lists the ids of the accounts on this computer.
func (s *Service) accountIDs() []string {
	accts, err := s.store.Accounts()
	if err != nil {
		return nil
	}
	ids := make([]string, len(accts))
	for i, a := range accts {
		ids[i] = a.ID
	}
	return ids
}

// migrateLabels runs on startup: labels that several mailboxes share
// (older versions) become one label per mailbox (splitLabels). The first
// time, it also moves the deleted provider labels to per-mailbox lists and
// drops the classifier's guesses of imported labels without a description
// (it is no longer offered them). It reports whether st changed.
func (s *Service) migrateLabels(st *models.Settings) bool {
	var ver int
	_ = s.store.GetSetting(labelsVersionKey, &ver)
	legacy := ver < labelsVersion
	if legacy && len(s.accountIDs()) > 0 && slices.ContainsFunc(st.Labels, func(l models.Label) bool { return l.Source != config.LabelSourceSystem }) {
		slog.Info("labels: giving every mailbox its own labels")
	}
	changed := s.splitLabels(st, legacy)
	if !legacy {
		return changed
	}
	var guesses []string
	for _, l := range st.Labels {
		if l.Source == config.LabelSourceProvider && l.Description == "" {
			guesses = append(guesses, l.ID)
		}
	}
	if err := s.store.DeleteAIAssignments(guesses); err != nil {
		slog.Warn("labels: removing AI guesses of imported labels failed", "err", err)
	}
	var old []string
	if err := s.store.GetSetting(dismissedLabelsKey, &old); err == nil && len(old) > 0 {
		dismissed := s.dismissedLabels()
		for _, a := range s.accountIDs() {
			for _, n := range old {
				if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !slices.Contains(dismissed[a], n) {
					dismissed[a] = append(dismissed[a], n)
				}
			}
		}
		if s.store.SetSetting(dismissedMailboxLabelsKey, dismissed) == nil {
			_ = s.store.SetSetting(dismissedLabelsKey, []string{})
		}
	}
	_ = s.store.SetSetting(labelsVersionKey, labelsVersion)
	return changed
}

// splitLabels gives every mailbox its own copy of the labels it shares
// with others (config.SplitLabels) and moves the mailbox's assignments to
// its copy. Mail that was AI-labeled already keeps its fingerprint, so the
// new ids don't make it go to the classifier (and cost) again; legacy
// means the fingerprints are from before imported labels without a
// description were left out. A provider label without mailboxes (older
// versions) first gets the mailboxes whose mail carries it. It reports
// whether st changed.
func (s *Service) splitLabels(st *models.Settings, legacy bool) bool {
	ids := s.accountIDs()
	if len(ids) == 0 {
		return false
	}
	before := *st
	before.Labels = slices.Clone(st.Labels)
	scoped := false
	var seen map[string][]string
	for i, l := range st.Labels {
		if l.Source != config.LabelSourceProvider || len(l.AccountIDs) > 0 {
			continue
		}
		if seen == nil {
			if seen, _ = s.store.LabelAccounts(); seen == nil {
				seen = map[string][]string{}
			}
		}
		if a := seen[l.ID]; len(a) > 0 {
			st.Labels[i].AccountIDs = slices.Clone(a)
			scoped = true
		}
	}
	moves, split := config.SplitLabels(st, ids)
	if split {
		if err := s.store.MoveLabels(moves); err != nil {
			slog.Warn("labels: giving mailboxes their own labels failed", "err", err)
			*st = before
			return false
		}
		config.Normalize(st)
	}
	if split || legacy {
		for _, a := range ids {
			was := labelDefsOf(before.Labels, a)
			if legacy {
				was = nil
				for _, l := range config.LabelsForAccount(before.Labels, a) {
					was = append(was, ai.LabelDef{ID: l.ID, Name: l.Name, Description: l.Description})
				}
			}
			if err := s.store.RenameAIHashes(a, labelsetHashOf(before, was), labelsetHashOf(*st, labelDefsOf(st.Labels, a))); err != nil {
				slog.Warn("labels: keeping AI fingerprints failed", "account", a, "err", err)
			}
		}
	}
	return split || scoped
}

// updateLabels changes the stored settings' labels outside SaveSettings
// (accounts added or removed). fn reports whether it changed anything.
func (s *Service) updateLabels(fn func(st *models.Settings) bool) {
	s.settingsMu.Lock()
	st := s.settings
	st.Labels = slices.Clone(st.Labels)
	st.LabelRules = slices.Clone(st.LabelRules)
	st.MutedMailboxes = slices.Clone(st.MutedMailboxes)
	st.AutoLabelAccounts = slices.Clone(st.AutoLabelAccounts)
	st.AICorrectsOff = slices.Clone(st.AICorrectsOff)
	st.JunkStaysPut = slices.Clone(st.JunkStaysPut)
	st.QuietLabels = maps.Clone(st.QuietLabels)
	st.NotJunkSenders = maps.Clone(st.NotJunkSenders)
	if !fn(&st) {
		s.settingsMu.Unlock()
		return
	}
	if err := s.persist(st); err != nil {
		s.settingsMu.Unlock()
		slog.Warn("saving label mailboxes failed", "err", err)
		return
	}
	s.labelsChangedLocked(s.settings.Labels, st.Labels)
	s.settings = st
	s.settingsMu.Unlock()
	s.store.SetLabels(st.Labels)
	s.emit(models.EventLabelsChanged, models.AccountEvent{})
}

// labelsGainAccount runs when a mailbox is added: it gets its own copies
// of the default labels (unless it has labels already: a restored backup
// brings its own). Likewise, when Jev auto-labels every mailbox so far, it
// auto-labels the new one too.
func (s *Service) labelsGainAccount(accountID string) {
	var others []string
	for _, id := range s.accountIDs() {
		if id != accountID {
			others = append(others, id)
		}
	}
	s.updateLabels(func(st *models.Settings) bool {
		changed := s.ownLabels(st, accountID)
		if len(others) > 0 && !slices.Contains(st.AutoLabelAccounts, accountID) && allIn(others, st.AutoLabelAccounts) {
			st.AutoLabelAccounts = append(st.AutoLabelAccounts, accountID)
			changed = true
		}
		return changed
	})
}

// ownLabels gives a mailbox without labels its own copies of the default
// labels; labels without a mailbox (settings saved before there was one)
// go to every mailbox first. It reports whether st changed.
func (s *Service) ownLabels(st *models.Settings, accountID string) bool {
	changed := s.splitLabels(st, false)
	if slices.ContainsFunc(st.Labels, func(l models.Label) bool {
		return l.Source != config.LabelSourceSystem && slices.Contains(l.AccountIDs, accountID)
	}) {
		return changed
	}
	st.Labels = config.NormalizeLabels(append(st.Labels, config.MailboxDefaultLabels(st.Labels, accountID)...))
	return true
}

// labelsLoseAccount runs when a mailbox is removed: its labels, rules,
// quiet labels, notification mute and other per-mailbox choices go with
// it.
func (s *Service) labelsLoseAccount(accountID string) {
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		rules := st.LabelRules[:0:0]
		for _, r := range st.LabelRules {
			if r.AccountID == accountID {
				changed = true
				continue
			}
			rules = append(rules, r)
		}
		st.LabelRules = rules
		if _, ok := st.QuietLabels[accountID]; ok {
			quiet := maps.Clone(st.QuietLabels)
			delete(quiet, accountID)
			st.QuietLabels = quiet
			changed = true
		}
		if i := slices.Index(st.MutedMailboxes, accountID); i >= 0 {
			st.MutedMailboxes = slices.Delete(st.MutedMailboxes, i, i+1)
			changed = true
		}
		if _, ok := st.NotJunkSenders[accountID]; ok {
			delete(st.NotJunkSenders, accountID)
			changed = true
		}
		out := st.Labels[:0:0]
		for _, l := range st.Labels {
			if i := slices.Index(l.AccountIDs, accountID); i >= 0 {
				changed = true
				if len(l.AccountIDs) == 1 {
					continue
				}
				l.AccountIDs = slices.Delete(slices.Clone(l.AccountIDs), i, i+1)
			}
			out = append(out, l)
		}
		st.Labels = out
		// The mailbox's per-mailbox choices go with it.
		for _, list := range []*[]string{&st.AutoLabelAccounts, &st.AICorrectsOff, &st.JunkStaysPut} {
			if i := slices.Index(*list, accountID); i >= 0 {
				*list = slices.Delete(slices.Clone(*list), i, i+1)
				changed = true
			}
		}
		if changed {
			// Rules and quiet lists drop the labels that went.
			config.Normalize(st)
		}
		return changed
	})
	valid := []string{}
	for _, l := range s.Settings().Labels {
		valid = append(valid, l.ID)
	}
	_ = s.store.DeleteUnknownLabels(valid)
	if d := s.dismissedLabels(); d[accountID] != nil {
		delete(d, accountID)
		_ = s.store.SetSetting(dismissedMailboxLabelsKey, d)
	}
}

// ---- OpenRouter key ----

func (s *Service) openRouterKey() (string, error) {
	k, err := s.secrets.Get(openRouterKeyName)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", nil
	}
	return k, err
}

// SetOpenRouterKey stores (or with "" removes) the OpenRouter API key.
func (s *Service) SetOpenRouterKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return s.secrets.Delete(openRouterKeyName)
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return errors.New("that doesn't look like an OpenRouter key (it contains spaces)")
	}
	return s.secrets.Set(openRouterKeyName, key)
}

// DefaultPrompts returns the built-in prompts.
func DefaultPrompts() models.DefaultPrompts {
	return models.DefaultPrompts{Grammar: ai.DefaultGrammarPrompt, Classify: ai.DefaultClassifyInstructions}
}

// allIn reports whether every id of ids is in list.
func allIn(ids, list []string) bool {
	for _, id := range ids {
		if !slices.Contains(list, id) {
			return false
		}
	}
	return true
}
