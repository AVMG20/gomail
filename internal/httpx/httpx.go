// Package httpx is a small JSON/REST client shared by the provider and AI
// clients: bearer auth with one forced token refresh on 401, retries with
// exponential backoff on 429/5xx honoring Retry-After, and readable errors.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TokenSource supplies bearer tokens. Invalidate forces the next Token call
// to refresh.
type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
	Invalidate()
}

// StaticToken is a TokenSource for API keys.
type StaticToken string

func (s StaticToken) AccessToken(context.Context) (string, error) { return string(s), nil }
func (StaticToken) Invalidate()                                   {}

// Client performs requests against one API.
type Client struct {
	HTTP       *http.Client
	BaseURL    string
	Tokens     TokenSource // nil = unauthenticated
	Headers    map[string]string
	MaxRetries int
	// Service is used in error messages ("Gmail", "Microsoft Graph", ...).
	Service string
	// Sleep is replaceable in tests.
	Sleep func(ctx context.Context, d time.Duration) error
	// RetryStatus decides whether a status is retryable (default: 429, 5xx).
	RetryStatus func(int) bool
	// Throttle, if set, runs before every attempt (retries included), e.g.
	// to stay under a per-user quota.
	Throttle func(ctx context.Context, r Request) error
}

// DefaultHTTPClient has sane timeouts for API calls.
var DefaultHTTPClient = &http.Client{Timeout: 90 * time.Second}

// Error is a non-2xx API response.
type Error struct {
	Service    string
	Status     int
	Code       string
	Reason     string // Google errors[].reason, e.g. "userRateLimitExceeded"
	Message    string
	RetryAfter time.Duration
}

// retryableReasons are quota errors Google reports as HTTP 403.
var retryableReasons = map[string]bool{
	"rateLimitExceeded": true, "userRateLimitExceeded": true, "backendError": true, "concurrentLimitExceeded": true,
	"RATE_LIMIT_EXCEEDED": true,
}

// Retryable reports whether the error is a transient quota/rate error.
func (e *Error) Retryable() bool {
	return retryableReasons[e.Reason] || e.RateQuota() || (e.Status == http.StatusForbidden && strings.EqualFold(e.Code, "ApplicationThrottled"))
}

// RateQuota reports a Google per-minute/per-user quota error. Its window is
// a minute, so it needs a longer backoff than a transient server error.
func (e *Error) RateQuota() bool {
	switch e.Reason {
	case "rateLimitExceeded", "userRateLimitExceeded", "RATE_LIMIT_EXCEEDED":
		return true
	}
	// "Quota exceeded for quota metric '...' and limit 'Units per minute per user'"
	return strings.HasPrefix(e.Message, "Quota exceeded") && strings.Contains(e.Message, "per minute")
}

func (e *Error) Error() string {
	svc := e.Service
	if svc == "" {
		svc = "server"
	}
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Status != http.StatusUnauthorized && e.Retryable() {
		return fmt.Sprintf("%s is rate limiting requests, please try again shortly", svc)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("%s rejected the credentials (%s)", svc, msg)
	case http.StatusForbidden:
		return fmt.Sprintf("%s denied access: %s", svc, msg)
	case http.StatusNotFound:
		return fmt.Sprintf("%s: not found (%s)", svc, msg)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("%s is rate limiting requests, please try again shortly", svc)
	}
	if e.Status >= 500 {
		return fmt.Sprintf("%s is having problems (HTTP %d): %s", svc, e.Status, msg)
	}
	return fmt.Sprintf("%s error (HTTP %d): %s", svc, e.Status, msg)
}

// StatusOf returns the HTTP status of an *Error, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func defaultRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status == 500 || status == 502 || status == 503 || status == 504
}

// Request describes one call.
type Request struct {
	Method      string
	Path        string // relative to BaseURL, or absolute URL
	Body        []byte
	ContentType string
	Headers     map[string]string
	// Idempotent allows retrying a POST after failures where the server may
	// already have acted on it (timeouts, 500/502/504). Other methods are
	// treated as idempotent. A non-idempotent POST (sending mail, creating a
	// draft) is only retried when it certainly wasn't processed: connection
	// failures before the request was sent, 429 and 503.
	Idempotent bool
}

func (r Request) idempotent() bool { return r.Idempotent || r.Method != http.MethodPost }

