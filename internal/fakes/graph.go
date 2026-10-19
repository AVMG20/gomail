package fakes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GraphMessage is a message in the fake Microsoft mailbox.
type GraphMessage struct {
	ID             string
	ConversationID string
	Folder         string // folder id or well-known name ("inbox", "archive", ...)
	Subject        string
	FromName       string
	From           string
	To             []string
	Received       time.Time
	IsRead         bool
	Flagged        bool
	IsDraft        bool
	Categories     []string
	HTML           string
	MessageID      string
	Attachments    []Attachment

	seq int
}

type graphFolder struct{ id, name, wellKnown string }

type graphRemoval struct {
	seq    int
	folder string
	id     string
}

type uploadSession struct {
	msg  string
	att  Attachment
	size int
	buf  bytes.Buffer
}

// Graph is a stateful fake of the Microsoft Graph mail API: /me, mail
// folders, delta queries, messages (get/patch/move/delete/send), drafts with
// createReply/createForward, attachments incl. upload sessions, master
// categories and JSON $batch (sub-requests go through the same handler).
type Graph struct {
	Server *httptest.Server
	Email  string
	Name   string
	Auth   func(token string) bool

	mu         sync.Mutex
	folders    []graphFolder
	msgs       map[string]*GraphMessage
	removals   []graphRemoval
	seq        int
	n          int
	sent       []GraphMessage
	categories []string
	uploads    map[string]*uploadSession

	// MissingImmutableID counts requests (incl. $batch sub-requests) that
	// did not ask for immutable ids.
	MissingImmutableID int
	Batches            int
}

// NewGraph starts a fake mailbox.
func NewGraph(email, name string, auth func(string) bool) *Graph {
	g := &Graph{Email: email, Name: name, Auth: auth, msgs: map[string]*GraphMessage{}, uploads: map[string]*uploadSession{}}
	for _, wk := range []struct{ wk, name string }{{"inbox", "Inbox"}, {"sentitems", "Sent Items"}, {"drafts", "Drafts"},
		{"archive", "Archive"}, {"junkemail", "Junk Email"}, {"deleteditems", "Deleted Items"}, {"outbox", "Outbox"}} {
		g.folders = append(g.folders, graphFolder{id: "AAMkF-" + wk.wk, name: wk.name, wellKnown: wk.wk})
	}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

// Close stops the server.
func (g *Graph) Close() { g.Server.Close() }

// URL is the Graph base URL including /v1.0 (pass as outlook.New's base).
func (g *Graph) URL() string { return g.Server.URL + "/v1.0" }

// AddFolder creates a custom top-level folder and returns its id.
func (g *Graph) AddFolder(name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	id := fmt.Sprintf("AAMkF-custom%d", g.n)
	g.folders = append(g.folders, graphFolder{id: id, name: name})
	return id
}

// AddCategory adds a master category.
func (g *Graph) AddCategory(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.categories = append(g.categories, name)
}

func (g *Graph) folderID(ref string) string {
	for _, f := range g.folders {
		if f.id == ref || strings.EqualFold(f.wellKnown, ref) {
			return f.id
		}
	}
	return ""
}

func (g *Graph) bump(m *GraphMessage) {
	g.seq++
	m.seq = g.seq
}

// Deliver adds a message and returns its id.
func (g *Graph) Deliver(m GraphMessage) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.add(&m)
}

func (g *Graph) add(m *GraphMessage) string {
	g.n++
	if m.ID == "" {
		m.ID = fmt.Sprintf("AAMkM-%04d", g.n)
	}
	if m.ConversationID == "" {
		m.ConversationID = "conv-" + m.ID
	}
	if m.Received.IsZero() {
		m.Received = time.Now()
	}
	if m.MessageID == "" {
		m.MessageID = "<" + m.ID + "@fake.outlook>"
	}
	if id := g.folderID(m.Folder); id != "" {
		m.Folder = id
	} else {
		m.Folder = g.folderID("inbox")
	}
	for i := range m.Attachments {
		if m.Attachments[i].ID == "" {
			g.n++
			m.Attachments[i].ID = fmt.Sprintf("AAMkA-%d", g.n)
		}
	}
	g.msgs[m.ID] = m
	g.bump(m)
	return m.ID
}

