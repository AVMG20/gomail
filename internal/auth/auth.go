// Package auth implements OAuth 2.0 for installed apps (RFC 8252): a
// loopback redirect on 127.0.0.1, PKCE (S256) and state verification, for
// Google and the Microsoft identity platform, plus persisted, refreshing
// token sources.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/models"
)

// Default OAuth client credentials baked in by distributors via
//
//	-ldflags "-X github.com/AVMG20/gomail/internal/auth.DefaultGoogleClientID=..."
//
// Precedence: Settings value > environment variable > these defaults.
var (
	DefaultGoogleClientID     = ""
	DefaultGoogleClientSecret = ""
	DefaultMicrosoftClientID  = ""
)

// Environment variables for client credentials.
const (
	EnvGoogleClientID     = "GOMAIL_GOOGLE_CLIENT_ID"
	EnvGoogleClientSecret = "GOMAIL_GOOGLE_CLIENT_SECRET"
	EnvMicrosoftClientID  = "GOMAIL_MICROSOFT_CLIENT_ID"
	EnvMicrosoftTenant    = "GOMAIL_MICROSOFT_TENANT"
)

// Scopes requested per provider.
var (
	GoogleScopes    = []string{"openid", "email", "profile", "https://www.googleapis.com/auth/gmail.modify"}
	MicrosoftScopes = []string{"offline_access", "openid", "email", "profile",
		"https://graph.microsoft.com/Mail.ReadWrite", "https://graph.microsoft.com/Mail.Send",
		"https://graph.microsoft.com/MailboxSettings.Read", "https://graph.microsoft.com/User.Read"}
)

// Credentials are the OAuth client identifiers in effect.
type Credentials struct {
	GoogleClientID     string
	GoogleClientSecret string
	MicrosoftClientID  string
	MicrosoftTenant    string
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// Resolve applies the precedence Settings > env > ldflags defaults.
func Resolve(s models.Settings) Credentials {
	c := Credentials{
		GoogleClientID:    first(s.GoogleClientID, os.Getenv(EnvGoogleClientID), DefaultGoogleClientID),
		MicrosoftClientID: first(s.MicrosoftClientID, os.Getenv(EnvMicrosoftClientID), DefaultMicrosoftClientID),
		MicrosoftTenant:   first(s.MicrosoftTenant, os.Getenv(EnvMicrosoftTenant), "common"),
	}
	// The secret belongs to whichever client id won.
	switch c.GoogleClientID {
	case strings.TrimSpace(s.GoogleClientID):
		c.GoogleClientSecret = strings.TrimSpace(s.GoogleClientSecret)
	case strings.TrimSpace(os.Getenv(EnvGoogleClientID)):
		c.GoogleClientSecret = strings.TrimSpace(os.Getenv(EnvGoogleClientSecret))
	default:
		c.GoogleClientSecret = DefaultGoogleClientSecret
	}
	return c
}

// ErrNotConfigured is wrapped by errors explaining missing client ids.
var ErrNotConfigured = errors.New("oauth client not configured")

// ErrReauthRequired is returned when the refresh token was revoked/expired.
var ErrReauthRequired = errors.New("sign-in expired")

// Endpoints (overridable in tests).
var (
	GoogleEndpoint = oauth2.Endpoint{
		AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:  "https://oauth2.googleapis.com/token",
		AuthStyle: oauth2.AuthStyleInParams,
	}
	MicrosoftAuthority = "https://login.microsoftonline.com"
)

// Config returns the oauth2 config for a provider. redirectURL may be empty
// when only refreshing tokens.
func Config(provider string, c Credentials, redirectURL string) (*oauth2.Config, error) {
	switch provider {
	case models.ProviderGmail:
		if c.GoogleClientID == "" {
			return nil, fmt.Errorf("%w: Gmail sign-in needs a Google OAuth client. Create a \"Desktop app\" OAuth client in the Google Cloud Console (with the Gmail API enabled) and enter its Client ID and Client secret in Settings, or set %s and %s. See docs/BACKEND.md for step-by-step instructions",
				ErrNotConfigured, EnvGoogleClientID, EnvGoogleClientSecret)
		}
		return &oauth2.Config{
			ClientID: c.GoogleClientID, ClientSecret: c.GoogleClientSecret,
			Endpoint: GoogleEndpoint, Scopes: GoogleScopes, RedirectURL: redirectURL,
		}, nil
	case models.ProviderOutlook:
		if c.MicrosoftClientID == "" {
			return nil, fmt.Errorf("%w: Outlook sign-in needs a Microsoft app registration. Register a public client (\"Mobile and desktop applications\" platform, redirect URI http://localhost) in the Azure portal and enter its Application (client) ID in Settings, or set %s. See docs/BACKEND.md for step-by-step instructions",
				ErrNotConfigured, EnvMicrosoftClientID)
		}
		tenant := c.MicrosoftTenant
		if tenant == "" {
			tenant = "common"
		}
		base := strings.TrimRight(MicrosoftAuthority, "/") + "/" + tenant + "/oauth2/v2.0"
		return &oauth2.Config{
			ClientID: c.MicrosoftClientID,
			Endpoint: oauth2.Endpoint{AuthURL: base + "/authorize", TokenURL: base + "/token", AuthStyle: oauth2.AuthStyleInParams},
			Scopes:   MicrosoftScopes, RedirectURL: redirectURL,
		}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

// FlowTimeout bounds how long AddAccount waits for the browser.
var FlowTimeout = 5 * time.Minute

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Login runs the interactive authorization code flow: it starts a loopback
// server, opens the system browser at the consent page and waits for the
// redirect (or ctx cancellation / FlowTimeout).
func Login(ctx context.Context, provider string, c Credentials, openURL func(string) error) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, FlowTimeout)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not start the sign-in listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	listeners := []net.Listener{ln}
	host := "127.0.0.1"
	if provider == models.ProviderOutlook {
		// Microsoft only allows arbitrary ports for "localhost"; browsers
		// may resolve it to ::1, so listen there too when possible.
		host = "localhost"
		if ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port)); err == nil {
			listeners = append(listeners, ln6)
		}
	}
	redirect := fmt.Sprintf("http://%s:%d", host, port)
	if provider == models.ProviderGmail {
		redirect += "/"
	}
	cfg, err := Config(provider, c, redirect)
	if err != nil {
		for _, l := range listeners {
			l.Close()
		}
		return nil, err
	}
	state := randomString(24)
	verifier := oauth2.GenerateVerifier()
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if provider == models.ProviderGmail {
		opts = append(opts, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	} else {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "select_account"))
	}
	authURL := cfg.AuthCodeURL(state, opts...)

	type result struct {
		tok *oauth2.Token
		err error
	}
	done := make(chan result, 1)
	var once sync.Once
	finish := func(r result) { once.Do(func() { done <- r }) }

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") == "" && q.Get("error") == "" {
			http.NotFound(w, r) // favicon etc.
			return
		}
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			writePage(w, http.StatusBadRequest, false, "The sign-in response could not be verified (state mismatch). Please try again from gomail.")
			finish(result{err: errors.New("sign-in failed: the response could not be verified (state mismatch)")})
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			msg := "Sign-in was cancelled or denied."
			if e != "access_denied" {
				msg = "Sign-in failed: " + e
				if desc != "" {
					msg += " — " + desc
				}
			}
			writePage(w, http.StatusOK, false, msg)
			finish(result{err: errors.New(msg)})
			return
		}
		tok, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(verifier))
		if err != nil {
			writePage(w, http.StatusOK, false, "Sign-in could not be completed. You can close this window and try again.")
			finish(result{err: fmt.Errorf("sign-in failed while exchanging the authorization code: %s", oauthErrorMessage(err))})
			return
		}
		writePage(w, http.StatusOK, true, "You're signed in. You can close this window and return to gomail.")
		finish(result{tok: tok})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	for _, l := range listeners {
		go func(l net.Listener) { _ = srv.Serve(l) }(l)
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx)
	}()

	if err := openURL(authURL); err != nil {
		return nil, fmt.Errorf("could not open the browser for sign-in: %w", err)
	}
	slog.Info("oauth flow started", "provider", provider, "redirect", redirect)
	select {
	case r := <-done:
		return r.tok, r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("sign-in timed out after 5 minutes; please try again")
		}
		return nil, errors.New("sign-in was cancelled")
	}
}

