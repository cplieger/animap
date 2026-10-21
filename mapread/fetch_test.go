package mapread

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// release is a fake GitHub: releases/latest answers with latest, and each
// asset path with its body. An asset in cut declares that many bytes more
// than it sends. It records every path asked.
type release struct {
	assets map[string]string
	cut    map[string]int
	latest func(w http.ResponseWriter)
	mu     sync.Mutex
	asked  []string
}

func (f *release) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asked = append(f.asked, r.URL.Path)
	f.mu.Unlock()
	if r.URL.Path == "/cplieger/animap/releases/latest" {
		f.latest(w)
		return
	}
	body, ok := f.assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if n, ok := f.cut[r.URL.Path]; ok {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+n))
	}
	_, _ = w.Write([]byte(body))
}

const (
	tag       = "v2026.10.16"
	assetPath = "/cplieger/animap/releases/download/" + tag + "/animap.json"
)

func sidecar(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:]) + "  animap.json\n"
}

func serve(t *testing.T, f *release) (Source, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return Source{Repo: "cplieger/animap", Base: srv.URL}, srv.Client()
}

func latestAt(base, tag string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Location", base+"/cplieger/animap/releases/tag/"+tag)
		w.WriteHeader(http.StatusFound)
	}
}

func TestLatestAndFetch(t *testing.T) {
	body := encodeSample(t)
	f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}}
	src, c := serve(t, f)
	f.latest = latestAt(src.Base, tag)
	rel, err := src.Latest(t.Context(), c)
	if err != nil || rel.Tag != tag || rel.Source != src {
		t.Fatalf("Latest = %+v, %v; want %s", rel, err, tag)
	}
	got, err := rel.Fetch(t.Context(), c, DefaultLimits())
	if err != nil {
		t.Fatalf("Fetch = %v", err)
	}
	if got.SHA256 != sha256.Sum256([]byte(body)) || len(got.Document.Entries) != len(sample().Entries) {
		t.Errorf("Fetch = %x with %d entries, want the sample and its digest", got.SHA256, len(got.Document.Entries))
	}
	if got.Bytes != int64(len(body)) {
		t.Errorf("Fetch Bytes = %d, want the served body's %d", got.Bytes, len(body))
	}
	want := []string{"/cplieger/animap/releases/latest", assetPath + ".sha256", assetPath}
	if strings.Join(f.asked, " ") != strings.Join(want, " ") {
		t.Errorf("asked %v, want %v: both assets at the tag", f.asked, want)
	}
}

// Every refused answer is ErrRelease carrying its status: a 302 refused for
// its Location carries the 302.
func TestLatestRefuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		latest func(base string) func(w http.ResponseWriter)
		code   int
	}{
		{"a 200", func(string) func(w http.ResponseWriter) {
			return func(w http.ResponseWriter) { _, _ = w.Write([]byte("page")) }
		}, http.StatusOK},
		{"a 301", func(base string) func(w http.ResponseWriter) {
			return func(w http.ResponseWriter) {
				w.Header().Set("Location", base+"/cplieger/animap/releases/tag/"+tag)
				w.WriteHeader(http.StatusMovedPermanently)
			}
		}, http.StatusMovedPermanently},
		{"a code tag", func(base string) func(w http.ResponseWriter) { return latestAt(base, "mapread/v0.1.0") }, http.StatusFound},
		{"a tag with a path", func(base string) func(w http.ResponseWriter) { return latestAt(base, tag+"/x") }, http.StatusFound},
		{"another repository", func(base string) func(w http.ResponseWriter) {
			return func(w http.ResponseWriter) {
				w.Header().Set("Location", base+"/other/animap/releases/tag/"+tag)
				w.WriteHeader(http.StatusFound)
			}
		}, http.StatusFound},
		{"another host", func(string) func(w http.ResponseWriter) { return latestAt("https://example.org", tag) }, http.StatusFound},
		{"no Location", func(string) func(w http.ResponseWriter) {
			return func(w http.ResponseWriter) { w.WriteHeader(http.StatusFound) }
		}, http.StatusFound},
		{"a 404", func(string) func(w http.ResponseWriter) {
			return func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }
		}, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &release{}
			src, c := serve(t, f)
			f.latest = tc.latest(src.Base)
			rel, err := src.Latest(t.Context(), c)
			if !errors.Is(err, ErrRelease) {
				t.Errorf("Latest = %+v, %v; want ErrRelease", rel, err)
			}
			if se, ok := errors.AsType[*StatusError](err); !ok || se.Code != tc.code || se.URL != src.Base+"/cplieger/animap/releases/latest" {
				t.Errorf("Latest error %v: StatusError %+v, %t; want code %d on releases/latest", err, se, ok, tc.code)
			}
		})
	}
}

