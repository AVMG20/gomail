package devsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/syncapi"
	"github.com/AVMG20/gomail/internal/syncserver"
)

func init() { KDFIterations = 1000 } // fast tests; the format is the same

func TestKeysAndSealing(t *testing.T) {
	k1, err := DeriveKeys("alice", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := DeriveKeys("alice", "correct horse battery")
	other, _ := DeriveKeys("alice", "wrong horse battery")
	bob, _ := DeriveKeys("bob", "correct horse battery")
	if !bytes.Equal(k1.Auth, again.Auth) || !bytes.Equal(k1.Enc, again.Enc) {
		t.Fatal("keys aren't deterministic")
	}
	if bytes.Equal(k1.Auth, k1.Enc) || bytes.Equal(k1.Enc, other.Enc) || bytes.Equal(k1.Enc, bob.Enc) || len(k1.Enc) != 32 {
		t.Fatal("keys must differ by purpose, passphrase and username")
	}

	plain := []byte(`{"theme":"dark","secret":"sk-or-123"}`)
	sealed, err := Seal(k1.Enc, "settings", plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("dark")) || bytes.Contains(sealed, []byte("sk-or")) {
		t.Fatal("plaintext visible in the sealed document")
	}
	twice, _ := Seal(k1.Enc, "settings", plain)
	if bytes.Equal(sealed, twice) {
		t.Fatal("sealing must use a fresh nonce")
	}
	got, err := Open(k1.Enc, "settings", sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %v %q", err, got)
	}
	if _, err := Open(other.Enc, "settings", sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, err := Open(k1.Enc, "labels", sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("document swapped by the server: %v", err)
	}
	bad := slices.Clone(sealed)
	bad[len(bad)-1] ^= 1
	if _, err := Open(k1.Enc, "settings", bad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := Open(k1.Enc, "settings", []byte{1, 2}); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short: %v", err)
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"":                           DefaultServer,
		"gomail.avmg.nl":             "https://gomail.avmg.nl",
		"https://sync.example.com/":  "https://sync.example.com",
		"http://127.0.0.1:8080":      "http://127.0.0.1:8080",
		"http://localhost:8080/sync": "http://localhost:8080/sync",
	} {
		if got, err := NormalizeServer(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"http://sync.example.com", "ftp://x", "https://user:pw@x.com", "https://"} {
		if _, err := NormalizeServer(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestMergeLastWriterWins(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	v := func(s string) json.RawMessage { return json.RawMessage(`"` + s + `"`) }
	mine := Entries{
		"a": {V: v("mine"), T: 20},
		"b": {V: v("mine"), T: 10},
		"c": {V: v("mine"), T: 0},          // known here before syncing
		"d": {T: now.UnixMilli(), D: true}, // a recent deletion
		"e": {V: v("only mine"), T: 5},
		"f": {T: now.Add(-tombstoneTTL - time.Hour).UnixMilli(), D: true},
	}
	theirs := Entries{
		"a": {V: v("theirs"), T: 10},
		"b": {V: v("theirs"), T: 20},
		"c": {V: v("theirs"), T: 0},
		"d": {V: v("theirs"), T: now.UnixMilli() - 1},
		"g": {V: v("only theirs"), T: 1},
	}
	got := merge(mine, theirs, now)
	want := Entries{
		"a": {V: v("mine"), T: 20},
		"b": {V: v("theirs"), T: 20},
		"c": {V: v("theirs"), T: 0}, // ties go to the server's copy
		"d": {T: now.UnixMilli(), D: true},
		"e": {V: v("only mine"), T: 5},
		"g": {V: v("only theirs"), T: 1},
	}
	if !got.equal(want) {
		t.Fatalf("merge:\n%v\nwant\n%v", got, want)
	}
}

func TestLocalChanges(t *testing.T) {
	st := docState{Entries: Entries{
		"same":    {V: json.RawMessage(`1`), T: 5},
		"changed": {V: json.RawMessage(`1`), T: 5},
		"gone":    {V: json.RawMessage(`1`), T: 5},
		"foreign": {V: json.RawMessage(`1`), T: 5},
		"waiting": {V: json.RawMessage(`1`), T: 5},
		"dead":    {T: 5, D: true},
	}, Pending: []string{"waiting"}}
	local := map[string]json.RawMessage{
		"same":    json.RawMessage(` 1 `),
		"changed": json.RawMessage(`2`),
		"new":     json.RawMessage(`3`),
		"waiting": json.RawMessage(`9`),
		"dead":    json.RawMessage(`4`),
	}
	owns := func(k string) bool { return k != "foreign" }
	got := localChanges(st, local, owns, 100)
	want := Entries{
		"same":    {V: json.RawMessage(`1`), T: 5},
		"changed": {V: json.RawMessage(`2`), T: 100},
		"gone":    {T: 100, D: true},
		"foreign": {V: json.RawMessage(`1`), T: 5},
		"waiting": {V: json.RawMessage(`1`), T: 5}, // not applied yet: not a local change
		"dead":    {V: json.RawMessage(`4`), T: 100},
		"new":     {V: json.RawMessage(`3`), T: 0},
	}
	if !got.equal(want) {
		t.Fatalf("local changes:\n%v\nwant\n%v", got, want)
	}
}

// ---- engines against a real server ----

// memSource is an app with one document of string values.
type memSource struct {
	mu       sync.Mutex
	vals     map[string]string
	foreign  map[string]bool // keys this device can't hold (Apply refuses, Owns false)
	applied  int
	snapshot int
	// onSnapshot runs once, after the next snapshot (to race another device).
	onSnapshot func()
}

func (m *memSource) Docs() []string { return []string{"prefs"} }

func (m *memSource) Snapshot(string, func(string) (json.RawMessage, bool)) (map[string]json.RawMessage, error) {
	m.mu.Lock()
	m.snapshot++
	out := map[string]json.RawMessage{}
	for k, v := range m.vals {
		out[k], _ = json.Marshal(v)
	}
	hook := m.onSnapshot
	m.onSnapshot = nil
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

func (m *memSource) Owns(_, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.foreign[key]
}

func (m *memSource) Apply(_ string, changes map[string]json.RawMessage) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var later []string
	for k, v := range changes {
		if m.foreign[k] {
			later = append(later, k)
			continue
		}
		m.applied++
		if v == nil {
			delete(m.vals, k)
			continue
		}
		var s string
		_ = json.Unmarshal(v, &s)
		m.vals[k] = s
	}
	return later, nil
}

func (m *memSource) set(k, v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v == "" {
		delete(m.vals, k)
	} else {
		m.vals[k] = v
	}
}

func (m *memSource) values() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.vals)
}

type memState struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memState) Load(key string, v any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, v)
}

func (s *memState) Save(key string, v any) error {
	b, err := json.Marshal(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = b
	return err
}

func (s *memState) DeletePrefix(prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			delete(s.m, k)
		}
	}
	return nil
}

type device struct {
	src   *memSource
	state *memState
	sec   *secrets.Memory
	eng   *Engine
	clock *time.Time
}

func newDevice(t *testing.T, vals map[string]string) *device {
	now := time.UnixMilli(1_800_000_000_000)
	d := &device{src: &memSource{vals: vals, foreign: map[string]bool{}}, state: &memState{m: map[string][]byte{}}, sec: secrets.NewMemory(), clock: &now}
	d.eng = New(Options{Source: d.src, State: d.state, Secrets: d.sec, Now: func() time.Time { return *d.clock }})
	return d
}

func (d *device) tick(dt time.Duration) { *d.clock = d.clock.Add(dt) }

func (d *device) sync(t *testing.T) {
	t.Helper()
	if err := d.eng.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newSyncServer(t *testing.T) (*syncserver.Server, string) {
	srv, err := syncserver.New(syncserver.Config{DataDir: t.TempDir(), Signups: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts.URL
}

func TestTwoDevicesConverge(t *testing.T) {
	_, url := newSyncServer(t)
	ctx := context.Background()
	a := newDevice(t, map[string]string{"theme": "dark", "color": "blue", "onlyA": "1"})
	b := newDevice(t, map[string]string{"theme": "light", "onlyB": "2"})

	if err := a.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "short", Create: true}); err == nil {
		t.Fatal("a short passphrase was accepted for a new account")
	}
	if err := a.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "a long sync passphrase", Create: true, Secrets: true}); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	if err := b.eng.Connect(ctx, ConnectParams{Server: url, Username: "Alice", Passphrase: "the wrong passphrase"}); err == nil || !strings.Contains(err.Error(), "wrong username or passphrase") {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if err := b.eng.Connect(ctx, ConnectParams{Server: url, Username: "Alice", Passphrase: "a long sync passphrase"}); err != nil {
		t.Fatal(err)
	}
	b.sync(t)
	// The joining device takes the synced values and adds its own.
	want := map[string]string{"theme": "dark", "color": "blue", "onlyA": "1", "onlyB": "2"}
	if got := b.src.values(); !maps.Equal(got, want) {
		t.Fatalf("b after joining: %v", got)
	}
	a.sync(t)
	if got := a.src.values(); !maps.Equal(got, want) {
		t.Fatalf("a after b joined: %v", got)
	}

	// Edits and deletions spread; the later write wins a conflict.
	a.tick(time.Minute)
	b.tick(2 * time.Minute)
	a.src.set("theme", "blue-ish")
	b.src.set("theme", "solarized")
	b.src.set("onlyA", "")
	a.sync(t)
	b.sync(t) // conflicts with a's upload, merges and retries
	a.sync(t)
	want = map[string]string{"theme": "solarized", "color": "blue", "onlyB": "2"}
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values(); !maps.Equal(got, want) {
			t.Fatalf("%s: %v want %v", name, got, want)
		}
	}

	// No echo: rounds without changes apply and upload nothing.
	appliedA, appliedB := a.src.applied, b.src.applied
	listBefore := docRevs(t, url, "alice", "a long sync passphrase")
	a.sync(t)
	b.sync(t)
	a.sync(t)
	if a.src.applied != appliedA || b.src.applied != appliedB {
		t.Fatalf("idle rounds applied something: %d->%d, %d->%d", appliedA, a.src.applied, appliedB, b.src.applied)
	}
	if after := docRevs(t, url, "alice", "a long sync passphrase"); !maps.Equal(listBefore, after) {
		t.Fatalf("idle rounds uploaded: %v -> %v", listBefore, after)
	}
}

