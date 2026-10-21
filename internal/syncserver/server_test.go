package syncserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/syncapi"
)

type testServer struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newTestServer(t *testing.T, cfg Config) *testServer {
	t.Helper()
	cfg.DataDir = t.TempDir()
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return &testServer{t: t, srv: srv, ts: ts}
}

func key(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

// Account ids as devices derive them from their sync codes.
const (
	alice = "a11ce000000000000000000000000001"
	bob   = "b0b00000000000000000000000000002"
)

// call sends a JSON request and decodes the answer into out (when 2xx) or
// returns the error body.
func (s *testServer) call(method, path, token string, in, out any) (int, syncapi.Error) {
	s.t.Helper()
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, s.ts.URL+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var e syncapi.Error
	if resp.StatusCode >= 300 {
		_ = json.NewDecoder(resp.Body).Decode(&e)
	} else if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			s.t.Fatal(err)
		}
	}
	return resp.StatusCode, e
}

func (s *testServer) register(account, k string) {
	s.t.Helper()
	if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: account, AuthKey: k}, nil); code != http.StatusCreated {
		s.t.Fatalf("register %s: %d %+v", account, code, e)
	}
}

func (s *testServer) login(account, k string) string {
	s.t.Helper()
	var res syncapi.LoginResponse
	if code, e := s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: account, AuthKey: k}, &res); code != http.StatusOK {
		s.t.Fatalf("login %s: %d %+v", account, code, e)
	}
	if res.Token == "" || res.ExpiresAt.Before(time.Now()) {
		s.t.Fatalf("login result %+v", res)
	}
	return res.Token
}

func TestAccountsAndSessions(t *testing.T) {
	s := newTestServer(t, Config{Signups: true})
	if code, _ := s.call("GET", "/healthz", "", nil, nil); code != http.StatusOK {
		t.Fatalf("health %d", code)
	}
	s.register(alice, key(1))
	if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: alice, AuthKey: key(2)}, nil); code != http.StatusConflict || e.Code != syncapi.ErrAccountExists {
		t.Fatalf("duplicate register: %d %+v", code, e)
	}
	for _, id := range []string{"x", "A11CE000000000000000000000000001", "a11ce00000000000000000000000000z"} {
		if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: id, AuthKey: key(2)}, nil); code != http.StatusBadRequest || e.Code != syncapi.ErrBadRequest {
			t.Fatalf("account id %q: %d %+v", id, code, e)
		}
	}
	if code, _ := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: bob, AuthKey: "c2hvcnQ="}, nil); code != http.StatusBadRequest {
		t.Fatalf("short key: %d", code)
	}
	if code, e := s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: alice, AuthKey: key(9)}, nil); code != http.StatusUnauthorized || e.Code != syncapi.ErrBadCredentials {
		t.Fatalf("wrong key: %d %+v", code, e)
	}
	if code, e := s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: bob, AuthKey: key(1)}, nil); code != http.StatusUnauthorized || e.Code != syncapi.ErrBadCredentials {
		t.Fatalf("unknown account: %d %+v", code, e)
	}
	token := s.login(alice, key(1))
	var list syncapi.DocList
	if code, _ := s.call("GET", "/v1/docs", token, nil, &list); code != http.StatusOK || len(list.Docs) != 0 || list.QuotaBytes != DefaultQuotaBytes {
		t.Fatalf("list %d %+v", code, list)
	}
	for _, tok := range []string{"", "nonsense"} {
		if code, e := s.call("GET", "/v1/docs", tok, nil, nil); code != http.StatusUnauthorized || e.Code != syncapi.ErrUnauthorized {
			t.Fatalf("token %q: %d %+v", tok, code, e)
		}
	}
	// The server keeps a salted hash, never the key.
	var salt, hash []byte
	if err := s.srv.db.sql.QueryRow(`SELECT salt, auth_hash FROM accounts WHERE id = ?`, alice).Scan(&salt, &hash); err != nil {
		t.Fatal(err)
	}
	if len(salt) != 16 || bytes.Contains(hash, bytes.Repeat([]byte{1}, 32)) {
		t.Fatalf("stored credentials %x %x", salt, hash)
	}
}

func TestSessionsExpire(t *testing.T) {
	s := newTestServer(t, Config{Signups: true, SessionTTL: time.Millisecond})
	s.register(alice, key(1))
	var res syncapi.LoginResponse
	s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: alice, AuthKey: key(1)}, &res)
	token := res.Token
	time.Sleep(5 * time.Millisecond)
	if code, e := s.call("GET", "/v1/docs", token, nil, nil); code != http.StatusUnauthorized || e.Code != syncapi.ErrUnauthorized {
		t.Fatalf("expired session: %d %+v", code, e)
	}
}

// Closed sign-ups refuse new accounts; existing ones keep working.
func TestSignupsClosed(t *testing.T) {
	s := newTestServer(t, Config{Signups: true})
	s.register(alice, key(1))
	s.srv.cfg.Signups = false
	if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: bob, AuthKey: key(2)}, nil); code != http.StatusForbidden || e.Code != syncapi.ErrSignupsClosed {
		t.Fatalf("closed: %d %+v", code, e)
	}
	s.login(alice, key(1))
}

