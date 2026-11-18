package mapread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cplieger/go/httpclient"
)

// Source is a repository whose releases carry animap.json.
type Source struct {
	// Repo is owner/name, such as "cplieger/animap".
	Repo string
	// Base is an https origin, with no userinfo or path; "" is
	// https://github.com.
	Base string
}

// Release is one tagged release of a Source.
type Release struct {
	Source Source
	Tag    string
}

// Fetched is a verified, decoded release asset and the SHA-256 its sidecar
// names.
type Fetched struct {
	Document *Document
	// Bytes is the document body's length as read.
	Bytes  int64
	SHA256 [32]byte
}

// StatusError is the status of every answer Latest or Fetch refuses for its
// status or its redirect; a 200 refused for its body carries none.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string {
	return e.URL + " answered HTTP " + strconv.Itoa(e.Code)
}

var (
	segmentRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	tagRE     = regexp.MustCompile(`^v\d{4}\.\d{2}\.\d{2}(\.\d{4})?$`)
	// sidecarRE is sha256sum's line for the asset.
	sidecarRE = regexp.MustCompile(`^([0-9a-f]{64}) {2}animap\.json\n?$`)
)

// assetHosts serve release assets after github.com redirects a download.
var assetHosts = []string{"release-assets.githubusercontent.com", "objects.githubusercontent.com"}

const maxSidecarBytes = 1 << 10

func (s Source) base() (*url.URL, error) {
	b := s.Base
	if b == "" {
		b = "https://github.com"
	}
	u, err := url.Parse(strings.TrimSuffix(b, "/"))
	switch {
	case err != nil:
		return nil, fmt.Errorf("mapread: base %q: %w", s.Base, err)
	case u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return nil, fmt.Errorf("mapread: base %q is not an https origin", s.Base)
	case !validRepo(s.Repo):
		return nil, fmt.Errorf("mapread: repository %q is not owner/name", s.Repo)
	}
	return u, nil
}

func validRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && repoSegment(owner) && repoSegment(name)
}

func repoSegment(s string) bool {
	return s != "." && s != ".." && segmentRE.MatchString(s)
}

// Latest asks which tag the latest release is, reading no body: GitHub
// answers releases/latest with a 302 to the tag's page. A transferred or
// renamed repository answers with a 301 or 308 to its new releases/latest
// on the same host; Latest follows one and returns a Release whose Source
// names the new repository. An HTTP 408, 425, 429 or 5xx answer is a plain
// *StatusError a caller can retry; any other, a 302 to anything but a dated
// data release's tag or a second move included, is ErrRelease wrapping
// one. c must not be nil; its CheckRedirect is not used.
func (s Source) Latest(ctx context.Context, c *http.Client) (Release, error) {
	if c == nil {
		return Release{}, errors.New("mapread: Latest needs an HTTP client")
	}
	base, err := s.base()
	if err != nil {
		return Release{}, err
	}
	nc := *c
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	latest := base.String() + "/" + s.Repo + "/releases/latest"
	a, err := ask(ctx, &nc, latest)
	if err != nil {
		return Release{}, err
	}
	if a.code == http.StatusMovedPermanently || a.code == http.StatusPermanentRedirect {
		if s.Repo, err = moved(base, latest, a); err != nil {
			return Release{}, err
		}
		latest = base.String() + "/" + s.Repo + "/releases/latest"
		if a, err = ask(ctx, &nc, latest); err != nil {
			return Release{}, err
		}
	}
	if a.code != http.StatusFound {
		return Release{}, statusError(latest, a.code)
	}
	loc, se, err := location(latest, a)
	if err != nil {
		return Release{}, err
	}
	prefix := base.String() + "/" + s.Repo + "/releases/tag/"
	tag, ok := strings.CutPrefix(loc, prefix)
	if !ok || !tagRE.MatchString(tag) {
		return Release{}, fmt.Errorf("%w: latest release points at %q, not %s<vYYYY.MM.DD>: %w", ErrRelease, loc, prefix, se)
	}
	return Release{Source: s, Tag: tag}, nil
}

// answer is a response with its body closed: its status, and its Location
// resolved or the reason it has none.
type answer struct {
	locErr error
	loc    string
	code   int
}

// ask requests u through c, which follows no redirect.
func ask(ctx context.Context, c *http.Client, u string) (answer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return answer{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("mapread: latest release: %w", err)
	}
	_ = resp.Body.Close()
	// Location resolves against resp.Request, which a RoundTripper need not set.
	resp.Request = req
	a := answer{code: resp.StatusCode}
	if loc, err := resp.Location(); err != nil {
		a.locErr = err
	} else {
		a.loc = loc.String()
	}
	return a, nil
}

// moved is the owner/name a 301 or 308 from latest moves the repository
// to, on base's host
// (https://docs.github.com/en/repositories/creating-and-managing-repositories/transferring-a-repository).
func moved(base *url.URL, latest string, a answer) (string, error) {
	loc, se, err := location(latest, a)
	if err != nil {
		return "", err
	}
	repo, ok := strings.CutPrefix(loc, base.String()+"/")
	if ok {
		repo, ok = strings.CutSuffix(repo, "/releases/latest")
	}
	if !ok || !validRepo(repo) {
		return "", fmt.Errorf("%w: latest release moved to %q, not %s/<owner>/<name>/releases/latest: %w", ErrRelease, loc, base, se)
	}
	return repo, nil
}

