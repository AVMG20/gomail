package outlook

import (
	"context"
	"encoding/json"
	"errors"
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

// A folder's mail goes to Deleted Items before the empty folder is deleted
// for good; a folder with subfolders, or mail that didn't move, keeps it;
// Outlook's own folders say why they stay.
func TestDeleteFolder(t *testing.T) {
	var mu sync.Mutex
	items, children, moved, deleted := 2, 0, 0, ""
	stuck := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case p == "/me/mailFolders/f1" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]int{"childFolderCount": children, "totalItemCount": items})
		case p == "/me/mailFolders/f1/messages":
			json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"id": "m1"}, {"id": "m2"}}})
		case p == "/$batch":
			var body struct {
				Requests []map[string]any `json:"requests"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			var resps []map[string]any
			for _, req := range body.Requests {
				if !strings.HasSuffix(req["url"].(string), "/move") || req["body"].(map[string]any)["destinationId"] != "deleteditems" {
					t.Errorf("unexpected batch request %v", req)
				}
				moved++
				resps = append(resps, map[string]any{"id": req["id"], "status": 201, "body": map[string]any{}})
			}
			if !stuck {
				items = 0
			}
			json.NewEncoder(w).Encode(map[string]any{"responses": resps})
		case p == "/me/mailFolders/f1/permanentDelete" && r.Method == http.MethodPost:
			deleted = "f1"
			w.WriteHeader(http.StatusNoContent)
		case p == "/me/mailFolders/rss/permanentDelete":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "ErrorDeleteDistinguishedFolder", "message": "Distinguished folders cannot be deleted."}})
		case p == "/me/mailFolders/rss":
			json.NewEncoder(w).Encode(map[string]int{})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	ctx := context.Background()
	f1 := mail.Folder{RemoteID: "f1", Name: "Old", Kind: models.FolderCustom}

	if err := c.DeleteFolder(ctx, mail.Folder{RemoteID: "id-inbox", Kind: models.FolderInbox}); err == nil {
		t.Fatal("deleted the inbox")
	}
	children = 1
	if err := c.DeleteFolder(ctx, f1); !errors.Is(err, provider.ErrFolderHasSubfolders) {
		t.Fatalf("subfolders: %v", err)
	}
	children, stuck = 0, true
	if err := c.DeleteFolder(ctx, f1); err == nil || deleted != "" {
		t.Fatalf("deleted a folder whose mail didn't move: %v", err)
	}
	stuck, moved = false, 0
	if err := c.DeleteFolder(ctx, f1); err != nil || deleted != "f1" || moved != 2 {
		t.Fatalf("delete: %v, deleted %q, moved %d", err, deleted, moved)
	}
	err := c.DeleteFolder(ctx, mail.Folder{RemoteID: "rss", Name: "RSS Feeds", Kind: models.FolderCustom})
	if err == nil || !strings.Contains(err.Error(), "hide it instead") {
		t.Fatalf("distinguished folder: %v", err)
	}
}
