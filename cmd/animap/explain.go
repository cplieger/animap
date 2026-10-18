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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/guard"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/animap/internal/tmdb"
	"github.com/cplieger/animap/mapread"
)

// explainDetail is what a reviewer needs beyond the sections: every AniDB
// episode of the subject's nodes and candidates with where its node puts
// it; every show they map to with its nodes and claims; the published
// entries the subject watches with anime-offline-database's AniDB ids for
// them, and the anime rows of its nodes and candidates; and the overlay
// data and acceptance record bearing on it. Candidates are the AniDB ids
// those entries and the database name that the subject's nodes are not.
// Fetched says when each show's layout was read.
type explainDetail struct {
	Entry      *overlay.Entry    `json:"entry,omitempty"`
	Acceptance *review.Record    `json:"acceptance,omitempty"`
	Fetched    map[string]string `json:"fetched,omitempty"`
	AOD        map[int][]int     `json:"aod,omitempty"`
	Episodes   []explainEpisode  `json:"episodes"`
	Links      []overlay.Link    `json:"links,omitempty"`
	Entries    []mapread.Entry   `json:"entries,omitempty"`
	Anime      []mapread.Anime   `json:"anime,omitempty"`
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
// promotion, parody or other, Number AniDB's within that kind, and
// SeasonZero the Anime-Lists season 0 number of a special or extra, 0 for
// an extra none addresses; Titles the digest of its AniDB titles.
type explainEpisode struct {
	TVDB       *explainPlacement `json:"tvdb,omitempty"`
	TMDB       *explainPlacement `json:"tmdb,omitempty"`
	Kind       string            `json:"kind"`
	AirDate    string            `json:"air_date,omitempty"`
	Titles     string            `json:"titles,omitempty"`
	AniDB      int               `json:"anidb"`
	Number     int               `json:"number"`
	SeasonZero int               `json:"season_zero,omitempty"`
}

// explainPlacement is where the node puts one episode on Show.
// Unaddressable is an extra no Anime-Lists number addresses: no row can
// place it, so it lands nowhere, as the published anime row says.
type explainPlacement struct {
	Targets       []explainTarget `json:"targets"`
	Show          int             `json:"show"`
	Absolute      int             `json:"absolute,omitempty"`
	Default       bool            `json:"default,omitempty"`
	Unaddressable bool            `json:"unaddressable,omitempty"`
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
	for _, l := range r.applied.Links() {
		if l.AniListID == sub.ID {
			out = append(out, l.AniDBIDs()...)
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
	d.Entries, d.AOD = r.watchedEntries(sub)
	d.Candidates = candidates(d.Entries, d.AOD, own)
	ids := slices.Concat(own, d.Candidates)
	d.Anime = r.animeRows(ids)
	for i := range r.entries {
		if sub.AniDB() && r.entries[i].AniDBID == sub.ID {
			d.Entry = &r.entries[i]
		}
	}
	for _, l := range r.applied.Links() {
		if l.AniListID == sub.ID && !sub.AniDB() || sub.AniDB() && slices.Contains(l.AniDBIDs(), sub.ID) {
			d.Links = append(d.Links, *l)
		}
	}
	claims := r.claimsOf(ids)
	for _, aid := range ids {
		d.Episodes = append(d.Episodes, r.episodes(aid, claims, d.Fetched)...)
	}
	d.Shows = r.shows(claims)
	return d
}

// animeRows are the published rows of ids, by AniDB id.
func (r *reviewRun) animeRows(ids []int) []mapread.Anime {
	if r.index == nil {
		return nil
	}
	var out []mapread.Anime
	for _, aid := range slices.Sorted(slices.Values(ids)) {
		if a, ok := r.index.Anime(aid); ok {
			out = append(out, a)
		}
	}
	return out
}

// watchedEntries are the published entries of the subject: the entry of
// an AniList subject, every AniList entry an AniDB subject's node is; and,
// by AniList id, the AniDB ids anime-offline-database gives each of them,
// an AniList subject with no entry included.
func (r *reviewRun) watchedEntries(sub review.Subject) (entries []mapread.Entry, aod map[int][]int) {
	if r.release != nil {
		for i := range r.release.Entries {
			e := &r.release.Entries[i]
			if sub.AniDB() && e.AniListID > 0 && identityOf(e) == sub.ID || !sub.AniDB() && e.AniListID == sub.ID {
				entries = append(entries, *e)
			}
		}
	}
	als := []int{}
	if !sub.AniDB() {
		als = append(als, sub.ID)
	}
	for i := range entries {
		als = append(als, entries[i].AniListID)
	}
	aod = map[int][]int{}
	for _, al := range als {
		if ids := r.aod[al]; len(ids) > 0 {
			aod[al] = ids
		}
	}
	return entries, aod
}

// identityOf is the AniDB anime entry e is, 0 when its parts are not one
// whole anime.
func identityOf(e *mapread.Entry) int {
	if p := e.Parts; len(p) == 1 && p[0].Identity() {
		return p[0].AniDBID
	}
	return 0
}

// candidates are the AniDB ids entries' parts and AniDB ids and aod name
// that own does not hold.
func candidates(entries []mapread.Entry, aod map[int][]int, own []int) []int {
	var out []int
	for i := range entries {
		for _, p := range entries[i].Parts {
			out = append(out, p.AniDBID)
		}
		if ids := entries[i].IDs; ids != nil {
			out = append(out, ids.AniDB...)
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

// claimsOf is the collision guard's claims on every show the nodes map
// to, with the links the build applies.
func (r *reviewRun) claimsOf(ids []int) map[claimKey]map[[2]int][]string {
	out := map[claimKey]map[[2]int][]string{}
	if r.facts == nil {
		return out
	}
	for _, aid := range ids {
		n := r.patched[aid]
		if n == nil {
			continue
		}
		for _, side := range []animelists.Side{animelists.TVDB, animelists.TMDB} {
			if s, ok := datecheck.Series(n, side); ok {
				k := claimKey{side: side, show: s}
				out[k] = r.claimsOn(side, s)
			}
		}
	}
	return out
}

// claimsOn is the collision guard's claims on show on side, with the
// links the build applies; the mirror must have been read.
func (r *reviewRun) claimsOn(side animelists.Side, show int) map[[2]int][]string {
	k := claimKey{side: side, show: show}
	if c, ok := r.claimCache[k]; ok {
		return c
	}
	if r.claimIndex == nil {
		r.claimIndex = guard.NewClaimIndex(r.list.Nodes, r.entries, r.applied, r.facts)
		r.claimCache = map[claimKey]map[[2]int][]string{}
	}
	r.claimCache[k] = r.claimIndex.Claims(side, show)
	return r.claimCache[k]
}

// episodes lists every AniDB episode of node aid with its placements.
func (r *reviewRun) episodes(aid int, claims map[claimKey]map[[2]int][]string, fetched map[string]string) []explainEpisode {
	n := r.patched[aid]
	if n == nil || r.facts == nil {
		return nil
	}
	a, _ := r.facts.Anime(aid)
	var out []explainEpisode
	for _, e := range a.Regular {
		out = append(out, explainEpisode{AniDB: aid, Kind: string(mapread.Regular), Number: e.Number, AirDate: e.AirDate, Titles: e.Titles})
	}
	for _, sp := range a.Specials {
		out = append(out, explainEpisode{AniDB: aid, Kind: string(mapread.Special), Number: sp.Number, SeasonZero: sp.Number, AirDate: sp.AirDate, Titles: sp.Titles})
	}
	for _, x := range a.Extras {
		out = append(out, explainEpisode{AniDB: aid, Kind: x.Kind, Number: x.Number, SeasonZero: x.SeasonZero, Titles: x.Titles})
	}
	for i := range out {
		season, k := 1, out[i].Number
		if out[i].Kind != string(mapread.Regular) {
			season, k = 0, out[i].SeasonZero
		}
		out[i].TVDB = r.placement(n, animelists.TVDB, season, k, claims, fetched)
		out[i].TMDB = r.placement(n, animelists.TMDB, season, k, claims, fetched)
	}
	return out
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
	if k < 1 {
		return &explainPlacement{Show: show, Targets: []explainTarget{}, Unaddressable: true}
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
		number := strconv.Itoa(e.Number)
		if e.Kind != string(mapread.Regular) && e.Kind != string(mapread.Special) && e.SeasonZero > 0 {
			number += fmt.Sprintf(" (S0 %d)", e.SeasonZero)
		}
		fmt.Fprintf(&b, "- %d %s %s %s: %s | %s\n", e.AniDB, e.Kind, number, dash(e.AirDate), placementText(e.TVDB), placementText(e.TMDB))
	}
	return b.String()
}

func placementText(p *explainPlacement) string {
	if p == nil {
		return "no show"
	}
	if p.Unaddressable {
		return "nowhere, no Anime-Lists number"
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