// location is the Location of a, latest's answer, and the *StatusError a
// refusal of it carries.
func location(latest string, a answer) (string, *StatusError, error) {
	se := &StatusError{URL: latest, Code: a.code}
	if a.locErr != nil {
		return "", nil, fmt.Errorf("%w: latest release: %w: %w", ErrRelease, a.locErr, se)
	}
	return a.loc, se, nil
}

// Fetch downloads r's animap.json and its .sha256 sidecar, both at r.Tag,
// checks the body against the sidecar and decodes it under lim. Redirects
// are followed only over HTTPS to the Source's host and GitHub's asset
// hosts; another is ErrRedirect wrapping the redirect's *StatusError. An
// HTTP 408, 425, 429 or 5xx answer is a plain, retryable *StatusError; any
// other but 200 is ErrRelease wrapping one. A mismatch or malformed sidecar
// is ErrChecksum, a body over lim.MaxDocumentBytes ErrTooLarge. c must not
// be nil; its CheckRedirect is not used.
func (r Release) Fetch(ctx context.Context, c *http.Client, lim Limits) (Fetched, error) {
	if c == nil {
		return Fetched{}, errors.New("mapread: Fetch needs an HTTP client")
	}
	base, err := r.Source.base()
	if err != nil {
		return Fetched{}, err
	}
	if !tagRE.MatchString(r.Tag) {
		return Fetched{}, fmt.Errorf("%w: tag %q is not a dated data release", ErrRelease, r.Tag)
	}
	lim = lim.orDefault()
	policy := redirects(base.Hostname())
	asset := base.String() + "/" + r.Source.Repo + "/releases/download/" + r.Tag + "/animap.json"
	side, err := get(ctx, *c, policy, asset+".sha256", maxSidecarBytes, ErrChecksum)
	if err != nil {
		return Fetched{}, err
	}
	m := sidecarRE.FindSubmatch(side)
	if m == nil {
		return Fetched{}, fmt.Errorf("%w: %s.sha256 is not one sha256sum line for animap.json", ErrChecksum, r.Tag)
	}
	var want [32]byte
	if _, hexErr := hex.Decode(want[:], m[1]); hexErr != nil {
		return Fetched{}, fmt.Errorf("%w: %w", ErrChecksum, hexErr)
	}
	body, err := get(ctx, *c, policy, asset, lim.MaxDocumentBytes, ErrTooLarge)
	if err != nil {
		return Fetched{}, err
	}
	if sha256.Sum256(body) != want {
		return Fetched{}, fmt.Errorf("%w: %s animap.json does not match its sidecar", ErrChecksum, r.Tag)
	}
	doc, err := Decode(bytes.NewReader(body), lim)
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{Document: doc, Bytes: int64(len(body)), SHA256: want}, nil
}

// transient reports a status that says nothing about the release: a
// timeout, a rate limit or a server failure (RFC 9110 §15.5.9, §15.6;
// RFC 8470 §5.2; RFC 6585 §4).
func transient(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return status >= 500 && status <= 599
}

// statusError is u's answer code as an error: a *StatusError, wrapped in
// ErrRelease unless the status is transient.
func statusError(u string, code int) error {
	se := &StatusError{URL: u, Code: code}
	if transient(code) {
		return se
	}
	return fmt.Errorf("%w: %w", ErrRelease, se)
}

func redirects(baseHost string) func(*http.Request, []*http.Request) error {
	policy := httpclient.RedirectPolicy(httpclient.WithAllowedHosts(append([]string{baseHost}, assetHosts...)...))
	return func(req *http.Request, via []*http.Request) error {
		// httpclient refuses only the https-to-http downgrade, and a caller's
		// transport may serve another scheme.
		err := fmt.Errorf("redirect to scheme %q", req.URL.Scheme)
		if strings.EqualFold(req.URL.Scheme, "https") {
			err = policy(req, via)
		}
		if err != nil {
			// req.Response is the redirect that asked for req; via's last
			// request received it. A RoundTripper need not set Response.Request.
			se := &StatusError{URL: via[len(via)-1].URL.String(), Code: req.Response.StatusCode}
			return fmt.Errorf("%w: %w: %w", ErrRedirect, err, se)
		}
		return nil
	}
}

// get reads one asset of at most limit bytes through c under policy,
// refusing a longer one with over. Any answer but 200, a 3xx the client did
// not follow included, is a statusError of the URL that gave it.
func get(ctx context.Context, c http.Client, policy func(*http.Request, []*http.Request) error, u string, limit int64, over error) ([]byte, error) {
	// at is the last URL the policy let the client follow, which is the one
	// that answered: Response.Request may be unset.
	at := u
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := policy(req, via); err != nil {
			return err
		}
		at = req.URL.String()
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mapread: %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(at, resp.StatusCode)
	}
	lr := &io.LimitedReader{R: resp.Body, N: limit}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("mapread: %s: %w", u, err)
	}
	if lr.N > 0 {
		return body, nil
	}
	var probe [1]byte
	switch _, perr := io.ReadAtLeast(resp.Body, probe[:], 1); {
	case perr == nil:
		return nil, fmt.Errorf("%w: %s is over %d bytes", over, u, limit)
	case !errors.Is(perr, io.EOF):
		return nil, fmt.Errorf("mapread: %s: %w", u, perr)
	}
	return body, nil
}
