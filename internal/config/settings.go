package config

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/AVMG20/gomail/internal/models"
)

// Defaults for settings.
const (
	DefaultTheme             = "system"
	DefaultDensity           = "comfortable"
	DefaultSyncInterval      = 5
	DefaultTenant            = "common"
	DefaultChatModel         = "openai/gpt-4o-mini"
	DefaultClassifierModel   = "typesafe/jev-1.13"
	DefaultClassifyThreshold = 0.5
	DefaultRemoveThreshold   = 0.15
	DefaultPrimaryColor      = "#2563eb"
	DefaultRadius            = 0.5
)

// JunkLabelID is the built-in label given to mail in Spam/Junk folders.
const JunkLabelID = "junk"

// Label sources.
const (
	LabelSourceUser     = "user"
	LabelSourceProvider = "provider"
	LabelSourceSystem   = "system"
)

// JunkLabel is the built-in system label for mail in Spam/Junk.
func JunkLabel() models.Label {
	return models.Label{ID: JunkLabelID, Name: "Junk", Color: "#a16207", Source: LabelSourceSystem,
		Description: "Unsolicited bulk email, scams, phishing attempts or other spam."}
}

// DefaultLabels are seeded on first run.
func DefaultLabels() []models.Label {
	return []models.Label{
		{Source: LabelSourceUser, ID: "important", Name: "Important", Color: "#ef4444", Description: "Time-sensitive or high-priority mail that needs the user's attention or action soon: requests from real people, deadlines, security alerts, appointments."},
		{Source: LabelSourceUser, ID: "personal", Name: "Personal", Color: "#ec4899", Description: "Personal conversations with friends and family; not work, not automated."},
		{Source: LabelSourceUser, ID: "work", Name: "Work", Color: "#3b82f6", Description: "Work-related conversations with colleagues, clients or partners, meetings, projects, documents to review."},
		{Source: LabelSourceUser, ID: "newsletters", Name: "Newsletters", Color: "#8b5cf6", Description: "Editorial newsletters, digests, blogs and publications the user subscribed to."},
		{Source: LabelSourceUser, ID: "promotions", Name: "Promotions", Color: "#f59e0b", Description: "Marketing, sales, deals, discounts, product announcements and advertising."},
		{Source: LabelSourceUser, ID: "social", Name: "Social", Color: "#06b6d4", Description: "Notifications from social networks and community sites (LinkedIn, Facebook, X, Discord, forums)."},
		{Source: LabelSourceUser, ID: "notifications", Name: "Notifications", Color: "#64748b", Description: "Automated notifications from apps and services: shipping updates, account notices, CI/build results, calendar notices."},
		{Source: LabelSourceUser, ID: "finance", Name: "Finance", Color: "#10b981", Description: "Receipts, invoices, bills, bank and payment notices, statements, taxes."},
		JunkLabel(),
	}
}

// DefaultSettings returns the settings used before the user changed anything.
func DefaultSettings() models.Settings {
	return models.Settings{
		Theme:               DefaultTheme,
		Density:             DefaultDensity,
		SyncIntervalMinutes: DefaultSyncInterval,
		MicrosoftTenant:     DefaultTenant,
		OpenRouterModel:     DefaultChatModel,
		ClassifierModel:     DefaultClassifierModel,
		ClassifyThreshold:   DefaultClassifyThreshold,

		ClassifyRemoveThreshold:  DefaultRemoveThreshold,
		AICorrectsProviderLabels: true,
		ImportProviderLabels:     true,
		Labels:                   DefaultLabels(),
		PrimaryColor:             DefaultPrimaryColor,
		Radius:                   DefaultRadius,
	}
}

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// IsHexColor reports whether s is a #rgb, #rrggbb or #rrggbbaa color.
func IsHexColor(s string) bool { return hexColor.MatchString(s) }

var categoryPalette = []string{"#ef4444", "#f59e0b", "#10b981", "#3b82f6", "#8b5cf6", "#ec4899", "#06b6d4", "#64748b", "#84cc16", "#f97316"}

