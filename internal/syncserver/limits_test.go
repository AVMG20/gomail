package syncserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/syncapi"
)

// A server takes at most MaxAccounts accounts.
func TestMaxAccounts(t *testing.T) {
	s := newTestServer(t, Config{Signups: true, MaxAccounts: 1})
	s.register(alice, key(1))
	if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: bob, AuthKey: key(2)}, nil); code != http.StatusForbidden || e.Code != syncapi.ErrSignupsClosed {
		t.Fatalf("over the limit: %d %+v", code, e)
	}
	if code, e := s.call("POST", "/v1/register", "", syncapi.RegisterRequest{Account: alice, AuthKey: key(2)}, nil); code != http.StatusConflict || e.Code != syncapi.ErrAccountExists {
		t.Fatalf("existing: %d %+v", code, e)
	}
	s.login(alice, key(1))
}

// loginFrom logs in as if from addr (through the proxy).
func (s *testServer) loginFrom(addr, account, k string) int {
	s.t.Helper()
	b, _ := json.Marshal(syncapi.LoginRequest{Account: account, AuthKey: k})
	req, _ := http.NewRequest("POST", s.ts.URL+"/v1/login", bytes.NewReader(b))
	req.Header.Set("X-Forwarded-For", addr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Wrong keys from one address don't lock the account's devices out
// elsewhere.
func TestLoginLimitIsPerAddress(t *testing.T) {
	s := newTestServer(t, Config{Signups: true, TrustProxy: true})
	s.register(alice, key(1))
	for i := range 11 {
		s.loginFrom("203.0.113.66", alice, key(byte(10+i)))
	}
	if code := s.loginFrom("203.0.113.66", alice, key(1)); code != http.StatusTooManyRequests {
		t.Fatalf("the guessing address: %d", code)
	}
	if code := s.loginFrom("198.51.100.1", alice, key(1)); code != http.StatusOK {
		t.Fatalf("the account's device: %d", code)
	}
}

// Finished windows are forgotten once per window, not scanned on every
// attempt.
func TestLimiterForgets(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	for i := range 100 {
		l.allow(fmt.Sprint("k", i))
	}
	if l.sweeps != 1 || len(l.hits) != 100 {
		t.Fatalf("sweeps %d keys %d", l.sweeps, len(l.hits))
	}
	now = now.Add(time.Minute)
	l.allow("new")
	if len(l.hits) != 1 || l.sweeps != 2 {
		t.Fatalf("after the window: sweeps %d keys %d", l.sweeps, len(l.hits))
	}
}

// slowBody sends parts with a pause before each.
type slowBody struct {
	parts []string
	pause time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(b.parts) == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.pause)
	n := copy(p, b.parts[0])
	b.parts[0] = b.parts[0][n:]
	if b.parts[0] == "" {
		b.parts = b.parts[1:]
	}
	return n, nil
}

func (s *testServer) slowPut(token, name string, body *slowBody) (int, syncapi.Error) {
	s.t.Helper()
	req, _ := http.NewRequest("PUT", s.ts.URL+"/v1/docs/"+name, body)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var e syncapi.Error
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return resp.StatusCode, e
}

// A document may take its time on a slow connection; one that stops
// coming gets a clear answer.
func TestSlowUpload(t *testing.T) {
	s := newTestServer(t, Config{Signups: true, RequestTimeout: 100 * time.Millisecond, DocTimeout: 2 * time.Second})
	s.register(alice, key(1))
	a := s.login(alice, key(1))
	body := &slowBody{parts: []string{`{"baseRev":0,`, `"data":"`, strings.Repeat("QUFB", 100), `"}`}, pause: 80 * time.Millisecond}
	if code, e := s.slowPut(a, "doc", body); code != http.StatusOK {
		t.Fatalf("slow upload: %d %+v", code, e)
	}
	// Half a body, then nothing.
	s.srv.cfg.DocTimeout = 200 * time.Millisecond
	conn, err := net.Dial("tcp", s.ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "PUT /v1/docs/other HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: 1000\r\n\r\n{\"baseRev\":0,", a)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e syncapi.Error
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != http.StatusRequestTimeout || !strings.Contains(e.Message, "too slow") {
		t.Fatalf("stalled upload: %d %+v", resp.StatusCode, e)
	}
}

// Closing waits for the requests being handled; later ones get 503.
func TestCloseWaitsForRequests(t *testing.T) {
	s := newTestServer(t, Config{Signups: true})
	s.register(alice, key(1))
	a := s.login(alice, key(1))
	type result struct {
		code int
		e    syncapi.Error
	}
	done := make(chan result, 1)
	go func() {
		code, e := s.slowPut(a, "doc", &slowBody{parts: []string{`{"baseRev":0,`, `"data":"QUFB"}`}, pause: 150 * time.Millisecond})
		done <- result{code, e}
	}()
	time.Sleep(200 * time.Millisecond) // the request is being handled
	closed := make(chan error, 1)
	go func() { closed <- s.srv.Close() }()
	select {
	case <-closed:
		t.Fatal("closed while a request was running")
	case <-time.After(50 * time.Millisecond):
	}
	if r := <-done; r.code != http.StatusOK {
		t.Fatalf("the running request: %d %+v", r.code, r.e)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if code, _ := s.call("GET", "/healthz", "", nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("after close: %d", code)
	}
}
