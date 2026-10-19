package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/secrets"
)

// fakeIDP is a minimal OAuth server: /token checks PKCE.
func fakeIDP(t *testing.T, challenge *string, refreshes *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != *challenge {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"PKCE verification failed"}`)
				return
			}
			if r.Form.Get("code") != "good-code" || r.Form.Get("client_id") != "cid" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"me@example.com","name":"Me Myself"}`))
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "refresh_token": "rt1", "expires_in": 3600,
				"token_type": "Bearer", "id_token": "x." + claims + ".sig"})
		case "refresh_token":
			n := atomic.AddInt32(refreshes, 1)
			if r.Form.Get("refresh_token") == "revoked" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
				return
			}
			// Rotate the refresh token like Microsoft does.
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-refreshed", "refresh_token": "rt" + string(rune('1'+n)),
				"expires_in": 3600, "token_type": "Bearer"})
		}
	}))
}

func withGoogleEndpoint(t *testing.T, srv *httptest.Server) {
	old := GoogleEndpoint
	GoogleEndpoint = oauth2.Endpoint{AuthURL: srv.URL + "/auth", TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
	t.Cleanup(func() { GoogleEndpoint = old })
}

func TestLoginPKCEAndState(t *testing.T) {
	var challenge string
	var refreshes int32
	srv := fakeIDP(t, &challenge, &refreshes)
	defer srv.Close()
	withGoogleEndpoint(t, srv)

	creds := Credentials{GoogleClientID: "cid", GoogleClientSecret: "secret"}
	tok, err := Login(context.Background(), models.ProviderGmail, creds, func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
			t.Errorf("auth url params: %v", q)
		}
		if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
			t.Errorf("redirect %s", q.Get("redirect_uri"))
		}
		if !strings.Contains(q.Get("scope"), "gmail.modify") {
			t.Errorf("scope %s", q.Get("scope"))
		}
		challenge = q.Get("code_challenge")
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=good-code&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.Contains(string(body), "signed in") {
					t.Errorf("page: %s", body)
				}
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at1" || tok.RefreshToken != "rt1" {
		t.Fatalf("token %+v", tok)
	}
	id := IdentityFromToken(tok)
	if id.Email != "me@example.com" || id.Name != "Me Myself" {
		t.Fatalf("identity %+v", id)
	}
}

func TestLoginRejectsBadState(t *testing.T) {
	var challenge string
	var refreshes int32
	srv := fakeIDP(t, &challenge, &refreshes)
	defer srv.Close()
	withGoogleEndpoint(t, srv)
	_, err := Login(context.Background(), models.ProviderGmail, Credentials{GoogleClientID: "cid"}, func(authURL string) error {
		u, _ := url.Parse(authURL)
		go http.Get(u.Query().Get("redirect_uri") + "?code=good-code&state=forged")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginCancelAndDenied(t *testing.T) {
	var challenge string
	var refreshes int32
	srv := fakeIDP(t, &challenge, &refreshes)
	defer srv.Close()
	withGoogleEndpoint(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := Login(ctx, models.ProviderGmail, Credentials{GoogleClientID: "cid"}, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel: %v", err)
	}
	_, err = Login(context.Background(), models.ProviderGmail, Credentials{GoogleClientID: "cid"}, func(authURL string) error {
		u, _ := url.Parse(authURL)
		go http.Get(u.Query().Get("redirect_uri") + "?error=access_denied&state=" + url.QueryEscape(u.Query().Get("state")))
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("denied: %v", err)
	}
}

func TestNotConfiguredAndPrecedence(t *testing.T) {
	t.Setenv(EnvGoogleClientID, "")
	t.Setenv(EnvMicrosoftClientID, "")
	_, err := Config(models.ProviderGmail, Resolve(models.Settings{}), "")
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("gmail: %v", err)
	}
	_, err = Config(models.ProviderOutlook, Resolve(models.Settings{}), "")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("outlook: %v", err)
	}
	DefaultGoogleClientID, DefaultGoogleClientSecret = "ld-id", "ld-secret"
	defer func() { DefaultGoogleClientID, DefaultGoogleClientSecret = "", "" }()
	if c := Resolve(models.Settings{}); c.GoogleClientID != "ld-id" || c.GoogleClientSecret != "ld-secret" {
		t.Fatalf("ldflags: %+v", c)
	}
	t.Setenv(EnvGoogleClientID, "env-id")
	t.Setenv(EnvGoogleClientSecret, "env-secret")
	if c := Resolve(models.Settings{}); c.GoogleClientID != "env-id" || c.GoogleClientSecret != "env-secret" {
		t.Fatalf("env: %+v", c)
	}
	if c := Resolve(models.Settings{GoogleClientID: "s-id", GoogleClientSecret: "s-secret"}); c.GoogleClientID != "s-id" || c.GoogleClientSecret != "s-secret" {
		t.Fatalf("settings: %+v", c)
	}
	cfg, _ := Config(models.ProviderOutlook, Credentials{MicrosoftClientID: "ms", MicrosoftTenant: "organizations"}, "")
	if !strings.Contains(cfg.Endpoint.TokenURL, "/organizations/oauth2/v2.0/token") || cfg.ClientSecret != "" {
		t.Fatalf("ms cfg %+v", cfg.Endpoint)
	}
}

func TestTokenSourceRefreshPersistsRotation(t *testing.T) {
	var challenge string
	var refreshes int32
	srv := fakeIDP(t, &challenge, &refreshes)
	defer srv.Close()
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	store := secrets.NewMemory()
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "rt1", Expiry: time.Now().Add(-time.Hour)}
	ts := NewTokenSource(cfg, expired, "me@example.com", func(t *oauth2.Token) error { return SaveToken(store, "acc", t) })
	at, err := ts.AccessToken(context.Background())
	if err != nil || at != "at-refreshed" {
		t.Fatal(at, err)
	}
	saved, err := LoadToken(store, "acc")
	if err != nil || saved.RefreshToken != "rt2" {
		t.Fatalf("rotated refresh token not persisted: %+v %v", saved, err)
	}
	// Valid token: no refresh.
	ts.AccessToken(context.Background())
	if refreshes != 1 {
		t.Fatalf("refreshes %d", refreshes)
	}
	ts.Invalidate()
	ts.AccessToken(context.Background())
	if refreshes != 2 {
		t.Fatalf("refreshes after invalidate %d", refreshes)
	}
	revoked := NewTokenSource(cfg, &oauth2.Token{RefreshToken: "revoked"}, "me@example.com", nil)
	if _, err := revoked.AccessToken(context.Background()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("revoked: %v", err)
	}
}
