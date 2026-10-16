package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/animap/internal/schema"
)

var (
	digestA = strings.Repeat("a", 64)
	digestB = strings.Repeat("b", 64)
)

func ruling(verdict string) Ruling {
	return Ruling{
		Verdict: verdict, Date: "2026-10-15", Reason: "AniDB 8 \"Summer Break\" is TVDB 1x08 \"Summer Break\".",
		Evidence: []string{"https://anidb.net/anime/3195", "https://thetvdb.com/series/kamichu/seasons/official/1"},
	}
}

func valid() Record {
	return Record{
		Key: AniDBKey(3195), Title: "Kamichu!",
		Parts: Parts{
			DateTVDB: &DatePart{Ruling: ruling(Fine), Findings: map[string]string{"date-mismatch:8": digestA, "missing-target:S1": digestB}},
			Watch:    []WatchPart{{AniListID: 1563, Ruling: ruling(Unmappable), Gaps: []string{"no_tvdb"}, RecordSHA256: digestA}},
		},
	}
}

func TestKeys(t *testing.T) {
	for key, want := range map[string]Subject{
		"anime:anidb:3195":  {Kind: "anidb", ID: 3195},
		"anime:anilist:156": {Kind: "anilist", ID: 156},
	} {
		got, ok := ParseKey(key)
		if !ok || got != want {
			t.Errorf("ParseKey(%q) = %+v, %t; want %+v", key, got, ok, want)
		}
	}
	if AniDBKey(3195) != "anime:anidb:3195" || AniListKey(156) != "anime:anilist:156" {
		t.Errorf("keys = %q, %q", AniDBKey(3195), AniListKey(156))
	}
	for _, bad := range []string{"anime:anidb:0", "anime:anidb:-1", "anime:anidb:01", "anime:mal:1", "lead:date:1", "anime:anidb", "anime:anidb:1x"} {
		if _, ok := ParseKey(bad); ok || File(bad) != "" {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
	if File(AniDBKey(3195)) != "anidb-3195.json" || File(AniListKey(156)) != "anilist-156.json" {
		t.Errorf("File = %q, %q", File(AniDBKey(3195)), File(AniListKey(156)))
	}
}

func write(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, ok := v.([]byte)
	if !ok {
		var err error
		if b, err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := valid()
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "anidb-3195.json", b)
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	back := got[AniDBKey(3195)]
	if len(got) != 1 || back.Title != "Kamichu!" || back.Parts.DateTVDB.Findings["date-mismatch:8"] != digestA || back.WatchFor(1563) == nil {
		t.Errorf("LoadDir = %+v, want the record back", got)
	}
	if none, err := LoadDir(filepath.Join(dir, "missing")); err != nil || len(none) != 0 {
		t.Errorf("LoadDir(missing) = %v, %v", none, err)
	}
}

func TestLoadDirRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		body any
		file string
	}{
		"a file not named for its key": {file: "anidb-1.json", body: valid()},
		"an unknown member":            {file: "anidb-3195.json", body: []byte(`{"key":"anime:anidb:3195","title":"x","parts":{},"extra":1}`)},
		"trailing data":                {file: "anidb-3195.json", body: []byte(`{"key":"anime:anidb:3195","title":"x","parts":{}} {}`)},
		"an old-format record":         {file: "3195.json", body: []byte(`{"anidb_id":3195,"title":"x","verdict":"fine"}`)},
		"an oversized file":            {file: "anidb-3195.json", body: []byte(strings.Repeat(" ", MaxFileBytes+1))},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.file, tc.body)
			if _, err := LoadDir(dir); err == nil {
				t.Error("LoadDir accepted it")
			}
		})
	}
}

