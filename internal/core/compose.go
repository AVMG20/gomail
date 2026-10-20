package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/safe"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncer"
)

const maxAttachmentBytes = 25 << 20

// draftCoalesce is the minimum time between provider draft saves.
var draftCoalesce = 3 * time.Second

func stripDataURL(s string) string {
	if strings.HasPrefix(s, "data:") {
		if i := strings.Index(s, ","); i >= 0 {
			return s[i+1:]
		}
	}
	return strings.TrimSpace(s)
}

func cleanAddrs(list []models.Address) ([]models.Address, error) {
	out := make([]models.Address, 0, len(list))
	for _, a := range list {
		a.Email = strings.TrimSpace(a.Email)
		a.Name = strings.TrimSpace(a.Name)
		if a.Email == "" {
			continue
		}
		if !strings.Contains(a.Email, "@") || strings.ContainsAny(a.Email, " <>,;\"\r\n") {
			return nil, fmt.Errorf("%q is not a valid email address", a.Email)
		}
		out = append(out, a)
	}
	return out, nil
}

// buildOutgoing validates a compose request. Drafts may lack recipients.
func (s *Service) buildOutgoing(req models.ComposeRequest, draft bool) (models.Account, mail.Outgoing, error) {
	acct, err := s.store.Account(req.AccountID)
	if err != nil {
		return acct, mail.Outgoing{}, errors.New("choose the account to send from")
	}
	out := mail.Outgoing{From: models.Address{Name: acct.DisplayName, Email: acct.Email}, Subject: strings.TrimSpace(req.Subject),
		HTML: req.BodyHTML, Text: req.BodyText, Mode: req.Mode, AttachmentsComplete: req.AttachmentsComplete}
	if out.Mode == "" {
		out.Mode = "new"
	}
	if out.To, err = cleanAddrs(req.To); err != nil {
		return acct, out, err
	}
	if out.Cc, err = cleanAddrs(req.Cc); err != nil {
		return acct, out, err
	}
	if out.Bcc, err = cleanAddrs(req.Bcc); err != nil {
		return acct, out, err
	}
	if !draft && len(out.To)+len(out.Cc)+len(out.Bcc) == 0 {
		return acct, out, errors.New("add at least one recipient")
	}
	if out.HTML == "" && out.Text == "" {
		out.Text = " "
	}
	if out.Text == "" {
		out.Text = mailmime.HTMLToText(out.HTML)
	}
	total := 0
	for _, a := range req.Attachments {
		data, err := base64.StdEncoding.DecodeString(stripDataURL(a.DataBase64))
		if err != nil {
			return acct, out, fmt.Errorf("attachment %s could not be read", a.Filename)
		}
		total += len(data)
		if total > maxAttachmentBytes {
			return acct, out, errors.New("attachments are larger than 25 MB in total")
		}
		out.Attachments = append(out.Attachments, mail.OutgoingAttachment{Filename: sanitizeFilename(filepath.Base(a.Filename)), MimeType: a.MimeType, Data: data})
	}
	if req.ReplyToID != "" && out.Mode != "new" {
		orig, err := s.messageRaw(strings.TrimPrefix(req.ReplyToID, store.ConversationPrefix)) // loads threading headers
		if err != nil {
			return acct, out, err
		}
		if orig.AccountID != acct.ID {
			return acct, out, errors.New("replies must be sent from the account that received the message")
		}
		out.Original = &mail.Message{RemoteID: orig.RemoteID, ThreadID: orig.ThreadID, MessageID: orig.MessageID,
			InReplyTo: orig.InReplyTo, References: orig.References, Subject: orig.Subject}
		if out.Subject == "" {
			if out.Mode == "forward" {
				out.Subject = mailmime.ForwardSubject(orig.Subject)
			} else {
				out.Subject = mailmime.ReplySubject(orig.Subject)
			}
		}
	}
	return acct, out, nil
}

func (s *Service) syncSoon(accountID string, delay time.Duration) {
	s.goBG(func() {
		select {
		case <-time.After(delay):
		case <-s.ctx.Done():
			return
		}
		_ = s.engine.SyncNow(s.ctx, accountID)
	})
}

