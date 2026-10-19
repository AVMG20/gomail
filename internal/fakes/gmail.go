package fakes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Attachment is a file attached to a fake message.
type Attachment struct {
	ID       string
	Name     string
	MimeType string
	Data     []byte
	Inline   bool
}

// GmailMessage is a message in the fake Gmail mailbox.
type GmailMessage struct {
	ID          string
	ThreadID    string
	LabelIDs    []string // INBOX, UNREAD, STARRED, SENT, DRAFT, SPAM, TRASH, user label ids
	From        string   // RFC 5322 address, e.g. "Ann <ann@example.com>"
	To          string
	Subject     string
	HTML        string
	Date        time.Time
	MessageID   string
	Attachments []Attachment
	Raw         []byte // raw MIME for messages sent or drafted by the app
}

type gmailLabel struct {
	ID, Name, Type string
}

type gmailHistory struct {
	id               int
	added, changed   []string
	deleted, removed []string
}

// Gmail is a stateful fake of the Gmail REST API (users/me only): profile,
// labels, messages (list/get/attachments/batchModify/trash/send), history,
// drafts and the multipart upload endpoint.
type Gmail struct {
	Server *httptest.Server
	Email  string
	Auth   func(token string) bool

	mu        sync.Mutex
	msgs      map[string]*GmailMessage
	labels    []gmailLabel
	history   []gmailHistory
	historyID int
	drafts    map[string]string // draft id -> message id
	sent      []*GmailMessage
	seq       int
	Requests  int
	Uploads   int
}

// NewGmail starts a fake mailbox for email. auth validates bearer tokens.
func NewGmail(email string, auth func(string) bool) *Gmail {
	g := &Gmail{Email: email, Auth: auth, msgs: map[string]*GmailMessage{}, drafts: map[string]string{}, historyID: 1000}
	for _, id := range []string{"INBOX", "SENT", "DRAFT", "SPAM", "TRASH", "STARRED", "UNREAD", "IMPORTANT"} {
		g.labels = append(g.labels, gmailLabel{ID: id, Name: id, Type: "system"})
	}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

// Close stops the server.
func (g *Gmail) Close() { g.Server.Close() }

// URL is the API base URL (pass as gmail.New's base).
func (g *Gmail) URL() string { return g.Server.URL }

func (g *Gmail) nextID(prefix string) string {
	g.seq++
	return fmt.Sprintf("%s%04d", prefix, g.seq)
}

func (g *Gmail) record(h gmailHistory) {
	g.historyID++
	h.id = g.historyID
	g.history = append(g.history, h)
}

// AddLabel creates a user label and returns its id.
func (g *Gmail) AddLabel(name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addLabel(name)
}

func (g *Gmail) addLabel(name string) string {
	for _, l := range g.labels {
		if strings.EqualFold(l.Name, name) {
			return l.ID
		}
	}
	id := g.nextID("Label_")
	g.labels = append(g.labels, gmailLabel{ID: id, Name: name, Type: "user"})
	return id
}

// Deliver adds a message (as new mail) and returns its id.
func (g *Gmail) Deliver(m GmailMessage) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.add(&m)
}

func (g *Gmail) add(m *GmailMessage) string {
	if m.ID == "" {
		m.ID = g.nextID("m")
	}
	if m.ThreadID == "" {
		m.ThreadID = "t" + m.ID
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	if m.MessageID == "" {
		m.MessageID = "<" + m.ID + "@fake.gmail>"
	}
	for i := range m.Attachments {
		if m.Attachments[i].ID == "" {
			m.Attachments[i].ID = strconv.Itoa(i + 1)
		}
	}
	g.msgs[m.ID] = m
	g.record(gmailHistory{added: []string{m.ID}})
	return m.ID
}

// Message returns a copy of a message.
func (g *Gmail) Message(id string) (GmailMessage, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.msgs[id]
	if !ok {
		return GmailMessage{}, false
	}
	c := *m
	c.LabelIDs = slices.Clone(m.LabelIDs)
	return c, true
}

// LabelID returns the id of a label by name ("" if missing).
func (g *Gmail) LabelID(name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, l := range g.labels {
		if strings.EqualFold(l.Name, name) {
			return l.ID
		}
	}
	return ""
}

// Sent returns the messages sent through the API (raw MIME in Raw).
func (g *Gmail) Sent() []GmailMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]GmailMessage, len(g.sent))
	for i, m := range g.sent {
		out[i] = *m
	}
	return out
}

// Drafts returns the number of drafts.
func (g *Gmail) Drafts() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.drafts)
}

