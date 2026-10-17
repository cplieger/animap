package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/mapread"
)

func explain(t *testing.T, c *reviewCase, key string, extra ...string) *reviewDoc {
	t.Helper()
	var out bytes.Buffer
	args := append([]string{"explain", "-key", key, "-json", "-list", listFixture, "-overlay", c.ov, "-mirror", c.mirror, "-reviewed", c.reviewed, "-layouts-tvdb", c.tvdb, "-layouts-tmdb", c.tmdb}, extra...)
	if err := run(t.Context(), args, &out); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var doc reviewDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Subjects) != 1 || doc.Subjects[0].Key != key || doc.Subjects[0].Detail == nil {
		t.Fatalf("explain = %+v, want one subject with its detail", doc.Subjects)
	}
	return &doc
}

// Every AniDB episode of every kind of AniDB 10 is listed by its AniDB
// number, past a gap in the regular episodes too, and an extra no
// Anime-Lists number addresses as landing nowhere, with where its node puts
// each on TVDB and on TMDB, and the claims on each target.
func TestExplainListsEveryEpisodeOnBothSides(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	a := anidb.Anime{
		Regular: []anidb.Episode{{Number: 1, AirDate: "2020-01-06"}, {Number: 2, AirDate: "2020-01-13"}, {Number: 5}}, Finished: true, Titles: fixtureOtherTitles,
		Specials: []anidb.Episode{{Number: 1, AirDate: "2020-06-01"}, {Number: 3}},
		Extras: []anidb.Extra{
			{Kind: anidb.Credit, Number: 1, SeasonZero: 101},
			{Kind: anidb.Credit, Number: 120},
			{Kind: anidb.Promotion, Number: 1, SeasonZero: 201},
			{Kind: anidb.Parody, Number: 1, SeasonZero: 301},
			{Kind: anidb.Other, Number: 1, SeasonZero: 401},
		},
	}
	c.mirror = snapshotWith(t, c.dir, map[int]anidb.Anime{10: a})
	st, err := datecheck.LoadStore(c.tvdb)
	if err != nil {
		t.Fatal(err)
	}
	lay := st.Series[5000]
	lay.Episodes = append(lay.Episodes, aired(tvdb(0, 2, 0, "2020-06-01"))...)
	st.Series[5000] = lay
	c.tvdb = writeStore(t, c.dir, "layouts-tvdb.json", st)
	doc := explain(t, c, review.AniDBKey(10))
	d := doc.Subjects[0].Detail
	var kinds []string
	for _, e := range d.Episodes {
		kinds = append(kinds, e.Kind)
		if e.TVDB == nil || e.TMDB == nil || e.TVDB.Show != 5000 || e.TMDB.Show != 9000 || e.AniDB != 10 {
			t.Errorf("episode %+v, want placements on TVDB 5000 and TMDB 9000", e)
		}
	}
	if want := []string{"regular", "regular", "regular", "special", "special", "credit", "credit", "promotion", "parody", "other"}; !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if n := d.Episodes[2].Number; n != 5 {
		t.Errorf("third regular episode = %d, want 5, its AniDB number past the gap", n)
	}
	s1 := d.Episodes[3].TVDB
	if s1.Default || len(s1.Targets) != 1 || s1.Targets[0].Season != 0 || s1.Targets[0].Number != 2 || s1.Targets[0].AirDate != "2020-06-01" ||
		s1.Targets[0].Title == "" || !slices.Equal(s1.Targets[0].Claims, []string{"10 S1"}) {
		t.Errorf("S1 on TVDB = %+v, want its row's 0x2 with the date, title digest and its own claim", s1)
	}
	if s3 := d.Episodes[4].TVDB; len(s3.Targets) != 0 {
		t.Errorf("S3 on TVDB = %+v, want its 3-0 row placing it nowhere", s3)
	}
	if e := d.Episodes[5]; e.Number != 1 || e.SeasonZero != 101 || !e.TVDB.Default || e.TVDB.Unaddressable || len(e.TVDB.Targets) != 1 || e.TVDB.Targets[0].Number != 101 || !e.TVDB.Targets[0].Missing {
		t.Errorf("credit 1 = %+v on TVDB %+v, want season 0 number 101 and the default 0x101, missing", e, e.TVDB)
	}
	for _, p := range []*explainPlacement{d.Episodes[6].TVDB, d.Episodes[6].TMDB} {
		if e := d.Episodes[6]; e.Number != 120 || e.SeasonZero != 0 || !p.Unaddressable || p.Default || len(p.Targets) != 0 {
			t.Errorf("credit 120 = %+v at %+v, want it listed and unaddressable, landing nowhere", e, p)
		}
	}
	if ep1 := d.Episodes[0].TMDB; len(ep1.Targets) != 1 || ep1.Targets[0].Season != 1 || ep1.Targets[0].Number != 1 || ep1.Targets[0].AirDate != "2020-01-06" {
		t.Errorf("episode 1 on TMDB = %+v, want 1x1 on its date", ep1)
	}
	if d.Fetched["tvdb:5000"] == "" || d.Fetched["tmdb:9000"] == "" {
		t.Errorf("fetched = %v, want both shows' fetch times", d.Fetched)
	}
	if s := doc.Subjects[0]; s.Sections.DateTVDB == nil || len(s.Sections.DateTVDB.Digests) == 0 {
		t.Errorf("sections = %+v, want the TVDB finding the review would raise", s.Sections)
	}
	var text bytes.Buffer
	args := []string{"explain", "-key", review.AniDBKey(10), "-list", listFixture, "-overlay", c.ov, "-mirror", c.mirror, "-reviewed", c.reviewed, "-layouts-tvdb", c.tvdb, "-layouts-tmdb", c.tmdb}
	if err := run(t.Context(), args, &text); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"anime: AniDB 10 Series with season 1, TVDB dates", "- 10 credit 1 (S0 101) none: by default S0E101 none missing | ", "- 10 credit 120 none: nowhere, no Anime-Lists number | nowhere, no Anime-Lists number\n", "TMDB dates: not checked this run (TMDB_TOKEN is not set)."} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}
}

