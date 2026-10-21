package devsync

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/secrets"
	"github.com/AVMG20/gomail/internal/syncapi"
	"github.com/AVMG20/gomail/internal/syncserver"
)

// Official BIP39 test vectors: the sync code is a standard 24-word mnemonic.
var bip39Vectors = []struct{ entropy, words string }{
	{"0000000000000000000000000000000000000000000000000000000000000000",
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"},
	{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f",
		"legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title"},
	{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote"},
}

func TestSyncCodes(t *testing.T) {
	if len(wordlist) != 2048 || wordlist[0] != "abandon" || wordlist[2047] != "zoo" {
		t.Fatalf("wordlist: %d words", len(wordlist))
	}
	for _, v := range bip39Vectors {
		entropy, _ := hex.DecodeString(v.entropy)
		if got := encodeCode(entropy); got != v.words {
			t.Errorf("encode %s:\n%s", v.entropy, got)
		}
		back, canonical, err := ParseCode(v.words)
		if err != nil || !bytes.Equal(back, entropy) || canonical != v.words {
			t.Errorf("parse %s: %v", v.entropy, err)
		}
	}
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewCode()
	if code == other || len(strings.Fields(code)) != CodeWords {
		t.Fatalf("codes %q %q", code, other)
	}
	words := strings.Fields(code)
	// Pasted from a numbered grid, in capitals, over several lines, or with
	// four-letter abbreviations: the same code.
	var numbered, abbreviated []string
	for i, w := range words {
		numbered = append(numbered, fmt.Sprintf("%d. %s", i+1, strings.ToUpper(w)))
		abbreviated = append(abbreviated, w[:min(4, len(w))])
	}
	for _, typed := range []string{"  " + strings.Join(words, "\n") + "\n", strings.Join(numbered, "\t"), strings.Join(words, ", "), strings.Join(abbreviated, " ")} {
		if _, canonical, err := ParseCode(typed); err != nil || canonical != code {
			t.Errorf("%q: %v", typed, err)
		}
	}

	// A typo names the word; a real word in the wrong place fails the checksum.
	typo := slices.Clone(words)
	typo[4] = "abandn"
	var ce *CodeError
	if _, _, err := ParseCode(strings.Join(typo, " ")); !errors.As(err, &ce) || ce.Word != 5 || !strings.Contains(err.Error(), "abandn") {
		t.Fatalf("typo: %v", err)
	}
	swapped := slices.Clone(words)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if swapped[0] == swapped[1] {
		swapped[0] = "zoo"
	}
	if _, _, err := ParseCode(strings.Join(swapped, " ")); err == nil || !strings.Contains(err.Error(), "aren't a valid sync code") {
		t.Fatalf("checksum: %v", err)
	}
	if _, _, err := ParseCode(strings.Join(words[:23], " ")); err == nil || !strings.Contains(err.Error(), "has 23") {
		t.Fatalf("short code: %v", err)
	}
	if _, _, err := ParseCode("   "); err == nil {
		t.Fatal("empty code accepted")
	}
}

func TestKeysAndSealing(t *testing.T) {
	k1, _, err := KeysFromCode(bip39Vectors[1].words)
	if err != nil {
		t.Fatal(err)
	}
	again, _, _ := KeysFromCode(strings.ToUpper(bip39Vectors[1].words))
	other, _, _ := KeysFromCode(bip39Vectors[0].words)
	if k1.Account != again.Account || !bytes.Equal(k1.Auth, again.Auth) || !bytes.Equal(k1.Enc, again.Enc) {
		t.Fatal("keys aren't deterministic")
	}
	if bytes.Equal(k1.Auth, k1.Enc) || bytes.Equal(k1.Enc, other.Enc) || k1.Account == other.Account || len(k1.Enc) != 32 || !syncapi.ValidAccount(k1.Account) {
		t.Fatalf("keys must differ by purpose and code: %+v", k1)
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
		t.Fatalf("wrong code: %v", err)
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
		"f": {T: 1, D: true}, // deletions are kept, however old
	}
	theirs := Entries{
		"a": {V: v("theirs"), T: 10},
		"b": {V: v("theirs"), T: 20},
		"c": {V: v("theirs"), T: 0},
		"d": {V: v("theirs"), T: now.UnixMilli() - 1},
		"g": {V: v("only theirs"), T: 1},
	}
	got := merge(mine, theirs)
	want := Entries{
		"a": {V: v("mine"), T: 20},
		"b": {V: v("theirs"), T: 20},
		"c": {V: v("theirs"), T: 0}, // ties go to the server's copy
		"d": {T: now.UnixMilli(), D: true},
		"e": {V: v("only mine"), T: 5},
		"f": {T: 1, D: true},
		"g": {V: v("only theirs"), T: 1},
	}
	if !got.equal(want) {
		t.Fatalf("merge:\n%v\nwant\n%v", got, want)
	}

	// A change is stamped after everything this device has seen, however
	// far behind its clock is.
	if got := stamp(500, Entries{"x": {T: 400}}, Entries{"y": {T: 900}}); got != 901 {
		t.Fatalf("stamp %d", got)
	}
	if got := stamp(500, Entries{"x": {T: 400}}); got != 500 {
		t.Fatalf("stamp %d", got)
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
		"nil":     nil,
	}
	owns := func(k string) bool { return k != "foreign" }
	got := localChanges(st, local, Entries{}, owns, 100, false)
	want := Entries{
		"same":    {V: json.RawMessage(`1`), T: 5},
		"changed": {V: json.RawMessage(`2`), T: 100},
		"gone":    {T: 100, D: true},
		"foreign": {V: json.RawMessage(`1`), T: 5},
		"waiting": {V: json.RawMessage(`1`), T: 5}, // not applied yet: not a local change
		"dead":    {V: json.RawMessage(`4`), T: 100},
		"new":     {V: json.RawMessage(`3`), T: 100},
		"nil":     {V: json.RawMessage(`null`), T: 100}, // a value, not a deletion
	}
	if !got.equal(want) {
		t.Fatalf("local changes:\n%v\nwant\n%v", got, want)
	}

	// A joining device's values give way to the synced ones, but not to
	// deletions: what it has stays.
	got = localChanges(docState{Entries: Entries{}}, map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)},
		Entries{"b": {T: 50, D: true}}, owns, 100, true)
	if want := (Entries{"a": {V: json.RawMessage(`1`), T: 0}, "b": {V: json.RawMessage(`2`), T: 100}}); !got.equal(want) {
		t.Fatalf("joining:\n%v\nwant\n%v", got, want)
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
	// keep: deletions of these keys aren't carried out (Apply leaves them).
	keep map[string]bool
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

func (m *memSource) Apply(_ string, changes map[string]json.RawMessage, _ func(string) (json.RawMessage, bool)) ([]string, error) {
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
			if !m.keep[k] {
				delete(m.vals, k)
			}
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

// create turns on sync with a new code.
func (d *device) create(t *testing.T, url string, secrets bool) {
	t.Helper()
	if err := d.eng.Connect(context.Background(), ConnectParams{Server: url, Create: true, Secrets: secrets}); err != nil {
		t.Fatal(err)
	}
}

// join connects with the code of another device.
func (d *device) join(t *testing.T, url string, from *device) {
	t.Helper()
	code, err := from.eng.Code()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.eng.Connect(context.Background(), ConnectParams{Server: url, Code: code}); err != nil {
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

	a.create(t, url, true)
	a.sync(t)
	// A valid code that no device turned on sync with.
	unknown, _ := NewCode()
	if err := b.eng.Connect(ctx, ConnectParams{Server: url, Code: unknown}); err == nil || !strings.Contains(err.Error(), "no sync account uses this code") {
		t.Fatalf("unknown code: %v", err)
	}
	if err := b.eng.Connect(ctx, ConnectParams{Server: url, Code: "zoo zoo"}); err == nil {
		t.Fatal("a broken code was accepted")
	}
	b.join(t, url, a)
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
	listBefore := docRevs(t, url, a)
	a.sync(t)
	b.sync(t)
	a.sync(t)
	if a.src.applied != appliedA || b.src.applied != appliedB {
		t.Fatalf("idle rounds applied something: %d->%d, %d->%d", appliedA, a.src.applied, appliedB, b.src.applied)
	}
	if after := docRevs(t, url, a); !maps.Equal(listBefore, after) {
		t.Fatalf("idle rounds uploaded: %v -> %v", listBefore, after)
	}
}

func docRevs(t *testing.T, url string, d *device) map[string]int64 {
	t.Helper()
	code, _ := d.eng.Code()
	keys, _, _ := KeysFromCode(code)
	cl := newClient(url, keys, nil)
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
	a := newDevice(t, map[string]string{"mailbox-x": "on", "shared": "1"})
	b := newDevice(t, map[string]string{"shared": "1"})
	b.src.foreign["mailbox-x"] = true // b doesn't have that mailbox
	for i, d := range []*device{a, b} {
		if i == 0 {
			d.create(t, url, false)
		} else {
			d.join(t, url, a)
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
	a.create(t, url, false)
	a.sync(t)
	code, _ := a.eng.Code()
	keys, _, _ := KeysFromCode(code)
	cl := newClient(url, keys, nil)
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
	if stored, err := a.sec.Get(codeSecret); err != nil || stored != code {
		t.Fatal("the code isn't in the secret store")
	}
	for k, v := range a.state.m {
		if bytes.Contains(v, []byte(strings.Fields(code)[0]+" "+strings.Fields(code)[1])) {
			t.Fatalf("sync code stored in %s", k)
		}
	}
	// Disconnect forgets keys and state.
	if err := a.eng.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.sec.Get(codeSecret); !errors.Is(err, secrets.ErrNotFound) || len(a.state.m) != 0 || a.eng.Connected() {
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
	a.create(t, url, true)
	a.sync(t)
	// A restart loads the connection from state and the secret store.
	again := New(Options{Source: a.src, State: a.state, Secrets: a.sec, Now: func() time.Time { return *a.clock }})
	st := again.Status()
	if !st.Connected || !st.Secrets || st.LastSync.IsZero() {
		t.Fatalf("status after restart %+v", st)
	}
	a.tick(time.Minute)
	a.src.set("k", "w")
	before := docRevs(t, url, a)
	if err := again.SyncNow(ctx); err != nil {
		t.Fatal(err)
	}
	if revs := docRevs(t, url, a); revs["prefs"] <= before["prefs"] {
		t.Fatalf("revisions %v -> %v", before, revs)
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
	err = d.eng.Connect(context.Background(), ConnectParams{Server: ts.URL, Create: true})
	if err == nil || !strings.Contains(err.Error(), "doesn't accept new sync accounts") {
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
	srv, _ := newSyncServer(t)
	var puts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, r)
		if r.Method == http.MethodPut && rec.Code == http.StatusOK {
			puts.Add(1)
		}
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(ts.Close)
	url := ts.URL
	a := newDevice(t, map[string]string{"x": "1", "y": "1"})
	b := newDevice(t, map[string]string{})
	for i, d := range []*device{a, b} {
		if i == 0 {
			d.create(t, url, false)
		} else {
			d.join(t, url, a)
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
	if n := puts.Load(); n != 3 {
		t.Fatalf("%d uploads (want the first, a's, then b's merged one)", n)
	}
}

func TestPeriodicRounds(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	for _, c := range []struct {
		name            string
		now, tick, last time.Time
		interval        time.Duration
		want            bool
	}{
		{"not yet", t0.Add(30 * time.Second), t0.Add(15 * time.Second), t0, time.Minute, false},
		{"interval passed", t0.Add(time.Minute), t0.Add(45 * time.Second), t0, time.Minute, true},
		{"every 15 minutes", t0.Add(10 * time.Minute), t0.Add(10*time.Minute - tickEvery), t0, 15 * time.Minute, false},
		{"woke from sleep", t0.Add(5 * time.Minute), t0.Add(time.Minute), t0.Add(4 * time.Minute), 15 * time.Minute, true},
		{"manual: never on its own", t0.Add(24 * time.Hour), t0, t0, 0, false},
	} {
		if got := due(c.now, c.tick, c.last, c.interval); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestManualSyncSkipsKicks(t *testing.T) {
	_, url := newSyncServer(t)
	d := newDevice(t, map[string]string{"k": "v"})
	d.eng.opts.Interval = func() time.Duration { return 0 }
	d.create(t, url, false)
	<-d.eng.now // connecting starts a round even when syncing is manual
	d.eng.Kick()
	select {
	case <-d.eng.now:
		t.Fatal("back online started a round although syncing is manual")
	default:
	}
}