func (g *Gmail) has(m *GmailMessage, label string) bool { return slices.Contains(m.LabelIDs, label) }

func (g *Gmail) modify(m *GmailMessage, add, remove []string) bool {
	changed := false
	for _, l := range remove {
		if i := slices.Index(m.LabelIDs, l); i >= 0 {
			m.LabelIDs = slices.Delete(m.LabelIDs, i, i+1)
			changed = true
		}
	}
	for _, l := range add {
		if !g.has(m, l) {
			m.LabelIDs = append(m.LabelIDs, l)
			changed = true
		}
	}
	return changed
}

func (g *Gmail) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.Requests++
	g.mu.Unlock()
	if g.Auth != nil && !g.Auth(bearer(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "Request had invalid authentication credentials.", "status": "UNAUTHENTICATED"}})
		return
	}
	if p, ok := strings.CutPrefix(r.URL.Path, "/upload/gmail/v1/users/me/"); ok {
		g.serveUpload(w, r, p)
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/gmail/v1/users/me/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			_ = json.Unmarshal(data, &body)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.route(w, r, p, body, nil)
}

func (g *Gmail) serveUpload(w http.ResponseWriter, r *http.Request, p string) {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/related" || r.URL.Query().Get("uploadType") != "multipart" {
		apiError(w, 400, "badRequest", "bad upload")
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	metaPart, err := mr.NextPart()
	if err != nil {
		apiError(w, 400, "badRequest", "no metadata")
		return
	}
	var meta map[string]any
	_ = json.NewDecoder(metaPart).Decode(&meta)
	rawPart, err := mr.NextPart()
	if err != nil {
		apiError(w, 400, "badRequest", "no message")
		return
	}
	raw, _ := io.ReadAll(rawPart)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Uploads++
	g.route(w, r, p, meta, raw)
}

