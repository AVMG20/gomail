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
	"log/slog"
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

// systemNames are the well-known folders' names, the same as Gmail's: Graph
// returns them in the mailbox's language ("Postvak IN"), which needn't be
// the app's.
var systemNames = map[string]string{
	models.FolderInbox: "Inbox", models.FolderSent: "Sent", models.FolderDrafts: "Drafts",
	models.FolderArchive: "All Mail", models.FolderSpam: "Spam", models.FolderTrash: "Trash",
}

// Client talks to one Microsoft mailbox.
type Client struct {
	api  *httpx.Client
	base string

	mu        sync.Mutex
	wellKnown map[string]string            // folder id -> kind ("" = hidden)
	byKind    map[string]string            // kind -> folder id
	added     map[string]map[string]string // draft id -> attachment key -> id (attachments we uploaded)

	wellKnownDone bool                   // wellKnown is complete (not a partial lookup)
	draftLocks    map[string]*sync.Mutex // draft id -> serializes saves of that draft
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
// sub-requests. It returns responses by request id; on error, the ones
// received before it (those sub-requests were applied).
func (c *Client) batch(ctx context.Context, reqs []batchReq) (map[string]batchResp, error) {
	out := map[string]batchResp{}
	pending := reqs
	for attempt := 0; len(pending) > 0; attempt++ {
		var retry []batchReq
		var wait time.Duration
		for _, chunk := range provider.Chunk(pending, batchSize) {
			for i := range chunk {
				if chunk[i].Headers == nil {
					chunk[i].Headers = map[string]string{}
				}
				// Headers of the $batch request don't apply to sub-requests.
				chunk[i].Headers["Prefer"] = `IdType="ImmutableId"`
				if chunk[i].Body != nil {
					chunk[i].Headers["Content-Type"] = "application/json"
				}
			}
			var resp struct {
				Responses []batchResp `json:"responses"`
			}
			if err := c.api.JSONRetry(ctx, http.MethodPost, "$batch", map[string]any{"requests": chunk}, &resp); err != nil {
				return out, err
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
				return out, err
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

// batchEach runs one request per id and returns the first error (a
// provider.PartialError when only some ids failed).
func (c *Client) batchEach(ctx context.Context, ids []string, mk func(id string) batchReq) error {
	reqs := make([]batchReq, len(ids))
	for i, id := range ids {
		r := mk(id)
		r.ID = strconv.Itoa(i)
		reqs[i] = r
	}
	resps, callErr := c.batch(ctx, reqs)
	if callErr != nil && len(resps) == 0 {
		return callErr
	}
	var first error
	var failed []string
	for i, id := range ids {
		r, ok := resps[strconv.Itoa(i)]
		var err error
		if !ok {
			err = callErr // a later $batch call failed: not applied
			if err == nil {
				err = errors.New("Microsoft Graph returned an incomplete batch response")
			}
		} else if err = batchError(r); r.Status == http.StatusNotFound {
			err = nil
		}
		if err != nil {
			failed = append(failed, id)
			if first == nil {
				first = err
			}
		}
	}
	return provider.Partial(first, failed, len(ids))
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
	done := c.wellKnownDone
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
	partial := false
	for _, w := range wellKnown {
		r := resps[w.name]
		if r.Status != http.StatusOK {
			// 404: the mailbox has no such folder. Anything else (an
			// outage that outlasted the retries) is looked up again next
			// time instead of being cached for the client's lifetime.
			partial = partial || r.Status != http.StatusNotFound
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
	c.wellKnown, c.byKind, c.wellKnownDone = wk, byKind, !partial
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
			if en := systemNames[kind]; known && en != "" {
				name = en
			}
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
	folders = append(folders, mail.Folder{RemoteID: Flagged, Name: "Starred", Kind: models.FolderStarred})
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
	ODataType string `json:"@odata.type"`
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
	res := mail.SyncResult{State: map[string]string{}, Removed: map[string][]string{}, Complete: map[string]time.Time{}}
	known := map[string]bool{}
	custom := 0
	for i, f := range req.Folders {
		req.Report("listing", i, len(req.Folders))
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
		upserts, removed, newLink, since, err := c.syncFolder(ctx, f.RemoteID, link, limit)
		if isSyncStateGone(err) {
			initial = true
			upserts, removed, newLink, since, err = c.syncFolder(ctx, f.RemoteID, "", limit)
		}
		if err != nil {
			return mail.SyncResult{}, fmt.Errorf("syncing %s: %w", f.Name, err)
		}
		if initial {
			res.Initial = true
			res.Complete[f.RemoteID] = since
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
// sequence restricted to (roughly) the newest limit messages; since is then
// the receivedDateTime it covers completely from (zero: the whole folder).
func (c *Client) syncFolder(ctx context.Context, folderID, link string, limit int) (ups []mail.Message, removed []string, deltaLink string, since time.Time, err error) {
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
			return nil, nil, "", time.Time{}, err
		}
		d := url.Values{}
		d.Set("$select", messageSelect)
		if len(top.Value) >= limit && limit > 0 {
			oldest := top.Value[len(top.Value)-1].ReceivedDateTime
			d.Set("$filter", "receivedDateTime ge "+oldest.UTC().Format("2006-01-02T15:04:05Z"))
			since = oldest.UTC().Truncate(time.Second)
		}
		next = "me/mailFolders/" + url.PathEscape(folderID) + "/messages/delta?" + d.Encode()
	}
	var partial []string // ids Graph sent without the message's headers
	for pages := 0; next != ""; pages++ {
		if pages > 500 {
			return nil, nil, "", time.Time{}, errors.New("too many delta pages")
		}
		resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet, Path: next,
			Headers: map[string]string{"Prefer": `IdType="ImmutableId", odata.maxpagesize=100`}})
		if err != nil {
			return nil, nil, "", time.Time{}, err
		}
		var page struct {
			Value     []gMessage `json:"value"`
			NextLink  string     `json:"@odata.nextLink"`
			DeltaLink string     `json:"@odata.deltaLink"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, nil, "", time.Time{}, fmt.Errorf("unexpected delta response: %w", err)
		}
		for _, g := range page.Value {
			if g.Removed != nil {
				removed = append(removed, g.ID)
				continue
			}
			if g.ParentFolderID == "" {
				g.ParentFolderID = folderID
			}
			if g.ReceivedDateTime.IsZero() && g.SentDateTime.IsZero() {
				// Graph sends some changes (e.g. read state) as just the id
				// and the changed properties: fetch the whole message
				// rather than store it without sender, subject and date.
				partial = append(partial, g.ID)
				continue
			}
			ups = append(ups, toMessage(g))
		}
		if page.DeltaLink != "" {
			deltaLink = page.DeltaLink
		}
		next = page.NextLink
	}
	full, err := c.fullMessages(ctx, partial, folderID)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	return append(ups, full...), removed, deltaLink, since, nil
}

// fullMessages fetches messages that a delta page only listed by id. Ones
// that are gone are skipped (their removal arrives separately).
func (c *Client) fullMessages(ctx context.Context, ids []string, folderID string) ([]mail.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	reqs := make([]batchReq, len(ids))
	for i, id := range ids {
		reqs[i] = batchReq{ID: strconv.Itoa(i), Method: http.MethodGet, URL: "/" + c.msgPath(id) + "?$select=" + messageSelect}
	}
	resps, err := c.batch(ctx, reqs)
	if err != nil {
		return nil, err
	}
	var out []mail.Message
	for i := range ids {
		r, ok := resps[strconv.Itoa(i)]
		if !ok || r.Status == http.StatusNotFound {
			continue
		}
		if err := batchError(r); err != nil {
			return nil, err
		}
		var g gMessage
		if err := json.Unmarshal(r.Body, &g); err != nil || (g.ReceivedDateTime.IsZero() && g.SentDateTime.IsZero()) {
			continue
		}
		if g.ParentFolderID == "" {
			g.ParentFolderID = folderID
		}
		out = append(out, toMessage(g))
	}
	return out, nil
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
	var unsub, unsubPost string
	for _, h := range g.InternetMessageHeaders {
		switch strings.ToLower(h.Name) {
		case "in-reply-to":
			b.InReplyTo = strings.TrimSpace(h.Value)
		case "references":
			b.References = strings.Join(strings.Fields(h.Value), " ")
		case "list-unsubscribe":
			unsub = h.Value
		case "list-unsubscribe-post":
			unsubPost = h.Value
		}
	}
	b.Unsubscribe = mailmime.ParseListUnsubscribe(unsub, unsubPost)
	if strings.Contains(strings.ToLower(g.ODataType), "eventmessage") {
		// Meeting requests/cancellations carry their event, not an ICS part.
		if inv, err := c.meetingInvite(ctx, remoteID); err == nil {
			b.Invite = inv
		} else {
			slog.Debug("reading the meeting of an event message failed", "err", err)
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

func draftFields(out mail.Outgoing) map[string]any {
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
	return msg
}

func attKey(name string, size int) string { return fmt.Sprintf("%s|%d", strings.ToLower(name), size) }

// wantKey identifies a wanted attachment: inline images by Content-ID.
func wantKey(a mail.OutgoingAttachment) string {
	if a.ContentID != "" {
		return "cid:" + strings.ToLower(a.ContentID)
	}
	return attKey(a.Filename, len(a.Data))
}

// SaveDraft creates or updates a draft. Replies and forwards are created
// with createReply/createReplyAll/createForward so Exchange threads them
// and keeps the forwarded attachments.
func (c *Client) SaveDraft(ctx context.Context, out mail.Outgoing, draftID string) (string, error) {
	msg := draftFields(out)
	if draftID != "" {
		// Saves of one draft can overlap (autosave flush vs. send); they
		// share its attachment bookkeeping, so run them one at a time.
		defer c.lockDraft(draftID)()
		err := c.api.JSON(ctx, http.MethodPatch, c.msgPath(draftID), msg, nil)
		if httpx.StatusOf(err) == http.StatusNotFound {
			draftID = "" // sent or deleted elsewhere: start over
		} else if err != nil {
			return "", err
		}
	}
	created := false
	if draftID == "" {
		var draft struct {
			ID string `json:"id"`
		}
		if o := out.Original; o != nil && out.Mode != "" && out.Mode != "new" {
			action := map[string]string{"reply": "createReply", "replyAll": "createReplyAll", "forward": "createForward"}[out.Mode]
			if action == "" {
				return "", fmt.Errorf("unknown compose mode %q", out.Mode)
			}
			if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(o.RemoteID)+"/"+action, map[string]any{}, &draft); err != nil {
				return "", err
			}
			// Replace the generated body and recipients with what the user wrote.
			if err := c.api.JSON(ctx, http.MethodPatch, c.msgPath(draft.ID), msg, nil); err != nil {
				c.discard(draft.ID)
				return "", err
			}
		} else if err := c.api.JSON(ctx, http.MethodPost, "me/messages", msg, &draft); err != nil {
			return "", err
		}
		draftID, created = draft.ID, true
	}
	// Forwards keep the files createForward put on the draft.
	if err := c.syncAttachments(ctx, draftID, out.Attachments, out.HTML, out.AttachmentsComplete && out.Mode != "forward"); err != nil {
		if created {
			c.discard(draftID)
		}
		return "", err
	}
	return draftID, nil
}

// attSlack bounds how many bytes more than its content Graph may report
// for a file (its size includes the attachment's properties).
const attSlack = 2 << 10

// syncAttachments makes the draft's user-added attachments match atts.
// Files the draft got from createForward, or that this client doesn't know
// about (uploaded before a restart, or elsewhere), are left alone unless
// complete says atts is the draft's whole file list.
func (c *Client) syncAttachments(ctx context.Context, draftID string, atts []mail.OutgoingAttachment, html string, complete bool) error {
	type wanted struct {
		key string // wantKey of the content
		a   mail.OutgoingAttachment
	}
	var want []wanted
	keys, seen := map[string]bool{}, map[string]bool{}
	for _, a := range atts {
		key := wantKey(a)
		dup := key // the same file attached twice is uploaded once
		if a.ID != "" {
			dup = "id:" + a.ID
		}
		if !seen[dup] {
			seen[dup], keys[key] = true, true
			want = append(want, wanted{key, a})
		}
	}
	c.mu.Lock()
	if c.added == nil {
		c.added = map[string]map[string]string{}
	}
	ours := c.added[draftID] // key -> attachment id
	if ours == nil {
		ours = map[string]string{}
		c.added[draftID] = ours
	}
	c.mu.Unlock()
	var existing struct {
		Value []gAttachment `json:"value"`
	}
	if len(atts) > 0 || len(ours) > 0 || complete {
		if err := c.api.JSON(ctx, http.MethodGet, c.msgPath(draftID)+"/attachments?$select=id,name,size,isInline", nil, &existing); err != nil {
			return err
		}
	}
	remove := func(id string) error {
		if id == "" {
			return nil
		}
		err := c.api.JSON(ctx, http.MethodDelete, c.msgPath(draftID)+"/attachments/"+url.PathEscape(id), nil, nil)
		if err != nil && httpx.StatusOf(err) != http.StatusNotFound {
			return err
		}
		return nil
	}
	// Files we uploaded that are no longer wanted go first, so a new file
	// of the same name (a replaced version) can't be paired with one.
	gone := map[string]bool{}
	c.mu.Lock()
	var drop []string
	for key := range ours {
		if !keys[key] {
			drop = append(drop, key)
		}
	}
	c.mu.Unlock()
	for _, key := range drop {
		c.mu.Lock()
		id := ours[key]
		c.mu.Unlock()
		if err := remove(id); err != nil {
			return err
		}
		gone[id] = true
		c.mu.Lock()
		delete(ours, key)
		c.mu.Unlock()
	}
	kept := existing.Value[:0:0]
	known := map[string]bool{}
	for _, e := range existing.Value {
		known[e.ID] = true
		if !gone[e.ID] {
			kept = append(kept, e)
		}
	}
	existing.Value = kept
	// Inline images pair by Content-ID, which the list doesn't include: read
	// it when an image isn't known to be ours.
	cids := map[string]string{} // server attachment id -> content id
	needCID := false
	for _, w := range want {
		needCID = needCID || w.a.ContentID != "" && ours[w.key] == ""
	}
	cidOf := func(e gAttachment) (string, error) {
		if cid, ok := cids[e.ID]; ok {
			return cid, nil
		}
		var full gAttachment
		if err := c.api.JSON(ctx, http.MethodGet, c.msgPath(draftID)+"/attachments/"+url.PathEscape(e.ID), nil, &full); err != nil {
			return "", err
		}
		cids[e.ID] = strings.Trim(full.ContentID, "<>")
		return cids[e.ID], nil
	}
	for _, e := range existing.Value {
		if needCID && e.IsInline {
			if _, err := cidOf(e); err != nil {
				return err
			}
		}
	}
	// Pair the wanted files with the draft's files: a file loaded from the
	// draft by its attachment id, a file or image we uploaded by our record
	// of it, an image by its Content-ID. Anything else is uploaded. Only for
	// a client that sends neither ids nor the whole file list, the same
	// name with a size that fits Graph's overhead counts too; with the
	// whole list, that file may be an older version the user removed,
	// which must not be sent instead.
	claimed := map[string]bool{} // server attachment id -> wanted
	present := make([]bool, len(want))
	for _, pass := range []func(e gAttachment, w wanted) bool{
		func(e gAttachment, w wanted) bool { return w.a.ID != "" && e.ID == w.a.ID },
		func(e gAttachment, w wanted) bool { return e.IsInline == (w.a.ContentID != "") && ours[w.key] == e.ID },
		func(e gAttachment, w wanted) bool {
			return w.a.ContentID != "" && e.IsInline && strings.EqualFold(cids[e.ID], w.a.ContentID)
		},
		func(e gAttachment, w wanted) bool {
			n := len(w.a.Data)
			return w.a.ID == "" && w.a.ContentID == "" && !complete && !e.IsInline && strings.EqualFold(e.Name, w.a.Filename) &&
				int(e.Size) >= n && int(e.Size) <= n+attSlack
		},
	} {
		for i, w := range want {
			if present[i] {
				continue
			}
			for _, e := range existing.Value {
				if !claimed[e.ID] && pass(e, w) {
					claimed[e.ID], present[i] = true, true
					if w.a.ContentID != "" {
						// Our image (its Content-ID came from this body): removed with it.
						c.mu.Lock()
						ours[w.key] = e.ID
						c.mu.Unlock()
					}
					break
				}
			}
		}
	}
	if complete {
		// The user removed these in the reopened draft: files, and inline
		// images the body no longer shows (deleted, or reopened from another
		// client and re-added under our Content-IDs).
		body := strings.ToLower(html)
		for _, e := range existing.Value {
			if claimed[e.ID] {
				continue
			}
			if e.IsInline {
				cid, err := cidOf(e)
				if err != nil {
					return err
				}
				if cid != "" && strings.Contains(body, "cid:"+strings.ToLower(cid)) {
					continue
				}
			}
			if err := remove(e.ID); err != nil {
				return err
			}
		}
	}
	for i, w := range want {
		if present[i] {
			continue
		}
		id, err := c.addAttachment(ctx, draftID, w.a, known)
		if err != nil {
			return fmt.Errorf("could not attach %s: %w", w.a.Filename, err)
		}
		known[id] = true
		if id != "" {
			c.mu.Lock()
			ours[w.key] = id
			c.mu.Unlock()
		}
	}
	return nil
}

func (c *Client) Send(ctx context.Context, out mail.Outgoing) error {
	id, err := c.SaveDraft(ctx, out, "")
	if err != nil {
		return err
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(id)+"/send", nil, nil); err != nil {
		c.discard(id)
		return err
	}
	c.forget(id)
	return nil
}

func (c *Client) SendDraft(ctx context.Context, out mail.Outgoing, draftID string) error {
	id, err := c.SaveDraft(ctx, out, draftID)
	if err != nil {
		return err
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(id)+"/send", nil, nil); err != nil {
		return err
	}
	c.forget(id)
	return nil
}

func (c *Client) DeleteDraft(ctx context.Context, draftID string) error {
	err := c.api.JSON(ctx, http.MethodDelete, c.msgPath(draftID), nil, nil)
	c.forget(draftID)
	if httpx.StatusOf(err) == http.StatusNotFound {
		return nil
	}
	return err
}

// DraftID: Graph drafts are messages, so the ids are the same.
func (c *Client) DraftID(_ context.Context, messageRemoteID string) (string, error) {
	return messageRemoteID, nil
}

func (c *Client) forget(draftID string) {
	c.mu.Lock()
	delete(c.added, draftID)
	delete(c.draftLocks, draftID)
	c.mu.Unlock()
}

// lockDraft locks one draft's saves and returns the unlock function.
func (c *Client) lockDraft(draftID string) func() {
	c.mu.Lock()
	if c.draftLocks == nil {
		c.draftLocks = map[string]*sync.Mutex{}
	}
	l := c.draftLocks[draftID]
	if l == nil {
		l = &sync.Mutex{}
		c.draftLocks[draftID] = l
	}
	c.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ---- older mail & search ----

func (c *Client) ListOlder(ctx context.Context, folder mail.Folder, before time.Time, limit int) ([]mail.Message, error) {
	if folder.RemoteID == Flagged || folder.Kind == models.FolderStarred {
		return nil, nil
	}
	q := url.Values{}
	q.Set("$top", strconv.Itoa(min(max(limit, 1), 1000)))
	q.Set("$orderby", "receivedDateTime desc")
	q.Set("$select", messageSelect)
	if !before.IsZero() {
		q.Set("$filter", "receivedDateTime lt "+provider.OlderBound(before).UTC().Format("2006-01-02T15:04:05Z"))
	}
	var resp struct {
		Value []gMessage `json:"value"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, "me/mailFolders/"+url.PathEscape(folder.RemoteID)+"/messages?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]mail.Message, 0, len(resp.Value))
	for _, g := range resp.Value {
		if g.ParentFolderID == "" {
			g.ParentFolderID = folder.RemoteID
		}
		out = append(out, toMessage(g))
	}
	return out, nil
}

func kqlValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \t") {
		return "'" + strings.ReplaceAll(s, "'", "") + "'"
	}
	return s
}

// KQL renders a query for Graph $search (Keyword Query Language). Unread
// and starred aren't searchable; they are applied locally afterwards.
func KQL(q mail.SearchQuery) string {
	var parts []string
	for _, t := range q.Terms {
		parts = append(parts, kqlValue(t))
	}
	for _, v := range q.From {
		parts = append(parts, "from:"+kqlValue(v))
	}
	for _, v := range q.To {
		parts = append(parts, "to:"+kqlValue(v))
	}
	for _, v := range q.Subject {
		parts = append(parts, "subject:"+kqlValue(v))
	}
	if q.HasAttachment {
		parts = append(parts, "hasattachments:true")
	}
	if !q.After.IsZero() {
		parts = append(parts, "received>="+q.After.Format("2006-01-02"))
	}
	if !q.Before.IsZero() {
		parts = append(parts, "received<"+q.Before.Format("2006-01-02"))
	}
	return strings.Join(parts, " ")
}

func (c *Client) Search(ctx context.Context, q mail.SearchQuery, limit int) ([]mail.Message, error) {
	kql := KQL(q)
	if kql == "" {
		return nil, nil
	}
	v := url.Values{}
	v.Set("$search", `"`+kql+`"`)
	v.Set("$top", strconv.Itoa(min(max(limit, 1), 250)))
	v.Set("$select", messageSelect)
	var resp struct {
		Value []gMessage `json:"value"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, "me/messages?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]mail.Message, 0, len(resp.Value))
	for _, g := range resp.Value {
		if g.ParentFolderID != "" {
			out = append(out, toMessage(g))
		}
	}
	return out, nil
}

func (c *Client) discard(draftID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = c.api.JSON(ctx, http.MethodDelete, c.msgPath(draftID), nil, nil)
}

// addAttachment uploads a file to a draft and returns its attachment id
// ("" if it can't be told). known lists the draft's attachment ids before
// the upload.
func (c *Client) addAttachment(ctx context.Context, draftID string, a mail.OutgoingAttachment, known map[string]bool) (string, error) {
	ct := a.MimeType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if len(a.Data) <= smallAttachment {
		var created struct {
			ID string `json:"id"`
		}
		body := map[string]any{
			"@odata.type": "#microsoft.graph.fileAttachment", "name": a.Filename, "contentType": ct,
			"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
		}
		if a.ContentID != "" {
			body["isInline"], body["contentId"] = true, a.ContentID
		}
		err := c.api.JSON(ctx, http.MethodPost, c.msgPath(draftID)+"/attachments", body, &created)
		return created.ID, err
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	item := map[string]any{"attachmentType": "file", "name": a.Filename, "size": len(a.Data), "contentType": ct}
	if a.ContentID != "" {
		item["isInline"], item["contentId"] = true, a.ContentID
	}
	if err := c.api.JSON(ctx, http.MethodPost, c.msgPath(draftID)+"/attachments/createUploadSession", map[string]any{
		"AttachmentItem": item,
	}, &sess); err != nil {
		return "", err
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
			return "", err
		}
		resp.Body.Close()
	}
	// Upload sessions don't return the attachment id; look it up: the new
	// file of that name (Graph's size includes metadata, so it reports at
	// least the content's size).
	var list struct {
		Value []gAttachment `json:"value"`
	}
	if err := c.api.JSON(ctx, http.MethodGet, c.msgPath(draftID)+"/attachments?$select=id,name,size", nil, &list); err == nil {
		for _, x := range list.Value {
			if !known[x.ID] && strings.EqualFold(x.Name, a.Filename) && int(x.Size) >= len(a.Data) {
				return x.ID, nil
			}
		}
	}
	return "", nil
}

// ---- folder-wide operations ----

// flaggedUnreadFilter selects the unread mail of the virtual Flagged folder.
const flaggedUnreadFilter = "flag/flagStatus eq 'flagged' and isRead eq false"

// folderIDs lists message ids of a folder (optionally filtered).
func (c *Client) folderIDs(ctx context.Context, folderID, filter string) ([]string, error) {
	return c.messageIDs(ctx, "me/mailFolders/"+url.PathEscape(folderID)+"/messages", filter)
}

// messageIDs lists message ids of a messages collection (optionally filtered).
func (c *Client) messageIDs(ctx context.Context, path, filter string) ([]string, error) {
	q := url.Values{"$select": {"id"}, "$top": {"1000"}}
	if filter != "" {
		q.Set("$filter", filter)
	}
	next := path + "?" + q.Encode()
	var ids []string
	for next != "" && len(ids) < provider.MaxFolderOp {
		var resp struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := c.api.JSON(ctx, http.MethodGet, next, nil, &resp); err != nil {
			return nil, err
		}
		for _, v := range resp.Value {
			ids = append(ids, v.ID)
		}
		next = resp.NextLink
	}
	return ids, nil
}

func (c *Client) MarkFolderRead(ctx context.Context, folder mail.Folder) error {
	var ids []string
	var err error
	if folder.RemoteID == Flagged {
		// Flagged isn't a Graph folder: it's flagged mail in any folder.
		ids, err = c.messageIDs(ctx, "me/messages", flaggedUnreadFilter)
	} else {
		ids, err = c.folderIDs(ctx, folder.RemoteID, "isRead eq false")
	}
	if err != nil {
		return err
	}
	return c.SetRead(ctx, ids, true)
}

func (c *Client) EmptyFolder(ctx context.Context, folder mail.Folder) error {
	if folder.Kind != models.FolderTrash && folder.Kind != models.FolderSpam {
		return errors.New("only Deleted Items and Junk Email can be emptied")
	}
	ids, err := c.folderIDs(ctx, folder.RemoteID, "")
	if err != nil {
		return err
	}
	return c.batchEach(ctx, ids, func(id string) batchReq {
		return batchReq{Method: http.MethodPost, URL: "/me/messages/" + url.PathEscape(id) + "/permanentDelete", Body: map[string]any{}}
	})
}

// DeleteFolder moves a custom folder's mail to Deleted Items, then deletes
// the empty folder for good (a plain delete would only move it, with its
// name, under Deleted Items). It checks on the server that the folder is
// empty first, so nothing is deleted with it (except mail an Outlook rule
// files into it in the moment between that check and the delete).
func (c *Client) DeleteFolder(ctx context.Context, folder mail.Folder) error {
	if folder.Kind != models.FolderCustom || folder.RemoteID == "" {
		return errors.New("only your own folders can be deleted")
	}
	path := "me/mailFolders/" + url.PathEscape(folder.RemoteID)
	var info struct {
		ChildFolderCount int `json:"childFolderCount"`
		TotalItemCount   int `json:"totalItemCount"`
	}
	stat := func() error {
		return c.api.JSON(ctx, http.MethodGet, path+"?$select=childFolderCount,totalItemCount", nil, &info)
	}
	if err := stat(); err != nil {
		return err
	}
	if info.ChildFolderCount > 0 {
		return provider.ErrFolderHasSubfolders
	}
	var ids []string // moved to Deleted Items (immutable ids: they keep them)
	if info.TotalItemCount > 0 {
		var err error
		if ids, err = c.folderIDs(ctx, folder.RemoteID, ""); err != nil {
			return err
		}
		if err := c.Move(ctx, ids, mail.Folder{RemoteID: "deleteditems", Name: "Trash", Kind: models.FolderTrash}); err != nil {
			return err
		}
		if err := stat(); err != nil {
			return err
		}
		if info.TotalItemCount > 0 || info.ChildFolderCount > 0 {
			return errors.New("not all of its mail could be moved to Deleted Items yet, so the folder was kept; delete it again to finish")
		}
	}
	err := c.api.JSON(ctx, http.MethodPost, path+"/permanentDelete", map[string]any{}, nil)
	switch httpx.StatusOf(err) {
	case http.StatusBadRequest, http.StatusForbidden:
		// Outlook's own folders (RSS Feeds, Scheduled, ...) can't be
		// deleted: their mail goes back.
		if len(ids) > 0 {
			if berr := c.Move(ctx, ids, folder); berr != nil {
				return fmt.Errorf("Outlook doesn’t let this folder be deleted, and its mail couldn’t all go back (it’s in Deleted Items): %w", berr)
			}
		}
		return fmt.Errorf("Outlook doesn’t let this folder be deleted; hide it instead (%w)", err)
	}
	return err
}

var _ provider.Provider = (*Client)(nil)

var meetingMethods = map[string]string{"meetingRequest": "REQUEST", "meetingCancelled": "CANCEL", "meetingAccepted": "REPLY",
	"meetingTenativelyAccepted": "REPLY", "meetingTentativelyAccepted": "REPLY", "meetingDeclined": "REPLY"}

// meetingInvite reads the event of an event message
// ($expand=microsoft.graph.eventMessage/event, times in UTC).
func (c *Client) meetingInvite(ctx context.Context, remoteID string) (*models.Invite, error) {
	resp, err := c.api.Do(ctx, httpx.Request{Method: http.MethodGet,
		Path:    c.msgPath(remoteID) + "?$expand=microsoft.graph.eventMessage/event",
		Headers: map[string]string{"Prefer": `IdType="ImmutableId", outlook.timezone="UTC"`}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m struct {
		MeetingMessageType string `json:"meetingMessageType"`
		Event              *struct {
			ICalUID  string `json:"iCalUId"`
			Subject  string `json:"subject"`
			IsAllDay bool   `json:"isAllDay"`
			Start    gTime  `json:"start"`
			End      gTime  `json:"end"`
			Location *struct {
				DisplayName string `json:"displayName"`
			} `json:"location"`
			Organizer *gAddr `json:"organizer"`
		} `json:"event"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	if m.Event == nil || m.Event.ICalUID == "" {
		return nil, errors.New("no event")
	}
	e := m.Event
	inv := &models.Invite{UID: e.ICalUID, Title: e.Subject, AllDay: e.IsAllDay, Method: meetingMethods[m.MeetingMessageType]}
	if inv.Method == "" {
		inv.Method = "REQUEST"
	}
	inv.Start, inv.End = graphUTC(e.Start.DateTime), graphUTC(e.End.DateTime)
	if e.IsAllDay {
		y, mo, d := inv.Start.Add(12 * time.Hour).Date()
		inv.Start = time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
		y, mo, d = inv.End.Add(12 * time.Hour).Date()
		inv.End = time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	}
	if e.Location != nil {
		inv.Location = e.Location.DisplayName
	}
	if e.Organizer != nil {
		inv.Organizer = addr(*e.Organizer)
	}
	return inv, nil
}

type gTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

func graphUTC(s string) time.Time {
	s = strings.TrimSuffix(s, "Z")
	for _, layout := range []string{"2006-01-02T15:04:05.0000000", "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}
