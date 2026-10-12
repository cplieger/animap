package datecheck

import (
	"cmp"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

var now = time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)

func TestSameAiring(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"2005-08-31", "2005-08-31", true},
		{"2005-08-31", "2005-09-01", true},
		{"2005-09-01", "2005-08-31", true},
		{"2005-08-31", "2005-09-02", false},
		{"2005-08-31", "2005-09-07", false},
		{"2001-04-01", "2001-04-18", true},
		{"2001-04-18", "2001-04-01", true},
		{"2001-04-01", "2001-05-18", false},
		{"2001-01-01", "2001-11-30", true},
		{"2001-11-30", "2001-01-01", true},
		{"2001-01-01", "2002-03-03", false},
		{"2001-04-18", "", false},
		{"18/04/2001", "2001-04-18", false},
	} {
		if got := sameAiring(tc.a, tc.b); got != tc.want {
			t.Errorf("sameAiring(%q, %q) = %t, want %t", tc.a, tc.b, got, tc.want)
		}
	}
}

func node(attrs map[string]string, rows ...animelists.Row) *animelists.Node {
	return &animelists.Node{AniDBID: 1, Attrs: attrs, Rows: rows}
}

func row(attrs map[string]string, text string) animelists.Row {
	return animelists.Row{Attrs: attrs, Text: text}
}

func ep(s, e, abs int, date string) skyhook.Episode {
	return skyhook.Episode{Season: s, Number: e, Absolute: abs, AirDate: date}
}

// weekly is a season 1 airing a week apart from 2005-08-10, with one
// episode with no date, a fifth episode after a break, and two specials.
var weekly = &Layout{Episodes: []skyhook.Episode{
	ep(0, 1, 0, "2005-11-23"), ep(0, 3, 0, "2005-12-21"),
	ep(1, 1, 1, "2005-08-10"), ep(1, 2, 2, "2005-08-17"), ep(1, 3, 3, "2005-08-24"), ep(1, 4, 4, ""), ep(1, 5, 5, "2005-10-05"),
}}

// daily is a season 1 a dub aired daily from 1983-08-01, and a season 0,
// neither on any date the weekly AniDB dates below come near.
var daily = &Layout{Episodes: []skyhook.Episode{
	ep(0, 1, 0, "1990-03-03"), ep(0, 2, 0, "1990-03-10"), ep(0, 3, 0, "1990-03-17"), ep(0, 4, 0, "1990-03-24"),
	ep(1, 1, 1, "1983-08-01"), ep(1, 2, 2, "1983-08-02"), ep(1, 3, 3, "1983-08-03"), ep(1, 4, 4, "1983-08-04"),
}}

// winter is four weekly AniDB dates in 1984.
var winter = []string{"1984-01-08", "1984-01-15", "1984-01-22", "1984-01-29"}

func season1(rows ...animelists.Row) *animelists.Node {
	return node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "1"}, rows...)
}

