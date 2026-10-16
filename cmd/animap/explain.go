package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/animap/internal/tmdb"
)

// explainDetail is what a reviewer needs beyond the sections: every AniDB
// episode of every kind, of the subject's nodes and of its candidates,
// with where the node puts it on both sides; every show those nodes map
// to, with each node on it and each episode's claims; the published
// records the subject watches and anime-offline-database's AniDB ids for
// them; and the overlay data and acceptance record that bear on the
// subject. Candidates are the AniDB ids those records and the database
// name that the subject's own nodes are not. Fetched says when each
// show's layout was read.
type explainDetail struct {
	Entry      *overlay.Entry    `json:"entry,omitempty"`
	Acceptance *review.Record    `json:"acceptance,omitempty"`
	Fetched    map[string]string `json:"fetched,omitempty"`
	AOD        map[int][]int     `json:"aod,omitempty"`
	Episodes   []explainEpisode  `json:"episodes"`
	Links      []overlay.Link    `json:"links,omitempty"`
	Records    []schema.Record   `json:"records,omitempty"`
	Candidates []int             `json:"candidates,omitempty"`
	Shows      []explainShow     `json:"shows,omitempty"`
}

// explainShow is one provider show: every node mapped to it, with the
// overlay entry patching it, and every episode of its layout with its
// claims, then each claimed target the layout lacks, Missing when the
// store holds the show's layout.
type explainShow struct {
	Side     string          `json:"side"`
	Nodes    []explainNode   `json:"nodes"`
	Episodes []explainTarget `json:"episodes"`
	Show     int             `json:"show"`
}

type explainNode struct {
	Name    string `json:"name,omitempty"`
	Overlay string `json:"overlay,omitempty"`
	AniDB   int    `json:"anidb"`
}

// explainEpisode is one AniDB episode. Kind is regular, special, credit,
// promotion, parody or other; Titles the digest of its AniDB titles.
type explainEpisode struct {
	TVDB    *explainPlacement `json:"tvdb,omitempty"`
	TMDB    *explainPlacement `json:"tmdb,omitempty"`
	Kind    string            `json:"kind"`
	AirDate string            `json:"air_date,omitempty"`
	Titles  string            `json:"titles,omitempty"`
	AniDB   int               `json:"anidb"`
	Number  int               `json:"number"`
}

type explainPlacement struct {
	Targets  []explainTarget `json:"targets"`
	Show     int             `json:"show"`
	Absolute int             `json:"absolute,omitempty"`
	Default  bool            `json:"default,omitempty"`
}

// explainTarget is one target with the provider's date and title digest,
// Missing when the layout lacks it; Claims are every claim on it, this
// node's included, as the collision guard reads them.
type explainTarget struct {
	AirDate  string   `json:"air_date,omitempty"`
	Title    string   `json:"title,omitempty"`
	Claims   []string `json:"claims,omitempty"`
	Season   int      `json:"season"`
	Number   int      `json:"number"`
	Absolute int      `json:"absolute,omitempty"`
	Missing  bool     `json:"missing,omitempty"`
}

