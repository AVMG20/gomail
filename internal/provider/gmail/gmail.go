// Package gmail is a lean Gmail REST v1 client implementing provider.Provider.
package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

// BaseURL of the Gmail API (overridable in tests).
const BaseURL = "https://gmail.googleapis.com"

// AllMail is the pseudo folder id for "All Mail" (Gmail has no label for
// it): every message that isn't in Spam or Trash.
const AllMail = "ALL_MAIL"

// Concurrency for metadata fetches.
const workers = 8

var systemFolders = []struct{ id, name, kind string }{
	{"INBOX", "Inbox", models.FolderInbox},
	{"STARRED", "Starred", models.FolderStarred},
	{"SENT", "Sent", models.FolderSent},
	{"DRAFT", "Drafts", models.FolderDrafts},
	{AllMail, "All Mail", models.FolderArchive},
	{"SPAM", "Spam", models.FolderSpam},
	{"TRASH", "Trash", models.FolderTrash},
}

var folderLabel = map[string]bool{"INBOX": true, "SENT": true, "DRAFT": true, "SPAM": true, "TRASH": true}

// Client talks to one Gmail account.
type Client struct {
	api  *httpx.Client
	base string

	mu       sync.Mutex
	labels   map[string]gLabel // id -> user label
	fwdCache map[string][]mailmime.Attachment
}

// New creates a client. base "" means the production API.
func New(tokens httpx.TokenSource, base string) *Client {
	if base == "" {
		base = BaseURL
	}
	return &Client{
		api:    &httpx.Client{BaseURL: base + "/gmail/v1/users/me", Tokens: tokens, Service: "Gmail"},
		base:   base,
		labels: map[string]gLabel{},
	}
}

// SetSleep replaces the backoff sleeper (tests).
func (c *Client) SetSleep(f func(context.Context, time.Duration) error) { c.api.Sleep = f }

type gLabel struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	MessagesTotal  int    `json:"messagesTotal"`
	MessagesUnread int    `json:"messagesUnread"`
	Color          *struct {
		BackgroundColor string `json:"backgroundColor"`
	} `json:"color"`
}

type gProfile struct {
	EmailAddress  string `json:"emailAddress"`
	MessagesTotal int    `json:"messagesTotal"`
	HistoryID     string `json:"historyId"`
}

func (c *Client) Profile(ctx context.Context) (provider.Profile, error) {
	var p gProfile
	if err := c.api.JSON(ctx, http.MethodGet, "profile", nil, &p); err != nil {
		return provider.Profile{}, err
	}
	return provider.Profile{Email: p.EmailAddress}, nil
}

