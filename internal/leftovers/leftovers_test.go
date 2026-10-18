package leftovers

import (
	"slices"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/skyhook"
)

// node is AniDB 10 on TVDB 5000 season 1, its specials row mapping S1 to
// 0x1 and S2 nowhere.
func node() *animelists.Node {
	return &animelists.Node{AniDBID: 10, Attrs: map[string]string{"tvdbid": "5000", "defaulttvdbseason": "1"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-1;2-0;"},
	}}
}

func layout(slug string, eps ...skyhook.Episode) *datecheck.Layout {
	lay := &datecheck.Layout{Slug: slug}
	for _, e := range eps {
		lay.Episodes = append(lay.Episodes, datecheck.Aired{Episode: e})
	}
	return lay
}

func s0(n int, date string) skyhook.Episode {
	return skyhook.Episode{Season: 0, Number: n, AirDate: date}
}

// anime lists specials 1 to 3 in 2020 and one extra of each kind.
func anime() *anidb.Anime {
	return &anidb.Anime{
		Regular:  []anidb.Episode{{Number: 1, AirDate: "2020-01-06"}},
		Specials: []anidb.Episode{{Number: 1, AirDate: "2020-02-01"}, {Number: 2, AirDate: "2020-03-01"}, {Number: 3, AirDate: "2020-04-01"}},
		Extras: []anidb.Extra{
			{Kind: anidb.Credit, Number: 1, SeasonZero: 101},
			{Kind: anidb.Promotion, Number: 1, SeasonZero: 201},
			{Kind: anidb.Parody, Number: 1, SeasonZero: 301},
			{Kind: anidb.Other, Number: 1, SeasonZero: 401},
		},
	}
}

func claimsOf(m map[[2]int][]string) func() map[[2]int][]string {
	return func() map[[2]int][]string { return m }
}

func TestFindPairsWhatLandsNowhereWithWhatNothingClaims(t *testing.T) {
	lay := layout("show", skyhook.Episode{Season: 1, Number: 1}, s0(1, "2020-02-01"), s0(5, "2020-03-02"), s0(6, ""))
	claims := map[[2]int][]string{{1, 1}: {"10 ep1"}, {0, 1}: {"10 S1"}, {0, 6}: {"11 S1"}}
	f := Find(node(), animelists.TVDB, 5000, anime(), lay, claimsOf(claims))
	if f == nil {
		t.Fatal("Find = nil, want a pair")
	}
	want := []Episode{{Kind: anidb.Special, Number: 2, SeasonZero: 2}, {Kind: anidb.Special, Number: 3, SeasonZero: 3}, {Kind: anidb.Other, Number: 1, SeasonZero: 401}}
	if !slices.Equal(f.Unplaced, want) {
		t.Errorf("unplaced = %v, want %v: S1 lands, S2 is -0, S3 and other 1 default to episodes TVDB lacks, credits and parodies never count", f.Unplaced, want)
	}
	if want := []Episode{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}}; !slices.Equal(f.Promotions, want) {
		t.Errorf("promotions = %v, want %v", f.Promotions, want)
	}
	if !slices.Equal(f.Unclaimed, []int{5}) {
		t.Errorf("unclaimed = %v, want [5]: 0x1 is this node's, 0x6 another node's", f.Unclaimed)
	}
	if f.Key() != "tvdb:5000" || f.Slug != "show" || len(f.Inputs) != 64 || !ValidFindingKey(f.Key()) {
		t.Errorf("finding %+v, want key tvdb:5000 with the slug and a digest", f)
	}
}

