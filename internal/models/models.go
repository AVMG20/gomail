// Package models holds the domain types shared between the backend services
// and the Wails bindings. These types are serialised to the Vue frontend, so
// field names and JSON tags form the frontend contract.
package models

import "time"

// Provider identifiers.
const (
	ProviderGmail   = "gmail"
	ProviderOutlook = "outlook"
)

// Folder kinds. Every provider maps its system folders onto these so the UI
// can render a consistent sidebar per mailbox.
const (
	FolderInbox   = "inbox"
	FolderStarred = "starred"
	FolderSent    = "sent"
	FolderDrafts  = "drafts"
	FolderArchive = "archive"
	FolderSpam    = "spam"
	FolderTrash   = "trash"
	FolderCustom  = "custom"
)

type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Account struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	Color       string     `json:"color"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastSyncAt  *time.Time `json:"lastSyncAt"`
	SyncError   string     `json:"syncError"`
	Syncing     bool       `json:"syncing"`
	UnreadCount int        `json:"unreadCount"`
	// NeedsReauth is set when the provider revoked or expired the sign-in;
	// call ReauthAccount to sign in again (local data is kept).
	NeedsReauth bool `json:"needsReauth"`
	// Signature is sanitized HTML inserted by the composer.
	Signature string `json:"signature"`
	// Position orders the mailboxes (account rail, settings); lists come sorted.
	Position int `json:"-"`
	// CalendarConsent is true when the sign-in includes calendar access.
	// Accounts added before calendar support need EnableCalendar.
	CalendarConsent bool `json:"calendarConsent"`
	// CalendarEnabled is the per-account "Disable calendar" switch (default
	// true). Disabled: no calendar sync, no reminders, events not listed;
	// the calendars stay in ListCalendars (to show them grayed out).
	CalendarEnabled bool `json:"calendarEnabled"`
	// CalendarMuted silences event reminders of the account (default false);
	// events still sync and show.
	CalendarMuted bool `json:"calendarMuted"`
}

// Contact is an address seen in mail, ranked by frequency and recency.
type Contact struct {
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

type Folder struct {
	ID        string `json:"id"` // local id, unique across accounts
	AccountID string `json:"accountId"`
	RemoteID  string `json:"remoteId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // one of the Folder* constants
	Unread    int    `json:"unread"`
	Total     int    `json:"total"`
}

type MessageSummary struct {
	ID             string    `json:"id"` // local id, unique across accounts
	AccountID      string    `json:"accountId"`
	RemoteID       string    `json:"remoteId"`
	ThreadID       string    `json:"threadId"`
	FolderIDs      []string  `json:"folderIds"`
	From           Address   `json:"from"`
	To             []Address `json:"to"`
	Subject        string    `json:"subject"`
	Snippet        string    `json:"snippet"`
	Date           time.Time `json:"date"`
	Unread         bool      `json:"unread"`
	Starred        bool      `json:"starred"`
	HasAttachments bool      `json:"hasAttachments"`
	// Labels are the ids of the user-defined labels on this message
	// (manual and AI-assigned). LabelDetails says who assigned each one.
	Labels       []string       `json:"labels"`
	LabelDetails []MessageLabel `json:"labelDetails"`

	// Conversation view (Settings.conversationView): one row per thread.
	// The row shows the thread's latest message matching the query; Unread,
	// Starred, HasAttachments and Labels are aggregated over the thread.
	// ConversationID ("c:<id>") can be passed to every action method instead
	// of message ids to act on the whole conversation (see docs/BACKEND.md).
	// It is "" in message view.
	ConversationID string   `json:"conversationId"`
	ThreadCount    int      `json:"threadCount"`  // messages in the conversation (1 in message view)
	Participants   []string `json:"participants"` // sender names, oldest first; "me" for the account itself
}

