package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/issues"
	"github.com/cplieger/animap/internal/leftovers"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
)

// leftoversCase gives AniDB 10 regular episodes on their TVDB dates,
// specials 1 and 4, an other episode, a promotion and a credit, and TVDB
// 5000 season 0 episodes 2 (the node's row gives it special 1), 7
// (unclaimed) and each of slots. Special 4 defaults to 0x4, which TVDB
// lacks; other 1 takes no default, so only a row or a link lands it on its
// number 401.
func leftoversCase(t *testing.T, slots ...int) *reviewCase {
	t.Helper()
	c := newReviewCase(t)
	a := anidb.Anime{
		Regular: []anidb.Episode{{Number: 1, AirDate: "2020-01-06"}, {Number: 2, AirDate: "2020-03-02"}}, Finished: true, Titles: fixtureOtherTitles,
		Specials: []anidb.Episode{{Number: 1}, {Number: 4}},
		Extras: []anidb.Extra{
			{Kind: anidb.Credit, Number: 1, SeasonZero: 101, ID: 3101},
			{Kind: anidb.Promotion, Number: 1, SeasonZero: 201, ID: 3201},
			{Kind: anidb.Other, Number: 1, SeasonZero: 401, ID: 3401},
		},
	}
	c.mirror = snapshotWith(t, c.dir, map[int]anidb.Anime{10: a})
	st, err := datecheck.LoadStore(c.tvdb)
	if err != nil {
		t.Fatal(err)
	}
	lay := st.Series[5000]
	for _, k := range append([]int{2, 7}, slots...) {
		lay.Episodes = append(lay.Episodes, aired(tvdb(0, k, 0, ""))...)
	}
	st.Series[5000] = lay
	c.tvdb = writeStore(t, c.dir, "layouts-tvdb.json", st)
	return c
}

func TestReviewRaisesSeasonZeroLeftovers(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	doc := leftoversCase(t).run(t)
	if h := doc.Sources[review.Leftovers]; h == nil || h.Evaluated || h.UnevaluatedReason != "TMDB is not checked: TMDB_TOKEN is not set" {
		t.Errorf("leftovers source = %+v, want unevaluated while TMDB is", h)
	}
	s := subjectIn(doc, review.AniDBKey(10))
	if s == nil || s.Sections.Leftovers == nil {
		t.Fatalf("AniDB 10 = %+v, want a leftovers section", s)
	}
	l := s.Sections.Leftovers
	if !l.Evaluated || !l.Applicable || len(l.Findings) != 1 || !strings.Contains(l.Node, "tvdbid=5000") {
		t.Fatalf("leftovers = %+v, want one evaluated TVDB finding with the node", l)
	}
	f := l.Findings[0]
	want := []leftovers.Episode{{Kind: anidb.Special, Number: 4, SeasonZero: 4}, {Kind: anidb.Other, Number: 1, SeasonZero: 401}}
	if f.Side != animelists.TVDB || f.Series != 5000 || !slices.Equal(f.Unplaced, want) || !slices.Equal(f.Unclaimed, []int{7}) ||
		!slices.Equal(f.Promotions, []leftovers.Episode{{Kind: anidb.Promotion, Number: 1, SeasonZero: 201}}) {
		t.Errorf("finding = %+v, want special 4 and other 1 with promotion 1 beside 0x7", f)
	}
	if len(l.Digests) != 1 || l.Digests["tvdb:5000"] != f.Inputs || l.Unchecked["tmdb:9000"] != "TMDB_TOKEN is not set" {
		t.Errorf("digests %v, unchecked %v; want the TVDB digest and TMDB 9000 not checked", l.Digests, l.Unchecked)
	}
	if s := subjectIn(doc, review.AniDBKey(19)); s != nil && s.Sections.Leftovers != nil {
		t.Errorf("AniDB 19 leftovers = %+v, want none: AniDB lists no special of it", s.Sections.Leftovers)
	}
	a := actionFor(planDoc(t, doc, nil), review.AniDBKey(10))
	if a == nil || a.Title != "anime: AniDB 10 Series with season 1, season 0 leftovers" {
		t.Fatalf("action = %+v, want an issue titled for the leftovers", a)
	}
	for _, want := range []string{
		"### Season 0 leftovers", "On TVDB series 5000:", "- AniDB episodes that land nowhere: special 4 (S0 4), other 1 (S0 401)\n",
		"- AniDB promotions that land nowhere: promotion 1 (S0 201)\n", "- Season 0 episodes no AniDB episode claims: S0E7\n",
		"- TVDB: https://thetvdb.com/series/series-5000/seasons/official/0", "TMDB show 9000: not checked this run (TMDB_TOKEN is not set).",
		"-key anime:anidb:10 -part leftovers -verdict fine", "-evidence https://thetvdb.com/series/series-5000/seasons/official/0\n", "The node as checked:",
	} {
		if !strings.Contains(a.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, a.Body)
		}
	}
	if strings.Contains(a.Body, "credit") {
		t.Errorf("body names a credit:\n%s", a.Body)
	}
}

