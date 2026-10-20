package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
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

// plainID strips a conversation prefix (reading a conversation row opens
// its latest message).
func plainID(id string) string { return strings.TrimPrefix(id, store.ConversationPrefix) }

// Folders lists folders of an account ("" = all).
func (s *Service) Folders(accountID string) ([]models.Folder, error) {
	return s.store.Folders(accountID)
}

// ListMessages returns one page of the message list (conversations when
// Settings.conversationView is on).
func (s *Service) ListMessages(q models.ListQuery) (models.MessagePage, error) {
	defer s.markFirstQuery()
	page, err := s.store.ListMessages(q, store.ListOptions{Conversations: s.Settings().ConversationView})
	if err == nil && strings.TrimSpace(q.Search) != "" && len(page.Messages) < remoteSearchBelow {
		s.remoteSearch(q.Search, q.AccountID)
	}
	return page, err
}

// SearchMessages powers the command palette. When few local results match,
// a server-side search runs in the background; when it caches new mail a
// search:updated event asks the UI to search again.
func (s *Service) SearchMessages(q models.SearchQuery) ([]models.SearchResult, error) {
	res, err := s.store.Search(q)
	if err == nil && len(res) < remoteSearchBelow {
		s.remoteSearch(q.Query, q.AccountID)
	}
	return res, err
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

// messageRaw returns a message with its body (fetched and cached when
// needed) without remote-content blocking.
func (s *Service) messageRaw(id string) (models.Message, error) {
	id = plainID(id)
	m, has, err := s.store.Message(id)
	if err != nil {
		return m, notFound(err)
	}
	if !has {
		if err := s.fetchBody(s.ctx, m); err != nil {
			return m, err
		}
		if m, _, err = s.store.Message(id); err != nil {
			return m, notFound(err)
		}
	}
	s.inviteEvent(&m)
	return m, nil
}

// Message returns a message with its body. Remote images/CSS are removed
// unless Settings.showRemoteImages is on (RemoteContentBlocked tells).
func (s *Service) Message(id string) (models.Message, error) {
	m, err := s.messageRaw(id)
	if err == nil && !s.Settings().ShowRemoteImages {
		m.BodyHTML, m.RemoteContentBlocked = mailmime.BlockRemoteContent(m.BodyHTML)
	}
	return m, err
}

// MessageWithRemoteContent returns the message with remote content kept.
func (s *Service) MessageWithRemoteContent(id string) (models.Message, error) {
	return s.messageRaw(id)
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
	invite := b.Invite
	if invite == nil {
		invite = calendar.ParseInvite(b.Calendar)
	}
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
		Unsubscribe: b.Unsubscribe, Invite: invite,
	}
}

