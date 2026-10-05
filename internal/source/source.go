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

// UserAgent identifies animap to every upstream.
const UserAgent = "animap (+https://github.com/cplieger/animap)"

// MaxCacheBytes bounds the persisted cache file.
const MaxCacheBytes = 64 << 20

// ErrNotFound reports a 404, which callers treat as an answer, not a failure.
var ErrNotFound = errors.New("source: not found")

type cached struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	Body         []byte `json:"body"`
}

// Client fetches with one request per Interval per host. The zero value is
// not usable; call New.
type Client struct {
	http     *http.Client
	last     map[string]time.Time
	cache    map[string]cached
	log      *slog.Logger
	path     string
	interval time.Duration
	mu       sync.Mutex
}

// New loads the cache at path (absent is empty) and returns a client.
// An empty path keeps the cache in memory only.
func New(path string, interval time.Duration, log *slog.Logger) (*Client, error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = abs
	}
	c := &Client{
		http:     httpx.NewClient(60 * time.Second),
		last:     map[string]time.Time{},
		cache:    map[string]cached{},
		log:      log,
		path:     path,
		interval: interval,
	}
	c.http.CheckRedirect = httpx.RefuseAllRedirects
	if path == "" {
		return c, nil
	}
	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return c, nil
	case err != nil:
		return nil, err
	case len(body) > MaxCacheBytes:
		log.Warn("source: cache over its bound, starting empty", "path", path, "bytes", len(body))
		return c, nil
	}
	if err := json.Unmarshal(body, &c.cache); err != nil {
		log.Warn("source: cache unreadable, starting empty", "path", path, "error", err)
		c.cache = map[string]cached{}
	}
	return c, nil
}

// Get returns the body at rawURL, revalidating a cached copy. It retries
// transient failures and maps a 404 to ErrNotFound.
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
		if paceErr := c.pace(ctx, u.Host); paceErr != nil {
			return httpx.ConditionalResult{}, paceErr
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		if reqErr != nil {
			return httpx.ConditionalResult{}, reqErr
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "application/json")
		return httpx.DoConditional(c.http, req, v, maxBytes)
	}, httpx.WithMaxAttempts(3), httpx.WithBaseDelay(2*time.Second), httpx.WithLogger(c.log), httpx.WithLabel(u.Host))
	if err != nil {
		if se, ok := errors.AsType[*httpx.HTTPStatusError](err); ok && se.Code == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if res.NotModified {
		return prev.Body, nil
	}
	c.mu.Lock()
	c.cache[rawURL] = cached{ETag: res.Validators.ETag, LastModified: res.Validators.LastModified, Body: res.Body}
	c.mu.Unlock()
	return res.Body, nil
}

func (c *Client) pace(ctx context.Context, host string) error {
	c.mu.Lock()
	wait := time.Until(c.last[host].Add(c.interval))
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
	if len(body) > MaxCacheBytes {
		return fmt.Errorf("source: cache is %d bytes, over its bound", len(body))
	}
	_, err = atomicfile.WriteFile(ctx, c.path, body)
	return err
}
