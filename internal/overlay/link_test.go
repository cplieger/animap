package overlay

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

func validLink() Link {
	return Link{
		AniListID: 376, Title: "OVA", Justification: "proven by title",
		Parts:    []Part{{AniDBID: 1544, Specials: []int{1}}},
		Evidence: map[string]string{"anidb": "https://anidb.net/anime/1544"},
		Captured: LinkCaptured{At: "2026-10-15", AniDBMirrorCommit: strings.Repeat("a", 40), AniDBSHA256: strings.Repeat("b", 64)},
	}
}

func TestLinkValidate(t *testing.T) {
	if l := validLink(); l.Validate() != nil {
		t.Fatalf("Setup: Validate(valid) = %v", l.Validate())
	}
	for _, tc := range []struct {
		name string
		edit func(*Link)
	}{
		{"no parts", func(l *Link) { l.Parts = nil }},
		{"no form", func(l *Link) { l.Parts = []Part{{AniDBID: 1}} }},
		{"specials and episodes", func(l *Link) { l.Parts = []Part{{AniDBID: 1, Specials: []int{1}, Episodes: []int{1}}} }},
		{"episodes and whole", func(l *Link) { l.Parts = []Part{{AniDBID: 1, Episodes: []int{1}, Whole: true}} }},
		{"a number below 1", func(l *Link) { l.Parts = []Part{{AniDBID: 1, Episodes: []int{0}}} }},
		{"a number twice in a part", func(l *Link) { l.Parts = []Part{{AniDBID: 1, Specials: []int{2, 2}}} }},
		{"a number twice across parts", func(l *Link) {
			l.Parts = []Part{{AniDBID: 1, Episodes: []int{3}}, {AniDBID: 1, Episodes: []int{3}}}
		}},
		{"a whole part after a regular episode of it", func(l *Link) {
			l.Parts = []Part{{AniDBID: 1, Episodes: []int{3}}, {AniDBID: 1, Whole: true}}
		}},
		{"a regular episode after a whole part of it", func(l *Link) {
			l.Parts = []Part{{AniDBID: 1, Whole: true}, {AniDBID: 1, Episodes: []int{3}}}
		}},
		{"two whole parts of one anime", func(l *Link) { l.Parts = []Part{{AniDBID: 1, Whole: true}, {AniDBID: 1, Whole: true}} }},
		{"a part with no anime", func(l *Link) { l.Parts = []Part{{Specials: []int{1}}} }},
		{"no anilist id", func(l *Link) { l.AniListID = 0 }},
		{"no title", func(l *Link) { l.Title = " " }},
		{"no justification", func(l *Link) { l.Justification = "" }},
		{"no evidence", func(l *Link) { l.Evidence = nil }},
		{"evidence on another host", func(l *Link) { l.Evidence = map[string]string{"x": "https://example.com/1"} }},
		{"no capture date", func(l *Link) { l.Captured.At = "" }},
		{"a capture commit that is not a SHA", func(l *Link) { l.Captured.AniDBMirrorCommit = "main" }},
		{"no capture digest", func(l *Link) { l.Captured.AniDBSHA256 = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := validLink()
			tc.edit(&l)
			if err := l.Validate(); err == nil {
				t.Errorf("Validate(%s) = nil, want a refusal", tc.name)
			}
		})
	}
	for name, parts := range map[string][]Part{
		"a special and a whole part of one anime":         {{AniDBID: 1, Specials: []int{1}}, {AniDBID: 1, Whole: true}},
		"a special and the regular episode of its number": {{AniDBID: 1, Specials: []int{1}}, {AniDBID: 1, Episodes: []int{1}}},
		"whole parts of three anime":                      {{AniDBID: 1, Whole: true}, {AniDBID: 2, Whole: true}, {AniDBID: 3, Whole: true}},
	} {
		l := validLink()
		l.Parts = parts
		if err := l.Validate(); err != nil {
			t.Errorf("Validate(%s) = %v, want accepted", name, err)
		}
	}
}