// AutoMove is mail an automation (Jev, a rule, a label's "When added")
// moved, for the "sorted for you" review.
type AutoMove struct {
	MessageID string    `json:"messageId"`
	AccountID string    `json:"accountId"`
	To        string    `json:"to"`     // folder kind: spam | archive | trash
	Reason    string    `json:"reason"` // e.g. "Jev: Junk", "Rule: sender contains “github.com”"
	At        time.Time `json:"at"`
	FromName  string    `json:"fromName"`
	FromEmail string    `json:"fromEmail"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
}

// MessageLabel is one label assignment on a message.
type MessageLabel struct {
	LabelID    string  `json:"labelId"`
	Source     string  `json:"source"`     // "manual" | "ai" | "provider" (Gmail label / Outlook category) | "system" (Junk)
	Confidence float64 `json:"confidence"` // classifier confidence (1 for manual)
}

type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	Size      int64  `json:"size"`
	Inline    bool   `json:"inline"`
	ContentID string `json:"contentId"`
}

type Message struct {
	MessageSummary
	Cc          []Address    `json:"cc"`
	Bcc         []Address    `json:"bcc"`
	ReplyTo     []Address    `json:"replyTo"`
	BodyHTML    string       `json:"bodyHtml"`
	BodyText    string       `json:"bodyText"`
	Attachments []Attachment `json:"attachments"`
	MessageID   string       `json:"messageId"` // RFC 5322 Message-ID header
	InReplyTo   string       `json:"inReplyTo"`
	References  string       `json:"references"`
	// HasRemoteContent reports whether the body references remote resources
	// (images, srcset, CSS url()). RemoteContentBlocked is true when they
	// were stripped from BodyHTML (Settings.showRemoteImages off); call
	// GetMessageWithRemoteContent to load them.
	HasRemoteContent     bool         `json:"hasRemoteContent"`
	RemoteContentBlocked bool         `json:"remoteContentBlocked"`
	Unsubscribe          *Unsubscribe `json:"unsubscribe"` // nil when the sender offers none
	IsDraft              bool         `json:"isDraft"`     // message is in a Drafts folder (editable via SaveDraft)
	// Invite is set when the message carries a text/calendar (ICS) part.
	Invite *Invite `json:"invite"`
}

// Unsubscribe describes the List-Unsubscribe options of a message.
type Unsubscribe struct {
	URL      string `json:"url"`      // https link ("" if none)
	Mailto   string `json:"mailto"`   // mailto: URI ("" if none)
	OneClick bool   `json:"oneClick"` // RFC 8058 one-click POST supported
}

// UnsubscribeResult says what Unsubscribe did.
type UnsubscribeResult struct {
	Method string `json:"method"` // "oneClick" (done), "mailto" (email sent) or "url" (opened in the browser)
	URL    string `json:"url"`
}

// AttachmentData is attachment content for previews.
type AttachmentData struct {
	Filename   string `json:"filename"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	DataBase64 string `json:"dataBase64"`
}

// ListQuery selects messages for the message list. An empty AccountID means
// "all accounts" (unified view); FolderKind selects the same system folder
// across accounts, FolderID selects one specific folder.
type ListQuery struct {
	AccountID     string   `json:"accountId"`
	FolderID      string   `json:"folderId"`
	FolderKind    string   `json:"folderKind"`
	Labels        []string `json:"labels"`        // messages having ANY of these label ids; empty = no label filter
	Unlabeled     bool     `json:"unlabeled"`     // only messages without any label
	ExcludeLabels []string `json:"excludeLabels"` // hide messages having ANY of these label ids
	Search        string   `json:"search"`
	UnreadOnly    bool     `json:"unreadOnly"`
	Limit         int      `json:"limit"`
	Cursor        string   `json:"cursor"`
}

// SearchQuery drives the command palette search across all mail. Query
// supports free text (prefix matching on the last word, typo tolerant),
// "quoted phrases" and operators: from: to: subject: label: in:<folder kind>
// is:unread|read|starred has:attachment before:YYYY-MM-DD after:YYYY-MM-DD.
type SearchQuery struct {
	Query     string   `json:"query"`
	AccountID string   `json:"accountId"` // "" = all accounts
	Labels    []string `json:"labels"`    // optional: restrict to messages with ANY of these labels
	Limit     int      `json:"limit"`     // default 20, max 100
}