// Message returns a copy of a message.
func (g *Graph) Message(id string) (GraphMessage, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.msgs[id]
	if !ok {
		return GraphMessage{}, false
	}
	c := *m
	c.Categories = slices.Clone(m.Categories)
	return c, true
}

// FolderOf returns the well-known name (or id) of a message's folder.
func (g *Graph) FolderOf(id string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.msgs[id]
	if !ok {
		return ""
	}
	for _, f := range g.folders {
		if f.id == m.Folder && f.wellKnown != "" {
			return f.wellKnown
		}
	}
	return m.Folder
}

// Sent returns copies of the messages sent through the API.
func (g *Graph) Sent() []GraphMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.sent)
}

// Drafts counts messages in the Drafts folder.
func (g *Graph) Drafts() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, m := range g.msgs {
		if m.Folder == g.folderID("drafts") {
			n++
		}
	}
	return n
}

func (g *Graph) move(m *GraphMessage, folder string) {
	if m.Folder == folder {
		return
	}
	g.seq++
	g.removals = append(g.removals, graphRemoval{seq: g.seq, folder: m.Folder, id: m.ID})
	m.Folder = folder
	g.bump(m)
}

func (g *Graph) remove(m *GraphMessage) {
	g.seq++
	g.removals = append(g.removals, graphRemoval{seq: g.seq, folder: m.Folder, id: m.ID})
	delete(g.msgs, m.ID)
}