func docRevs(t *testing.T, url, user, pass string) map[string]int64 {
	t.Helper()
	keys, _ := DeriveKeys(user, pass)
	cl := newClient(url, user, keys.Auth, nil)
	l, err := cl.list(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, d := range l.Docs {
		out[d.Name] = d.Rev
	}
	return out
}

func TestUnknownKeysAreKept(t *testing.T) {
	_, url := newSyncServer(t)
	ctx := context.Background()
	a := newDevice(t, map[string]string{"mailbox-x": "on", "shared": "1"})
	b := newDevice(t, map[string]string{"shared": "1"})
	b.src.foreign["mailbox-x"] = true // b doesn't have that mailbox
	for i, d := range []*device{a, b} {
		if err := d.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "a long sync passphrase", Create: i == 0}); err != nil {
			t.Fatal(err)
		}
		d.sync(t)
	}
	if _, ok := b.src.values()["mailbox-x"]; ok {
		t.Fatal("b applied a key it can't hold")
	}
	b.tick(time.Minute)
	b.src.set("shared", "2")
	b.sync(t)
	a.sync(t)
	if got := a.src.values(); got["mailbox-x"] != "on" || got["shared"] != "2" {
		t.Fatalf("b's round dropped a's key: %v", got)
	}
	// Once b has the mailbox, the waiting value applies.
	b.src.mu.Lock()
	delete(b.src.foreign, "mailbox-x")
	b.src.mu.Unlock()
	b.sync(t)
	if got := b.src.values(); got["mailbox-x"] != "on" {
		t.Fatalf("pending key not applied: %v", got)
	}
}

