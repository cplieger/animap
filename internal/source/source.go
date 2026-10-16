// Package source is animap's one outbound HTTP path: a bounded client,
// per-host pacing, and a conditional-GET cache persisted between runs.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/httpx/v5"
)

// userAgent identifies animap to every upstream.
const userAgent = "animap (+https://github.com/cplieger/animap)"

// maxCacheBytes bounds the persisted cache file.
const maxCacheBytes = 64 << 20

// retryBase is the first retry's delay, a var so tests need not wait it.
var retryBase = 2 * time.Second

// ErrNotFound reports a 404, which callers treat as an answer, not a failure.
var ErrNotFound = errors.New("source: not found")

// ErrRefused reports a 401 or 403: the host refused the client's credentials.
var ErrRefused = errors.New("source: credentials refused")

type cached struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	Body         []byte `json:"body"`
}

// Client fetches with one request per Interval per host, counting New as
// a request to every host: a client started right after another process's
// last request still keeps the gap. The zero value is not usable; call New.
type Client struct {
	started    time.Time
	http       *http.Client
	last       map[string]time.Time
	cache      map[string]cached
	log        *slog.Logger
	path       string
	bearerHost string
	bearer     string
	interval   time.Duration
	mu         sync.Mutex
	noCache    bool
}

// Option configures a Client.
type Option func(*Client)

// WithBearer sends token as an Authorization bearer on requests to host
// and to no other host. Redirects stay refused, so the token never follows
// one.
func WithBearer(host, token string) Option {
	return func(c *Client) { c.bearerHost, c.bearer = host, token }
}

// WithoutCache keeps no body and sends no validators: for a client that
// reads thousands of large bodies once each.
func WithoutCache() Option {
	return func(c *Client) { c.noCache = true }
}

// NewMemory returns a client whose cache lives in memory only.
func NewMemory(interval time.Duration, log *slog.Logger, opts ...Option) *Client {
	c := &Client{
		started:  time.Now(),
		http:     httpx.NewClient(60 * time.Second),
		last:     map[string]time.Time{},
		cache:    map[string]cached{},
		log:      log,
		interval: interval,
	}
	for _, o := range opts {
		o(c)
	}
	c.http.CheckRedirect = httpx.RefuseAllRedirects
	return c
}

// New loads the cache at path (absent is empty) and returns a client.
// An empty path keeps the cache in memory only.
func New(path string, interval time.Duration, log *slog.Logger) (*Client, error) {
	c := NewMemory(interval, log)
	if path == "" {
		return c, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.path = abs
	body, err := os.ReadFile(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return c, nil
	case err != nil:
		return nil, err
	case len(body) > maxCacheBytes:
		log.Warn("source: cache over its bound, starting empty", "path", abs, "bytes", len(body))
		return c, nil
	}
	if err := json.Unmarshal(body, &c.cache); err != nil {
		log.Warn("source: cache unreadable, starting empty", "path", abs, "error", err)
		c.cache = map[string]cached{}
	}
	return c, nil
}

// Get returns the body at rawURL, revalidating a cached copy. It retries
// every 5xx and a 429, honouring Retry-After, and maps a 404 to
// ErrNotFound and a 401 or 403 from the bearer's host to ErrRefused.
func (c *Client) Get(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("source: refusing %q", rawURL)
	}
	c.mu.Lock()
	prev, have := c.cache[rawURL]
	c.mu.Unlock()
	var v httpx.Validators
	if have {
		v = httpx.Validators{ETag: prev.ETag, LastModified: prev.LastModified}
	}
	res, err := httpx.Do(ctx, func(ctx context.Context) (httpx.ConditionalResult, error) {
		return c.attempt(ctx, u.Host, rawURL, v, maxBytes)
	}, httpx.WithMaxAttempts(3), httpx.WithBaseDelay(retryBase), httpx.WithRateLimitRetry(time.Minute),
		httpx.WithLogger(c.log), httpx.WithLabel(u.Host))
	if err != nil {
		return nil, c.classify(err, u.Host)
	}
	if res.NotModified {
		return prev.Body, nil
	}
	if c.noCache {
		return res.Body, nil
	}
	c.mu.Lock()
	c.cache[rawURL] = cached{ETag: res.Validators.ETag, LastModified: res.Validators.LastModified, Body: res.Body}
	c.mu.Unlock()
	return res.Body, nil
}

// attempt is one paced request for rawURL on host, the bearer sent only
// when host is the bearer's.
func (c *Client) attempt(ctx context.Context, host, rawURL string, v httpx.Validators, maxBytes int64) (httpx.ConditionalResult, error) {
	if err := c.pace(ctx, host); err != nil {
		return httpx.ConditionalResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return httpx.ConditionalResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if c.bearer != "" && host == c.bearerHost {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	res, err := httpx.DoConditional(c.http, req, v, maxBytes)
	// httpx.HTTPStatusError.IsTransient admits only 502, 503 and 504;
	// every other 5xx is retried here too.
	if se, ok := errors.AsType[*httpx.HTTPStatusError](err); ok && se.IsServerError() {
		return res, httpx.MarkTransient(err)
	}
	return res, err
}

// classify maps a 404 to ErrNotFound, and a 401 or 403 to ErrRefused only
// from the host the bearer goes to: a host given no credential refuses
// one request, not the client's token.
func (c *Client) classify(err error, host string) error {
	if se, ok := errors.AsType[*httpx.HTTPStatusError](err); ok && se.Code == http.StatusNotFound {
		return ErrNotFound
	}
	if _, ok := errors.AsType[*httpx.AuthError](err); ok && c.bearer != "" && host == c.bearerHost {
		return fmt.Errorf("%w: %s: %w", ErrRefused, host, err)
	}
	return err
}

func (c *Client) pace(ctx context.Context, host string) error {
	c.mu.Lock()
	prev, ok := c.last[host]
	if !ok {
		prev = c.started
	}
	wait := time.Until(prev.Add(c.interval))
	c.last[host] = time.Now().Add(max(wait, 0))
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	return httpx.SleepCtx(ctx, wait)
}

// Save writes the cache atomically. A memory-only client saves nothing.
func (c *Client) Save(ctx context.Context) error {
	if c.path == "" {
		return nil
	}
	c.mu.Lock()
	body, err := json.Marshal(c.cache)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if len(body) > maxCacheBytes {
		return fmt.Errorf("source: cache is %d bytes, over its bound", len(body))
	}
	_, err = atomicfile.WriteFile(ctx, c.path, body)
	return err
}
