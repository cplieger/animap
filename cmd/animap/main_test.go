package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/release"
	"github.com/cplieger/animap/mapread"
)

const (
	miniFixture    = "../../mapread/testdata/mini.json"
	earlierFixture = "../../mapread/testdata/earlier-shape.json"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func fresh(t *testing.T) *mapread.Document {
	t.Helper()
	f, err := os.Open(miniFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	doc.GeneratedAt = now.Add(-24 * time.Hour).Format(time.RFC3339)
	doc.Sources.AniDB.TitlesDump = now.Add(-48 * time.Hour).Format(time.DateOnly)
	return doc
}

func write(t *testing.T, doc *mapread.Document) string {
	t.Helper()
	b, err := mapread.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "animap.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// noFloors lets the mini fixture pass the guard with no comparable release.
func noFloors(t *testing.T) {
	t.Helper()
	saved := release.Floors
	release.Floors = release.Populations{}
	t.Cleanup(func() { release.Floors = saved })
}

func verify(t *testing.T, args ...string) (verdict, error) {
	t.Helper()
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := run(append([]string{"verify"}, args...), &out, log, now)
	var v verdict
	if err == nil {
		if derr := json.Unmarshal(out.Bytes(), &v); derr != nil {
			t.Fatalf("verify printed %q: %v", out.String(), derr)
		}
	} else if out.Len() > 0 {
		t.Errorf("a failed verify printed %q", out.String())
	}
	return v, err
}

func TestVerifyPrintsTheVerdict(t *testing.T) {
	doc := fresh(t)
	in := write(t, doc)
	prevDoc := fresh(t)
	prevDoc.Anime[0].TVDBID = 4999
	prev := write(t, prevDoc)
	v, err := verify(t, "-in", in, "-previous", prev)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := mapread.ContentHash(doc)
	prevHash, _ := mapread.ContentHash(prevDoc)
	census := release.Census(doc)
	if v.ContentHash != hash || v.PreviousHash != prevHash || !v.Changed || v.Previous != previousComparable {
		t.Errorf("hashes = %s, %s, changed %t, previous %s; want %s, %s, true, comparable", v.ContentHash, v.PreviousHash, v.Changed, v.Previous, hash, prevHash)
	}
	if v.Census != census || v.PreviousCensus == nil || *v.PreviousCensus != release.Census(prevDoc) {
		t.Errorf("census = %+v, previous %+v; want %+v", v.Census, v.PreviousCensus, census)
	}
	if v.Revision != "mini" || v.TitlesDump != "2026-10-19" || v.GeneratedAt != "2026-10-20T12:00:00Z" || v.Notice != doc.Attribution.Notice || v.Stale != nil {
		t.Errorf("verdict = %+v, want the export's revision, dump date, build time and notice, and no stale reason", v)
	}
}

// A release whose rows equal the export's is unchanged, whatever its build
// time and sources.
func TestVerifyUnchanged(t *testing.T) {
	doc := fresh(t)
	prevDoc := fresh(t)
	prevDoc.GeneratedAt = "2026-10-01T00:00:00Z"
	prevDoc.Sources.Mapping.Revision = "older"
	v, err := verify(t, "-in", write(t, doc), "-previous", write(t, prevDoc))
	if err != nil {
		t.Fatal(err)
	}
	if v.Changed || v.ContentHash != v.PreviousHash || v.Previous != previousComparable {
		t.Errorf("verdict = %+v, want unchanged against a comparable release", v)
	}
}

func TestVerifyFreshness(t *testing.T) {
	noFloors(t)
	for name, tc := range map[string]struct {
		built, dump string
		stale       string
	}{
		"built at the bound":           {built: "2026-10-13T12:00:00Z", dump: "2026-10-19"},
		"built past the bound":         {built: "2026-10-13T11:59:59Z", dump: "2026-10-19", stale: "generated_at 2026-10-13T11:59:59Z"},
		"built at the bound lowercase": {built: "2026-10-13t12:00:00z", dump: "2026-10-19"},
		"dump at the bound":            {built: "2026-10-20T12:00:00Z", dump: "2026-10-07"},
		"dump past the bound":          {built: "2026-10-20T12:00:00Z", dump: "2026-10-06", stale: "sources.anidb.titles_dump 2026-10-06"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := fresh(t)
			doc.GeneratedAt, doc.Sources.AniDB.TitlesDump = tc.built, tc.dump
			in := write(t, doc)
			_, err := verify(t, "-in", in)
			if tc.stale == "" {
				if err != nil {
					t.Errorf("verify = %v, want fresh", err)
				}
				return
			}
			if !errors.Is(err, errStale) || !strings.Contains(err.Error(), tc.stale) {
				t.Errorf("verify = %v, want errStale naming %s", err, tc.stale)
			}
			v, err := verify(t, "-in", in, "-accept-stale")
			if err != nil || len(v.Stale) != 1 || !strings.HasPrefix(v.Stale[0], tc.stale) {
				t.Errorf("with -accept-stale: %v, stale %q; want accepted, naming %s", err, v.Stale, tc.stale)
			}
		})
	}
}

func TestVerifyBothStaleReasons(t *testing.T) {
	noFloors(t)
	doc := fresh(t)
	doc.GeneratedAt, doc.Sources.AniDB.TitlesDump = "2026-10-01T00:00:00Z", "2026-09-01"
	v, err := verify(t, "-in", write(t, doc), "-accept-stale")
	if err != nil || len(v.Stale) != 2 {
		t.Errorf("verify = %v, stale %q; want both reasons", err, v.Stale)
	}
}

// With no release to compare, the census must meet the floors, which
// -accept-shrink does not lift.
func TestVerifyWithNoComparableRelease(t *testing.T) {
	version3 := filepath.Join(t.TempDir(), "v3.json")
	if err := os.WriteFile(version3, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := write(t, fresh(t))
	for name, tc := range map[string]struct {
		previous, want string
	}{
		"no path":       {"", previousNone},
		"no file":       {filepath.Join(t.TempDir(), "absent.json"), previousNone},
		"earlier shape": {earlierFixture, previousEarlier},
		"other version": {version3, previousOther},
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"-in", in, "-accept-shrink"}
			if tc.previous != "" {
				args = append(args, "-previous", tc.previous)
			}
			if _, err := verify(t, args...); !errors.Is(err, release.ErrCoverage) || !strings.Contains(err.Error(), "below the floor") {
				t.Errorf("verify on the floors = %v, want ErrCoverage below a floor", err)
			}
			noFloors(t)
			v, err := verify(t, args...)
			if err != nil || v.Previous != tc.want || v.PreviousHash != "" || v.PreviousCensus != nil || !v.Changed {
				t.Errorf("verify = %v, %+v; want previous %s, changed, with no previous hash or census", err, v, tc.want)
			}
		})
	}
}