func TestServerOnlySeesCiphertext(t *testing.T) {
	srv, url := newSyncServer(t)
	_ = srv
	ctx := context.Background()
	a := newDevice(t, map[string]string{"openrouter": "sk-or-v1-supersecret", "label": "Invoices from Acme"})
	if err := a.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "a long sync passphrase", Create: true}); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	keys, _ := DeriveKeys("alice", "a long sync passphrase")
	cl := newClient(url, "alice", keys.Auth, nil)
	doc, err := cl.get(ctx, "prefs")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"supersecret", "Acme", "openrouter", "entries"} {
		if bytes.Contains(doc.Data, []byte(s)) {
			t.Fatalf("server copy contains %q", s)
		}
	}
	// The local shadow is encrypted too, and the keys are in the secret store.
	for k, v := range a.state.m {
		if bytes.Contains(v, []byte("supersecret")) {
			t.Fatalf("local state %s holds a secret in plain text", k)
		}
	}
	if _, err := a.sec.Get(keysSecret); err != nil {
		t.Fatal("keys not in the secret store")
	}
	for k, v := range a.state.m {
		if bytes.Contains(v, []byte("long sync passphrase")) {
			t.Fatalf("passphrase stored in %s", k)
		}
	}
	// Disconnect forgets keys and state.
	if err := a.eng.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.sec.Get(keysSecret); !errors.Is(err, secrets.ErrNotFound) || len(a.state.m) != 0 || a.eng.Connected() {
		t.Fatal("disconnect left keys or state behind")
	}
	if err := a.eng.SyncNow(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("sync after disconnect: %v", err)
	}
}

