package devsync

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/syncserver"
)

// hookTransport runs a function before a request to a path (once).
type hookTransport struct {
	path string
	hook atomic.Pointer[func()]
}

func (h *hookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == h.path {
		if f := h.hook.Swap(nil); f != nil {
			(*f)()
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// A change made while a round downloads the other devices' changes is
// newer than them: it is kept and uploaded.
func TestEditDuringDownloadIsKept(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"k": "v0"})
	b := newDevice(t, map[string]string{})
	ht := &hookTransport{path: "/v1/docs/prefs"}
	b.eng = New(Options{Source: b.src, State: b.state, Secrets: b.sec, HTTP: &http.Client{Transport: ht}, Now: func() time.Time { return *b.clock }})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)

	a.tick(time.Minute)
	a.src.set("k", "X")
	a.sync(t)

	b.tick(2 * time.Minute)
	edit := func() { b.src.set("k", "mine") }
	ht.hook.Store(&edit)
	b.sync(t)
	a.sync(t)
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values()["k"]; got != "mine" {
			t.Fatalf("%s: k=%q, the change made during the download was lost", name, got)
		}
	}
}

// A device's first change to a key it hasn't synced yet is a change like
// any other: it wins over an older value from another device.
func TestFirstChangeOfAKeyWins(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"base": "1"})
	b := newDevice(t, map[string]string{"base": "1"})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)

	a.tick(time.Minute)
	a.src.set("color", "red")
	a.sync(t)
	b.tick(2 * time.Minute)
	b.src.set("color", "blue") // later, before b synced a's
	b.sync(t)
	a.sync(t)
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values()["color"]; got != "blue" {
			t.Fatalf("%s: color=%q, want the later blue", name, got)
		}
	}
}

// A clock that runs ahead doesn't make a device's changes win over later
// ones: a device stamps its change after everything it has seen.
func TestClockAheadDoesNotWin(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"theme": "light"})
	b := newDevice(t, map[string]string{})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)

	a.tick(time.Hour) // a's clock is an hour fast
	a.src.set("theme", "dark")
	a.sync(t)
	b.tick(10 * time.Minute)
	b.sync(t) // b has seen a's change
	b.tick(time.Minute)
	b.src.set("theme", "blue") // and changes it after
	b.sync(t)
	a.sync(t)
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values()["theme"]; got != "blue" {
			t.Fatalf("%s: theme=%q, want the later blue", name, got)
		}
	}
}

// A deletion stays deleted, however long a device was away and whatever
// its clock says.
func TestDeletionsAreNotForgotten(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"k": "1", "base": "1"})
	c := newDevice(t, map[string]string{"base": "1"})
	a.create(t, url, true)
	a.sync(t)
	c.join(t, url, a)
	c.sync(t)

	a.src.set("k", "")
	a.tick(time.Minute)
	a.sync(t)
	a.tick(400 * 24 * time.Hour) // a year later, c comes back
	c.tick(400 * 24 * time.Hour)
	a.sync(t)
	c.sync(t)
	a.sync(t)
	if _, ok := a.src.values()["k"]; ok {
		t.Fatal("a deleted key came back after a long time")
	}
	if _, ok := c.src.values()["k"]; ok {
		t.Fatal("the deletion didn't reach the device that was away")
	}

	// A clock 200 days ahead doesn't drop a deletion either.
	b := newDevice(t, map[string]string{})
	b.join(t, url, a)
	b.tick(600 * 24 * time.Hour)
	b.sync(t)
	b.sync(t)
	a.sync(t)
	if _, ok := a.src.values()["k"]; ok {
		t.Fatal("a fast clock brought a deleted key back")
	}
}

// failOnce applies everything, then reports an error once (a round that
// stops after the app applied the changes).
type failOnce struct {
	*memSource
	fail atomic.Bool
}

func (f *failOnce) Apply(doc string, ch map[string]json.RawMessage, synced func(string) (json.RawMessage, bool)) ([]string, error) {
	n, err := f.memSource.Apply(doc, ch, synced)
	if f.fail.Swap(false) {
		return n, errors.New("disk full")
	}
	return n, err
}

// Values applied in a round that then failed aren't taken for changes
// made here the next time, so they don't overwrite newer ones.
func TestFailedRoundDoesNotResendAppliedValues(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"k": "v0"})
	b := newDevice(t, map[string]string{})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)

	a.tick(time.Minute)
	a.src.set("k", "X")
	a.sync(t)

	src := &failOnce{memSource: b.src}
	src.fail.Store(true)
	b.eng = New(Options{Source: src, State: b.state, Secrets: b.sec, Now: func() time.Time { return *b.clock }})
	b.tick(10 * time.Second)
	if err := b.eng.SyncNow(context.Background()); err == nil {
		t.Fatal("the round didn't fail")
	}
	a.tick(time.Minute)
	a.src.set("k", "Z") // newer
	a.sync(t)
	b.tick(10 * time.Minute)
	b.sync(t)
	a.sync(t)
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values()["k"]; got != "Z" {
			t.Fatalf("%s: k=%q, want the newer Z", name, got)
		}
	}
}