func (c *Client) loadLabels(ctx context.Context) ([]gLabel, error) {
	var resp struct {
		Labels []gLabel `json:"labels"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, "labels", nil, &resp); err != nil {
		return nil, err
	}
	m := map[string]gLabel{}
	for _, l := range resp.Labels {
		if l.Type == "user" {
			m[l.ID] = l
		}
	}
	c.mu.Lock()
	c.labels = m
	c.mu.Unlock()
	return resp.Labels, nil
}

func (c *Client) Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error) {
	all, err := c.loadLabels(ctx)
	if err != nil {
		return nil, nil, err
	}
	present := map[string]bool{}
	for _, l := range all {
		present[l.ID] = true
	}
	folders := make([]mail.Folder, len(systemFolders))
	err = provider.Parallel(ctx, workers, indexes(len(systemFolders)), func(ctx context.Context, i int) error {
		sf := systemFolders[i]
		f := mail.Folder{RemoteID: sf.id, Name: sf.name, Kind: sf.kind}
		switch {
		case sf.id == AllMail:
			var p gProfile
			if err := c.api.JSON(ctx, http.MethodGet, "profile", nil, &p); err != nil {
				return err
			}
			f.Total = p.MessagesTotal
		case present[sf.id]:
			var l gLabel
			if err := c.api.JSON(ctx, http.MethodGet, "labels/"+sf.id, nil, &l); err != nil {
				return err
			}
			f.Total, f.Unread = l.MessagesTotal, l.MessagesUnread
		}
		folders[i] = f
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var labels []mail.Label
	for _, l := range all {
		if l.Type != "user" {
			continue
		}
		ml := mail.Label{RemoteID: l.ID, Name: l.Name}
		if l.Color != nil {
			ml.Color = l.Color.BackgroundColor
		}
		labels = append(labels, ml)
	}
	sort.Slice(labels, func(i, j int) bool { return strings.ToLower(labels[i].Name) < strings.ToLower(labels[j].Name) })
	return folders, labels, nil
}

func indexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// ---- messages ----

type gHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gPart struct {
	PartID   string    `json:"partId"`
	MimeType string    `json:"mimeType"`
	Filename string    `json:"filename"`
	Headers  []gHeader `json:"headers"`
	Body     struct {
		AttachmentID string `json:"attachmentId"`
		Size         int64  `json:"size"`
		Data         string `json:"data"`
	} `json:"body"`
	Parts []gPart `json:"parts"`
}

func (p *gPart) header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

type gMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	InternalDate string   `json:"internalDate"`
	Payload      gPart    `json:"payload"`
}

const metadataQuery = "format=metadata&metadataHeaders=From&metadataHeaders=To&metadataHeaders=Cc&metadataHeaders=Bcc" +
	"&metadataHeaders=Reply-To&metadataHeaders=Subject&metadataHeaders=Date&metadataHeaders=Message-ID" +
	"&metadataHeaders=In-Reply-To&metadataHeaders=References&metadataHeaders=Content-Type" +
	"&metadataHeaders=List-Unsubscribe&metadataHeaders=List-Unsubscribe-Post"

func (c *Client) toMessage(g gMessage) mail.Message {
	h := &g.Payload
	m := mail.Message{
		RemoteID:    g.ID,
		ThreadID:    g.ThreadID,
		From:        mailmime.ParseAddress(h.header("From")),
		To:          mailmime.ParseAddressList(h.header("To")),
		Cc:          mailmime.ParseAddressList(h.header("Cc")),
		Bcc:         mailmime.ParseAddressList(h.header("Bcc")),
		ReplyTo:     mailmime.ParseAddressList(h.header("Reply-To")),
		Subject:     mailmime.DecodeHeader(h.header("Subject")),
		Snippet:     htmlUnescape(g.Snippet),
		MessageID:   strings.TrimSpace(h.header("Message-ID")),
		InReplyTo:   strings.TrimSpace(h.header("In-Reply-To")),
		References:  strings.Join(strings.Fields(h.header("References")), " "),
		Unsubscribe: mailmime.ParseListUnsubscribe(h.header("List-Unsubscribe"), h.header("List-Unsubscribe-Post")),
	}
	if ms, err := strconv.ParseInt(g.InternalDate, 10, 64); err == nil && ms > 0 {
		m.Date = time.UnixMilli(ms).UTC()
	} else {
		m.Date = mailmime.ParseDate(h.header("Date"))
	}
	ct := strings.ToLower(h.header("Content-Type"))
	if ct == "" {
		ct = strings.ToLower(h.MimeType)
	}
	m.HasAttachments = strings.HasPrefix(ct, "multipart/mixed") || strings.HasPrefix(ct, "application/")
	inTrashOrSpam := false
	c.mu.Lock()
	for _, l := range g.LabelIDs {
		switch {
		case l == "UNREAD":
			m.Unread = true
		case l == "STARRED":
			m.Starred = true
		case folderLabel[l]:
			m.FolderRemoteIDs = append(m.FolderRemoteIDs, l)
			if l == "SPAM" || l == "TRASH" {
				inTrashOrSpam = true
			}
		default:
			if ul, ok := c.labels[l]; ok {
				m.Labels = append(m.Labels, ul.Name)
			}
		}
	}
	c.mu.Unlock()
	if !inTrashOrSpam {
		m.FolderRemoteIDs = append(m.FolderRemoteIDs, AllMail)
	}
	return m
}

var snippetReplacer = strings.NewReplacer("&#39;", "'", "&quot;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ")

func htmlUnescape(s string) string { return snippetReplacer.Replace(s) }

// listIDs lists up to limit message ids for a label (or query).
func (c *Client) listIDs(ctx context.Context, labelID, q string, limit int) ([]string, error) {
	var ids []string
	page := ""
	for len(ids) < limit {
		v := url.Values{}
		v.Set("maxResults", strconv.Itoa(min(500, limit-len(ids))))
		if labelID != "" {
			v.Set("labelIds", labelID)
		}
		if q != "" {
			v.Set("q", q)
		}
		if page != "" {
			v.Set("pageToken", page)
		}
		var resp struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, "messages?"+v.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.ID)
		}
		if resp.NextPageToken == "" {
			break
		}
		page = resp.NextPageToken
	}
	return ids, nil
}

// fetchMetadata fetches message metadata with a bounded worker pool. Ids
// that no longer exist are returned in gone.
func (c *Client) fetchMetadata(ctx context.Context, ids []string, progress ...func(done, total int)) (msgs []mail.Message, gone []string, err error) {
	var mu sync.Mutex
	done := 0
	err = provider.Parallel(ctx, workers, ids, func(ctx context.Context, id string) error {
		var g gMessage
		err := c.api.JSON(ctx, http.MethodGet, "messages/"+url.PathEscape(id)+"?"+metadataQuery, nil, &g)
		mu.Lock()
		defer mu.Unlock()
		done++
		for _, p := range progress {
			p(done, len(ids))
		}
		if httpx.StatusOf(err) == http.StatusNotFound {
			gone = append(gone, id)
			return nil
		}
		if err != nil {
			return err
		}
		msgs = append(msgs, c.toMessage(g))
		return nil
	})
	return msgs, gone, err
}

func (c *Client) Sync(ctx context.Context, req provider.SyncRequest) (mail.SyncResult, error) {
	c.mu.Lock()
	haveLabels := len(c.labels) > 0
	c.mu.Unlock()
	if !haveLabels {
		if _, err := c.loadLabels(ctx); err != nil {
			return mail.SyncResult{}, err
		}
	}
	if hid := req.State["historyId"]; hid != "" {
		res, err := c.incremental(ctx, hid)
		if httpx.StatusOf(err) != http.StatusNotFound {
			return res, err
		}
		// History too old: fall back to a full sync.
	}
	return c.initial(ctx, req)
}

func (c *Client) initial(ctx context.Context, req provider.SyncRequest) (mail.SyncResult, error) {
	lim := req.Limits.WithDefaults()
	var p gProfile
	if err := c.api.JSON(ctx, http.MethodGet, "profile", nil, &p); err != nil {
		return mail.SyncResult{}, err
	}
	type spec struct {
		label, q string
		n        int
	}
	specs := []spec{
		{"INBOX", "", lim.Inbox}, {"SENT", "", lim.Other}, {"DRAFT", "", lim.Other / 2},
		{"STARRED", "", lim.Other}, {"", "-in:inbox -in:spam -in:trash", lim.Other},
		{"SPAM", "", lim.Other / 2}, {"TRASH", "", lim.Other / 2},
	}
	seen := map[string]bool{}
	var ids []string
	listed := make([][]string, len(specs))
	for i, s := range specs {
		req.Report("listing", i, len(specs))
		list, err := c.listIDs(ctx, s.label, s.q, max(1, s.n))
		if err != nil {
			return mail.SyncResult{}, err
		}
		listed[i] = list
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	msgs, _, err := c.fetchMetadata(ctx, ids, func(done, total int) { req.Report("fetching", done, total) })
	if err != nil {
		return mail.SyncResult{}, err
	}
	// Each listing is newest first, so a folder is complete from the
	// oldest listed message on (or entirely when the listing wasn't cut).
	dates := make(map[string]time.Time, len(msgs))
	for _, m := range msgs {
		dates[m.RemoteID] = m.Date
	}
	since := func(i int) (time.Time, bool) {
		if len(listed[i]) < max(1, specs[i].n) {
			return time.Time{}, true
		}
		var oldest time.Time
		for _, id := range listed[i] {
			if d, ok := dates[id]; ok && (oldest.IsZero() || d.Before(oldest)) {
				oldest = d
			}
		}
		return oldest, !oldest.IsZero()
	}
	complete := map[string]time.Time{}
	var inbox, archived time.Time
	inboxOK, archivedOK := false, false
	for i, s := range specs {
		t, ok := since(i)
		switch {
		case !ok:
		case s.label == "STARRED":
		case s.label == "":
			archived, archivedOK = t, true
		default:
			complete[s.label] = t
			if s.label == "INBOX" {
				inbox, inboxOK = t, true
			}
		}
	}
	if inboxOK && archivedOK {
		// All Mail is Inbox plus everything archived: complete from the
		// later of the two cut-off dates.
		if inbox.After(archived) {
			complete[AllMail] = inbox
		} else {
			complete[AllMail] = archived
		}
	}
	return mail.SyncResult{Upserts: msgs, State: map[string]string{"historyId": p.HistoryID}, Initial: true, Complete: complete}, nil
}

type gHistory struct {
	History []struct {
		Messages      []struct{ ID string } `json:"messages"`
		MessagesAdded []struct {
			Message struct{ ID string } `json:"message"`
		} `json:"messagesAdded"`
		MessagesDeleted []struct {
			Message struct{ ID string } `json:"message"`
		} `json:"messagesDeleted"`
		LabelsAdded []struct {
			Message struct{ ID string } `json:"message"`
		} `json:"labelsAdded"`
		LabelsRemoved []struct {
			Message struct{ ID string } `json:"message"`
		} `json:"labelsRemoved"`
	} `json:"history"`
	NextPageToken string `json:"nextPageToken"`
	HistoryID     string `json:"historyId"`
}

func (c *Client) incremental(ctx context.Context, startHistoryID string) (mail.SyncResult, error) {
	changed := map[string]bool{}
	deleted := map[string]bool{}
	page := ""
	newHistory := startHistoryID
	for {
		v := url.Values{}
		v.Set("startHistoryId", startHistoryID)
		v.Set("maxResults", "500")
		if page != "" {
			v.Set("pageToken", page)
		}
		var h gHistory
		if err := c.api.JSON(ctx, http.MethodGet, "history?"+v.Encode(), nil, &h); err != nil {
			return mail.SyncResult{}, err
		}
		for _, rec := range h.History {
			for _, m := range rec.MessagesAdded {
				changed[m.Message.ID] = true
			}
			for _, m := range rec.LabelsAdded {
				changed[m.Message.ID] = true
			}
			for _, m := range rec.LabelsRemoved {
				changed[m.Message.ID] = true
			}
			for _, m := range rec.MessagesDeleted {
				deleted[m.Message.ID] = true
			}
		}
		if h.HistoryID != "" {
			newHistory = h.HistoryID
		}
		if h.NextPageToken == "" {
			break
		}
		page = h.NextPageToken
	}
	var ids []string
	for id := range changed {
		if !deleted[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	msgs, gone, err := c.fetchMetadata(ctx, ids)
	if err != nil {
		return mail.SyncResult{}, err
	}
	res := mail.SyncResult{Upserts: msgs, State: map[string]string{"historyId": newHistory}}
	for id := range deleted {
		res.Deleted = append(res.Deleted, id)
	}
	res.Deleted = append(res.Deleted, gone...)
	sort.Strings(res.Deleted)
	return res, nil
}

// ---- bodies ----

func decodeB64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

func (c *Client) full(ctx context.Context, remoteID string) (*gMessage, error) {
	var g gMessage
	if err := c.api.JSON(ctx, http.MethodGet, "messages/"+url.PathEscape(remoteID)+"?format=full", nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (c *Client) attachmentData(ctx context.Context, remoteID, attachmentID string) ([]byte, error) {
	var a struct {
		Data string `json:"data"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, "messages/"+url.PathEscape(remoteID)+"/attachments/"+url.PathEscape(attachmentID), nil, &a); err != nil {
		return nil, err
	}
	return decodeB64URL(a.Data)
}