func TestReconnectAfterRestart(t *testing.T) {
	_, url := newSyncServer(t)
	ctx := context.Background()
	a := newDevice(t, map[string]string{"k": "v"})
	if err := a.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "a long sync passphrase", Create: true, Secrets: true}); err != nil {
		t.Fatal(err)
	}
	a.sync(t)
	// A restart loads the connection from state and the secret store.
	again := New(Options{Source: a.src, State: a.state, Secrets: a.sec, Now: func() time.Time { return *a.clock }})
	st := again.Status()
	if !st.Connected || st.Username != "alice" || !st.Secrets || st.LastSync.IsZero() {
		t.Fatalf("status after restart %+v", st)
	}
	a.tick(time.Minute)
	a.src.set("k", "w")
	if err := again.SyncNow(ctx); err != nil {
		t.Fatal(err)
	}
	if revs := docRevs(t, url, "alice", "a long sync passphrase"); revs["prefs"] != 2 {
		t.Fatalf("revisions %v", revs)
	}
}

func TestConnectErrors(t *testing.T) {
	srv, err := syncserver.New(syncserver.Config{DataDir: t.TempDir(), Signups: false})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer func() { ts.Close(); srv.Close() }()
	d := newDevice(t, nil)
	err = d.eng.Connect(context.Background(), ConnectParams{Server: ts.URL, Username: "alice", Passphrase: "a long sync passphrase", Create: true})
	if err == nil || !strings.Contains(err.Error(), "doesn't accept new accounts") {
		t.Fatalf("closed signups: %v", err)
	}
	if !IsCode(&APIError{Body: syncapi.Error{Code: syncapi.ErrQuota}}, syncapi.ErrQuota) {
		t.Fatal("IsCode")
	}
	if d.eng.Connected() {
		t.Fatal("connected after a failed connect")
	}
}

func TestConflictRetry(t *testing.T) {
	_, url := newSyncServer(t)
	ctx := context.Background()
	a := newDevice(t, map[string]string{"x": "1", "y": "1"})
	b := newDevice(t, map[string]string{})
	for i, d := range []*device{a, b} {
		if err := d.eng.Connect(ctx, ConnectParams{Server: url, Username: "alice", Passphrase: "a long sync passphrase", Create: i == 0}); err != nil {
			t.Fatal(err)
		}
		d.sync(t)
	}
	a.tick(time.Minute)
	b.tick(time.Minute)
	a.src.set("x", "from a")
	b.src.set("y", "from b")
	// a uploads while b is between reading the server and uploading.
	b.src.onSnapshot = func() { a.sync(t) }
	b.sync(t)
	a.sync(t)
	want := map[string]string{"x": "from a", "y": "from b"}
	for name, d := range map[string]*device{"a": a, "b": b} {
		if got := d.src.values(); !maps.Equal(got, want) {
			t.Fatalf("%s: %v", name, got)
		}
	}
	if revs := docRevs(t, url, "alice", "a long sync passphrase"); revs["prefs"] != 3 {
		t.Fatalf("revisions %v (want a's upload, then b's merged one)", revs)
	}
}