// Send sends a new message, reply or forward. With req.DraftID the draft
// is updated and sent (and so removed from Drafts).
func (s *Service) Send(req models.ComposeRequest) error {
	acct, out, err := s.buildOutgoing(req, false)
	if err != nil {
		return err
	}
	p, err := s.provider(acct.ID)
	if err != nil {
		return errors.New(syncer.HumanError(acct, err))
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Minute)
	defer cancel()
	if req.DraftID != "" {
		h, err := s.resolveDraft(ctx, req.DraftID)
		if err != nil && !draftMissing(err) {
			// Sending as a new message would leave the draft behind.
			return fmt.Errorf("sending failed: %s", syncer.HumanError(acct, err))
		}
		if err == nil {
			s.drafts.cancel(h.String()) // also waits for an autosave in flight
		}
		switch {
		case err != nil:
			slog.Warn("draft no longer exists, sending as a new message", "err", err)
		case h.accountID != acct.ID:
			// The From account changed: discard the old draft.
			if op, err := s.provider(h.accountID); err == nil {
				_ = op.DeleteDraft(ctx, h.remoteID)
			}
		default:
			if err := p.SendDraft(ctx, out, h.remoteID); err != nil {
				return fmt.Errorf("sending failed: %s", syncer.HumanError(acct, err))
			}
			s.syncSoon(acct.ID, 2*time.Second)
			return nil
		}
	}
	if err := p.Send(ctx, out); err != nil {
		return fmt.Errorf("sending failed: %s", syncer.HumanError(acct, err))
	}
	s.syncSoon(acct.ID, 2*time.Second)
	return nil
}

// ---- drafts ----

// draftHandle identifies a provider draft: "draft:<account id>:<remote id>".
type draftHandle struct{ accountID, remoteID string }

const draftPrefix = "draft:"

func (h draftHandle) String() string { return draftPrefix + h.accountID + ":" + h.remoteID }

func parseDraftHandle(id string) (draftHandle, bool) {
	rest, ok := strings.CutPrefix(id, draftPrefix)
	if !ok {
		return draftHandle{}, false
	}
	acct, remote, ok := strings.Cut(rest, ":")
	return draftHandle{acct, remote}, ok && acct != "" && remote != ""
}

var errNotDraft = errors.New("that message is not a draft")

// draftMissing reports whether a resolveDraft error means there is no such
// draft (as opposed to a transient failure looking it up).
func draftMissing(err error) bool {
	return errors.Is(err, provider.ErrDraftGone) || errors.Is(err, errNotDraft)
}

// resolveDraft accepts a draft handle or the local id of a message in a
// Drafts folder.
func (s *Service) resolveDraft(ctx context.Context, id string) (draftHandle, error) {
	if h, ok := parseDraftHandle(id); ok {
		return s.drafts.resolve(h), nil
	}
	m, _, err := s.store.Message(strings.TrimPrefix(id, store.ConversationPrefix))
	if err != nil {
		return draftHandle{}, provider.ErrDraftGone
	}
	if !m.IsDraft {
		return draftHandle{}, errNotDraft
	}
	p, err := s.provider(m.AccountID)
	if err != nil {
		return draftHandle{}, err
	}
	rid, err := p.DraftID(ctx, m.RemoteID)
	if err != nil {
		return draftHandle{}, err
	}
	return draftHandle{m.AccountID, rid}, nil
}

// draftSaver coalesces autosaves per draft.
type draftSaver struct {
	mu      sync.Mutex
	entries map[string]*draftEntry // by handle string
	// moved maps the handle of a draft that was recreated (deleted
	// elsewhere) to its new handle, for saves still using the old one.
	moved map[string]string
}

type draftEntry struct {
	last    time.Time
	pending *mail.Outgoing
	timer   *time.Timer
	// flushing is closed when the coalesced save in flight finishes.
	flushing chan struct{}
}

// flushWait bounds how long send/discard wait for an autosave in flight.
var flushWait = 30 * time.Second

// cancel drops pending autosaves of a draft and waits for one in flight,
// so it can't land (and recreate the draft) after a send or discard.
func (d *draftSaver) cancel(handle string) {
	d.mu.Lock()
	e, ok := d.entries[handle]
	var flushing chan struct{}
	if ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		flushing = e.flushing
		delete(d.entries, handle)
	}
	d.mu.Unlock()
	waitFlush(flushing)
}

func waitFlush(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-time.After(flushWait):
	}
}

// resolve follows recreated drafts to their current handle.
func (d *draftSaver) resolve(h draftHandle) draftHandle {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := 0; i < 8; i++ {
		next, ok := d.moved[h.String()]
		if !ok {
			break
		}
		h, _ = parseDraftHandle(next)
	}
	return h
}

// recreated records that the draft old now lives at rid.
func (d *draftSaver) recreated(old draftHandle, rid string) draftHandle {
	h := draftHandle{old.accountID, rid}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.moved == nil {
		d.moved = map[string]string{}
	}
	d.moved[old.String()] = h.String()
	if e, ok := d.entries[old.String()]; ok {
		delete(d.entries, old.String())
		d.entries[h.String()] = e
	}
	return h
}

