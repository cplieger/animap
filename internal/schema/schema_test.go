package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sample() *Document {
	return &Document{
		Version:     Version,
		GeneratedAt: "2026-10-05T00:00:00Z",
		Sources: Sources{
			AnimeOfflineDatabase: OfflineDatabaseSource{Repository: "r", Release: "2026-40", Asset: "a", SHA256: "s"},
			AnimeLists:           AnimeListsSource{Repository: "r", Commit: "c", File: "f"},
			Overlay:              OverlaySource{Entries: 1, SHA256: "o"},
		},
		Attribution: DefaultAttribution,
		Records: []Record{
			{
				AniListID: 1, AniDBID: 2, Type: "TV", Episodes: 12, TVDBID: 3, TVDBSeason: new(0), TVDBEpisodeOffset: new(0),
				TMDBTVID: 4, TMDBSeason: new(1), TMDBMovieIDs: []int{5}, IMDbIDs: []string{"tt0000001"},
				MappingList:   []Row{{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 2}, {3}}}},
				TVDBPlacement: []Segment{{Start: 1, End: 11, Season: new(0), Episode: new(1)}, {Start: 12, End: 12}},
			},
			{AniDBID: 9, TVDBID: 7, TVDBAbsolute: true},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	doc := sample()
	b, err := Encode(doc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, doc) {
		t.Errorf("Decode(Encode(doc)) = %+v, want %+v", got, doc)
	}
}

func TestEncodeIsMinifiedDeterministicAndKeepsExplicitZeros(t *testing.T) {
	a, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(sample())
	if !bytes.Equal(a, b) {
		t.Error("two encodes of one document differ")
	}
	if bytes.HasSuffix(a, []byte("\n")) || bytes.Contains(a, []byte("\n  ")) {
		t.Error("encoding is not minified")
	}
	for _, want := range []string{`"tvdb_season":0`, `"tvdb_episode_offset":0`, `"anidb_season":0`, `"episodes":[[1,2],[3]]`, `"tvdb_placement":[{"start":1,"end":11,"season":0,"episode":1},{"start":12,"end":12}]`} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("encoding lacks %s", want)
		}
	}
	if bytes.Contains(a, []byte(`"mal_id"`)) || bytes.Contains(a, []byte(`"tmdb_episode_offset"`)) {
		t.Error("an empty field was encoded")
	}
	if !bytes.HasPrefix(a, []byte(`{"version":1,"generated_at":`)) {
		t.Errorf("member order: %.40s", a)
	}
}

func TestEncodeDoesNotEscapeHTML(t *testing.T) {
	doc := sample()
	doc.Sources.AnimeLists.File = "a<b>&c"
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"a<b>&c"`)) {
		t.Errorf("Encode escaped HTML characters: %s", b)
	}
}

func TestDecodeIsStrict(t *testing.T) {
	good, _ := Encode(sample())
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown member", strings.Replace(string(good), `"version":1`, `"version":1,"extra":true`, 1)},
		{"unknown record member", strings.Replace(string(good), `"anilist_id":1`, `"anilist_id":1,"x":1`, 1)},
		{"wrong version", strings.Replace(string(good), `"version":1`, `"version":2`, 1)},
		{"a second document", string(good) + "{}"},
		{"a trailing ]", string(good) + "]"},
		{"a trailing }", string(good) + "\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(tc.body)); err == nil {
				t.Errorf("Decode(%s) = nil error, want one", tc.name)
			}
		})
	}
}

func TestDecodeRefusesOversize(t *testing.T) {
	big := bytes.Repeat([]byte(" "), MaxDocumentBytes+1)
	if _, err := Decode(bytes.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Decode(oversize) = %v, want ErrTooLarge", err)
	}
}

func TestDecodeRefusesAnUnpublishedType(t *testing.T) {
	doc := sample()
	doc.Records[0].Type = "TV`\n@everyone"
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(bytes.NewReader(b)); !errors.Is(err, ErrType) {
		t.Errorf("Decode(type %q) = %v, want ErrType", doc.Records[0].Type, err)
	}
	doc.Records[0].Type = ""
	b, _ = Encode(doc)
	if _, err := Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("Decode(no type) = %v, want nil", err)
	}
}

func TestTypesMatchTheJSONSchemaEnum(t *testing.T) {
	body, err := os.ReadFile("../../docs/animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Defs struct {
			Record struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"record"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(body, &js); err != nil {
		t.Fatal(err)
	}
	if got := js.Defs.Record.Properties.Type.Enum; !slices.Equal(got, types) {
		t.Errorf("JSON Schema type enum = %v, want %v", got, types)
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
	b.Records[0].TVDBID = 99
	if hc, _ := ContentHash(b); hc == ha {
		t.Error("ContentHash did not change with a record")
	}
}

func TestCensus(t *testing.T) {
	recs := []Record{
		{AniListID: 1, AniDBID: 10, TVDBID: 5, MappingList: []Row{{}}},
		{AniListID: 2, AniDBID: 10, TVDBID: 5, TVDBPlacement: []Segment{{Start: 1, End: 1}}},
		{AniListID: 3},
		{AniDBID: 11, TMDBMovieIDs: []int{1}},
	}
	want := Populations{Records: 4, AniListWithAniDB: 2, AniDBWithTVDB: 1, WithTMDB: 1, WithMappingList: 1, WithPlacement: 1}
	if got := Census(recs); got != want {
		t.Errorf("Census = %+v, want %+v", got, want)
	}
}

// The JSON Schema file and the Go types must name the same members.
func TestJSONSchemaMatchesStructTags(t *testing.T) {
	body, err := os.ReadFile("../../docs/animap.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(body, &js); err != nil {
		t.Fatal(err)
	}
	for name, typ := range map[string]reflect.Type{
		"": reflect.TypeFor[Document](), "record": reflect.TypeFor[Record](), "row": reflect.TypeFor[Row](),
		"sources": reflect.TypeFor[Sources](), "attribution": reflect.TypeFor[Attribution](),
		"parent_specials": reflect.TypeFor[ParentSpecials](), "segment": reflect.TypeFor[Segment](),
	} {
		props := js.Properties
		if name != "" {
			props = js.Defs[name].Properties
		}
		var got []string
		for k := range props {
			got = append(got, k)
		}
		want := tags(typ)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("schema %q properties = %v, want %v", name, got, want)
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
