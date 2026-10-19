package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeTokens struct {
	n, invalidated int32
}

func (f *fakeTokens) AccessToken(context.Context) (string, error) {
	n := atomic.AddInt32(&f.n, 1)
	if atomic.LoadInt32(&f.invalidated) > 0 {
		return "fresh", nil
	}
	_ = n
	return "stale", nil
}
func (f *fakeTokens) Invalidate() { atomic.AddInt32(&f.invalidated, 1) }

func TestRetryAndRefresh(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		switch {
		case r.Header.Get("Authorization") == "Bearer stale":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":401,"message":"expired"}}`))
		case n == 2:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
		case n == 3:
			w.WriteHeader(503)
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	var slept []time.Duration
	tok := &fakeTokens{}
	c := &Client{BaseURL: srv.URL, Tokens: tok, Service: "Test", Sleep: func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}}
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), "GET", "/x", nil, &out); err != nil || !out.OK {
		t.Fatal(err, out)
	}
	if calls != 4 || tok.invalidated != 1 {
		t.Fatalf("calls %d invalidated %d", calls, tok.invalidated)
	}
	if len(slept) != 2 || slept[0] != 3*time.Second {
		t.Fatalf("slept %v", slept)
	}
}

func TestErrorMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/google":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"Gmail API has not been used in project 123","status":"PERMISSION_DENIED"}}`))
		case "/graph":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":"ErrorInvalidIdMalformed","message":"Id is malformed."}}`))
		case "/oauth":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Service: "Svc"}
	for path, want := range map[string]string{
		"/google": "Svc denied access: Gmail API has not been used in project 123",
		"/graph":  "Svc error (HTTP 400): Id is malformed.",
		"/oauth":  "Token has been expired or revoked.",
	} {
		err := c.JSON(context.Background(), "GET", path, nil, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
		if StatusOf(err) == 0 {
			t.Errorf("%s: no status", path)
		}
	}
}

func TestGoogleQuota403IsRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":403,"message":"User-rate limit exceeded.","errors":[{"domain":"usageLimits","reason":"userRateLimitExceeded"}],"status":"PERMISSION_DENIED"}}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Sleep: func(context.Context, time.Duration) error { return nil }}
	if err := c.JSON(context.Background(), "GET", "/", nil, nil); err != nil || calls != 3 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	// A real permission error is not retried.
	calls = 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":403,"message":"Insufficient Permission","errors":[{"reason":"insufficientPermissions"}]}}`))
	}))
	defer srv2.Close()
	c.BaseURL = srv2.URL
	if err := c.JSON(context.Background(), "GET", "/", nil, nil); err == nil || calls != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
}
