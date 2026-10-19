// Package outlook is a lean Microsoft Graph v1.0 mail client implementing
// provider.Provider for Microsoft 365 and outlook.com accounts.
package outlook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// BaseURL of Microsoft Graph.
const BaseURL = "https://graph.microsoft.com/v1.0"

// Flagged is the virtual folder for flagged ("starred") messages.
const Flagged = "__flagged"

const (
	batchSize = 20 // Graph JSON batching limit
	workers   = 4
)

var wellKnown = []struct{ name, kind string }{
	{"inbox", models.FolderInbox}, {"sentitems", models.FolderSent}, {"drafts", models.FolderDrafts},
	{"archive", models.FolderArchive}, {"junkemail", models.FolderSpam}, {"deleteditems", models.FolderTrash},
	// Hidden from the sidebar:
	{"outbox", ""}, {"conversationhistory", ""}, {"syncissues", ""},
}

// Client talks to one Microsoft mailbox.
type Client struct {
	api  *httpx.Client
	base string

	mu        sync.Mutex
	wellKnown map[string]string // folder id -> kind ("" = hidden)
	byKind    map[string]string // kind -> folder id
}

// New creates a client. base "" means production Graph.
func New(tokens httpx.TokenSource, base string) *Client {
	if base == "" {
		base = BaseURL
	}
	return &Client{
		api: &httpx.Client{BaseURL: base, Tokens: tokens, Service: "Microsoft Graph",
			Headers: map[string]string{"Prefer": `IdType="ImmutableId"`}},
		base: base,
	}
}

// SetSleep replaces the backoff sleeper (tests).
func (c *Client) SetSleep(f func(context.Context, time.Duration) error) { c.api.Sleep = f }

// ---- JSON batching ----

