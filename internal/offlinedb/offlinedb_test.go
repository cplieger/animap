package offlinedb

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/strictjson"
	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/jsoncap/v2"
)

const meta = `{"license":{"name":"Open Data Commons Open Database License (ODbL) v1.0","url":"u"},"lastUpdate":"2026-10-04"}`

func TestLoadMini(t *testing.T) {
	entries, err := Load("../../testdata/aod-mini.jsonl", DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 11 {
		t.Fatalf("Load = %d entries, want 11", len(entries))
	}
	want := Entry{Type: "TV", AniList: []int{100}, AniDB: []int{10}, IDs: mapread.IDs{AniDB: []int{10}, MAL: []int{1000}}, Episodes: 12}
	if !reflect.DeepEqual(entries[0], want) {
		t.Errorf("entries[0] = %+v, want %+v", entries[0], want)
	}
	if got := entries[3].AniList; !reflect.DeepEqual(got, []int{102, 103}) {
		t.Errorf("two AniList sources read as %v", got)
	}
}

func TestReadRefuses(t *testing.T) {
	small := Limits{MaxFileBytes: 1 << 20, MaxLineBytes: 256, MaxEntries: 2, MaxSources: 2}
	line := `{"sources":["https://anilist.co/anime/1"],"type":"TV"}`
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"no licence", `{"license":{"name":"MIT"}}` + "\n" + line, errLicense},
		{"malformed metadata", "nope\n" + line, errFormat},
		{"malformed entry", meta + "\n" + line + "\n{bad", errFormat},
		{"empty", "", errFormat},
		{"no entries", meta + "\n", errFormat},
		{"line over the cap", meta + "\n" + `{"sources":["` + strings.Repeat("x", 300) + `"]}`, errTooLarge},
		{"too many entries", meta + "\n" + strings.Repeat(line+"\n", 3), errTooLarge},
		{"too many sources", meta + "\n" + `{"sources":["a","b","c"]}`, errTooLarge},
		{"unpublished type", meta + "\n" + `{"sources":["https://anilist.co/anime/1"],"type":"TV\n@x"}`, errFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := read(strings.NewReader(tc.body), small); !errors.Is(err, tc.want) {
				t.Errorf("read(%s) = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestReadRefusesARepeatedMember(t *testing.T) {
	line := `{"sources":["https://anilist.co/anime/1"],"type":"TV"}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"an entry's sources twice", meta + "\n" + `{"sources":["https://anilist.co/anime/7"],"type":"TV","sources":["https://anidb.net/anime/8"]}`},
		{"an entry's sources twice, in another case", meta + "\n" + `{"sources":["https://anilist.co/anime/7"],"type":"TV","Sources":["https://anidb.net/anime/8"]}`},
		{"a repeated member inside an entry's object", meta + "\n" + `{"sources":["https://anilist.co/anime/7"],"type":"TV","animeSeason":{"year":1,"year":2}}`},
		{"the metadata's licence twice", `{"license":{"name":"MIT"},"license":{"name":"ODbL"}}` + "\n" + line},
		{"the licence's name twice, in another case", `{"license":{"name":"MIT","NAME":"ODbL"}}` + "\n" + line},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := read(strings.NewReader(tc.body), DefaultLimits); !errors.Is(err, errFormat) || !errors.Is(err, jsoncap.ErrDuplicateKey) {
				t.Errorf("read(%s) = %+v, %v; want errFormat for the repeated key", tc.name, got, err)
			}
		})
	}
}

func TestReadRefusesInvalidUTF8(t *testing.T) {
	line := `{"sources":["https://anilist.co/anime/1"],"type":"TV"}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"an invalid byte in an Anime-Planet key", meta + "\n" + "{\"sources\":[\"https://anilist.co/anime/1\",\"https://anime-planet.com/anime/a\xffb\"],\"type\":\"TV\"}"},
		{"an invalid byte in a member name", meta + "\n" + "{\"sour\xffces\":[\"https://anilist.co/anime/1\"],\"type\":\"TV\"}"},
		{"an invalid byte in the licence", "{\"license\":{\"name\":\"ODbL\xff\"}}\n" + line},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := read(strings.NewReader(tc.body), DefaultLimits); !errors.Is(err, errFormat) || !errors.Is(err, strictjson.ErrNotUTF8) {
				t.Errorf("read(%s) = %+v, %v; want errFormat for the invalid byte", tc.name, got, err)
			}
		})
	}
}

func TestLoadRefusesOversizeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.jsonl")
	if err := os.WriteFile(p, []byte(meta+"\n"+`{"sources":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p, Limits{MaxFileBytes: 10, MaxLineBytes: 1 << 10, MaxEntries: 10, MaxSources: 10}); !errors.Is(err, errTooLarge) {
		t.Errorf("Load(oversize) = %v, want errTooLarge", err)
	}
}

func TestSourceIDs(t *testing.T) {
	body := meta + "\n" + `{"sources":["https://anilist.co/anime/7","https://anilist.co/anime/7","https://anidb.net/anime/0","https://anidb.net/anime/x","https://kitsu.app/anime/3"],"episodes":-1}`
	entries, err := read(strings.NewReader(body), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if e := entries[0]; !reflect.DeepEqual(e.AniList, []int{7}) || e.AniDB != nil || e.Episodes != 0 || !reflect.DeepEqual(e.IDs.Kitsu, []int{3}) {
		t.Errorf("entry = %+v, want AniList [7], no AniDB, Kitsu [3], episodes 0", e)
	}
}

// Every service anime-offline-database lists is filed under its own ids,
// sorted and each once; a source on any other service is reported by host.
func TestSourcesPerService(t *testing.T) {
	sources := []string{
		"https://anidb.net/anime/23", "https://anidb.net/anime/4", "https://myanimelist.net/anime/1", "https://myanimelist.net/anime/1",
		"https://kitsu.app/anime/1", "https://anisearch.com/anime/1572", "https://animecountdown.com/37089",
		"https://simkl.com/anime/37089", "https://animenewsnetwork.com/encyclopedia/anime.php?id=13",
		"https://livechart.me/anime/3418", "https://anime-planet.com/anime/cowboy-bebop", "https://anime-planet.com/anime/a-b",
		"https://notify.moe/anime/Tk3ccKimg", "https://anime-planet.com/anime/", "https://anime-planet.com/anime/x/y",
		"https://example.org/anime/1", "https://example.org/anime/2", "https://kitsu.io/anime/9",
	}
	q := make([]string, 0, len(sources))
	for _, s := range sources {
		q = append(q, `"`+s+`"`)
	}
	entries, err := read(strings.NewReader(meta+"\n"+`{"sources":[`+strings.Join(q, ",")+`],"type":"TV"}`), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]
	want := mapread.IDs{
		AniDB: []int{4, 23}, AnimeCountdown: []int{37089}, AnimePlanet: []string{"a-b", "cowboy-bebop"}, AniSearch: []int{1572}, ANN: []int{13},
		Kitsu: []int{1}, LiveChart: []int{3418}, MAL: []int{1}, NotifyMoe: []string{"Tk3ccKimg"}, Simkl: []int{37089},
	}
	if !reflect.DeepEqual(e.IDs, want) {
		t.Errorf("IDs = %+v\nwant %+v", e.IDs, want)
	}
	if !reflect.DeepEqual(e.AniDB, []int{23, 4}) {
		t.Errorf("AniDB = %v, want file order [23 4]", e.AniDB)
	}
	if !reflect.DeepEqual(e.Unknown, []string{"example.org", "kitsu.io"}) {
		t.Errorf("Unknown = %v, want each unknown host once", e.Unknown)
	}
}

// A key's length is counted in characters, as the JSON Schema's maxLength
// counts it, never in bytes: 256 three-byte characters are kept, 257 are not.
func TestServiceKeyLengthInCharacters(t *testing.T) {
	kept, over := strings.Repeat("翼", 256), strings.Repeat("翼", 257)
	body := meta + "\n" + `{"sources":["https://anime-planet.com/anime/` + kept + `","https://notify.moe/anime/` + over + `"],"type":"TV"}`
	entries, err := read(strings.NewReader(body), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries[0].IDs; !reflect.DeepEqual(got.AnimePlanet, []string{kept}) || got.NotifyMoe != nil {
		t.Errorf("IDs = animeplanet %d keys, notifymoe %q; want the 256-character key and no 257-character one", len(got.AnimePlanet), got.NotifyMoe)
	}
}
