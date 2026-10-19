package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

type fakeGmail struct {
	mu        sync.Mutex
	modify    []map[string]any
	sent      map[string]string
	trashed   []string
	throttled int32
}

func (f *fakeGmail) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/")
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case p == "profile":
			write(map[string]any{"emailAddress": "me@gmail.com", "messagesTotal": 1234, "historyId": "100"})
		case p == "labels" && r.Method == "GET":
			write(map[string]any{"labels": []map[string]any{
				{"id": "INBOX", "name": "INBOX", "type": "system"}, {"id": "SPAM", "name": "SPAM", "type": "system"},
				{"id": "CATEGORY_PROMOTIONS", "name": "CATEGORY_PROMOTIONS", "type": "system"},
				{"id": "Label_1", "name": "Work/Clients", "type": "user", "color": map[string]string{"backgroundColor": "#16a766"}},
			}})
		case p == "labels" && r.Method == "POST":
			write(map[string]any{"id": "Label_2", "name": "New"})
		case strings.HasPrefix(p, "labels/"):
			write(map[string]any{"id": strings.TrimPrefix(p, "labels/"), "messagesTotal": 10, "messagesUnread": 3})
		case p == "messages" && q.Get("labelIds") == "INBOX":
			write(map[string]any{"messages": []map[string]string{{"id": "m1"}, {"id": "m2"}}})
		case p == "messages":
			write(map[string]any{})
		case p == "messages/m1" && q.Get("format") == "metadata":
			write(map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX", "UNREAD", "Label_1", "CATEGORY_PROMOTIONS", "IMPORTANT"},
				"snippet": "Hello &amp; welcome", "internalDate": "1790000000000",
				"payload": map[string]any{"mimeType": "multipart/mixed", "headers": []map[string]string{
					{"name": "From", "value": "=?utf-8?q?J=C3=BCrgen?= <j@example.de>"}, {"name": "To", "value": "me@gmail.com"},
					{"name": "Subject", "value": "=?utf-8?b?R3LDvMOfZQ==?="}, {"name": "Message-ID", "value": "<abc@example.de>"},
					{"name": "References", "value": "<root@x>"}, {"name": "Content-Type", "value": "multipart/mixed; boundary=x"},
				}}})
		case p == "messages/m2" && q.Get("format") == "metadata":
			if atomic.AddInt32(&f.throttled, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			write(map[string]any{"id": "m2", "threadId": "t2", "labelIds": []string{"SPAM", "STARRED"}, "internalDate": "1790000001000",
				"payload": map[string]any{"mimeType": "text/plain", "headers": []map[string]string{{"name": "From", "value": "spam@x.com"}}}})
		case p == "messages/m3":
			w.WriteHeader(404)
			write(map[string]any{"error": map[string]any{"code": 404, "message": "Not Found"}})
		case p == "history":
			if q.Get("startHistoryId") == "1" {
				w.WriteHeader(404)
				return
			}
			write(map[string]any{"historyId": "150", "history": []map[string]any{
				{"messagesAdded": []map[string]any{{"message": map[string]string{"id": "m3"}}}},
				{"labelsRemoved": []map[string]any{{"message": map[string]string{"id": "m1"}}}},
				{"messagesDeleted": []map[string]any{{"message": map[string]string{"id": "m9"}}}},
			}})
		case p == "messages/m1" && q.Get("format") == "full":
			write(map[string]any{"id": "m1", "payload": map[string]any{"partId": "", "mimeType": "multipart/mixed",
				"headers": []map[string]string{{"name": "Message-ID", "value": "<abc@example.de>"}, {"name": "Cc", "value": "Zoë <z@x.com>"}},
				"parts": []map[string]any{
					{"partId": "0", "mimeType": "multipart/related", "parts": []map[string]any{
						{"partId": "0.0", "mimeType": "multipart/alternative", "parts": []map[string]any{
							{"partId": "0.0.0", "mimeType": "text/plain", "headers": []map[string]string{{"name": "Content-Type", "value": "text/plain; charset=iso-8859-1"}},
								"body": map[string]any{"data": base64.RawURLEncoding.EncodeToString([]byte("Gr\xfc\xdfe"))}},
							{"partId": "0.0.1", "mimeType": "text/html", "headers": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
								"body": map[string]any{"data": b64(`<p>Hi <img src="cid:img1"></p>`)}},
						}},
						{"partId": "0.1", "mimeType": "image/png", "filename": "logo.png", "headers": []map[string]string{{"name": "Content-ID", "value": "<img1>"}, {"name": "Content-Disposition", "value": "inline"}},
							"body": map[string]any{"attachmentId": "ATT-inline", "size": 4}},
					}},
					{"partId": "1", "mimeType": "application/pdf", "filename": "doc.pdf", "headers": []map[string]string{{"name": "Content-Disposition", "value": "attachment; filename=doc.pdf"}},
						"body": map[string]any{"attachmentId": "ATT-pdf", "size": 3}},
				}}})
		case p == "messages/m1/attachments/ATT-inline":
			write(map[string]any{"data": b64("PNG!")})
		case p == "messages/m1/attachments/ATT-pdf":
			write(map[string]any{"data": b64("PDF")})
		case p == "messages/batchModify":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.modify = append(f.modify, body)
			f.mu.Unlock()
		case strings.HasSuffix(p, "/trash"):
			f.mu.Lock()
			f.trashed = append(f.trashed, strings.TrimSuffix(strings.TrimPrefix(p, "messages/"), "/trash"))
			f.mu.Unlock()
			write(map[string]any{})
		case p == "messages/send":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.sent = body
			f.mu.Unlock()
			write(map[string]any{"id": "sent1"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}
}

func newTest(t *testing.T) (*Client, *fakeGmail) {
	f := &fakeGmail{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c, f
}

func TestFoldersAndInitialSync(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()
	folders, labels, err := c.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]mail.Folder{}
	for _, f := range folders {
		kinds[f.Kind] = f
	}
	if kinds[models.FolderInbox].Unread != 3 || kinds[models.FolderArchive].Total != 1234 || kinds[models.FolderArchive].RemoteID != AllMail {
		t.Fatalf("folders %+v", folders)
	}
	if len(labels) != 1 || labels[0].Name != "Work/Clients" || labels[0].Color != "#16a766" {
		t.Fatalf("labels %+v", labels)
	}
	res, err := c.Sync(ctx, provider.SyncRequest{State: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Initial || res.State["historyId"] != "100" || len(res.Upserts) != 2 {
		t.Fatalf("result %+v", res)
	}
	var m1, m2 mail.Message
	for _, m := range res.Upserts {
		if m.RemoteID == "m1" {
			m1 = m
		} else {
			m2 = m
		}
	}
	if m1.Subject != "Grüße" || m1.From.Name != "Jürgen" || !m1.Unread || m1.Starred || !m1.HasAttachments || m1.Snippet != "Hello & welcome" {
		t.Fatalf("m1 %+v", m1)
	}
	if strings.Join(m1.FolderRemoteIDs, ",") != "INBOX,ALL_MAIL" || strings.Join(m1.Labels, ",") != "Work/Clients" {
		t.Fatalf("m1 folders %v labels %v", m1.FolderRemoteIDs, m1.Labels)
	}
	if m1.MessageID != "<abc@example.de>" || m1.References != "<root@x>" || m1.Date.UnixMilli() != 1790000000000 {
		t.Fatalf("m1 headers %+v", m1)
	}
	if strings.Join(m2.FolderRemoteIDs, ",") != "SPAM" || !m2.Starred || m2.Unread {
		t.Fatalf("m2 %+v", m2)
	}
}

func TestIncrementalSync(t *testing.T) {
	c, _ := newTest(t)
	res, err := c.Sync(context.Background(), provider.SyncRequest{State: map[string]string{"historyId": "120"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Initial || res.State["historyId"] != "150" {
		t.Fatalf("%+v", res)
	}
	if len(res.Upserts) != 1 || res.Upserts[0].RemoteID != "m1" {
		t.Fatalf("upserts %+v", res.Upserts)
	}
	if strings.Join(res.Deleted, ",") != "m3,m9" {
		t.Fatalf("deleted %v", res.Deleted)
	}
	// Expired history id falls back to a full sync.
	res, err = c.Sync(context.Background(), provider.SyncRequest{State: map[string]string{"historyId": "1"}})
	if err != nil || !res.Initial {
		t.Fatalf("fallback %v %+v", err, res)
	}
}

func TestBodyAndAttachments(t *testing.T) {
	c, _ := newTest(t)
	b, err := c.Body(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Text != "Grüße" || !strings.Contains(b.HTML, "cid:img1") || len(b.Cc) != 1 || b.Cc[0].Name != "Zoë" {
		t.Fatalf("body %+v", b)
	}
	if len(b.Attachments) != 2 {
		t.Fatalf("attachments %+v", b.Attachments)
	}
	in := b.Attachments[0]
	if !in.Inline || in.ContentID != "img1" || string(in.Data) != "PNG!" || in.ID != "0.1" {
		t.Fatalf("inline %+v", in)
	}
	pdf := b.Attachments[1]
	if pdf.Inline || pdf.Filename != "doc.pdf" || pdf.ID != "1" {
		t.Fatalf("pdf %+v", pdf)
	}
	data, err := c.Attachment(context.Background(), "m1", "1")
	if err != nil || string(data) != "PDF" {
		t.Fatal(data, err)
	}
}

func TestActionsAndSend(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	if err := c.SetRead(ctx, []string{"a", "b"}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Move(ctx, []string{"a"}, mail.Folder{Kind: models.FolderArchive}); err != nil {
		t.Fatal(err)
	}
	if err := c.Move(ctx, []string{"a", "b"}, mail.Folder{Kind: models.FolderTrash}); err != nil {
		t.Fatal(err)
	}
	if len(f.modify) != 2 || f.modify[0]["removeLabelIds"].([]any)[0] != "UNREAD" || len(f.trashed) != 2 {
		t.Fatalf("modify %+v trashed %v", f.modify, f.trashed)
	}
	if got := c.PlanMove([]string{"INBOX", AllMail}, mail.Folder{Kind: models.FolderTrash}); strings.Join(got, ",") != "TRASH" {
		t.Fatalf("plan %v", got)
	}
	if got := c.PlanMove([]string{"SPAM"}, mail.Folder{Kind: models.FolderInbox}); strings.Join(got, ",") != "INBOX,ALL_MAIL" {
		t.Fatalf("plan %v", got)
	}
	c.Folders(ctx)
	if err := c.SetLabel(ctx, []string{"a"}, "work/clients", true); err != nil {
		t.Fatal(err)
	}
	if last := f.modify[len(f.modify)-1]; last["addLabelIds"].([]any)[0] != "Label_1" {
		t.Fatalf("label modify %+v", last)
	}
	orig := &mail.Message{RemoteID: "m1", ThreadID: "t1", MessageID: "<abc@example.de>", References: "<root@x>"}
	err := c.Send(ctx, mail.Outgoing{From: models.Address{Email: "me@gmail.com"}, To: []models.Address{{Email: "j@example.de"}},
		Subject: "Re: Grüße", HTML: "<p>Danke</p>", Mode: "reply", Original: orig})
	if err != nil {
		t.Fatal(err)
	}
	if f.sent["threadId"] != "t1" {
		t.Fatalf("thread %v", f.sent)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(f.sent["raw"])
	p, err := mailmime.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.InReplyTo != "<abc@example.de>" || p.References != "<root@x> <abc@example.de>" || p.Subject != "Re: Grüße" || !strings.Contains(p.HTML, "Danke") {
		t.Fatalf("sent %+v", p)
	}
}