type batchReq struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Body    any               `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type batchResp struct {
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

// batch executes requests via /$batch (20 per call), retrying throttled
// sub-requests. It returns responses by request id.
func (c *Client) batch(ctx context.Context, reqs []batchReq) (map[string]batchResp, error) {
	out := map[string]batchResp{}
	pending := reqs
	for attempt := 0; len(pending) > 0; attempt++ {
		var retry []batchReq
		var wait time.Duration
		for _, chunk := range provider.Chunk(pending, batchSize) {
			for i := range chunk {
				if chunk[i].Body != nil {
					if chunk[i].Headers == nil {
						chunk[i].Headers = map[string]string{}
					}
					chunk[i].Headers["Content-Type"] = "application/json"
				}
			}
			var resp struct {
				Responses []batchResp `json:"responses"`
			}
			if err := c.api.JSON(ctx, http.MethodPost, "$batch", map[string]any{"requests": chunk}, &resp); err != nil {
				return nil, err
			}
			byID := map[string]batchReq{}
			for _, r := range chunk {
				byID[r.ID] = r
			}
			for _, r := range resp.Responses {
				if (r.Status == 429 || r.Status == 503 || r.Status == 504) && attempt < 5 {
					retry = append(retry, byID[r.ID])
					if s, err := strconv.Atoi(r.Headers["Retry-After"]); err == nil {
						wait = max(wait, time.Duration(s)*time.Second)
					}
					continue
				}
				out[r.ID] = r
			}
		}
		pending = retry
		if len(pending) > 0 {
			if wait == 0 {
				wait = time.Duration(1<<attempt) * time.Second
			}
			sleep := c.api.Sleep
			if sleep == nil {
				sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(d):
						return nil
					}
				}
			}
			if err := sleep(ctx, min(wait, time.Minute)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func batchError(r batchResp) error {
	if r.Status >= 200 && r.Status < 300 {
		return nil
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &env)
	return &httpx.Error{Service: "Microsoft Graph", Status: r.Status, Code: env.Error.Code, Message: env.Error.Message}
}

// batchEach runs one request per id and returns the first error.
func (c *Client) batchEach(ctx context.Context, ids []string, mk func(id string) batchReq) error {
	reqs := make([]batchReq, len(ids))
	for i, id := range ids {
		r := mk(id)
		r.ID = strconv.Itoa(i)
		reqs[i] = r
	}
	resps, err := c.batch(ctx, reqs)
	if err != nil {
		return err
	}
	for i := range ids {
		r, ok := resps[strconv.Itoa(i)]
		if !ok {
			return errors.New("Microsoft Graph returned an incomplete batch response")
		}
		if err := batchError(r); err != nil && r.Status != http.StatusNotFound {
			return err
		}
	}
	return nil
}

// ---- profile & folders ----

func (c *Client) Profile(ctx context.Context) (provider.Profile, error) {
	var me struct {
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, "me?$select=displayName,mail,userPrincipalName", nil, &me); err != nil {
		return provider.Profile{}, err
	}
	email := me.Mail
	if email == "" {
		email = me.UserPrincipalName
	}
	return provider.Profile{Email: email, Name: me.DisplayName}, nil
}

type gFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ParentFolderID   string `json:"parentFolderId"`
	ChildFolderCount int    `json:"childFolderCount"`
	TotalItemCount   int    `json:"totalItemCount"`
	UnreadItemCount  int    `json:"unreadItemCount"`
}

const folderSelect = "$select=id,displayName,parentFolderId,childFolderCount,totalItemCount,unreadItemCount&$top=250"

func (c *Client) loadWellKnown(ctx context.Context) error {
	c.mu.Lock()
	done := c.wellKnown != nil
	c.mu.Unlock()
	if done {
		return nil
	}
	reqs := make([]batchReq, len(wellKnown))
	for i, w := range wellKnown {
		reqs[i] = batchReq{ID: w.name, Method: http.MethodGet, URL: "/me/mailFolders/" + w.name + "?$select=id"}
	}
	resps, err := c.batch(ctx, reqs)
	if err != nil {
		return err
	}
	wk, byKind := map[string]string{}, map[string]string{}
	for _, w := range wellKnown {
		r := resps[w.name]
		if r.Status != http.StatusOK {
			continue
		}
		var f struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(r.Body, &f) == nil && f.ID != "" {
			wk[f.ID] = w.kind
			if w.kind != "" {
				byKind[w.kind] = f.ID
			}
		}
	}
	c.mu.Lock()
	c.wellKnown, c.byKind = wk, byKind
	c.mu.Unlock()
	return nil
}

func (c *Client) listFolders(ctx context.Context, path, prefix string, depth int, out *[]mail.Folder) error {
	next := path
	for next != "" {
		var resp struct {
			Value    []gFolder `json:"value"`
			NextLink string    `json:"@odata.nextLink"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, next, nil, &resp); err != nil {
			return err
		}
		for _, f := range resp.Value {
			c.mu.Lock()
			kind, known := c.wellKnown[f.ID]
			c.mu.Unlock()
			if known && kind == "" {
				continue
			}
			if !known {
				kind = models.FolderCustom
			}
			name := f.DisplayName
			if prefix != "" {
				name = prefix + "/" + name
			}
			*out = append(*out, mail.Folder{RemoteID: f.ID, Name: name, Kind: kind, Unread: f.UnreadItemCount, Total: f.TotalItemCount})
			if f.ChildFolderCount > 0 && depth < 5 {
				if err := c.listFolders(ctx, "me/mailFolders/"+url.PathEscape(f.ID)+"/childFolders?"+folderSelect, name, depth+1, out); err != nil {
					return err
				}
			}
		}
		next = resp.NextLink
	}
	return nil
}

// presetColors maps Outlook category presets to hex colors.
var presetColors = map[string]string{
	"preset0": "#e74856", "preset1": "#ff8c00", "preset2": "#a0522d", "preset3": "#ffd700", "preset4": "#16c60c",
	"preset5": "#00b7c3", "preset6": "#808000", "preset7": "#0078d4", "preset8": "#8764b8", "preset9": "#c239b3",
	"preset10": "#4c6a85", "preset11": "#394b5a", "preset12": "#9ea2a6", "preset13": "#5d5a58", "preset14": "#1f1f1f",
	"preset15": "#a4262c", "preset16": "#ca5010", "preset17": "#6b3a1e", "preset18": "#c19c00", "preset19": "#0b6a0b",
	"preset20": "#005b70", "preset21": "#5c5c00", "preset22": "#004e8c", "preset23": "#5c2e91", "preset24": "#8a0f5c",
}

// PresetColor returns the hex color for an Outlook category preset.
func PresetColor(p string) string { return presetColors[strings.ToLower(p)] }