// Thread returns the thread of a message, oldest first, with bodies.
func (s *Service) Thread(id string) ([]models.Message, error) {
	id = plainID(id)
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
//
// Every action accepts message ids and conversation ids ("c:<id>" from a
// conversation row). Conversation ids expand server-side: read state and
// labels apply to the whole thread, moves to the thread's messages in the
// row's folder, starring to the latest message (unstarring to all).

func (s *Service) expand(ids []string, mode store.ExpandMode) ([]string, error) {
	return s.store.ExpandConversations(ids, mode)
}

func (s *Service) MarkRead(ids []string, read bool) error {
	ids, err := s.expand(ids, store.ExpandThread)
	if err != nil {
		return err
	}
	return s.engine.MarkRead(ids, read)
}

func (s *Service) SetStarred(ids []string, starred bool) error {
	mode := store.ExpandThread
	if starred {
		mode = store.ExpandAnchor
	}
	ids, err := s.expand(ids, mode)
	if err != nil {
		return err
	}
	return s.engine.SetStarred(ids, starred)
}

func (s *Service) Archive(ids []string) error {
	ids, err := s.expand(ids, store.ExpandFolder)
	if err != nil {
		return err
	}
	return s.engine.MoveToKind(ids, models.FolderArchive)
}

func (s *Service) Trash(ids []string) error {
	ids, err := s.expand(ids, store.ExpandFolder)
	if err != nil {
		return err
	}
	return s.engine.MoveToKind(ids, models.FolderTrash)
}

func (s *Service) MoveToFolder(ids []string, folderID string) error {
	ids, err := s.expand(ids, store.ExpandFolder)
	if err != nil {
		return err
	}
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

// AddLabel assigns a label manually (pushed to Gmail/Outlook for
// provider-sourced labels).
func (s *Service) AddLabel(ids []string, labelID string) error {
	return s.changeLabel(ids, labelID, true)
}

// RemoveLabel removes a label (whatever assigned it).
func (s *Service) RemoveLabel(ids []string, labelID string) error {
	return s.changeLabel(ids, labelID, false)
}

func (s *Service) changeLabel(ids []string, labelID string, add bool) error {
	l, err := s.label(labelID)
	if err != nil {
		return err
	}
	if ids, err = s.expand(ids, store.ExpandThread); err != nil {
		return err
	}
	if len(ids) > syncer.MaxBulk {
		return fmt.Errorf("too many messages selected (max %d)", syncer.MaxBulk)
	}
	if add {
		err = s.store.AddLabels(ids, labelID)
	} else {
		err = s.store.RemoveLabels(ids, labelID)
	}
	if err != nil {
		return err
	}
	s.messagesChanged(ids)
	if l.Source == config.LabelSourceProvider {
		s.engine.PushLabel(ids, l.Name, add)
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

// ---- older mail & server search ----

const (
	olderPageSize     = 50
	remoteSearchBelow = 5 // run a server search when fewer local results
	remoteSearchLimit = 50
	remoteSearchTTL   = 2 * time.Minute
)

// LoadOlder fetches the next page of older mail from the server for the
// folders of a view (FolderID, or FolderKind across accounts). It returns
// how many messages were added; messages:changed follows.
func (s *Service) LoadOlder(q models.ListQuery) (int, error) {
	folders, err := s.store.QueryFolders(q)
	if err != nil {
		return 0, err
	}
	if len(folders) == 0 {
		return 0, nil
	}
	var mu sync.Mutex
	added := 0
	var errs []string
	_ = provider.Parallel(s.ctx, 4, folders, func(ctx context.Context, f models.Folder) error {
		n, err := s.loadOlderFolder(ctx, f)
		mu.Lock()
		defer mu.Unlock()
		added += n
		if err != nil {
			acct, _ := s.store.Account(f.AccountID)
			errs = append(errs, fmt.Sprintf("%s: %s", acct.Email, syncer.HumanError(acct, err)))
		}
		return nil
	})
	if len(errs) > 0 && added == 0 {
		return 0, errors.New("couldn't load older mail: " + strings.Join(errs, "; "))
	}
	return added, nil
}

func (s *Service) loadOlderFolder(ctx context.Context, f models.Folder) (int, error) {
	p, err := s.provider(f.AccountID)
	if err != nil {
		return 0, err
	}
	before, err := s.store.OldestInFolder(f.ID)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	msgs, err := p.ListOlder(ctx, mail.Folder{RemoteID: f.RemoteID, Name: f.Name, Kind: f.Kind}, before, olderPageSize)
	if err != nil {
		return 0, err
	}
	n, err := s.engine.Ingest(f.AccountID, msgs)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		_ = s.store.SetOlderDone(f.AccountID, f.ID, true) // nothing older on the server
	}
	return n, nil
}

type remoteSearches struct {
	mu     sync.Mutex
	recent map[string]time.Time
	busy   map[string]bool
}

func toProviderQuery(ps store.ParsedSearch) mail.SearchQuery {
	q := mail.SearchQuery{From: ps.From, To: ps.To, Subject: ps.Subject, Unread: ps.Unread, Starred: ps.Starred,
		HasAttachment: ps.HasAttachment, Before: ps.Before, After: ps.After}
	for _, t := range ps.Terms {
		q.Terms = append(q.Terms, t.Text)
	}
	return q
}

// remoteSearch runs a throttled background server search per account.
func (s *Service) remoteSearch(raw, accountID string) {
	ps := store.ParseSearch(raw)
	text := 0
	for _, t := range ps.Terms {
		text += len([]rune(t.Text))
	}
	if text < 3 && len(ps.From)+len(ps.To)+len(ps.Subject) == 0 {
		return
	}
	pq := toProviderQuery(ps)
	norm := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	var accts []string
	if accountID != "" {
		accts = []string{accountID}
	} else if all, err := s.store.Accounts(); err == nil {
		for _, a := range all {
			if !a.NeedsReauth {
				accts = append(accts, a.ID)
			}
		}
	}
	if s.engine.Offline() {
		return
	}
	for _, a := range accts {
		key := a + "\x00" + norm
		rs := &s.searches
		rs.mu.Lock()
		if t, ok := rs.recent[key]; (ok && time.Since(t) < remoteSearchTTL) || rs.busy[a] {
			rs.mu.Unlock()
			continue
		}
		rs.recent[key], rs.busy[a] = time.Now(), true
		if len(rs.recent) > 500 {
			rs.recent = map[string]time.Time{key: time.Now()}
		}
		rs.mu.Unlock()
		acct := a
		s.goBG(func() {
			defer func() { rs.mu.Lock(); delete(rs.busy, acct); rs.mu.Unlock() }()
			p, err := s.provider(acct)
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			defer cancel()
			msgs, err := p.Search(ctx, pq, remoteSearchLimit)
			if err != nil {
				slog.Debug("server search failed", "account", acct, "err", err)
				return
			}
			n, err := s.engine.Ingest(acct, msgs)
			if err == nil && n > 0 {
				s.emit(models.EventSearchUpdated, models.SearchUpdated{Query: raw, AccountID: acct, Added: n})
			}
		})
	}
}

// ---- attachments ----

// maxPreviewBytes caps GetAttachmentData.
const maxPreviewBytes = 15 << 20

func (s *Service) attachmentBytes(messageID, attachmentID string) (models.Message, models.Attachment, []byte, error) {
	m, _, err := s.store.Message(plainID(messageID))
	if err != nil {
		return m, models.Attachment{}, nil, notFound(err)
	}
	att, err := s.store.Attachment(m.ID, attachmentID)
	if err != nil {
		return m, att, nil, errors.New("that attachment no longer exists")
	}
	if data, ok := s.attCache.get(m.AccountID, m.RemoteID, attachmentID); ok {
		return m, att, data, nil
	}
	p, err := s.provider(m.AccountID)
	if err != nil {
		return m, att, nil, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	data, err := p.Attachment(ctx, m.RemoteID, attachmentID)
	if err != nil {
		acct, _ := s.store.Account(m.AccountID)
		return m, att, nil, fmt.Errorf("downloading the attachment failed: %s", syncer.HumanError(acct, err))
	}
	s.attCache.put(m.AccountID, m.RemoteID, attachmentID, data)
	return m, att, data, nil
}

// AttachmentData returns attachment bytes for previews (max 15 MB).
func (s *Service) AttachmentData(messageID, attachmentID string) (models.AttachmentData, error) {
	m, _, err := s.store.Message(plainID(messageID))
	if err != nil {
		return models.AttachmentData{}, notFound(err)
	}
	if att, err := s.store.Attachment(m.ID, attachmentID); err == nil && att.Size > maxPreviewBytes {
		return models.AttachmentData{}, errors.New("this attachment is too large to preview; save it instead")
	}
	_, att, data, err := s.attachmentBytes(messageID, attachmentID)
	if err != nil {
		return models.AttachmentData{}, err
	}
	if len(data) > maxPreviewBytes {
		return models.AttachmentData{}, errors.New("this attachment is too large to preview; save it instead")
	}
	return models.AttachmentData{Filename: att.Filename, MimeType: att.MimeType, Size: int64(len(data)),
		DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

// SaveAttachment asks where to save an attachment and writes it.
func (s *Service) SaveAttachment(messageID, attachmentID string) error {
	m, _, err := s.store.Message(plainID(messageID))
	if err != nil {
		return notFound(err)
	}
	att, err := s.store.Attachment(m.ID, attachmentID)
	if err != nil {
		return errors.New("that attachment no longer exists")
	}
	path, err := s.ui.SaveFile(sanitizeFilename(att.Filename))
	if err != nil {
		return err
	}
	if path == "" {
		return nil // cancelled
	}
	_, _, data, err := s.attachmentBytes(messageID, attachmentID)
	if err != nil {
		return err
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
	// Windows: no trailing dots/spaces, no device names (CON, NUL.txt, ...).
	n = strings.TrimRight(n, ". ")
	if n == "" {
		return "attachment"
	}
	stem, _, _ := strings.Cut(n, ".")
	switch u := strings.ToUpper(strings.TrimSpace(stem)); {
	case u == "CON" || u == "PRN" || u == "AUX" || u == "NUL",
		len(u) == 4 && (strings.HasPrefix(u, "COM") || strings.HasPrefix(u, "LPT")) && u[3] >= '0' && u[3] <= '9':
		n = "_" + n
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
	case "http", "https":
		if u.Host == "" {
			return errors.New("invalid link")
		}
		return s.ui.OpenURL(u.String())
	case "mailto":
		return s.ui.OpenURL(u.String())
	}
	return errors.New("only web and mailto links can be opened")
}
