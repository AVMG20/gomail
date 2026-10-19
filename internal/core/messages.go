package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/config"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/store"
	"github.com/AVMG20/gomail/internal/syncer"
)

func notFound(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("that message no longer exists")
	}
	return err
}

// Folders lists folders of an account ("" = all).
func (s *Service) Folders(accountID string) ([]models.Folder, error) {
	return s.store.Folders(accountID)
}

// ListMessages returns one page of the message list.
func (s *Service) ListMessages(q models.ListQuery) (models.MessagePage, error) {
	return s.store.ListMessages(q)
}

// SearchMessages powers the command palette.
func (s *Service) SearchMessages(q models.SearchQuery) ([]models.SearchResult, error) {
	return s.store.Search(q)
}

// ResolveQueryIDs returns ids of messages matching q (max capped at 5000).
func (s *Service) ResolveQueryIDs(q models.ListQuery, max int) ([]string, error) {
	if max <= 0 || max > syncer.MaxBulk {
		max = syncer.MaxBulk
	}
	return s.store.ResolveIDs(q, store.FilterOptions{}, max)
}

// LabelCounts returns per-label counts (Trash excluded).
func (s *Service) LabelCounts(accountID string) ([]models.LabelCount, error) {
	return s.store.LabelCounts(accountID)
}

// Message returns a message with its body, fetching and caching it first
// when needed.
func (s *Service) Message(id string) (models.Message, error) {
	m, has, err := s.store.Message(id)
	if err != nil {
		return m, notFound(err)
	}
	if has {
		return m, nil
	}
	if err := s.fetchBody(s.ctx, m); err != nil {
		return m, err
	}
	m, _, err = s.store.Message(id)
	return m, notFound(err)
}

