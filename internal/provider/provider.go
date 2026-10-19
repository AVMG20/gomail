// Package provider defines the interface implemented by the mail backends
// (provider/gmail, provider/outlook and the in-memory demo provider).
package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
)

// Profile is the signed-in user.
type Profile struct {
	Email string
	Name  string
}

// Limits controls how many recent messages an initial sync fetches.
type Limits struct {
	Inbox  int // default 300
	Other  int // system folders (sent, archive, spam, ...), default 100
	Custom int // custom folders, default 50
}

// WithDefaults fills zero values.
func (l Limits) WithDefaults() Limits {
	if l.Inbox <= 0 {
		l.Inbox = 300
	}
	if l.Other <= 0 {
		l.Other = 100
	}
	if l.Custom <= 0 {
		l.Custom = 50
	}
	return l
}

// SyncRequest is the input of one sync round.
type SyncRequest struct {
	State   map[string]string // opaque provider state from the last round
	Folders []mail.Folder     // as returned by Folders()
	Limits  Limits
	// Progress, if set, is called during long (initial) syncs. Phases:
	// "listing" (done/total folders) and "fetching" (done/total messages).
	Progress func(phase string, done, total int)
}

// Report calls Progress if set.
func (r SyncRequest) Report(phase string, done, total int) {
	if r.Progress != nil {
		r.Progress(phase, done, total)
	}
}

// Provider is a mail account backend. Methods take remote ids.
type Provider interface {
	// Profile returns the account's address and display name.
	Profile(ctx context.Context) (Profile, error)
	// Folders lists folders (with server counts) and provider labels.
	Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error)
	// Sync performs an initial or incremental sync depending on State.
	Sync(ctx context.Context, req SyncRequest) (mail.SyncResult, error)
	// Body fetches the full content of a message.
	Body(ctx context.Context, remoteID string) (*mail.Body, error)
	// Attachment downloads one attachment.
	Attachment(ctx context.Context, remoteID, attachmentID string) ([]byte, error)
	SetRead(ctx context.Context, remoteIDs []string, read bool) error
	SetStarred(ctx context.Context, remoteIDs []string, starred bool) error
	// Move moves messages to the target folder (archive, trash, spam, ...).
	Move(ctx context.Context, remoteIDs []string, target mail.Folder) error
	// PlanMove returns the folder remote ids a message will be in after
	// Move, given its current ones, so the local cache can be updated
	// optimistically.
	PlanMove(current []string, target mail.Folder) []string
	// SetLabel adds or removes a provider label (Gmail label / Outlook
	// category) by name.
	SetLabel(ctx context.Context, remoteIDs []string, labelName string, add bool) error
	// Send sends a new message, reply or forward.
	Send(ctx context.Context, out mail.Outgoing) error

	// ListOlder returns up to limit messages of a folder received before
	// the given time, newest first (scrolling past the cached range).
	ListOlder(ctx context.Context, folder mail.Folder, before time.Time, limit int) ([]mail.Message, error)
	// Search runs a server-side search.
	Search(ctx context.Context, q mail.SearchQuery, limit int) ([]mail.Message, error)

	// SaveDraft creates (draftID "") or replaces a draft and returns its id.
	SaveDraft(ctx context.Context, out mail.Outgoing, draftID string) (string, error)
	// SendDraft updates the draft with out and sends it.
	SendDraft(ctx context.Context, out mail.Outgoing, draftID string) error
	// DeleteDraft discards a draft (missing drafts are not an error).
	DeleteDraft(ctx context.Context, draftID string) error
	// DraftID returns the draft id for a message in the Drafts folder.
	DraftID(ctx context.Context, messageRemoteID string) (string, error)

	// MarkFolderRead marks every message of a folder read on the server.
	MarkFolderRead(ctx context.Context, folder mail.Folder) error
	// EmptyFolder permanently deletes everything in a Trash or Spam folder
	// (Gmail without full-mailbox scope: Spam moves to Trash; Trash is
	// emptied by Gmail itself after 30 days).
	EmptyFolder(ctx context.Context, folder mail.Folder) error
}

// MaxFolderOp caps how many messages a folder-wide operation touches.
const MaxFolderOp = 20000

// ErrUnsupported is returned for operations a provider can't do.
var ErrUnsupported = errors.New("not supported by this account")

// Parallel runs fn over items with at most n concurrent calls and returns
// the first error (after all started calls finish).
func Parallel[T any](ctx context.Context, n int, items []T, fn func(context.Context, T) error) error {
	if n <= 0 {
		n = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(it T) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, it); err != nil {
				once.Do(func() { firstErr = err; cancel() })
			}
		}(it)
	}
	wg.Wait()
	if firstErr == nil && ctx.Err() != nil {
		// parent context cancelled
		return context.Cause(ctx)
	}
	return firstErr
}

// Chunk splits ids into slices of at most n.
func Chunk[T any](ids []T, n int) [][]T {
	var out [][]T
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}