// SaveDraft creates or updates a draft and returns its handle. Saves of the
// same draft within draftCoalesce are merged: the latest content is written
// when the window ends.
func (s *Service) SaveDraft(req models.ComposeRequest, draftID string) (string, error) {
	acct, out, err := s.buildOutgoing(req, true)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	var h draftHandle
	if draftID != "" {
		if h, err = s.resolveDraft(ctx, draftID); err != nil {
			if !draftMissing(err) {
				// Creating a new draft would leave the original behind.
				return "", fmt.Errorf("saving the draft failed: %s", syncer.HumanError(acct, err))
			}
			h = draftHandle{}
		}
	}
	p, err := s.provider(acct.ID)
	if err != nil {
		return "", errors.New(syncer.HumanError(acct, err))
	}
	if h.remoteID != "" && h.accountID != acct.ID {
		if op, err := s.provider(h.accountID); err == nil {
			_ = op.DeleteDraft(ctx, h.remoteID)
		}
		s.drafts.cancel(h.String())
		h = draftHandle{}
	}
	if h.remoteID == "" {
		rid, err := p.SaveDraft(ctx, out, "")
		if err != nil {
			return "", fmt.Errorf("saving the draft failed: %s", syncer.HumanError(acct, err))
		}
		h = draftHandle{acct.ID, rid}
		s.drafts.mu.Lock()
		s.drafts.entries[h.String()] = &draftEntry{last: time.Now()}
		s.drafts.mu.Unlock()
		s.syncSoon(acct.ID, 5*time.Second)
		return h.String(), nil
	}
	key := h.String()
	s.drafts.mu.Lock()
	e := s.drafts.entries[key]
	if e == nil {
		e = &draftEntry{}
		s.drafts.entries[key] = e
	}
	if wait := draftCoalesce - time.Since(e.last); wait > 0 {
		o := out
		e.pending = &o
		if e.timer == nil {
			e.timer = time.AfterFunc(wait, func() {
				defer safe.Recover("draft autosave")
				s.flushDraft(key)
			})
		}
		s.drafts.mu.Unlock()
		return key, nil
	}
	e.last, e.pending = time.Now(), nil
	flushing := e.flushing
	s.drafts.mu.Unlock()
	waitFlush(flushing) // an older coalesced save must not land after this one
	rid, err := p.SaveDraft(ctx, out, h.remoteID)
	if err != nil {
		return "", fmt.Errorf("saving the draft failed: %s", syncer.HumanError(acct, err))
	}
	if rid != "" && rid != h.remoteID { // recreated (deleted elsewhere)
		h = s.drafts.recreated(h, rid)
	}
	return h.String(), nil
}

// flushPendingDrafts writes every coalesced draft save now (shutdown),
// waiting at most wait.
func (s *Service) flushPendingDrafts(wait time.Duration) {
	s.drafts.mu.Lock()
	var keys []string
	for k, e := range s.drafts.entries {
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
		if e.pending != nil {
			keys = append(keys, k)
		}
	}
	s.drafts.mu.Unlock()
	if len(keys) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for _, k := range keys {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer safe.Recover("draft flush")
				s.flushDraft(k)
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(wait):
		slog.Warn("saving drafts at shutdown took too long", "drafts", len(keys))
	}
}

func (s *Service) flushDraft(key string) {
	s.drafts.mu.Lock()
	e, ok := s.drafts.entries[key]
	if !ok || e.pending == nil {
		if ok {
			e.timer = nil
		}
		s.drafts.mu.Unlock()
		return
	}
	out := *e.pending
	e.pending, e.timer, e.last = nil, nil, time.Now()
	done := make(chan struct{})
	e.flushing = done
	s.drafts.mu.Unlock()
	defer func() {
		s.drafts.mu.Lock()
		if e.flushing == done {
			e.flushing = nil
		}
		s.drafts.mu.Unlock()
		close(done)
	}()
	h, _ := parseDraftHandle(key)
	p, err := s.provider(h.accountID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	rid, err := p.SaveDraft(ctx, out, h.remoteID)
	if err != nil {
		slog.Warn("autosaving draft failed", "account", h.accountID, "err", err)
		return
	}
	if rid != "" && rid != h.remoteID { // recreated (deleted elsewhere)
		s.drafts.recreated(h, rid)
	}
}

// DeleteDraft discards a draft (handle or local draft message id).
func (s *Service) DeleteDraft(id string) error {
	ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()
	h, err := s.resolveDraft(ctx, id)
	if err != nil {
		return err
	}
	s.drafts.cancel(h.String())
	p, err := s.provider(h.accountID)
	if err != nil {
		return err
	}
	if err := p.DeleteDraft(ctx, h.remoteID); err != nil {
		acct, _ := s.store.Account(h.accountID)
		return fmt.Errorf("deleting the draft failed: %s", syncer.HumanError(acct, err))
	}
	if !strings.HasPrefix(id, draftPrefix) {
		// Remove the cached draft message right away.
		_, _ = s.store.ApplySync(h.accountID, mail.SyncResult{Deleted: []string{mustRemoteID(s, id)}}, nil)
		s.emit(models.EventMessagesChanged, models.AccountEvent{AccountID: h.accountID})
	}
	s.syncSoon(h.accountID, 2*time.Second)
	return nil
}

func mustRemoteID(s *Service, localID string) string {
	m, _, err := s.store.Message(strings.TrimPrefix(localID, store.ConversationPrefix))
	if err != nil {
		return ""
	}
	return m.RemoteID
}