func TestVerifyShrinkGuard(t *testing.T) {
	prevDoc := fresh(t)
	prev := write(t, prevDoc)
	doc := fresh(t)
	doc.Entries = slices.Delete(doc.Entries, 0, 2)
	in := write(t, doc)
	if _, err := verify(t, "-in", in, "-previous", prev); !errors.Is(err, release.ErrCoverage) {
		t.Errorf("verify on a shrink = %v, want ErrCoverage", err)
	}
	if _, err := verify(t, "-in", in, "-previous", prev, "-accept-shrink"); err != nil {
		t.Errorf("verify on a shrink with -accept-shrink = %v, want accepted", err)
	}
}

func TestVerifyRefuses(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte(`{"version":2,"generated_at":`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := write(t, fresh(t))
	for name, tc := range map[string]struct {
		want error
		args []string
	}{
		"an export in the earlier shape": {mapread.ErrVersion, []string{"-in", earlierFixture}},
		"a missing export":               {os.ErrNotExist, []string{"-in", filepath.Join(t.TempDir(), "absent.json")}},
		"an unreadable previous release": {nil, []string{"-in", in, "-previous", broken}},
		"no command":                     {errUsage, nil},
		"no -in":                         {errUsage, []string{}},
		"an argument":                    {errUsage, []string{"-in", in, "extra"}},
		"an unknown flag":                {errUsage, []string{"-in", in, "-build"}},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if tc.args == nil {
				err = run(nil, io.Discard, slog.Default(), now)
			} else {
				_, err = verify(t, tc.args...)
			}
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("verify = %v, want %v", err, tc.want)
			}
		})
	}
	if err := run([]string{"build"}, io.Discard, slog.Default(), now); !errors.Is(err, errUsage) {
		t.Errorf("an unknown command = %v, want errUsage", err)
	}
}
