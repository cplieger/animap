package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/schema"
)

// fin is a finished anime as the AniDB mirror lists it.
func fin(regular int, specials ...int) anidb.Anime {
	a := anidb.Anime{Regular: make([]string, regular), Specials: []anidb.Episode{}, Finished: true}
	for _, k := range specials {
		a.Specials = append(a.Specials, anidb.Episode{Number: k})
	}
	return a
}

// snapshotWith writes a mirror snapshot at mirrorCommit listing listed.
func snapshotWith(t *testing.T, dir string, listed map[int]anidb.Anime) string {
	t.Helper()
	return writeFixture(t, dir, "snapshot.json", &anidb.Snapshot{Commit: mirrorCommit, Anime: listed, Refused: []int{}})
}

func TestBuildPlacesTheMirrorCount(t *testing.T) {
	noFloors(t)
	dir, ov := t.TempDir(), t.TempDir()
	writeFixture(t, ov, "20.json", offsetEntry())
	with := func(n int) string { return snapshotWith(t, dir, map[int]anidb.Anime{20: fin(3), 30: fin(n)}) }
	if _, err := build(t, dir, ov, "", "-mirror", with(1)); err != nil {
		t.Errorf("build with the mirror counting AniDB 30 at 1 = %v, want it placed on 1x1 and accepted", err)
	}
	_, err := build(t, dir, ov, "", "-mirror", with(2))
	if err == nil || !strings.Contains(err.Error(), "TVDB 1x2") || !strings.Contains(err.Error(), "30 ep2") {
		t.Errorf("build with the mirror counting AniDB 30 at 2 = %v, want its episode 2 colliding on TVDB 1x2", err)
	}
	_, err = build(t, dir, ov, "", "-mirror", with(0))
	if err == nil || !strings.Contains(err.Error(), "no regular episode count") || !strings.Contains(err.Error(), "AniDB 30") {
		t.Errorf("build with the mirror listing AniDB 30 with no regular episode = %v, want it refused as uncounted", err)
	}
	check := func(n int) error {
		args := []string{"overlay", "check", "-overlay", ov, "-list", listFixture, "-aod", aodFixture, "-mirror", with(n)}
		return run(t.Context(), args, &bytes.Buffer{})
	}
	if err := check(1); err != nil {
		t.Errorf("overlay check with the mirror counting AniDB 30 at 1 = %v, want accepted", err)
	}
	if err := check(2); err == nil {
		t.Error("overlay check passed the collision the mirror's count reveals")
	}
}

func TestBuildRefusesTheMirrorAtAnotherCommit(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	other := strings.Repeat("e", 40)
	_, err := build(t, dir, t.TempDir(), "", "-mirror-commit", other)
	if err == nil || !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), mirrorCommit) {
		t.Errorf("build pinned to %s with a snapshot at %s = %v, want a refusal naming both", other, mirrorCommit, err)
	}
	if _, err := build(t, dir, t.TempDir(), "", "-mirror", filepath.Join(dir, "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("build with no snapshot file = %v, want it refused", err)
	}
}

// A published record's episodes is anidb.Facts.Count, the same number the
// collision check places.
func TestBuildPublishesTheMirrorCount(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	airing := fin(9)
	airing.Finished = false
	snap := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(13), 12: airing, 30: fin(4)})
	stats := filepath.Join(dir, "stats.json")
	if _, err := build(t, dir, t.TempDir(), "", "-mirror", snap, "-stats", stats); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "animap.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := schema.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if want := (schema.AniDBMirrorSource{Repository: anidb.Repository, Commit: mirrorCommit}); doc.Sources.AniDBMirror != want {
		t.Errorf("sources.anidb_mirror = %+v, want %+v", doc.Sources.AniDBMirror, want)
	}
	episodes := map[int]int{}
	for i := range doc.Records {
		if r := &doc.Records[i]; r.AniDBID > 0 {
			episodes[r.AniDBID] = r.Episodes
		}
	}
	for aid, want := range map[int]int{10: 13, 12: 2, 30: 4} {
		if episodes[aid] != want {
			t.Errorf("record for AniDB %d has %d episodes, want %d", aid, episodes[aid], want)
		}
	}
}

func TestBuildReportsCountsRowsTheSourcesAgreeWith(t *testing.T) {
	noFloors(t)
	dir := t.TempDir()
	rows := writeFixture(t, dir, "counts.json", []anidb.Row{countRow(10, 13), countRow(30, 2)})
	stats := filepath.Join(dir, "stats.json")
	snap := snapshotWith(t, dir, map[int]anidb.Anime{10: fin(13)})
	if _, err := build(t, dir, t.TempDir(), "", "-mirror", snap, "-counts", rows, "-stats", stats); err != nil {
		t.Fatal(err)
	}
	var st buildStats
	if err := readJSON(stats, &st); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.CountsRedundant, []int{10}) {
		t.Errorf("stats counts_redundant = %v, want [10], the row equal to the mirror's count", st.CountsRedundant)
	}
}

func mirrorArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "AnimeAggregations-" + mirrorCommit + "/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func extract(t *testing.T, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runMirror(t.Context(), append([]string{"extract"}, args...), stdin, &out, slog.New(slog.DiscardHandler))
	return out.String(), err
}

func TestMirrorExtract(t *testing.T) {
	dir := t.TempDir()
	body := mirrorArchive(t, map[string]string{
		"anime/10.json": `{"anime_id":10,"end_date":"2005","description":"Secret plot.","titles":[{"title":"Hidden Title"}],"episodes":{"REGULAR":[{"number":1,"air_date":"2005-04-01","titles":[{"title":"Episode Name"}]}],"SPECIAL":[{"number":1,"air_date":"2005-05-01"}]}}`,
		"anime/11.json": `{"anime_id":11,"episodes":{"REGULAR":[{"number":2}]}}`,
	})
	out := filepath.Join(dir, "snapshot.json")
	stdout, err := extract(t, bytes.NewReader(body), "-commit", mirrorCommit, "-out", out)
	if err != nil || stdout != "mirror extract: 1 anime listed, 1 refused\n" {
		t.Fatalf("mirror extract = %q, %v", stdout, err)
	}
	got, err := anidb.LoadSnapshot(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]anidb.Anime{10: {Regular: []string{"2005-04-01"}, Specials: []anidb.Episode{{Number: 1, AirDate: "2005-05-01"}}, Finished: true}}
	if !reflect.DeepEqual(got.Anime, want) || !slices.Equal(got.Refused, []int{11}) || got.Commit != mirrorCommit {
		t.Errorf("snapshot = %+v, want %+v with AniDB 11 refused", got, want)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Secret plot", "Hidden Title", "Episode Name"} {
		if bytes.Contains(raw, []byte(text)) {
			t.Errorf("the snapshot keeps AniDB text %q", text)
		}
	}
	if _, err := extract(t, bytes.NewReader(body), "-commit", strings.Repeat("e", 40), "-out", filepath.Join(dir, "other.json")); err == nil {
		t.Error("mirror extract accepted an archive of another commit")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "other.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a refused extract still wrote its snapshot")
	}
}

func TestMirrorExtractUsage(t *testing.T) {
	for _, args := range [][]string{{"fetch"}, {"extract"}, {"extract", "-commit", mirrorCommit}} {
		var out bytes.Buffer
		if err := runMirror(t.Context(), args, strings.NewReader(""), &out, slog.New(slog.DiscardHandler)); !errors.Is(err, errUsage) {
			t.Errorf("mirror %q = %v, want errUsage", args, err)
		}
	}
}
