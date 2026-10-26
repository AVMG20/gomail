package outlook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
)

// draftServer is a Graph draft whose attachments report their size with
// metadata overhead (like Graph does).
type draftServer struct {
	mu   sync.Mutex
	n    int
	atts []gAttachment
	url  string
	// pending upload session: name and bytes received
	upName string
	upSize int
	upGot  int
}

const sizeOverhead = 213

func newDraftServer(t *testing.T) (*Client, *draftServer) {
	d := &draftServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		p := r.URL.Path
		switch {
		case r.Method == http.MethodPatch && strings.HasSuffix(p, "/messages/D"):
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/attachments"):
			json.NewEncoder(w).Encode(map[string]any{"value": d.atts})
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/attachments"):
			var in struct {
				Name, ContentBytes, ContentID string
				IsInline                      bool
			}
			json.NewDecoder(r.Body).Decode(&in)
			data, _ := base64.StdEncoding.DecodeString(in.ContentBytes)
			d.n++
			a := gAttachment{ID: fmt.Sprint("att", d.n), Name: in.Name, Size: int64(len(data) + sizeOverhead), IsInline: in.IsInline, ContentID: in.ContentID}
			d.atts = append(d.atts, a)
			json.NewEncoder(w).Encode(a)
		case r.Method == http.MethodGet && strings.Contains(p, "/attachments/"):
			id := p[strings.LastIndex(p, "/")+1:]
			for _, a := range d.atts {
				if a.ID == id {
					json.NewEncoder(w).Encode(a)
					return
				}
			}
			w.WriteHeader(404)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/createUploadSession"):
			var in struct {
				AttachmentItem struct {
					Name string
					Size int
				}
			}
			json.NewDecoder(r.Body).Decode(&in)
			d.upName, d.upSize, d.upGot = in.AttachmentItem.Name, in.AttachmentItem.Size, 0
			json.NewEncoder(w).Encode(map[string]string{"uploadUrl": d.url + "/upload"})
		case r.Method == http.MethodPut && p == "/upload":
			b, _ := io.ReadAll(r.Body)
			d.upGot += len(b)
			if d.upGot >= d.upSize {
				d.n++
				d.atts = append(d.atts, gAttachment{ID: fmt.Sprint("att", d.n), Name: d.upName, Size: int64(d.upSize + sizeOverhead)})
				w.WriteHeader(201)
			} else {
				w.WriteHeader(200)
			}
			w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && strings.Contains(p, "/attachments/"):
			id := p[strings.LastIndex(p, "/")+1:]
			found := false
			for i, a := range d.atts {
				if a.ID == id {
					d.atts = append(d.atts[:i], d.atts[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				w.WriteHeader(400) // e.g. ".../attachments/" with no id
				w.Write([]byte(`{"error":{"code":"BadRequest","message":"bad id"}}`))
				return
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	d.url = srv.URL
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c, d
}

func (d *draftServer) list() []gAttachment {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]gAttachment(nil), d.atts...)
}

// Replacing a file with another of the same name (a smaller version) must
// leave the new one on the draft. The new file was paired with the old
// upload by name, then the old upload was removed and the new one never
// uploaded: Send went out without the attachment.
func TestReplacingAttachmentWithSameNameKeepsIt(t *testing.T) {
	c, d := newDraftServer(t)
	save := func(n int) {
		t.Helper()
		out := mail.Outgoing{Subject: "s", Text: "t"}
		if n > 0 {
			out.Attachments = []mail.OutgoingAttachment{{Filename: "invoice.pdf", Data: make([]byte, n)}}
		}
		if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
			t.Fatal(err)
		}
	}
	save(1000)
	save(900)
	got := d.list()
	if len(got) != 1 || got[0].Name != "invoice.pdf" || got[0].Size != 900+sizeOverhead {
		t.Fatalf("draft attachments %+v, want the 900-byte invoice.pdf", got)
	}
}

// A reopened draft (or one restored after a restart, so nothing was
// uploaded this session) has invoice.pdf; the user removes it and attaches
// a newer, smaller invoice.pdf. The new file was paired with the old one
// by name, never uploaded, and the old file was sent.
func TestReopenedDraftReplacedFileIsUploaded(t *testing.T) {
	c, d := newDraftServer(t)
	d.atts = []gAttachment{{ID: "old", Name: "invoice.pdf", Size: 1000 + sizeOverhead}}
	out := mail.Outgoing{Subject: "s", Text: "t", Mode: "new", AttachmentsComplete: true,
		Attachments: []mail.OutgoingAttachment{{Filename: "invoice.pdf", Data: make([]byte, 900)}}}
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	if got := d.list(); len(got) != 1 || got[0].ID == "old" || got[0].Size != 900+sizeOverhead {
		t.Fatalf("draft attachments %+v, want only the new 900-byte invoice.pdf", got)
	}
}

// Files loaded from a reopened draft come back with their attachment id:
// they stay on the draft as they are (no upload), next to a new file of
// the same name, and a removed one is deleted.
func TestReopenedDraftFilesPairedByID(t *testing.T) {
	c, d := newDraftServer(t)
	d.atts = []gAttachment{
		{ID: "a", Name: "invoice.pdf", Size: 1000 + sizeOverhead},
		{ID: "b", Name: "notes.txt", Size: 50 + sizeOverhead},
	}
	out := mail.Outgoing{Subject: "s", Text: "t", Mode: "new", AttachmentsComplete: true,
		Attachments: []mail.OutgoingAttachment{
			{ID: "a", Filename: "invoice.pdf", Data: make([]byte, 1000)},
			{Filename: "invoice.pdf", Data: make([]byte, 1000)},
		}}
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	got := d.list()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID == "b" || got[1].Name != "invoice.pdf" {
		t.Fatalf("draft attachments %+v, want a and one new invoice.pdf", got)
	}
	// Saving again uploads nothing.
	n := d.n
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	if d.n != n || len(d.list()) != 2 {
		t.Fatalf("second save re-uploaded: %+v", d.list())
	}
}

// Without ids or the whole file list (an older client), a file of the same
// name is taken for the draft's copy only when its size fits Graph's
// overhead: a clearly different file is uploaded.
func TestNameMatchWithoutIDIsBounded(t *testing.T) {
	c, d := newDraftServer(t)
	d.atts = []gAttachment{{ID: "old", Name: "report.pdf", Size: 50000 + sizeOverhead}}
	out := mail.Outgoing{Subject: "s", Text: "t", Mode: "new",
		Attachments: []mail.OutgoingAttachment{{Filename: "report.pdf", Data: make([]byte, 1000)}}}
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	if got := d.list(); len(got) != 2 {
		t.Fatalf("draft attachments %+v, want the old file kept and the new one uploaded", got)
	}
}

// A file added through an upload session (over 3 MB) can be removed again:
// its id was looked up by exact size, which Graph's overhead never
// matches, so an empty id was stored and the removal failed or did nothing.
func TestRemovingLargeAttachment(t *testing.T) {
	c, d := newDraftServer(t)
	big := []mail.OutgoingAttachment{{Filename: "big.bin", Data: make([]byte, smallAttachment+10)}}
	if _, err := c.SaveDraft(context.Background(), mail.Outgoing{Subject: "s", Text: "t", Attachments: big}, "D"); err != nil {
		t.Fatal(err)
	}
	if len(d.list()) != 1 {
		t.Fatalf("not uploaded: %+v", d.list())
	}
	if _, err := c.SaveDraft(context.Background(), mail.Outgoing{Subject: "s", Text: "t"}, "D"); err != nil {
		t.Fatal(err)
	}
	if got := d.list(); len(got) != 0 {
		t.Fatalf("removed file still on the draft: %+v", got)
	}
}

// Images in the body go up as inline attachments with their Content-ID, and
// later saves (also after a restart, when the client doesn't know its
// uploads) pair them by Content-ID instead of uploading them again.
func TestInlineImagesPairByContentID(t *testing.T) {
	c, d := newDraftServer(t)
	img := mail.OutgoingAttachment{Filename: "image.png", MimeType: "image/png", Data: []byte("PNG1"), ContentID: "img-1@gomail"}
	file := mail.OutgoingAttachment{Filename: "image.png", Data: []byte("PNG2")}
	save := func(c *Client, atts ...mail.OutgoingAttachment) {
		t.Helper()
		out := mail.Outgoing{Subject: "s", HTML: `<img src="cid:img-1@gomail">`, Attachments: atts, AttachmentsComplete: true}
		if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
			t.Fatal(err)
		}
	}
	save(c, img, file)
	save(c, img, file)
	got := d.list()
	if len(got) != 2 {
		t.Fatalf("draft attachments %+v", got)
	}
	inline := 0
	for _, a := range got {
		if a.IsInline {
			inline++
			if a.ContentID != "img-1@gomail" {
				t.Fatalf("content id %q", a.ContentID)
			}
		}
	}
	if inline != 1 {
		t.Fatalf("inline parts %d: %+v", inline, got)
	}
	restarted := New(httpx.StaticToken("tok"), d.url)
	restarted.SetSleep(func(context.Context, time.Duration) error { return nil })
	save(restarted, img, file)
	if got := d.list(); len(got) != 2 {
		t.Fatalf("after a restart: %+v", got)
	}
	save(restarted, file) // the image was deleted from the body
	if got := d.list(); len(got) != 1 || got[0].IsInline {
		t.Fatalf("removed image still on the draft: %+v", got)
	}
}

// A draft reopened in a later session (this client never uploaded its
// image) whose image was deleted, or one from Outlook on the web whose
// images came back under our Content-IDs: the images the body no longer
// shows are removed, not sent as extra parts.
func TestUnreferencedInlineImagesAreRemoved(t *testing.T) {
	c, d := newDraftServer(t)
	d.atts = []gAttachment{{ID: "web1", Name: "image001.png", Size: 300, IsInline: true, ContentID: "image001.png@01DA"}}
	img := mail.OutgoingAttachment{Filename: "image001.png", MimeType: "image/png", Data: []byte("PNG1"), ContentID: "img-1@gomail"}
	out := mail.Outgoing{Subject: "s", HTML: `<img src="cid:img-1@gomail">`, Attachments: []mail.OutgoingAttachment{img}, AttachmentsComplete: true}
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	got := d.list()
	if len(got) != 1 || got[0].ContentID != "img-1@gomail" {
		t.Fatalf("draft attachments %+v, want only the re-added image", got)
	}
	// Without the complete list (files still loading) nothing unknown goes.
	d.atts = append(d.atts, gAttachment{ID: "web2", Name: "image002.png", Size: 300, IsInline: true, ContentID: "image002.png@01DA"})
	out.AttachmentsComplete = false
	if _, err := c.SaveDraft(context.Background(), out, "D"); err != nil {
		t.Fatal(err)
	}
	if got := d.list(); len(got) != 2 {
		t.Fatalf("partial list removed %+v", got)
	}
}

// Older pages include the whole second of the cursor: Graph compares whole
// seconds, and mail sharing that second with the oldest cached message
// (bulk deliveries) was excluded by "lt" for good.
func TestListOlderIncludesTheCursorSecond(t *testing.T) {
	var filter string
	c := auditClient(t, func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("$filter")
		w.Write([]byte(`{"value":[]}`))
	})
	before := time.Date(2026, 3, 4, 5, 6, 7, 500e6, time.UTC)
	if _, err := c.ListOlder(context.Background(), mail.Folder{RemoteID: "inbox"}, before, 10); err != nil {
		t.Fatal(err)
	}
	if filter != "receivedDateTime lt 2026-03-04T05:06:08Z" {
		t.Fatalf("filter %q", filter)
	}
}