func kinds(l *Lead) []Kind {
	if l == nil {
		return nil
	}
	var out []Kind
	for _, f := range l.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func TestCheckFlags(t *testing.T) {
	for _, tc := range []struct {
		node   *animelists.Node
		layout *Layout
		anime  anidb.Anime
		name   string
		want   []Kind
	}{
		{
			name: "a row one episode off on a weekly show", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";2-3;")),
			anime: anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17"}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "a default past the season's end", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17", "2005-08-24", "2005-08-31", "2005-09-07", "2005-09-14"}, Finished: true},
			want:  []Kind{MissingTarget},
		},
		{
			name: "a row special onto an episode TVDB lacks", node: season1(row(map[string]string{"anidbseason": "0", "tvdbseason": "1"}, ";1-11;")),
			anime: anidb.Anime{Specials: []anidb.Episode{{Number: 1, AirDate: "1999-01-01"}}, Finished: true}, want: []Kind{MissingTarget},
		},
		{
			name: "a special's date no TVDB special meets on a series that keeps AniDB's calendar", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-08-10"}, Specials: []anidb.Episode{{Number: 1, AirDate: "2005-12-07"}}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "a default special TVDB files elsewhere", node: season1(),
			anime: anidb.Anime{Specials: []anidb.Episode{{Number: 2, AirDate: "2005-11-23"}}, Finished: true}, want: []Kind{MissingTarget},
		},
		{
			name: "one of two targets on the wrong date", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-1+2;")),
			anime: anidb.Anime{Regular: []string{"2005-08-10"}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "an absolute number the series lacks", node: node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "a", "episodeoffset": "9"}),
			anime: anidb.Anime{Regular: []string{"2005-08-31"}, Finished: true}, want: []Kind{MissingTarget},
		},
		{
			name: "a missing episode on a date another episode shares", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17", "2005-08-24", "2005-08-31", "2005-09-14", "2005-09-14"}, Finished: true},
			want:  []Kind{MissingTarget},
		},
		{
			name: "a special on the date of a regular episode", node: season1(row(map[string]string{"anidbseason": "0", "tvdbseason": "0"}, ";1-3;")),
			anime: anidb.Anime{Regular: []string{"2005-11-23"}, Specials: []anidb.Episode{{Number: 1, AirDate: "2005-11-23"}}, Finished: true},
			want:  []Kind{DateMismatch, DateMismatch},
		},
		{
			name: "a default episode far off its TVDB date", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-10-05"}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "a default episode in season 0 a few weeks off", node: node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "0"}),
			anime: anidb.Anime{Regular: []string{"2005-12-21"}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "a default episode in another season's date", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-11-23"}, Finished: true}, want: []Kind{DateMismatch},
		},
		{
			name: "a missing episode with no AniDB date on a finished anime", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-9;")),
			anime: anidb.Anime{Regular: []string{""}, Finished: true}, want: []Kind{MissingTarget},
		},
		{
			name: "a second target off the date the film's parts share", want: []Kind{DateMismatch},
			node:  node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "0"}, row(map[string]string{"anidbseason": "1", "tvdbseason": "0"}, ";1-1+3;2-1;")),
			anime: anidb.Anime{Regular: []string{"2005-11-23", "2005-11-23"}, Finished: true},
		},
		{
			name: "a revisit on another calendar", layout: daily, node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-3;2-4;")),
			anime: anidb.Anime{Regular: winter, Finished: true}, want: []Kind{OutOfOrder, OutOfOrder, OutOfOrder},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := Check(tc.node, tc.anime, cmp.Or(tc.layout, weekly), now)
			if got := kinds(l); !slices.Equal(got, tc.want) {
				t.Errorf("Check(%s) kinds = %v, want %v; lead %+v", tc.name, got, tc.want, l)
			}
		})
	}
}