func runExplain(ctx context.Context, args []string, stdout io.Writer, log *slog.Logger) error {
	var c reviewConfig
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	reviewFlags(fs, &c)
	key := fs.String("key", "", "subject key: anime:anidb:<id> or anime:anilist:<id>")
	fromReview := fs.String("review", "", "review.json whose sections to explain, instead of judging the stores now")
	fetch := fs.Bool("fetch", false, "refresh this subject's TVDB and TMDB shows first; the stores are not written")
	asJSON := fs.Bool("json", false, "print explain.json instead of text")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list", "key"); err != nil {
		return err
	}
	sub, ok := review.ParseKey(*key)
	if !ok {
		return fmt.Errorf("%w: explain: -key %q is not a subject key", errUsage, *key)
	}
	token, err := tmdbToken()
	if err != nil {
		return err
	}
	r, err := loadReview(&c, token, log)
	if err != nil {
		return err
	}
	if *fetch {
		r.refreshSubject(ctx, sub, log)
	}
	var doc *reviewDoc
	if *fromReview != "" {
		doc = &reviewDoc{}
		if rErr := readJSON(*fromReview, doc); rErr != nil {
			return rErr
		}
	} else {
		r.readWatch(ctx, &c, log)
		doc = r.evaluate(time.Now())
	}
	s := &subject{Key: *key, Title: r.titleOf(*key)}
	if i := slices.IndexFunc(doc.Subjects, func(x *subject) bool { return x.Key == *key }); i >= 0 {
		s = doc.Subjects[i]
	}
	s.Detail = r.detail(sub)
	out := &reviewDoc{Sources: doc.Sources, Inputs: doc.Inputs, Subjects: []*subject{s}}
	if *asJSON {
		b, mErr := json.MarshalIndent(out, "", "  ")
		if mErr != nil {
			return mErr
		}
		_, err = fmt.Fprintf(stdout, "%s\n", b)
		return err
	}
	_, err = io.WriteString(stdout, explainText(out, s))
	return err
}

