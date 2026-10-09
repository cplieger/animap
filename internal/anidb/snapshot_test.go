package anidb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var commit = strings.Repeat("a", 40)

// archive is a gzipped tar laid out as GitHub's codeload serves the mirror:
// every path under AnimeAggregations-<commit>/.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: "AnimeAggregations-" + commit + "/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
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

func animeFileJSON(aid int, end, episodes string) string {
	return `{"anime_id":` + strconv.Itoa(aid) + `,"end_date":` + end + `,"titles":[{"title":"x"}],"description":"text","episodes":` + episodes + `}`
}

func TestExtract(t *testing.T) {
	body := archive(t, map[string]string{
		"anime/10.json":  animeFileJSON(10, `"2005-06-30"`, `{"REGULAR":[{"number":2,"air_date":"2005-04-08"},{"number":1,"air_date":"2005-04-01"}],"SPECIAL":[{"number":2,"air_date":null},{"number":1,"air_date":"2005-05-01"}],"CREDITS":[{"number":1}]}`),
		"anime/11.json":  animeFileJSON(11, `null`, `{"REGULAR":[{"number":1,"air_date":"2026-09-30"}]}`),
		"anime/12.json":  animeFileJSON(12, `"2001"`, `null`),
		"anime/13.json":  animeFileJSON(13, `"2001"`, `{"REGULAR":[{"number":1},{"number":3}]}`),
		"episode/1.json": `{"episode_id":1}`,
		"LICENSE":        "Unlicense",
	})
	s, refusals, err := Extract(bytes.NewReader(body), commit)
	if err != nil {
		t.Fatalf("Extract = %v", err)
	}
	want := map[int]Anime{
		10: {Regular: []string{"2005-04-01", "2005-04-08"}, Specials: []Episode{{Number: 1, AirDate: "2005-05-01"}, {Number: 2}}, Finished: true},
		11: {Regular: []string{"2026-09-30"}, Specials: []Episode{}},
	}
	if !reflect.DeepEqual(s.Anime, want) {
		t.Errorf("Extract anime = %+v, want %+v", s.Anime, want)
	}
	if !slices.Equal(s.Refused, []int{13}) || len(refusals) != 1 || !errors.Is(refusals[0], errInvalid) {
		t.Errorf("Extract refused %v with %v, want AniDB 13 refused for its gap", s.Refused, refusals)
	}
	if s.Commit != commit {
		t.Errorf("Extract commit = %q, want %q", s.Commit, commit)
	}
}

func TestExtractRefusesTheArchive(t *testing.T) {
	good := archive(t, map[string]string{"anime/10.json": animeFileJSON(10, `null`, `{}`)})
	badCRC := bytes.Clone(good)
	badCRC[len(badCRC)-8] ^= 0xff
	for _, tc := range []struct {
		name   string
		body   []byte
		commit string
	}{
		{"not gzip", []byte("<html>"), commit},
		{"truncated", good[:len(good)-12], commit},
		{"a gzip trailer whose checksum does not match", badCRC, commit},
		{"another commit's directory", good, strings.Repeat("b", 40)},
		{"no anime file", archive(t, map[string]string{"LICENSE": "x"}), commit},
		{"a commit that is not a SHA", good, "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if s, _, err := Extract(bytes.NewReader(tc.body), tc.commit); err == nil {
				t.Errorf("Extract(%s) = %+v, want an error", tc.name, s)
			}
		})
	}
}