// notSent reports transport errors that happened before the request could
// reach the server (dialing, DNS, TLS handshake).
func notSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// Do performs the request with retries; the caller must close the body of
// a successful response. Non-2xx responses are returned as *Error.
func (c *Client) Do(ctx context.Context, r Request) (*http.Response, error) {
	hc := c.HTTP
	if hc == nil {
		hc = DefaultHTTPClient
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	retryable := c.RetryStatus
	if retryable == nil {
		retryable = defaultRetryable
	}
	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = 5
	}
	url := r.Path
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(r.Path, "/")
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if r.Body != nil {
			body = bytes.NewReader(r.Body)
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, url, body)
		if err != nil {
			return nil, err
		}
		if r.ContentType != "" {
			req.Header.Set("Content-Type", r.ContentType)
		}
		for k, v := range c.Headers {
			req.Header.Set(k, v)
		}
		for k, v := range r.Headers {
			req.Header.Set(k, v)
		}
		if c.Throttle != nil {
			if err := c.Throttle(ctx, r); err != nil {
				return nil, err
			}
		}
		if c.Tokens != nil {
			tok, err := c.Tokens.AccessToken(ctx)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < maxRetries && (r.idempotent() || notSent(err)) {
				if serr := sleep(ctx, backoff(attempt, 0)); serr != nil {
					return nil, serr
				}
				continue
			}
			return nil, fmt.Errorf("could not reach %s: %w", c.service(), err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		apiErr := readError(resp, c.service())
		if resp.StatusCode == http.StatusUnauthorized && c.Tokens != nil && !refreshed {
			refreshed = true
			c.Tokens.Invalidate()
			continue
		}
		// A quota error (403 with a rate-limit reason) wasn't acted on; a
		// 500 is ambiguous even when Google names it "backendError".
		safeToRetry := r.idempotent() || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable ||
			(apiErr.Retryable() && resp.StatusCode < 500)
		if (retryable(resp.StatusCode) || apiErr.Retryable()) && safeToRetry && attempt < maxRetries {
			d := backoff(attempt, apiErr.RetryAfter)
			if apiErr.RateQuota() && apiErr.RetryAfter == 0 {
				d = QuotaBackoff(attempt)
			}
			slog.Debug("retrying request", "service", c.service(), "status", resp.StatusCode, "wait", d)
			if serr := sleep(ctx, d); serr != nil {
				return nil, serr
			}
			continue
		}
		return nil, apiErr
	}
}

func (c *Client) service() string {
	if c.Service != "" {
		return c.Service
	}
	return "server"
}

func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, 2*time.Minute)
	}
	d := time.Duration(500<<attempt) * time.Millisecond
	d = min(d, 30*time.Second)
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// QuotaBackoff waits out a per-minute quota window: 5-10s, 10-20s, 20-40s,
// then 30-60s, so five retries span well over a minute.
func QuotaBackoff(attempt int) time.Duration {
	d := min(10*time.Second<<attempt, time.Minute)
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

func readError(resp *http.Response, service string) *Error {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &Error{Service: service, Status: resp.StatusCode}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil {
			e.RetryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			e.RetryAfter = time.Until(t)
		}
	}
	// Google / Graph / OpenRouter: {"error": {"code": ..., "message": ...}}
	// OAuth: {"error": "invalid_grant", "error_description": "..."}
	var env struct {
		Error            json.RawMessage `json:"error"`
		ErrorDescription string          `json:"error_description"`
		Message          string          `json:"message"`
	}
	if json.Unmarshal(data, &env) == nil {
		var obj struct {
			Code    any    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		}
		var str string
		switch {
		case json.Unmarshal(env.Error, &obj) == nil && (obj.Message != "" || obj.Code != nil):
			e.Message = obj.Message
			if len(obj.Errors) > 0 {
				e.Reason = obj.Errors[0].Reason
			}
			for _, d := range obj.Details {
				if e.Reason == "" && d.Reason != "" {
					e.Reason = d.Reason // google.rpc.ErrorInfo
				}
			}
			if s, ok := obj.Code.(string); ok {
				e.Code = s
			} else if obj.Status != "" {
				e.Code = obj.Status
			} else if obj.Code != nil {
				e.Code = fmt.Sprint(obj.Code)
			}
		case json.Unmarshal(env.Error, &str) == nil:
			e.Code, e.Message = str, env.ErrorDescription
		case env.Message != "":
			e.Message = env.Message
		}
	}
	if e.Message == "" {
		// Only short plain-text bodies are worth showing; HTML error pages
		// (proxies, load balancers) and JSON we couldn't read are not.
		txt := strings.TrimSpace(string(data))
		if txt != "" && len(txt) <= 200 && !strings.ContainsAny(txt, "<>{}\n") && utf8.ValidString(txt) {
			e.Message = txt
		}
	}
	return e
}

// JSON performs a request with an optional JSON body and decodes a JSON
// response into out (if non-nil).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	return c.doJSON(ctx, Request{Method: method, Path: path}, in, out)
}

// JSONRetry is JSON for a POST that is safe to repeat (batch modify,
// read-only queries sent as POST).
func (c *Client) JSONRetry(ctx context.Context, method, path string, in, out any) error {
	return c.doJSON(ctx, Request{Method: method, Path: path, Idempotent: true}, in, out)
}

// JSONHeaders is JSON with extra request headers (e.g. If-Match).
func (c *Client) JSONHeaders(ctx context.Context, method, path string, headers map[string]string, in, out any) error {
	return c.doJSON(ctx, Request{Method: method, Path: path, Headers: headers}, in, out)
}

func (c *Client) doJSON(ctx context.Context, r Request, in, out any) error {
	var body []byte
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = b, "application/json"
	}
	r.Body, r.ContentType = body, ct
	resp, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("unexpected response from %s: %w", c.service(), err)
	}
	return nil
}

// Bytes performs a GET and returns the body.
func (c *Client) Bytes(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.Do(ctx, Request{Method: http.MethodGet, Path: path})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
