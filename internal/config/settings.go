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
	DefaultPrimaryColor      = "#2563eb"
	DefaultRadius            = 0.5
)

// DefaultCategories are seeded on first run.
func DefaultCategories() []models.Category {
	return []models.Category{
		{ID: "important", Name: "Important", Color: "#ef4444", Description: "Time-sensitive or high-priority mail that needs the user's attention or action soon: requests from real people, deadlines, security alerts, appointments."},
		{ID: "personal", Name: "Personal", Color: "#ec4899", Description: "Personal conversations with friends and family; not work, not automated."},
		{ID: "work", Name: "Work", Color: "#3b82f6", Description: "Work-related conversations with colleagues, clients or partners, meetings, projects, documents to review."},
		{ID: "newsletters", Name: "Newsletters", Color: "#8b5cf6", Description: "Editorial newsletters, digests, blogs and publications the user subscribed to."},
		{ID: "promotions", Name: "Promotions", Color: "#f59e0b", Description: "Marketing, sales, deals, discounts, product announcements and advertising."},
		{ID: "social", Name: "Social", Color: "#06b6d4", Description: "Notifications from social networks and community sites (LinkedIn, Facebook, X, Discord, forums)."},
		{ID: "notifications", Name: "Notifications", Color: "#64748b", Description: "Automated notifications from apps and services: shipping updates, account notices, CI/build results, calendar notices."},
		{ID: "finance", Name: "Finance", Color: "#10b981", Description: "Receipts, invoices, bills, bank and payment notices, statements, taxes."},
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
		Categories:          DefaultCategories(),
		PrimaryColor:        DefaultPrimaryColor,
		Radius:              DefaultRadius,
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
	if s.ClassifyThreshold < 0 || s.ClassifyThreshold > 1 {
		s.ClassifyThreshold = DefaultClassifyThreshold
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
	s.Categories = normalizeCategories(s.Categories)
}

func normalizeCategories(in []models.Category) []models.Category {
	out := make([]models.Category, 0, len(in))
	seen := map[string]bool{"none": true} // "none" is reserved by the classifier
	for i, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Description = strings.TrimSpace(c.Description)
		if c.Name == "" {
			continue
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
		c.ID = id
		if !IsHexColor(c.Color) {
			c.Color = categoryPalette[i%len(categoryPalette)]
		}
		out = append(out, c)
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
