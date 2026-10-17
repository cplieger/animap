package mapread

import (
	"strings"
	"testing"
)

func TestKindsReturnsACopy(t *testing.T) {
	ks := Kinds()
	ks[0] = Other
	if got := Kinds(); got[0] != Regular {
		t.Errorf("Kinds() after a caller changed its result = %v, want regular first", got)
	}
	if _, err := Decode(strings.NewReader(encodeSample(t)), DefaultLimits()); err != nil {
		t.Errorf("Decode after a caller changed Kinds()' result = %v, want the document", err)
	}
}

func TestPartIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		part Part
		want bool
	}{
		{"the rangeless regular part", Part{AniDBID: 1, Kind: Regular}, true},
		{"a ranged regular part", Part{AniDBID: 1, Kind: Regular, Start: 1, End: 12}, false},
		{"a start alone", Part{AniDBID: 1, Kind: Regular, Start: 3}, false},
		{"an end alone", Part{AniDBID: 1, Kind: Regular, End: 12}, false},
		{"a combined part", Part{AniDBID: 1, Kind: Regular, Combined: true}, false},
		{"rangeless specials", Part{AniDBID: 1, Kind: Special}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.part.Identity(); got != tc.want {
				t.Errorf("%+v.Identity() = %t, want %t", tc.part, got, tc.want)
			}
		})
	}
}

func TestRunExtendedBy(t *testing.T) {
	run := Run{Kind: Regular, Start: 1, End: 2, AniDBEpisodeIDs: []int{1, 2}, TVDB: targets(at(1, 1), dflt(2, 5)), TMDB: targets()}
	for _, tc := range []struct {
		name string
		next Run
		want bool
	}{
		{"the next episode on every target", Run{Kind: Regular, Start: 3, End: 4, AniDBEpisodeIDs: []int{3, 4}, TVDB: targets(at(1, 3), dflt(2, 7)), TMDB: targets()}, true},
		{"another kind", Run{Kind: Special, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), dflt(2, 7)), TMDB: targets()}, false},
		{"a gap in the numbers", Run{Kind: Regular, Start: 4, End: 4, AniDBEpisodeIDs: []int{4}, TVDB: targets(at(1, 4), dflt(2, 8)), TMDB: targets()}, false},
		{"not listed", Run{Kind: Regular, Start: 3, End: 3, TVDB: targets(at(1, 3), dflt(2, 7)), TMDB: targets()}, false},
		{"a target not one on", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 4), dflt(2, 7)), TMDB: targets()}, false},
		{"a target on another season", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), dflt(3, 7)), TMDB: targets()}, false},
		{"a target's default flag differs", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), at(2, 7)), TMDB: targets()}, false},
		{"fewer targets", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3)), TMDB: targets()}, false},
		{"a site stating nothing", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), dflt(2, 7))}, false},
		{"a site landing somewhere", Run{Kind: Regular, Start: 3, End: 3, AniDBEpisodeIDs: []int{3}, TVDB: targets(at(1, 3), dflt(2, 7)), TMDB: targets(at(1, 1))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := run.ExtendedBy(&tc.next); got != tc.want {
				t.Errorf("ExtendedBy(%+v) = %t, want %t", tc.next, got, tc.want)
			}
		})
	}
	unstated := Run{Kind: Other, Start: 1, End: 1}
	if !unstated.ExtendedBy(&Run{Kind: Other, Start: 2, End: 2}) {
		t.Error("ExtendedBy of two runs stating nothing on either site = false, want true")
	}
}