// SearchResult is one ranked search hit.
type SearchResult struct {
	Message   MessageSummary `json:"message"`
	Highlight string         `json:"highlight"` // HTML-escaped excerpt; matches wrapped in <mark></mark>
	MatchedIn string         `json:"matchedIn"` // "subject" | "from" | "to" | "snippet" | "body" | "" (filters only)
	Score     float64        `json:"score"`     // higher is better
	Fuzzy     bool           `json:"fuzzy"`     // true when found by typo-tolerant matching
}

type MessagePage struct {
	Messages   []MessageSummary `json:"messages"`
	NextCursor string           `json:"nextCursor"`
	Total      int              `json:"total"` // conversations in conversation view, messages otherwise
	// HasMoreRemote is true on the last local page when the server has
	// older mail for this view: call LoadOlder(query) to fetch the next page.
	HasMoreRemote bool `json:"hasMoreRemote"`
	// OlderBeyondDepth is true on the last local page when the server only
	// has mail older than Settings.syncDepthDays: LoadOlder fetches it, but
	// only when asked (never automatically on scrolling).
	OlderBeyondDepth bool `json:"olderBeyondDepth"`
	// Similar is true when the search as typed matched nothing and the
	// page shows close matches (spelling corrected) instead.
	Similar bool `json:"similar"`
}

type OutgoingAttachment struct {
	Filename   string `json:"filename"`
	MimeType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
}

type ComposeRequest struct {
	AccountID   string               `json:"accountId"`
	To          []Address            `json:"to"`
	Cc          []Address            `json:"cc"`
	Bcc         []Address            `json:"bcc"`
	Subject     string               `json:"subject"`
	BodyHTML    string               `json:"bodyHtml"`
	BodyText    string               `json:"bodyText"`
	ReplyToID   string               `json:"replyToId"` // local message id being replied to / forwarded
	DraftID     string               `json:"draftId"`   // draft handle from SaveDraft; sending it removes the draft
	Mode        string               `json:"mode"`      // "new" | "reply" | "replyAll" | "forward"
	Attachments []OutgoingAttachment `json:"attachments"`
	// AttachmentsComplete: Attachments is the draft's whole file list (the
	// client loaded a reopened draft's files), so files on the server draft
	// that aren't listed were removed by the user and are deleted there.
	// Without it, unknown server files are kept.
	AttachmentsComplete bool `json:"attachmentsComplete"`
}

// LabelRule acts on new mail whose sender or subject contains some text.
type LabelRule struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"` // the mailbox ("" = every mailbox, only in settings from older versions)
	Field     string `json:"field"`     // "from" (name or address) | "subject"
	Contains  string `json:"contains"`  // case-insensitive
	Action    string `json:"action"`    // "label" | "junk" (move to Junk/Spam)
	LabelID   string `json:"labelId"`   // the label for "label"
}

// Label is a user-defined label. Labels are applied manually or by the AI
// classifier (OpenRouter Jev), which uses Description as guidance.
type Label struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"` // guidance passed to the classifier
	Color       string `json:"color"`
	// Source is "user" (created in gomail), "provider" (imported from Gmail
	// labels / Outlook categories) or "system" (built-in, e.g. "junk").
	Source string `json:"source"`
	// AccountIDs is the one mailbox the label belongs to: it is offered,
	// counted and applied only there, and editing it never changes another
	// mailbox (every mailbox has its own labels; labels with the same name
	// are grouped only for display in the unified view). Empty for the
	// built-in Junk label, which belongs to every mailbox. A list for
	// settings from older versions, which shared labels between mailboxes
	// (config.SplitLabels gives each mailbox its own copy).
	AccountIDs []string `json:"accountIds"`
	// Then is what happens to mail that a rule or Jev gives the label (and
	// the user, with Settings.ManualLabelActions): "" (stays where it is),
	// "archive", "junk" or "trash". Not used for Junk, which always moves
	// mail to Junk.
	Then string `json:"then"`
}

