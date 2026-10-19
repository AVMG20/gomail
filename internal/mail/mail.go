// Package mail holds provider-neutral message types exchanged between the
// provider clients, the local store and the sync engine. Unlike
// internal/models these types are not part of the frontend contract.
package mail

import (
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// Folder is a mailbox folder (Graph mailFolder) or label (Gmail label) as
// reported by a provider.
type Folder struct {
	RemoteID string
	Name     string
	Kind     string // models.Folder* constant
	Unread   int
	Total    int
}

// Message is the metadata of a remote message, enough to render a list row
// and to reply.
type Message struct {
	RemoteID        string
	ThreadID        string
	FolderRemoteIDs []string
	From            models.Address
	To              []models.Address
	Cc              []models.Address
	Bcc             []models.Address
	ReplyTo         []models.Address
	Subject         string
	Snippet         string
	Date            time.Time
	Unread          bool
	Starred         bool
	HasAttachments  bool
	MessageID       string // RFC 5322 Message-ID
	InReplyTo       string
	References      string
	// Labels are the provider label names on the message (Gmail user labels,
	// Outlook categories). System labels/folders are never included.
	Labels []string
	// Unsubscribe holds List-Unsubscribe info when known at sync time.
	Unsubscribe Unsubscribe
}

// Unsubscribe is parsed List-Unsubscribe / List-Unsubscribe-Post data.
type Unsubscribe struct {
	URL      string // https URL
	Mailto   string // mailto: URI
	OneClick bool   // RFC 8058 List-Unsubscribe-Post: List-Unsubscribe=One-Click
}

// Empty reports whether no unsubscribe method is known.
func (u Unsubscribe) Empty() bool { return u.URL == "" && u.Mailto == "" }

// Label is a provider-side label definition (Gmail user label, Outlook
// master category).
type Label struct {
	RemoteID string // Gmail label id; Outlook category display name
	Name     string
	Color    string // hex, "" if none
}

// Attachment describes an attachment; Data is only filled for inline parts
// that are embedded into the HTML body (cid: images) or when downloading.
type Attachment struct {
	models.Attachment
	Data []byte
}

// Body is the full content of a message.
type Body struct {
	HTML        string // raw (unsanitized) HTML from the provider
	Text        string
	Attachments []Attachment
	// Optional header data discovered while fetching the full message.
	MessageID   string
	InReplyTo   string
	References  string
	Cc          []models.Address
	Bcc         []models.Address
	ReplyTo     []models.Address
	Unsubscribe Unsubscribe
}

// SyncResult is what a provider returns from one sync round.
type SyncResult struct {
	// Upserts are new or changed messages; their FolderRemoteIDs replace the
	// stored folder memberships.
	Upserts []Message
	// Deleted lists remote ids of messages that no longer exist.
	Deleted []string
	// Removed maps folder remote id -> message remote ids that left that
	// folder (Graph delta "@removed"). Messages left without any folder are
	// dropped locally.
	Removed map[string][]string
	// State is the new opaque provider sync state (history id, delta links).
	// Keys present here replace stored keys; keys with "" values are removed.
	State map[string]string
	// Initial is true when this round was a full (re)synchronisation; the
	// engine then doesn't announce the messages as new mail.
	Initial bool
}

// Outgoing is a message to send.
type Outgoing struct {
	From        models.Address
	To          []models.Address
	Cc          []models.Address
	Bcc         []models.Address
	Subject     string
	HTML        string
	Text        string
	Attachments []OutgoingAttachment
	Mode        string // "new" | "reply" | "replyAll" | "forward"
	// Original is set for replies/forwards.
	Original *Message
	// ForwardAttachments are the original's attachments to include when
	// forwarding (filled by the core for providers that need them).
	ForwardAttachments []OutgoingAttachment
}

// SearchQuery is a provider-neutral server search.
type SearchQuery struct {
	Terms         []string // words or phrases
	From, To      []string
	Subject       []string
	Unread        *bool
	Starred       *bool
	HasAttachment bool
	Before, After time.Time
}

// Empty reports whether the query has no criteria.
func (q SearchQuery) Empty() bool {
	return len(q.Terms)+len(q.From)+len(q.To)+len(q.Subject) == 0 && q.Unread == nil && q.Starred == nil &&
		!q.HasAttachment && q.Before.IsZero() && q.After.IsZero()
}

type OutgoingAttachment struct {
	Filename string
	MimeType string
	Data     []byte
}
