package mapread

import (
	"fmt"
	"strings"
	"testing"
)

func targets(ts ...Target) *[]Target {
	if ts == nil {
		ts = []Target{}
	}
	return &ts
}

func at(season, episode int) Target { return Target{Season: season, Episode: episode} }

func dflt(season, episode int) Target { return Target{Season: season, Episode: episode, Default: true} }

// sample is a small valid document: two anime on one TVDB series, one of
// them on a TMDB show and with AniDB's list (a gap at special 3, regular 5
// listed past last and an other episode a row names that AniDB does not
// list), a listed film, and entries of every part shape.
func sample() *Document {
	return &Document{
		Version:     Version,
		GeneratedAt: "2026-10-05T00:00:00Z",
		Sources: Sources{
			AnimeOfflineDatabase: OfflineDatabaseSource{Repository: "r", Release: "2026-42", Asset: "a", SHA256: strings.Repeat("0", 64)},
			AnimeLists:           AnimeListsSource{Repository: "r", Commit: strings.Repeat("a", 40), File: "f"},
			AniDBMirror:          AniDBMirrorSource{Repository: "r", Commit: strings.Repeat("b", 40)},
			Overlay:              OverlaySource{SHA256: strings.Repeat("1", 64), Entries: 2, Links: 1},
		},
		Attribution: Attribution{License: "ODbL-1.0", LicenseURL: "u", ContentsLicense: "DbCL-1.0", ContentsLicenseURL: "u", Notice: "n"},
		Anime: []Anime{
			{
				AniDBID: 1, Type: "TV", TVDBID: 10, TMDBTVID: 20, Last: Last{Regular: 4, Special: 3, Credit: 1, Other: 1}, EpisodesListed: true,
				Episodes: []Run{
					{Kind: Regular, Start: 1, End: 2, AniDBEpisodeIDs: []int{101, 102}, TVDB: targets(at(1, 1)), TMDB: targets(dflt(1, 1))},
					{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{103}, TVDB: targets(at(1, 3), at(1, 4)), TMDB: targets()},
					{Kind: Regular, Start: 4, End: 4, AniDBEpisodeIDs: []int{104}, TVDB: targets(), TMDB: targets()},
					{Kind: Regular, Start: 5, End: 5, AniDBEpisodeIDs: []int{105}},
					{Kind: Special, Start: 1, End: 1, AniDBEpisodeIDs: []int{201}, TVDB: targets(at(0, 5)), TMDB: targets()},
					{Kind: Special, Start: 2, End: 2, AniDBEpisodeIDs: []int{202}, TMDB: targets(dflt(0, 2))},
					{Kind: Credit, Start: 1, End: 1, AniDBEpisodeIDs: []int{301}, TVDB: targets(), TMDB: targets()},
					{Kind: Other, Start: 1, End: 1, TVDB: targets(at(0, 5))},
				},
			},
			{
				AniDBID: 2, Type: "TV", TVDBID: 10, Last: Last{Regular: 2},
				Episodes: []Run{{Kind: Regular, Start: 1, End: 2, TVDB: targets(dflt(2, 1))}},
			},
			{
				AniDBID: 3, Type: "MOVIE", TMDBMovieIDs: []int{30, 31}, IMDbIDs: []string{"tt0000003"}, Last: Last{Regular: 1}, EpisodesListed: true,
				Episodes: []Run{{Kind: Regular, Start: 1, End: 1, AniDBEpisodeIDs: []int{900}}},
			},
			{AniDBID: 4, Type: "OVA"},
		},
		Entries: []Entry{
			{AniListID: 1, Type: "TV", Episodes: 4, IDs: &IDs{AniDB: []int{1}, MAL: []int{1}}, Parts: []Part{{AniDBID: 1, Kind: Regular}}},
			{AniListID: 2, Type: "SPECIAL", Parts: []Part{{AniDBID: 1, Kind: Special, Start: 1, End: 2}, {AniDBID: 1, Kind: Other, Start: 1, End: 1}}},
			{AniListID: 3, Type: "TV", IDs: &IDs{AniDB: []int{1, 2}}},
			{AniListID: 4, Type: "MOVIE", Parts: []Part{{AniDBID: 3, Kind: Regular}}},
			{AniListID: 5, Type: "OVA", Parts: []Part{{AniDBID: 2, Kind: Regular, Combined: true}, {AniDBID: 4, Kind: Regular}}},
			{Type: "OVA", IDs: &IDs{AnimePlanet: []string{"a"}, Kitsu: []int{9}}},
			{Type: "TV", IDs: &IDs{Kitsu: []int{3}}, Parts: []Part{{AniDBID: 2, Kind: Regular}}},
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
// states: Targets non-empty exactly for Lands, nil for the others.
func checkStatement(t *testing.T, where string, st *Statement) {
	t.Helper()
	if st == nil {
		return
	}
	switch st.State {
	case Lands:
		if len(st.Targets) == 0 {
			t.Errorf("%s: Lands with no targets", where)
		}
	case Unstated, Nowhere:
		if st.Targets != nil {
			t.Errorf("%s: state %d with targets %v", where, st.State, st.Targets)
		}
	default:
		t.Errorf("%s: state %d is not a State", where, st.State)
	}
}

// checkEpisode fails t when ref's Episode is not closed: each statement in
// its states, an id exactly when Listed, false for a ref naming no episode
// and nothing beside false.
func checkEpisode(t *testing.T, x *Index, ref Ref) {
	t.Helper()
	ep, ok := x.Episode(ref)
	where := fmt.Sprintf("%+v", ref)
	switch {
	case ok && (ref.Kind.order() < 0 || ref.Number < 1):
		t.Errorf("%s: true for a reference naming no episode: %+v", where, ep)
	case !ok && (ep.TVDB != nil || ep.TMDB != nil || ep.Listing != ListingUnknown || ep.ID != 0):
		t.Errorf("%s: false with %+v", where, ep)
	}
	checkStatement(t, where+" TVDB", ep.TVDB)
	checkStatement(t, where+" TMDB", ep.TMDB)
	if (ep.Listing == Listed) != (ep.ID > 0) || ep.Listing < ListingUnknown || ep.Listing > NotListed {
		t.Errorf("%s: listing %d with id %d", where, ep.Listing, ep.ID)
	}
}