type Settings struct {
	Theme               string  `json:"theme"`   // "system" | "light" | "dark"
	Density             string  `json:"density"` // "comfortable" | "compact"
	SyncIntervalMinutes int     `json:"syncIntervalMinutes"`
	ShowRemoteImages    bool    `json:"showRemoteImages"`
	PrimaryColor        string  `json:"primaryColor"`     // hex accent color, e.g. "#2563eb"
	Radius              float64 `json:"radius"`           // UI corner radius in rem (default 0.5)
	Notifications       bool    `json:"notifications"`    // desktop notifications for new mail (default true)
	ConversationView    bool    `json:"conversationView"` // group the message list by conversation (default true)
	ShowInTray          bool    `json:"showInTray"`       // system tray icon with unread count (default true)
	CloseToTray         bool    `json:"closeToTray"`      // closing the window hides it to the tray (default false)
	UndoSendSeconds     int     `json:"undoSendSeconds"`  // send delay handled by the UI (default 5, 0..30)
	MarkReadMode        string  `json:"markReadMode"`     // "immediate" | "delay" | "manual" (default "immediate")
	ReadingPane         string  `json:"readingPane"`      // "right" | "off" (default "right")
	// ReplyQuote is how replies quote the original: "header" (On … wrote: + quote),
	// "plain" (quote only) or "none" (default "header").
	ReplyQuote string `json:"replyQuote"`
	// EmailBackground is "auto" (follow the theme, dark bodies in dark mode
	// where the email allows it) or "light" (always render emails on white).
	EmailBackground string `json:"emailBackground"` // default "auto"
	// DarkenDesignedMail (experimental): in dark mode, also show designed (white)
	// mail dark by inverting its colors, photos excepted. Default false.
	DarkenDesignedMail bool `json:"darkenDesignedMail"`
	// Calendar preferences.
	CalendarWeekStart       string  `json:"calendarWeekStart"`       // "auto" | "sunday" | "monday" | "saturday" (default "auto")
	CalendarTimeFormat      string  `json:"calendarTimeFormat"`      // "auto" | "12h" | "24h" (default "auto": follows the locale)
	CalendarDefaultView     string  `json:"calendarDefaultView"`     // "week" | "day" | "month" | "agenda" (default "week")
	CalendarDefaultDuration int     `json:"calendarDefaultDuration"` // minutes for new events (default 30)
	CalendarShowDeclined    bool    `json:"calendarShowDeclined"`    // ListEvents includes declined events (default false)
	CalendarDefaultReminder int     `json:"calendarDefaultReminder"` // minutes; used when an event has no reminders (default 10)
	CalendarNotifications   bool    `json:"calendarNotifications"`   // desktop reminders for upcoming events (default true)
	Zoom                    float64 `json:"zoom"`                    // UI zoom factor (default 1.0, 0.5..2)
	SyncDepthDays           int     `json:"syncDepthDays"`           // background backfill depth: 30, 90 (default), 365 or 0 = all mail
	BackfillPaused          bool    `json:"backfillPaused"`          // pause the background backfill
	AIDailyBudgetUSD        float64 `json:"aiDailyBudgetUsd"`        // automatic labeling pauses when reached (default 1.00, 0 = unlimited)
	LaunchAtLogin           bool    `json:"launchAtLogin"`           // start gomail at login, minimized to the tray when enabled (default false)
	CheckUpdates            bool    `json:"checkUpdates"`            // check GitHub releases once a day (default true)

	// TrustedLinkDomains open from mail without the "Open link?" prompt
	// (lowercase host names without "www."; subdomains match too).
	TrustedLinkDomains []string `json:"trustedLinkDomains"`
	// TrustedImageSenders always get remote images loaded (lowercase
	// addresses), even with ShowRemoteImages off.
	TrustedImageSenders []string `json:"trustedImageSenders"`

	GoogleClientID     string `json:"googleClientId"`
	GoogleClientSecret string `json:"googleClientSecret"`
	MicrosoftClientID  string `json:"microsoftClientId"`
	MicrosoftTenant    string `json:"microsoftTenant"` // default "common"

	OpenRouterKeySet bool `json:"openRouterKeySet"` // read-only; the key itself never leaves the backend
	// LabelsRev is the revision of the label list this copy was read at
	// (read-only, not stored). The UI sends it back unchanged with
	// SaveSettings, so labels the backend added after the UI read them
	// (provider labels found by a sync) aren't taken as deleted.
	LabelsRev       int64  `json:"labelsRev"`
	OpenRouterModel string `json:"openRouterModel"` // chat model (grammar, summaries, drafts)
	ClassifierModel string `json:"classifierModel"` // OpenRouter Decisions model, default "typesafe/jev-1.13"

	// Labels and AI labeling.
	Labels               []Label `json:"labels"`
	ImportProviderLabels bool    `json:"importProviderLabels"` // show Gmail labels / Outlook categories as labels (default true)
	// AutoClassify is the old "AI-label new mail" switch for every mailbox.
	// Deprecated: AutoLabelAccounts replaces it; it's kept in sync (on when
	// any mailbox is auto-labeled) and read once to migrate old settings.
	AutoClassify             bool    `json:"autoClassify"`
	ClassifyThreshold        float64 `json:"classifyThreshold"`        // AI adds a label when P(yes) >= this (default 0.5)
	ClassifyRemoveThreshold  float64 `json:"classifyRemoveThreshold"`  // AI removes provider/Junk labels when P(yes) < this (default 0.15)
	AICorrectsProviderLabels bool    `json:"aiCorrectsProviderLabels"` // allow those removals (default true)
	ClassifyInstructions     string  `json:"classifyInstructions"`     // per-label question template; "" = default. "{label}" is replaced by the label name
	GrammarSystemPrompt      string  `json:"grammarSystemPrompt"`      // "" = built-in default (see GetDefaultPrompts)

	// JunkStaysPut lists account ids where the built-in Junk label only
	// labels mail; elsewhere (the default) it moves it to Junk, read.
	JunkStaysPut []string `json:"junkStaysPut"`
	// ManualLabelActions: a label added by hand (not by Jev or a rule) also
	// does what its "When added" says (default off: it only labels).
	ManualLabelActions bool `json:"manualLabelActions"`

	// LabelRules label (or junk) new mail by simple conditions, without AI.
	LabelRules []LabelRule `json:"labelRules"`
	// QuietLabels maps an account id to the labels whose new mail doesn't
	// notify. Accounts without an entry use DefaultQuietLabels (Junk).
	QuietLabels map[string][]string `json:"quietLabels"`
	// MutedMailboxes lists account ids whose new mail never notifies
	// (calendar reminders are separate: Account.CalendarMuted).
	MutedMailboxes []string `json:"mutedMailboxes"`
	// HiddenFolders lists folder ids (other folders, like an old "GitHub"
	// folder) left out of the sidebar. Their mail still shows in search.
	HiddenFolders []string `json:"hiddenFolders"`
	// AutoLabelAccounts lists the account ids whose new mail Jev labels
	// automatically. Missing (nil) in settings from before it existed: see
	// config.MigrateAutoLabel.
	AutoLabelAccounts []string `json:"autoLabelAccounts"`
	// AICorrectsOff lists account ids where the AI never removes imported
	// Gmail/Outlook labels. AICorrectsProviderLabels stays the switch for
	// every mailbox; an empty list means all mailboxes.
	AICorrectsOff []string `json:"aiCorrectsOff"`
}

