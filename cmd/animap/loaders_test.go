package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/animap/internal/tmdb"
	"github.com/cplieger/animap/internal/watch"
)

// served answers every Get with its body, as an upstream would.
type served []byte

func (s served) Get(context.Context, string, int64) ([]byte, error) { return s, nil }

// loader is one JSON input animap reads. load returns nil when it accepts
// body; utf8 and repeat are edits of the valid body (old to new, once) that
// put an invalid byte where encoding/json would repair it, and repeat a
// member it would read as the last.
type loader struct {
	load   func(t *testing.T, body []byte) error
	name   string
	valid  string
	utf8   [2]string
	repeat [2]string
}

func inFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

var loaders = []loader{
	{
		name:  "an AniDB mirror anime file",
		valid: `{"anime_id":10,"episodes":{"REGULAR":[{"number":1,"episode_id":101,"titles":[{"language":"ENGLISH","title":"a"}]}]}}`,
		utf8:  [2]string{`"episodes"`, "\"epis\xffdes\""}, repeat: [2]string{`"episode_id":101`, `"episode_id":102,"episode_id":101`},
		load: func(t *testing.T, body []byte) error {
			archive := mirrorArchive(t, map[string]string{"anime/10.json": string(body), "anime/11.json": `{"anime_id":11,"episodes":{"REGULAR":[{"number":1,"episode_id":111}]}}`})
			_, refusals, err := anidb.Extract(bytes.NewReader(archive), mirrorCommit)
			if err != nil {
				t.Fatal(err)
			}
			return errors.Join(refusals...)
		},
	},
	{
		name:  "an anime-offline-database file",
		valid: "{\"license\":{\"name\":\"ODbL-1.0\"}}\n" + `{"type":"TV","sources":["https://anilist.co/anime/1","https://anime-planet.com/anime/ab"],"episodes":1}`,
		utf8:  [2]string{`anime/ab"`, "anime/a\xffb\""}, repeat: [2]string{`"episodes":1`, `"episodes":2,"Episodes":1`},
		load: func(t *testing.T, body []byte) error {
			_, err := offlinedb.Load(inFile(t, "aod.jsonl", body), offlinedb.DefaultLimits)
			return err
		},
	},
	{
		name:  "the anime-offline-database metadata line",
		valid: "{\"license\":{\"name\":\"ODbL-1.0\"}}\n" + `{"type":"TV","sources":["https://anilist.co/anime/1"],"episodes":1}`,
		utf8:  [2]string{`"ODbL-1.0"`, "\"ODbL-1.0\xff\""}, repeat: [2]string{`"name":"ODbL-1.0"`, `"name":"ODbL-1.0","Name":"ODbL-1.0"`},
		load: func(t *testing.T, body []byte) error {
			// The trailing edit lands on the entry line; this row's own is
			// the metadata line's, so move the entry before checking it.
			meta, entry, _ := strings.Cut(string(body), "\n")
			if i := strings.Index(entry, "} {}"); i >= 0 {
				meta, entry = meta+" {}", entry[:i+1]
			}
			_, err := offlinedb.Load(inFile(t, "aod.jsonl", []byte(meta+"\n"+entry)), offlinedb.DefaultLimits)
			return err
		},
	},
	{
		name: "an overlay entry", valid: `{"anidb_id":7,"title":"a","justification":"b"}`,
		utf8: [2]string{`"title":"a"`, "\"title\":\"a\xff\""}, repeat: [2]string{`"title":"a"`, `"title":"x","Title":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := overlay.ReadEntry(inFile(t, "7.json", body))
			return err
		},
	},
	{
		name: "an overlay link", valid: `{"anilist_id":7,"title":"a"}`,
		utf8: [2]string{`"title":"a"`, "\"title\":\"a\xff\""}, repeat: [2]string{`"title":"a"`, `"title":"x","title":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := overlay.ReadLink(inFile(t, "7.json", body))
			return err
		},
	},
	{
		name:  "a review record",
		valid: `{"key":"anime:anidb:3195","title":"x","parts":{"date-tvdb":{"findings":{"date-mismatch:8":"` + strings.Repeat("a", 64) + `"},"verdict":"fine","date":"2026-10-15","reason":"r","evidence":["https://anidb.net/anime/3195"]}}}`,
		utf8:  [2]string{`"title":"x"`, "\"title\":\"x\xff\""}, repeat: [2]string{`"title":"x"`, `"title":"y","title":"x"`},
		load: func(t *testing.T, body []byte) error {
			_, err := review.Read(inFile(t, "anidb-3195.json", body))
			return err
		},
	},
	{
		name:  "the counts file",
		valid: `[{"anidb_id":1,"regular_episodes":12,"evidence":"https://anidb.net/anime/1","date":"2026-01-01","justification":"a"}]`,
		utf8:  [2]string{`"justification":"a"`, "\"justification\":\"a\xff\""}, repeat: [2]string{`"regular_episodes":12`, `"regular_episodes":13,"regular_episodes":12`},
		load: func(t *testing.T, body []byte) error {
			_, err := anidb.LoadRows(inFile(t, "counts.json", body))
			return err
		},
	},
	{
		name:  "the collision baseline",
		valid: `{"version":1,"anime_lists_commit":"c","basis":"a","collisions":[],"uncounted":[]}`,
		utf8:  [2]string{`"anime_lists_commit":"c"`, "\"anime_lists_commit\":\"c\xff\""}, repeat: [2]string{`"basis":"a"`, `"basis":"b","basis":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := guard.LoadBaseline(inFile(t, "baseline.json", body))
			return err
		},
	},
	{
		name:  "a layouts store",
		valid: `{"series":{"5":{"fetched":"2026-01-01T00:00:00Z","slug":"a"}}}`,
		utf8:  [2]string{`"slug":"a"`, "\"slug\":\"a\xff\""}, repeat: [2]string{`"slug":"a"`, `"slug":"b","slug":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := datecheck.LoadStore(inFile(t, "layouts.json", body))
			return err
		},
	},
	{
		name:  "an AniDB mirror snapshot",
		valid: `{"commit":"` + mirrorCommit + `","anime":{},"refused":[2]}`,
		utf8:  [2]string{`"refused"`, "\"refus\xffd\""}, repeat: [2]string{`"refused":[2]`, `"refused":[],"refused":[2]`},
		load: func(t *testing.T, body []byte) error {
			_, err := anidb.LoadSnapshot(inFile(t, "snapshot.json", body))
			return err
		},
	},
	{
		name:  "a watch-set config",
		valid: `{"name":"a","list_url":"https://a.example/","id_field":"id","per_page":10,"max_pages":1,"entry_url":"https://a.example/{id}"}`,
		utf8:  [2]string{`"name":"a"`, "\"name\":\"a\xff\""}, repeat: [2]string{`"name":"a"`, `"name":"b","name":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := watch.LoadConfig(inFile(t, "watch.json", body))
			return err
		},
	},
	{
		name:  "a watch-set listing page",
		valid: `{"items":[{"id":1}],"totalItems":1,"totalPages":1}`,
		utf8:  [2]string{`{"id":1}`, "{\"id\":1,\"x\xff\":2}"}, repeat: [2]string{`"totalItems":1`, `"totalItems":2,"totalItems":1`},
		load: func(t *testing.T, body []byte) error {
			_, err := watch.FetchIDs(t.Context(), served(body), &watch.Config{ListURL: "https://a.example/", IDField: "id", PerPage: 10, MaxPages: 1})
			return err
		},
	},
	{
		name:  "a SkyHook show",
		valid: `{"slug":"a","episodes":[{"airDate":"2020-01-01","title":"t","seasonNumber":1,"episodeNumber":1,"absoluteEpisodeNumber":1}]}`,
		utf8:  [2]string{`"title":"t"`, "\"title\":\"t\xff\""}, repeat: [2]string{`"title":"t"`, `"title":"u","title":"t"`},
		load: func(t *testing.T, body []byte) error {
			_, err := skyhook.Fetch(t.Context(), served(body), 5)
			return err
		},
	},
	{
		name:  "a TMDB show",
		valid: `{"seasons":[{"season_number":1}],"season/1":{"episodes":[{"air_date":"2020-01-01","name":"a","season_number":1,"episode_number":1}]}}`,
		utf8:  [2]string{`"name":"a"`, "\"name\":\"a\xff\""}, repeat: [2]string{`"name":"a"`, `"name":"b","name":"a"`},
		load: func(t *testing.T, body []byte) error {
			_, err := tmdb.Fetch(t.Context(), served(body), 5)
			return err
		},
	},
	{
		name:  "the HTTP validator cache",
		valid: `{"https://a.example/":{"etag":"e","body":"e30="}}`,
		utf8:  [2]string{`"etag":"e"`, "\"etag\":\"e\xff\""}, repeat: [2]string{`"etag":"e"`, `"etag":"f","etag":"e"`},
		load: func(t *testing.T, body []byte) error {
			var logs bytes.Buffer
			if _, err := source.New(inFile(t, "http.json", body), 0, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "cache unreadable") {
				return errors.New(logs.String())
			}
			return nil
		},
	},
	{
		name: "a review, issue or run file the subcommands read", valid: `{"a":"b"}`,
		utf8: [2]string{`"b"`, "\"b\xff\""}, repeat: [2]string{`"a":"b"`, `"a":"c","A":"b"`},
		load: func(t *testing.T, body []byte) error {
			var v map[string]any
			return readJSON(inFile(t, "in.json", body), &v)
		},
	},
}

// Every JSON input animap reads, from upstream or from the repo, refuses a
// body with an invalid byte, a repeated member or trailing data, rather than
// reading a repaired or partial value.
func TestEveryLoaderRefusesAMalformedBody(t *testing.T) {
	for _, l := range loaders {
		t.Run(l.name, func(t *testing.T) {
			if err := l.load(t, []byte(l.valid)); err != nil {
				t.Fatalf("Setup: the valid body is refused: %v", err)
			}
			for _, c := range []struct {
				name string
				body string
			}{
				{"an invalid byte", edit(t, l.valid, l.utf8)},
				{"a repeated member", edit(t, l.valid, l.repeat)},
				{"trailing data", l.valid + " {}"},
			} {
				if err := l.load(t, []byte(c.body)); err == nil {
					t.Errorf("%s: accepted %q", c.name, c.body)
				}
			}
		})
	}
}

func edit(t *testing.T, body string, e [2]string) string {
	t.Helper()
	if !strings.Contains(body, e[0]) {
		t.Fatalf("Setup: %q lacks %q", body, e[0])
	}
	return strings.Replace(body, e[0], e[1], 1)
}