// explain -review takes the sections from the daily run's report.
func TestExplainFromAReview(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	from := writeFixture(t, c.dir, "daily.json", &reviewDoc{Sources: evaluatedSources(), Subjects: []*subject{{Key: review.AniDBKey(10), Sections: sections{Drift: raisedDrift()}}}})
	doc := explain(t, c, review.AniDBKey(10), "-review", from)
	if s := doc.Subjects[0]; s.Sections.Drift == nil || s.Sections.DateTVDB != nil || len(s.Detail.Episodes) != 2 {
		t.Errorf("subject = %+v, want the report's sections and the detail from the inputs", s)
	}
}

// An AniDB subject's published entries are those whose one part is the
// anime itself; an entry naming some of its episodes is not one.
func TestExplainAnAniDBSubjectsEntries(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	var rel mapread.Document
	if err := json.Unmarshal(previousDoc(t, mapread.Version, 0), &rel); err != nil {
		t.Fatal(err)
	}
	rel.Anime = []mapread.Anime{{AniDBID: 10, TVDBID: 5000, Last: mapread.Last{Regular: 12}}}
	rel.Entries = []mapread.Entry{
		{AniListID: 100, Type: "TV", Parts: []mapread.Part{{AniDBID: 10, Kind: mapread.Regular}}},
		{AniListID: 101, Type: "TV", Parts: []mapread.Part{{AniDBID: 10, Kind: mapread.Regular, Start: 1, End: 12}}},
	}
	release := writeFixture(t, c.dir, "release.json", rel)
	d := explain(t, c, review.AniDBKey(10), "-release", release).Subjects[0].Detail
	if len(d.Entries) != 1 || d.Entries[0].AniListID != 100 {
		t.Errorf("entries = %+v, want AniList 100's alone", d.Entries)
	}
}