// route handles one API call; raw is the uploaded message for the upload
// endpoint (else the base64 "raw" field of the body is used).
func (g *Gmail) route(w http.ResponseWriter, r *http.Request, p string, body map[string]any, raw []byte) {
	q := r.URL.Query()
	seg := strings.Split(p, "/")
	switch {
	case p == "profile":
		writeJSON(w, 200, map[string]any{"emailAddress": g.Email, "messagesTotal": len(g.msgs), "historyId": strconv.Itoa(g.historyID)})
	case p == "labels" && r.Method == http.MethodGet:
		var out []map[string]any
		for _, l := range g.labels {
			out = append(out, map[string]any{"id": l.ID, "name": l.Name, "type": l.Type})
		}
		writeJSON(w, 200, map[string]any{"labels": out})
	case p == "labels" && r.Method == http.MethodPost:
		name, _ := body["name"].(string)
		id := g.addLabel(name)
		writeJSON(w, 200, map[string]any{"id": id, "name": name, "type": "user"})
	case len(seg) == 2 && seg[0] == "labels":
		total, unread := 0, 0
		for _, m := range g.msgs {
			if g.has(m, seg[1]) {
				total++
				if g.has(m, "UNREAD") {
					unread++
				}
			}
		}
		writeJSON(w, 200, map[string]any{"id": seg[1], "name": seg[1], "messagesTotal": total, "messagesUnread": unread})
	case p == "messages" && r.Method == http.MethodGet:
		g.list(w, q)
	case p == "messages/batchModify":
		ids := strs(body["ids"])
		add, remove := strs(body["addLabelIds"]), strs(body["removeLabelIds"])
		var changed []string
		for _, id := range ids {
			if m, ok := g.msgs[id]; ok && g.modify(m, add, remove) {
				changed = append(changed, id)
			}
		}
		if len(changed) > 0 {
			g.record(gmailHistory{changed: changed})
		}
		w.WriteHeader(http.StatusNoContent)
	case p == "messages/send" && r.Method == http.MethodPost:
		m, err := g.fromRaw(body, raw)
		if err != nil {
			apiError(w, 400, "invalidArgument", err.Error())
			return
		}
		m.LabelIDs = []string{"SENT"}
		g.add(m)
		g.sent = append(g.sent, m)
		writeJSON(w, 200, map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.LabelIDs})
	case len(seg) == 2 && seg[0] == "messages" && r.Method == http.MethodGet:
		m, ok := g.msgs[seg[1]]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		writeJSON(w, 200, g.render(m, q.Get("format") == "full"))
	case len(seg) == 3 && seg[0] == "messages" && seg[2] == "trash":
		m, ok := g.msgs[seg[1]]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		g.modify(m, []string{"TRASH"}, []string{"INBOX", "SPAM"})
		g.record(gmailHistory{changed: []string{m.ID}})
		writeJSON(w, 200, map[string]any{"id": m.ID})
	case len(seg) == 4 && seg[0] == "messages" && seg[2] == "attachments":
		m, ok := g.msgs[seg[1]]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		for _, a := range m.Attachments {
			if "att-"+m.ID+"-"+a.ID == seg[3] {
				writeJSON(w, 200, map[string]any{"size": len(a.Data), "data": base64.URLEncoding.EncodeToString(a.Data)})
				return
			}
		}
		apiError(w, 404, "notFound", "attachment not found")
	case p == "history":
		g.historyList(w, q)
	case p == "drafts" && r.Method == http.MethodGet:
		var out []map[string]any
		for id, mid := range g.drafts {
			out = append(out, map[string]any{"id": id, "message": map[string]string{"id": mid}})
		}
		writeJSON(w, 200, map[string]any{"drafts": out})
	case p == "drafts" && r.Method == http.MethodPost:
		m, err := g.fromRaw(msgField(body), raw)
		if err != nil {
			apiError(w, 400, "invalidArgument", err.Error())
			return
		}
		m.LabelIDs = []string{"DRAFT"}
		g.add(m)
		id := g.nextID("r")
		g.drafts[id] = m.ID
		writeJSON(w, 200, map[string]any{"id": id, "message": map[string]any{"id": m.ID, "threadId": m.ThreadID}})
	case p == "drafts/send":
		id, _ := body["id"].(string)
		mid, ok := g.drafts[id]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		delete(g.drafts, id)
		m := g.msgs[mid]
		g.modify(m, []string{"SENT"}, []string{"DRAFT"})
		g.record(gmailHistory{changed: []string{mid}})
		g.sent = append(g.sent, m)
		writeJSON(w, 200, map[string]any{"id": mid, "threadId": m.ThreadID})
	case len(seg) == 2 && seg[0] == "drafts" && r.Method == http.MethodPut:
		old, ok := g.drafts[seg[1]]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		m, err := g.fromRaw(msgField(body), raw)
		if err != nil {
			apiError(w, 400, "invalidArgument", err.Error())
			return
		}
		// Gmail replaces the draft's message with a new one.
		delete(g.msgs, old)
		g.record(gmailHistory{deleted: []string{old}})
		m.LabelIDs = []string{"DRAFT"}
		g.add(m)
		g.drafts[seg[1]] = m.ID
		writeJSON(w, 200, map[string]any{"id": seg[1], "message": map[string]any{"id": m.ID, "threadId": m.ThreadID}})
	case len(seg) == 2 && seg[0] == "drafts" && r.Method == http.MethodDelete:
		mid, ok := g.drafts[seg[1]]
		if !ok {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		delete(g.drafts, seg[1])
		delete(g.msgs, mid)
		g.record(gmailHistory{deleted: []string{mid}})
		w.WriteHeader(http.StatusNoContent)
	default:
		apiError(w, 404, "notFound", "fake gmail: no route for "+r.Method+" "+p)
	}
}

func msgField(body map[string]any) map[string]any {
	m, _ := body["message"].(map[string]any)
	return m
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// fromRaw parses a message resource ({raw, threadId}) or an uploaded
// message into a GmailMessage.
func (g *Gmail) fromRaw(msg map[string]any, raw []byte) (*GmailMessage, error) {
	if raw == nil {
		s, _ := msg["raw"].(string)
		var err error
		if raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")); err != nil {
			return nil, fmt.Errorf("raw is not base64url: %w", err)
		}
	}
	pm, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid MIME: %w", err)
	}
	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(pm.Header.Get("Subject"))
	m := &GmailMessage{From: pm.Header.Get("From"), To: pm.Header.Get("To"), Subject: subject,
		MessageID: pm.Header.Get("Message-ID"), Raw: raw, Date: time.Now()}
	if t, _ := msg["threadId"].(string); t != "" {
		m.ThreadID = t
	}
	return m, nil
}

func (g *Gmail) list(w http.ResponseWriter, q map[string][]string) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	label, query := get("labelIds"), get("q")
	limit, _ := strconv.Atoi(get("maxResults"))
	if limit <= 0 {
		limit = 100
	}
	var list []*GmailMessage
	for _, m := range g.msgs {
		if label != "" && !g.has(m, label) {
			continue
		}
		if !g.matches(m, query) {
			continue
		}
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Date.After(list[j].Date) })
	if len(list) > limit {
		list = list[:limit]
	}
	out := []map[string]string{}
	for _, m := range list {
		out = append(out, map[string]string{"id": m.ID, "threadId": m.ThreadID})
	}
	writeJSON(w, 200, map[string]any{"messages": out, "resultSizeEstimate": len(out)})
}