func TestLatestLeavesATransientAnswerRetryable(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusServiceUnavailable, 599,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f := &release{latest: func(w http.ResponseWriter) { w.WriteHeader(status) }}
			src, c := serve(t, f)
			rel, err := src.Latest(t.Context(), c)
			if err == nil || errors.Is(err, ErrRelease) || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(status)) {
				t.Errorf("Latest = %+v, %v; want a plain error naming HTTP %d, not ErrRelease", rel, err, status)
			}
			if se, ok := errors.AsType[*StatusError](err); !ok || se.Code != status {
				t.Errorf("Latest error %v: StatusError %+v, %t; want code %d", err, se, ok, status)
			}
		})
	}
}

// Each refusal of a 200 is for its body: the sidecar or the document.
func TestFetchRefuses(t *testing.T) {
	body := encodeSample(t)
	other := strings.Replace(body, `"notice":"n"`, `"notice":"m"`, 1)
	earlierBody, err := os.ReadFile("testdata/earlier-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	earlier := string(earlierBody)
	for _, tc := range []struct {
		edit func(f *release)
		want error
		name string
	}{
		{name: "a body that is not the sidecar's", want: ErrChecksum, edit: func(f *release) { f.assets[assetPath] = other }},
		{name: "a malformed sidecar", want: ErrChecksum, edit: func(f *release) { f.assets[assetPath+".sha256"] = "abc  animap.json\n" }},
		{name: "a sidecar for another file", want: ErrChecksum, edit: func(f *release) {
			f.assets[assetPath+".sha256"] = strings.Replace(sidecar(body), "animap.json", "other.json", 1)
		}},
		{name: "a sidecar over its cap", want: ErrChecksum, edit: func(f *release) { f.assets[assetPath+".sha256"] = strings.Repeat("0", 2000) }},
		{name: "a document of another version", want: ErrVersion, edit: func(f *release) {
			v1 := strings.Replace(body, `"version":2`, `"version":1`, 1)
			f.assets[assetPath], f.assets[assetPath+".sha256"] = v1, sidecar(v1)
		}},
		{name: "a document of the earlier version 2 shape", want: ErrVersion, edit: func(f *release) {
			f.assets[assetPath], f.assets[assetPath+".sha256"] = earlier, sidecar(earlier)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}}
			src, c := serve(t, f)
			tc.edit(f)
			got, err := (Release{Source: src, Tag: tag}).Fetch(t.Context(), c, DefaultLimits())
			if !errors.Is(err, tc.want) || got.Document != nil {
				t.Errorf("Fetch = %+v, %v; want %v", got, err, tc.want)
			}
			if se, ok := errors.AsType[*StatusError](err); ok {
				t.Errorf("Fetch error %v carries %+v, want no StatusError for a 200", err, se)
			}
		})
	}
	f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}}
	src, c := serve(t, f)
	if _, err := (Release{Source: src, Tag: "latest"}).Fetch(t.Context(), c, DefaultLimits()); !errors.Is(err, ErrRelease) || len(f.asked) != 0 {
		t.Errorf("Fetch(tag latest) = %v after asking %v, want ErrRelease and no request", err, f.asked)
	}
}