// AIUsage summarizes OpenRouter spending recorded by gomail (local time).
type AIUsage struct {
	TodayUSD       float64 `json:"todayUsd"`
	MonthUSD       float64 `json:"monthUsd"`
	RequestsToday  int     `json:"requestsToday"`
	RequestsMonth  int     `json:"requestsMonth"`
	DailyBudgetUSD float64 `json:"dailyBudgetUsd"` // 0 = unlimited
	BudgetReached  bool    `json:"budgetReached"`  // automatic labeling is paused until tomorrow
}

// ClassifyJob identifies a background AI labeling job.
type ClassifyJob struct {
	JobID string `json:"jobId"`
	Total int    `json:"total"`
}

// ClassifyEstimate is the expected size and cost of a labeling job.
type ClassifyEstimate struct {
	Count            int     `json:"count"`
	Requests         int     `json:"requests"`        // Decisions API calls (labels are sent in chunks of 20)
	EstimatedTokens  int     `json:"estimatedTokens"` // input tokens (~chars/4)
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	PriceKnown       bool    `json:"priceKnown"`    // false when OpenRouter lists no price for the classifier model
	Unchanged        int     `json:"unchanged"`     // messages that would be skipped (already labeled with the current labels)
	TodayUSD         float64 `json:"todayUsd"`      // spent today
	ExceedsBudget    bool    `json:"exceedsBudget"` // today's spend + estimate is over the daily budget (allowed, but warn)
}

