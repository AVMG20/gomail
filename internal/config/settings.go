package config

import (
	"regexp"
	"slices"
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

// DefaultQuietLabels are the labels that don't notify for a mailbox that
// has no list of its own.
var DefaultQuietLabels = []string{JunkLabelID}

// QuietLabels returns the labels whose new mail doesn't notify for an account.
func QuietLabels(s models.Settings, accountID string) []string {
	if l, ok := s.QuietLabels[accountID]; ok {
		return l
	}
	return DefaultQuietLabels
}

// Label sources.
const (
	LabelSourceUser     = "user"
	LabelSourceProvider = "provider"
	LabelSourceSystem   = "system"
)

// What happens to new mail that gets a label (Label.Then); "" keeps it
// where it is.
const (
	LabelThenArchive = "archive"
	LabelThenJunk    = "junk"
	LabelThenTrash   = "trash"
)

// JunkLabel is the built-in system label for mail in Spam/Junk.
func JunkLabel() models.Label {
	return models.Label{ID: JunkLabelID, Name: "Junk", Color: "#a16207", Source: LabelSourceSystem,
		Description: "Unsolicited bulk email, scams, phishing attempts or other spam."}
}

// DefaultLabels are the labels every new mailbox starts with (each
// mailbox gets its own copies, see MailboxDefaultLabels), plus Junk.
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

// MailboxDefaultLabels returns a mailbox's own copies of the default user
// labels. The ids are unique among labels (a copy whose id is taken gets
// "-2", "-3", ...).
func MailboxDefaultLabels(labels []models.Label, accountID string) []models.Label {
	taken := labelIDs(labels)
	var out []models.Label
	for _, l := range DefaultLabels() {
		if l.Source != LabelSourceUser {
			continue
		}
		l.ID = uniqueID(taken, l.ID)
		l.AccountIDs = []string{accountID}
		out = append(out, l)
	}
	return out
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
		Notifications:            true,
		ConversationView:         true,
		UndoSendSeconds:          5,
		MarkReadMode:             "immediate",
		ReadingPane:              "right",
		ReplyQuote:               "header",
		EmailBackground:          "auto",
		CalendarWeekStart:        "auto",
		CalendarTimeFormat:       "auto",
		CalendarDefaultView:      "week",
		CalendarDefaultDuration:  30,
		CalendarDefaultReminder:  10,
		CalendarNotifications:    true,
		Zoom:                     1,
		SyncDepthDays:            90,
		AIDailyBudgetUSD:         1,
		CheckUpdates:             true,
		ShowInTray:               true,
		Labels:                   []models.Label{JunkLabel()}, // mailboxes bring their own (MailboxDefaultLabels)
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
	if s.UndoSendSeconds < 0 || s.UndoSendSeconds > 30 {
		s.UndoSendSeconds = 5
	}
	switch s.MarkReadMode {
	case "immediate", "delay", "manual":
	default:
		s.MarkReadMode = "immediate"
	}
	switch s.ReplyQuote {
	case "header", "plain", "none":
	default:
		s.ReplyQuote = "header"
	}
	if s.EmailBackground != "light" {
		s.EmailBackground = "auto"
	}
	switch s.CalendarTimeFormat {
	case "auto", "12h", "24h":
	default:
		s.CalendarTimeFormat = "auto"
	}
	switch s.CalendarWeekStart {
	case "auto", "sunday", "monday", "saturday":
	default:
		s.CalendarWeekStart = "auto"
	}
	switch s.CalendarDefaultView {
	case "week", "day", "month", "agenda":
	default:
		s.CalendarDefaultView = "week"
	}
	if s.CalendarDefaultDuration < 5 || s.CalendarDefaultDuration > 24*60 {
		s.CalendarDefaultDuration = 30
	}
	if s.CalendarDefaultReminder < 0 || s.CalendarDefaultReminder > 7*24*60 {
		s.CalendarDefaultReminder = 10
	}
	switch s.ReadingPane {
	case "right", "off":
	default:
		s.ReadingPane = "right"
	}
	switch s.SyncDepthDays {
	case 0, 30, 90, 365:
	default:
		s.SyncDepthDays = 90
	}
	if s.AIDailyBudgetUSD < 0 {
		s.AIDailyBudgetUSD = 1
	}
	if s.Zoom < 0.5 || s.Zoom > 2 {
		s.Zoom = 1
	}
	s.TrustedLinkDomains = NormalizeDomains(s.TrustedLinkDomains)
	s.TrustedImageSenders = NormalizeSenders(s.TrustedImageSenders)
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
	// After the labels: rules and quiet lists drop labels that are gone
	// or belong to another mailbox.
	labels := map[string]bool{}
	byID := map[string]models.Label{}
	for _, l := range s.Labels {
		labels[l.ID] = true
		byID[l.ID] = l
	}
	s.LabelRules = normalizeRules(s.LabelRules, labels)
	for i, r := range s.LabelRules {
		if r.LabelID != "" && r.AccountID != "" && !LabelInAccount(byID[r.LabelID], r.AccountID) {
			s.LabelRules[i].LabelID = ""
		}
	}
	s.QuietLabels = normalizeQuietLabels(s.QuietLabels, labels) // new lists: safe to filter in place
	for acct, ids := range s.QuietLabels {
		s.QuietLabels[acct] = slices.DeleteFunc(ids, func(id string) bool { return !LabelInAccount(byID[id], acct) })
	}
	s.MutedMailboxes = uniqueTrimmed(s.MutedMailboxes)
	s.AutoLabelAccounts = uniqueTrimmed(s.AutoLabelAccounts)
	s.AutoClassify = len(s.AutoLabelAccounts) > 0 // for older versions
	s.AICorrectsOff = uniqueTrimmed(s.AICorrectsOff)
	s.JunkStaysPut = uniqueTrimmed(s.JunkStaysPut)
}

// AutoClassifies reports whether new mail of accountID is AI-labeled
// automatically.
func AutoClassifies(s models.Settings, accountID string) bool {
	return slices.Contains(s.AutoLabelAccounts, accountID)
}

// MigrateAutoLabel turns the old app-wide auto-label switch into the
// per-mailbox list, once (AutoLabelAccounts is nil only in settings from
// before it existed): on means every mailbox, off none. It must run
// before Normalize, which makes the list non-nil. It reports whether it
// changed s.
func MigrateAutoLabel(s *models.Settings, accountIDs []string) bool {
	if s.AutoLabelAccounts != nil {
		return false
	}
	s.AutoLabelAccounts = []string{}
	if s.AutoClassify {
		s.AutoLabelAccounts = append(s.AutoLabelAccounts, accountIDs...)
	}
	return true
}

// AICorrects reports whether the AI may remove imported labels from mail
// of accountID: AICorrectsProviderLabels is on and the mailbox isn't
// turned off.
func AICorrects(s models.Settings, accountID string) bool {
	return s.AICorrectsProviderLabels && !slices.Contains(s.AICorrectsOff, accountID)
}

// NormalizeLabels validates label definitions: unique slug ids, names
// unique within a mailbox (case-insensitive), valid colors, sources and
// actions, and makes sure the built-in Junk label exists. Junk belongs to
// every mailbox, so no other label takes its name.
func NormalizeLabels(in []models.Label) []models.Label {
	out := make([]models.Label, 0, len(in)+1)
	seen := map[string]bool{}
	names := map[string]bool{} // mailbox + "\x00" + lower-cased name; "*" = every mailbox
	junkName := "junk"
	for _, c := range in {
		if c.ID == JunkLabelID && strings.TrimSpace(c.Name) != "" {
			junkName = strings.ToLower(strings.TrimSpace(c.Name))
		}
	}
	hasJunk := false
	for i, c := range in {
		c.Name = strings.TrimSpace(c.Name)
		c.Description = strings.TrimSpace(c.Description)
		if c.Name == "" {
			continue
		}
		lower := strings.ToLower(c.Name)
		if c.ID == JunkLabelID {
			if hasJunk {
				continue
			}
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
		} else if lower == junkName {
			continue
		}
		switch c.Source {
		case LabelSourceProvider, LabelSourceSystem:
		default:
			c.Source = LabelSourceUser
		}
		if c.Source == LabelSourceSystem && c.ID != JunkLabelID {
			c.Source = LabelSourceUser
		}
		switch c.Then {
		case LabelThenArchive, LabelThenJunk, LabelThenTrash:
		default:
			c.Then = ""
		}
		if c.Source == LabelSourceSystem {
			c.AccountIDs = []string{} // Junk belongs to every mailbox
			c.Then = ""               // and is the Junk folder already
		} else {
			c.AccountIDs = uniqueTrimmed(c.AccountIDs)
		}
		keys := []string{"*\x00" + lower}
		for _, a := range c.AccountIDs {
			keys = append(keys, a+"\x00"+lower)
		}
		if slices.ContainsFunc(keys, func(k string) bool { return names[k] }) {
			continue
		}
		id := Slug(c.ID)
		if id == "" {
			id = Slug(c.Name)
		}
		if c.ID != JunkLabelID && id == JunkLabelID {
			id = "junk-label"
		}
		id = uniqueID(seen, id)
		if len(c.AccountIDs) == 0 {
			names[keys[0]] = true
		}
		for _, k := range keys[1:] {
			names[k] = true
		}
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

// uniqueID returns id, or id-2, id-3, ... when taken, and marks it taken.
func uniqueID(taken map[string]bool, id string) string {
	if id == "" {
		id = "label"
	}
	base, n := id, 2
	for taken[id] {
		id = base + "-" + strconv.Itoa(n)
		n++
	}
	taken[id] = true
	return id
}

func labelIDs(labels []models.Label) map[string]bool {
	m := make(map[string]bool, len(labels))
	for _, l := range labels {
		m[l.ID] = true
	}
	return m
}

// NewLabelID returns an unused label id for a label called name.
func NewLabelID(labels []models.Label, name string) string {
	return uniqueID(labelIDs(labels), Slug(name))
}

// LabelInAccount reports whether a label belongs to a mailbox. The
// built-in Junk label belongs to every mailbox; accountID "" (the unified
// view) matches every label. A label without a mailbox (only before
// SplitLabels ran) counts as every mailbox's.
func LabelInAccount(l models.Label, accountID string) bool {
	return accountID == "" || l.Source == LabelSourceSystem || len(l.AccountIDs) == 0 || slices.Contains(l.AccountIDs, accountID)
}

// LabelsForAccount returns the labels of a mailbox ("" = all labels).
func LabelsForAccount(labels []models.Label, accountID string) []models.Label {
	out := make([]models.Label, 0, len(labels))
	for _, l := range labels {
		if LabelInAccount(l, accountID) {
			out = append(out, l)
		}
	}
	return out
}

// LabelNamed returns the label of a mailbox with that name
// (case-insensitive), the built-in Junk label included.
func LabelNamed(labels []models.Label, accountID, name string) (models.Label, bool) {
	name = strings.TrimSpace(name)
	for _, l := range labels {
		if strings.EqualFold(l.Name, name) && (l.Source == LabelSourceSystem || slices.Contains(l.AccountIDs, accountID)) {
			return l, true
		}
	}
	return models.Label{}, false
}

// LabelMove says that a mailbox's assignments of label From now belong to
// label To (SplitLabels gave the mailbox its own copy).
type LabelMove struct {
	AccountID string
	From, To  string
}

// SplitLabels makes every label belong to exactly one mailbox. A label of
// several mailboxes (older versions shared labels, and merged Gmail labels
// and Outlook categories by name) stays with the first one and every other
// mailbox gets its own copy with a new id; a label without mailboxes is
// copied to every mailbox of accountIDs (kept as is without accounts).
// Copies keep name, color, description, source and action. Rules for
// every mailbox (AccountID "") become one rule per mailbox, and rules and
// quiet lists follow the copies. The built-in Junk label stays shared.
//
// It returns the moves the stored assignments must follow (see
// store.MoveLabels); nil when nothing changed. Running it again changes
// nothing.
func SplitLabels(s *models.Settings, accountIDs []string) (moves []LabelMove, changed bool) {
	taken := labelIDs(s.Labels)
	remap := map[string]map[string]string{} // account -> old id -> new id
	out := make([]models.Label, 0, len(s.Labels))
	for _, l := range s.Labels {
		accts := l.AccountIDs
		if l.Source == LabelSourceSystem || len(accts) == 1 {
			out = append(out, l)
			continue
		}
		if len(accts) == 0 {
			if accts = accountIDs; len(accts) == 0 {
				out = append(out, l)
				continue
			}
		}
		changed = true
		for i, a := range accts {
			c := l
			c.AccountIDs = []string{a}
			if i > 0 {
				c.ID = uniqueID(taken, l.ID)
				if remap[a] == nil {
					remap[a] = map[string]string{}
				}
				remap[a][l.ID] = c.ID
				moves = append(moves, LabelMove{AccountID: a, From: l.ID, To: c.ID})
			}
			out = append(out, c)
		}
	}
	s.Labels = out
	mapID := func(acct, id string) string {
		if to, ok := remap[acct][id]; ok {
			return to
		}
		return id
	}
	byID := map[string]models.Label{}
	for _, l := range out {
		byID[l.ID] = l
	}
	rules := make([]models.LabelRule, 0, len(s.LabelRules))
	ruleIDs := map[string]bool{}
	for _, r := range s.LabelRules {
		ruleIDs[r.ID] = true
	}
	for _, r := range s.LabelRules {
		if r.AccountID != "" {
			if to := mapID(r.AccountID, r.LabelID); to != r.LabelID {
				r.LabelID = to
				changed = true
			}
			rules = append(rules, r)
			continue
		}
		if len(accountIDs) == 0 {
			rules = append(rules, r)
			continue
		}
		// A rule for every mailbox: one per mailbox where its label is.
		changed = true
		first := true
		for _, a := range accountIDs {
			c := r
			c.AccountID = a
			c.LabelID = mapID(a, r.LabelID)
			if c.Action != "junk" && c.LabelID != "" {
				if l, ok := byID[c.LabelID]; ok && !LabelInAccount(l, a) {
					continue
				}
			}
			if !first {
				c.ID = uniqueRuleID(ruleIDs)
			}
			first = false
			rules = append(rules, c)
		}
		if first { // its label is in no mailbox: keep it for the first
			r.AccountID = accountIDs[0]
			rules = append(rules, r)
		}
	}
	s.LabelRules = rules
	if len(remap) > 0 && len(s.QuietLabels) > 0 {
		quiet := make(map[string][]string, len(s.QuietLabels))
		for acct, ids := range s.QuietLabels {
			list := make([]string, len(ids))
			for i, id := range ids {
				list[i] = mapID(acct, id)
			}
			quiet[acct] = list
		}
		s.QuietLabels = quiet
	}
	return moves, changed
}

func uniqueRuleID(taken map[string]bool) string {
	n := 1
	for taken["rule_"+strconv.Itoa(n)] {
		n++
	}
	id := "rule_" + strconv.Itoa(n)
	taken[id] = true
	return id
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

// maxRules caps the number of label rules.
const maxRules = 500

// normalizeRules fills defaults and makes ids unique; ids are otherwise
// kept stable (the UI edits rules by id). Rules that aren't filled in yet
// are kept (the UI shows them) but never match. A rule whose label was
// deleted loses it (so a new label that happens to get the same id isn't
// applied by surprise); "add the Junk label" means "move to Junk".
func normalizeRules(in []models.LabelRule, labels map[string]bool) []models.LabelRule {
	out := []models.LabelRule{}
	taken := map[string]bool{}
	for _, r := range in {
		taken[r.ID] = true
	}
	seen := map[string]bool{}
	next := 1
	for _, r := range in {
		if len(out) >= maxRules {
			break
		}
		r.ID = strings.TrimSpace(r.ID)
		r.Contains = strings.TrimSpace(r.Contains)
		if r.Field != "subject" {
			r.Field = "from"
		}
		r.LabelID = strings.TrimSpace(r.LabelID)
		if r.Action != "junk" {
			r.Action = "label"
		}
		if r.Action == "label" && r.LabelID == JunkLabelID {
			r.Action = "junk"
		}
		if r.Action == "junk" || !labels[r.LabelID] {
			r.LabelID = ""
		}
		r.AccountID = strings.TrimSpace(r.AccountID)
		if r.ID == "" || seen[r.ID] {
			for taken["rule_"+strconv.Itoa(next)] {
				next++
			}
			r.ID = "rule_" + strconv.Itoa(next)
			taken[r.ID] = true
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out
}

// normalizeQuietLabels drops empty account ids and blank, duplicate or
// deleted label ids. An empty list means "only Junk is quiet" for that
// mailbox (Junk never notifies).
func normalizeQuietLabels(in map[string][]string, labels map[string]bool) map[string][]string {
	out := map[string][]string{}
	for acct, ids := range in {
		if acct = strings.TrimSpace(acct); acct == "" {
			continue
		}
		list := []string{}
		for _, id := range uniqueTrimmed(ids) {
			if labels[id] {
				list = append(list, id)
			}
		}
		out[acct] = list
	}
	return out
}

// uniqueTrimmed trims the strings and drops blanks and duplicates.
func uniqueTrimmed(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