// reply is one answer of the network fake.
type reply struct {
	location string
	body     string
	status   int
}

// network answers each URL with its reply and any other with a 404,
// recording every URL asked. A real transport sets Response.Request;
// withRequest says whether this one does.
type network struct {
	replies     map[string]reply
	asked       []string
	withRequest bool
}

func (n *network) RoundTrip(r *http.Request) (*http.Response, error) {
	n.asked = append(n.asked, r.URL.String())
	rp, ok := n.replies[r.URL.String()]
	if !ok {
		rp = reply{status: http.StatusNotFound}
	}
	resp := &http.Response{StatusCode: rp.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(rp.body))}
	if rp.location != "" {
		resp.Header.Set("Location", rp.location)
	}
	if n.withRequest {
		resp.Request = r
	}
	return resp, nil
}

// Every answer to either request: a 200, each status class, a redirect
// to each kind of target, and a redirect with no Location. A transient
// status is a plain *StatusError; any other but 200 is ErrRelease, and a
// refused redirect ErrRedirect, each wrapping the *StatusError of the
// answer refused, and a refused target is never asked.
func TestFetchAnswers(t *testing.T) {
	const (
		doc    = "https://github.com" + assetPath
		side   = doc + ".sha256"
		assets = "https://release-assets.githubusercontent.com/a"
	)
	body := encodeSample(t)
	ok := map[string]reply{side: {status: http.StatusOK, body: sidecar(body)}, doc: {status: http.StatusOK, body: body}}
	to := func(at, target string, status int) map[string]reply {
		r := maps.Clone(ok)
		r[at] = reply{status: status, location: target}
		r[target] = reply{status: http.StatusOK, body: ok[at].body}
		return r
	}
	answer := func(at string, status int) map[string]reply {
		r := maps.Clone(ok)
		r[at] = reply{status: status}
		return r
	}
	type row struct {
		name    string
		replies map[string]reply
		want    error
		// at and code are the *StatusError the error carries, none when code is 0.
		at       string
		code     int
		retry    bool
		notAsked string
	}
	rows := []row{
		{name: "200 to both", replies: ok},
		{name: "the document from GitHub's asset host", replies: to(doc, assets, http.StatusFound)},
		{name: "the document from the other asset host", replies: to(doc, "https://objects.githubusercontent.com/a", http.StatusFound)},
		{name: "the document elsewhere on the source's host", replies: to(doc, "https://github.com/elsewhere", http.StatusFound)},
		{name: "the sidecar from GitHub's asset host", replies: to(side, assets+".sha256", http.StatusFound)},
		{name: "a 301 to an allowed host", replies: to(doc, assets, http.StatusMovedPermanently)},
		{name: "a 307 to an allowed host", replies: to(doc, assets, http.StatusTemporaryRedirect)},
		{name: "a 308 to an allowed host", replies: to(doc, assets, http.StatusPermanentRedirect)},
		{name: "a redirect to a foreign host", replies: to(doc, "https://example.org/a", http.StatusFound), want: ErrRedirect, at: doc, code: http.StatusFound, notAsked: "https://example.org/a"},
		{name: "a redirect to a host ending like an allowed one", replies: to(doc, "https://release-assets.githubusercontent.com.example.org/a", http.StatusFound), want: ErrRedirect, at: doc, code: http.StatusFound, notAsked: "https://release-assets.githubusercontent.com.example.org/a"},
		{name: "a sidecar redirect to a foreign host", replies: to(side, "https://example.org/a", http.StatusFound), want: ErrRedirect, at: side, code: http.StatusFound, notAsked: "https://example.org/a"},
		{name: "a 307 to a foreign host", replies: to(doc, "https://example.org/a", http.StatusTemporaryRedirect), want: ErrRedirect, at: doc, code: http.StatusTemporaryRedirect, notAsked: "https://example.org/a"},
		{name: "a redirect to http", replies: to(doc, "http://release-assets.githubusercontent.com/a", http.StatusFound), want: ErrRedirect, at: doc, code: http.StatusFound, notAsked: "http://release-assets.githubusercontent.com/a"},
		{name: "a redirect to ftp", replies: to(doc, "ftp://release-assets.githubusercontent.com/a", http.StatusFound), want: ErrRedirect, at: doc, code: http.StatusFound, notAsked: "ftp://release-assets.githubusercontent.com/a"},
		{name: "a redirect to gopher", replies: to(doc, "gopher://github.com/a", http.StatusFound), want: ErrRedirect, at: doc, code: http.StatusFound, notAsked: "gopher://github.com/a"},
		{name: "a redirect with no Location", replies: answer(doc, http.StatusFound), want: ErrRelease, at: doc, code: http.StatusFound},
		{name: "a redirect to a 404", replies: func() map[string]reply {
			r := to(doc, assets, http.StatusFound)
			r[assets] = reply{status: http.StatusNotFound}
			return r
		}(), want: ErrRelease, at: assets, code: http.StatusNotFound},
		{name: "a redirect to a 503", replies: func() map[string]reply {
			r := to(doc, assets, http.StatusFound)
			r[assets] = reply{status: http.StatusServiceUnavailable}
			return r
		}(), retry: true, at: assets, code: http.StatusServiceUnavailable},
	}
	for name, at := range map[string]string{"sidecar": side, "document": doc} {
		for _, code := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable, 599} {
			rows = append(rows, row{name: name + " " + strconv.Itoa(code), replies: answer(at, code), retry: true, at: at, code: code})
		}
		for _, code := range []int{
			http.StatusNoContent, http.StatusNotModified, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnavailableForLegalReasons,
		} {
			rows = append(rows, row{name: name + " " + strconv.Itoa(code), replies: answer(at, code), want: ErrRelease, at: at, code: code})
		}
	}
	for _, tc := range rows {
		for _, withRequest := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s, Response.Request set %t", tc.name, withRequest), func(t *testing.T) {
				n := &network{replies: tc.replies, withRequest: withRequest}
				got, err := (Release{Source: Source{Repo: "cplieger/animap"}, Tag: tag}).Fetch(t.Context(), &http.Client{Transport: n}, DefaultLimits())
				success := tc.want == nil && !tc.retry
				switch {
				case success && (err != nil || got.Document == nil):
					t.Errorf("Fetch = %+v, %v; want the document", got, err)
				case !success && (err == nil || got.Document != nil):
					t.Errorf("Fetch = %+v, %v; want an error", got, err)
				case tc.want != nil && !errors.Is(err, tc.want):
					t.Errorf("Fetch = %v, want %v", err, tc.want)
				case tc.retry && (errors.Is(err, ErrRelease) || errors.Is(err, ErrRedirect)):
					t.Errorf("Fetch = %v, want a plain, retryable error", err)
				}
				se, has := errors.AsType[*StatusError](err)
				if has != (tc.code != 0) || has && (se.Code != tc.code || se.URL != tc.at) {
					t.Errorf("Fetch error %v: StatusError %+v, %t; want HTTP %d from %s", err, se, has, tc.code, tc.at)
				}
				if tc.notAsked != "" && slices.Contains(n.asked, tc.notAsked) {
					t.Errorf("asked %v, want no request to %s", n.asked, tc.notAsked)
				}
			})
		}
	}
}