// subjectIDs are the AniDB nodes an explanation covers: the node itself,
// or every AniDB anime the subject's link names.
func (r *reviewRun) subjectIDs(sub review.Subject) []int {
	if sub.AniDB() {
		return []int{sub.ID}
	}
	var out []int
	for i := range r.links {
		if r.links[i].AniListID == sub.ID {
			out = append(out, r.links[i].AniDBIDs()...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// refreshSubject fetches the shows and movies of the subject's nodes,
// due or not, into the in-memory stores.
func (r *reviewRun) refreshSubject(ctx context.Context, sub review.Subject, log *slog.Logger) {
	var tvdb, shows, movies []int
	for _, aid := range r.subjectIDs(sub) {
		n := r.patched[aid]
		if n == nil {
			continue
		}
		if s, ok := datecheck.Series(n, animelists.TVDB); ok {
			tvdb = append(tvdb, s)
		}
		if s, ok := datecheck.Series(n, animelists.TMDB); ok {
			shows = append(shows, s)
		}
		movies = append(movies, datecheck.Movies(n)...)
	}
	sky := getterCount{g: source.NewMemory(pace, log, source.WithoutCache()), n: &atomic.Int64{}}
	fetchAll(ctx, &refreshJob{
		name: "SkyHook", into: r.tvdb.Series, due: tvdb, stale: r.tvdbStale, budget: time.Minute, health: r.health[review.DateTVDB], count: sky.n,
		fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
			show, err := skyhook.Fetch(ctx, sky, id)
			if err != nil {
				return datecheck.Layout{}, err
			}
			return datecheck.LayoutOf(show, time.Now().UTC()), nil
		},
	}, log)
	if r.token == "" || r.facts == nil {
		return
	}
	tm := getterCount{g: source.NewMemory(tmdbPace, log, source.WithoutCache(), source.WithBearer(tmdb.Host, r.token)), n: &atomic.Int64{}}
	h := r.health[review.DateTMDB]
	fetchAll(ctx, &refreshJob{
		name: tmdbName, into: r.tmdb.Series, due: shows, stale: r.tmdbStale, budget: time.Minute, health: h, count: tm.n,
		fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
			show, err := tmdb.Fetch(ctx, tm, id)
			if err != nil {
				return datecheck.Layout{}, err
			}
			return datecheck.LayoutOfTMDB(show, time.Now().UTC()), nil
		},
	}, log)
	fetchAll(ctx, &refreshJob{
		name: tmdbName, into: r.tmdb.Movies, due: movies, stale: r.movieStale, budget: time.Minute, health: h, count: tm.n,
		fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
			if err := tmdb.Movie(ctx, tm, id); err != nil {
				return datecheck.Layout{}, err
			}
			return datecheck.Layout{Fetched: time.Now().UTC()}, nil
		},
	}, log)
}

func (r *reviewRun) detail(sub review.Subject) *explainDetail {
	d := &explainDetail{Fetched: map[string]string{}, Episodes: []explainEpisode{}}
	if rec, ok := r.records[review.AniDBKey(sub.ID)]; ok && sub.AniDB() {
		d.Acceptance = &rec
	} else if rec, ok := r.records[review.AniListKey(sub.ID)]; ok && !sub.AniDB() {
		d.Acceptance = &rec
	}
	own := r.subjectIDs(sub)
	d.Records, d.AOD = r.watchedRecords(sub)
	d.Candidates = candidates(d.Records, d.AOD, own)
	ids := slices.Concat(own, d.Candidates)
	for i := range r.entries {
		if sub.AniDB() && r.entries[i].AniDBID == sub.ID {
			d.Entry = &r.entries[i]
		}
	}
	for i := range r.links {
		l := &r.links[i]
		if l.AniListID == sub.ID && !sub.AniDB() || sub.AniDB() && slices.Contains(l.AniDBIDs(), sub.ID) {
			d.Links = append(d.Links, *l)
		}
	}
	claims := r.claimIndex(ids)
	for _, aid := range ids {
		d.Episodes = append(d.Episodes, r.episodes(aid, claims, d.Fetched)...)
	}
	d.Shows = r.shows(claims)
	return d
}

// watchedRecords are the published records of the subject: the AniList
// record of an AniList subject, every record carrying an AniDB subject's
// node; and, by AniList id, the AniDB ids anime-offline-database gives
// each of them, an AniList subject with no record included.
func (r *reviewRun) watchedRecords(sub review.Subject) (recs []schema.Record, aod map[int][]int) {
	if r.release != nil {
		for i := range r.release.Records {
			rec := &r.release.Records[i]
			if sub.AniDB() && rec.AniDBID == sub.ID && rec.AniListID > 0 || !sub.AniDB() && rec.AniListID == sub.ID {
				recs = append(recs, *rec)
			}
		}
	}
	als := []int{}
	if !sub.AniDB() {
		als = append(als, sub.ID)
	}
	for i := range recs {
		als = append(als, recs[i].AniListID)
	}
	aod = map[int][]int{}
	for _, al := range als {
		if ids := r.aod[al]; len(ids) > 0 {
			aod[al] = ids
		}
	}
	return recs, aod
}

// candidates are the AniDB ids recs and aod name, the parent of a record
// filed as another anime's specials included, that own does not hold.
func candidates(recs []schema.Record, aod map[int][]int, own []int) []int {
	var out []int
	for i := range recs {
		out = append(out, recs[i].AniDBID)
		if p := recs[i].AniDBParent; p != nil {
			out = append(out, p.AniDBID)
		}
	}
	for _, ids := range aod {
		out = append(out, ids...)
	}
	out = slices.DeleteFunc(out, func(id int) bool { return id <= 0 || slices.Contains(own, id) })
	slices.Sort(out)
	return slices.Compact(out)
}

// shows lists each show claims indexes: its nodes after the overlay, and
// its episodes with their claims.
func (r *reviewRun) shows(claims map[claimKey]map[[2]int][]string) []explainShow {
	keys := slices.SortedFunc(maps.Keys(claims), func(a, b claimKey) int { return cmp.Or(cmp.Compare(a.side, b.side), cmp.Compare(a.show, b.show)) })
	paths := make(map[int]string, len(r.entries))
	for i := range r.entries {
		paths[r.entries[i].AniDBID] = r.entries[i].Path()
	}
	out := make([]explainShow, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.show(k, claims[k], paths))
	}
	return out
}