// Credits, parodies and promotions never raise the section by themselves,
// and the claims are not read when every special and other episode lands
// by a row.
func TestFindIgnoresExtrasThatCannotRaiseAlone(t *testing.T) {
	a := anime()
	a.Specials = a.Specials[:1]
	a.Extras = slices.DeleteFunc(a.Extras, func(x anidb.Extra) bool { return x.Kind == anidb.Other })
	lay := layout("", s0(1, ""), s0(5, ""))
	read := false
	f := Find(node(), animelists.TVDB, 5000, a, lay, func() map[[2]int][]string { read = true; return nil })
	if f != nil || read {
		t.Errorf("Find = %+v, claims read %t; want nil without reading them", f, read)
	}
}

func TestFindNeedsAnUnclaimedEpisode(t *testing.T) {
	lay := layout("", s0(1, ""), s0(5, ""))
	claims := map[[2]int][]string{{0, 1}: {"10 S1"}, {0, 5}: {"12 S4 (default)"}}
	if f := Find(node(), animelists.TVDB, 5000, anime(), lay, claimsOf(claims)); f != nil {
		t.Errorf("Find = %+v, want nil: every season 0 episode is claimed", f)
	}
}

// An other episode and a promotion no Anime-Lists number addresses land
// nowhere, so they are listed with no season 0 number.
func TestFindListsExtrasNoAniListsNumberAddresses(t *testing.T) {
	a := anime()
	a.Specials = a.Specials[:1]
	a.Extras = []anidb.Extra{{Kind: anidb.Promotion, Number: 2}, {Kind: anidb.Other, Number: 2}}
	f := Find(node(), animelists.TVDB, 5000, a, layout("", s0(1, ""), s0(5, "")), claimsOf(map[[2]int][]string{{0, 1}: {"10 S1"}}))
	if f == nil {
		t.Fatal("Find = nil, want other 2 beside 0x5")
	}
	if !slices.Equal(f.Unplaced, []Episode{{Kind: anidb.Other, Number: 2}}) || !slices.Equal(f.Promotions, []Episode{{Kind: anidb.Promotion, Number: 2}}) || !slices.Equal(f.Unclaimed, []int{5}) {
		t.Errorf("finding = %+v, want other 2 and promotion 2 with no season 0 number beside 0x5", f)
	}
}

// Anime-Lists gives only a special a default: an other episode or a
// promotion no row names lands nowhere, though the show has the episode
// its number would default to.
func TestFindGivesOnlyASpecialItsDefault(t *testing.T) {
	a := anime()
	a.Specials = []anidb.Episode{{Number: 1}, {Number: 3}}
	lay := layout("", s0(1, ""), s0(3, ""), s0(201, ""), s0(401, ""))
	claims := map[[2]int][]string{{0, 1}: {"10 S1"}, {0, 3}: {"10 S3 (default)"}}
	f := Find(node(), animelists.TVDB, 5000, a, lay, claimsOf(claims))
	if f == nil {
		t.Fatal("Find = nil, want other 1 beside the unclaimed 0x401")
	}
	if want := []Episode{{Kind: anidb.Other, Number: 1, SeasonZero: 401}}; !slices.Equal(f.Unplaced, want) {
		t.Errorf("unplaced = %v, want %v: special 3 lands on its default 0x3", f.Unplaced, want)
	}
	if want := []Episode{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}}; !slices.Equal(f.Promotions, want) {
		t.Errorf("promotions = %v, want %v", f.Promotions, want)
	}
	if !slices.Equal(f.Unclaimed, []int{201, 401}) {
		t.Errorf("unclaimed = %v, want [201 401]", f.Unclaimed)
	}
}

