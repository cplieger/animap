package mapread

import (
	"fmt"
	"testing"
)

// goldens are the documents in testdata, each in its canonical encoding.
var goldens = []string{"testdata/mini.json", "testdata/place.json", "testdata/series.json"}

func targets(ts ...Target) *[]Target {
	if ts == nil {
		ts = []Target{}
	}
	return &ts
}

func at(season, episode int) Target { return Target{Season: season, Episode: episode} }

func dflt(season, episode int) Target { return Target{Season: season, Episode: episode, Default: true} }

// on is a target on series, another than its row's.
func on(series, season, episode int) Target {
	return Target{Series: series, Season: season, Episode: episode}
}

// withIDs is t holding the TVDB episode ids of its run's episodes.
func withIDs(t Target, ids ...int) Target {
	t.EpisodeIDs = ids
	return t
}

// placed is a stated placement on series, as an Index answers it.
func placed(series, season, episode int) Placement {
	return Placement{TVDBEpisode: TVDBEpisode{series, season, episode}}
}

// placedID is placed holding its TVDB episode id.
func placedID(series, season, episode, id int) Placement {
	p := placed(series, season, episode)
	p.ID = id
	return p
}

// sample is a small valid document: two anime on one TVDB series, one of
// them with AniDB's list (a gap at special 3, regular 5 listed past last,
// a special on another series, two specials that are movies, an other
// episode a row names that AniDB does not list, and TVDB episode ids on
// three targets, one of them on a slot another target shares without
// them), a listed film, and entries of every part shape.
func sample() *Document {
	return &Document{
		Version:     Version,
		GeneratedAt: "2026-10-05T00:00:00Z",
		Sources:     Sources{Mapping: MappingSource{Revision: "r"}, AniDB: AniDBSource{TitlesDump: "2026-10-04"}},
		Attribution: Attribution{License: "ODbL-1.0", LicenseURL: "u", ContentsLicense: "DbCL-1.0", ContentsLicenseURL: "u", Notice: "n"},
		Anime: []Anime{
			{
				AniDBID: 1, Type: "TV", TVDBID: 10, MALIDs: []int{1}, Last: Last{Regular: 4, Special: 4, Credit: 1, Other: 1}, EpisodesListed: true,
				Episodes: []Run{
					{Kind: Regular, Start: 1, End: 2, AniDBEpisodeIDs: []int{101, 102}, TVDB: targets(withIDs(at(1, 1), 7001, 7002))},
					{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{103}, TVDB: targets(at(1, 3), at(1, 4))},
					{Kind: Regular, Start: 4, End: 4, AniDBEpisodeIDs: []int{104}, TVDB: targets()},
					{Kind: Regular, Start: 5, End: 5, AniDBEpisodeIDs: []int{105}},
					{Kind: Special, Start: 1, End: 1, AniDBEpisodeIDs: []int{201}, TVDB: targets(withIDs(at(0, 5), 7005)), TMDBMovies: []int{40}},
					{Kind: Special, Start: 2, End: 2, AniDBEpisodeIDs: []int{202}, TMDBMovies: []int{41}},
					{Kind: Special, Start: 4, End: 4, AniDBEpisodeIDs: []int{204}, TVDB: targets(withIDs(on(11, 0, 1), 7101))},
					{Kind: Credit, Start: 1, End: 1, AniDBEpisodeIDs: []int{301}, TVDB: targets()},
					{Kind: Other, Start: 1, End: 1, TVDB: targets(at(0, 5))},
				},
			},
			{
				AniDBID: 2, Type: "TV", TVDBID: 10, Last: Last{Regular: 2},
				Episodes: []Run{{Kind: Regular, Start: 1, End: 2, TVDB: targets(dflt(2, 1))}},
			},
			{
				AniDBID: 3, Type: "MOVIE", MALIDs: []int{3}, IMDbIDs: []string{"tt0000003"}, Last: Last{Regular: 1}, EpisodesListed: true,
				Episodes: []Run{{Kind: Regular, Start: 1, End: 1, AniDBEpisodeIDs: []int{900}, TMDBMovies: []int{30, 31}}},
			},
			{AniDBID: 4, Type: "OVA"},
		},
		Entries: []Entry{
			{AniListID: 1, Type: "TV", IDs: &IDs{AniDB: []int{1}, MAL: []int{1}}, Parts: []Part{{AniDBID: 1, Kind: Regular}}},
			{AniListID: 2, Type: "SPECIAL", Parts: []Part{{AniDBID: 1, Kind: Special, Start: 1, End: 2}, {AniDBID: 1, Kind: Other, Start: 1, End: 1}}},
			{AniListID: 3, Type: "TV", IDs: &IDs{AniDB: []int{1, 2}}},
			{AniListID: 4, Type: "MOVIE", IDs: &IDs{MAL: []int{3}}, Parts: []Part{{AniDBID: 3, Kind: Regular}}},
			{AniListID: 5, Type: "OVA", Parts: []Part{{AniDBID: 2, Kind: Regular, Combined: true}, {AniDBID: 4, Kind: Regular}}},
			{AniListID: 6, Type: "SPECIAL", Parts: []Part{{AniDBID: 1, Kind: Special, Start: 4, End: 4}}},
		},
	}
}

func encodeSample(t *testing.T) string {
	t.Helper()
	b, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkStatement fails t when st is not one of Statement's three closed
// states: Placements non-empty exactly for Lands, nil for the others.
func checkStatement(t *testing.T, where string, st *Statement) {
	t.Helper()
	if st == nil {
		return
	}
	switch st.State {
	case Lands:
		if len(st.Placements) == 0 {
			t.Errorf("%s: Lands with no placements", where)
		}
	case Unstated, Nowhere:
		if st.Placements != nil {
			t.Errorf("%s: state %d with placements %v", where, st.State, st.Placements)
		}
	default:
		t.Errorf("%s: state %d is not a State", where, st.State)
	}
}

// checkEpisode fails t when ref's Episode is not closed: its statement in
// its states with every placement on a series and its TVDB episode id 0 or
// naming it, an id exactly when Listed, false for a ref naming no episode
// and nothing beside false.
func checkEpisode(t *testing.T, x *Index, ref Ref) {
	t.Helper()
	ep, ok := x.Episode(ref)
	where := fmt.Sprintf("%+v", ref)
	switch {
	case ok && (ref.Kind.order() < 0 || ref.Number < 1):
		t.Errorf("%s: true for a reference naming no episode: %+v", where, ep)
	case !ok && (ep.TVDB != nil || ep.TMDBMovies != nil || ep.Listing != ListingUnknown || ep.ID != 0):
		t.Errorf("%s: false with %+v", where, ep)
	}
	checkStatement(t, where+" TVDB", ep.TVDB)
	if ep.TVDB != nil {
		for _, p := range ep.TVDB.Placements {
			if p.Series <= 0 {
				t.Errorf("%s: placement %+v carries no series", where, p)
			}
			if got, ok := x.tvdbIDs[p.ID]; p.ID != 0 && (!ok || got != p.TVDBEpisode) || p.ID < 0 {
				t.Errorf("%s: placement %+v carries an id the index places at %+v", where, p, got)
			}
		}
	}
	if (ep.Listing == Listed) != (ep.ID > 0) || ep.Listing < ListingUnknown || ep.Listing > NotListed {
		t.Errorf("%s: listing %d with id %d", where, ep.Listing, ep.ID)
	}
}
