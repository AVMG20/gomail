// Package ai is the OpenRouter client: model listing, chat completions
// (grammar fixes, summaries, reply drafts) and the Decisions API used with
// the Jev model for label classification.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
)

// BaseURL is the OpenRouter API root (overridable in tests).
const BaseURL = "https://openrouter.ai/api"

// ErrNoKey is returned when no OpenRouter key is configured.
var ErrNoKey = errors.New("add your OpenRouter API key in Settings to use AI features")

// KeyFunc returns the configured OpenRouter API key ("" if none).
type KeyFunc func() (string, error)

// Client is an OpenRouter client.
type Client struct {
	base string
	key  KeyFunc
	http *http.Client
	// Sleep replaces backoff sleeping (tests).
	Sleep func(context.Context, time.Duration) error

	models *modelCache
}

// New creates a client. base "" = production; cacheFile "" disables the
// on-disk model cache.
func New(base string, key KeyFunc, cacheFile string) *Client {
	if base == "" {
		base = BaseURL
	}
	return &Client{base: strings.TrimRight(base, "/"), key: key, models: &modelCache{file: cacheFile}}
}

// SetHTTPClient overrides the HTTP client (tests).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h }

var defaultHeaders = map[string]string{
	"HTTP-Referer": "https://github.com/AVMG20/gomail",
	"X-Title":      "gomail",
}

func retryable(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504 || status == 524 || status == 529
}

// api returns an httpx client; authenticated requires a key.
func (c *Client) api(authenticated bool) (*httpx.Client, error) {
	hc := &httpx.Client{BaseURL: c.base, Headers: defaultHeaders, Service: "OpenRouter", HTTP: c.http,
		RetryStatus: retryable, MaxRetries: 4, Sleep: c.Sleep}
	if authenticated {
		key, err := c.key()
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, ErrNoKey
		}
		hc.Tokens = httpx.StaticToken(key)
	}
	return hc, nil
}

// friendly maps OpenRouter errors to actionable messages.
func friendly(err error) error {
	var e *httpx.Error
	if !errors.As(err, &e) {
		return err
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return errors.New("OpenRouter rejected the API key. Check the key in Settings")
	case http.StatusPaymentRequired:
		return errors.New("your OpenRouter account has insufficient credits. Add credits at https://openrouter.ai/settings/credits")
	case http.StatusForbidden:
		return fmt.Errorf("OpenRouter refused the request: %s", e.Message)
	case http.StatusNotFound:
		return fmt.Errorf("OpenRouter could not find that model or endpoint: %s", e.Message)
	case http.StatusRequestEntityTooLarge:
		return errors.New("the request is too large for the model")
	case http.StatusTooManyRequests:
		return errors.New("OpenRouter is rate limiting requests; please try again in a moment")
	}
	return err
}