// A row places an extra where it names, and on TVDB an applied link's
// claim places a default one on an episode the show has; TMDB takes no
// link claims. The claims are read once.
func TestFindPlacesExtrasByRowsAndLinks(t *testing.T) {
	a := anime()
	a.Specials = a.Specials[:2]
	a.Extras = append(a.Extras, anidb.Extra{Kind: anidb.Promotion, Number: 2, SeasonZero: 202})
	lay := layout("", s0(1, ""), s0(5, ""), s0(9, ""), s0(201, ""))
	tvdb := &animelists.Node{AniDBID: 10, Attrs: map[string]string{"tvdbid": "5000", "defaulttvdbseason": "1"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "0", "tvdbseason": "0"}, Text: ";1-1;2-0;401-9;"},
	}}
	claims := map[[2]int][]string{{0, 1}: {"10 S1"}, {0, 9}: {"10 S401"}, {0, 201}: {"10 S201"}, {0, 202}: {"10 S202"}}
	reads := 0
	f := Find(tvdb, animelists.TVDB, 5000, a, lay, func() map[[2]int][]string { reads++; return claims })
	if f == nil || !slices.Equal(f.Unplaced, []Episode{{Kind: anidb.Special, Number: 2, SeasonZero: 2}}) || !slices.Equal(f.Unclaimed, []int{5}) {
		t.Errorf("Find on TVDB = %+v, want special 2 beside 0x5: the row places other 1", f)
	}
	if want := []Episode{{Kind: anidb.Promotion, Number: 2, SeasonZero: 202}}; f == nil || !slices.Equal(f.Promotions, want) {
		t.Errorf("Find on TVDB = %+v, want promotions %v: the link places promotion 1, and promotion 2 on 0x202, which the show lacks", f, want)
	}
	if reads != 1 {
		t.Errorf("claims read %d times, want once", reads)
	}
	tmdb := &animelists.Node{AniDBID: 10, Attrs: map[string]string{"tmdbtv": "9000", "tmdbseason": "1"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "0", "tmdbseason": "0"}, Text: ";1-1;2-0;401-9;"},
	}}
	f = Find(tmdb, animelists.TMDB, 9000, a, lay, claimsOf(claims))
	if f == nil || !slices.Equal(f.Unplaced, []Episode{{Kind: anidb.Special, Number: 2, SeasonZero: 2}}) {
		t.Errorf("Find on TMDB = %+v, want only special 2 unplaced: the row places other 1", f)
	}
	if f == nil || !slices.Equal(f.Promotions, []Episode{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}, {Kind: anidb.Promotion, Number: 2, SeasonZero: 202}}) {
		t.Errorf("Find on TMDB = %+v, want both promotions listed: no link places one there", f)
	}
}

// A dated special pairs only with an unclaimed episode of the same airing,
// and every special with an undated one.
func TestFindPairsADatedSpecialByItsAiring(t *testing.T) {
	a := anime()
	a.Extras, a.Specials[1].AirDate = nil, "2020-03-10"
	lay := layout("", s0(1, ""), s0(5, "2020-03-11"), s0(6, "2020-03-12"), s0(7, "2020-04-20"), s0(8, ""), s0(9, "x"))
	f := Find(node(), animelists.TVDB, 5000, a, lay, claimsOf(map[[2]int][]string{{0, 1}: {"10 S1"}}))
	if f == nil || !slices.Equal(f.Unclaimed, []int{5, 7, 8, 9}) {
		t.Errorf("Find = %+v, want unclaimed 5 (a day from S2), 7 (S3's month-precision date), 8 and 9 (undated)", f)
	}
}