func TestFetchNeedsAClientAndAnOrigin(t *testing.T) {
	if _, err := (Source{Repo: "cplieger/animap"}).Latest(t.Context(), nil); err == nil {
		t.Error("Latest(nil client) = nil error")
	}
	if _, err := (Release{Source: Source{Repo: "cplieger/animap"}, Tag: tag}).Fetch(t.Context(), nil, DefaultLimits()); err == nil {
		t.Error("Fetch(nil client) = nil error")
	}
}

// A Source that is not an https origin and an owner/name is refused before
// any request.
func TestSourceRefusedBeforeARequest(t *testing.T) {
	body := encodeSample(t)
	f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}}
	good, c := serve(t, f)
	f.latest = latestAt(good.Base, tag)
	host := strings.TrimPrefix(good.Base, "https://")
	for _, tc := range []struct {
		name string
		src  Source
	}{
		{"http", Source{Repo: good.Repo, Base: "http://" + host}},
		{"userinfo", Source{Repo: good.Repo, Base: "https://user:pass@" + host}},
		{"a path", Source{Repo: good.Repo, Base: good.Base + "/prefix"}},
		{"two trailing slashes", Source{Repo: good.Repo, Base: good.Base + "//"}},
		{"a query", Source{Repo: good.Repo, Base: good.Base + "?x=1"}},
		{"an empty query", Source{Repo: good.Repo, Base: good.Base + "?"}},
		{"a fragment", Source{Repo: good.Repo, Base: good.Base + "#x"}},
		{"opaque", Source{Repo: good.Repo, Base: "https:" + host}},
		{"one segment", Source{Repo: "animap", Base: good.Base}},
		{"three segments", Source{Repo: "a/b/c", Base: good.Base}},
		{"an empty owner", Source{Repo: "/animap", Base: good.Base}},
		{"an empty name", Source{Repo: "cplieger/", Base: good.Base}},
		{"a dot-dot owner", Source{Repo: "../animap", Base: good.Base}},
		{"a dot-dot name", Source{Repo: "cplieger/..", Base: good.Base}},
		{"a dot owner", Source{Repo: "./animap", Base: good.Base}},
		{"a dot name", Source{Repo: "cplieger/.", Base: good.Base}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.mu.Lock()
			f.asked = nil
			f.mu.Unlock()
			if _, err := tc.src.Latest(t.Context(), c); err == nil {
				t.Errorf("Latest(%+v) = nil error, want the source refused", tc.src)
			}
			if _, err := (Release{Source: tc.src, Tag: tag}).Fetch(t.Context(), c, DefaultLimits()); err == nil {
				t.Errorf("Fetch(%+v) = nil error, want the source refused", tc.src)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.asked) != 0 {
				t.Errorf("asked %v, want no request", f.asked)
			}
		})
	}
	for _, src := range []Source{good, {Repo: good.Repo, Base: good.Base + "/"}, {Repo: "c.p-l_1/a.n", Base: good.Base}} {
		if _, err := src.base(); err != nil {
			t.Errorf("base(%+v) = %v, want it accepted", src, err)
		}
	}
}

