package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

// auditServer serves a tiny Gmail mailbox for the tests below.
func auditServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, p string) bool) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/")
		if !h(w, r, p) {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c
}

func writeJSON(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }

// Files sent with "Content-Disposition: inline" but no Content-ID (Apple
// Mail) are real attachments and must be forwarded; CID images are not.
func TestForwardKeepsInlineDispositionFiles(t *testing.T) {
	var mu sync.Mutex
	var raw []byte
	c := auditServer(t, func(w http.ResponseWriter, r *http.Request, p string) bool {
		switch {
		case p == "messages/f1" && r.URL.Query().Get("format") == "full":
			writeJSON(w, map[string]any{"id": "f1", "payload": map[string]any{"partId": "", "mimeType": "multipart/mixed", "parts": []map[string]any{
				{"partId": "0", "mimeType": "text/html", "body": map[string]any{"data": b64(`<p>see <img src="cid:logo"></p>`)}},
				{"partId": "1", "mimeType": "image/png", "filename": "logo.png", "headers": []map[string]string{
					{"name": "Content-ID", "value": "<logo>"}, {"name": "Content-Disposition", "value": "inline"}},
					"body": map[string]any{"attachmentId": "A-logo", "size": 3}},
				{"partId": "2", "mimeType": "application/pdf", "filename": "invoice.pdf", "headers": []map[string]string{
					{"name": "Content-Disposition", "value": `inline; filename="invoice.pdf"`}},
					"body": map[string]any{"attachmentId": "A-pdf", "size": 3}},
			}}})
		case strings.HasPrefix(p, "messages/f1/attachments/"):
			writeJSON(w, map[string]any{"data": b64("PDF")})
		case p == "messages/send":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			b, _ := base64.RawURLEncoding.DecodeString(body["raw"])
			mu.Lock()
			raw = b
			mu.Unlock()
			writeJSON(w, map[string]string{"id": "s1"})
		default:
			return false
		}
		return true
	})
	out := mail.Outgoing{From: models.Address{Email: "me@gmail.com"}, To: []models.Address{{Email: "x@y.z"}}, Subject: "Fwd", Text: "fyi",
		Mode: "forward", Original: &mail.Message{RemoteID: "f1"}}
	if err := c.Send(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(raw), "invoice.pdf") {
		t.Fatal("inline-disposition attachment dropped from the forward")
	}
	if strings.Contains(string(raw), "logo.png") {
		t.Fatal("embedded CID image forwarded as an attachment")
	}
}

// Large text parts that Gmail serves by attachment id are still the body.
func TestExternalizedBody(t *testing.T) {
	c := auditServer(t, func(w http.ResponseWriter, r *http.Request, p string) bool {
		switch {
		case p == "messages/b1":
			writeJSON(w, map[string]any{"id": "b1", "payload": map[string]any{"partId": "", "mimeType": "text/html",
				"body": map[string]any{"attachmentId": "BIG", "size": 5000}}})
		case p == "messages/b1/attachments/BIG":
			writeJSON(w, map[string]any{"data": b64("<p>big body</p>")})
		default:
			return false
		}
		return true
	})
	b, err := c.Body(context.Background(), "b1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.HTML, "big body") {
		t.Fatalf("body %q", b.HTML)
	}
}

// A full sync reports from which date each folder was listed completely,
// and All Mail older-mail pages include Inbox mail.
func TestInitialSyncCompletenessAndAllMailOlder(t *testing.T) {
	var mu sync.Mutex
	var olderQ string
	date := map[string]int64{"i1": 10, "i2": 5, "a1": 3}
	c := auditServer(t, func(w http.ResponseWriter, r *http.Request, p string) bool {
		q := r.URL.Query()
		switch {
		case p == "labels":
			writeJSON(w, map[string]any{"labels": []any{}})
		case p == "profile":
			writeJSON(w, map[string]any{"historyId": "7"})
		case p == "messages" && strings.Contains(q.Get("q"), "before:"):
			mu.Lock()
			olderQ = q.Get("q")
			mu.Unlock()
			writeJSON(w, map[string]any{})
		case p == "messages" && q.Get("labelIds") == "INBOX":
			writeJSON(w, map[string]any{"messages": []map[string]string{{"id": "i1"}, {"id": "i2"}}})
		case p == "messages" && q.Get("q") != "":
			writeJSON(w, map[string]any{"messages": []map[string]string{{"id": "a1"}}})
		case p == "messages":
			writeJSON(w, map[string]any{})
		case strings.HasPrefix(p, "messages/"):
			id := strings.TrimPrefix(p, "messages/")
			labels := []string{"INBOX"}
			if id == "a1" {
				labels = nil
			}
			writeJSON(w, map[string]any{"id": id, "threadId": id, "labelIds": labels,
				"internalDate": strconv.FormatInt(time.Unix(date[id]*3600, 0).UnixMilli(), 10), "payload": map[string]any{"mimeType": "text/plain"}})
		default:
			return false
		}
		return true
	})
	res, err := c.Sync(context.Background(), provider.SyncRequest{Limits: provider.Limits{Inbox: 2, Other: 2}})
	if err != nil {
		t.Fatal(err)
	}
	at := func(h int64) time.Time { return time.Unix(h*3600, 0).UTC() }
	if got := res.Complete["INBOX"]; !got.Equal(at(5)) {
		t.Fatalf("inbox complete from %v", got)
	}
	if got := res.Complete[AllMail]; !got.Equal(at(5)) {
		t.Fatalf("all mail complete from %v", got)
	}
	if got, ok := res.Complete["SENT"]; !ok || !got.IsZero() {
		t.Fatalf("sent (fully listed) %v %v", got, ok)
	}
	if _, ok := res.Complete["STARRED"]; ok {
		t.Fatal("starred is not a folder")
	}
	if _, err := c.ListOlder(context.Background(), mail.Folder{RemoteID: AllMail}, at(5), 10); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(olderQ, "-in:inbox") {
		t.Fatalf("older All Mail query skips the inbox: %q", olderQ)
	}
}

// When trashing fails part-way, the error names only the messages that
// were not trashed.
func TestTrashPartialFailure(t *testing.T) {
	c := auditServer(t, func(w http.ResponseWriter, r *http.Request, p string) bool {
		switch p {
		case "messages/ok1/trash", "messages/ok2/trash":
			writeJSON(w, map[string]any{})
		case "messages/bad/trash":
			time.Sleep(50 * time.Millisecond) // the others finish first
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": map[string]any{"code": 400, "message": "nope"}})
		default:
			return false
		}
		return true
	})
	err := c.Move(context.Background(), []string{"ok1", "bad", "ok2"}, mail.Folder{Kind: models.FolderTrash})
	var pe *provider.PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("want a partial error, got %v", err)
	}
	if len(pe.Failed) != 1 || pe.Failed[0] != "bad" {
		t.Fatalf("failed ids %v", pe.Failed)
	}
}
