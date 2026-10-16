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
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/schema"
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

// Every AniDB episode kind of AniDB 10 is listed with where its node puts
// it on TVDB and on TMDB, and the claims on each target.
func TestExplainListsEveryEpisodeOnBothSides(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	a := anidb.Anime{
		Regular: []string{"2020-01-06", "2020-01-13"}, Finished: true, Titles: fixtureOtherTitles,
		Specials: []anidb.Episode{{Number: 1, AirDate: "2020-06-01"}, {Number: 3}},
		Extras:   []anidb.Episode{{Number: 101}, {Number: 201}, {Number: 301}, {Number: 401}},
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
	if want := []string{"regular", "regular", "special", "special", "credit", "promotion", "parody", "other"}; !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	s1 := d.Episodes[2].TVDB
	if s1.Default || len(s1.Targets) != 1 || s1.Targets[0].Season != 0 || s1.Targets[0].Number != 2 || s1.Targets[0].AirDate != "2020-06-01" ||
		s1.Targets[0].Title == "" || !slices.Equal(s1.Targets[0].Claims, []string{"10 S1"}) {
		t.Errorf("S1 on TVDB = %+v, want its row's 0x2 with the date, title digest and its own claim", s1)
	}
	if s3 := d.Episodes[3].TVDB; len(s3.Targets) != 0 {
		t.Errorf("S3 on TVDB = %+v, want its 3-0 row placing it nowhere", s3)
	}
	if x := d.Episodes[4].TVDB; !x.Default || len(x.Targets) != 1 || x.Targets[0].Number != 101 || !x.Targets[0].Missing {
		t.Errorf("credit 101 on TVDB = %+v, want the default 0x101, missing", x)
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
	for _, want := range []string{"anime: AniDB 10 Series with season 1, TVDB dates", "- 10 credit 101 none: by default S0E101 none missing | ", "TMDB dates: not checked this run (TMDB_TOKEN is not set)."} {
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

// An AniList subject with no node of its own is explained through what
// names it: its published record, anime-offline-database's AniDB ids for
// it, the nodes those name with their episodes, and every show they map
// to with each node on it and each episode's claims.
func TestExplainAnAniListSubjectThroughItsCandidates(t *testing.T) {
	t.Setenv("TMDB_TOKEN", "")
	c := newReviewCase(t)
	rec := schema.Record{AniListID: 102, Type: "OVA", AniDBParent: &schema.ParentSpecials{AniDBID: 10, Specials: []int{1}}}
	release := writeFixture(t, c.dir, "release.json", schema.Document{Version: schema.Version, Attribution: schema.DefaultAttribution, Records: []schema.Record{rec}})
	d := explain(t, c, review.AniListKey(102), "-release", release, "-aod", aodFixture).Subjects[0].Detail
	if len(d.Records) != 1 || d.Records[0].AniListID != 102 || d.Records[0].AniDBParent.AniDBID != 10 {
		t.Errorf("records = %+v, want AniList 102's published record", d.Records)
	}
	if !slices.Equal(d.AOD[102], []int{13}) || !slices.Equal(d.Candidates, []int{10, 13}) {
		t.Errorf("aod %v, candidates %v; want AniDB 13 from the database and 10, the parent, from the record", d.AOD, d.Candidates)
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