func (c *Client) Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error) {
	if err := c.loadWellKnown(ctx); err != nil {
		return nil, nil, err
	}
	var folders []mail.Folder
	if err := c.listFolders(ctx, "me/mailFolders?includeHiddenFolders=false&"+folderSelect, "", 0, &folders); err != nil {
		return nil, nil, err
	}
	folders = append(folders, mail.Folder{RemoteID: Flagged, Name: "Flagged", Kind: models.FolderStarred})
	var cats struct {
		Value []struct {
			DisplayName string `json:"displayName"`
			Color       string `json:"color"`
		} `json:"value"`
	}
	var labels []mail.Label
	if err := c.api.JSON(ctx, http.MethodGet, "me/outlook/masterCategories", nil, &cats); err == nil {
		for _, cat := range cats.Value {
			labels = append(labels, mail.Label{RemoteID: cat.DisplayName, Name: cat.DisplayName, Color: PresetColor(cat.Color)})
		}
	} else if s := httpx.StatusOf(err); s != http.StatusForbidden && s != http.StatusNotFound {
		return nil, nil, err
	}
	return folders, labels, nil
}

// ---- sync ----

type gAddr struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

func addr(a gAddr) models.Address {
	return models.Address{Name: a.EmailAddress.Name, Email: a.EmailAddress.Address}
}

func addrs(list []gAddr) []models.Address {
	out := make([]models.Address, 0, len(list))
	for _, a := range list {
		if a.EmailAddress.Address != "" {
			out = append(out, addr(a))
		}
	}
	return out
}

func toGAddrs(list []models.Address) []gAddr {
	out := make([]gAddr, 0, len(list))
	for _, a := range list {
		var g gAddr
		g.EmailAddress.Name, g.EmailAddress.Address = a.Name, a.Email
		out = append(out, g)
	}
	return out
}