const maxInlineBytes = 15 << 20

// maxExternalBody caps text parts Gmail serves separately (like attachments).
const maxExternalBody = 20 << 20

func (c *Client) Body(ctx context.Context, remoteID string) (*mail.Body, error) {
	g, err := c.full(ctx, remoteID)
	if err != nil {
		return nil, err
	}
	b := &mail.Body{
		MessageID:   strings.TrimSpace(g.Payload.header("Message-ID")),
		InReplyTo:   strings.TrimSpace(g.Payload.header("In-Reply-To")),
		References:  strings.Join(strings.Fields(g.Payload.header("References")), " "),
		Cc:          mailmime.ParseAddressList(g.Payload.header("Cc")),
		Bcc:         mailmime.ParseAddressList(g.Payload.header("Bcc")),
		ReplyTo:     mailmime.ParseAddressList(g.Payload.header("Reply-To")),
		Unsubscribe: mailmime.ParseListUnsubscribe(g.Payload.header("List-Unsubscribe"), g.Payload.header("List-Unsubscribe-Post")),
	}
	var walk func(p *gPart, depth int)
	walk = func(p *gPart, depth int) {
		if depth > 20 {
			return
		}
		if strings.HasPrefix(p.MimeType, "multipart/") {
			for i := range p.Parts {
				walk(&p.Parts[i], depth+1)
			}
			return
		}
		disp := strings.ToLower(p.header("Content-Disposition"))
		cid := strings.Trim(strings.TrimSpace(p.header("Content-ID")), "<>")
		if strings.EqualFold(p.MimeType, "text/calendar") && b.Calendar == "" && p.Body.Size < 1<<20 {
			var data []byte
			if p.Body.Data != "" {
				data, _ = decodeB64URL(p.Body.Data)
			} else if p.Body.AttachmentID != "" {
				data, _ = c.attachmentData(ctx, remoteID, p.Body.AttachmentID)
			}
			b.Calendar = mailmime.DecodeCharset(data, charsetOf(p.header("Content-Type")))
			if p.Filename == "" {
				return // the invitation itself, not a file the user attached
			}
		}
		isBody := p.Filename == "" && !strings.HasPrefix(disp, "attachment") && (p.MimeType == "text/html" || p.MimeType == "text/plain")
		if isBody {
			data, _ := decodeB64URL(p.Body.Data)
			if p.Body.Data == "" && p.Body.AttachmentID != "" && p.Body.Size < maxExternalBody {
				// Large bodies are served like attachments.
				data, _ = c.attachmentData(ctx, remoteID, p.Body.AttachmentID)
			}
			text := mailmime.DecodeCharset(data, charsetOf(p.header("Content-Type")))
			if p.MimeType == "text/html" {
				if b.HTML != "" {
					b.HTML += "<hr>"
				}
				b.HTML += text
			} else {
				if b.Text != "" {
					b.Text += "\n\n"
				}
				b.Text += text
			}
			return
		}
		a := mail.Attachment{Attachment: models.Attachment{
			ID:        p.PartID,
			Filename:  mailmime.DecodeHeader(p.Filename),
			MimeType:  p.MimeType,
			Size:      p.Body.Size,
			Inline:    strings.HasPrefix(disp, "inline") || (disp == "" && cid != ""),
			ContentID: cid,
		}}
		if a.Filename == "" {
			a.Filename = "attachment"
		}
		if a.ContentID != "" && strings.HasPrefix(a.MimeType, "image/") && a.Size < maxInlineBytes {
			if p.Body.Data != "" {
				a.Data, _ = decodeB64URL(p.Body.Data)
			} else if p.Body.AttachmentID != "" {
				a.Data, _ = c.attachmentData(ctx, remoteID, p.Body.AttachmentID)
			}
		}
		b.Attachments = append(b.Attachments, a)
	}
	walk(&g.Payload, 0)
	return b, nil
}