func TestReadLink(t *testing.T) {
	dir := t.TempDir()
	l := validLink()
	if err := os.MkdirAll(filepath.Join(dir, LinkDir), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, LinkDir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	body := `{"anilist_id":376,"title":"OVA","parts":[{"anidb_id":1544,"specials":[1]}],"justification":"proven by title",` +
		`"evidence":{"anidb":"https://anidb.net/anime/1544"},"captured":{"at":"2026-10-15","anidb_mirror_commit":"` + strings.Repeat("a", 40) +
		`","anidb_sha256":"` + strings.Repeat("b", 64) + `"}}`
	write("376.json", body)
	got, err := LoadLinks(dir)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], l) {
		t.Fatalf("LoadLinks = %+v, %v; want %+v", got, err, l)
	}
	if got[0].Path() != "overlay/links/376.json" {
		t.Errorf("Path() = %q", got[0].Path())
	}
	if _, err := ReadLink(write("377.json", body)); err == nil || !strings.Contains(err.Error(), "must be named 376.json") {
		t.Errorf("ReadLink(misnamed) = %v, want a refusal naming 376.json", err)
	}
	if _, err := ReadLink(write("376.json", strings.Replace(body, `"parts"`, `"kind":"x","parts"`, 1))); err == nil {
		t.Error("ReadLink(an unknown member) = nil, want a strict refusal")
	}
	if links, err := LoadLinks(t.TempDir()); err != nil || len(links) != 0 {
		t.Errorf("LoadLinks(no links directory) = %v, %v; want none", links, err)
	}
}

// facts lists AniDB 10 with three regular episodes, specials 1 and 2,
// promotion 1 (201) and other episode 1 (401), AniDB 20 with two regular
// episodes, and AniDB 30 with one.
// titledRegular is regular episodes 1 to len(digests), each with its title
// digest.
func titledRegular(digests ...string) []anidb.Episode {
	out := make([]anidb.Episode, len(digests))
	for i, d := range digests {
		out[i] = anidb.Episode{Number: i + 1, Titles: d}
	}
	return out
}

func linkFacts() *anidb.Facts {
	d := func(c string) string { return strings.Repeat(c, 64) }
	return anidb.NewFacts(&anidb.Snapshot{Anime: map[int]anidb.Anime{
		10: {
			Regular: titledRegular(d("1"), d("2"), d("3")), Finished: true,
			Specials: []anidb.Episode{{Number: 1, Titles: d("a")}, {Number: 2, Titles: d("b")}},
			Extras:   []anidb.Extra{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201, Titles: d("p")}, {Kind: anidb.Other, Number: 1, SeasonZero: 401, Titles: d("o")}},
		},
		20: {Regular: titledRegular(d("4"), d("5")), Finished: true},
		30: {Regular: titledRegular(d("6")), Finished: true},
		40: {Regular: []anidb.Episode{{Number: 1, Titles: d("7")}, {Number: 2, Titles: d("8")}, {Number: 5, Titles: d("9")}}, Finished: true},
	}}, nil, nil)
}