type gMessage struct {
	ID               string    `json:"id"`
	ConversationID   string    `json:"conversationId"`
	Subject          string    `json:"subject"`
	BodyPreview      string    `json:"bodyPreview"`
	From             *gAddr    `json:"from"`
	Sender           *gAddr    `json:"sender"`
	ToRecipients     []gAddr   `json:"toRecipients"`
	CcRecipients     []gAddr   `json:"ccRecipients"`
	BccRecipients    []gAddr   `json:"bccRecipients"`
	ReplyTo          []gAddr   `json:"replyTo"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	SentDateTime     time.Time `json:"sentDateTime"`
	IsRead           bool      `json:"isRead"`
	IsDraft          bool      `json:"isDraft"`
	HasAttachments   bool      `json:"hasAttachments"`
	InternetMsgID    string    `json:"internetMessageId"`
	ParentFolderID   string    `json:"parentFolderId"`
	Categories       []string  `json:"categories"`
	Flag             struct {
		FlagStatus string `json:"flagStatus"`
	} `json:"flag"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	InternetMessageHeaders []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"internetMessageHeaders"`
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

const messageSelect = "id,conversationId,subject,bodyPreview,from,sender,toRecipients,ccRecipients,bccRecipients,replyTo," +
	"receivedDateTime,sentDateTime,isRead,isDraft,hasAttachments,internetMessageId,parentFolderId,categories,flag"

func toMessage(g gMessage) mail.Message {
	m := mail.Message{
		RemoteID:        g.ID,
		ThreadID:        g.ConversationID,
		FolderRemoteIDs: []string{g.ParentFolderID},
		To:              addrs(g.ToRecipients),
		Cc:              addrs(g.CcRecipients),
		Bcc:             addrs(g.BccRecipients),
		ReplyTo:         addrs(g.ReplyTo),
		Subject:         g.Subject,
		Snippet:         strings.Join(strings.Fields(g.BodyPreview), " "),
		Date:            g.ReceivedDateTime,
		Unread:          !g.IsRead && !g.IsDraft,
		Starred:         g.Flag.FlagStatus == "flagged",
		HasAttachments:  g.HasAttachments,
		MessageID:       g.InternetMsgID,
		Labels:          g.Categories,
	}
	if g.From != nil {
		m.From = addr(*g.From)
	} else if g.Sender != nil {
		m.From = addr(*g.Sender)
	}
	if m.Date.IsZero() {
		m.Date = g.SentDateTime
	}
	return m
}

func deltaKey(folderID string) string { return "delta:" + folderID }

func (c *Client) Sync(ctx context.Context, req provider.SyncRequest) (mail.SyncResult, error) {
	lim := req.Limits.WithDefaults()
	res := mail.SyncResult{State: map[string]string{}, Removed: map[string][]string{}}
	known := map[string]bool{}
	custom := 0
	for _, f := range req.Folders {
		if f.RemoteID == Flagged || f.Kind == models.FolderStarred {
			continue
		}
		known[deltaKey(f.RemoteID)] = true
		limit := lim.Other
		switch f.Kind {
		case models.FolderInbox:
			limit = lim.Inbox
		case models.FolderCustom:
			custom++
			if custom > 50 {
				continue
			}
			limit = lim.Custom
		}
		link := req.State[deltaKey(f.RemoteID)]
		initial := link == ""
		upserts, removed, newLink, err := c.syncFolder(ctx, f.RemoteID, link, limit)
		if isSyncStateGone(err) {
			initial = true
			upserts, removed, newLink, err = c.syncFolder(ctx, f.RemoteID, "", limit)
		}
		if err != nil {
			return mail.SyncResult{}, fmt.Errorf("syncing %s: %w", f.Name, err)
		}
		if initial {
			res.Initial = true
		}
		res.Upserts = append(res.Upserts, upserts...)
		if len(removed) > 0 {
			res.Removed[f.RemoteID] = removed
		}
		res.State[deltaKey(f.RemoteID)] = newLink
	}
	// Forget delta links of folders that disappeared.
	for k := range req.State {
		if strings.HasPrefix(k, "delta:") && !known[k] {
			res.State[k] = ""
		}
	}
	return res, nil
}

func isSyncStateGone(err error) bool {
	var e *httpx.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Status == http.StatusGone || strings.EqualFold(e.Code, "SyncStateNotFound") ||
		strings.EqualFold(e.Code, "syncStateInvalid") || strings.EqualFold(e.Code, "ErrorSyncStateInvalid")
}

// syncFolder runs one delta round. With an empty link it starts a new delta
// sequence restricted to (roughly) the newest limit messages.
func (c *Client) syncFolder(ctx context.Context, folderID, link string, limit int) (ups []mail.Message, removed []string, deltaLink string, err error) {
	next := link
	if next == "" {
		// Find the receivedDateTime of the limit-th newest message so the
		// delta round only covers recent mail.
		var top struct {
			Value []struct {
				ReceivedDateTime time.Time `json:"receivedDateTime"`
			} `json:"value"`
		}
		q := url.Values{}
		q.Set("$top", strconv.Itoa(min(limit, 1000)))
		q.Set("$orderby", "receivedDateTime desc")
		q.Set("$select", "receivedDateTime")
		if err := c.api.JSON(ctx, http.MethodGet, "me/mailFolders/"+url.PathEscape(folderID)+"/messages?"+q.Encode(), nil, &top); err != nil {
			return nil, nil, "", err
		}
		d := url.Values{}
		d.Set("$select", messageSelect)
		if len(top.Value) >= limit && limit > 0 {
			oldest := top.Value[len(top.Value)-1].ReceivedDateTime
			d.Set("$filter", "receivedDateTime ge "+oldest.UTC().Format("2006-01-02T15:04:05Z"))
		}
		next = "me/mailFolders/" + url.PathEscape(folderID) + "/messages/delta?" + d.Encode()
	}
	for pages := 0; next != ""; pages++ {
		if pages > 500 {
			return nil, nil, "", errors.New("too many delta pages")
		}
		resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet, Path: next,
			Headers: map[string]string{"Prefer": `IdType="ImmutableId", odata.maxpagesize=100`}})
		if err != nil {
			return nil, nil, "", err
		}
		var page struct {
			Value     []gMessage `json:"value"`
			NextLink  string     `json:"@odata.nextLink"`
			DeltaLink string     `json:"@odata.deltaLink"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, nil, "", fmt.Errorf("unexpected delta response: %w", err)
		}
		for _, g := range page.Value {
			if g.Removed != nil {
				removed = append(removed, g.ID)
				continue
			}
			if g.ParentFolderID == "" {
				g.ParentFolderID = folderID
			}
			ups = append(ups, toMessage(g))
		}
		if page.DeltaLink != "" {
			deltaLink = page.DeltaLink
		}
		next = page.NextLink
	}
	return ups, removed, deltaLink, nil
}

// ---- bodies ----

