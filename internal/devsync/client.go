package devsync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/syncapi"
)

// DefaultServer is the sync server gomail suggests.
const DefaultServer = "https://gomail.avmg.nl"

// NormalizeServer checks a server URL: https, or http for this computer
// only (local testing). "" means DefaultServer.
func NormalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultServer
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enter the server address, like https://gomail.example.com")
	}
	host := u.Hostname()
	local := host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("the server address must start with https://")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// APIError is an error response of the server.
type APIError struct {
	Status int
	Body   syncapi.Error
}

func (e *APIError) Error() string {
	if e.Body.Message != "" {
		return e.Body.Message
	}
	return fmt.Sprintf("the sync server answered %d", e.Status)
}

// IsCode reports whether err is a server error with that code.
func IsCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Body.Code == code
}

// client talks to one sync server account. It signs in on first use and
// again when the session expires.
type client struct {
	base     string
	username string
	authKey  []byte
	http     *http.Client

	mu    sync.Mutex
	token string
}

func newClient(base, username string, authKey []byte, hc *http.Client) *client {
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	return &client{base: base, username: username, authKey: authKey, http: hc}
}

func (c *client) do(ctx context.Context, method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach the sync server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		ae := &APIError{Status: resp.StatusCode}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(data, &ae.Body) != nil || ae.Body.Code == "" {
			ae.Body = syncapi.Error{Message: fmt.Sprintf("the sync server answered %s", resp.Status)}
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("the sync server sent an unexpected answer: %w", err)
	}
	return nil
}

func (c *client) authKeyB64() string { return base64.StdEncoding.EncodeToString(c.authKey) }

func (c *client) register(ctx context.Context, signupCode string) error {
	return c.do(ctx, http.MethodPost, "/v1/register", "", syncapi.RegisterRequest{Username: c.username, AuthKey: c.authKeyB64(), SignupCode: signupCode}, nil)
}

func (c *client) login(ctx context.Context) (string, error) {
	var res syncapi.LoginResponse
	if err := c.do(ctx, http.MethodPost, "/v1/login", "", syncapi.LoginRequest{Username: c.username, AuthKey: c.authKeyB64()}, &res); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.token = res.Token
	c.mu.Unlock()
	return res.Token, nil
}

// authed runs a request with the session, signing in (again) when needed.
func (c *client) authed(ctx context.Context, method, path string, in, out any) error {
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if token != "" {
		err := c.do(ctx, method, path, token, in, out)
		if !IsCode(err, syncapi.ErrUnauthorized) {
			return err
		}
	}
	token, err := c.login(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, token, in, out)
}

func (c *client) list(ctx context.Context) (syncapi.DocList, error) {
	var l syncapi.DocList
	err := c.authed(ctx, http.MethodGet, "/v1/docs", nil, &l)
	return l, err
}

func (c *client) get(ctx context.Context, name string) (syncapi.Doc, error) {
	var d syncapi.Doc
	err := c.authed(ctx, http.MethodGet, "/v1/docs/"+name, nil, &d)
	return d, err
}

// put stores a document; on a conflict the error is an *APIError with the
// current revision.
func (c *client) put(ctx context.Context, name string, baseRev int64, data []byte) (int64, error) {
	var r syncapi.PutResponse
	err := c.authed(ctx, http.MethodPut, "/v1/docs/"+name, syncapi.PutRequest{BaseRev: baseRev, Data: data}, &r)
	return r.Rev, err
}
