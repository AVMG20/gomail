// Package provider defines the interface implemented by the mail backends
// (provider/gmail, provider/outlook and the in-memory demo provider).
package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/safe"
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
	// the end of the second of the given time (OlderBound), newest first
	// (scrolling past the cached range). The cursor's own second is
	// included: servers filter by whole seconds, and messages sharing it
	// with the oldest one cached may not be cached yet.
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
	// DeleteFolder deletes a custom folder on the server. Its mail goes to
	// Trash first (recoverable there); a folder with subfolders is
	// refused. ErrUnsupported where there are no custom folders.
	DeleteFolder(ctx context.Context, folder mail.Folder) error
}

// ErrFolderHasSubfolders refuses deleting a folder with folders inside.
var ErrFolderHasSubfolders = errors.New("this folder has folders inside it; delete those first")

// OlderBound is the exclusive upper date bound ListOlder uses for before:
// the end of before's second.
func OlderBound(before time.Time) time.Time { return before.Truncate(time.Second).Add(time.Second) }

// MaxFolderOp caps how many messages a folder-wide operation touches.
const MaxFolderOp = 20000

// ErrUnsupported is returned for operations a provider can't do.
var ErrUnsupported = errors.New("not supported by this account")

// ErrDraftGone is returned when a draft doesn't exist (any more).
var ErrDraftGone = errors.New("that draft no longer exists")

// PartialError reports an action that failed for some messages only; the
// others were applied on the server.
type PartialError struct {
	Failed []string // remote ids the action was not applied to
	Err    error
}

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// Partial returns err as a PartialError listing failed, unless every id
// failed (then err is returned unchanged) or err is nil.
func Partial(err error, failed []string, total int) error {
	if err == nil || len(failed) == 0 || len(failed) >= total {
		return err
	}
	return &PartialError{Failed: failed, Err: err}
}

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
			if err := safe.Call("provider worker", func() error { return fn(ctx, it) }); err != nil {
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
