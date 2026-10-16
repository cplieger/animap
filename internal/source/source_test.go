package source

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/httpx/v5"
)

func TestGetRevalidatesFromThePersistedCache(t *testing.T) {
	var full, revalidated atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			revalidated.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "cache.json")
	newClient := func() *Client {
		c, err := New(path, 0, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		c.http = srv.Client()
		return c
	}
	c := newClient()
	if b, err := c.Get(t.Context(), srv.URL+"/x", 1<<10); err != nil || string(b) != "body" {
		t.Fatalf("first Get = %q, %v", b, err)
	}
	if err := c.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	c2 := newClient()
	if b, err := c2.Get(t.Context(), srv.URL+"/x", 1<<10); err != nil || string(b) != "body" {
		t.Fatalf("second Get = %q, %v", b, err)
	}
	if full.Load() != 1 || revalidated.Load() != 1 {
		t.Errorf("full %d, revalidated %d; want 1 and 1", full.Load(), revalidated.Load())
	}
	if _, err := c2.Get(t.Context(), srv.URL+"/missing", 1<<10); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v, want ErrNotFound", err)
	}
}

func TestGetRefusesNonHTTPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	c, err := New("", 0, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := c.Get(t.Context(), srv.URL+"/", 10); err == nil {
		t.Errorf("an http URL was fetched: %q", b)
	}
}

func TestPacePerHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := New("", time.Second, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		for range 3 {
			if err := c.pace(t.Context(), "a.example"); err != nil {
				t.Fatal(err)
			}
		}
		if el := time.Since(start); el != 3*time.Second {
			t.Errorf("three requests to one host took %v, want 3s: the first waits a gap after New", el)
		}
		if err := c.pace(t.Context(), "b.example"); err != nil {
			t.Fatal(err)
		}
		if el := time.Since(start); el != 3*time.Second {
			t.Errorf("a first request to another host, a gap after New, waited: %v", el)
		}
	})
}

// served is a client of srv as production builds it, its requests going
// over srv's TLS transport, and no wait between retries.
func served(t *testing.T, srv *httptest.Server, opts ...Option) *Client {
	t.Helper()
	base := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = base })
	c := NewMemory(0, slog.New(slog.DiscardHandler), opts...)
	c.http.Transport = srv.Client().Transport
	return c
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// status answers each request with the next code of codes, then 200; a
// 429 asks for the shortest wait Retry-After can say.
func status(codes ...int) (http.Handler, *atomic.Int32) {
	var n atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if i := int(n.Add(1)) - 1; i < len(codes) {
			if codes[i] == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "1")
			}
			w.WriteHeader(codes[i])
			return
		}
		_, _ = w.Write([]byte("ok"))
	}), &n
}

func TestGetRetriesEveryServerError(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusNotImplemented, http.StatusBadGateway, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(code)+" then success", func(t *testing.T) {
			h, n := status(code)
			srv := httptest.NewTLSServer(h)
			defer srv.Close()
			if b, err := served(t, srv).Get(t.Context(), srv.URL+"/", 10); err != nil || string(b) != "ok" || n.Load() != 2 {
				t.Errorf("Get = %q, %v after %d requests; want ok on the second", b, err, n.Load())
			}
		})
	}
	t.Run("500 on every attempt", func(t *testing.T) {
		h, n := status(500, 500, 500, 500)
		srv := httptest.NewTLSServer(h)
		defer srv.Close()
		_, err := served(t, srv).Get(t.Context(), srv.URL+"/", 10)
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRefused) || n.Load() != 3 {
			t.Errorf("Get = %v after %d requests, want a plain failure after 3", err, n.Load())
		}
	})
}

// A 401 or 403 refuses the token only on the host the token goes to;
// elsewhere it is one failed request.
func TestGetRefusalsNameOnlyTheBearersHost(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, bearer := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d bearer=%t", code, bearer), func(t *testing.T) {
				h, _ := status(code)
				srv := httptest.NewTLSServer(h)
				defer srv.Close()
				var opts []Option
				if bearer {
					opts = append(opts, WithBearer(hostOf(t, srv), "test-key"))
				}
				_, err := served(t, srv, opts...).Get(t.Context(), srv.URL+"/", 10)
				_, auth := errors.AsType[*httpx.AuthError](err)
				if !auth || errors.Is(err, ErrRefused) != bearer {
					t.Errorf("Get = %v, want the AuthError, as ErrRefused only with the bearer", err)
				}
			})
		}
	}
}

func TestGetSendsTheBearerOnlyToItsHost(t *testing.T) {
	var auth []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	for _, host := range []string{hostOf(t, srv), "api.example"} {
		if _, err := served(t, srv, WithBearer(host, "test-key")).Get(t.Context(), srv.URL+"/", 10); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(auth, []string{"Bearer test-key", ""}) {
		t.Errorf("Authorization headers = %q, want the bearer on its host only", auth)
	}
}

func TestGetRefusesRedirectsAndOversizedBodies(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	c := served(t, srv, WithBearer(hostOf(t, srv), "test-key"))
	if b, err := c.Get(t.Context(), srv.URL+"/moved", 100); err == nil {
		t.Errorf("a redirect was followed: %q", b)
	}
	if b, err := c.Get(t.Context(), srv.URL+"/", 9); err == nil {
		t.Errorf("a body over the bound was read: %q", b)
	}
}