// LabelCount is the number of messages carrying a label.
type LabelCount struct {
	LabelID string `json:"labelId"`
	Total   int    `json:"total"`
	Unread  int    `json:"unread"`
}

// DefaultPrompts holds the built-in prompts used when the corresponding
// Settings fields are empty ("Reset to default" in the UI).
type DefaultPrompts struct {
	Grammar  string `json:"grammar"`
	Classify string `json:"classify"`
}

// AppInfo describes the running build for the About screen.
type AppInfo struct {
	Version  string `json:"version"`
	DataDir  string `json:"dataDir"`
	LogFile  string `json:"logFile"`
	Platform string `json:"platform"` // GOOS/GOARCH
	OS       string `json:"os"`       // runtime.GOOS: "darwin" | "windows" | "linux"
	Arch     string `json:"arch"`     // runtime.GOARCH
	Demo     bool   `json:"demo"`
}

type AIModel struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	ContextLength   int     `json:"contextLength"`
	PromptPrice     float64 `json:"promptPrice"`     // USD per 1M tokens
	CompletionPrice float64 `json:"completionPrice"` // USD per 1M tokens
	PriceUnknown    bool    `json:"priceUnknown"`    // OpenRouter reported no (or variable) pricing
	Kind            string  `json:"kind"`            // "chat" | "decision" (Jev, Decisions API)
}

// ClassificationResult is the outcome of labeling one message with the AI
// classifier: which labels were added/removed and the P(yes) per label id.
type ClassificationResult struct {
	MessageID string             `json:"messageId"`
	Added     []string           `json:"added"`
	Removed   []string           `json:"removed"`
	Scores    map[string]float64 `json:"scores"`
	Cost      float64            `json:"cost"`    // USD
	Error     string             `json:"error"`   // per-message failure, "" on success
	Skipped   bool               `json:"skipped"` // unchanged since the last AI run: scores are the stored ones, no cost
	// Moved is the folder kind a label's "When added" moved the message
	// to ("" = it stayed), so Undo can move it back.
	Moved string `json:"moved"`
}

// Event names emitted to the frontend.
const (
	EventSyncStarted      = "sync:started"      // SyncEvent
	EventSyncFinished     = "sync:finished"     // SyncEvent
	EventMessagesChanged  = "messages:changed"  // AccountEvent ("" accountId = several accounts)
	EventAccountsChanged  = "accounts:changed"  // AccountEvent
	EventLabelsChanged    = "labels:changed"    // AccountEvent: label definitions changed (re-read GetSettings) or counts changed
	EventClassifyProgress = "classify:progress" // ClassifyProgress
	EventClassifyFinished = "classify:finished" // ClassifyProgress
	EventNewMail          = "mail:new"          // NewMailEvent
	EventSyncProgress     = "sync:progress"     // SyncProgress (first sync of an account)
	EventNetworkStatus    = "network:status"    // NetworkStatus
	EventSearchUpdated    = "search:updated"    // SearchUpdated: server search cached more results; re-run the query
	EventAIBudget         = "ai:budget"         // AIBudgetEvent: automatic labeling paused (budget reached) or resumed
	EventNotificationOpen = "notification:open" // NotificationOpen: the user clicked a new-mail notification
	EventDatabaseStatus   = "database:status"   // DatabaseStatus: integrity problems / repair
	EventComposeOpen      = "compose:open"      // ComposeOpen: a mailto: link was opened
	EventUpdateAvailable  = "update:available"  // UpdateInfo: a newer release exists
	EventCalendarChanged  = "calendar:changed"  // CalendarChanged: calendars or events of an account changed
	EventCalendarOpen     = "calendar:open"     // CalendarOpen: the user clicked an event reminder
)

