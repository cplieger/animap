package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/animap/mapread"
)

func sample() *mapread.Document {
	tv := func(ts ...mapread.Target) *[]mapread.Target { return &ts }
	return &mapread.Document{
		Version: mapread.Version, GeneratedAt: "2026-10-05T00:00:00Z", Attribution: DefaultAttribution,
		Anime: []mapread.Anime{
			{AniDBID: 2, TVDBID: 3, TMDBTVID: 4, Last: mapread.Last{Regular: 12}, EpisodesListed: true, Episodes: []mapread.Run{
				{Kind: mapread.Regular, Start: 1, End: 10, AniDBEpisodeIDs: []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30}, TVDB: tv(mapread.Target{Season: 1, Episode: 1}), TMDB: tv()},
				{Kind: mapread.Regular, Start: 11, End: 12, TMDB: tv(mapread.Target{Season: 1, Episode: 11, Default: true})},
			}},
			{AniDBID: 9, TMDBMovieIDs: []int{5}},
		},
		Entries: []mapread.Entry{
			{AniListID: 1, Parts: []mapread.Part{{AniDBID: 2, Kind: mapread.Regular}}},
			{AniListID: 3},
			{IDs: &mapread.IDs{MAL: []int{7}}, Parts: []mapread.Part{{AniDBID: 9, Kind: mapread.Regular}}},
		},
	}
}

func TestEncodeRefusesOverTheBound(t *testing.T) {
	saved := maxDocumentBytes
	t.Cleanup(func() { maxDocumentBytes = saved })
	b, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	maxDocumentBytes = int64(len(b))
	if _, err := Encode(sample()); err != nil {
		t.Errorf("Encode at the bound = %v, want accepted", err)
	}
	maxDocumentBytes = int64(len(b)) - 1
	if _, err := Encode(sample()); !errors.Is(err, errTooLarge) {
		t.Errorf("Encode one byte over the bound = %v, want errTooLarge", err)
	}
}

// The build writes at most what a reader on mapread's defaults reads.
func TestTheBoundIsTheReadersDefault(t *testing.T) {
	if maxDocumentBytes != mapread.DefaultLimits().MaxDocumentBytes || maxDocumentBytes != 32<<20 {
		t.Errorf("bound = %d, want mapread's default %d (32 MiB)", maxDocumentBytes, mapread.DefaultLimits().MaxDocumentBytes)
	}
}

func TestContentHashIgnoresGeneratedAtAndSources(t *testing.T) {
	a, b := sample(), sample()
	b.GeneratedAt = "2030-01-01T00:00:00Z"
	b.Sources.AnimeLists.Commit = "other"
	ha, _ := ContentHash(a)
	hb, _ := ContentHash(b)
	if ha != hb {
		t.Errorf("ContentHash changed with generated_at/sources: %s vs %s", ha, hb)
	}
	for name, edit := range map[string]func(d *mapread.Document){
		"an anime row":  func(d *mapread.Document) { d.Anime[0].TVDBID = 99 },
		"a run target":  func(d *mapread.Document) { (*d.Anime[0].Episodes[0].TVDB)[0].Episode = 2 },
		"an episode id": func(d *mapread.Document) { d.Anime[0].Episodes[0].AniDBEpisodeIDs[0] = 99 },
		"an entry":      func(d *mapread.Document) { d.Entries[1].Type = "TV" },
		"the notice":    func(d *mapread.Document) { d.Attribution.Notice = "x" },
		"the version":   func(d *mapread.Document) { d.Version = 3 },
		"a part's kind": func(d *mapread.Document) { d.Entries[0].Parts[0].Kind = mapread.Special },
	} {
		c := sample()
		edit(c)
		if hc, _ := ContentHash(c); hc == ha {
			t.Errorf("ContentHash did not change with %s", name)
		}
	}
}

func TestCensus(t *testing.T) {
	want := Populations{Entries: 2, EntriesWithParts: 1, AnimeWithTVDB: 1, AnimeWithTMDB: 2, EpisodesOnTVDB: 10, EpisodesOnTMDB: 2, AniDBEpisodeIDs: 10}
	if got := Census(sample()); got != want {
		t.Errorf("Census = %+v, want %+v", got, want)
	}
}

// The notice is the NOTICE file's AniDB clause, so the file and the
// release credit the same facts.
func TestNoticeMatchesTheNOTICEFile(t *testing.T) {
	body, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	const clause = "from AniDB (https://anidb.net) which episodes exist, with their kind, number and AniDB episode id, " +
		"read through AnimeAggregations (https://github.com/notseteve/AnimeAggregations); AniDB's data is licensed under CC BY-NC-SA 4.0."
	if !strings.Contains(DefaultAttribution.Notice, clause) || !strings.Contains(string(body), clause) {
		t.Errorf("the notice and NOTICE must both carry %q", clause)
	}
	nested, err := os.ReadFile("../../mapread/NOTICE")
	if err != nil || !bytes.Equal(nested, body) {
		t.Errorf("mapread/NOTICE differs from NOTICE (%v)", err)
	}
}

type jsonSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Defs       map[string]json.RawMessage `json:"$defs"`
	Enum       []string                   `json:"enum"`
}

func readSchema(t *testing.T) jsonSchema {
	t.Helper()
	body, err := os.ReadFile("../../docs/animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var js jsonSchema
	if err := json.Unmarshal(body, &js); err != nil {
		t.Fatal(err)
	}
	return js
}

func (js jsonSchema) def(t *testing.T, name string) jsonSchema {
	t.Helper()
	var d jsonSchema
	if err := json.Unmarshal(js.Defs[name], &d); err != nil {
		t.Fatalf("$defs.%s: %v", name, err)
	}
	return d
}

func (js jsonSchema) prop(t *testing.T, name string) jsonSchema {
	t.Helper()
	var d jsonSchema
	if err := json.Unmarshal(js.Properties[name], &d); err != nil {
		t.Fatalf("properties.%s: %v", name, err)
	}
	return d
}

// The JSON Schema and mapread's types name the same members at every
// level; member names only, never the values each member admits.
func TestJSONSchemaMatchesTheTypes(t *testing.T) {
	js := readSchema(t)
	sources := js.def(t, "sources")
	entry := js.def(t, "entry")
	for name, tc := range map[string]struct {
		s   jsonSchema
		typ reflect.Type
	}{
		"document":                       {js, reflect.TypeFor[mapread.Document]()},
		"sources":                        {sources, reflect.TypeFor[mapread.Sources]()},
		"sources.anime_offline_database": {sources.prop(t, "anime_offline_database"), reflect.TypeFor[mapread.OfflineDatabaseSource]()},
		"sources.anime_lists":            {sources.prop(t, "anime_lists"), reflect.TypeFor[mapread.AnimeListsSource]()},
		"sources.anidb_mirror":           {sources.prop(t, "anidb_mirror"), reflect.TypeFor[mapread.AniDBMirrorSource]()},
		"sources.overlay":                {sources.prop(t, "overlay"), reflect.TypeFor[mapread.OverlaySource]()},
		"attribution":                    {js.def(t, "attribution"), reflect.TypeFor[mapread.Attribution]()},
		"anime":                          {js.def(t, "anime"), reflect.TypeFor[mapread.Anime]()},
		"anime.last":                     {js.def(t, "anime").prop(t, "last"), reflect.TypeFor[mapread.Last]()},
		"run":                            {js.def(t, "run"), reflect.TypeFor[mapread.Run]()},
		"target":                         {js.def(t, "target"), reflect.TypeFor[mapread.Target]()},
		"entry":                          {entry, reflect.TypeFor[mapread.Entry]()},
		"entry.ids":                      {entry.prop(t, "ids"), reflect.TypeFor[mapread.IDs]()},
		"part":                           {js.def(t, "part"), reflect.TypeFor[mapread.Part]()},
	} {
		got := slices.Sorted(maps.Keys(tc.s.Properties))
		if want := tags(tc.typ); !slices.Equal(got, want) {
			t.Errorf("schema %s properties = %v, want %v", name, got, want)
		}
	}
}

func tags(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		out = append(out, strings.Split(f.Tag.Get("json"), ",")[0])
	}
	slices.Sort(out)
	return out
}

// No object in the schema closes its members, so a member added within
// version 2 validates against an older copy.
func TestJSONSchemaIsOpenAtEveryLevel(t *testing.T) {
	body, err := os.ReadFile("../../docs/animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, closing := range []string{"additionalProperties", "unevaluatedProperties"} {
				if c, ok := x[closing]; ok && c != true {
					t.Errorf("%s sets %s to %v, which refuses an added member", path, closing, c)
				}
			}
			for k, c := range x {
				walk(path+"/"+k, c)
			}
		case []any:
			for i, c := range x {
				walk(path+"/"+strconv.Itoa(i), c)
			}
		}
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	walk("#", root)
}

func TestVocabulariesMatchTheJSONSchemaEnums(t *testing.T) {
	js := readSchema(t)
	types := js.def(t, "type").Enum
	for _, typ := range types {
		if !mapread.ValidType(typ) {
			t.Errorf("schema type %q is not a mapread type", typ)
		}
	}
	if len(types) != 6 || mapread.ValidType("TV`\n@everyone") {
		t.Errorf("schema types = %v, want the six mapread accepts and nothing else", types)
	}
	var kinds []mapread.Kind
	for _, k := range js.def(t, "kind").Enum {
		kinds = append(kinds, mapread.Kind(k))
	}
	if !slices.Equal(kinds, mapread.Kinds()) {
		t.Errorf("schema kinds = %v, want %v in that order", kinds, mapread.Kinds())
	}
}
