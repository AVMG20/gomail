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
				Name, ContentBytes string
			}
			json.NewDecoder(r.Body).Decode(&in)
			data, _ := base64.StdEncoding.DecodeString(in.ContentBytes)
			d.n++
			a := gAttachment{ID: fmt.Sprint("att", d.n), Name: in.Name, Size: int64(len(data) + sizeOverhead)}
			d.atts = append(d.atts, a)
			json.NewEncoder(w).Encode(a)
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
