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
}

// MessageLabel is one label assignment on a message.
type MessageLabel struct {
	LabelID    string  `json:"labelId"`
	Source     string  `json:"source"`     // "ai" | "manual"
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
	// HasRemoteContent reports whether BodyHTML references remote resources
	// (http/https images, stylesheets). The UI can offer "Show images".
	HasRemoteContent bool `json:"hasRemoteContent"`
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

type MessagePage struct {
	Messages   []MessageSummary `json:"messages"`
	NextCursor string           `json:"nextCursor"`
	Total      int              `json:"total"`
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
	Mode        string               `json:"mode"`      // "new" | "reply" | "replyAll" | "forward"
	Attachments []OutgoingAttachment `json:"attachments"`
}

// Label is a user-defined label. Labels are applied manually or by the AI
// classifier (OpenRouter Jev), which uses Description as guidance.
type Label struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"` // guidance passed to the model
	Color       string `json:"color"`
}

type Settings struct {
	Theme                string  `json:"theme"`   // "system" | "light" | "dark"
	Density              string  `json:"density"` // "comfortable" | "compact"
	SyncIntervalMinutes  int     `json:"syncIntervalMinutes"`
	ShowRemoteImages     bool    `json:"showRemoteImages"`
	GoogleClientID       string  `json:"googleClientId"`
	GoogleClientSecret   string  `json:"googleClientSecret"`
	MicrosoftClientID    string  `json:"microsoftClientId"`
	MicrosoftTenant      string  `json:"microsoftTenant"`   // default "common"
	OpenRouterKeySet     bool    `json:"openRouterKeySet"`  // read-only; the key itself never leaves the backend
	OpenRouterModel      string  `json:"openRouterModel"`   // chat model (grammar, summaries, drafts)
	ClassifierModel      string  `json:"classifierModel"`   // OpenRouter Decisions model, default "typesafe/jev-1.13"
	ClassifyThreshold    float64 `json:"classifyThreshold"` // 0..1; below this confidence a message stays unlabeled
	AutoClassify         bool    `json:"autoClassify"`
	Labels               []Label `json:"labels"`
	GrammarSystemPrompt  string  `json:"grammarSystemPrompt"`  // "" = built-in default (see GetDefaultPrompts)
	ClassifyInstructions string  `json:"classifyInstructions"` // Jev "label" question instructions; "" = default
	PrimaryColor         string  `json:"primaryColor"`         // hex accent color, e.g. "#2563eb"
	Radius               float64 `json:"radius"`               // UI corner radius in rem
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
}

type ClassificationResult struct {
	MessageID  string  `json:"messageId"`
	Label      string  `json:"label"` // assigned label id, "" when none applied (or below threshold)
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Event names emitted to the frontend via the Wails runtime.
const (
	EventSyncStarted      = "sync:started"      // payload: {accountId}
	EventSyncFinished     = "sync:finished"     // payload: {accountId, newMessages, error}
	EventMessagesChanged  = "messages:changed"  // payload: {accountId}
	EventAccountsChanged  = "accounts:changed"  // payload: none
	EventClassifyProgress = "classify:progress" // payload: {done, total, cost}
	EventNewMail          = "mail:new"          // payload: {accountId, count, latest: MessageSummary}
)