func charsetOf(ct string) string {
	for _, p := range strings.Split(ct, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && strings.EqualFold(k, "charset") {
			return strings.Trim(v, `"' `)
		}
	}
	return ""
}

// Attachment downloads by part id. Gmail attachment ids are not stable
// between fetches, so the current one is looked up first.
func (c *Client) Attachment(ctx context.Context, remoteID, partID string) ([]byte, error) {
	g, err := c.full(ctx, remoteID)
	if err != nil {
		return nil, err
	}
	var found *gPart
	var walk func(p *gPart)
	walk = func(p *gPart) {
		if p.PartID == partID {
			found = p
		}
		for i := range p.Parts {
			walk(&p.Parts[i])
		}
	}
	walk(&g.Payload)
	if found == nil {
		return nil, errors.New("attachment not found (the message may have changed)")
	}
	if found.Body.Data != "" {
		return decodeB64URL(found.Body.Data)
	}
	return c.attachmentData(ctx, remoteID, found.Body.AttachmentID)
}

// ---- actions ----

func (c *Client) batchModify(ctx context.Context, ids []string, add, remove []string) error {
	for i, chunk := range provider.Chunk(ids, 1000) {
		body := map[string]any{"ids": chunk}
		if len(add) > 0 {
			body["addLabelIds"] = add
		}
		if len(remove) > 0 {
			body["removeLabelIds"] = remove
		}
		if err := c.api.JSONRetry(ctx, http.MethodPost, "messages/batchModify", body, nil); err != nil {
			// Earlier chunks were applied.
			return provider.Partial(err, ids[i*1000:], len(ids))
		}
	}
	return nil
}

