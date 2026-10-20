// Package fakes provides stateful in-process fakes of the external services
// gomail talks to (OAuth token endpoints, the Gmail REST API, Microsoft
// Graph and the OpenRouter Decisions API) for end-to-end tests.
package fakes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

// OAuth is a fake authorization server for Google and Microsoft. The token
// endpoint answers at any path ending in "/token" (Google: /token,
// Microsoft: /{tenant}/oauth2/v2.0/token).
type OAuth struct {
	Server *httptest.Server

	mu     sync.Mutex
	n      int
	codes  map[string]string // code -> PKCE challenge
	scopes map[string]string // code or refresh token -> granted scopes
	// Grant, if set, filters the scopes the user "allows" on the consent
	// page (e.g. to simulate unticking calendar access).
	Grant      func(requested string) string
	access     map[string]bool // valid access tokens
	refresh    map[string]bool // valid refresh tokens
	exchanges  int
	refreshes  int
	pkceFailed int
}

// Stats returns how many code exchanges, refreshes and PKCE failures the
// token endpoint saw.
func (o *OAuth) Stats() (exchanges, refreshes, pkceFailed int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.exchanges, o.refreshes, o.pkceFailed
}

// NewOAuth starts the server.
func NewOAuth() *OAuth {
	o := &OAuth{codes: map[string]string{}, scopes: map[string]string{}, access: map[string]bool{}, refresh: map[string]bool{}}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	return o
}

// Close stops the server.
func (o *OAuth) Close() { o.Server.Close() }

// URL is the server's base URL.
func (o *OAuth) URL() string { return o.Server.URL }

// Valid reports whether an access token is currently accepted. Resource
// servers use it to authenticate requests.
func (o *OAuth) Valid(token string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.access[token]
}

// ExpireAccessTokens makes every issued access token invalid (the resource
// servers then answer 401).
func (o *OAuth) ExpireAccessTokens() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.access = map[string]bool{}
}

// RevokeRefreshTokens makes every refresh token fail with invalid_grant.
func (o *OAuth) RevokeRefreshTokens() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refresh = map[string]bool{}
}

// Consent plays the user in the browser: it reads the authorization URL
// the app opened and calls the loopback redirect with a code (and the
// app's state), like the consent page would. Use it as the UI's OpenURL.
func (o *OAuth) Consent(authURL string) error {
	u, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := u.Query()
	redirect, state := q.Get("redirect_uri"), q.Get("state")
	if redirect == "" || state == "" {
		return errors.New("fake oauth: authorization URL without redirect_uri or state")
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		return errors.New("fake oauth: authorization URL without PKCE")
	}
	o.mu.Lock()
	o.n++
	code := fmt.Sprintf("code-%d", o.n)
	o.codes[code] = q.Get("code_challenge")
	granted := q.Get("scope")
	if o.Grant != nil {
		granted = o.Grant(granted)
	}
	o.scopes[code] = granted
	o.mu.Unlock()
	go func() {
		resp, err := http.Get(redirect + "?" + url.Values{"code": {code}, "state": {state}}.Encode())
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

func (o *OAuth) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/token") || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	fail := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "fake: " + code})
	}
	if r.Form.Get("client_id") == "" {
		fail("invalid_client")
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	scope := ""
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		challenge, ok := o.codes[r.Form.Get("code")]
		if !ok {
			fail("invalid_grant")
			return
		}
		delete(o.codes, r.Form.Get("code"))
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			o.pkceFailed++
			fail("invalid_grant")
			return
		}
		o.exchanges++
		scope = o.scopes[r.Form.Get("code")]
	case "refresh_token":
		if !o.refresh[r.Form.Get("refresh_token")] {
			fail("invalid_grant")
			return
		}
		o.refreshes++
		scope = o.scopes[r.Form.Get("refresh_token")]
	default:
		fail("unsupported_grant_type")
		return
	}
	o.n++
	at, rt := fmt.Sprintf("at-%d", o.n), fmt.Sprintf("rt-%d", o.n)
	o.access[at], o.refresh[rt] = true, true
	o.scopes[rt] = scope
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": at, "refresh_token": rt, "token_type": "Bearer", "expires_in": 3600, "scope": scope})
}

// bearer extracts the access token of a request.
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg, "status": code}})
}
