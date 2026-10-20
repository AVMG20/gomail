package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/secrets"
)

// TokenKey is the secrets key holding an account's OAuth token.
func TokenKey(accountID string) string { return "oauth:" + accountID }

// SaveToken persists a token.
func SaveToken(store secrets.Store, accountID string, tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return store.Set(TokenKey(accountID), string(b))
}

// LoadToken reads a persisted token.
func LoadToken(store secrets.Store, accountID string) (*oauth2.Token, error) {
	raw, err := store.Get(TokenKey(accountID))
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, fmt.Errorf("%w: no saved sign-in for this account", ErrReauthRequired)
		}
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// TokenSource refreshes access tokens and persists every new token
// (Microsoft rotates refresh tokens on each refresh).
type TokenSource struct {
	mu    sync.Mutex
	cfg   *oauth2.Config
	tok   *oauth2.Token
	save  func(*oauth2.Token) error
	label string // account email, for error messages
	// unsaved is set while the current token couldn't be persisted; the
	// save is retried on later calls (a rotated refresh token must not be
	// lost when the app quits).
	unsaved   bool
	lastRetry time.Time
}

// NewTokenSource wraps tok. save is called whenever the token changes.
func NewTokenSource(cfg *oauth2.Config, tok *oauth2.Token, label string, save func(*oauth2.Token) error) *TokenSource {
	return &TokenSource{cfg: cfg, tok: tok, save: save, label: label}
}

// AccessToken returns a valid access token, refreshing when needed.
func (t *TokenSource) AccessToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tok.Valid() && time.Until(t.tok.Expiry) > 30*time.Second {
		if t.unsaved && t.save != nil && time.Since(t.lastRetry) > 30*time.Second {
			t.lastRetry = time.Now()
			t.unsaved = t.save(t.tok) != nil
		}
		return t.tok.AccessToken, nil
	}
	if t.tok.RefreshToken == "" {
		return "", fmt.Errorf("%w for %s: please sign in again", ErrReauthRequired, t.label)
	}
	old := *t.tok
	old.Expiry = time.Now().Add(-time.Minute) // force refresh
	nt, err := t.cfg.TokenSource(ctx, &old).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.ErrorCode == "interaction_required" || re.ErrorCode == "invalid_client") {
			return "", fmt.Errorf("%w for %s: %s. Please sign in again", ErrReauthRequired, t.label, strings.TrimSuffix(oauthErrorMessage(err), "."))
		}
		if errors.As(err, &re) {
			return "", fmt.Errorf("could not refresh the sign-in for %s: %s", t.label, oauthErrorMessage(err))
		}
		return "", fmt.Errorf("could not refresh the sign-in for %s: %w", t.label, err) // keeps network errors detectable
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = old.RefreshToken
	}
	t.tok = nt
	if t.save != nil {
		if err := t.save(nt); err != nil {
			t.unsaved, t.lastRetry = true, time.Now()
			return "", fmt.Errorf("could not store the refreshed sign-in: %w", err)
		}
	}
	t.unsaved = false
	return nt.AccessToken, nil
}

// Invalidate forces a refresh on the next AccessToken call.
func (t *TokenSource) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tok.Expiry = time.Now().Add(-time.Minute)
	t.tok.AccessToken = ""
}
