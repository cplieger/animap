package mapread_test

import (
	"fmt"
	"os"

	"github.com/cplieger/animap/mapread"
)

func ExampleIndex_TVDBSeasons() {
	f, err := os.Open("testdata/mini.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		panic(err)
	}
	x := mapread.NewIndex(doc)
	e, _ := x.Entry(108)
	for _, s := range x.TVDBSeasons(e) {
		for _, season := range s.Seasons {
			fmt.Printf("TVDB %d season %d: stated %v, by default %v\n", s.Series, season.Season, season.Stated, season.Default)
		}
	}
	// Output: TVDB 5005 season 0: stated [1 2], by default [3]
}

func ExampleIndex_AtTVDB() {
	f, err := os.Open("testdata/place.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		panic(err)
	}
	x := mapread.NewIndex(doc)
	for _, l := range x.AtTVDB(mapread.TVDBEpisode{Series: 500, Season: 2, Episode: 1}) {
		fmt.Printf("AniDB %d %s %d (AniDB episode id %d), default %t\n", l.AniDB, l.Kind, l.Number, l.ID, l.Default)
	}
	// Output:
	// AniDB 1 regular 6 (AniDB episode id 1006), default false
	// AniDB 1 regular 7 (AniDB episode id 1007), default false
}

func ExampleIndex_AtTVDBEpisodeID() {
	f, err := os.Open("testdata/place.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		panic(err)
	}
	x := mapread.NewIndex(doc)
	for _, l := range x.AtTVDBEpisodeID(5012) {
		fmt.Printf("AniDB %d %s %d (AniDB episode id %d)\n", l.AniDB, l.Kind, l.Number, l.ID)
	}
	// Output: AniDB 1 regular 4 (AniDB episode id 1004)
}

func ExampleIndex_Episode() {
	f, err := os.Open("testdata/place.json")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	doc, err := mapread.Decode(f, mapread.DefaultLimits())
	if err != nil {
		panic(err)
	}
	x := mapread.NewIndex(doc)
	listing := map[mapread.Listing]string{mapread.Listed: "listed", mapread.NotListed: "not listed", mapread.ListingUnknown: "unknown"}
	state := map[mapread.State]string{mapread.Lands: "lands", mapread.Nowhere: "nowhere", mapread.Unstated: "unstated"}
	for _, n := range []int{5, 50} {
		ep, _ := x.Episode(mapread.Ref{AniDB: 1, Kind: mapread.Special, Number: n})
		fmt.Printf("special %d: %s, AniDB episode id %d, TVDB %s\n", n, listing[ep.Listing], ep.ID, state[ep.TVDB.State])
		for _, p := range ep.TVDB.Placements {
			fmt.Printf("  on TVDB %d S%dE%d, TVDB episode id %d, default %t\n", p.Series, p.Season, p.Episode, p.ID, p.Default)
		}
	}
	// Output:
	// special 5: listed, AniDB episode id 2005, TVDB lands
	//   on TVDB 500 S0E5, TVDB episode id 5005, default true
	// special 50: not listed, AniDB episode id 0, TVDB nowhere
}