func TestDocumentRevisions(t *testing.T) {
	s := newTestServer(t, Config{Signups: true})
	s.register(alice, key(1))
	s.register(bob, key(2))
	a, b := s.login(alice, key(1)), s.login(bob, key(2))

	put := func(token, name string, base int64, data string) (int, syncapi.Error, int64) {
		var res syncapi.PutResponse
		code, e := s.call("PUT", "/v1/docs/"+name, token, syncapi.PutRequest{BaseRev: base, Data: []byte(data)}, &res)
		return code, e, res.Rev
	}
	if code, _, rev := put(a, "settings", 0, "v1"); code != http.StatusOK || rev != 1 {
		t.Fatalf("first put: %d rev %d", code, rev)
	}
	if code, _, rev := put(a, "settings", 1, "v2"); code != http.StatusOK || rev != 2 {
		t.Fatalf("second put: %d rev %d", code, rev)
	}
	// A device that read revision 1 loses the race and learns the current one.
	if code, e, _ := put(a, "settings", 1, "stale"); code != http.StatusConflict || e.Code != syncapi.ErrConflict || e.Rev != 2 {
		t.Fatalf("stale put: %d %+v", code, e)
	}
	if code, e, _ := put(a, "fresh", 3, "x"); code != http.StatusConflict || e.Rev != 0 {
		t.Fatalf("put to a new doc with a base: %d %+v", code, e)
	}
	var doc syncapi.Doc
	if code, _ := s.call("GET", "/v1/docs/settings", a, nil, &doc); code != http.StatusOK || doc.Rev != 2 || string(doc.Data) != "v2" {
		t.Fatalf("get: %d %+v", code, doc)
	}
	// Accounts don't see each other's documents.
	if code, e := s.call("GET", "/v1/docs/settings", b, nil, nil); code != http.StatusNotFound || e.Code != syncapi.ErrNotFound {
		t.Fatalf("other account: %d %+v", code, e)
	}
	if code, _ := s.call("GET", "/v1/docs/Bad.Name", a, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", code)
	}
	if code, _, _ := put(a, "empty", 0, ""); code != http.StatusBadRequest {
		t.Fatalf("empty doc: %d", code)
	}
	var list syncapi.DocList
	s.call("GET", "/v1/docs", a, nil, &list)
	if len(list.Docs) != 1 || list.Docs[0].Name != "settings" || list.Docs[0].Rev != 2 || list.UsedBytes != 2 {
		t.Fatalf("list %+v", list)
	}
}

func TestLimits(t *testing.T) {
	s := newTestServer(t, Config{Signups: true, MaxDocBytes: 100, QuotaBytes: 250, MaxDocs: 3})
	s.register(alice, key(1))
	a := s.login(alice, key(1))
	put := func(name string, base int64, n int) (int, syncapi.Error) {
		return s.call("PUT", "/v1/docs/"+name, a, syncapi.PutRequest{BaseRev: base, Data: bytes.Repeat([]byte("x"), n)}, nil)
	}
	if code, e := put("big", 0, 101); code != http.StatusRequestEntityTooLarge || e.Code != syncapi.ErrTooLarge {
		t.Fatalf("too large: %d %+v", code, e)
	}
	if code, _ := put("one", 0, 100); code != http.StatusOK {
		t.Fatalf("one: %d", code)
	}
	if code, _ := put("two", 0, 100); code != http.StatusOK {
		t.Fatalf("two: %d", code)
	}
	if code, e := put("three", 0, 100); code != http.StatusInsufficientStorage || e.Code != syncapi.ErrQuota {
		t.Fatalf("over quota: %d %+v", code, e)
	}
	// Replacing a document counts its new size only.
	if code, _ := put("two", 1, 50); code != http.StatusOK {
		t.Fatalf("shrink: %d", code)
	}
	if code, _ := put("three", 0, 50); code != http.StatusOK {
		t.Fatalf("three: %d", code)
	}
	if code, e := put("four", 0, 1); code != http.StatusInsufficientStorage || e.Code != syncapi.ErrQuota {
		t.Fatalf("too many docs: %d %+v", code, e)
	}
	// A body far over the limit is cut off before it is read.
	req, _ := http.NewRequest("PUT", s.ts.URL+"/v1/docs/one", strings.NewReader(`{"baseRev":1,"data":"`+strings.Repeat("A", 10000)+`"}`))
	req.Header.Set("Authorization", "Bearer "+a)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge body: %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := newTestServer(t, Config{Signups: true})
	s.register(alice, key(1))
	var code int
	var e syncapi.Error
	for i := 0; i < 11; i++ {
		code, e = s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: alice, AuthKey: key(byte(10 + i))}, nil)
	}
	if code != http.StatusTooManyRequests || e.Code != syncapi.ErrRateLimited {
		t.Fatalf("after 11 wrong keys: %d %+v", code, e)
	}
	// Even the right key waits now.
	if code, _ := s.call("POST", "/v1/login", "", syncapi.LoginRequest{Account: alice, AuthKey: key(1)}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("right key while limited: %d", code)
	}
}

func TestLimiterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("first")
	}
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("second")
	}
	if ok, wait := l.allow("k"); ok || wait != time.Minute {
		t.Fatalf("third: %v %v", ok, wait)
	}
	if ok, _ := l.allow("other"); !ok {
		t.Fatal("other key")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("next window")
	}
}

func TestClientIPBehindProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if ip := (&Server{}).clientIP(r); ip != "10.0.0.2" {
		t.Fatalf("direct: %s", ip)
	}
	if ip := (&Server{cfg: Config{TrustProxy: true}}).clientIP(r); ip != "203.0.113.9" {
		t.Fatalf("proxied: %s", ip)
	}
}