// An undated special pairs with an unclaimed episode dated within the
// anime's span widened by a month, or any when AniDB dates nothing of the
// anime; an other episode, which AniDB's mirror never dates, with any.
func TestFindPairsAnUndatedEpisodeWithinTheAnimesSpan(t *testing.T) {
	a := anime()
	a.Specials, a.Extras = []anidb.Episode{{Number: 1, AirDate: "2020-02-01"}, {Number: 2, AirDate: "2020-04-10"}, {Number: 3}}, nil
	lay := layout("", s0(1, ""), s0(2, "2020-04-10"), s0(5, "2019-12-06"), s0(6, "2019-12-05"), s0(7, "2020-05-11"), s0(8, "2020-05-12"))
	claims := map[[2]int][]string{{0, 1}: {"10 S1"}, {0, 2}: {"10 S2"}}
	f := Find(node(), animelists.TVDB, 5000, a, lay, claimsOf(claims))
	if f == nil || !slices.Equal(f.Unclaimed, []int{5, 7}) {
		t.Fatalf("Find = %+v, want unclaimed 5 and 7: within 31 days of the span", f)
	}
	a.Extras = []anidb.Extra{{Kind: anidb.Other, Number: 1, SeasonZero: 401}}
	if f := Find(node(), animelists.TVDB, 5000, a, lay, claimsOf(claims)); f == nil || !slices.Equal(f.Unclaimed, []int{5, 6, 7, 8}) {
		t.Errorf("Find with other 1 unplaced = %+v, want every unclaimed episode", f)
	}
	a.Extras, a.Regular, a.Specials = nil, nil, []anidb.Episode{{Number: 1}, {Number: 2}, {Number: 3}}
	if f := Find(node(), animelists.TVDB, 5000, a, lay, claimsOf(claims)); f == nil || !slices.Equal(f.Unclaimed, []int{5, 6, 7, 8}) {
		t.Errorf("Find for an anime AniDB dates nothing of = %+v, want every unclaimed episode", f)
	}
}

// On TMDB a node places its specials through its TMDB rows and season.
func TestFindOnTMDB(t *testing.T) {
	n := &animelists.Node{AniDBID: 10, Attrs: map[string]string{"tmdbtv": "9000", "tmdbseason": "1"}, Rows: []animelists.Row{
		{Attrs: map[string]string{"anidbseason": "0", "tmdbseason": "0"}, Text: ";1-4;"},
	}}
	a := anime()
	a.Specials, a.Extras = a.Specials[:2], nil
	f := Find(n, animelists.TMDB, 9000, a, layout("", s0(3, ""), s0(4, "")), claimsOf(map[[2]int][]string{{0, 4}: {"10 S1"}}))
	if f == nil || f.Key() != "tmdb:9000" || !slices.Equal(f.Unplaced, []Episode{{Kind: anidb.Special, Number: 2, SeasonZero: 2}}) || !slices.Equal(f.Unclaimed, []int{3}) {
		t.Errorf("Find on TMDB = %+v, want special 2, defaulting to the missing 0x2, beside unclaimed 3", f)
	}
}

// The digest moves with the show and both sets, and with nothing else.
func TestFindingDigest(t *testing.T) {
	base := Finding{Side: animelists.TVDB, Series: 5000, Unplaced: []Episode{{Kind: anidb.Special, Number: 2, SeasonZero: 2}}, Unclaimed: []int{5}}
	d := base.digest()
	same := base
	same.Slug = "renamed"
	if same.digest() != d {
		t.Error("a slug moved the digest")
	}
	for name, change := range map[string]func(*Finding){
		"side":   func(f *Finding) { f.Side = animelists.TMDB },
		"series": func(f *Finding) { f.Series = 5001 },
		"unplaced": func(f *Finding) {
			f.Unplaced = append(slices.Clone(f.Unplaced), Episode{Kind: anidb.Other, Number: 1, SeasonZero: 401})
		},
		"promotion": func(f *Finding) { f.Promotions = []Episode{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}} },
		"unclaimed": func(f *Finding) { f.Unclaimed = []int{5, 6} },
	} {
		t.Run(name, func(t *testing.T) {
			f := base
			change(&f)
			if f.digest() == d {
				t.Errorf("changing the %s left the digest", name)
			}
		})
	}
}

func TestValidFindingKey(t *testing.T) {
	for k, want := range map[string]bool{"tvdb:5000": true, "tmdb:1": true, "tvdb:0": false, "tvdb:05": false, "imdb:5": false, "tvdb:": false, "tvdb:5 ": false} {
		if got := ValidFindingKey(k); got != want {
			t.Errorf("ValidFindingKey(%q) = %t, want %t", k, got, want)
		}
	}
}