func TestCheckPasses(t *testing.T) {
	soon := now.Add(-3 * 24 * time.Hour).Format(time.DateOnly)
	for _, tc := range []struct {
		node   *animelists.Node
		layout *Layout
		anime  anidb.Anime
		name   string
	}{
		{name: "dates a day apart", node: season1(), anime: anidb.Anime{Regular: []string{"2005-08-11", "2005-08-16"}, Finished: true}},
		{name: "no TVDB date", node: season1(), anime: anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17", "2005-08-24", "2005-08-23"}, Finished: true}},
		{name: "no AniDB date", node: season1(), anime: anidb.Anime{Regular: []string{"", ""}, Finished: true}},
		{
			name: "no AniDB date on an anime still airing", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-9;")),
			anime: anidb.Anime{Regular: []string{""}},
		},
		{
			name: "an episode not settled yet", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-9;")),
			anime: anidb.Anime{Regular: []string{soon}, Finished: true},
		},
		{name: "a month-precision AniDB date", node: season1(), anime: anidb.Anime{Regular: []string{"2005-08-01"}, Finished: true}},
		{name: "a series on another calendar", node: season1(), anime: anidb.Anime{Regular: []string{"2003-01-20", "2003-01-27"}, Finished: true}},
		{
			name: "a mismatch on a date two episodes share", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";2-3;")),
			anime: anidb.Anime{Regular: []string{"2005-08-17", "2005-08-17"}, Finished: true},
		},
		{name: "a default episode a week off its TVDB date", node: season1(), anime: anidb.Anime{Regular: []string{"2005-08-17", "2005-08-24"}, Finished: true}},
		{
			name: "a month-precision date names no other episode", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "0"}, ";1-1;")),
			anime: anidb.Anime{Regular: []string{"2005-08-01"}, Finished: true},
		},
		{
			name: "a default special airing the day of a regular episode", node: season1(),
			anime: anidb.Anime{Specials: []anidb.Episode{{Number: 2, AirDate: "2005-08-17"}}, Finished: true},
		},
		{
			name: "a default special TVDB does not have", node: season1(),
			anime: anidb.Anime{Specials: []anidb.Episode{{Number: 5, AirDate: "2006-03-03"}}, Finished: true},
		},
		{
			name: "a regular date no episode meets on a series that keeps AniDB's calendar", node: season1(),
			anime: anidb.Anime{Regular: []string{"2005-08-10", "2005-08-20"}, Finished: true},
		},
		{
			name: "a date on the 1st meeting an episode says nothing of the calendar", layout: daily, node: season1(),
			anime: anidb.Anime{Regular: []string{"1983-08-01"}, Specials: []anidb.Episode{{Number: 1, AirDate: "1991-05-05"}}, Finished: true},
		},
		{
			name: "a date two episodes share meeting an episode says nothing of the calendar", layout: daily, node: season1(),
			anime: anidb.Anime{Regular: []string{"1983-08-02", "1983-08-02"}, Specials: []anidb.Episode{{Number: 1, AirDate: "1991-05-05"}}, Finished: true},
		},
		{
			name: "an episode mapped to no episode", node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-0;")),
			anime: anidb.Anime{Regular: []string{"2005-08-31"}, Finished: true},
		},
		{
			name: "an absolute number on the right date", node: node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "a", "episodeoffset": "1"}),
			anime: anidb.Anime{Regular: []string{"2005-08-17"}, Finished: true},
		},
		{name: "no TVDB series", node: node(map[string]string{"tvdbid": "movie"}), anime: anidb.Anime{Regular: []string{"2005-08-31"}, Finished: true}},
		{
			name: "a special's second target in a regular season", node: season1(row(map[string]string{"anidbseason": "0", "tvdbseason": "1"}, ";1-2+3;2-2;")),
			anime: anidb.Anime{Specials: []anidb.Episode{{Number: 1, AirDate: "2005-08-17"}, {Number: 2, AirDate: "2005-08-17"}}, Finished: true},
		},
		{
			name: "a run TVDB merged into one episode on another calendar", layout: daily, node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-1;2-1;3-1;")),
			anime: anidb.Anime{Regular: winter[:3], Finished: true},
		},
		{
			name: "two episodes on one TVDB episode around one that lands before it", layout: daily,
			node:  season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-2;2-1;3-2;")),
			anime: anidb.Anime{Regular: winter[:3], Finished: true},
		},
		{
			name: "one episode on two targets and the next on the first, on another calendar", layout: daily,
			node:  season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-1+3;2-1;")),
			anime: anidb.Anime{Regular: winter[:2], Finished: true},
		},
		{
			name: "one episode on the first target and the next on two, on another calendar", layout: daily,
			node:  season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-1;2-3+1;")),
			anime: anidb.Anime{Regular: winter[:2], Finished: true},
		},
		{
			name: "episodes reordered on another calendar", layout: daily, node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-2;2-1;")),
			anime: anidb.Anime{Regular: winter[:2], Finished: true},
		},
		{
			name: "a revisit around a date on the 1st", layout: daily, node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-3;2-4;")),
			anime: anidb.Anime{Regular: []string{"1984-01-08", "1984-02-01", "1984-02-08"}, Finished: true},
		},
		{
			name: "a revisit in season 0", layout: daily,
			node:  node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "0"}, row(map[string]string{"anidbseason": "1", "tvdbseason": "0"}, ";1-3;2-4;")),
			anime: anidb.Anime{Regular: winter[:3], Finished: true},
		},
		{
			name: "a revisit from a date two episodes share", layout: daily, node: season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";1-3;2-4;")),
			anime: anidb.Anime{Regular: []string{"1984-01-08", "1984-01-15", "1984-01-08"}, Finished: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if l := Check(tc.node, tc.anime, cmp.Or(tc.layout, weekly), now); l != nil {
				t.Errorf("Check(%s) = %+v, want no lead", tc.name, l)
			}
		})
	}
}

func TestCheckFindingDetail(t *testing.T) {
	n := season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";2-3;"))
	l := Check(n, anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17"}, Finished: true}, weekly, now)
	if l == nil || len(l.Findings) != 1 {
		t.Fatalf("Check = %+v, want one finding", l)
	}
	f := l.Findings[0]
	want := Finding{
		Kind: DateMismatch, Episode: Episode{Number: 2, AirDate: "2005-08-17"},
		Target: Target{Season: 1, Number: 3, AirDate: "2005-08-24"}, Carriers: []skyhook.Episode{ep(1, 2, 2, "2005-08-17")},
	}
	if f.Kind != want.Kind || f.Episode != want.Episode || f.Target != want.Target || f.Default || !slices.Equal(f.Carriers, want.Carriers) {
		t.Errorf("finding = %+v, want %+v", f, want)
	}
	if l.AniDB != 1 || l.Series != 79881 || l.Dead {
		t.Errorf("lead = %+v, want AniDB 1 on series 79881", l)
	}
}

func TestCheckDeadSeries(t *testing.T) {
	l := Check(season1(), anidb.Anime{Regular: []string{"2005-08-10"}}, &Layout{Absent: true}, now)
	if l == nil || !l.Dead || len(l.Findings) != 0 || l.Series != 79881 {
		t.Errorf("Check on a dead series = %+v, want a dead lead with no findings", l)
	}
}

func TestKey(t *testing.T) {
	if got := Key(3195); got != "lead:date:3195" {
		t.Errorf("Key(3195) = %q, want lead:date:3195", got)
	}
}