// Another node's claim on a season 0 episode, read through the collision
// guard, leaves it claimed.
func TestReviewCountsEveryNodesClaims(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := leftoversCase(t)
	snap, err := anidb.LoadSnapshot(c.mirror)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[int]anidb.Anime{19: fin(1)}
	maps.Copy(listed, snap.Anime)
	c.mirror = snapshotWith(t, c.dir, listed)
	b, err := os.ReadFile(listFixture)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(b), `anidbid="19" tvdbid="5004" defaulttvdbseason="a" episodeoffset=""`, `anidbid="19" tvdbid="5000" defaulttvdbseason="0" episodeoffset="6"`, 1)
	if moved == string(b) {
		t.Fatal("the fixture list no longer has node 19 on 5004")
	}
	list := filepath.Join(c.dir, "list.xml")
	if err := os.WriteFile(list, []byte(moved), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := subjectIn(c.run(t, "-list", list), review.AniDBKey(10)); s != nil && s.Sections.Leftovers != nil && s.Sections.Leftovers.raised() {
		t.Errorf("AniDB 10 leftovers = %+v, want none raised: AniDB 19 episode 1 claims 0x7", s.Sections.Leftovers)
	}
}

// The issue offers a link for other 1 beside the unclaimed 0x401, its
// Anime-Lists number, and an applied link to it, which no row names, lands
// it there: the episode and the slot leave the finding.
func TestReviewLinkLandsAnOtherEpisodeOnItsAnimeListsNumber(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := leftoversCase(t, 401)
	key := review.AniDBKey(10)
	other := leftovers.Episode{Kind: anidb.Other, Number: 1, SeasonZero: 401}
	doc := c.run(t)
	s := subjectIn(doc, key)
	if s == nil {
		t.Fatalf("no subject %s", key)
	}
	l := s.Sections.Leftovers
	if l == nil || len(l.Findings) != 1 || !slices.Contains(l.Findings[0].Unplaced, other) || !slices.Equal(l.Findings[0].Unclaimed, []int{7, 401}) {
		t.Fatalf("leftovers without the link = %+v, want other 1 beside 0x7 and 0x401", l)
	}
	if a := actionFor(planDoc(t, doc, nil), key); a == nil || !strings.Contains(a.Body, linkRemedy) {
		t.Errorf("action = %+v, want the link remedy", a)
	}
	writeLinks(t, c.ov, linkFile(100, overlay.Part{AniDBID: 10, Specials: []int{401}}))
	doc = c.run(t)
	if s = subjectIn(doc, key); s == nil {
		t.Fatalf("no subject %s with the link", key)
	}
	l = s.Sections.Leftovers
	if l == nil || len(l.Findings) != 1 || !slices.Equal(l.Findings[0].Unplaced, []leftovers.Episode{{Kind: anidb.Special, Number: 4, SeasonZero: 4}}) || !slices.Equal(l.Findings[0].Unclaimed, []int{7}) {
		t.Fatalf("leftovers with the link = %+v, want special 4 beside 0x7 alone", l)
	}
	if a := actionFor(planDoc(t, doc, nil), key); a == nil || strings.Contains(a.Body, "overlay link") {
		t.Errorf("action = %+v, want the leftovers raised with no link remedy", a)
	}
}

