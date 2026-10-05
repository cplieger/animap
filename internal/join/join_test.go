package join

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/schema"
)

func fixtures(t *testing.T) ([]offlinedb.Entry, map[int]*animelists.Node) {
	t.Helper()
	_, entries, err := offlinedb.Load("../../testdata/aod-mini.jsonl", offlinedb.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../../testdata/anime-list-mini.xml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := animelists.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return entries, l.Nodes
}

func byKey(recs []schema.Record) (al, ad map[int]schema.Record) {
	al, ad = map[int]schema.Record{}, map[int]schema.Record{}
	for _, r := range recs {
		if r.AniListID > 0 {
			al[r.AniListID] = r
		} else {
			ad[r.AniDBID] = r
		}
	}
	return al, ad
}

func TestBuildRecordSet(t *testing.T) {
	entries, nodes := fixtures(t)
	recs, st := Build(entries, nodes)
	al, ad := byKey(recs)

	want100 := schema.Record{
		AniListID: 100, AniDBID: 10, MALID: 1000, Type: "TV", Episodes: 12, TVDBID: 5000,
		TVDBSeason: new(1), TMDBTVID: 9000, TMDBSeason: new(1), IMDbIDs: []string{"tt0000001"},
		MappingList: []schema.Row{
			{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 2}, {3}}},
			{AniDBSeason: 1, TVDBSeason: new(1), Start: 13, End: 24, Offset: -12},
			{AniDBSeason: 1, TMDBSeason: new(2), Start: 13, End: 24, Offset: -12},
		},
	}
	if !reflect.DeepEqual(al[100], want100) {
		t.Errorf("record 100 = %+v\nwant %+v", al[100], want100)
	}
	if r := al[101]; r.AniDBID != 0 || r.Type != "SPECIAL" || r.MALID != 1001 || r.TVDBID != 0 {
		t.Errorf("type-only record 101 = %+v", r)
	}
	if al[102].AniDBID != 13 || al[103].AniDBID != 13 {
		t.Errorf("both AniList ids of one entry must carry AniDB 13: %+v %+v", al[102], al[103])
	}
	if r := al[104]; r.AniDBID != 0 || r.Type != "MOVIE" {
		t.Errorf("ambiguous AniList 104 = %+v, want no anidb_id", r)
	}
	for _, id := range []int{12, 14, 15, 16, 17, 30} {
		if _, ok := ad[id]; !ok {
			t.Errorf("no AniDB-keyed record for %d", id)
		}
	}
	if _, ok := ad[31]; ok {
		t.Error("a node with no published fact and no database entry got a record")
	}
	if _, ok := ad[10]; ok {
		t.Error("AniDB 10 is carried by AniList 100 and must not repeat")
	}
	if want := (Stats{DroppedRows: 1, AmbiguousAniDB: 2, AniListRecords: 9, AniDBOnlyRecords: 6, TypeOnlyRecords: 1, NodesWithoutEntry: 1}); st != want {
		t.Errorf("Stats = %+v, want %+v", st, want)
	}
}

func TestBuildPrecedenceAndFieldRules(t *testing.T) {
	entries, nodes := fixtures(t)
	recs, _ := Build(entries, nodes)
	al, ad := byKey(recs)
	if r := ad[12]; r.Type != "OVA" || r.TVDBSeason == nil || *r.TVDBSeason != 0 || r.TVDBEpisodeOffset == nil || *r.TVDBEpisodeOffset != 0 || r.MappingList != nil {
		t.Errorf("AniDB 12 = %+v, want OVA, season 0, explicit offset 0, its unparseable row dropped", r)
	}
	if r := al[102]; !reflect.DeepEqual(r.TMDBMovieIDs, []int{77, 78}) || !reflect.DeepEqual(r.IMDbIDs, []string{"tt0000002"}) {
		t.Errorf("comma lists = %v %v, want de-duplicated valid ids", r.TMDBMovieIDs, r.IMDbIDs)
	}
	if r := al[106]; r.TVDBID != 0 || r.TVDBSeason != nil || r.MappingList != nil || !reflect.DeepEqual(r.TMDBMovieIDs, []int{555}) {
		t.Errorf("tvdbid=movie record = %+v, want no TVDB id, season or mapping list, TMDB movie kept", r)
	}
	if r := al[107]; !r.TVDBAbsolute || r.TVDBSeason != nil {
		t.Errorf("absolute record = %+v", r)
	}
	if r := ad[30]; r.Type != "" || r.TVDBID != 5006 {
		t.Errorf("node-only record = %+v", r)
	}
}

func TestBuildOrder(t *testing.T) {
	entries, nodes := fixtures(t)
	recs, _ := Build(entries, nodes)
	seenAniDBOnly := false
	for i := 1; i < len(recs); i++ {
		a, b := recs[i-1], recs[i]
		if a.AniListID == 0 {
			seenAniDBOnly = true
		}
		if seenAniDBOnly && b.AniListID != 0 {
			t.Fatalf("AniList record %d after AniDB-only records", b.AniListID)
		}
		if a.AniListID != 0 && b.AniListID != 0 && a.AniListID >= b.AniListID {
			t.Errorf("AniList order %d before %d", a.AniListID, b.AniListID)
		}
		if a.AniListID == 0 && b.AniListID == 0 && a.AniDBID >= b.AniDBID {
			t.Errorf("AniDB order %d before %d", a.AniDBID, b.AniDBID)
		}
	}
}

// The golden pins the whole record set the fixtures produce. Regenerate
// with UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden and review
// the diff.
func TestGolden(t *testing.T) {
	entries, nodes := fixtures(t)
	recs, _ := Build(entries, nodes)
	doc := &schema.Document{Version: schema.Version, GeneratedAt: "2026-10-05T00:00:00Z", Attribution: schema.DefaultAttribution, Records: recs}
	got, err := schema.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	const path = "../../testdata/golden/animap.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("golden mismatch (run UPDATE_GOLDEN=1 go test ./internal/join -run TestGolden and review):\n--- want\n%s\n+++ got\n%s", want, got)
	}
}