func (c *Client) SetRead(ctx context.Context, ids []string, read bool) error {
	if read {
		return c.batchModify(ctx, ids, nil, []string{"UNREAD"})
	}
	return c.batchModify(ctx, ids, []string{"UNREAD"}, nil)
}

func (c *Client) SetStarred(ctx context.Context, ids []string, starred bool) error {
	if starred {
		return c.batchModify(ctx, ids, []string{"STARRED"}, nil)
	}
	return c.batchModify(ctx, ids, nil, []string{"STARRED"})
}

func (c *Client) Move(ctx context.Context, ids []string, target mail.Folder) error {
	switch target.Kind {
	case models.FolderTrash:
		var mu sync.Mutex
		done := map[string]bool{}
		err := provider.Parallel(ctx, workers, ids, func(ctx context.Context, id string) error {
			if err := c.api.JSONRetry(ctx, http.MethodPost, "messages/"+url.PathEscape(id)+"/trash", nil, nil); err != nil {
				return err
			}
			mu.Lock()
			done[id] = true
			mu.Unlock()
			return nil
		})
		var failed []string
		for _, id := range ids {
			if !done[id] {
				failed = append(failed, id)
			}
		}
		return provider.Partial(err, failed, len(ids))
	case models.FolderSpam:
		return c.batchModify(ctx, ids, []string{"SPAM"}, []string{"INBOX", "TRASH"})
	case models.FolderInbox:
		return c.batchModify(ctx, ids, []string{"INBOX"}, []string{"SPAM", "TRASH"})
	case models.FolderArchive:
		return c.batchModify(ctx, ids, nil, []string{"INBOX", "SPAM", "TRASH"})
	case models.FolderStarred:
		return c.SetStarred(ctx, ids, true)
	}
	return fmt.Errorf("messages can't be moved to %s in Gmail: %w", target.Name, provider.ErrUnsupported)
}

