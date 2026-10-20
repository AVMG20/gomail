package core

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/AVMG20/gomail/internal/ai"
	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/store"
)

const dismissedLabelsKey = "dismissedProviderLabels"

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
	config.Normalize(&st)
	if s.scopeLabels(st.Labels) {
		_ = s.persist(st) // labels from before labels were per mailbox
	}
	s.settingsMu.Lock()
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
	key, _ := s.openRouterKey()
	st.OpenRouterKeySet = key != ""
	return st
}

func (s *Service) persist(st models.Settings) error {
	st.OpenRouterKeySet = false
	return s.store.SetSetting(settingsKey, st)
}

// SaveSettings validates and stores settings. Labels removed from the list
// lose their assignments; removed provider labels are not re-imported.
func (s *Service) SaveSettings(in models.Settings) error {
	config.Normalize(&in)
	s.settingsMu.Lock()
	old := s.settings
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
	s.scopeLabels(in.Labels)
	if err := s.persist(in); err != nil {
		s.settingsMu.Unlock()
		return err
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
	var dismissed []string
	_ = s.store.GetSetting(dismissedLabelsKey, &dismissed)
	changed := false
	for _, l := range old.Labels {
		if !kept[l.ID] && l.Source == config.LabelSourceProvider {
			dismissed = append(dismissed, strings.ToLower(l.Name))
			changed = true
		}
	}
	for _, l := range in.Labels {
		if i := slices.Index(dismissed, strings.ToLower(l.Name)); i >= 0 {
			dismissed = slices.Delete(dismissed, i, i+1)
			changed = true
		}
	}
	if changed {
		_ = s.store.SetSetting(dismissedLabelsKey, dismissed)
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

// resolveProviderLabels merges provider labels into the label list by
// case-insensitive name and returns lower-cased name -> label id.
func (s *Service) resolveProviderLabels(accountID string, labels []mail.Label) map[string]string {
	var dismissed []string
	_ = s.store.GetSetting(dismissedLabelsKey, &dismissed)
	s.settingsMu.Lock()
	st := s.settings
	st.Labels = slices.Clone(st.Labels)
	byName := map[string]int{}
	for i, l := range st.Labels {
		byName[strings.ToLower(l.Name)] = i
	}
	out := map[string]string{}
	changed := false
	for _, pl := range labels {
		name := strings.TrimSpace(pl.Name)
		lower := strings.ToLower(name)
		if name == "" {
			continue
		}
		if i, ok := byName[lower]; ok {
			out[lower] = st.Labels[i].ID
			// The same name in another mailbox is the same label: it now
			// belongs there too (its mail carries it).
			if l := st.Labels[i]; l.Source != config.LabelSourceSystem && len(l.AccountIDs) > 0 && !slices.Contains(l.AccountIDs, accountID) {
				st.Labels[i].AccountIDs = append(slices.Clone(l.AccountIDs), accountID)
				changed = true
			}
			if st.Labels[i].Source == config.LabelSourceProvider && pl.Color != "" && st.Labels[i].Color != pl.Color && config.IsHexColor(pl.Color) {
				st.Labels[i].Color = pl.Color
				changed = true
			}
			continue
		}
		if slices.Contains(dismissed, lower) {
			continue
		}
		st.Labels = append(st.Labels, models.Label{Name: name, Color: pl.Color, Source: config.LabelSourceProvider, AccountIDs: []string{accountID}})
		st.Labels = config.NormalizeLabels(st.Labels)
		added := st.Labels[len(st.Labels)-1]
		for _, l := range st.Labels { // NormalizeLabels may append Junk last
			if strings.EqualFold(l.Name, name) {
				added = l
			}
		}
		byName[lower] = slices.IndexFunc(st.Labels, func(l models.Label) bool { return l.ID == added.ID })
		out[lower] = added.ID
		changed = true
	}
	if changed {
		if err := s.persist(st); err == nil {
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

// ---- mailbox scope of labels ----

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

// scopeLabels gives labels without a mailbox list their mailboxes:
// provider labels the accounts whose mail carries them, every other label
// every mailbox (see config.ScopeLabels). It reports whether a label
// changed.
func (s *Service) scopeLabels(labels []models.Label) bool {
	ids := s.accountIDs()
	if len(ids) == 0 {
		return false
	}
	changed := false
	var seen map[string][]string
	for i, l := range labels {
		if l.Source != config.LabelSourceProvider || len(l.AccountIDs) > 0 {
			continue
		}
		if seen == nil {
			if seen, _ = s.store.LabelAccounts(); seen == nil {
				seen = map[string][]string{}
			}
		}
		if a := seen[l.ID]; len(a) > 0 {
			labels[i].AccountIDs = slices.Clone(a)
			changed = true
		}
	}
	return config.ScopeLabels(labels, ids) || changed
}

// updateLabels changes the stored settings' labels outside SaveSettings
// (accounts added or removed). fn reports whether it changed anything.
func (s *Service) updateLabels(fn func(st *models.Settings) bool) {
	s.settingsMu.Lock()
	st := s.settings
	st.Labels = slices.Clone(st.Labels)
	st.LabelRules = slices.Clone(st.LabelRules)
	st.MutedMailboxes = slices.Clone(st.MutedMailboxes)
	if !fn(&st) {
		s.settingsMu.Unlock()
		return
	}
	if err := s.persist(st); err != nil {
		s.settingsMu.Unlock()
		slog.Warn("saving label mailboxes failed", "err", err)
		return
	}
	s.settings = st
	s.settingsMu.Unlock()
	s.store.SetLabels(st.Labels)
	s.emit(models.EventLabelsChanged, models.AccountEvent{})
}

// labelsGainAccount runs when a mailbox is added: user labels that belonged
// to every mailbox so far ("All") belong to the new one too, and labels
// without a list get one.
func (s *Service) labelsGainAccount(accountID string) {
	var others []string
	for _, id := range s.accountIDs() {
		if id != accountID {
			others = append(others, id)
		}
	}
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		for i, l := range st.Labels {
			if l.Source != config.LabelSourceUser || len(l.AccountIDs) == 0 || slices.Contains(l.AccountIDs, accountID) {
				continue
			}
			all := true
			for _, a := range others {
				if !slices.Contains(l.AccountIDs, a) {
					all = false
					break
				}
			}
			if all {
				st.Labels[i].AccountIDs = append(slices.Clone(l.AccountIDs), accountID)
				changed = true
			}
		}
		return s.scopeLabels(st.Labels) || changed
	})
}

// labelsLoseAccount runs when a mailbox is removed: labels forget it.
// Provider labels of only that mailbox are dropped (their mail is gone);
// user labels left without a mailbox belong to every remaining one.
func (s *Service) labelsLoseAccount(accountID string) {
	s.updateLabels(func(st *models.Settings) bool {
		changed := false
		out := st.Labels[:0:0]
		for _, l := range st.Labels {
			if i := slices.Index(l.AccountIDs, accountID); i >= 0 {
				l.AccountIDs = slices.Delete(slices.Clone(l.AccountIDs), i, i+1)
				changed = true
				if len(l.AccountIDs) == 0 && l.Source == config.LabelSourceProvider {
					continue
				}
			}
			out = append(out, l)
		}
		st.Labels = out
		return s.scopeLabels(st.Labels) || changed
	})
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