// A regular list with a gap is read by its numbers, never its positions.
func TestLinkReadsAGappedListByNumber(t *testing.T) {
	facts := linkFacts()
	whole := Link{AniListID: 1, Parts: []Part{{AniDBID: 40, Whole: true}}}
	want := []Source{
		{AniDB: 40, AniDBSeason: 1, Number: 1, AniListEpisode: 1},
		{AniDB: 40, AniDBSeason: 1, Number: 2, AniListEpisode: 1},
		{AniDB: 40, AniDBSeason: 1, Number: 5, AniListEpisode: 1},
	}
	if got := whole.Sources(facts); !slices.Equal(got, want) {
		t.Errorf("Sources(whole AniDB 40) = %+v, want %+v", got, want)
	}
	for _, tc := range []struct {
		episodes []int
		want     int
	}{{[]int{1, 2, 5}, 0}, {[]int{1, 2, 3}, 0}, {[]int{1, 2}, 0}} {
		l := Link{AniListID: 1, Parts: []Part{{AniDBID: 40, Episodes: tc.episodes}}}
		if got, ok := l.Identity(facts); got != tc.want || ok != (tc.want > 0) {
			t.Errorf("Identity(AniDB 40 %v) = %d, %t; want %d", tc.episodes, got, ok, tc.want)
		}
	}
	if err := (&Link{AniListID: 1, Parts: []Part{{AniDBID: 40, Episodes: []int{5}}}}).Listed(facts); err != nil {
		t.Errorf("Listed(AniDB 40 ep5) = %v, want listed", err)
	}
	if err := (&Link{AniListID: 1, Parts: []Part{{AniDBID: 40, Episodes: []int{3}}}}).Listed(facts); err == nil || !strings.Contains(err.Error(), "no ep3 for AniDB 40") {
		t.Errorf("Listed(AniDB 40 ep3) = %v, want the gap refused", err)
	}
	ep5 := Link{AniListID: 1, Parts: []Part{{AniDBID: 40, Episodes: []int{5}}}}
	before := ep5.AniDBDigest(facts)
	a, _ := facts.Anime(40)
	a.Regular = slices.Clone(a.Regular)
	a.Regular[2].Titles = strings.Repeat("c", 64)
	if ep5.AniDBDigest(anidb.NewFacts(&anidb.Snapshot{Anime: map[int]anidb.Anime{40: a}}, nil, nil)) == before {
		t.Error("AniDBDigest(AniDB 40 ep5) did not move when episode 5 was retitled")
	}
}

func TestLinkSources(t *testing.T) {
	l := Link{AniListID: 1, Parts: []Part{
		{AniDBID: 10, Specials: []int{2, 1}}, {AniDBID: 20, Whole: true}, {AniDBID: 10, Episodes: []int{3}}, {AniDBID: 99, Whole: true},
	}}
	want := []Source{
		{AniDB: 10, AniDBSeason: 0, Number: 2, AniListEpisode: 1},
		{AniDB: 10, AniDBSeason: 0, Number: 1, AniListEpisode: 2},
		{AniDB: 20, AniDBSeason: 1, Number: 1, AniListEpisode: 3},
		{AniDB: 20, AniDBSeason: 1, Number: 2, AniListEpisode: 3},
		{AniDB: 10, AniDBSeason: 1, Number: 3, AniListEpisode: 4},
	}
	if got := l.Sources(linkFacts()); !slices.Equal(got, want) {
		t.Errorf("Sources = %+v, want %+v", got, want)
	}
	if got := l.episodes(); got != 5 {
		t.Errorf("episodes() = %d, want 5: two specials, two whole parts and one regular episode", got)
	}
	if got := l.AniDBIDs(); !slices.Equal(got, []int{10, 20, 99}) {
		t.Errorf("AniDBIDs() = %v, want [10 20 99]", got)
	}
}

func TestLinkIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []Part
		want  int
	}{
		{"every regular episode in order", []Part{{AniDBID: 10, Episodes: []int{1, 2, 3}}}, 10},
		{"a partial list", []Part{{AniDBID: 10, Episodes: []int{1, 2}}}, 0},
		{"a reordered list", []Part{{AniDBID: 10, Episodes: []int{1, 3, 2}}}, 0},
		{"two parts", []Part{{AniDBID: 30, Episodes: []int{1}}, {AniDBID: 20, Whole: true}}, 0},
		{"a whole part", []Part{{AniDBID: 30, Whole: true}}, 0},
		{"specials", []Part{{AniDBID: 10, Specials: []int{1, 2}}}, 0},
		{"an anime the mirror does not list", []Part{{AniDBID: 99, Episodes: []int{1}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Link{AniListID: 1, Parts: tc.parts}
			if got, ok := l.Identity(linkFacts()); got != tc.want || ok != (tc.want > 0) {
				t.Errorf("Identity = %d, %t; want %d", got, ok, tc.want)
			}
		})
	}
}