func (c *Client) PlanMove(current []string, target mail.Folder) []string {
	out := []string{}
	for _, f := range current {
		switch f {
		case "INBOX", "SPAM", "TRASH", AllMail:
		default:
			out = append(out, f)
		}
	}
	switch target.Kind {
	case models.FolderInbox:
		out = append(out, "INBOX", AllMail)
	case models.FolderSpam:
		out = append(out, "SPAM")
	case models.FolderTrash:
		out = append(out, "TRASH")
	case models.FolderArchive:
		out = append(out, AllMail)
	default:
		return current
	}
	return out
}

func (c *Client) labelID(ctx context.Context, name string, create bool) (string, error) {
	find := func() string {
		c.mu.Lock()
		defer c.mu.Unlock()
		for id, l := range c.labels {
			if strings.EqualFold(l.Name, name) {
				return id
			}
		}
		return ""
	}
	if id := find(); id != "" {
		return id, nil
	}
	if _, err := c.loadLabels(ctx); err != nil {
		return "", err
	}
	if id := find(); id != "" || !create {
		return id, nil
	}
	var l gLabel
	if err := c.api.JSON(ctx, http.MethodPost, "labels", map[string]string{"name": name,
		"labelListVisibility": "labelShow", "messageListVisibility": "show"}, &l); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.labels[l.ID] = gLabel{ID: l.ID, Name: name, Type: "user"}
	c.mu.Unlock()
	return l.ID, nil
}

func (c *Client) SetLabel(ctx context.Context, ids []string, name string, add bool) error {
	id, err := c.labelID(ctx, name, add)
	if err != nil || id == "" {
		return err
	}
	if add {
		return c.batchModify(ctx, ids, []string{id}, nil)
	}
	return c.batchModify(ctx, ids, nil, []string{id})
}

// ---- send & drafts ----

// forwardAttachments loads (and briefly caches) the original attachments
// for forwards; draft autosaves would otherwise download them each time.
func (c *Client) forwardAttachments(ctx context.Context, remoteID string) ([]mailmime.Attachment, error) {
	c.mu.Lock()
	if c.fwdCache == nil {
		c.fwdCache = map[string][]mailmime.Attachment{}
	}
	if atts, ok := c.fwdCache[remoteID]; ok {
		c.mu.Unlock()
		return atts, nil
	}
	c.mu.Unlock()
	body, err := c.Body(ctx, remoteID)
	if err != nil {
		return nil, fmt.Errorf("could not load the original attachments: %w", err)
	}
	var atts []mailmime.Attachment
	for _, a := range body.Attachments {
		// Only parts the HTML embeds by Content-ID stay behind; files sent
		// with "Content-Disposition: inline" (Apple Mail) are real
		// attachments and must be forwarded.
		if a.Inline && a.ContentID != "" {
			continue
		}
		data, err := c.Attachment(ctx, remoteID, a.ID)
		if err != nil {
			return nil, fmt.Errorf("could not load attachment %s: %w", a.Filename, err)
		}
		atts = append(atts, mailmime.Attachment{Filename: a.Filename, MimeType: a.MimeType, Data: data})
	}
	c.mu.Lock()
	if len(c.fwdCache) > 4 {
		c.fwdCache = map[string][]mailmime.Attachment{}
	}
	c.fwdCache[remoteID] = atts
	c.mu.Unlock()
	return atts, nil
}

