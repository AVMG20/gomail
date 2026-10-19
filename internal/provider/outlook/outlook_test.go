package outlook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

type fakeGraph struct {
	mu        sync.Mutex
	url       string
	batches   [][]map[string]any
	patched   map[string]any
	sentDraft string
	throttle  bool
}

func msgJSON(id, folder string, read bool) map[string]any {
	return map[string]any{
		"id": id, "conversationId": "conv-" + id, "subject": "Subject " + id, "bodyPreview": "preview  text",
		"from":             map[string]any{"emailAddress": map[string]string{"name": "Ana", "address": "ana@contoso.com"}},
		"toRecipients":     []any{map[string]any{"emailAddress": map[string]string{"name": "Me", "address": "me@contoso.com"}}},
		"receivedDateTime": "2026-10-01T10:00:00Z", "isRead": read, "hasAttachments": true,
		"internetMessageId": "<" + id + "@contoso.com>", "parentFolderId": folder,
		"categories": []string{"Blue category"}, "flag": map[string]string{"flagStatus": "flagged"},
	}
}

func (f *fakeGraph) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if !strings.Contains(r.Header.Get("Prefer"), `IdType="ImmutableId"`) {
			t.Errorf("missing immutable id preference on %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		p := r.URL.Path
		switch {
		case p == "/$batch":
			var body struct {
				Requests []map[string]any `json:"requests"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.batches = append(f.batches, body.Requests)
			throttle := f.throttle
			f.throttle = false
			f.mu.Unlock()
			var resps []map[string]any
			for i, req := range body.Requests {
				u := req["url"].(string)
				resp := map[string]any{"id": req["id"], "status": 200, "body": map[string]any{}}
				switch {
				case throttle && i == 0:
					resp["status"] = 429
					resp["headers"] = map[string]string{"Retry-After": "1"}
				case strings.HasPrefix(u, "/me/mailFolders/"):
					name := strings.TrimSuffix(strings.TrimPrefix(u, "/me/mailFolders/"), "?$select=id")
					if name == "archive" {
						resp["status"] = 404
					} else {
						resp["body"] = map[string]string{"id": "id-" + name}
					}
				case strings.HasSuffix(u, "?$select=categories"):
					resp["body"] = map[string]any{"categories": []string{"Red category"}}
				}
				resps = append(resps, resp)
			}
			write(map[string]any{"responses": resps})
		case p == "/me/mailFolders":
			write(map[string]any{"value": []map[string]any{
				{"id": "id-inbox", "displayName": "Inbox", "totalItemCount": 20, "unreadItemCount": 4, "childFolderCount": 1},
				{"id": "id-outbox", "displayName": "Outbox"},
				{"id": "id-junkemail", "displayName": "Junk Email", "totalItemCount": 2},
			}})
		case p == "/me/mailFolders/id-inbox/childFolders":
			write(map[string]any{"value": []map[string]any{{"id": "id-proj", "displayName": "Projects", "totalItemCount": 5}}})
		case p == "/me/outlook/masterCategories":
			write(map[string]any{"value": []map[string]string{{"displayName": "Blue category", "color": "preset7"}}})
		case p == "/me/mailFolders/id-inbox/messages":
			write(map[string]any{"value": []map[string]string{{"receivedDateTime": "2026-10-02T00:00:00Z"}, {"receivedDateTime": "2026-09-01T00:00:00Z"}}})
		case p == "/me/mailFolders/id-inbox/messages/delta":
			if r.URL.Query().Get("$deltatoken") == "expired" {
				w.WriteHeader(410)
				write(map[string]any{"error": map[string]string{"code": "SyncStateNotFound", "message": "gone"}})
				return
			}
			if r.URL.Query().Get("$skiptoken") == "" && r.URL.Query().Get("$deltatoken") == "" {
				if !strings.HasPrefix(r.URL.Query().Get("$filter"), "receivedDateTime ge 2026-09-01T00:00:00Z") {
					t.Errorf("delta filter %q", r.URL.Query().Get("$filter"))
				}
				write(map[string]any{"value": []any{msgJSON("A", "id-inbox", false)},
					"@odata.nextLink": f.url + "/me/mailFolders/id-inbox/messages/delta?$skiptoken=2"})
				return
			}
			if r.URL.Query().Get("$skiptoken") == "2" {
				write(map[string]any{"value": []any{msgJSON("B", "", true), map[string]any{"id": "C", "@removed": map[string]string{"reason": "deleted"}}},
					"@odata.deltaLink": f.url + "/me/mailFolders/id-inbox/messages/delta?$deltatoken=next"})
				return
			}
			write(map[string]any{"value": []any{}, "@odata.deltaLink": f.url + "/me/mailFolders/id-inbox/messages/delta?$deltatoken=next2"})
		case strings.HasPrefix(p, "/me/mailFolders/") && strings.HasSuffix(p, "/messages"):
			write(map[string]any{"value": []any{}})
		case strings.HasPrefix(p, "/me/mailFolders/") && strings.HasSuffix(p, "/messages/delta"):
			write(map[string]any{"value": []any{}, "@odata.deltaLink": f.url + p + "?$deltatoken=x"})
		case p == "/me/messages/A" && r.Method == "GET":
			if !strings.Contains(r.Header.Get("Prefer"), `outlook.body-content-type="html"`) {
				t.Error("missing html preference")
			}
			write(map[string]any{"body": map[string]string{"contentType": "html", "content": `<p>Hello <img src="cid:pic"></p>`},
				"hasAttachments": true, "internetMessageId": "<A@contoso.com>",
				"internetMessageHeaders": []map[string]string{{"name": "References", "value": "<r1> <r2>"}}})
		case p == "/me/messages/A/attachments":
			write(map[string]any{"value": []map[string]any{
				{"id": "att1", "name": "pic.png", "contentType": "image/png", "size": 3, "isInline": true},
				{"id": "att2", "name": "report.xlsx", "contentType": "application/vnd.ms-excel", "size": 10},
			}})
		case p == "/me/messages/A/attachments/att1":
			write(map[string]any{"contentId": "<pic>", "contentBytes": "UE5H"})
		case p == "/me/messages/A/attachments/att2/$value":
			w.Write([]byte("XLSX"))
		case p == "/me/messages/A/createReply":
			write(map[string]string{"id": "draft1"})
		case p == "/me/messages/draft1" && r.Method == "PATCH":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.patched = body
			f.mu.Unlock()
			write(map[string]any{})
		case p == "/me/messages/draft1/attachments":
			write(map[string]any{})
		case p == "/me/messages/draft1/send":
			f.mu.Lock()
			f.sentDraft = "draft1"
			f.mu.Unlock()
			w.WriteHeader(202)
		case p == "/me":
			write(map[string]string{"displayName": "Me Myself", "userPrincipalName": "me@contoso.com"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}
}

func newTest(t *testing.T) (*Client, *fakeGraph) {
	f := &fakeGraph{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c, f
}

func TestFoldersAndSync(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()
	p, err := c.Profile(ctx)
	if err != nil || p.Email != "me@contoso.com" || p.Name != "Me Myself" {
		t.Fatal(p, err)
	}
	folders, labels, err := c.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range folders {
		names = append(names, f.Name+":"+f.Kind)
	}
	if strings.Join(names, ",") != "Inbox:inbox,Inbox/Projects:custom,Junk Email:spam,Flagged:starred" {
		t.Fatalf("folders %v", names)
	}
	if len(labels) != 1 || labels[0].Color != "#0078d4" {
		t.Fatalf("labels %+v", labels)
	}
	inbox := []mail.Folder{folders[0]}
	res, err := c.Sync(ctx, provider.SyncRequest{State: map[string]string{}, Folders: inbox, Limits: provider.Limits{Inbox: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Initial || len(res.Upserts) != 2 || strings.Join(res.Removed["id-inbox"], ",") != "C" {
		t.Fatalf("res %+v", res)
	}
	a := res.Upserts[0]
	if a.RemoteID != "A" || !a.Unread || !a.Starred || a.ThreadID != "conv-A" || a.Snippet != "preview text" ||
		a.From.Email != "ana@contoso.com" || strings.Join(a.Labels, ",") != "Blue category" || a.FolderRemoteIDs[0] != "id-inbox" {
		t.Fatalf("A %+v", a)
	}
	if res.Upserts[1].FolderRemoteIDs[0] != "id-inbox" {
		t.Fatal("missing parent folder fallback")
	}
	link := res.State["delta:id-inbox"]
	if !strings.Contains(link, "deltatoken=next") {
		t.Fatalf("delta link %q", link)
	}
	res, err = c.Sync(ctx, provider.SyncRequest{State: map[string]string{"delta:id-inbox": link, "delta:gone": "x"}, Folders: inbox})
	if err != nil || res.Initial || len(res.Upserts) != 0 || res.State["delta:gone"] != "" {
		t.Fatalf("incremental %v %+v", err, res)
	}
	if _, ok := res.State["delta:gone"]; !ok {
		t.Fatal("stale delta link not cleared")
	}
	// Expired delta token: resync the folder.
	expired := strings.Replace(link, "deltatoken=next", "deltatoken=expired", 1)
	res, err = c.Sync(ctx, provider.SyncRequest{State: map[string]string{"delta:id-inbox": expired}, Folders: inbox, Limits: provider.Limits{Inbox: 2}})
	if err != nil || !res.Initial || len(res.Upserts) != 2 {
		t.Fatalf("expired %v %+v", err, res)
	}
}

func TestBodyActionsSend(t *testing.T) {
	c, f := newTest(t)
	ctx := context.Background()
	b, err := c.Body(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	if b.Text != "Hello" || b.References != "<r1> <r2>" || len(b.Attachments) != 2 || b.Attachments[0].ContentID != "pic" || string(b.Attachments[0].Data) != "PNG" {
		t.Fatalf("body %+v", b)
	}
	data, err := c.Attachment(ctx, "A", "att2")
	if err != nil || string(data) != "XLSX" {
		t.Fatal(data, err)
	}
	ids := make([]string, 25)
	for i := range ids {
		ids[i] = "id" + string(rune('a'+i))
	}
	f.throttle = true
	if err := c.SetRead(ctx, ids, true); err != nil {
		t.Fatal(err)
	}
	// 25 ids -> 2 batches, plus one retry of the throttled request.
	if n := len(f.batches); n != 3 || len(f.batches[0]) != 20 || f.batches[2][0]["method"] != "PATCH" {
		t.Fatalf("batches %d", n)
	}
	if err := c.SetLabel(ctx, []string{"A"}, "Blue category", true); err != nil {
		t.Fatal(err)
	}
	last := f.batches[len(f.batches)-1][0]
	cats := last["body"].(map[string]any)["categories"].([]any)
	if len(cats) != 2 || cats[1] != "Blue category" {
		t.Fatalf("categories patch %+v", last)
	}
	if got := c.PlanMove([]string{"id-inbox"}, mail.Folder{RemoteID: "id-deleteditems", Kind: models.FolderTrash}); got[0] != "id-deleteditems" {
		t.Fatal(got)
	}
	err = c.Send(ctx, mail.Outgoing{To: []models.Address{{Email: "ana@contoso.com"}}, Subject: "Re: Subject A", HTML: "<p>Thanks</p>",
		Mode: "reply", Original: &mail.Message{RemoteID: "A"}, Attachments: []mail.OutgoingAttachment{{Filename: "a.txt", Data: []byte("hi")}}})
	if err != nil {
		t.Fatal(err)
	}
	if f.sentDraft != "draft1" || f.patched["body"].(map[string]any)["content"] != "<p>Thanks</p>" {
		t.Fatalf("send %+v", f.patched)
	}
}
