package source

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
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
		if el := time.Since(start); el != 2*time.Second {
			t.Errorf("three requests to one host took %v, want 2s", el)
		}
		if err := c.pace(t.Context(), "b.example"); err != nil {
			t.Fatal(err)
		}
		if el := time.Since(start); el != 2*time.Second {
			t.Errorf("a first request to another host waited: %v", el)
		}
	})
}