// A show the run could not refresh leaves the section not checked while
// the source is evaluated, so its issue says so.
func TestReviewLeavesTheLeftoversOfAnUnreadShowUnevaluated(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := leftoversCase(t)
	st, err := datecheck.LoadStore(c.tvdb)
	if err != nil {
		t.Fatal(err)
	}
	old := st.Series[5000]
	old.Fetched = time.Now().Add(-156 * time.Hour)
	st.Series[5000] = old
	c.tvdb = writeStore(t, c.dir, "layouts-tvdb.json", st)
	doc := c.run(t)
	if h := doc.Sources[review.Leftovers]; !h.Evaluated {
		t.Errorf("leftovers source = %+v, want evaluated", h)
	}
	l := subjectIn(doc, review.AniDBKey(10)).Sections.Leftovers
	if l == nil || l.Evaluated || !l.Applicable || l.Reason != "TVDB series 5000: SkyHook fetch budget spent" {
		t.Errorf("leftovers = %+v, want not checked for the spent budget", l)
	}
}

// A show the store holds no layout for is not judged, though a run fetches
// or marks stale every show it judges first.
func TestLayoutUnreadWithoutALayout(t *testing.T) {
	r := &reviewRun{
		health: map[string]*sourceHealth{review.DateTMDB: {Evaluated: true}},
		tmdb:   datecheck.NewStore(), tmdbStale: map[int]string{},
	}
	if got := r.layoutUnread(review.DateTMDB, 9000); got != "no layout fetched yet" {
		t.Errorf("layoutUnread = %q, want %q", got, "no layout fetched yet")
	}
	r.tmdb.Series[9000] = datecheck.Layout{}
	if got := r.layoutUnread(review.DateTMDB, 9000); got != "" {
		t.Errorf("layoutUnread with a layout = %q, want none", got)
	}
}

// Without the mirror the source is not checked, and a subject listed for
// another reason says so wherever its node maps to a show, an acceptance
// part on the section or a date section included.
func TestReviewWithoutAMirrorLeavesTheLeftoversUnevaluated(t *testing.T) {
	c := newReviewCase(t)
	for _, aid := range []int{10, 12, 31, 999} {
		rec := fineRecord()
		rec.Key = review.AniDBKey(aid)
		if aid == 12 || aid == 999 {
			rec.Parts.Leftovers = leftoversPart(aid)
		}
		b, err := rec.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c.reviewed, review.File(rec.Key)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc := c.run(t, "-mirror", "")
	if h := doc.Sources[review.Leftovers]; h == nil || h.Evaluated || h.UnevaluatedReason != "the AniDB mirror was not read" {
		t.Errorf("leftovers source = %+v, want unevaluated without the mirror", h)
	}
	s10, s31 := subjectIn(doc, review.AniDBKey(10)), subjectIn(doc, review.AniDBKey(31))
	if l := s10.Sections.Leftovers; l == nil || l.Evaluated || !l.Applicable || l.Reason != "the AniDB mirror was not read" {
		t.Errorf("AniDB 10 leftovers = %+v, want applicable and not checked", l)
	}
	if d := s10.Sections.DateTVDB; d == nil || d.Evaluated || !d.Applicable || d.Acceptance == nil {
		t.Errorf("AniDB 10 TVDB dates = %+v, want its part matched, applicable and not checked", d)
	}
	if l := subjectIn(doc, review.AniDBKey(12)).Sections.Leftovers; l == nil || l.Evaluated || !l.Applicable || l.Reason != "the AniDB mirror was not read" || l.Acceptance == nil {
		t.Errorf("AniDB 12 leftovers = %+v, want its part matched, applicable and not checked", l)
	}
	if l := s31.Sections.Leftovers; l != nil {
		t.Errorf("AniDB 31 leftovers = %+v, want none: its node maps to no show", l)
	}
	if d := s31.Sections.DateTVDB; d == nil || d.Applicable {
		t.Errorf("AniDB 31 TVDB dates = %+v, want its part matched and not applicable: its node maps to no show", d)
	}
	gone := subjectIn(doc, review.AniDBKey(999))
	if l, d := gone.Sections.Leftovers, gone.Sections.DateTVDB; l == nil || l.Applicable || d == nil || d.Applicable {
		t.Errorf("AniDB 999 leftovers %+v, TVDB dates %+v; want both parts matched and not applicable: the list lacks the node", l, d)
	}
}

// A part on an anime the mirror does not list is matched by an evaluated
// section that does not apply, so it is stale.
func TestPartsOfAnAnimeTheMirrorLacksAreStale(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := leftoversCase(t)
	rec := fineRecord()
	rec.Key, rec.Parts.Leftovers = review.AniDBKey(12), leftoversPart(12)
	b, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.reviewed, review.File(rec.Key)), b, 0o600); err != nil {
		t.Fatal(err)
	}
	doc := c.run(t)
	s := subjectIn(doc, review.AniDBKey(12))
	if l := s.Sections.Leftovers; l == nil || !l.Evaluated || l.Applicable || l.Acceptance == nil {
		t.Errorf("AniDB 12 leftovers = %+v, want evaluated, not applicable and its part matched", l)
	}
	if d := s.Sections.DateTVDB; d == nil || !d.Evaluated || d.Applicable || d.Acceptance == nil {
		t.Errorf("AniDB 12 TVDB dates = %+v, want evaluated, not applicable and its part matched", d)
	}
	sum := actionFor(planDoc(t, doc, nil), summaryKey)
	for _, part := range []string{review.Leftovers, review.DateTVDB} {
		if want := "- [ ] `reviewed/anidb-12.json` `" + part + "`: its section is clear without it, delete the part"; sum == nil || !strings.Contains(sum.Body, want) {
			t.Errorf("summary = %+v, want %q", sum, want)
		}
	}
}