func oauthErrorMessage(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorDescription != "" {
			return re.ErrorDescription
		}
		if re.ErrorCode != "" {
			return re.ErrorCode
		}
	}
	return err.Error()
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>gomail</title>
<style>
:root{color-scheme:light dark}
body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;background:Canvas;color:CanvasText}
.card{max-width:420px;padding:40px;border-radius:16px;text-align:center;box-shadow:0 10px 40px rgba(0,0,0,.12)}
.icon{font-size:48px;line-height:1;margin-bottom:16px;color:{{if .OK}}#16a34a{{else}}#dc2626{{end}}}
h1{font-size:20px;margin:0 0 8px} p{margin:0;opacity:.75;line-height:1.5}
</style></head>
<body><div class="card"><div class="icon">{{if .OK}}&#10003;{{else}}&#10007;{{end}}</div>
<h1>{{if .OK}}Signed in{{else}}Sign-in problem{{end}}</h1><p>{{.Message}}</p></div>
{{if .OK}}<script>setTimeout(function(){window.close()},1500)</script>{{end}}
</body></html>`))

func writePage(w http.ResponseWriter, status int, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, struct {
		OK      bool
		Message string
	}{ok, msg})
}

// Identity is the user info found in an OpenID Connect id_token.
type Identity struct {
	Email string
	Name  string
}

// IdentityFromToken decodes the id_token returned with tok. The token comes
// straight from the provider's token endpoint over TLS, so its signature is
// not verified; it is only used for display defaults.
func IdentityFromToken(tok *oauth2.Token) Identity {
	raw, _ := tok.Extra("id_token").(string)
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return Identity{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return Identity{}
	}
	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	_ = json.Unmarshal(payload, &claims)
	id := Identity{Email: claims.Email, Name: claims.Name}
	if id.Email == "" && strings.Contains(claims.PreferredUsername, "@") {
		id.Email = claims.PreferredUsername
	}
	return id
}