func (r *reviewRun) show(k claimKey, claims map[[2]int][]string, paths map[int]string) explainShow {
	sh := explainShow{Side: "tvdb", Show: k.show, Nodes: []explainNode{}, Episodes: []explainTarget{}}
	store := r.tvdb
	if k.side == animelists.TMDB {
		sh.Side, store = "tmdb", r.tmdb
	}
	for _, aid := range slices.Sorted(maps.Keys(r.patched)) {
		if s, ok := datecheck.Series(r.patched[aid], k.side); ok && s == k.show {
			sh.Nodes = append(sh.Nodes, explainNode{AniDB: aid, Name: r.patched[aid].Name, Overlay: paths[aid]})
		}
	}
	lay, stored := store.Series[k.show]
	laid := map[[2]int]bool{}
	for _, e := range lay.Episodes {
		t := [2]int{e.Season, e.Number}
		laid[t] = true
		sh.Episodes = append(sh.Episodes, explainTarget{Season: e.Season, Number: e.Number, Absolute: e.Absolute, AirDate: e.AirDate, Title: e.Title, Claims: claims[t]})
	}
	for _, t := range slices.SortedFunc(maps.Keys(claims), func(a, b [2]int) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) }) {
		if !laid[t] {
			sh.Episodes = append(sh.Episodes, explainTarget{Season: t[0], Number: t[1], Claims: claims[t], Missing: stored})
		}
	}
	return sh
}

type claimKey struct {
	side animelists.Side
	show int
}

// claimIndex is the collision guard's claims on every show the nodes map
// to, with the overlay links compiled as the build compiles them.
func (r *reviewRun) claimIndex(ids []int) map[claimKey]map[[2]int][]string {
	out := map[claimKey]map[[2]int][]string{}
	if r.facts == nil {
		return out
	}
	compiled := r.compiledLinks()
	for _, aid := range ids {
		n := r.patched[aid]
		if n == nil {
			continue
		}
		for _, side := range []animelists.Side{animelists.TVDB, animelists.TMDB} {
			if s, ok := datecheck.Series(n, side); ok {
				k := claimKey{side: side, show: s}
				if out[k] == nil {
					out[k] = guard.Claims(r.list.Nodes, r.entries, compiled, r.facts, side, s)
				}
			}
		}
	}
	return out
}

