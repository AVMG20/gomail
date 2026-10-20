package outlook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

func auditClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(context.Context, time.Duration) error { return nil })
	return c
}

// batchHandler answers $batch sub-requests with status(req).
func batchHandler(status func(url string) int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Requests []batchReq `json:"requests"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		var resps []map[string]any
		for _, q := range body.Requests {
			st := status(q.URL)
			resps = append(resps, map[string]any{"id": q.ID, "status": st, "body": map[string]any{"id": "id-" + q.URL,
				"error": map[string]string{"code": "x", "message": fmt.Sprint("status ", st)}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"responses": resps})
	}
}

// Concurrent saves of one draft must not race on its attachment
// bookkeeping (a concurrent map write is a fatal crash). Run with -race.
func TestConcurrentDraftSaves(t *testing.T) {
	var n atomic.Int64
	c := auditClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/attachments"):
			w.Write([]byte(`{"value":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/attachments"):
			json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprint("att", n.Add(1))})
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		default:
			w.Write([]byte(`{}`))
		}
	})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			atts := []mail.OutgoingAttachment{{Filename: fmt.Sprintf("f%d.txt", i), Data: []byte("x")}}
			_, _ = c.SaveDraft(context.Background(), mail.Outgoing{Subject: "s", Text: "t", Attachments: atts}, "D")
		}(i)
		go func() {
			defer wg.Done()
			_, _ = c.SaveDraft(context.Background(), mail.Outgoing{Subject: "s", Text: "t"}, "D")
		}()
	}
	wg.Wait()
}

// A batch that fails for some messages reports just those.
func TestBatchPartialFailure(t *testing.T) {
	c := auditClient(t, batchHandler(func(u string) int {
		if strings.Contains(u, "/m2") {
			return 400
		}
		return 200
	}))
	err := c.SetRead(context.Background(), []string{"m1", "m2", "m3"}, true)
	var pe *provider.PartialError
	if !errors.As(err, &pe) || fmt.Sprint(pe.Failed) != "[m2]" {
		t.Fatalf("want partial error for m2, got %v", err)
	}
	// Every id failing is a plain error.
	c = auditClient(t, batchHandler(func(string) int { return 400 }))
	if err := c.SetRead(context.Background(), []string{"m1"}, true); err == nil || errors.As(err, &pe) {
		t.Fatalf("all failed: %v", err)
	}
}

// A well-known folder lookup that failed (beyond the throttling retries)
// is tried again instead of being cached for the client's lifetime.
func TestWellKnownNotCachedWhenPartial(t *testing.T) {
	var calls atomic.Int64
	c := auditClient(t, func(w http.ResponseWriter, r *http.Request) {
		first := calls.Add(1) == 1
		batchHandler(func(u string) int {
			switch {
			case strings.Contains(u, "/inbox") && first:
				return 500
			case strings.Contains(u, "/archive"):
				return 404 // the mailbox has none: fine to cache
			}
			return 200
		})(w, r)
	})
	ctx := context.Background()
	if err := c.loadWellKnown(ctx); err != nil {
		t.Fatal(err)
	}
	if c.byKind[models.FolderInbox] != "" {
		t.Fatal("setup: inbox should be missing after the failed lookup")
	}
	if err := c.loadWellKnown(ctx); err != nil {
		t.Fatal(err)
	}
	if c.byKind[models.FolderInbox] == "" {
		t.Fatal("failed inbox lookup was cached")
	}
	if err := c.loadWellKnown(ctx); err != nil || calls.Load() != 2 {
		t.Fatalf("complete lookup not cached: %d calls %v", calls.Load(), err)
	}
}

// A fresh delta round reports from which date the folder was listed
// completely.
func TestFreshDeltaCompleteness(t *testing.T) {
	newest := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	oldest := newest.Add(-time.Hour)
	c := auditClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages/delta"):
			json.NewEncoder(w).Encode(map[string]any{"value": []any{msgJSON("A", "inbox", true)}, "@odata.deltaLink": "next"})
		case strings.HasSuffix(r.URL.Path, "/messages"):
			json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{{"receivedDateTime": newest}, {"receivedDateTime": oldest}}})
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	})
	res, err := c.Sync(context.Background(), provider.SyncRequest{Folders: []mail.Folder{{RemoteID: "inbox", Kind: models.FolderInbox}},
		Limits: provider.Limits{Inbox: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := res.Complete["inbox"]; !ok || !got.Equal(oldest) {
		t.Fatalf("complete %v %v", got, ok)
	}
	// Incremental rounds claim nothing.
	res, err = c.Sync(context.Background(), provider.SyncRequest{Folders: []mail.Folder{{RemoteID: "inbox", Kind: models.FolderInbox}},
		State: map[string]string{"delta:inbox": "/me/mailFolders/inbox/messages/delta?x=1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Complete) != 0 {
		t.Fatalf("incremental round reported completeness %v", res.Complete)
	}
}