func TestValidateVerdictsAndParts(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Record)
		ok     bool
	}{
		"a valid record": {ok: true, mutate: func(*Record) {}},
		"a date part inexpressible": {ok: true, mutate: func(r *Record) {
			r.Parts.DateTVDB.Verdict, r.Parts.DateTVDB.Requires = Inexpressible, "a per-episode TVDB series"
		}},
		"a TMDB date part alone":     {ok: true, mutate: func(r *Record) { r.Parts.DateTMDB, r.Parts.DateTVDB, r.Parts.Watch = r.Parts.DateTVDB, nil, nil }},
		"a watch part inexpressible": {ok: true, mutate: func(r *Record) { r.Parts.Watch[0].Verdict, r.Parts.Watch[0].Requires = Inexpressible, "x" }},
		"a dead movie inexpressible": {ok: true, mutate: func(r *Record) {
			r.Parts.DateTVDB.Findings = map[string]string{"dead-movie:5": digestA}
			r.Parts.DateTVDB.Verdict, r.Parts.DateTVDB.Requires = Inexpressible, "x"
		}},
		"a dead series inexpressible": {mutate: func(r *Record) {
			r.Parts.DateTVDB.Findings = map[string]string{"dead-series": digestA}
			r.Parts.DateTVDB.Verdict, r.Parts.DateTVDB.Requires = Inexpressible, "x"
		}},
		"a date part unmappable":         {mutate: func(r *Record) { r.Parts.DateTVDB.Verdict = Unmappable }},
		"a watch part fine":              {mutate: func(r *Record) { r.Parts.Watch[0].Verdict = Fine }},
		"a dead series fine":             {mutate: func(r *Record) { r.Parts.DateTVDB.Findings["dead-series"] = digestA }},
		"a dead movie fine":              {mutate: func(r *Record) { r.Parts.DateTVDB.Findings["dead-movie:5"] = digestA }},
		"inexpressible without requires": {mutate: func(r *Record) { r.Parts.DateTVDB.Verdict = Inexpressible }},
		"requires on a fine verdict":     {mutate: func(r *Record) { r.Parts.DateTVDB.Requires = "x" }},
		"no parts":                       {mutate: func(r *Record) { r.Parts = Parts{} }},
		"no title":                       {mutate: func(r *Record) { r.Title = " " }},
		"no reason":                      {mutate: func(r *Record) { r.Parts.DateTVDB.Reason = "" }},
		"no evidence":                    {mutate: func(r *Record) { r.Parts.Watch[0].Evidence = nil }},
		"evidence on another host":       {mutate: func(r *Record) { r.Parts.DateTVDB.Evidence = []string{"https://example.com/"} }},
		"a bad date":                     {mutate: func(r *Record) { r.Parts.DateTVDB.Date = "15/10/2026" }},
		"no findings":                    {mutate: func(r *Record) { r.Parts.DateTVDB.Findings = nil }},
		"a bad finding key":              {mutate: func(r *Record) { r.Parts.DateTVDB.Findings["lead:1"] = digestA }},
		"a bad finding digest":           {mutate: func(r *Record) { r.Parts.DateTVDB.Findings["date-mismatch:8"] = "AB" }},
		"a watch part with no gaps":      {mutate: func(r *Record) { r.Parts.Watch[0].Gaps = nil }},
		"unsorted gaps":                  {mutate: func(r *Record) { r.Parts.Watch[0].Gaps = []string{"no_tvdb", "no_anidb"} }},
		"a bad record digest":            {mutate: func(r *Record) { r.Parts.Watch[0].RecordSHA256 = "x" }},
		"two watch parts for one id":     {mutate: func(r *Record) { r.Parts.Watch = append(r.Parts.Watch, r.Parts.Watch[0]) }},
		"a date part on an AniList key":  {mutate: func(r *Record) { r.Key = AniListKey(1563) }},
		"another id on an AniList key":   {mutate: func(r *Record) { r.Key, r.Parts.DateTVDB = AniListKey(9), nil }},
		"a key that does not parse":      {mutate: func(r *Record) { r.Key = "lead:date:3195" }},
	} {
		t.Run(name, func(t *testing.T) {
			r := valid()
			tc.mutate(&r)
			if err := r.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate = %v, want ok %t", err, tc.ok)
			}
			if _, err := r.Encode(); (err == nil) != tc.ok {
				t.Errorf("Encode = %v, want ok %t", err, tc.ok)
			}
		})
	}
}