// A deletion a device doesn't carry out (it keeps the value) isn't sent
// back to the others, until the value changes there.
func TestKeptValueIsNotSentBack(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"key": "sk-1"})
	b := newDevice(t, map[string]string{})
	b.src.keep = map[string]bool{"key": true}
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)

	a.tick(time.Minute)
	a.src.set("key", "")
	a.sync(t)
	for range 3 {
		b.tick(time.Minute)
		a.tick(time.Minute)
		b.sync(t)
		a.sync(t)
	}
	if got, ok := a.src.values()["key"]; ok {
		t.Fatalf("a's deleted key came back: %q", got)
	}
	b.tick(time.Minute)
	b.src.set("key", "sk-2") // a new value on b is a change
	b.sync(t)
	a.sync(t)
	if got := a.src.values()["key"]; got != "sk-2" {
		t.Fatalf("a: key=%q", got)
	}
}

// nullSource reports a null value for a key.
type nullSource struct{ *memSource }

func (n nullSource) Snapshot(doc string, synced func(string) (json.RawMessage, bool)) (map[string]json.RawMessage, error) {
	out, err := n.memSource.Snapshot(doc, synced)
	if _, ok := out["n"]; ok {
		out["n"] = nil
	}
	return out, err
}

// A value that is null isn't a deletion.
func TestNullValueIsNotADeletion(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"n": "x"})
	b := newDevice(t, map[string]string{})
	a.eng = New(Options{Source: nullSource{a.src}, State: a.state, Secrets: a.sec, Now: func() time.Time { return *a.clock }})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	b.sync(t)
	if _, ok := b.src.values()["n"]; !ok {
		t.Fatalf("a null value arrived as a deletion: %v", b.src.values())
	}
}

// Two devices that agree upload nothing, round after round.
func TestQuietOnceConverged(t *testing.T) {
	_, url := newSyncServer(t)
	a := newDevice(t, map[string]string{"theme": "dark", "n": "1"})
	b := newDevice(t, map[string]string{"density": "compact"})
	a.create(t, url, true)
	a.sync(t)
	b.join(t, url, a)
	for range 2 {
		b.sync(t)
		a.sync(t)
	}
	revs := docRevs(t, url, a)
	for range 5 {
		a.tick(time.Minute)
		b.tick(time.Minute)
		a.sync(t)
		b.sync(t)
	}
	if after := docRevs(t, url, a); !maps.Equal(revs, after) {
		t.Fatalf("converged devices uploaded: %v -> %v", revs, after)
	}
}

// Start twice runs one loop; Stop waits for a round and no round runs
// after it.
func TestStartStop(t *testing.T) {
	_, url := newSyncServer(t)
	d := newDevice(t, map[string]string{"k": "v"})
	d.create(t, url, false) // asks for a round
	entered, release := make(chan struct{}), make(chan struct{})
	d.src.mu.Lock()
	d.src.onSnapshot = func() { close(entered); <-release }
	d.src.mu.Unlock()
	ctx := context.Background()
	d.eng.Start(ctx)
	first := d.eng.done
	d.eng.Start(ctx)
	if d.eng.done != first {
		t.Fatal("a second Start started another loop")
	}
	<-entered
	stopped := make(chan struct{})
	go func() { d.eng.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a round was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-stopped
	select {
	case <-first:
	default:
		t.Fatal("the loop is still running")
	}
	if err := d.eng.SyncNow(ctx); err == nil {
		t.Fatal("a round ran after Stop")
	}
}

// swapServer lets a test replace the server behind a URL (its data lost).
type swapServer struct {
	h atomic.Pointer[syncserver.Server]
}

func (s *swapServer) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.Load().ServeHTTP(w, r) }

func (s *swapServer) reset(t *testing.T, signups bool) {
	srv, err := syncserver.New(syncserver.Config{DataDir: t.TempDir(), Signups: signups})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	s.h.Store(srv)
}

// A server that lost its data gets the account back from the devices:
// the first one to sync signs up again under the same code and uploads
// everything. With sign-ups closed, the devices say so.
func TestServerLostItsData(t *testing.T) {
	sw := &swapServer{}
	sw.reset(t, true)
	ts := httptest.NewServer(sw)
	t.Cleanup(ts.Close)
	a := newDevice(t, map[string]string{"theme": "dark"})
	b := newDevice(t, map[string]string{})
	a.create(t, ts.URL, true)
	a.sync(t)
	b.join(t, ts.URL, a)
	b.sync(t)

	sw.reset(t, false)
	if err := a.eng.SyncNow(context.Background()); err == nil || !strings.Contains(a.eng.Status().Error, "doesn't accept new sync accounts") {
		t.Fatalf("closed sign-ups: %v / %q", err, a.eng.Status().Error)
	}

	sw.reset(t, true)
	a.tick(time.Minute)
	a.src.set("theme", "light")
	a.sync(t)
	b.sync(t)
	if got := b.src.values()["theme"]; got != "light" {
		t.Fatalf("b after the server was restored: %q", got)
	}
	if revs := docRevs(t, ts.URL, a); revs["prefs"] == 0 {
		t.Fatalf("nothing uploaded again: %v", revs)
	}
}