// matches supports the subset of Gmail search the app sends: -in:x, in:x,
// before:/after: (unix seconds or yyyy/mm/dd) and plain words.
func (g *Gmail) matches(m *GmailMessage, query string) bool {
	for _, t := range strings.Fields(query) {
		neg := strings.HasPrefix(t, "-")
		t = strings.TrimPrefix(t, "-")
		ok := true
		switch {
		case strings.HasPrefix(t, "in:"):
			ok = g.has(m, strings.ToUpper(strings.TrimPrefix(t, "in:")))
		case strings.HasPrefix(t, "before:"), strings.HasPrefix(t, "after:"):
			k, v, _ := strings.Cut(t, ":")
			var at time.Time
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				at = time.Unix(n, 0)
			} else if d, err := time.Parse("2006/01/02", v); err == nil {
				at = d
			}
			if k == "before" {
				ok = m.Date.Before(at)
			} else {
				ok = !m.Date.Before(at)
			}
		case strings.Contains(t, ":"):
			// other operators: ignored
		default:
			hay := strings.ToLower(m.Subject + " " + m.From + " " + m.HTML)
			ok = strings.Contains(hay, strings.ToLower(strings.Trim(t, `"()`)))
		}
		if ok == neg {
			return false
		}
	}
	return true
}

func (g *Gmail) historyList(w http.ResponseWriter, q map[string][]string) {
	start, _ := strconv.Atoi(first(q["startHistoryId"]))
	if start < 1000 {
		apiError(w, 404, "notFound", "Requested entity was not found.")
		return
	}
	var out []map[string]any
	ref := func(ids []string) []map[string]any {
		var list []map[string]any
		for _, id := range ids {
			list = append(list, map[string]any{"message": map[string]string{"id": id}})
		}
		return list
	}
	for _, h := range g.history {
		if h.id <= start {
			continue
		}
		out = append(out, map[string]any{"id": strconv.Itoa(h.id), "messagesAdded": ref(h.added),
			"labelsAdded": ref(h.changed), "messagesDeleted": ref(h.deleted)})
	}
	writeJSON(w, 200, map[string]any{"history": out, "historyId": strconv.Itoa(g.historyID)})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func b64url(b []byte) string { return base64.URLEncoding.EncodeToString(b) }

// render returns the API representation (metadata or full).
func (g *Gmail) render(m *GmailMessage, full bool) map[string]any {
	headers := []map[string]string{
		{"name": "From", "value": m.From}, {"name": "To", "value": m.To},
		{"name": "Subject", "value": mime.QEncoding.Encode("utf-8", m.Subject)},
		{"name": "Date", "value": m.Date.Format(time.RFC1123Z)}, {"name": "Message-ID", "value": m.MessageID},
	}
	ct := "text/html; charset=utf-8"
	if len(m.Attachments) > 0 {
		ct = "multipart/mixed; boundary=x"
	}
	headers = append(headers, map[string]string{"name": "Content-Type", "value": ct})
	payload := map[string]any{"partId": "", "mimeType": strings.SplitN(ct, ";", 2)[0], "headers": headers}
	if full {
		html := m.HTML
		if html == "" && m.Raw != nil {
			if pm, err := mail.ReadMessage(bytes.NewReader(m.Raw)); err == nil {
				data, _ := io.ReadAll(pm.Body)
				html = "<pre>" + string(data) + "</pre>"
			}
		}
		htmlPart := map[string]any{"partId": "0", "mimeType": "text/html",
			"headers": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
			"body":    map[string]any{"size": len(html), "data": b64url([]byte(html))}}
		if len(m.Attachments) == 0 {
			payload["body"] = htmlPart["body"]
		} else {
			parts := []any{htmlPart}
			for _, a := range m.Attachments {
				disp := "attachment"
				if a.Inline {
					disp = "inline"
				}
				parts = append(parts, map[string]any{"partId": a.ID, "mimeType": a.MimeType, "filename": a.Name,
					"headers": []map[string]string{{"name": "Content-Disposition", "value": disp + `; filename="` + a.Name + `"`}},
					"body":    map[string]any{"attachmentId": "att-" + m.ID + "-" + a.ID, "size": len(a.Data)}})
			}
			payload["parts"] = parts
		}
	}
	snippet := m.Subject
	return map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.LabelIDs, "snippet": snippet,
		"internalDate": strconv.FormatInt(m.Date.UnixMilli(), 10), "payload": payload}
}