// build renders out as raw MIME and returns the Gmail thread id.
func (c *Client) build(ctx context.Context, out mail.Outgoing) ([]byte, string, error) {
	m := mailmime.Message{
		From: out.From, To: out.To, Cc: out.Cc, Bcc: out.Bcc, IncludeBcc: true,
		Subject: out.Subject, Text: out.Text, HTML: out.HTML,
	}
	for _, a := range out.Attachments {
		m.Attachments = append(m.Attachments, mailmime.Attachment{Filename: a.Filename, MimeType: a.MimeType, Data: a.Data})
	}
	threadID := ""
	if o := out.Original; o != nil {
		switch out.Mode {
		case "reply", "replyAll":
			m.InReplyTo = o.MessageID
			refs := o.References
			if refs == "" {
				// RFC 5322 3.6.4: without References, the parent's
				// In-Reply-To names its own parent.
				refs = mailmime.MessageIDs(o.InReplyTo)
			}
			m.References = mailmime.ReferencesFor(refs, o.MessageID)
			threadID = o.ThreadID
		case "forward":
			atts, err := c.forwardAttachments(ctx, o.RemoteID)
			if err != nil {
				return nil, "", err
			}
			m.Attachments = append(m.Attachments, atts...)
		}
	}
	raw, err := mailmime.Build(m)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 35<<20 {
		return nil, "", errors.New("the message is larger than Gmail's 35 MB limit")
	}
	return raw, threadID, nil
}

