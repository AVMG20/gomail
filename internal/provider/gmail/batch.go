package gmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/httpx"
	"github.com/AVMG20/gomail/internal/provider"
)

// Batch requests: https://developers.google.com/workspace/gmail/api/guides/batch.
// A batch holds up to 100 calls, but Google advises at most 50 to avoid
// rateLimitExceeded; each call still costs its own quota.
const (
	batchPath    = "/batch/gmail/v1"
	batchSize    = 50
	batchWorkers = 2
	// batchAttempts bounds how often a throttled or failing call is retried.
	batchAttempts = 6
	// maxPartBytes caps one answer (metadata is a few KB).
	maxPartBytes = 4 << 20
)

// getResult is the answer to one GET: its JSON body, or err (an
// *httpx.Error for an API error).
type getResult struct {
	body json.RawMessage
	err  error
}

// errBatchUnsupported means the batch endpoint can't be used here.
var errBatchUnsupported = errors.New("Gmail batch requests are unavailable")

// costKey carries the quota cost of a batch request to the throttle.
type costKey struct{}

// getAll performs GETs (paths relative to users/me) through the batch
// endpoint, batchSize calls per request and batchWorkers requests at a time.
// Throttled (429, quota 403) and failing (5xx) calls are retried with
// backoff, honouring Retry-After. done reports settled calls. It returns
// the first error other than a 404 (those stay in their result); when the
// batch endpoint is unavailable it falls back to one request per call.
func (c *Client) getAll(ctx context.Context, paths []string, done func(n int)) ([]getResult, error) {
	out := make([]getResult, len(paths))
	if done == nil {
		done = func(int) {}
	}
	chunks := provider.Chunk(indexes(len(paths)), batchSize)
	err := provider.Parallel(ctx, batchWorkers, chunks, func(ctx context.Context, chunk []int) error {
		return c.batch(ctx, paths, chunk, out, done)
	})
	return out, err
}

// batch runs the calls idx of paths, retrying the ones that failed
// transiently, and stores their results in out.
func (c *Client) batch(ctx context.Context, paths []string, idx []int, out []getResult, done func(int)) error {
	pending := idx
	refreshed := false
	for attempt := 0; len(pending) > 0; attempt++ {
		if c.noBatch.Load() {
			return c.getEach(ctx, paths, pending, out, done)
		}
		parts, err := c.postBatch(ctx, paths, pending)
		if errors.Is(err, errBatchUnsupported) {
			if !c.noBatch.Swap(true) {
				slog.Warn("Gmail batch requests failed; fetching one by one", "err", err)
			}
			return c.getEach(ctx, paths, pending, out, done)
		}
		if err != nil {
			return err
		}
		var retry []int
		var wait time.Duration
		var last error
		settled := 0
		report := func() {
			if settled > 0 {
				done(settled)
			}
		}
		refreshNow := false
		for _, i := range pending {
			p, ok := parts[i]
			if !ok {
				retry = append(retry, i)
				last = errors.New("Gmail returned an incomplete batch response")
				continue
			}
			if p.status >= 200 && p.status < 300 {
				out[i] = getResult{body: p.body}
				settled++
				continue
			}
			apiErr := partError(p)
			switch {
			case p.status == http.StatusNotFound:
				out[i] = getResult{err: apiErr}
				settled++
				continue
			case p.status == http.StatusUnauthorized && (!refreshed || refreshNow) && c.api.Tokens != nil:
				// Like httpx: one forced token refresh, then a 401 is final.
				if !refreshed {
					refreshed, refreshNow = true, true
					c.api.Tokens.Invalidate()
				}
			case transient(apiErr) && attempt+1 < batchAttempts:
				wait = max(wait, apiErr.RetryAfter)
			default:
				report()
				return apiErr
			}
			retry = append(retry, i)
			last = apiErr
		}
		report()
		pending = retry
		if len(pending) == 0 {
			break
		}
		if attempt+1 >= batchAttempts {
			return last
		}
		if wait == 0 {
			d := min(time.Second<<attempt, 30*time.Second)
			wait = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
		}
		slog.Debug("retrying Gmail batch calls", "calls", len(pending), "wait", wait)
		if err := c.sleep(ctx, min(wait, 2*time.Minute)); err != nil {
			return err
		}
	}
	return nil
}

// transient reports a call worth retrying: throttled, a quota error, or a
// server error (GETs are safe to repeat).
func transient(e *httpx.Error) bool {
	switch e.Status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return e.Retryable()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.api.Sleep != nil {
		return c.api.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// getEach is the fallback without batching: one request per call.
func (c *Client) getEach(ctx context.Context, paths []string, idx []int, out []getResult, done func(int)) error {
	return provider.Parallel(ctx, workers, idx, func(ctx context.Context, i int) error {
		var body json.RawMessage
		err := c.api.JSON(ctx, http.MethodGet, paths[i], nil, &body)
		done(1)
		if err != nil && httpx.StatusOf(err) != http.StatusNotFound {
			return err
		}
		out[i] = getResult{body: body, err: err}
		return nil
	})
}

// batchPart is one answer of a batch response.
type batchPart struct {
	status int
	header http.Header
	body   []byte
}

// postBatch sends one batch request for the calls idx of paths and returns
// the answers by call index. Answers missing from a cut-off response are
// simply absent.
func (c *Client) postBatch(ctx context.Context, paths []string, idx []int) (map[int]batchPart, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	cost := 0.0
	for _, i := range idx {
		w, err := mw.CreatePart(map[string][]string{"Content-Type": {"application/http"}, "Content-ID": {"<" + strconv.Itoa(i) + ">"}})
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(w, "GET /gmail/v1/users/me/%s HTTP/1.1\r\n\r\n", paths[i])
		cost += quotaCost(http.MethodGet, paths[i])
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	resp, err := c.api.Do(context.WithValue(ctx, costKey{}, cost), httpx.Request{
		Method: http.MethodPost, Path: c.base + batchPath, Body: buf.Bytes(),
		ContentType: "multipart/mixed; boundary=" + mw.Boundary(), Idempotent: true,
	})
	switch httpx.StatusOf(err) {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		// Not something a retry fixes: a proxy or server without batching.
		return nil, fmt.Errorf("%w: %v", errBatchUnsupported, err)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	mt, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" || params["boundary"] == "" {
		return nil, fmt.Errorf("%w: response of type %q", errBatchUnsupported, resp.Header.Get("Content-Type"))
	}
	out := map[int]batchPart{}
	mr := multipart.NewReader(resp.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			// io.EOF, or a cut-off response: what's missing is retried.
			break
		}
		id := strings.TrimPrefix(strings.Trim(p.Header.Get("Content-ID"), "<> "), "response-")
		i, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		r, err := http.ReadResponse(bufio.NewReader(p), nil)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxPartBytes))
		r.Body.Close()
		if err != nil {
			continue
		}
		out[i] = batchPart{status: r.StatusCode, header: r.Header, body: body}
	}
	return out, nil
}

// partError reads a failed answer like httpx reads a failed response.
func partError(p batchPart) *httpx.Error {
	e := &httpx.Error{Service: "Gmail", Status: p.status}
	if s, err := strconv.Atoi(strings.TrimSpace(p.header.Get("Retry-After"))); err == nil {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	var env struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(p.body, &env) == nil {
		e.Code, e.Message = env.Error.Status, env.Error.Message
		if len(env.Error.Errors) > 0 {
			e.Reason = env.Error.Errors[0].Reason
		}
	}
	return e
}
