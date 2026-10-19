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
	AccountID  string   `json:"accountId"`
	FolderID   string   `json:"folderId"`
	FolderKind string   `json:"folderKind"`
	Labels     []string `json:"labels"`    // messages having ANY of these label ids; empty = no label filter
	Unlabeled  bool     `json:"unlabeled"` // only messages without any label
	Search     string   `json:"search"`
	UnreadOnly bool     `json:"unreadOnly"`
	Limit      int      `json:"limit"`
	Cursor     string   `json:"cursor"`
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

	GoogleClientID     string `json:"googleClientId"`
	GoogleClientSecret string `json:"googleClientSecret"`
	MicrosoftClientID  string `json:"microsoftClientId"`
	MicrosoftTenant    string `json:"microsoftTenant"` // default "common"

	OpenRouterKeySet bool   `json:"openRouterKeySet"` // read-only; the key itself never leaves the backend
	OpenRouterModel  string `json:"openRouterModel"`  // chat model (grammar, summaries, drafts)
	ClassifierModel  string `json:"classifierModel"`  // OpenRouter Decisions model, default "typesafe/jev-1.13"

	// Labels and AI labeling.
	Labels                   []Label `json:"labels"`
	ImportProviderLabels     bool    `json:"importProviderLabels"`     // show Gmail labels / Outlook categories as labels (default true)
	AutoClassify             bool    `json:"autoClassify"`             // AI-label new incoming mail
	ClassifyThreshold        float64 `json:"classifyThreshold"`        // AI adds a label when P(yes) >= this (default 0.5)
	ClassifyRemoveThreshold  float64 `json:"classifyRemoveThreshold"`  // AI removes provider/Junk labels when P(yes) < this (default 0.15)
	AICorrectsProviderLabels bool    `json:"aiCorrectsProviderLabels"` // allow those removals (default true)
	ClassifyInstructions     string  `json:"classifyInstructions"`     // per-label question template; "" = default. "{label}" is replaced by the label name
	GrammarSystemPrompt      string  `json:"grammarSystemPrompt"`      // "" = built-in default (see GetDefaultPrompts)
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
	PriceKnown       bool    `json:"priceKnown"` // false when OpenRouter lists no price for the classifier model
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
	Cost      float64            `json:"cost"`  // USD
	Error     string             `json:"error"` // per-message failure, "" on success
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
)

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