// An identity is published as the rangeless part 1 to Count, so a link is
// one only when its episodes are exactly those; with no count yet, when
// they are the mirror's list 1 to N, which that part names none of.
func TestLinkIdentityNeedsTheCount(t *testing.T) {
	upTo := func(n int) []anidb.Episode {
		var out []anidb.Episode
		for k := 1; k <= n; k++ {
			out = append(out, anidb.Episode{Number: k})
		}
		return out
	}
	facts := anidb.NewFacts(&anidb.Snapshot{Anime: map[int]anidb.Anime{
		50: {Regular: upTo(3)}, 60: {Regular: upTo(14)}, 70: {Regular: upTo(12)}, 75: {Regular: upTo(2), Specials: upTo(2)},
		80: {Regular: upTo(3)}, 90: {Regular: []anidb.Episode{{Number: 1}, {Number: 2}, {Number: 5}}},
	}}, nil, map[int]int{50: 12, 60: 12, 70: 12, 75: 2})
	regular := func(aid int, episodes ...int) []Part { return []Part{{AniDBID: aid, Episodes: episodes}} }
	for _, tc := range []struct {
		name  string
		parts []Part
		want  int
	}{
		{"a list short of its count", regular(50, 1, 2, 3), 0},
		{"a list past its count", regular(60, upToInts(14)...), 0},
		{"the counted list", regular(70, upToInts(12)...), 70},
		{"the counted list less its first episode", regular(70, upToInts(12)[1:]...), 0},
		{"part of the counted list", regular(70, upToInts(11)...), 0},
		{"specials 1 to the count", []Part{{AniDBID: 75, Specials: []int{1, 2}}}, 0},
		{"an uncounted list", regular(80, 1, 2, 3), 80},
		{"part of an uncounted list", regular(80, 1, 2), 0},
		{"an uncounted gapped list", regular(90, 1, 2, 5), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Link{AniListID: 1, Parts: tc.parts}
			if got, ok := l.Identity(facts); got != tc.want || ok != (tc.want > 0) {
				t.Errorf("Identity(%+v) = %d, %t; want %d", tc.parts, got, ok, tc.want)
			}
		})
	}
}

func upToInts(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

func TestLinkListed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []Part
		says  string
	}{
		{"listed", []Part{{AniDBID: 10, Specials: []int{2}}, {AniDBID: 10, Episodes: []int{3}}, {AniDBID: 20, Whole: true}}, ""},
		{"a promotion and an other episode", []Part{{AniDBID: 10, Specials: []int{201, 401}}}, ""},
		{"a special the mirror lacks", []Part{{AniDBID: 10, Specials: []int{3}}}, "no S3 for AniDB 10"},
		{"an other episode the mirror lacks", []Part{{AniDBID: 10, Specials: []int{402}}}, "no S402 for AniDB 10"},
		{"a regular episode past the list", []Part{{AniDBID: 10, Episodes: []int{4}}}, "no ep4 for AniDB 10"},
		{"an unlisted anime", []Part{{AniDBID: 99, Episodes: []int{1}}}, "no ep1 for AniDB 99"},
		{"a whole unlisted anime", []Part{{AniDBID: 99, Whole: true}}, "AniDB 99 lists no regular episode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Link{AniListID: 1, Parts: tc.parts}
			err := l.Listed(linkFacts())
			if (tc.says == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.says)) {
				t.Errorf("Listed = %v, want %q", err, tc.says)
			}
		})
	}
}

// A whole part is published as AniDB's regular episodes 1 to Count, so it
// is listed only when the mirror lists exactly those.
func TestLinkListedWholePartNeedsTheCountedList(t *testing.T) {
	upTo := func(n int, finished bool) anidb.Anime {
		a := anidb.Anime{Finished: finished}
		for k := 1; k <= n; k++ {
			a.Regular = append(a.Regular, anidb.Episode{Number: k})
		}
		return a
	}
	facts := anidb.NewFacts(&anidb.Snapshot{Anime: map[int]anidb.Anime{
		40: {Regular: []anidb.Episode{{Number: 1}, {Number: 2}, {Number: 5}}, Finished: true},
		50: upTo(3, false), 60: upTo(14, false), 70: upTo(2, true), 80: upTo(3, true), 90: upTo(12, false),
	}}, []anidb.Row{{AniDBID: 80, RegularEpisodes: 4}}, map[int]int{40: 5, 50: 12, 60: 12, 90: 12})
	for _, tc := range []struct {
		name string
		aid  int
		says string
	}{
		{"the finished list", 70, ""},
		{"an airing list at its database count", 90, ""},
		{"a gapped list", 40, "AniDB 40 lists regular episodes with a gap before ep5"},
		{"an airing list short of its count", 50, "AniDB 50 lists 3 regular episodes and counts 12"},
		{"an airing list past its count", 60, "AniDB 60 lists 14 regular episodes and counts 12"},
		{"a list a counts row overrides", 80, "AniDB 80 lists 3 regular episodes and counts 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Link{AniListID: 1, Parts: []Part{{AniDBID: tc.aid, Whole: true}}}
			err := l.Listed(facts)
			if (tc.says == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.says)) {
				t.Errorf("Listed(whole AniDB %d) = %v, want %q", tc.aid, err, tc.says)
			}
		})
	}
}