// While the source is not checked, a subject listed for another reason is
// judged on the shows the run read.
func TestReviewJudgesTheLeftoversOfAListedSubject(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	doc := newReviewCase(t).run(t)
	s12 := subjectIn(doc, review.AniDBKey(12))
	if s12 == nil || s12.Sections.Leftovers == nil || !s12.Sections.Leftovers.Evaluated || s12.Sections.Leftovers.raised() {
		t.Errorf("AniDB 12 = %+v, want its leftovers evaluated and clear beside its dead series", s12)
	}
}

// A leftovers part covers the pair it was written against: the next run
// silences it, a new unclaimed episode raises it again naming why, and a
// pair that cleared on both sides leaves the part stale.
func TestAcceptLeftoversCoversThePairItJudged(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := leftoversCase(t)
	from := writeFixture(t, c.dir, "first.json", c.run(t))
	if err := accept(t, from, c.reviewed, "-key", review.AniDBKey(10), "-part", review.Leftovers, "-verdict", review.Fine); err != nil {
		t.Fatal(err)
	}
	recs, err := review.LoadDir(c.reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if p := recs[review.AniDBKey(10)].Parts.Leftovers; p == nil || len(p.Findings["tvdb:5000"]) != 64 || p.Verdict != review.Fine {
		t.Fatalf("record = %+v, want the pair's digest under a fine verdict", recs[review.AniDBKey(10)])
	}
	doc := c.run(t)
	if m := subjectIn(doc, review.AniDBKey(10)).Sections.Leftovers.Acceptance; m == nil || !m.Matches {
		t.Errorf("acceptance = %+v, want it to cover the pair", m)
	}
	actions := planDoc(t, doc, nil)
	if a := actionFor(actions, review.AniDBKey(10)); a != nil && strings.Contains(a.Body, "Season 0 leftovers") {
		t.Errorf("an accepted pair raised %+v", a)
	}
	if s := actionFor(actions, summaryKey); s == nil || !strings.Contains(s.Body, "- season 0 leftovers accepted as fine: 1 anime\n") {
		t.Errorf("summary = %+v, want the accepted pair counted", s)
	}
	moved := leftoversCase(t, 8)
	moved.reviewed = c.reviewed
	doc = moved.run(t)
	l := subjectIn(doc, review.AniDBKey(10)).Sections.Leftovers
	if m := l.Acceptance; m == nil || m.Matches || !slices.Equal(m.Causes, []string{"finding-inputs:tvdb:5000"}) {
		t.Fatalf("acceptance with 0x8 new = %+v, want it to lapse on the moved digest", m)
	}
	if a := actionFor(planDoc(t, doc, nil), review.AniDBKey(10)); a == nil || !strings.Contains(a.Body, "`reviewed/anidb-10.json` `leftovers` accepted these leftovers, and they are raised again because what `tvdb:5000` rests on changed.") {
		t.Errorf("action = %+v, want the re-raise cause", a)
	}
	cleared := newReviewCase(t)
	cleared.reviewed = c.reviewed
	doc = cleared.run(t)
	l = subjectIn(doc, review.AniDBKey(10)).Sections.Leftovers
	if l == nil || !l.Evaluated || l.raised() || l.Acceptance == nil || !l.Acceptance.Matches {
		t.Fatalf("leftovers after the pair cleared = %+v, want evaluated, clear and its part stale", l)
	}
	if s := actionFor(planDoc(t, doc, nil), summaryKey); s == nil || !strings.Contains(s.Body, "- [ ] `reviewed/anidb-10.json` `leftovers`: its section is clear without it, delete the part") {
		t.Errorf("summary = %+v, want the stale part listed", s)
	}
}

func TestAcceptRefusesLeftoversNotRaised(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := leftoversCase(t)
	from := writeFixture(t, c.dir, "r.json", c.run(t))
	clear := newReviewCase(t)
	clearFrom := writeFixture(t, clear.dir, "r.json", clear.run(t))
	for name, tc := range map[string]struct {
		from, want string
		args       []string
	}{
		"a clear section":    {from: clearFrom, want: "has no leftovers finding", args: []string{"-key", review.AniDBKey(12), "-part", review.Leftovers, "-verdict", review.Fine}},
		"unmappable":         {from: from, want: "verdict", args: []string{"-key", review.AniDBKey(10), "-part", review.Leftovers, "-verdict", review.Unmappable}},
		"inexpressible bare": {from: from, want: "names the format change", args: []string{"-key", review.AniDBKey(10), "-part", review.Leftovers, "-verdict", review.Inexpressible}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := accept(t, tc.from, t.TempDir(), tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("accept = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestExplainPrintsTheLeftovers(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := leftoversCase(t)
	var text bytes.Buffer
	args := []string{"explain", "-key", review.AniDBKey(10), "-list", listFixture, "-overlay", c.ov, "-mirror", c.mirror, "-reviewed", c.reviewed, "-layouts-tvdb", c.tvdb, "-layouts-tmdb", c.tmdb}
	if err := run(t.Context(), args, &text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "### Season 0 leftovers") || !strings.Contains(text.String(), "S0E7") {
		t.Errorf("explain text lacks the leftovers section:\n%s", text.String())
	}
	if l := explain(t, c, review.AniDBKey(10)).Subjects[0].Sections.Leftovers; l == nil || len(l.Findings) != 1 {
		t.Errorf("explain -json leftovers = %+v, want the finding", l)
	}
}

// A TVDB pair's issue is created after the watch's, and one whose pairs are
// all on TMDB with what only TMDB disagrees with; a section carried from
// the open issue ranks as a TVDB pair, whichever show it names.
func TestLeftoversRank(t *testing.T) {
	tv := raisedLeftovers(5000)
	tm := raisedLeftovers(9000)
	tm.Findings[0].Side = animelists.TMDB
	if leftoversRank(tv) != rankLeftovers || leftoversRank(tm) != rankTMDBOnly || rankLeftovers <= rankWatch {
		t.Errorf("ranks %d and %d, want %d after the watch and %d", leftoversRank(tv), leftoversRank(tm), rankLeftovers, rankTMDBOnly)
	}
	if got := carriedRank(review.Leftovers); got != rankLeftovers {
		t.Errorf("carried rank %d, want %d", got, rankLeftovers)
	}
}

// Each show links its own season 0 page, a TVDB series without a usable
// slug its series page, and an episode no Anime-Lists number addresses is
// listed by its number alone, with an inexpressible acceptance as its
// remedy beside the row and the fine acceptance.
func TestLeftoversTextLinksEachShowsSeasonZero(t *testing.T) {
	l := raisedLeftovers(5000)
	tm, bare := l.Findings[0], l.Findings[0]
	tm.Side, tm.Series = animelists.TMDB, 9000
	bare.Series, bare.Slug = 5001, ""
	bare.Unplaced = []leftovers.Episode{{Kind: anidb.Other, Number: 2}}
	bare.Promotions = []leftovers.Episode{{Kind: anidb.Promotion, Number: 2}}
	l.Findings = append(l.Findings, tm, bare)
	text := leftoversText(&subject{Key: review.AniDBKey(10)}, l)
	for _, want := range []string{
		"- TVDB: https://thetvdb.com/series/series-5000/seasons/official/0\n",
		"On TMDB show 9000:", "- TMDB: https://www.themoviedb.org/tv/9000/season/0\n",
		"On TVDB series 5001:\n\n- AniDB episodes that land nowhere: other 2 (no Anime-Lists number)\n- AniDB promotions that land nowhere: promotion 2 (no Anime-Lists number)\n",
		"- TVDB: https://thetvdb.com/dereferrer/series/5001\n",
		"Map one with an Anime-Lists number with a row on the node's overlay entry (docs/overlay.md)",
		"No row can map one with no Anime-Lists number: accept the pair as inexpressible",
		"-key anime:anidb:10 -part leftovers -verdict inexpressible -requires \"<the format change>\" -reason \"<which episode is which, by title>\" -evidence https://anidb.net/anime/10 -evidence https://thetvdb.com/series/series-5000/seasons/official/0\n",
		"If none is, accept the pair as fine",
		"-key anime:anidb:10 -part leftovers -verdict fine -reason ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if text := leftoversText(&subject{Key: review.AniDBKey(10)}, raisedLeftovers(5000)); strings.Contains(text, "inexpressible") {
		t.Errorf("text for episodes that all have an Anime-Lists number offers inexpressible:\n%s", text)
	}
	promo := raisedLeftovers(5000)
	promo.Findings[0].Promotions = []leftovers.Episode{{Kind: anidb.Promotion, Number: 2}}
	if text := leftoversText(&subject{Key: review.AniDBKey(10)}, promo); !strings.Contains(text, "-verdict inexpressible") {
		t.Errorf("text for a promotion with no Anime-Lists number lacks the inexpressible acceptance:\n%s", text)
	}
}

const linkRemedy = "On TVDB an overlay link can instead map an other episode or promotion no row names, when an AniList entry is that episode and the unclaimed episode carries its Anime-Lists number (docs/review.md, \"Season 0 leftovers\").\n\n"

// The link remedy is offered only where a TVDB finding lists an other
// episode or a promotion whose Anime-Lists number is an unclaimed
// episode, and it names the row condition the finding cannot check.
func TestLeftoversTextOffersALinkOnlyOntoAnUnclaimedNumber(t *testing.T) {
	for _, c := range []struct {
		edit func(f *leftovers.Finding)
		name string
		want bool
	}{
		{name: "a TVDB other episode on an unclaimed number", want: true, edit: func(f *leftovers.Finding) { f.Unplaced[1].SeasonZero = 5 }},
		{name: "a TVDB promotion on an unclaimed number", want: true, edit: func(f *leftovers.Finding) {
			f.Unplaced = f.Unplaced[:1]
			f.Promotions[0].SeasonZero = 6
		}},
		{name: "numbers no unclaimed episode carries", edit: func(*leftovers.Finding) {}},
		{name: "a TMDB show", edit: func(f *leftovers.Finding) {
			f.Side, f.Series = animelists.TMDB, 9000
			f.Unplaced[1].SeasonZero = 5
		}},
		{name: "specials only", edit: func(f *leftovers.Finding) {
			f.Unplaced, f.Promotions = f.Unplaced[:1], nil
			f.Unclaimed = []int{3, 5}
		}},
		{name: "no Anime-Lists number", edit: func(f *leftovers.Finding) {
			f.Unplaced = []leftovers.Episode{f.Unplaced[0], {Kind: anidb.Other, Number: 1}}
			f.Promotions = []leftovers.Episode{{Kind: anidb.Promotion, Number: 1}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := raisedLeftovers(5000)
			c.edit(&l.Findings[0])
			text := leftoversText(&subject{Key: review.AniDBKey(10)}, l)
			if c.want && !strings.Contains(text, linkRemedy) {
				t.Errorf("text lacks the link remedy:\n%s", text)
			}
			if !c.want && strings.Contains(text, "overlay link") {
				t.Errorf("text offers a link:\n%s", text)
			}
		})
	}
}

// Each list names at most maxListedLines items, then how many more.
func TestLeftoversTextCapsEachList(t *testing.T) {
	l := raisedLeftovers(5000)
	f := &l.Findings[0]
	f.Unplaced, f.Unclaimed = nil, nil
	for k := 1; k <= maxListedLines+2; k++ {
		f.Unplaced = append(f.Unplaced, leftovers.Episode{Kind: anidb.Special, Number: k, SeasonZero: k})
		f.Unclaimed = append(f.Unclaimed, k)
	}
	text := leftoversText(&subject{Key: review.AniDBKey(10)}, l)
	last := maxListedLines
	for _, want := range []string{fmt.Sprintf("special %d (S0 %d), 2 more\n", last, last), fmt.Sprintf("S0E%d, 2 more\n", last)} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	for _, unwanted := range []string{fmt.Sprintf("special %d (", last+1), fmt.Sprintf("S0E%d", last+1)} {
		if strings.Contains(text, unwanted) {
			t.Errorf("text names %q past the cap", unwanted)
		}
	}
}

// An acceptance covering every show the run read says nothing of the show
// it could not read, so the open issue keeps that show's text and stays
// open.
func TestAcceptedLeftoversBesideAnUnreadShowKeepTheIssueOpen(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := leftoversCase(t)
	st, err := datecheck.LoadStore(c.tmdb)
	if err != nil {
		t.Fatal(err)
	}
	old := st.Series[9000]
	old.Fetched = time.Now().Add(-240 * time.Hour)
	st.Series[9000] = old
	c.tmdb = writeStore(t, c.dir, "layouts-tmdb.json", st)
	key := review.AniDBKey(10)
	first := c.run(t)
	if l := subjectIn(first, key).Sections.Leftovers; l == nil || !l.Evaluated || len(l.Findings) != 1 || l.Unchecked["tmdb:9000"] == "" {
		t.Fatalf("leftovers = %+v, want the TVDB pair raised beside TMDB 9000 not checked", l)
	}
	if err := accept(t, writeFixture(t, c.dir, "first.json", first), c.reviewed, "-key", key, "-part", review.Leftovers, "-verdict", review.Fine); err != nil {
		t.Fatal(err)
	}
	both := raisedLeftovers(5000)
	tm := both.Findings[0]
	tm.Side, tm.Series = animelists.TMDB, 9000
	both.Findings = append(both.Findings, tm)
	open := actionFor(planDoc(t, &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{{Key: key, Sections: sections{Leftovers: both}}}}, nil), key)
	doc := c.run(t)
	s := subjectIn(doc, key)
	if s == nil {
		t.Fatalf("no subject %s", key)
	}
	l := s.Sections.Leftovers
	if l == nil || l.Evaluated || l.Acceptance == nil || !l.Acceptance.Matches || l.Reason == "" {
		t.Fatalf("leftovers = %+v, want covered and not checked for TMDB 9000", l)
	}
	a := actionFor(planDoc(t, doc, []issues.Issue{{Number: 7, State: "OPEN", Title: open.Title, Body: open.Body}}), key)
	if a == nil || a.Kind == issues.Close || !strings.Contains(a.Body, "On TMDB show 9000:") {
		t.Errorf("action = %+v, want the issue kept open with the TMDB pair carried", a)
	}
}

func leftoversPart(aid int) *review.LeftoversPart {
	return &review.LeftoversPart{
		Findings: map[string]string{"tvdb:5000": strings.Repeat("b", 64)},
		Verdict:  review.Fine, Date: "2026-10-15", Reason: reviewReason, Evidence: []string{fmt.Sprintf("https://anidb.net/anime/%d", aid)},
	}
}

// A part on an anime the list no longer has is stale.
func TestLeftoversPartOfAGoneNodeIsStale(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "test-key")
	c := newReviewCase(t)
	rec := review.Record{Key: review.AniDBKey(999), Title: "Gone", Parts: review.Parts{Leftovers: leftoversPart(999)}}
	b, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.reviewed, review.File(rec.Key)), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := actionFor(planDoc(t, c.run(t), nil), summaryKey); s == nil || !strings.Contains(s.Body, "- [ ] `reviewed/anidb-999.json` `leftovers`: its section is clear without it, delete the part") {
		t.Errorf("summary = %+v, want the gone node's part listed for deletion", s)
	}
}

// Kinds outside AniDB's own names never reach an issue.
func TestLeftoversTextOnlyCarriesAniDBKinds(t *testing.T) {
	l := raisedLeftovers(5000)
	l.Findings[0].Unplaced[0].Kind = "@everyone"
	text := leftoversText(&subject{Key: review.AniDBKey(10)}, l)
	if strings.Contains(text, "@everyone") || !strings.Contains(text, "episode 3 (S0 3)") {
		t.Errorf("text = %s", text)
	}
	if got := sectionsOf(issues.Render(&issues.Want{Key: "k", Body: wrapSection(review.Leftovers, text)})); got[review.Leftovers] == "" {
		t.Errorf("the section does not round-trip through sectionsOf")
	}
}