// compiledLinks are the links that compile against this run's inputs, as
// the build compiles them; a link that does not compile claims nothing.
func (r *reviewRun) compiledLinks() []overlay.Compiled {
	byAniDB := make(map[int]*overlay.Entry, len(r.entries))
	for i := range r.entries {
		byAniDB[r.entries[i].AniDBID] = &r.entries[i]
	}
	var out []overlay.Compiled
	for i := range r.links {
		if _, identity := r.links[i].Identity(r.facts); identity {
			continue
		}
		if c, err := r.links[i].Compile(r.patched, byAniDB, r.facts); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// episodes lists every AniDB episode of node aid with its placements.
func (r *reviewRun) episodes(aid int, claims map[claimKey]map[[2]int][]string, fetched map[string]string) []explainEpisode {
	n := r.patched[aid]
	if n == nil || r.facts == nil {
		return nil
	}
	a, _ := r.facts.Anime(aid)
	var out []explainEpisode
	for i, date := range a.Regular {
		e := explainEpisode{AniDB: aid, Kind: "regular", Number: i + 1, AirDate: date}
		if i < len(a.RegularTitles) {
			e.Titles = a.RegularTitles[i]
		}
		out = append(out, e)
	}
	for _, sp := range a.Specials {
		out = append(out, explainEpisode{AniDB: aid, Kind: "special", Number: sp.Number, AirDate: sp.AirDate, Titles: sp.Titles})
	}
	for _, x := range a.Extras {
		out = append(out, explainEpisode{AniDB: aid, Kind: extraKind(x.Number), Number: x.Number, Titles: x.Titles})
	}
	for i := range out {
		season := 1
		if out[i].Kind != "regular" {
			season = 0
		}
		out[i].TVDB = r.placement(n, animelists.TVDB, season, out[i].Number, claims, fetched)
		out[i].TMDB = r.placement(n, animelists.TMDB, season, out[i].Number, claims, fetched)
	}
	return out
}

// extraKind names an extra by its Anime-Lists season 0 block.
func extraKind(n int) string {
	switch {
	case n < 201:
		return "credit"
	case n < 301:
		return "promotion"
	case n < 401:
		return "parody"
	}
	return "other"
}

func (r *reviewRun) placement(n *animelists.Node, side animelists.Side, season, k int, claims map[claimKey]map[[2]int][]string, fetched map[string]string) *explainPlacement {
	show, ok := datecheck.Series(n, side)
	if !ok {
		return nil
	}
	store, label := r.tvdb, "tvdb"
	if side == animelists.TMDB {
		store, label = r.tmdb, "tmdb"
	}
	lay, laid := store.Series[show]
	if laid {
		fetched[fmt.Sprintf("%s:%d", label, show)] = lay.Fetched.UTC().Format(time.RFC3339)
	}
	byEp, byAbs := map[[2]int]datecheck.Aired{}, map[int]datecheck.Aired{}
	for _, e := range lay.Episodes {
		byEp[[2]int{e.Season, e.Number}] = e
		if e.Absolute > 0 && e.Season > 0 {
			byAbs[e.Absolute] = e
		}
	}
	p := n.Place(side, season, k)
	out := &explainPlacement{Show: show, Absolute: p.Absolute, Default: p.Default, Targets: []explainTarget{}}
	targets := p.Targets
	if p.Absolute > 0 {
		if e, ok := byAbs[p.Absolute]; ok {
			targets = [][2]int{{e.Season, e.Number}}
		} else {
			out.Targets = append(out.Targets, explainTarget{Absolute: p.Absolute, Missing: true})
		}
	}
	for _, t := range targets {
		e, ok := byEp[t]
		out.Targets = append(out.Targets, explainTarget{
			Season: t[0], Number: t[1], Absolute: e.Absolute, AirDate: e.AirDate, Title: e.Title, Missing: laid && !ok,
			Claims: claims[claimKey{side: side, show: show}][t],
		})
	}
	return out
}

// explainText is the report as a person reads it: the issue the subject
// would have, then every episode's placement.
func explainText(doc *reviewDoc, s *subject) string {
	var b strings.Builder
	p := planner{doc: doc, open: map[string]string{}, evaluated: map[string]bool{}, closings: map[string]string{}, counts: map[string]int{}}
	p.subject(s)
	if len(p.wants) == 0 {
		fmt.Fprintf(&b, "%s: nothing raised.\n\n", s.Key)
	} else {
		w := p.wants[0]
		fmt.Fprintf(&b, "%s\n\n%s\n", w.Title, w.Body)
	}
	if s.Detail == nil {
		return b.String()
	}
	b.WriteString("Episodes (AniDB, kind, number, date: TVDB targets | TMDB targets):\n\n")
	for _, e := range s.Detail.Episodes {
		fmt.Fprintf(&b, "- %d %s %d %s: %s | %s\n", e.AniDB, e.Kind, e.Number, dash(e.AirDate), placementText(e.TVDB), placementText(e.TMDB))
	}
	return b.String()
}

func placementText(p *explainPlacement) string {
	if p == nil {
		return "no show"
	}
	if len(p.Targets) == 0 {
		return "nowhere"
	}
	parts := make([]string, 0, len(p.Targets))
	for _, t := range p.Targets {
		s := fmt.Sprintf("S%dE%d %s", t.Season, t.Number, dash(t.AirDate))
		switch {
		case t.Missing && t.Absolute > 0 && t.Number == 0:
			s = fmt.Sprintf("absolute %d missing", t.Absolute)
		case t.Missing:
			s += " missing"
		}
		if others := len(t.Claims); others > 1 {
			s += fmt.Sprintf(" (claimed %d times: %s)", others, strings.Join(t.Claims, ", "))
		}
		parts = append(parts, s)
	}
	if p.Default {
		parts[0] = "by default " + parts[0]
	}
	return strings.Join(parts, ", ")
}
