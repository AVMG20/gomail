package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/AVMG20/gomail/internal/secrets"
)

// A rotated token that couldn't be stored is stored on a later call.
func TestTokenSaveIsRetried(t *testing.T) {
	var challenge string
	var refreshes int32
	srv := fakeIDP(t, &challenge, &refreshes)
	defer srv.Close()
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	store := secrets.NewMemory()
	failing := true
	ts := NewTokenSource(cfg, &oauth2.Token{RefreshToken: "rt1", Expiry: time.Now().Add(-time.Hour)}, "me@example.com", func(t *oauth2.Token) error {
		if failing {
			return errors.New("keyring locked")
		}
		return SaveToken(store, "acc", t)
	})
	if _, err := ts.AccessToken(context.Background()); err == nil {
		t.Fatal("save failure not reported")
	}
	failing = false
	ts.mu.Lock()
	ts.lastRetry = time.Time{}
	ts.mu.Unlock()
	if _, err := ts.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadToken(store, "acc")
	if err != nil || saved.RefreshToken != "rt2" {
		t.Fatalf("rotated token never stored: %+v %v", saved, err)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes %d", refreshes)
	}
}