func (s *Service) fetchBody(ctx context.Context, m models.Message) error {
	p, err := s.provider(m.AccountID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	b, err := p.Body(ctx, m.RemoteID)
	if err != nil {
		acct, _ := s.store.Account(m.AccountID)
		return fmt.Errorf("couldn't load the message: %s", syncer.HumanError(acct, err))
	}
	return s.store.SaveBody(m.ID, processBody(b))
}

// processBody embeds cid: images, sanitizes HTML and fills text.
func processBody(b *mail.Body) store.BodyUpdate {
	var images []mailmime.InlineImage
	atts := make([]models.Attachment, 0, len(b.Attachments))
	for _, a := range b.Attachments {
		if a.ContentID != "" && len(a.Data) > 0 {
			images = append(images, mailmime.InlineImage{ContentID: a.ContentID, MimeType: a.MimeType, Data: a.Data})
		}
		atts = append(atts, a.Attachment)
	}
	html := b.HTML
	text := b.Text
	if html != "" {
		html = mailmime.SanitizeHTML(mailmime.RewriteCID(html, images))
		if strings.TrimSpace(text) == "" {
			text = mailmime.HTMLToText(b.HTML)
		}
	} else if text != "" {
		html = mailmime.TextToHTML(text)
	}
	return store.BodyUpdate{
		HTML: html, Text: text, HasRemoteContent: mailmime.HasRemoteContent(html), Attachments: atts,
		MessageID: b.MessageID, InReplyTo: b.InReplyTo, References: b.References, Cc: b.Cc, Bcc: b.Bcc, ReplyTo: b.ReplyTo,
	}
}

// Thread returns the thread of a message, oldest first, with bodies.
func (s *Service) Thread(id string) ([]models.Message, error) {
	m, _, err := s.store.Message(id)
	if err != nil {
		return nil, notFound(err)
	}
	if m.ThreadID == "" {
		full, err := s.Message(id)
		return []models.Message{full}, err
	}
	sums, err := s.store.Thread(m.AccountID, m.ThreadID)
	if err != nil {
		return nil, err
	}
	out := make([]models.Message, len(sums))
	errs := make([]error, len(sums))
	_ = provider.Parallel(s.ctx, 4, indexes(len(sums)), func(ctx context.Context, i int) error {
		out[i], errs[i] = s.Message(sums[i].ID)
		return nil
	})
	for i, err := range errs {
		if err != nil {
			if sums[i].ID == id {
				return nil, err
			}
			// Keep the thread usable; show the summary without a body.
			out[i] = models.Message{MessageSummary: sums[i], BodyText: sums[i].Snippet, Attachments: []models.Attachment{}}
		}
	}
	return out, nil
}

func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// ---- actions ----

func (s *Service) MarkRead(ids []string, read bool) error { return s.engine.MarkRead(ids, read) }
func (s *Service) SetStarred(ids []string, starred bool) error {
	return s.engine.SetStarred(ids, starred)
}
func (s *Service) Archive(ids []string) error { return s.engine.MoveToKind(ids, models.FolderArchive) }
func (s *Service) Trash(ids []string) error   { return s.engine.MoveToKind(ids, models.FolderTrash) }
func (s *Service) MoveToFolder(ids []string, folderID string) error {
	return s.engine.MoveToFolder(ids, folderID)
}

func (s *Service) label(id string) (models.Label, error) {
	for _, l := range s.Settings().Labels {
		if l.ID == id {
			return l, nil
		}
	}
	return models.Label{}, errors.New("that label no longer exists")
}

// AddLabel assigns a label manually.
func (s *Service) AddLabel(ids []string, labelID string) error {
	l, err := s.label(labelID)
	if err != nil {
		return err
	}
	if len(ids) > syncer.MaxBulk {
		return fmt.Errorf("too many messages selected (max %d)", syncer.MaxBulk)
	}
	if err := s.store.AddLabels(ids, labelID); err != nil {
		return err
	}
	s.messagesChanged(ids)
	if l.Source == config.LabelSourceProvider {
		s.engine.PushLabel(ids, l.Name, true)
	}
	return nil
}

// RemoveLabel removes a label (whatever assigned it).
func (s *Service) RemoveLabel(ids []string, labelID string) error {
	l, err := s.label(labelID)
	if err != nil {
		return err
	}
	if len(ids) > syncer.MaxBulk {
		return fmt.Errorf("too many messages selected (max %d)", syncer.MaxBulk)
	}
	if err := s.store.RemoveLabels(ids, labelID); err != nil {
		return err
	}
	s.messagesChanged(ids)
	if l.Source == config.LabelSourceProvider {
		s.engine.PushLabel(ids, l.Name, false)
	}
	return nil
}

func (s *Service) messagesChanged(ids []string) {
	accts := map[string]bool{}
	if sums, err := s.store.Summaries(ids); err == nil {
		for _, m := range sums {
			accts[m.AccountID] = true
		}
	}
	for a := range accts {
		s.emit(models.EventMessagesChanged, models.AccountEvent{AccountID: a})
	}
	s.emit(models.EventLabelsChanged, models.AccountEvent{})
}

// ---- compose ----

const maxAttachmentBytes = 25 << 20

// Send sends a new message, reply or forward.
func (s *Service) Send(req models.ComposeRequest) error {
	acct, err := s.store.Account(req.AccountID)
	if err != nil {
		return errors.New("choose the account to send from")
	}
	clean := func(list []models.Address) ([]models.Address, error) {
		out := make([]models.Address, 0, len(list))
		for _, a := range list {
			a.Email = strings.TrimSpace(a.Email)
			a.Name = strings.TrimSpace(a.Name)
			if a.Email == "" {
				continue
			}
			if !strings.Contains(a.Email, "@") || strings.ContainsAny(a.Email, " <>,;\"") {
				return nil, fmt.Errorf("%q is not a valid email address", a.Email)
			}
			out = append(out, a)
		}
		return out, nil
	}
	out := mail.Outgoing{From: models.Address{Name: acct.DisplayName, Email: acct.Email}, Subject: strings.TrimSpace(req.Subject),
		HTML: req.BodyHTML, Text: req.BodyText, Mode: req.Mode}
	if out.Mode == "" {
		out.Mode = "new"
	}
	if out.To, err = clean(req.To); err != nil {
		return err
	}
	if out.Cc, err = clean(req.Cc); err != nil {
		return err
	}
	if out.Bcc, err = clean(req.Bcc); err != nil {
		return err
	}
	if len(out.To)+len(out.Cc)+len(out.Bcc) == 0 {
		return errors.New("add at least one recipient")
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
			return fmt.Errorf("attachment %s could not be read", a.Filename)
		}
		total += len(data)
		if total > maxAttachmentBytes {
			return errors.New("attachments are larger than 25 MB in total")
		}
		out.Attachments = append(out.Attachments, mail.OutgoingAttachment{Filename: filepath.Base(a.Filename), MimeType: a.MimeType, Data: data})
	}
	if req.ReplyToID != "" && out.Mode != "new" {
		orig, err := s.Message(req.ReplyToID) // ensures threading headers are loaded
		if err != nil {
			return err
		}
		if orig.AccountID != acct.ID {
			return errors.New("replies must be sent from the account that received the message")
		}
		out.Original = &mail.Message{RemoteID: orig.RemoteID, ThreadID: orig.ThreadID, MessageID: orig.MessageID,
			References: orig.References, Subject: orig.Subject}
		if out.Subject == "" {
			if out.Mode == "forward" {
				out.Subject = mailmime.ForwardSubject(orig.Subject)
			} else {
				out.Subject = mailmime.ReplySubject(orig.Subject)
			}
		}
	}
	p, err := s.provider(acct.ID)
	if err != nil {
		return errors.New(syncer.HumanError(acct, err))
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Minute)
	defer cancel()
	if err := p.Send(ctx, out); err != nil {
		return fmt.Errorf("sending failed: %s", syncer.HumanError(acct, err))
	}
	s.goBG(func() {
		select {
		case <-time.After(2 * time.Second):
		case <-s.ctx.Done():
			return
		}
		_ = s.engine.SyncNow(s.ctx, acct.ID)
	})
	return nil
}

func stripDataURL(s string) string {
	if strings.HasPrefix(s, "data:") {
		if i := strings.Index(s, ","); i >= 0 {
			return s[i+1:]
		}
	}
	return strings.TrimSpace(s)
}

// SaveAttachment asks where to save an attachment and writes it.
func (s *Service) SaveAttachment(messageID, attachmentID string) error {
	m, _, err := s.store.Message(messageID)
	if err != nil {
		return notFound(err)
	}
	att, err := s.store.Attachment(messageID, attachmentID)
	if err != nil {
		return errors.New("that attachment no longer exists")
	}
	name := sanitizeFilename(att.Filename)
	path, err := s.ui.SaveFile(name)
	if err != nil {
		return err
	}
	if path == "" {
		return nil // cancelled
	}
	p, err := s.provider(m.AccountID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	data, err := p.Attachment(ctx, m.RemoteID, attachmentID)
	if err != nil {
		acct, _ := s.store.Account(m.AccountID)
		return fmt.Errorf("downloading the attachment failed: %s", syncer.HumanError(acct, err))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("could not save the file: %w", err)
	}
	return nil
}

func sanitizeFilename(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, n)
	if n == "" || n == "." || n == ".." {
		n = "attachment"
	}
	return n
}

// OpenExternalURL opens http(s)/mailto links in the system browser.
func (s *Service) OpenExternalURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("invalid link")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return s.ui.OpenURL(u.String())
	}
	return errors.New("only web and mailto links can be opened")
}
