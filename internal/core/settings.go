package core

import (
	"errors"
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
		return err
	}
	config.Normalize(&st)
	s.settingsMu.Lock()
	s.settings = st
	s.settingsMu.Unlock()
	s.store.SetLabels(st.Labels)
	s.store.SetHideProviderLabels(!st.ImportProviderLabels)
	return nil
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
		if p, ok := prev[l.ID]; ok && l.Source == config.LabelSourceUser && p.Source != config.LabelSourceUser {
			in.Labels[i].Source = p.Source
		}
	}
	in.Labels = config.NormalizeLabels(in.Labels)
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
			if st.Labels[i].Source == config.LabelSourceProvider && pl.Color != "" && st.Labels[i].Color != pl.Color && config.IsHexColor(pl.Color) {
				st.Labels[i].Color = pl.Color
				changed = true
			}
			continue
		}
		if slices.Contains(dismissed, lower) {
			continue
		}
		st.Labels = append(st.Labels, models.Label{Name: name, Color: pl.Color, Source: config.LabelSourceProvider})
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