// ComposeOpen asks the UI to open the composer (mailto: links).
type ComposeOpen struct {
	To      []Address `json:"to"`
	Cc      []Address `json:"cc"`
	Bcc     []Address `json:"bcc"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
}

// UpdateInfo describes the latest GitHub release.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`   // release page
	Notes     string `json:"notes"` // release notes (markdown)
	Available bool   `json:"available"`
}

// AIBudgetEvent reports the daily AI budget state.
type AIBudgetEvent struct {
	Paused    bool    `json:"paused"`
	TodayUSD  float64 `json:"todayUsd"`
	BudgetUSD float64 `json:"budgetUsd"`
}

// NotificationOpen asks the UI to show a message.
type NotificationOpen struct {
	MessageID string `json:"messageId"`
	AccountID string `json:"accountId"`
	EventID   string `json:"eventId"` // set for event reminders (clicks emit calendar:open)
}

// DatabaseStatus reports database health.
type DatabaseStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// SyncProgress reports the progress of an account's initial sync.
type SyncProgress struct {
	AccountID string `json:"accountId"`
	Phase     string `json:"phase"` // "folders" | "listing" | "fetching" | "saving"
	Done      int    `json:"done"`
	Total     int    `json:"total"`
}

// NetworkStatus is emitted when connectivity changes.
type NetworkStatus struct {
	Online bool `json:"online"`
}

// SearchUpdated is emitted when a server-side search added messages.
type SearchUpdated struct {
	Query     string `json:"query"`
	AccountID string `json:"accountId"`
	Added     int    `json:"added"`
}

// AccountEvent is the payload of account-scoped change events.
type AccountEvent struct {
	AccountID string `json:"accountId"`
}

// SyncEvent is the payload of sync:started / sync:finished.
type SyncEvent struct {
	AccountID   string `json:"accountId"`
	NewMessages int    `json:"newMessages"`
	Error       string `json:"error"`
}

// NewMailEvent announces newly arrived inbox mail.
type NewMailEvent struct {
	AccountID string         `json:"accountId"`
	Count     int            `json:"count"`
	Latest    MessageSummary `json:"latest"`
	Senders   []string       `json:"senders"` // distinct sender names, newest first
}

// ClassifyProgress is the payload of classify:progress / classify:finished.
// JobID is "auto" for automatic labeling of new mail and "selection" for
// ClassifyMessages.
type ClassifyProgress struct {
	JobID     string  `json:"jobId"`
	Done      int     `json:"done"`
	Total     int     `json:"total"`
	Labeled   int     `json:"labeled"` // messages whose labels changed
	Skipped   int     `json:"skipped"`
	Failed    int     `json:"failed"`
	Cost      float64 `json:"cost"` // USD so far
	Cancelled bool    `json:"cancelled"`
	Error     string  `json:"error"`
}

// SenderSummary is one sender of a mailbox's folder for "Clean up by
// sender": an address, or every address at a domain.
type SenderSummary struct {
	AccountID   string    `json:"accountId"`
	Key         string    `json:"key"`         // the address, or "@domain" for a domain group
	Address     string    `json:"address"`     // lower-cased; for a domain group, the newest message's
	Name        string    `json:"name"`        // display name of the newest message
	Domain      string    `json:"domain"`      // the address's domain
	Addresses   int       `json:"addresses"`   // distinct addresses in the group (1 for an address)
	Count       int       `json:"count"`       // messages
	Unread      int       `json:"unread"`      // of which unread
	Newest      time.Time `json:"newest"`      // date of the newest message
	Unsubscribe bool      `json:"unsubscribe"` // the newest message offers List-Unsubscribe
}