// An AniList subject with no node of its own is explained through what
// names it: its published entry, anime-offline-database's AniDB ids for
// it, the nodes and anime rows those name with their episodes, and every
// show they map to with each node on it and each episode's claims.
func TestExplainAnAniListSubjectThroughItsCandidates(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	var rel mapread.Document
	if err := json.Unmarshal(previousDoc(t, mapread.Version, 0), &rel); err != nil {
		t.Fatal(err)
	}
	rel.Anime = []mapread.Anime{{AniDBID: 10, TVDBID: 5000, Last: mapread.Last{Special: 1}}}
	rel.Entries = []mapread.Entry{{AniListID: 102, Type: "OVA", Parts: []mapread.Part{{AniDBID: 10, Kind: mapread.Special, Start: 1, End: 1}}}}
	release := writeFixture(t, c.dir, "release.json", rel)
	d := explain(t, c, review.AniListKey(102), "-release", release, "-aod", aodFixture).Subjects[0].Detail
	if len(d.Entries) != 1 || d.Entries[0].AniListID != 102 || d.Entries[0].Parts[0].AniDBID != 10 {
		t.Errorf("entries = %+v, want AniList 102's published entry", d.Entries)
	}
	if len(d.Anime) != 1 || d.Anime[0].AniDBID != 10 {
		t.Errorf("anime = %+v, want the published row of AniDB 10, the only candidate the release has", d.Anime)
	}
	if !slices.Equal(d.AOD[102], []int{13}) || !slices.Equal(d.Candidates, []int{10, 13}) {
		t.Errorf("aod %v, candidates %v; want AniDB 13 from the database and 10 from the entry's part", d.AOD, d.Candidates)
	}
	if len(d.Episodes) == 0 || d.Episodes[0].AniDB != 10 || d.Episodes[0].TVDB == nil || d.Episodes[0].TVDB.Show != 5000 {
		t.Errorf("episodes = %+v, want AniDB 10's, placed on TVDB 5000", d.Episodes)
	}
	var shows []string
	for _, sh := range d.Shows {
		shows = append(shows, fmt.Sprintf("%s:%d", sh.Side, sh.Show))
		if sh.Side == "tvdb" && sh.Show == 5000 {
			if len(sh.Nodes) != 1 || sh.Nodes[0].AniDB != 10 || len(sh.Episodes) != 4 || !slices.Equal(sh.Episodes[0].Claims, []string{"10 ep1"}) {
				t.Fatalf("TVDB 5000 = %+v, want node 10, its three episodes and its episode 1 claiming S1E1", sh)
			}
			if last := sh.Episodes[3]; last.Season != 0 || last.Number != 2 || !last.Missing || !slices.Equal(last.Claims, []string{"10 S1"}) {
				t.Errorf("last TVDB 5000 episode = %+v, want S0E2, claimed by special 1 and missing from the layout", last)
			}
		}
	}
	if !slices.Equal(shows, []string{"tvdb:5000", "tvdb:5002", "tmdb:9000"}) {
		t.Errorf("shows = %v, want every show of both candidates", shows)
	}
}

// The claims explain lists are the build's: a link the build skips, here
// one whose AniList id the database already gives AniDB 10, claims nothing
// once anime-offline-database is read.
func TestExplainListsNoClaimOfASkippedLink(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	parent := fin(12, 1)
	parent.Extras = []anidb.Extra{{Kind: anidb.Other, Number: 1, SeasonZero: 401}}
	c.mirror = snapshotWith(t, c.dir, map[int]anidb.Anime{10: parent})
	writeLinks(t, c.ov, linkFile(100, overlay.Part{AniDBID: 10, Specials: []int{401}}))
	claimed := func(d *explainDetail) []string {
		for _, sh := range d.Shows {
			if sh.Side != "tvdb" || sh.Show != 5000 {
				continue
			}
			for _, e := range sh.Episodes {
				if e.Season == 0 && e.Number == 401 {
					return e.Claims
				}
			}
		}
		return nil
	}
	if got := claimed(explain(t, c, review.AniDBKey(10)).Subjects[0].Detail); !slices.Equal(got, []string{"10 S401"}) {
		t.Fatalf("claims on TVDB 5000 S0E401 with the database unread = %v, want the link's", got)
	}
	if got := claimed(explain(t, c, review.AniDBKey(10), "-aod", aodFixture).Subjects[0].Detail); got != nil {
		t.Errorf("claims on TVDB 5000 S0E401 = %v, want none: the build skips the link", got)
	}
}