func (g *Graph) serve(w http.ResponseWriter, r *http.Request) {
	if p, ok := strings.CutPrefix(r.URL.Path, "/upload/"); ok {
		// Pre-authenticated upload URL: no bearer token expected.
		g.serveUpload(w, r, p)
		return
	}
	if g.Auth != nil && !g.Auth(bearer(r)) {
		apiError(w, http.StatusUnauthorized, "InvalidAuthenticationToken", "Access token has expired or is not yet valid.")
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/v1.0/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if data, _ := io.ReadAll(r.Body); len(data) > 0 {
		if err := json.Unmarshal(data, &body); err != nil {
			apiError(w, 400, "BadRequest", "invalid JSON")
			return
		}
	}
	if p == "$batch" {
		g.batch(w, r, body)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !strings.Contains(r.Header.Get("Prefer"), `IdType="ImmutableId"`) {
		g.MissingImmutableID++
	}
	g.route(w, r, p, body)
}

func (g *Graph) batch(w http.ResponseWriter, r *http.Request, body map[string]any) {
	reqs, _ := body["requests"].([]any)
	if len(reqs) > 20 {
		apiError(w, 400, "BadRequest", "too many requests in batch")
		return
	}
	g.mu.Lock()
	g.Batches++
	g.mu.Unlock()
	var out []map[string]any
	for _, x := range reqs {
		sub, _ := x.(map[string]any)
		id, _ := sub["id"].(string)
		method, _ := sub["method"].(string)
		u, _ := sub["url"].(string)
		var rb io.Reader = http.NoBody
		if b, ok := sub["body"]; ok {
			data, _ := json.Marshal(b)
			rb = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, "/v1.0"+u, rb)
		req.Header.Set("Authorization", r.Header.Get("Authorization"))
		if hs, ok := sub["headers"].(map[string]any); ok {
			for k, v := range hs {
				if s, ok := v.(string); ok {
					req.Header.Set(k, s)
				}
			}
		}
		rec := httptest.NewRecorder()
		g.serve(rec, req)
		res := map[string]any{"id": id, "status": rec.Code, "headers": map[string]string{"Content-Type": rec.Header().Get("Content-Type")}}
		if rec.Body.Len() > 0 {
			res["body"] = json.RawMessage(bytes.TrimSpace(rec.Body.Bytes()))
		}
		out = append(out, res)
	}
	writeJSON(w, 200, map[string]any{"responses": out})
}

func (g *Graph) render(m *GraphMessage, withBody bool) map[string]any {
	to := []any{}
	for _, a := range m.To {
		to = append(to, map[string]any{"emailAddress": map[string]string{"address": a}})
	}
	flag := "notFlagged"
	if m.Flagged {
		flag = "flagged"
	}
	cats := m.Categories
	if cats == nil {
		cats = []string{}
	}
	out := map[string]any{
		"id": m.ID, "conversationId": m.ConversationID, "subject": m.Subject, "bodyPreview": m.Subject,
		"from":             map[string]any{"emailAddress": map[string]string{"name": m.FromName, "address": m.From}},
		"toRecipients":     to,
		"receivedDateTime": m.Received.UTC().Format(time.RFC3339), "sentDateTime": m.Received.UTC().Format(time.RFC3339),
		"isRead": m.IsRead, "isDraft": m.IsDraft, "hasAttachments": len(m.Attachments) > 0,
		"internetMessageId": m.MessageID, "parentFolderId": m.Folder, "categories": cats,
		"flag": map[string]string{"flagStatus": flag},
	}
	if withBody {
		out["body"] = map[string]string{"contentType": "html", "content": m.HTML}
		out["internetMessageHeaders"] = []any{}
	}
	return out
}

func (g *Graph) inFolder(folder string) []*GraphMessage {
	var list []*GraphMessage
	for _, m := range g.msgs {
		if m.Folder == folder {
			list = append(list, m)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Received.After(list[j].Received) })
	return list
}

// timeFilter applies "receivedDateTime ge|lt <time>" filters.
func timeFilter(list []*GraphMessage, filter string) []*GraphMessage {
	f := strings.Fields(filter)
	if len(f) != 3 || f[0] != "receivedDateTime" {
		return list
	}
	at, err := time.Parse(time.RFC3339, f[2])
	if err != nil {
		return list
	}
	var out []*GraphMessage
	for _, m := range list {
		if (f[1] == "ge" && !m.Received.Before(at)) || (f[1] == "lt" && m.Received.Before(at)) {
			out = append(out, m)
		}
	}
	return out
}

func (g *Graph) route(w http.ResponseWriter, r *http.Request, p string, body map[string]any) {
	q := r.URL.Query()
	seg := strings.Split(p, "/")
	get := r.Method == http.MethodGet
	switch {
	case p == "me" && get:
		writeJSON(w, 200, map[string]any{"displayName": g.Name, "mail": g.Email, "userPrincipalName": g.Email})
	case p == "me/outlook/masterCategories" && get:
		var out []any
		for _, c := range g.categories {
			out = append(out, map[string]string{"displayName": c, "color": "preset7"})
		}
		writeJSON(w, 200, map[string]any{"value": out})
	case p == "me/mailFolders" && get:
		var out []any
		for _, f := range g.folders {
			if f.wellKnown == "outbox" && q.Get("includeHiddenFolders") == "false" {
				continue
			}
			total, unread := 0, 0
			for _, m := range g.msgs {
				if m.Folder == f.id {
					total++
					if !m.IsRead {
						unread++
					}
				}
			}
			out = append(out, map[string]any{"id": f.id, "displayName": f.name, "parentFolderId": "root",
				"childFolderCount": 0, "totalItemCount": total, "unreadItemCount": unread})
		}
		writeJSON(w, 200, map[string]any{"value": out})
	case len(seg) == 3 && seg[1] == "mailFolders" && get:
		id := g.folderID(seg[2])
		if id == "" {
			apiError(w, 404, "ErrorFolderNotFound", "The specified folder could not be found in the store.")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
	case len(seg) == 4 && seg[1] == "mailFolders" && seg[3] == "childFolders":
		writeJSON(w, 200, map[string]any{"value": []any{}})
	case len(seg) == 4 && seg[1] == "mailFolders" && seg[3] == "messages" && get:
		id := g.folderID(seg[2])
		list := timeFilter(g.inFolder(id), q.Get("$filter"))
		if q.Get("$filter") == "isRead eq false" {
			var unread []*GraphMessage
			for _, m := range list {
				if !m.IsRead {
					unread = append(unread, m)
				}
			}
			list = unread
		}
		if n, err := strconv.Atoi(q.Get("$top")); err == nil && len(list) > n {
			list = list[:n]
		}
		out := []any{}
		for _, m := range list {
			out = append(out, g.render(m, false))
		}
		writeJSON(w, 200, map[string]any{"value": out})
	case len(seg) == 5 && seg[1] == "mailFolders" && seg[3] == "messages" && seg[4] == "delta":
		g.delta(w, g.folderID(seg[2]), q)
	case p == "me/messages" && get:
		search := strings.ToLower(strings.Trim(q.Get("$search"), `"`))
		out := []any{}
		for _, m := range g.msgs {
			if search == "" || strings.Contains(strings.ToLower(m.Subject+" "+m.From+" "+m.HTML), search) {
				out = append(out, g.render(m, false))
			}
		}
		writeJSON(w, 200, map[string]any{"value": out})
	case p == "me/messages" && r.Method == http.MethodPost:
		m := &GraphMessage{Folder: "drafts", IsDraft: true, IsRead: true}
		g.applyFields(m, body)
		g.add(m)
		writeJSON(w, 201, g.render(m, true))
	case len(seg) >= 3 && seg[1] == "messages":
		m, ok := g.msgs[seg[2]]
		if !ok {
			apiError(w, 404, "ErrorItemNotFound", "The specified object was not found in the store.")
			return
		}
		g.message(w, r, m, seg[3:], body)
	default:
		apiError(w, 400, "BadRequest", "fake graph: no route for "+r.Method+" "+p)
	}
}

func (g *Graph) delta(w http.ResponseWriter, folder string, q url.Values) {
	if folder == "" {
		apiError(w, 404, "ErrorFolderNotFound", "folder not found")
		return
	}
	out := []any{}
	if tok := q.Get("$deltatoken"); tok != "" {
		since, err := strconv.Atoi(tok)
		if err != nil {
			apiError(w, 410, "SyncStateNotFound", "sync state not found")
			return
		}
		for _, m := range g.inFolder(folder) {
			if m.seq > since {
				out = append(out, g.render(m, false))
			}
		}
		for _, rm := range g.removals {
			if rm.seq > since && rm.folder == folder {
				if m, ok := g.msgs[rm.id]; ok && m.Folder == folder {
					continue
				}
				out = append(out, map[string]any{"id": rm.id, "@removed": map[string]string{"reason": "deleted"}})
			}
		}
	} else {
		for _, m := range timeFilter(g.inFolder(folder), q.Get("$filter")) {
			out = append(out, g.render(m, false))
		}
	}
	link := g.URL() + "/me/mailFolders/" + url.PathEscape(folder) + "/messages/delta?$deltatoken=" + strconv.Itoa(g.seq)
	writeJSON(w, 200, map[string]any{"value": out, "@odata.deltaLink": link})
}

func (g *Graph) applyFields(m *GraphMessage, body map[string]any) {
	if v, ok := body["subject"].(string); ok {
		m.Subject = v
	}
	if b, ok := body["body"].(map[string]any); ok {
		m.HTML, _ = b["content"].(string)
	}
	if list, ok := body["toRecipients"].([]any); ok {
		m.To = nil
		for _, x := range list {
			a, _ := x.(map[string]any)
			e, _ := a["emailAddress"].(map[string]any)
			if addr, _ := e["address"].(string); addr != "" {
				m.To = append(m.To, addr)
			}
		}
	}
	if v, ok := body["isRead"].(bool); ok {
		m.IsRead = v
	}
	if f, ok := body["flag"].(map[string]any); ok {
		m.Flagged = f["flagStatus"] == "flagged"
	}
	if c, ok := body["categories"]; ok {
		m.Categories = strs(c)
	}
}

func (g *Graph) message(w http.ResponseWriter, r *http.Request, m *GraphMessage, rest []string, body map[string]any) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		writeJSON(w, 200, g.render(m, true))
	case len(rest) == 0 && r.Method == http.MethodPatch:
		g.applyFields(m, body)
		g.bump(m)
		writeJSON(w, 200, g.render(m, false))
	case len(rest) == 0 && r.Method == http.MethodDelete:
		g.remove(m)
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && rest[0] == "move":
		dest := g.folderID(fmt.Sprint(body["destinationId"]))
		if dest == "" {
			apiError(w, 400, "ErrorInvalidIdMalformed", "bad destination")
			return
		}
		g.move(m, dest)
		writeJSON(w, 201, g.render(m, false))
	case len(rest) == 1 && rest[0] == "permanentDelete":
		g.remove(m)
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 1 && rest[0] == "send":
		if !m.IsDraft {
			apiError(w, 400, "ErrorInvalidRequest", "only drafts can be sent")
			return
		}
		m.IsDraft = false
		g.move(m, g.folderID("sentitems"))
		c := *m
		c.Attachments = slices.Clone(m.Attachments)
		g.sent = append(g.sent, c)
		w.WriteHeader(http.StatusAccepted)
	case len(rest) == 1 && (rest[0] == "createReply" || rest[0] == "createReplyAll" || rest[0] == "createForward"):
		prefix := "RE: "
		d := &GraphMessage{Folder: "drafts", IsDraft: true, IsRead: true, ConversationID: m.ConversationID}
		if rest[0] == "createForward" {
			prefix = "FW: "
			for _, a := range m.Attachments {
				a.ID = ""
				d.Attachments = append(d.Attachments, a)
			}
		} else {
			d.To = []string{m.From}
		}
		d.Subject = prefix + m.Subject
		g.add(d)
		writeJSON(w, 201, g.render(d, true))
	case len(rest) == 1 && rest[0] == "attachments" && r.Method == http.MethodGet:
		out := []any{}
		for _, a := range m.Attachments {
			out = append(out, map[string]any{"id": a.ID, "name": a.Name, "contentType": a.MimeType, "size": len(a.Data), "isInline": a.Inline})
		}
		writeJSON(w, 200, map[string]any{"value": out})
	case len(rest) == 1 && rest[0] == "attachments" && r.Method == http.MethodPost:
		data, err := base64.StdEncoding.DecodeString(fmt.Sprint(body["contentBytes"]))
		if err != nil {
			apiError(w, 400, "BadRequest", "contentBytes")
			return
		}
		g.n++
		a := Attachment{ID: fmt.Sprintf("AAMkA-%d", g.n), Name: fmt.Sprint(body["name"]), MimeType: fmt.Sprint(body["contentType"]), Data: data}
		m.Attachments = append(m.Attachments, a)
		g.bump(m)
		writeJSON(w, 201, map[string]any{"id": a.ID, "name": a.Name, "size": len(data)})
	case len(rest) == 2 && rest[0] == "attachments" && rest[1] == "createUploadSession":
		item, _ := body["AttachmentItem"].(map[string]any)
		size, _ := item["size"].(float64)
		g.n++
		key := strconv.Itoa(g.n)
		g.uploads[key] = &uploadSession{msg: m.ID, size: int(size),
			att: Attachment{Name: fmt.Sprint(item["name"]), MimeType: fmt.Sprint(item["contentType"])}}
		writeJSON(w, 201, map[string]any{"uploadUrl": g.Server.URL + "/upload/" + key})
	case len(rest) >= 2 && rest[0] == "attachments":
		i := slices.IndexFunc(m.Attachments, func(a Attachment) bool { return a.ID == rest[1] })
		if i < 0 {
			apiError(w, 404, "ErrorItemNotFound", "attachment not found")
			return
		}
		a := m.Attachments[i]
		switch {
		case len(rest) == 3 && rest[2] == "$value":
			w.Header().Set("Content-Type", a.MimeType)
			_, _ = w.Write(a.Data)
		case r.Method == http.MethodDelete:
			m.Attachments = slices.Delete(m.Attachments, i, i+1)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(w, 200, map[string]any{"@odata.type": "#microsoft.graph.fileAttachment", "id": a.ID, "name": a.Name,
				"contentType": a.MimeType, "size": len(a.Data), "isInline": a.Inline, "contentBytes": base64.StdEncoding.EncodeToString(a.Data)})
		}
	default:
		apiError(w, 400, "BadRequest", "fake graph: unsupported message operation "+r.Method+" "+strings.Join(rest, "/"))
	}
}

func (g *Graph) serveUpload(w http.ResponseWriter, r *http.Request, key string) {
	if r.Header.Get("Authorization") != "" {
		apiError(w, 401, "InvalidAuthenticationToken", "upload URLs must not carry a bearer token")
		return
	}
	data, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.uploads[key]
	if !ok {
		apiError(w, 404, "ItemNotFound", "upload session not found")
		return
	}
	var start, end, total int
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != s.buf.Len() || end-start+1 != len(data) {
		apiError(w, 400, "InvalidRange", "bad Content-Range")
		return
	}
	s.buf.Write(data)
	if s.buf.Len() < s.size {
		writeJSON(w, 200, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", s.buf.Len())}})
		return
	}
	delete(g.uploads, key)
	if m, ok := g.msgs[s.msg]; ok {
		g.n++
		s.att.ID = fmt.Sprintf("AAMkA-%d", g.n)
		s.att.Data = slices.Clone(s.buf.Bytes())
		m.Attachments = append(m.Attachments, s.att)
		g.bump(m)
	}
	w.WriteHeader(http.StatusCreated)
}