// The document cap holds at every size: a body exactly at it is read, one
// byte over is ErrTooLarge, and the largest cap reads a body whole. A body
// cut off right at the cap is a failed read, not an answer.
func TestFetchDocumentCap(t *testing.T) {
	body := encodeSample(t)
	n := int64(len(body))
	for _, tc := range []struct {
		name string
		cap  int64
		want error
	}{
		{name: "exactly at the cap", cap: n},
		{name: "one byte over", cap: n - 1, want: ErrTooLarge},
		{name: "the largest cap", cap: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}}
			src, c := serve(t, f)
			got, err := (Release{Source: src, Tag: tag}).Fetch(t.Context(), c, Limits{MaxDocumentBytes: tc.cap})
			if !errors.Is(err, tc.want) || (tc.want == nil) != (got.Document != nil) {
				t.Errorf("Fetch(cap %d) of %d bytes = %+v, %v; want %v", tc.cap, n, got, err, tc.want)
			}
		})
	}
	t.Run("cut off at the cap", func(t *testing.T) {
		f := &release{assets: map[string]string{assetPath: body, assetPath + ".sha256": sidecar(body)}, cut: map[string]int{assetPath: 10}}
		src, c := serve(t, f)
		_, err := (Release{Source: src, Tag: tag}).Fetch(t.Context(), c, Limits{MaxDocumentBytes: n})
		if !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrChecksum) {
			t.Errorf("Fetch of a body cut at the cap = %v, want the failed read (io.ErrUnexpectedEOF)", err)
		}
	})
}