func TestLinkAniDBDigest(t *testing.T) {
	l := Link{AniListID: 1, Parts: []Part{{AniDBID: 10, Specials: []int{1, 401}}, {AniDBID: 20, Whole: true}}}
	base := linkFacts()
	want := l.AniDBDigest(base)
	if !hexRE.MatchString(want) {
		t.Fatalf("AniDBDigest = %q, want 64 hex", want)
	}
	edit := func(f func(map[int]anidb.Anime)) *anidb.Facts {
		m := map[int]anidb.Anime{}
		for _, id := range []int{10, 20} {
			a, _ := base.Anime(id)
			a.Specials, a.Extras, a.Regular = slices.Clone(a.Specials), slices.Clone(a.Extras), slices.Clone(a.Regular)
			m[id] = a
		}
		f(m)
		return anidb.NewFacts(&anidb.Snapshot{Anime: m}, nil, nil)
	}
	for _, tc := range []struct {
		name  string
		f     func(map[int]anidb.Anime)
		moves bool
	}{
		{"the linked special retitled", func(m map[int]anidb.Anime) { m[10].Specials[0].Titles = strings.Repeat("c", 64) }, true},
		{"another special retitled", func(m map[int]anidb.Anime) { m[10].Specials[1].Titles = strings.Repeat("c", 64) }, false},
		{"the linked other episode retitled", func(m map[int]anidb.Anime) { m[10].Extras[1].Titles = strings.Repeat("c", 64) }, true},
		{"another extra retitled", func(m map[int]anidb.Anime) { m[10].Extras[0].Titles = strings.Repeat("c", 64) }, false},
		{"an unlinked extra added", func(m map[int]anidb.Anime) {
			a := m[10]
			a.Extras = append(a.Extras, anidb.Extra{Kind: anidb.Other, Number: 2, SeasonZero: 402, Titles: strings.Repeat("c", 64)})
			m[10] = a
		}, false},
		{"the linked other episode removed", func(m map[int]anidb.Anime) {
			a := m[10]
			a.Extras = a.Extras[:1]
			m[10] = a
		}, true},
		{"another anime's regular episode retitled", func(m map[int]anidb.Anime) { m[10].Regular[0].Titles = strings.Repeat("c", 64) }, false},
		{"a whole part's episode retitled", func(m map[int]anidb.Anime) { m[20].Regular[1].Titles = strings.Repeat("c", 64) }, true},
		{"the linked special removed", func(m map[int]anidb.Anime) {
			a := m[10]
			a.Specials = a.Specials[1:]
			m[10] = a
		}, true},
		{"a whole part's episode added", func(m map[int]anidb.Anime) {
			a := m[20]
			a.Regular = append(a.Regular, anidb.Episode{Number: 3, Titles: strings.Repeat("7", 64)})
			m[20] = a
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if moved := l.AniDBDigest(edit(tc.f)) != want; moved != tc.moves {
				t.Errorf("AniDBDigest moved=%t, want %t", moved, tc.moves)
			}
		})
	}
}

func linkNode(id int, attrs map[string]string, rows ...animelists.Row) *animelists.Node {
	return &animelists.Node{AniDBID: id, Attrs: attrs, Rows: rows}
}

