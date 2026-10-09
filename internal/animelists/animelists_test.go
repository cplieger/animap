package animelists

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/xmlx"
)

func mini(t *testing.T) *List {
	t.Helper()
	body, err := os.ReadFile("../../testdata/anime-list-mini.xml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse(mini): %v", err)
	}
	return l
}

func TestParseKeepsWireStrings(t *testing.T) {
	l := mini(t)
	if want := []int{10, 12, 13, 18, 19, 20, 30, 31}; !reflect.DeepEqual(l.Order, want) {
		t.Errorf("Order = %v, want %v", l.Order, want)
	}
	n := l.Nodes[10]
	if n.Attr("defaulttvdbseason") != "1" || n.Name != "Series with season 1" || n.Before != ";1-12;" || len(n.Rows) != 3 {
		t.Errorf("node 10 = %+v", n)
	}
	if _, ok := n.Attrs["anidbid"]; ok {
		t.Error("anidbid stayed in Attrs")
	}
	if got := l.Nodes[12].Attr("episodeoffset"); got != "0" {
		t.Errorf("explicit episodeoffset 0 read as %q", got)
	}
}

func TestParseRefuses(t *testing.T) {
	deep := "<anime-list>" + strings.Repeat("<a>", 20) + strings.Repeat("</a>", 20) + "</anime-list>"
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"wrong root", `<other><anime anidbid="1"/></other>`, errRoot},
		{"no nodes", `<anime-list></anime-list>`, errNoNodes},
		{"too deep", deep, xmlx.ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.body)); !errors.Is(err, tc.want) {
				t.Errorf("Parse(%s) = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestRowSchema(t *testing.T) {
	l := mini(t)
	got, err := l.Nodes[10].Rows[1].Schema()
	if err != nil {
		t.Fatal(err)
	}
	want := schema.Row{AniDBSeason: 1, TVDBSeason: new(1), Start: 13, End: 24, Offset: -12}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ranged row = %+v, want %+v", got, want)
	}
	tmdb, err := l.Nodes[10].Rows[2].Schema()
	if err != nil || tmdb.TVDBSeason != nil || tmdb.TMDBSeason == nil || *tmdb.TMDBSeason != 2 {
		t.Errorf("TMDB-only row = %+v, %v", tmdb, err)
	}
	if _, err := l.Nodes[12].Rows[0].Schema(); !errors.Is(err, errRow) {
		t.Errorf("row with an unparseable pair = %v, want errRow", err)
	}
}

func TestParsePairs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want [][]int
		bad  bool
	}{
		{";1-2;3-0;", [][]int{{1, 2}, {3}}, false},
		{" ;4-5+6; ;", [][]int{{4, 5, 6}}, false},
		{"0-1", [][]int{{0, 1}}, false},
		{"", nil, false},
		{";1-x;", nil, true},
		{";1;", nil, true},
		{";1-0+2;", nil, true},
		{";-1-2;", nil, true},
	} {
		got, err := parsePairs(tc.in)
		if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parsePairs(%q) = %v, %v; want %v, error %t", tc.in, got, err, tc.want, tc.bad)
		}
	}
}

func TestRowRoundTrip(t *testing.T) {
	for _, r := range []schema.Row{
		{AniDBSeason: 0, TVDBSeason: new(0), Episodes: [][]int{{1, 2}, {3}, {4, 5, 6}}},
		{AniDBSeason: 1, TVDBSeason: new(2), Start: 14, End: 26, Offset: -13},
		{AniDBSeason: 1, TMDBSeason: new(0), Start: 1},
	} {
		got, err := RowFromSchema(r).Schema()
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Errorf("RowFromSchema(%+v).Schema() = %+v, %v", r, got, err)
		}
	}
}

func TestCanonicalHash(t *testing.T) {
	base := `<anime-list><anime anidbid="1" tvdbid="5" defaulttvdbseason="1" tmdbid=""><name>A</name>` +
		`<mapping-list><mapping anidbseason="0" tvdbseason="0">;1-2;</mapping></mapping-list></anime></anime-list>`
	hash := func(body string) string {
		t.Helper()
		l, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse(%s): %v", body, err)
		}
		return l.Nodes[1].Hash()
	}
	h := hash(base)
	same := []string{
		strings.Replace(base, `tvdbid="5" defaulttvdbseason="1"`, `defaulttvdbseason="1" tvdbid="5"`, 1),
		strings.Replace(base, `<name>A</name>`, `<name>Renamed</name>`, 1),
		strings.Replace(base, `;1-2;`, ` ;1-2;; `, 1),
		strings.Replace(base, `</anime></anime-list>`, `<supplemental-info><studio>X</studio></supplemental-info></anime></anime-list>`, 1),
	}
	for _, b := range same {
		if hash(b) != h {
			t.Errorf("hash changed for an equivalent node: %s", b)
		}
	}
	differ := []string{
		strings.Replace(base, `tvdbid="5"`, `tvdbid="6"`, 1),
		strings.Replace(base, `tmdbid=""`, `tmdbid="1"`, 1),
		strings.Replace(base, `tvdbseason="0">`, `tvdbseason="1">`, 1),
		strings.Replace(base, `;1-2;`, `;1-3;`, 1),
		strings.Replace(base, `</mapping-list>`, `</mapping-list><before>;1-2;</before>`, 1),
	}
	for _, b := range differ {
		if hash(b) == h {
			t.Errorf("hash did not change for a mapping change: %s", b)
		}
	}
	if HashOf(nil) != HashAbsent {
		t.Error("HashOf(nil) is not HashAbsent")
	}
}
