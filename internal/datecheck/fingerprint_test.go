package datecheck

import (
	"slices"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/skyhook"
)

func fingerprintInputs() (*animelists.Node, anidb.Anime, *Layout) {
	n := node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "1"}, row(map[string]string{"anidbseason": "1", "tvdbseason": "1"}, ";8-9;"))
	n.Name = "Kamichu!"
	a := anidb.Anime{Regular: []string{"2005-08-10", "2005-08-17"}, Specials: []anidb.Episode{{Number: 1, AirDate: "2005-11-23"}}, Finished: true, Titles: "anidb titles"}
	lay := &Layout{
		Episodes: []skyhook.Episode{ep(0, 1, 0, "2005-11-23"), ep(1, 1, 1, "2005-08-10"), ep(1, 2, 2, "2005-08-17"), ep(2, 1, 3, "2006-01-01")},
		Titles:   map[int]string{0: "season 0 titles", 1: "season 1 titles", 2: "season 2 titles"},
	}
	return n, a, lay
}

func TestFingerprintsFollowEachInput(t *testing.T) {
	n, a, lay := fingerprintInputs()
	base := FingerprintsOf(n, a, lay)
	for _, h := range []string{base.Node, base.TVDB, base.AniDB} {
		if !hex64(h) {
			t.Fatalf("fingerprints %+v, want three 64-hex digests", base)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*animelists.Node, *anidb.Anime, *Layout)
		want   Fingerprints
	}{
		{
			"node attribute", func(n *animelists.Node, _ *anidb.Anime, _ *Layout) { n.Attrs["episodeoffset"] = "1" },
			Fingerprints{Node: "changed", TVDB: base.TVDB, AniDB: base.AniDB},
		},
		{
			"node row", func(n *animelists.Node, _ *anidb.Anime, _ *Layout) { n.Rows[0].Text = ";8-8;" },
			Fingerprints{Node: "changed", TVDB: base.TVDB, AniDB: base.AniDB},
		},
		{"node title", func(n *animelists.Node, _ *anidb.Anime, _ *Layout) { n.Name = "Kamichu" }, base},
		{
			"TVDB date in a touched season", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[2].AirDate = "2005-08-18" },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB special when AniDB lists specials", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[0].Number = 2 },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB date in a season the node does not map onto", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[3].AirDate = "2006-01-02" },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB number in a season the node does not map onto", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Episodes[3].Number = 2 },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB titles in a touched season", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Titles[1] = "season 1 titles retitled" },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB titles of the specials", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Titles[0] = "season 0 titles retitled" },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"TVDB titles in a season the node does not map onto", func(_ *animelists.Node, _ *anidb.Anime, l *Layout) { l.Titles[2] = "season 2 titles retitled" },
			Fingerprints{Node: base.Node, TVDB: "changed", AniDB: base.AniDB},
		},
		{
			"AniDB episode titles", func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Titles = "anidb titles retitled" },
			Fingerprints{Node: base.Node, TVDB: base.TVDB, AniDB: "changed"},
		},
		{
			"AniDB regular date", func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Regular[1] = "2005-08-18" },
			Fingerprints{Node: base.Node, TVDB: base.TVDB, AniDB: "changed"},
		},
		{
			"AniDB special number", func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Specials[0].Number = 2 },
			Fingerprints{Node: base.Node, TVDB: base.TVDB, AniDB: "changed"},
		},
		{
			"AniDB end date", func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Finished = false },
			Fingerprints{Node: base.Node, TVDB: base.TVDB, AniDB: "changed"},
		},
		{
			"AniDB episode count", func(_ *animelists.Node, a *anidb.Anime, _ *Layout) { a.Regular = append(a.Regular, "") },
			Fingerprints{Node: base.Node, TVDB: base.TVDB, AniDB: "changed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, a, lay := fingerprintInputs()
			tc.mutate(n, &a, lay)
			got := FingerprintsOf(n, a, lay)
			for _, part := range []struct{ name, got, base, want string }{
				{"node", got.Node, base.Node, tc.want.Node},
				{"TVDB", got.TVDB, base.TVDB, tc.want.TVDB},
				{"AniDB", got.AniDB, base.AniDB, tc.want.AniDB},
			} {
				if changed := part.got != part.base; changed != (part.want == "changed") {
					t.Errorf("%s fingerprint changed=%t, want changed=%t", part.name, changed, part.want == "changed")
				}
			}
		})
	}
}

func TestFingerprintsReuseTheOverlayCaptureNodeHash(t *testing.T) {
	n, a, lay := fingerprintInputs()
	if got, want := FingerprintsOf(n, a, lay).Node, animelists.HashOf(n); got != want {
		t.Errorf("node fingerprint %s, want the overlay capture's node hash %s", got, want)
	}
}

// Check reads an absent series as dead before it reads any episode, so a
// series SkyHook stops serving moves the TVDB fingerprint even when its
// official order was empty.
func TestFingerprintsTellAnAbsentSeriesFromAnEmptyOrder(t *testing.T) {
	n, a, _ := fingerprintInputs()
	served, gone := FingerprintsOf(n, a, &Layout{}), FingerprintsOf(n, a, &Layout{Absent: true})
	if served.TVDB == gone.TVDB || served.Node != gone.Node || served.AniDB != gone.AniDB {
		t.Errorf("an empty order %+v and an absent series %+v, want only the TVDB fingerprint to differ", served, gone)
	}
}

