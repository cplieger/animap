package release

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/mapread"
)

func sample() *mapread.Document {
	tv := func(ts ...mapread.Target) *[]mapread.Target { return &ts }
	return &mapread.Document{
		Version: mapread.Version, GeneratedAt: "2026-10-05T00:00:00Z",
		Anime: []mapread.Anime{
			{AniDBID: 2, TVDBID: 3, Last: mapread.Last{Regular: 12, Special: 2}, EpisodesListed: true, Episodes: []mapread.Run{
				{Kind: mapread.Regular, Start: 1, End: 10, AniDBEpisodeIDs: []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30}, TVDB: tv(mapread.Target{Season: 1, Episode: 1})},
				{Kind: mapread.Regular, Start: 11, End: 12, TVDB: tv()},
				{Kind: mapread.Special, Start: 1, End: 2, TMDBMovies: []int{5, 6}},
			}},
			{AniDBID: 8, Last: mapread.Last{Special: 1}, Episodes: []mapread.Run{
				{Kind: mapread.Special, Start: 1, End: 1, TVDB: tv(mapread.Target{Series: 4, Season: 0, Episode: 1})},
			}},
			{AniDBID: 9, Type: "MOVIE"},
		},
		Entries: []mapread.Entry{
			{AniListID: 1, Parts: []mapread.Part{{AniDBID: 2, Kind: mapread.Regular}}},
			{AniListID: 3},
			{AniListID: 4, IDs: &mapread.IDs{MAL: []int{7}}, Parts: []mapread.Part{{AniDBID: 9, Kind: mapread.Regular}}},
		},
	}
}

func TestCensus(t *testing.T) {
	want := Populations{Entries: 3, EntriesWithParts: 2, RowsOnTVDB: 2, EpisodesOnTVDB: 11, EpisodesWithMovies: 2, AniDBEpisodeIDs: 10}
	if got := Census(sample()); got != want {
		t.Errorf("Census = %+v, want %+v", got, want)
	}
}

func TestCoverage(t *testing.T) {
	prev := Populations{Entries: 100, EntriesWithParts: 100, RowsOnTVDB: 100, EpisodesOnTVDB: 100, EpisodesWithMovies: 100, AniDBEpisodeIDs: 100}
	for name, tc := range map[string]struct {
		edit   func(p *Populations)
		accept bool
		ok     bool
	}{
		"unchanged":                      {edit: func(*Populations) {}, ok: true},
		"at 90%":                         {edit: func(p *Populations) { p.EpisodesWithMovies = 90 }, ok: true},
		"below 90%":                      {edit: func(p *Populations) { p.RowsOnTVDB = 89 }},
		"below 90% accepted":             {edit: func(p *Populations) { p.RowsOnTVDB = 89 }, accept: true, ok: true},
		"extinct":                        {edit: func(p *Populations) { p.AniDBEpisodeIDs = 0 }},
		"extinct with -accept-shrink":    {edit: func(p *Populations) { p.EntriesWithParts = 0 }, accept: true},
		"grown":                          {edit: func(p *Populations) { p.Entries = 1000 }, ok: true},
		"each population: the last one":  {edit: func(p *Populations) { p.AniDBEpisodeIDs = 89 }},
		"each population: the first one": {edit: func(p *Populations) { p.Entries = 89 }},
	} {
		t.Run(name, func(t *testing.T) {
			cur := prev
			tc.edit(&cur)
			if err := Coverage(&prev, cur, tc.accept); (err == nil) != tc.ok || err != nil && !errors.Is(err, ErrCoverage) {
				t.Errorf("Coverage = %v, want ok %t", err, tc.ok)
			}
		})
	}
}

func TestCoverageFloors(t *testing.T) {
	at := Floors
	if err := Coverage(nil, at, false); err != nil {
		t.Errorf("Coverage at the floors = %v, want accepted", err)
	}
	below := Floors
	below.EpisodesWithMovies--
	for _, accept := range []bool{false, true} {
		if err := Coverage(nil, below, accept); !errors.Is(err, ErrCoverage) || !strings.Contains(err.Error(), "episodes_with_movies is 1399, below the floor 1400") {
			t.Errorf("Coverage one below a floor (accept %t) = %v, want ErrCoverage naming it", accept, err)
		}
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 10, 21, 23, 59, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		built, dump string
		want        []string
	}{
		"fresh":                 {built: "2026-10-20T08:30:00Z", dump: "2026-10-20"},
		"built 8 days ago":      {built: "2026-10-13T23:59:00Z", dump: "2026-10-20"},
		"built over 8 days ago": {built: "2026-10-13T23:58:59Z", dump: "2026-10-20", want: []string{"generated_at 2026-10-13T23:58:59Z is 8.0 days old, over 8.0 days"}},
		"a leap second":         {built: "2026-10-13T23:59:60Z", dump: "2026-10-20"},
		"an offset":             {built: "2026-10-20T08:30:00+00:00", dump: "2026-10-20"},
		"a dump 14 days old":    {built: "2026-10-20T08:30:00Z", dump: "2026-10-07"},
		"a dump 15 days old":    {built: "2026-10-20T08:30:00Z", dump: "2026-10-06", want: []string{"sources.anidb.titles_dump 2026-10-06 is 15.0 days old, over 14.0 days"}},
		"a dump from tomorrow":  {built: "2026-10-20T08:30:00Z", dump: "2026-10-22"},
		"both":                  {built: "2026-09-01T00:00:00Z", dump: "2026-09-01", want: []string{"generated_at 2026-09-01T00:00:00Z is 51.0 days old, over 8.0 days", "sources.anidb.titles_dump 2026-09-01 is 50.0 days old, over 14.0 days"}},
		"built in another case": {built: "2026-10-20t08:30:00z", dump: "2026-10-20"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := sample()
			doc.GeneratedAt, doc.Sources.AniDB.TitlesDump = tc.built, tc.dump
			got, err := Stale(doc, now)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("Stale = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	doc := sample()
	doc.Sources.AniDB.TitlesDump = "2026-10"
	if _, err := Stale(doc, now); err == nil {
		t.Errorf("Stale on a malformed dump date = nil error")
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
		"document":        {js, reflect.TypeFor[mapread.Document]()},
		"sources":         {sources, reflect.TypeFor[mapread.Sources]()},
		"sources.mapping": {sources.prop(t, "mapping"), reflect.TypeFor[mapread.MappingSource]()},
		"sources.anidb":   {sources.prop(t, "anidb"), reflect.TypeFor[mapread.AniDBSource]()},
		"attribution":     {js.def(t, "attribution"), reflect.TypeFor[mapread.Attribution]()},
		"anime":           {js.def(t, "anime"), reflect.TypeFor[mapread.Anime]()},
		"anime.last":      {js.def(t, "anime").prop(t, "last"), reflect.TypeFor[mapread.Last]()},
		"run":             {js.def(t, "run"), reflect.TypeFor[mapread.Run]()},
		"target":          {js.def(t, "target"), reflect.TypeFor[mapread.Target]()},
		"entry":           {entry, reflect.TypeFor[mapread.Entry]()},
		"entry.ids":       {entry.prop(t, "ids"), reflect.TypeFor[mapread.IDs]()},
		"part":            {js.def(t, "part"), reflect.TypeFor[mapread.Part]()},
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
