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
	for id, a := range s.Anime {
		if err := checkListed(id, &a); err != nil {
			t.Errorf("Extract AniDB %d = %+v, want every title digest set: %v", id, a, err)
		}
		a.Titles, a.RegularTitles = "", nil
		for i := range a.Specials {
			a.Specials[i].Titles = ""
		}
		for i := range a.Extras {
			a.Extras[i].Titles = ""
		}
		s.Anime[id] = a
	}
	want := map[int]Anime{
		10: {Regular: []string{"2005-04-01", "2005-04-08"}, Specials: []Episode{{Number: 1, AirDate: "2005-05-01"}, {Number: 2}}, Extras: []Episode{{Number: 101}}, Finished: true},
		11: {Regular: []string{"2026-09-30"}, Specials: []Episode{}, Extras: []Episode{}},
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

// Extras take Anime-Lists' season 0 numbers, and one those numbers cannot
// tell apart is dropped without refusing the file.
func TestParseNumbersTheExtras(t *testing.T) {
	for _, tc := range []struct {
		name     string
		episodes string
		want     []int
	}{
		{"each kind from its base", `{"OTHER":[{"number":13},{"number":1}],"PARODY":[{"number":1}],"PROMOTION":[{"number":2}],"CREDITS":[{"number":1}]}`, []int{101, 202, 301, 401, 413}},
		{"a kind's 100th and the other kind past it", `{"PROMOTION":[{"number":100},{"number":101}],"OTHER":[{"number":600}]}`, []int{300, 1000}},
		{"a credit on a special's number", `{"SPECIAL":[{"number":101}],"CREDITS":[{"number":1},{"number":2}]}`, []int{102}},
		{"an extra numbered twice or below 1", `{"OTHER":[{"number":1},{"number":1},{"number":0}]}`, []int{401}},
		{"an extra's malformed date, which nothing reads", `{"OTHER":[{"number":1,"air_date":"2022"},{"number":2,"air_date":"2022-10-09"}]}`, []int{401, 402}},
		{"an unknown kind", `{"TRAILER":[{"number":1}]}`, []int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, listed, err := parse(10, []byte(animeFileJSON(10, `null`, tc.episodes)))
			if err != nil || !listed {
				t.Fatalf("parse = %v, listed %t; want the file kept", err, listed)
			}
			got := []int{}
			for _, e := range a.Extras {
				got = append(got, e.Number)
				if e.AirDate != "" {
					t.Errorf("extra %d air date = %q, want none kept", e.Number, e.AirDate)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("extras = %v, want %v", got, tc.want)
			}
			if err := checkListed(10, &a); err != nil {
				t.Errorf("checkListed(parse's anime) = %v, want accepted", err)
			}
		})
	}
}

// The digest follows every title of a regular episode or a special, and
// nothing else: the order AniDB lists titles in, and the titles of credits,
// do not move it.
func TestParseDigestsTheEpisodeTitles(t *testing.T) {
	const base = `{"REGULAR":[` +
		`{"number":2,"air_date":"2005-04-08","titles":[{"language":"ENGLISH","title":"Second"}]},` +
		`{"number":1,"air_date":"2005-04-01","titles":[{"language":"ENGLISH","title":"Over Lord"}]}],` +
		`"SPECIAL":[{"number":1,"titles":[{"language":"ENGLISH","title":"Memory"},{"language":"JAPANESE","title":"翼の記憶"}]}],` +
		`"CREDITS":[{"number":1,"titles":[{"language":"ENGLISH","title":"Opening"}]}]}`
	digest := func(t *testing.T, episodes string) string {
		t.Helper()
		a, _, err := parse(10, []byte(animeFileJSON(10, `null`, episodes)))
		if err != nil {
			t.Fatalf("parse = %v", err)
		}
		return a.Titles
	}
	want := digest(t, base)
	if !digestRE.MatchString(want) {
		t.Fatalf("titles = %q, want a 64-hex digest", want)
	}
	for _, tc := range []struct {
		name, old, new string
		moves          bool
	}{
		{"one regular episode's title", `"Over Lord"`, `"Overlord"`, true},
		{"two regular episodes trading titles", `"Second"}]},{"number":1,"air_date":"2005-04-01","titles":[{"language":"ENGLISH","title":"Over Lord"}`, `"Over Lord"}]},{"number":1,"air_date":"2005-04-01","titles":[{"language":"ENGLISH","title":"Second"}`, true},
		{"a special's title", `"Memory"`, `"Memories"`, true},
		{"a title's language", `"JAPANESE"`, `"JAPANESE_TRANSLITERATED"`, true},
		{"a credits title", `"Opening"`, `"Ending"`, false},
		{"the order of an episode's titles", `{"language":"ENGLISH","title":"Memory"},{"language":"JAPANESE","title":"翼の記憶"}`, `{"language":"JAPANESE","title":"翼の記憶"},{"language":"ENGLISH","title":"Memory"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(base, tc.old) {
				t.Fatalf("Setup: the fixture lacks %q", tc.old)
			}
			if moved := digest(t, strings.Replace(base, tc.old, tc.new, 1)) != want; moved != tc.moves {
				t.Errorf("digest moved=%t, want moved=%t", moved, tc.moves)
			}
		})
	}
}

// Each episode's digest follows its own number and titles only, so a link
// over some episodes is not moved by an unrelated one.
func TestParseDigestsEachEpisode(t *testing.T) {
	const base = `{"REGULAR":[` +
		`{"number":1,"titles":[{"language":"ENGLISH","title":"Over Lord"}]},` +
		`{"number":2,"titles":[{"language":"ENGLISH","title":"Second"}]}],` +
		`"SPECIAL":[{"number":1,"titles":[{"language":"ENGLISH","title":"Memory"}]}],` +
		`"OTHER":[{"number":1,"titles":[{"language":"ENGLISH","title":"Concert"}]}]}`
	digests := func(t *testing.T, episodes string) []string {
		t.Helper()
		a, _, err := parse(10, []byte(animeFileJSON(10, `null`, episodes)))
		if err != nil {
			t.Fatalf("parse = %v", err)
		}
		if len(a.RegularTitles) != 2 || len(a.Specials) != 1 || len(a.Extras) != 1 {
			t.Fatalf("parse = %+v, want two regular digests, one special and one extra", a)
		}
		return []string{a.RegularTitles[0], a.RegularTitles[1], a.Specials[0].Titles, a.Extras[0].Titles}
	}
	want := digests(t, base)
	if want[0] == want[1] || want[0] == want[2] || want[2] == want[3] || slices.ContainsFunc(want, notDigest) {
		t.Fatalf("digests = %v, want four distinct 64-hex digests", want)
	}
	for _, tc := range []struct {
		name, old, new string
		moved          []bool
	}{
		{"episode 2 retitled", `"Second"`, `"Seconds"`, []bool{false, true, false, false}},
		{"the special retitled", `"Memory"`, `"Memories"`, []bool{false, false, true, false}},
		{"the other episode retitled", `"Concert"`, `"Live"`, []bool{false, false, false, true}},
		{"episodes 1 and 2 trading titles", `"Over Lord"}]},{"number":2,"titles":[{"language":"ENGLISH","title":"Second"`, `"Second"}]},{"number":2,"titles":[{"language":"ENGLISH","title":"Over Lord"`, []bool{true, true, false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := digests(t, strings.Replace(base, tc.old, tc.new, 1))
			for i := range got {
				if moved := got[i] != want[i]; moved != tc.moved[i] {
					t.Errorf("digest %d moved=%t, want %t", i, moved, tc.moved[i])
				}
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
	z, one := strings.Repeat("0", 64), strings.Repeat("1", 64)
	ok := `{"commit":"` + commit + `","anime":{"1":{"titles":"` + z + `","regular":["2005-04-01"],"regular_titles":["` + one + `"],"specials":[{"number":1,"air_date":"","titles":"` + one + `"}],"extras":[{"number":201,"air_date":"","titles":"` + one + `"}],"finished":true}},"refused":[2]}`
	if _, err := decodeSnapshot([]byte(ok)); err != nil {
		t.Fatalf("Setup: decodeSnapshot(valid) = %v", err)
	}
	for _, body := range []string{
		strings.Replace(ok, commit, "main", 1),
		strings.Replace(ok, `"1":`, `"0":`, 1),
		strings.Replace(ok, `"2005-04-01"`, `"2005"`, 1),
		strings.Replace(ok, `[{"number":1,"air_date":"","titles":"`+one+`"}]`, `[{"number":2,"air_date":"","titles":"`+one+`"},{"number":1,"air_date":"","titles":"`+one+`"}]`, 1),
		strings.Replace(ok, `[{"number":1,"air_date":"","titles":"`+one+`"}]`, `[{"number":0,"air_date":"","titles":"`+one+`"}]`, 1),
		strings.Replace(ok, `"number":201`, `"number":100`, 1),
		strings.Replace(ok, `"extras":[{"number":201,"air_date":"",`, `"extras":[{"number":401,"air_date":"","titles":"`+one+`"},{"number":201,"air_date":"",`, 1),
		strings.Replace(ok, `"specials":[{"number":1,`, `"specials":[{"number":201,`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[1]`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[3,2]`, 1),
		strings.Replace(ok, `"refused":[2]`, `"refused":[2],"titles":[]`, 1),
		strings.Replace(ok, z, "Over Lord", 1),
		ok + " {}",
		ok + " ]",
		ok + "}",
	} {
		if s, err := decodeSnapshot([]byte(body)); !errors.Is(err, errInvalid) {
			t.Errorf("decodeSnapshot(%s) = %+v, %v; want errInvalid", body, s, err)
		}
	}
	for name, body := range map[string]string{
		"an anime with no title digest":        strings.Replace(ok, `"titles":"`+z+`",`, "", 1),
		"a regular episode with no digest":     strings.Replace(ok, `"regular_titles":["`+one+`"]`, `"regular_titles":[]`, 1),
		"a regular digest that is not 64 hex":  strings.Replace(ok, `"regular_titles":["`+one+`"]`, `"regular_titles":["x"]`, 1),
		"a special with no digest":             strings.Replace(ok, `"air_date":"","titles":"`+one+`"`, `"air_date":""`, 1),
		"no extras, as an older extract wrote": strings.Replace(ok, `"extras":[{"number":201,"air_date":"","titles":"`+one+`"}],`, "", 1),
		"an extra with no digest":              strings.Replace(ok, `{"number":201,"air_date":"","titles":"`+one+`"}`, `{"number":201,"air_date":""}`, 1),
	} {
		if _, err := decodeSnapshot([]byte(body)); err == nil || !strings.Contains(err.Error(), "re-extract") {
			t.Errorf("decodeSnapshot(%s) = %v, want a refusal saying to re-extract", name, err)
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