func TestExtractRefusesAnArchiveOverItsBounds(t *testing.T) {
	body := archive(t, map[string]string{"anime/10.json": animeFileJSON(10, `null`, `{}`), "episode/1.json": strings.Repeat(" ", 4096)})
	unpacked := int64(3 * 512 * 3)
	if _, _, err := extract(bytes.NewReader(body), commit, int64(len(body)), 16<<10); err != nil {
		t.Fatalf("Setup: extract(within both bounds) = %v", err)
	}
	if _, _, err := extract(bytes.NewReader(body), commit, int64(len(body))-1, 16<<10); !errors.Is(err, errInvalid) {
		t.Errorf("extract(one byte over the archive bound) = %v, want errInvalid", err)
	}
	if _, _, err := extract(bytes.NewReader(body), commit, int64(len(body)), unpacked); !errors.Is(err, errInvalid) {
		t.Errorf("extract(%d unpacked bytes allowed, a 4 KiB file inside) = %v, want errInvalid", unpacked, err)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		says string
	}{
		{"another anime's file", animeFileJSON(11, `null`, `{}`), "anime_id 11"},
		{"a gap in the regular episodes", animeFileJSON(10, `null`, `{"REGULAR":[{"number":1},{"number":3}]}`), "not 1 to 2"},
		{"a regular episode twice", animeFileJSON(10, `null`, `{"REGULAR":[{"number":1},{"number":1}]}`), "not 1 to 2"},
		{"a regular episode numbered 0", animeFileJSON(10, `null`, `{"REGULAR":[{"number":0}]}`), "not 1 to 1"},
		{"a decimal episode number", animeFileJSON(10, `null`, `{"REGULAR":[{"number":1.5}]}`), "number"},
		{"a special twice", animeFileJSON(10, `null`, `{"SPECIAL":[{"number":1},{"number":1}]}`), "listed twice"},
		{"a special numbered 0", animeFileJSON(10, `null`, `{"SPECIAL":[{"number":0}]}`), "below 1"},
		{"a timestamp for a date", animeFileJSON(10, `null`, `{"REGULAR":[{"number":1,"air_date":"2005-04-01T00:00:00Z"}]}`), "not YYYY-MM-DD"},
		{"a special's bad date", animeFileJSON(10, `null`, `{"SPECIAL":[{"number":1,"air_date":"April"}]}`), "not YYYY-MM-DD"},
		{"not JSON", "404: Not Found", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parse(10, []byte(tc.body))
			if !errors.Is(err, errInvalid) || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("parse(%s) = %v; want errInvalid naming %q", tc.name, err, tc.says)
			}
		})
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	body := archive(t, map[string]string{
		"anime/10.json": animeFileJSON(10, `"2005"`, `{"REGULAR":[{"number":1,"air_date":"2005-04-01"}],"SPECIAL":[{"number":1}]}`),
		"anime/13.json": animeFileJSON(13, `"2001"`, `{"REGULAR":[{"number":2}]}`),
	})
	s, _, err := Extract(bytes.NewReader(body), commit)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(path)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Errorf("LoadSnapshot(Extract's output) = %+v, %v; want %+v", got, err, s)
	}
	if strings.Contains(string(b), "text") || strings.Contains(string(b), `"x"`) {
		t.Errorf("snapshot %s keeps AniDB text", b)
	}
}

func TestDecodeSnapshotRefuses(t *testing.T) {
	ok := `{"commit":"` + commit + `","anime":{"1":{"regular":["2005-04-01"],"specials":[{"number":1,"air_date":""}],"finished":true}},"refused":[2]}`
	if _, err := decodeSnapshot([]byte(ok)); err != nil {
		t.Fatalf("Setup: decodeSnapshot(valid) = %v", err)
	}
	for _, body := range []string{
		strings.Replace(ok, commit, "main", 1),
		strings.Replace(ok, `"1":`, `"0":`, 1),
		strings.Replace(ok, `"2005-04-01"`, `"2005"`, 1),
		strings.Replace(ok, `[{"number":1,"air_date":""}]`, `[{"number":2,"air_date":""},{"number":1,"air_date":""}]`, 1),
		strings.Replace(ok, `[{"number":1,"air_date":""}]`, `[{"number":0,"air_date":""}]`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[1]`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[3,2]`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[2],"titles":[]`, 1),
		ok + " {}",
		ok + " ]",
		ok + "}",
	} {
		if s, err := decodeSnapshot([]byte(body)); !errors.Is(err, errInvalid) {
			t.Errorf("decodeSnapshot(%s) = %+v, %v; want errInvalid", body, s, err)
		}
	}
}

func TestLoadSnapshotRefusesAnOversizeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	valid := `{"commit":"` + commit + `","anime":{},"refused":[]}`
	if err := os.WriteFile(path, []byte(valid+strings.Repeat(" ", maxSnapshotBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); !errors.Is(err, errInvalid) {
		t.Errorf("LoadSnapshot(oversize) = %v, want errInvalid", err)
	}
}