// upload posts a message resource ({raw, threadId}, optionally wrapped in a
// draft) to a Gmail endpoint, using the multipart upload API for large ones.
func (c *Client) upload(ctx context.Context, method, path string, raw []byte, threadID, draftID string, out any) error {
	msg := map[string]string{}
	if threadID != "" {
		msg["threadId"] = threadID
	}
	isDraft := strings.HasPrefix(path, "drafts")
	if len(raw) < 4<<20 {
		msg["raw"] = base64.RawURLEncoding.EncodeToString(raw)
		var body any = msg
		if isDraft {
			d := map[string]any{"message": msg}
			if draftID != "" {
				d["id"] = draftID
			}
			body = d
		}
		return c.api.JSON(ctx, method, path, body, out)
	}
	var metaObj any = msg
	if isDraft {
		d := map[string]any{"message": msg}
		if draftID != "" {
			d["id"] = draftID
		}
		metaObj = d
	}
	meta, _ := json.Marshal(metaObj)
	boundary := "gomail-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var buf strings.Builder
	buf.WriteString("--" + boundary + "\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n")
	buf.Write(meta)
	buf.WriteString("\r\n--" + boundary + "\r\nContent-Type: message/rfc822\r\n\r\n")
	buf.Write(raw)
	buf.WriteString("\r\n--" + boundary + "--")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	resp, err := c.api.Do(ctx, httpx.Request{
		Method: method, Path: c.base + "/upload/gmail/v1/users/me/" + path + sep + "uploadType=multipart",
		Body: []byte(buf.String()), ContentType: "multipart/related; boundary=" + boundary,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Send(ctx context.Context, out mail.Outgoing) error {
	raw, threadID, err := c.build(ctx, out)
	if err != nil {
		return err
	}
	return c.upload(ctx, http.MethodPost, "messages/send", raw, threadID, "", nil)
}

func (c *Client) SaveDraft(ctx context.Context, out mail.Outgoing, draftID string) (string, error) {
	raw, threadID, err := c.build(ctx, out)
	if err != nil {
		return "", err
	}
	var d struct {
		ID string `json:"id"`
	}
	if draftID != "" {
		err = c.upload(ctx, http.MethodPut, "drafts/"+url.PathEscape(draftID), raw, threadID, draftID, &d)
		if httpx.StatusOf(err) != http.StatusNotFound {
			if err != nil {
				return "", err
			}
			return d.ID, nil
		}
		// The draft was sent or deleted elsewhere: create a new one.
	}
	if err := c.upload(ctx, http.MethodPost, "drafts", raw, threadID, "", &d); err != nil {
		return "", err
	}
	return d.ID, nil
}

func (c *Client) SendDraft(ctx context.Context, out mail.Outgoing, draftID string) error {
	id, err := c.SaveDraft(ctx, out, draftID)
	if err != nil {
		return err
	}
	return c.api.JSON(ctx, http.MethodPost, "drafts/send", map[string]string{"id": id}, nil)
}

func (c *Client) DeleteDraft(ctx context.Context, draftID string) error {
	err := c.api.JSON(ctx, http.MethodDelete, "drafts/"+url.PathEscape(draftID), nil, nil)
	if httpx.StatusOf(err) == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *Client) DraftID(ctx context.Context, messageRemoteID string) (string, error) {
	page := ""
	for i := 0; i < 20; i++ {
		v := url.Values{"maxResults": {"500"}}
		if page != "" {
			v.Set("pageToken", page)
		}
		var resp struct {
			Drafts []struct {
				ID      string `json:"id"`
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"drafts"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, "drafts?"+v.Encode(), nil, &resp); err != nil {
			return "", err
		}
		for _, d := range resp.Drafts {
			if d.Message.ID == messageRemoteID {
				return d.ID, nil
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		page = resp.NextPageToken
	}
	return "", provider.ErrDraftGone
}

// ---- older mail & search ----

func (c *Client) ListOlder(ctx context.Context, folder mail.Folder, before time.Time, limit int) ([]mail.Message, error) {
	label, q := folder.RemoteID, ""
	if folder.RemoteID == AllMail {
		// Inbox mail is part of All Mail (and of its cursor).
		label, q = "", "-in:spam -in:trash"
	}
	if !before.IsZero() {
		q = strings.TrimSpace(q + " before:" + strconv.FormatInt(before.Unix(), 10))
	}
	ids, err := c.listIDs(ctx, label, q, limit)
	if err != nil {
		return nil, err
	}
	msgs, _, err := c.fetchMetadata(ctx, ids)
	return msgs, err
}

func gmailQuote(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	if strings.ContainsAny(s, " \t()") {
		return `"` + s + `"`
	}
	return s
}

// SearchString renders a query in Gmail search syntax.
func SearchString(q mail.SearchQuery) string {
	var parts []string
	for _, t := range q.Terms {
		parts = append(parts, gmailQuote(t))
	}
	for _, v := range q.From {
		parts = append(parts, "from:"+gmailQuote(v))
	}
	for _, v := range q.To {
		parts = append(parts, "to:"+gmailQuote(v))
	}
	for _, v := range q.Subject {
		parts = append(parts, "subject:"+gmailQuote(v))
	}
	if q.Unread != nil {
		parts = append(parts, map[bool]string{true: "is:unread", false: "is:read"}[*q.Unread])
	}
	if q.Starred != nil && *q.Starred {
		parts = append(parts, "is:starred")
	}
	if q.HasAttachment {
		parts = append(parts, "has:attachment")
	}
	if !q.After.IsZero() {
		parts = append(parts, "after:"+strconv.FormatInt(q.After.Unix(), 10))
	}
	if !q.Before.IsZero() {
		parts = append(parts, "before:"+strconv.FormatInt(q.Before.Unix(), 10))
	}
	return strings.Join(parts, " ")
}

func (c *Client) Search(ctx context.Context, q mail.SearchQuery, limit int) ([]mail.Message, error) {
	qs := SearchString(q)
	if qs == "" {
		return nil, nil
	}
	ids, err := c.listIDs(ctx, "", qs, limit)
	if err != nil {
		return nil, err
	}
	msgs, _, err := c.fetchMetadata(ctx, ids)
	return msgs, err
}

// ---- folder-wide operations ----

func folderQuery(folder mail.Folder) (label, q string) {
	if folder.RemoteID == AllMail {
		return "", "-in:spam -in:trash"
	}
	return folder.RemoteID, ""
}

func (c *Client) MarkFolderRead(ctx context.Context, folder mail.Folder) error {
	label, q := folderQuery(folder)
	ids, err := c.listIDs(ctx, label, strings.TrimSpace(q+" is:unread"), provider.MaxFolderOp)
	if err != nil {
		return err
	}
	return c.SetRead(ctx, ids, true)
}

// ErrGmailTrash explains why Gmail's Trash can't be emptied.
var ErrGmailTrash = errors.New("Gmail deletes messages in Trash automatically after 30 days; permanently deleting them needs full mailbox access, which gomail doesn't request")

func (c *Client) EmptyFolder(ctx context.Context, folder mail.Folder) error {
	switch folder.Kind {
	case models.FolderSpam:
		ids, err := c.listIDs(ctx, "SPAM", "", provider.MaxFolderOp)
		if err != nil {
			return err
		}
		return c.Move(ctx, ids, mail.Folder{Kind: models.FolderTrash})
	case models.FolderTrash:
		return ErrGmailTrash
	}
	return errors.New("only Trash and Spam can be emptied")
}

var _ provider.Provider = (*Client)(nil)
