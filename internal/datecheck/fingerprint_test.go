package datecheck

import (
	"regexp"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
)

var hexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// digestInputs is a node whose ep2 lands one episode off on a weekly show,
// so its one finding is date-mismatch:2.
func digestInputs() (*animelists.Node, anidb.Anime, *Layout) {
	n := season1(row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";2-3;"))
	a := anidb.Anime{Regular: []anidb.Episode{{Number: 1, AirDate: "2005-08-10", Titles: "t1"}, {Number: 2, AirDate: "2005-08-17", Titles: "t2"}}, Finished: true}
	return n, a, laid(ep(1, 1, 1, "2005-08-10"), ep(1, 2, 2, "2005-08-17"), ep(1, 3, 3, "2005-08-24"), ep(2, 1, 4, "2006-01-04"))
}

func inputsOfLead(t *testing.T, n *animelists.Node, a *anidb.Anime, lay *Layout) string {
	t.Helper()
	l := Check(n, animelists.TVDB, a, lay, now, nil)
	if l == nil {
		t.Fatal("Check = nil, want the date-mismatch lead")
	}
	return l.Digests()["date-mismatch:2"]
}

func TestFindingInputsFollowWhatTheProofReads(t *testing.T) {
	n, a, lay := digestInputs()
	base := inputsOfLead(t, n, &a, lay)
	if !hexRE.MatchString(base) {
		t.Fatalf("digest %q, want 64 hex", base)
	}
	for _, tc := range []struct {
		mutate func(*animelists.Node, *anidb.Anime, *Layout)
		name   string
		moves  bool
	}{
		{name: "the AniDB episode's titles", moves: true, mutate: func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Regular[1].Titles = "other" }},
		{name: "the target's title", moves: true, mutate: func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[2].Title = "other" }},
		{name: "the carrier's title", moves: true, mutate: func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[1].Title = "other" }},
		{name: "the target's absolute number", moves: true, mutate: func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[2].Absolute = 9 }},
		{name: "an episode the proof does not read", mutate: func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[3].Title = "other" }},
		{name: "another AniDB episode's titles", mutate: func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Regular[0].Titles = "other" }},
		{name: "the node's name", mutate: func(n *animelists.Node, _ *anidb.Anime, _ *Layout) { n.Name = "renamed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, a, lay := digestInputs()
			tc.mutate(n, &a, lay)
			if got := inputsOfLead(t, n, &a, lay); (got != base) != tc.moves {
				t.Errorf("digest moved = %t, want %t", got != base, tc.moves)
			}
		})
	}
	t.Run("the node placing the episode elsewhere", func(t *testing.T) {
		n, a, lay := digestInputs()
		n.Rows[0].Text = ";2-1;"
		l := Check(n, animelists.TVDB, &a, lay, now, nil)
		if l == nil || l.Digests()["date-mismatch:2"] == base {
			t.Errorf("Check after the move = %+v, want a finding with other inputs", l)
		}
	})
}

// The same coordinates on another show are another proof, on either side,
// so a ruling on one show never silences a node moved to another.
func TestFindingInputsNameTheShow(t *testing.T) {
	for _, tc := range []struct {
		onSide func(*animelists.Node) *animelists.Node
		name   string
		attr   string
		side   animelists.Side
	}{
		{name: "TVDB", attr: "tvdbid", side: animelists.TVDB, onSide: func(n *animelists.Node) *animelists.Node { return n }},
		{name: "TMDB", attr: "tmdbtv", side: animelists.TMDB, onSide: onTMDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digests := func(id string, lay *Layout) map[string]string {
				n, a, _ := digestInputs()
				n = tc.onSide(n)
				n.Attrs[tc.attr] = id
				l := Check(n, tc.side, &a, tmdbLayout(lay), now, nil)
				if l == nil {
					t.Fatalf("Check on show %s = nil, want a lead", id)
				}
				return l.Digests()
			}
			_, _, lay := digestInputs()
			if a, b := digests("100", lay), digests("200", lay); a["date-mismatch:2"] == "" || a["date-mismatch:2"] == b["date-mismatch:2"] {
				t.Errorf("date-mismatch:2 on show 100 %q and on show 200 %q, want two digests", a["date-mismatch:2"], b["date-mismatch:2"])
			}
			gone := &Layout{Absent: true}
			if a, b := digests("100", gone), digests("200", gone); a["dead-series"] == b["dead-series"] {
				t.Errorf("dead-series digest %q on both shows, want two", a["dead-series"])
			}
		})
	}
}

func TestDigestsKeys(t *testing.T) {
	dead := &Lead{Dead: true, Findings: []Finding{{Kind: DeadMovie, Movie: 4, Inputs: "m"}}}
	got := dead.Digests()
	if len(got) != 2 || !hexRE.MatchString(got["dead-series"]) || got["dead-movie:4"] != "m" {
		t.Errorf("Digests(dead) = %v, want dead-series and dead-movie:4", got)
	}
	two := &Lead{Findings: []Finding{
		{Kind: DateMismatch, Episode: Episode{Number: 1}, Inputs: "a"},
		{Kind: DateMismatch, Episode: Episode{Number: 1}, Inputs: "a"},
		{Kind: MissingTarget, Episode: Episode{Number: 3, Special: true}, Inputs: "b"},
	}}
	if got := two.Digests(); len(got) != 2 || got["date-mismatch:1"] != "a" || got["missing-target:S3"] != "b" {
		t.Errorf("Digests = %v, want one key per kind and episode", got)
	}
}

func TestValidFindingKey(t *testing.T) {
	for k, want := range map[string]bool{
		"missing-target:1": true, "date-mismatch:S12": true, "out-of-order:40": true, "dead-series": true, "dead-movie:12": true,
		"missing-target:0": false, "dead-movie": false, "dead-series:1": false, "lead:1": false, "date-mismatch:S": false,
	} {
		if got := ValidFindingKey(k); got != want {
			t.Errorf("ValidFindingKey(%q) = %t, want %t", k, got, want)
		}
	}
}

func TestTitleDigest(t *testing.T) {
	if got := TitleDigest(" Ep 1 "); got != TitleDigest("Ep 1") || len(got) != 16 {
		t.Errorf("TitleDigest = %q, want 16 hex, the same trimmed", got)
	}
	if TitleDigest("Ep 1") == TitleDigest("Ep 2") {
		t.Error("two titles share a digest")
	}
}