// Check places an absolute node's specials on TVDB season 0, so a change
// there moves the fingerprint as it does for a season-numbered node.
func TestFingerprintsCoverSeasonZeroOfAnAbsoluteNode(t *testing.T) {
	n := node(map[string]string{"tvdbid": "79881", "defaulttvdbseason": "a"})
	a := anidb.Anime{Regular: []string{"2005-08-10"}, Specials: []anidb.Episode{{Number: 1, AirDate: "2005-11-23"}}, Finished: true}
	layout := func() *Layout {
		return &Layout{
			Episodes: []skyhook.Episode{ep(0, 1, 0, "2005-12-30"), ep(0, 2, 0, "2005-11-23"), ep(1, 1, 1, "2005-08-10")},
			Titles:   map[int]string{0: "season 0 titles", 1: "season 1 titles"},
		}
	}
	base := FingerprintsOf(n, a, layout()).TVDB
	for _, tc := range []struct {
		name   string
		mutate func(*Layout)
	}{
		{"a season-0 air date", func(l *Layout) { l.Episodes[0].AirDate = "2006-03-01" }},
		{"season-0 titles", func(l *Layout) { l.Titles[0] = "season 0 titles retitled" }},
		{"a season-1 air date", func(l *Layout) { l.Episodes[2].AirDate = "2005-08-11" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lay := layout()
			tc.mutate(lay)
			if FingerprintsOf(n, a, lay).TVDB == base {
				t.Errorf("TVDB fingerprint unchanged after %s changed on an absolute node", tc.name)
			}
		})
	}
}

// Check looks for a regular episode's date on every season, so a season the
// node does not map onto can flip its verdict and must move the TVDB
// fingerprint.
func TestVerdictChangeOffTheMappedSeasonsMovesTheTVDBFingerprint(t *testing.T) {
	n := season1()
	a := anidb.Anime{Regular: []string{"2005-08-10"}, Finished: true}
	layout := func(season2 string) *Layout {
		return &Layout{
			Episodes: []skyhook.Episode{ep(1, 1, 1, "2005-10-05"), ep(2, 1, 2, season2)},
			Titles:   map[int]string{1: "season 1 titles", 2: "season 2 titles"},
		}
	}
	carried, moved := layout("2005-08-10"), layout("2005-12-07")
	if l, p := Check(n, a, carried, now), Check(n, a, moved, now); l == nil || p != nil {
		t.Fatalf("Check = %+v, then %+v; want a lead while season 2 airs on the AniDB date and a pass once it moves", l, p)
	}
	if FingerprintsOf(n, a, carried).TVDB == FingerprintsOf(n, a, moved).TVDB {
		t.Error("TVDB fingerprint unchanged though only season 2 moved and Check went from a lead to a pass")
	}
}

// With no regular episode Check reads only the seasons the node maps onto
// and season 0.
func TestFingerprintsOfASpecialsOnlyAnimeLeaveOutSeasonsCheckCannotRead(t *testing.T) {
	n := season1()
	a := anidb.Anime{Specials: []anidb.Episode{{Number: 1, AirDate: "2005-11-23"}}, Finished: true}
	layout := func() *Layout {
		return &Layout{
			Episodes: []skyhook.Episode{ep(0, 1, 0, "2005-11-23"), ep(1, 1, 1, "2005-08-10"), ep(2, 1, 2, "2006-01-04")},
			Titles:   map[int]string{0: "season 0 titles", 1: "season 1 titles", 2: "season 2 titles"},
		}
	}
	base := FingerprintsOf(n, a, layout()).TVDB
	for _, tc := range []struct {
		name   string
		mutate func(*Layout)
		moves  bool
	}{
		{"a season-0 air date", func(l *Layout) { l.Episodes[0].AirDate = "2005-11-24" }, true},
		{"a season-1 air date", func(l *Layout) { l.Episodes[1].AirDate = "2005-08-11" }, true},
		{"a season-2 air date", func(l *Layout) { l.Episodes[2].AirDate = "2006-01-05" }, false},
		{"season-2 titles", func(l *Layout) { l.Titles[2] = "season 2 titles retitled" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lay := layout()
			tc.mutate(lay)
			if moved := FingerprintsOf(n, a, lay).TVDB != base; moved != tc.moves {
				t.Errorf("TVDB fingerprint moved=%t after %s changed, want moved=%t", moved, tc.name, tc.moves)
			}
		})
	}
}

func TestFindingKeys(t *testing.T) {
	fs := []Finding{
		{Kind: OutOfOrder, Episode: Episode{Number: 12}},
		{Kind: DateMismatch, Episode: Episode{Number: 3}},
		{Kind: DateMismatch, Episode: Episode{Number: 3}},
		{Kind: MissingTarget, Episode: Episode{Number: 1, Special: true}},
	}
	got := FindingKeys(fs)
	if want := []string{"date-mismatch:3", "missing-target:S1", "out-of-order:12"}; !slices.Equal(got, want) {
		t.Errorf("FindingKeys = %v, want %v: kind and AniDB episode, sorted, each once", got, want)
	}
	for _, k := range got {
		if !ValidFindingKey(k) {
			t.Errorf("ValidFindingKey(%q) = false for a key FindingKeys wrote", k)
		}
	}
	for _, k := range []string{"", "date-mismatch", "date-mismatch:0", "dead-series:1", "date-mismatch:S", "date-mismatch:3 ", "Date-mismatch:3", "date-mismatch:03"} {
		if ValidFindingKey(k) {
			t.Errorf("ValidFindingKey(%q) = true, want false", k)
		}
	}
}

func hex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