func TestDatePartChanges(t *testing.T) {
	p := valid().Parts.DateTVDB
	for name, tc := range map[string]struct {
		now  map[string]string
		want []string
	}{
		"every finding as accepted":       {now: map[string]string{"date-mismatch:8": digestA, "missing-target:S1": digestB}},
		"a finding cleared":               {now: map[string]string{"date-mismatch:8": digestA}},
		"nothing left":                    {now: map[string]string{}},
		"a listed finding's inputs moved": {now: map[string]string{"date-mismatch:8": digestB}, want: []string{"finding-inputs:date-mismatch:8"}},
		"a new finding":                   {now: map[string]string{"date-mismatch:8": digestA, "out-of-order:2": digestA}, want: []string{"finding:out-of-order:2"}},
		"both":                            {now: map[string]string{"missing-target:S1": digestA, "date-mismatch:9": digestA}, want: []string{"finding:date-mismatch:9", "finding-inputs:missing-target:S1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := p.Changes(tc.now); !slices.Equal(got, tc.want) {
				t.Errorf("Changes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWatchPartChanges(t *testing.T) {
	p := WatchPart{RecordSHA256: digestA, Gaps: []string{"no_tvdb", "season0_unresolved"}}
	for name, tc := range map[string]struct {
		sha  string
		gaps []string
		want []string
	}{
		"as accepted":      {sha: digestA, gaps: []string{"no_tvdb"}},
		"the record moved": {sha: digestB, gaps: []string{"no_tvdb"}, want: []string{"record"}},
		"a new gap":        {sha: digestA, gaps: []string{"lands_nowhere"}, want: []string{"gap:lands_nowhere"}},
		"both":             {sha: digestB, gaps: []string{"no_anidb"}, want: []string{"record", "gap:no_anidb"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := p.Changes(tc.sha, tc.gaps); !slices.Equal(got, tc.want) {
				t.Errorf("Changes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecordDigestFollowsTheWatchedFields(t *testing.T) {
	season := 1
	base := schema.Record{AniListID: 1, AniDBID: 2, Type: "TV", TVDBID: 3, TVDBSeason: &season, Episodes: 12}
	sum := RecordDigest(&base)
	if !hexRE.MatchString(sum) || RecordDigest(nil) == sum || !hexRE.MatchString(RecordDigest(nil)) {
		t.Fatalf("digests %q, %q", sum, RecordDigest(nil))
	}
	for name, tc := range map[string]struct {
		mutate func(*schema.Record)
		moves  bool
	}{
		"the type":          {moves: true, mutate: func(r *schema.Record) { r.Type = "MOVIE" }},
		"the season":        {moves: true, mutate: func(r *schema.Record) { zero := 0; r.TVDBSeason = &zero }},
		"the placement":     {moves: true, mutate: func(r *schema.Record) { r.TVDBPlacement = []schema.Segment{{Start: 1, End: 1}} }},
		"the episode count": {moves: true, mutate: func(r *schema.Record) { r.Episodes = 13 }},
		"a TMDB movie":      {moves: true, mutate: func(r *schema.Record) { r.TMDBMovieIDs = []int{5} }},
		"the MAL id":        {mutate: func(r *schema.Record) { r.MALID = 9 }},
		"the TMDB show":     {mutate: func(r *schema.Record) { r.TMDBTVID = 9 }},
		"the AniList id":    {mutate: func(r *schema.Record) { r.AniListID = 9 }},
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if got := RecordDigest(&r); (got != sum) != tc.moves {
				t.Errorf("digest moved = %t, want %t", got != sum, tc.moves)
			}
		})
	}
}
