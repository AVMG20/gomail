package gmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/fakes"
	"github.com/AVMG20/gomail/internal/httpx"
)

// batchServer serves messages/<id> metadata through the fake batch
// endpoint; status picks a failure for an id and attempt (0 = success).
type batchServer struct {
	mu       sync.Mutex
	batches  []int // calls per batch request
	singles  int   // GETs outside a batch
	attempts map[string]int
	sleeps   []time.Duration
	status   func(id string, attempt int) int
	noBatch  bool // answer the batch endpoint with 404
}

func newBatchServer(t *testing.T, status func(id string, attempt int) int) (*Client, *batchServer) {
	b := &batchServer{attempts: map[string]int{}, status: status}
	msgs := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
		if !ok || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		b.mu.Lock()
		n := b.attempts[id]
		b.attempts[id]++
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch s := b.status(id, n); s {
		case 0:
			writeJSON(w, map[string]any{"id": id, "threadId": "t" + id, "labelIds": []string{"INBOX"}, "internalDate": "1790000000000",
				"payload": map[string]any{"mimeType": "text/plain", "headers": []map[string]string{{"name": "Subject", "value": "About " + id}}}})
		case 429:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			writeJSON(w, map[string]any{"error": map[string]any{"code": 429, "message": "Too many concurrent requests for user", "status": "RESOURCE_EXHAUSTED",
				"errors": []map[string]string{{"reason": "rateLimitExceeded"}}}})
		default:
			w.WriteHeader(s)
			writeJSON(w, map[string]any{"error": map[string]any{"code": s, "message": http.StatusText(s)}})
		}
	})
	batch := fakes.GoogleBatch(fakes.GmailBatchPath, msgs)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		noBatch := b.noBatch
		if r.URL.Path == fakes.GmailBatchPath {
			b.batches = append(b.batches, strings.Count(readAll(r), "Content-Type: application/http"))
		} else {
			b.singles++
		}
		b.mu.Unlock()
		if noBatch && r.URL.Path == fakes.GmailBatchPath {
			http.NotFound(w, r)
			return
		}
		batch.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(httpx.StaticToken("tok"), srv.URL)
	c.SetSleep(func(_ context.Context, d time.Duration) error {
		b.mu.Lock()
		b.sleeps = append(b.sleeps, d)
		b.mu.Unlock()
		return nil
	})
	return c, b
}

// readAll reads a request body and puts it back for the next handler.
func readAll(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	return string(data)
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("m%03d", i)
	}
	return out
}

// One batch answers 200, 404, 429 and 503 at once: the found messages are
// kept, the missing one is gone, and only the throttled and failed calls
// are sent again, after the Retry-After wait.
func TestBatchMixedStatuses(t *testing.T) {
	c, b := newBatchServer(t, func(id string, attempt int) int {
		switch {
		case id == "gone":
			return 404
		case id == "busy" && attempt == 0:
			return 429
		case id == "flaky" && attempt < 2:
			return 503
		}
		return 0
	})
	var progress []int
	msgs, gone, err := c.fetchMetadata(context.Background(), []string{"a", "gone", "busy", "b", "flaky"}, func(done, total int) {
		if total != 5 {
			t.Errorf("total %d", total)
		}
		progress = append(progress, done)
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.RemoteID+":"+m.Subject)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "a:About a,b:About b,busy:About busy,flaky:About flaky" || strings.Join(gone, ",") != "gone" {
		t.Fatalf("messages %v gone %v", got, gone)
	}
	if fmt.Sprint(b.batches) != "[5 2 1]" || b.singles != 0 {
		t.Fatalf("batches %v singles %d", b.batches, b.singles)
	}
	if len(b.sleeps) != 2 || b.sleeps[0] != 7*time.Second {
		t.Fatalf("sleeps %v (want Retry-After first)", b.sleeps)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 5 {
		t.Fatalf("progress %v", progress)
	}
}

// More ids than one batch holds are split into batches of 50, with all
// results and the progress complete.
func TestBatchSplitsLargeFetches(t *testing.T) {
	c, b := newBatchServer(t, func(string, int) int { return 0 })
	var mu sync.Mutex
	last := 0
	msgs, gone, err := c.fetchMetadata(context.Background(), ids(120), func(done, total int) {
		mu.Lock()
		defer mu.Unlock()
		if done < last || total != 120 {
			t.Errorf("progress %d/%d after %d", done, total, last)
		}
		last = done
	})
	if err != nil || len(msgs) != 120 || len(gone) != 0 {
		t.Fatalf("%d messages, gone %v, err %v", len(msgs), gone, err)
	}
	sort.Ints(b.batches)
	if fmt.Sprint(b.batches) != "[20 50 50]" || b.singles != 0 || last != 120 {
		t.Fatalf("batches %v singles %d progress %d", b.batches, b.singles, last)
	}
}

// A call that keeps failing, or fails for good, fails the fetch.
func TestBatchErrors(t *testing.T) {
	c, b := newBatchServer(t, func(id string, _ int) int {
		if id == "m001" {
			return 503
		}
		return 0
	})
	_, _, err := c.fetchMetadata(context.Background(), ids(3))
	if httpx.StatusOf(err) != 503 || b.attempts["m001"] != batchAttempts {
		t.Fatalf("err %v after %d attempts", err, b.attempts["m001"])
	}
	c, _ = newBatchServer(t, func(id string, _ int) int {
		if id == "m001" {
			return 400
		}
		return 0
	})
	if _, _, err := c.fetchMetadata(context.Background(), ids(3)); httpx.StatusOf(err) != 400 {
		t.Fatalf("bad request: %v", err)
	}
	// Cancelling stops the retries.
	c, _ = newBatchServer(t, func(string, int) int { return 429 })
	c.SetSleep(func(context.Context, time.Duration) error { return context.Canceled })
	if _, _, err := c.fetchMetadata(context.Background(), ids(3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

// Without a batch endpoint (404), messages are fetched one by one, and the
// endpoint isn't tried again.
func TestBatchFallsBackToSingleRequests(t *testing.T) {
	c, b := newBatchServer(t, func(id string, _ int) int {
		if id == "m002" {
			return 404
		}
		return 0
	})
	b.noBatch = true
	for range 2 {
		msgs, gone, err := c.fetchMetadata(context.Background(), ids(3))
		if err != nil || len(msgs) != 2 || strings.Join(gone, ",") != "m002" {
			t.Fatalf("%d messages, gone %v, err %v", len(msgs), gone, err)
		}
	}
	if len(b.batches) != 1 || b.singles != 6 {
		t.Fatalf("batches %v singles %d", b.batches, b.singles)
	}
}