type gAttachment struct {
	ODataType    string `json:"@odata.type"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	Size         int64  `json:"size"`
	IsInline     bool   `json:"isInline"`
	ContentID    string `json:"contentId"`
	ContentBytes string `json:"contentBytes"`
}

func (c *Client) msgPath(id string) string { return "me/messages/" + url.PathEscape(id) }

func (c *Client) Body(ctx context.Context, remoteID string) (*mail.Body, error) {
	resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet,
		Path:    c.msgPath(remoteID) + "?$select=body,internetMessageHeaders,internetMessageId,ccRecipients,bccRecipients,replyTo,hasAttachments",
		Headers: map[string]string{"Prefer": `IdType="ImmutableId", outlook.body-content-type="html"`}})
	if err != nil {
		return nil, err
	}
	var g gMessage
	err = json.NewDecoder(resp.Body).Decode(&g)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	b := &mail.Body{MessageID: g.InternetMsgID, Cc: addrs(g.CcRecipients), Bcc: addrs(g.BccRecipients), ReplyTo: addrs(g.ReplyTo)}
	for _, h := range g.InternetMessageHeaders {
		switch strings.ToLower(h.Name) {
		case "in-reply-to":
			b.InReplyTo = strings.TrimSpace(h.Value)
		case "references":
			b.References = strings.Join(strings.Fields(h.Value), " ")
		}
	}
	if strings.EqualFold(g.Body.ContentType, "html") {
		b.HTML = g.Body.Content
		b.Text = mailmime.HTMLToText(g.Body.Content)
	} else {
		b.Text = g.Body.Content
	}
	if g.HasAttachments || strings.Contains(strings.ToLower(b.HTML), "cid:") {
		var list struct {
			Value []gAttachment `json:"value"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, c.msgPath(remoteID)+"/attachments?$select=id,name,contentType,size,isInline", nil, &list); err != nil {
			return nil, err
		}
		for _, a := range list.Value {
			att := mail.Attachment{Attachment: models.Attachment{ID: a.ID, Filename: a.Name, MimeType: a.ContentType, Size: a.Size, Inline: a.IsInline}}
			if att.Filename == "" {
				att.Filename = "attachment"
			}
			if a.IsInline && strings.HasPrefix(strings.ToLower(a.ContentType), "image/") && a.Size < 15<<20 {
				var full gAttachment
				if err := c.api.JSON(ctx, http.MethodGet, c.msgPath(remoteID)+"/attachments/"+url.PathEscape(a.ID), nil, &full); err == nil {
					att.ContentID = strings.Trim(full.ContentID, "<>")
					att.Data, _ = base64.StdEncoding.DecodeString(full.ContentBytes)
				}
			}
			b.Attachments = append(b.Attachments, att)
		}
	}
	return b, nil
}