func row(anidbSeason, tvdbSeason, text string) animelists.Row {
	return animelists.Row{Attrs: map[string]string{"anidbseason": anidbSeason, "tvdbseason": tvdbSeason}, Text: text}
}

func TestLinkCompile(t *testing.T) {
	onSeason := func(s string) map[string]string { return map[string]string{"tvdbid": "500", "defaulttvdbseason": s} }
	absolute := []skyhook.Episode{{Season: 1, Number: 1, Absolute: 1}, {Season: 1, Number: 2, Absolute: 2}, {Season: 2, Number: 1, Absolute: 3}}
	entries := map[int]*Entry{10: {AniDBID: 10, Captured: Captured{TVDB: &CapturedTVDB{Series: 500, Episodes: absolute}}}}
	for _, tc := range []struct {
		nodes   map[int]*animelists.Node
		entries map[int]*Entry
		name    string
		says    string
		parts   []Part
		want    Compiled
	}{
		{
			name: "a default target, with no offset", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1"))},
			parts: []Part{{AniDBID: 10, Episodes: []int{2}}},
			want:  Compiled{AniList: 1, Series: 500, Season: 1, Targets: []int{2}, Claims: []Claim{{Parent: 10, AniDBSeason: 1, Number: 2, Target: [2]int{1, 2}}}},
		},
		{
			name: "specials through a row", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1"), row("0", "0", ";1-4;2-5;"))},
			parts: []Part{{AniDBID: 10, Specials: []int{2, 1}}},
			want: Compiled{AniList: 1, Series: 500, Season: 0, Targets: []int{5, 4}, Claims: []Claim{
				{Parent: 10, Number: 2, Target: [2]int{0, 5}}, {Parent: 10, Number: 1, Target: [2]int{0, 4}},
			}},
		},
		{
			name:  "other episodes through a row onto another season",
			nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("0"), row("0", "0", ";1-4;"), row("0", "1", ";401-1;"))},
			parts: []Part{{AniDBID: 10, Specials: []int{401}}},
			want:  Compiled{AniList: 1, Series: 500, Season: 1, Targets: []int{1}, Claims: []Claim{{Parent: 10, Number: 401, Target: [2]int{1, 1}}}},
		},
		{
			name: "absolute numbers through the parent entry's capture", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("a"))},
			entries: entries, parts: []Part{{AniDBID: 10, Episodes: []int{3}}},
			want: Compiled{AniList: 1, Series: 500, Season: 2, Targets: []int{1}, Claims: []Claim{{Parent: 10, AniDBSeason: 1, Number: 3, Target: [2]int{2, 1}}}},
		},
		{
			name: "whole parts of three anime, each collapsing to one target",
			nodes: map[int]*animelists.Node{
				10: linkNode(10, onSeason("1"), row("1", "0", ";1-2;2-2;3-2;")), 20: linkNode(20, onSeason("1"), row("1", "0", ";1-3;2-3;")),
				30: linkNode(30, onSeason("1"), row("1", "0", ";1-4;")),
			},
			parts: []Part{{AniDBID: 10, Whole: true}, {AniDBID: 20, Whole: true}, {AniDBID: 30, Whole: true}},
			want: Compiled{AniList: 1, Series: 500, Season: 0, Targets: []int{2, 3, 4}, Claims: []Claim{
				{Parent: 10, AniDBSeason: 1, Number: 1, Target: [2]int{0, 2}},
				{Parent: 10, AniDBSeason: 1, Number: 2, Target: [2]int{0, 2}},
				{Parent: 10, AniDBSeason: 1, Number: 3, Target: [2]int{0, 2}},
				{Parent: 20, AniDBSeason: 1, Number: 1, Target: [2]int{0, 3}},
				{Parent: 20, AniDBSeason: 1, Number: 2, Target: [2]int{0, 3}},
				{Parent: 30, AniDBSeason: 1, Number: 1, Target: [2]int{0, 4}},
			}},
		},
		{
			name: "an absolute number with no parent entry", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("a"))},
			parts: []Part{{AniDBID: 10, Episodes: []int{3}}},
			says:  "AniList 1: AniDB 10 ep3 lands on TVDB absolute episode 3; no overlay entry for AniDB 10 captures one TVDB episode with that number",
		},
		{
			name: "an absolute number the entry's capture lacks", nodes: map[int]*animelists.Node{10: linkNode(10, map[string]string{"tvdbid": "500", "defaulttvdbseason": "a", "episodeoffset": "5"})},
			entries: entries, parts: []Part{{AniDBID: 10, Episodes: []int{1}}}, says: "lands on TVDB absolute episode 6",
		},
		{
			name: "an absolute number on another series' capture", nodes: map[int]*animelists.Node{10: linkNode(10, map[string]string{"tvdbid": "501", "defaulttvdbseason": "a"})},
			entries: entries, parts: []Part{{AniDBID: 10, Episodes: []int{3}}}, says: "lands on TVDB absolute episode 3",
		},
		{
			name: "two targets", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1"), row("0", "0", ";1-4+5;"))},
			parts: []Part{{AniDBID: 10, Specials: []int{1}}}, says: "AniList 1: AniDB 10 S1 maps to no single TVDB episode",
		},
		{
			name: "no target", nodes: map[int]*animelists.Node{10: linkNode(10, map[string]string{"tvdbid": "500"})},
			parts: []Part{{AniDBID: 10, Episodes: []int{1}}}, says: "maps to no single TVDB episode",
		},
		{
			name: "a whole part on two targets", nodes: map[int]*animelists.Node{20: linkNode(20, onSeason("1"))},
			parts: []Part{{AniDBID: 20, Whole: true}}, says: "AniList 1 episode 1 lands on TVDB 1x1 and TVDB 1x2",
		},
		{
			name: "no parent node", nodes: map[int]*animelists.Node{}, parts: []Part{{AniDBID: 10, Specials: []int{1}}},
			says: "AniList 1: AniDB 10 has no Anime-Lists node",
		},
		{
			name: "a parent with no TVDB series", nodes: map[int]*animelists.Node{10: linkNode(10, map[string]string{"tvdbid": "OVA"})},
			parts: []Part{{AniDBID: 10, Specials: []int{1}}}, says: `AniDB 10 is on no TVDB series (tvdbid "OVA")`,
		},
		{
			name:  "two series",
			nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1")), 20: linkNode(20, map[string]string{"tvdbid": "600", "defaulttvdbseason": "1"})},
			parts: []Part{{AniDBID: 10, Episodes: []int{1}}, {AniDBID: 20, Episodes: []int{1}}}, says: "on TVDB 500 and 600",
		},
		{
			name: "two seasons", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1"))},
			parts: []Part{{AniDBID: 10, Episodes: []int{1}}, {AniDBID: 10, Specials: []int{1}}}, says: "on TVDB seasons 1 and 0",
		},
		{
			name: "two AniList episodes on one target", nodes: map[int]*animelists.Node{10: linkNode(10, onSeason("1"), row("0", "0", ";1-4;2-4;"))},
			parts: []Part{{AniDBID: 10, Specials: []int{1, 2}}}, says: "AniList 1 episodes 1 and 2 both land on TVDB 0x4",
		},
		{
			name: "a regular episode past the count", nodes: map[int]*animelists.Node{30: linkNode(30, onSeason("1"))},
			parts: []Part{{AniDBID: 30, Episodes: []int{2}}}, says: "AniDB 30 counts 1 regular episodes, so the collision check places no ep2",
		},
		{
			name: "a whole part the mirror lacks", nodes: map[int]*animelists.Node{99: linkNode(99, onSeason("1"))},
			parts: []Part{{AniDBID: 99, Whole: true}}, says: "AniList 1 episode 1 names no AniDB episode the mirror lists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Link{AniListID: 1, Parts: tc.parts}
			got, err := l.Compile(tc.nodes, tc.entries, linkFacts())
			if tc.says != "" {
				if err == nil || !strings.Contains(err.Error(), tc.says) {
					t.Errorf("Compile = %+v, %v; want a refusal saying %q", got, err, tc.says)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Compile = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}