// Normalize validates s in place, replacing invalid values with defaults.
func Normalize(s *models.Settings) {
	switch s.Theme {
	case "system", "light", "dark":
	default:
		s.Theme = DefaultTheme
	}
	switch s.Density {
	case "comfortable", "compact":
	default:
		s.Density = DefaultDensity
	}
	if s.SyncIntervalMinutes <= 0 {
		s.SyncIntervalMinutes = DefaultSyncInterval
	}
	if s.SyncIntervalMinutes > 24*60 {
		s.SyncIntervalMinutes = 24 * 60
	}
	s.GoogleClientID = strings.TrimSpace(s.GoogleClientID)
	s.GoogleClientSecret = strings.TrimSpace(s.GoogleClientSecret)
	s.MicrosoftClientID = strings.TrimSpace(s.MicrosoftClientID)
	s.MicrosoftTenant = strings.TrimSpace(s.MicrosoftTenant)
	if s.MicrosoftTenant == "" {
		s.MicrosoftTenant = DefaultTenant
	}
	s.OpenRouterModel = strings.TrimSpace(s.OpenRouterModel)
	if s.OpenRouterModel == "" {
		s.OpenRouterModel = DefaultChatModel
	}
	s.ClassifierModel = strings.TrimSpace(s.ClassifierModel)
	if s.ClassifierModel == "" {
		s.ClassifierModel = DefaultClassifierModel
	}
	if s.ClassifyThreshold <= 0 || s.ClassifyThreshold > 1 {
		s.ClassifyThreshold = DefaultClassifyThreshold
	}
	if s.ClassifyRemoveThreshold < 0 || s.ClassifyRemoveThreshold >= s.ClassifyThreshold {
		s.ClassifyRemoveThreshold = min(DefaultRemoveThreshold, s.ClassifyThreshold/2)
	}
	s.GrammarSystemPrompt = strings.TrimSpace(s.GrammarSystemPrompt)
	s.ClassifyInstructions = strings.TrimSpace(s.ClassifyInstructions)
	s.PrimaryColor = strings.TrimSpace(s.PrimaryColor)
	if !IsHexColor(s.PrimaryColor) {
		s.PrimaryColor = DefaultPrimaryColor
	}
	if s.Radius < 0 || s.Radius > 2 {
		s.Radius = DefaultRadius
	}
	s.Labels = NormalizeLabels(s.Labels)
}

// NormalizeLabels validates label definitions: unique slug ids, unique
// names (case-insensitive), valid colors and sources, and makes sure the
// built-in Junk label exists.
func NormalizeLabels(in []models.Label) []models.Label {
	out := make([]models.Label, 0, len(in)+1)
	seen := map[string]bool{}
	names := map[string]bool{}
	hasJunk := false
	for i, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Description = strings.TrimSpace(c.Description)
		if c.Name == "" || names[strings.ToLower(c.Name)] {
			continue
		}
		if c.ID == JunkLabelID {
			j := JunkLabel()
			if c.Description != "" {
				j.Description = c.Description
			}
			if IsHexColor(c.Color) {
				j.Color = c.Color
			}
			j.Name = c.Name
			c = j
			hasJunk = true
		}
		switch c.Source {
		case LabelSourceProvider, LabelSourceSystem:
		default:
			c.Source = LabelSourceUser
		}
		if c.Source == LabelSourceSystem && c.ID != JunkLabelID {
			c.Source = LabelSourceUser
		}
		id := Slug(c.ID)
		if id == "" {
			id = Slug(c.Name)
		}
		if id == "" {
			id = "label"
		}
		base, n := id, 2
		for seen[id] {
			id = base + "-" + strconv.Itoa(n)
			n++
		}
		seen[id] = true
		names[strings.ToLower(c.Name)] = true
		c.ID = id
		if !IsHexColor(c.Color) {
			c.Color = categoryPalette[i%len(categoryPalette)]
		}
		out = append(out, c)
	}
	if !hasJunk && !seen[JunkLabelID] {
		out = append(out, JunkLabel())
	}
	return out
}

// Slug turns a label name into a stable lowercase identifier.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