func (c *Client) Attachment(ctx context.Context, remoteID, attachmentID string) ([]byte, error) {
	resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet, Path: c.msgPath(remoteID) + "/attachments/" + url.PathEscape(attachmentID) + "/$value"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ---- actions ----

func (c *Client) SetRead(ctx context.Context, ids []string, read bool) error {
	return c.batchEach(ctx, ids, func(id string) batchReq {
		return batchReq{Method: http.MethodPatch, URL: "/me/messages/" + url.PathEscape(id), Body: map[string]bool{"isRead": read}}
	})
}

func (c *Client) SetStarred(ctx context.Context, ids []string, starred bool) error {
	status := "notFlagged"
	if starred {
		status = "flagged"
	}
	return c.batchEach(ctx, ids, func(id string) batchReq {
		return batchReq{Method: http.MethodPatch, URL: "/me/messages/" + url.PathEscape(id), Body: map[string]any{"flag": map[string]string{"flagStatus": status}}}
	})
}

func (c *Client) Move(ctx context.Context, ids []string, target mail.Folder) error {
	if target.Kind == models.FolderStarred {
		return c.SetStarred(ctx, ids, true)
	}
	dest := target.RemoteID
	if dest == "" {
		return fmt.Errorf("this account has no %s folder", target.Name)
	}
	return c.batchEach(ctx, ids, func(id string) batchReq {
		return batchReq{Method: http.MethodPost, URL: "/me/messages/" + url.PathEscape(id) + "/move", Body: map[string]string{"destinationId": dest}}
	})
}

func (c *Client) PlanMove(current []string, target mail.Folder) []string {
	if target.Kind == models.FolderStarred {
		return current
	}
	return []string{target.RemoteID}
}

func (c *Client) SetLabel(ctx context.Context, ids []string, name string, add bool) error {
	reqs := make([]batchReq, len(ids))
	for i, id := range ids {
		reqs[i] = batchReq{ID: strconv.Itoa(i), Method: http.MethodGet, URL: "/me/messages/" + url.PathEscape(id) + "?$select=categories"}
	}
	resps, err := c.batch(ctx, reqs)
	if err != nil {
		return err
	}
	var patches []batchReq
	for i, id := range ids {
		r := resps[strconv.Itoa(i)]
		if err := batchError(r); err != nil {
			if r.Status == http.StatusNotFound {
				continue
			}
			return err
		}
		var m struct {
			Categories []string `json:"categories"`
		}
		_ = json.Unmarshal(r.Body, &m)
		cats := []string{}
		has := false
		for _, cat := range m.Categories {
			if strings.EqualFold(cat, name) {
				has = true
				if !add {
					continue
				}
			}
			cats = append(cats, cat)
		}
		if add && !has {
			cats = append(cats, name)
		}
		if has == add {
			continue // already in the desired state
		}
		patches = append(patches, batchReq{ID: strconv.Itoa(len(patches)), Method: http.MethodPatch,
			URL: "/me/messages/" + url.PathEscape(id), Body: map[string]any{"categories": cats}})
	}
	if len(patches) == 0 {
		return nil
	}
	resps, err = c.batch(ctx, patches)
	if err != nil {
		return err
	}
	for _, r := range resps {
		if err := batchError(r); err != nil {
			return err
		}
	}
	return nil
}

// ---- send ----

const smallAttachment = 3 << 20 // larger attachments use an upload session

func (c *Client) Send(ctx context.Context, out mail.Outgoing) error {
	var draft struct {
		ID string `json:"id"`
	}
	msg := map[string]any{
		"subject":       out.Subject,
		"toRecipients":  toGAddrs(out.To),
		"ccRecipients":  toGAddrs(out.Cc),
		"bccRecipients": toGAddrs(out.Bcc),
	}
	if out.HTML != "" {
		msg["body"] = map[string]string{"contentType": "HTML", "content": out.HTML}
	} else {
		msg["body"] = map[string]string{"contentType": "Text", "content": out.Text}
	}
	if o := out.Original; o != nil && out.Mode != "" && out.Mode != "new" {
		action := map[string]string{"reply": "createReply", "replyAll": "createReplyAll", "forward": "createForward"}[out.Mode]
		if action == "" {
			return fmt.Errorf("unknown compose mode %q", out.Mode)
		}
		if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(o.RemoteID)+"/"+action, map[string]any{}, &draft); err != nil {
			return err
		}
		// Replace the generated body and recipients with what the user wrote.
		if err := c.api.JSON(ctx, http.MethodPatch, c.msgPath(draft.ID), msg, nil); err != nil {
			c.discard(draft.ID)
			return err
		}
	} else {
		if err := c.api.JSON(ctx, http.MethodPost, "me/messages", msg, &draft); err != nil {
			return err
		}
	}
	for _, a := range out.Attachments {
		if err := c.addAttachment(ctx, draft.ID, a); err != nil {
			c.discard(draft.ID)
			return fmt.Errorf("could not attach %s: %w", a.Filename, err)
		}
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(draft.ID)+"/send", nil, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) discard(draftID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = c.api.JSON(ctx, http.MethodDelete, c.msgPath(draftID), nil, nil)
}

func (c *Client) addAttachment(ctx context.Context, draftID string, a mail.OutgoingAttachment) error {
	ct := a.MimeType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if len(a.Data) <= smallAttachment {
		return c.api.JSON(ctx, http.MethodPost, c.msgPath(draftID)+"/attachments", map[string]any{
			"@odata.type": "#microsoft.graph.fileAttachment", "name": a.Filename, "contentType": ct,
			"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
		}, nil)
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(draftID)+"/attachments/createUploadSession", map[string]any{
		"AttachmentItem": map[string]any{"attachmentType": "file", "name": a.Filename, "size": len(a.Data), "contentType": ct},
	}, &sess); err != nil {
		return err
	}
	// The upload URL is pre-authenticated: no bearer token.
	up := &httpx.Client{Service: "Microsoft Graph upload", Sleep: c.api.Sleep}
	const chunk = 320 * 1024 * 12 // multiple of 320 KiB
	for off := 0; off < len(a.Data); off += chunk {
		end := min(off+chunk, len(a.Data))
		resp, err := up.Do(ctx, httpx.Request{Method: http.MethodPut, Path: sess.UploadURL, Body: a.Data[off:end],
			ContentType: "application/octet-stream",
			Headers:     map[string]string{"Content-Range": fmt.Sprintf("bytes %d-%d/%d", off, end-1, len(a.Data))}})
		if err != nil {
			return err
		}
		resp.Body.Close()
	}
	return nil
}

var _ provider.Provider = (*Client)(nil)
