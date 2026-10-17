package mapread

import (
	"cmp"
	"slices"
	"sort"
)

// Index answers lookups over one decoded document. Every result is a
// copy, so a caller may change it freely.
type Index struct {
	doc     *Document
	anime   map[int]int
	entries map[int]int
	tvdb    map[int][]int
	tmdb    map[int][]int
	movies  map[int][]int
}

// TVDBEpisode is an episode of a TVDB series in official order.
type TVDBEpisode struct{ Series, Season, Episode int }

// TMDBEpisode is an episode of a TMDB show.
type TMDBEpisode struct{ Show, Season, Episode int }

// Ref is one AniDB episode: its anime, kind and number in that kind.
type Ref struct {
	Kind   Kind
	AniDB  int
	Number int
}

// AniDBEpisode is an AniDB episode the document names, with its AniDB
// episode id, 0 when AniDB's list does not hold it.
type AniDBEpisode struct {
	Ref
	ID int
}

// Landing is an AniDB episode found on a site episode; Default marks a
// placement that rests only on a node default.
type Landing struct {
	AniDBEpisode
	Default bool
}

// Listing is what AniDB's episode list says of an episode.
type Listing int

const (
	// ListingUnknown is an anime whose row carries no AniDB episode list.
	ListingUnknown Listing = iota
	// Listed is an episode AniDB lists, with its AniDB episode id.
	Listed
	// NotListed is a number AniDB's list holds no episode at.
	NotListed
)

// State is what a Statement says of an episode on one site.
type State int

const (
	// Unstated is a run that omits the site: the document states nothing.
	Unstated State = iota
	// Nowhere is an episode that lands nowhere on the site.
	Nowhere
	// Lands is an episode landing on the Statement's Targets.
	Lands
)

// Statement is what the document says about one episode on one site.
// Targets is non-empty when State is Lands, and nil otherwise.
type Statement struct {
	Targets []Target
	State   State
}

// Episode is an AniDB episode's statements, a site nil where the anime has
// no id on it, and what AniDB's list says of it: ID is its AniDB episode
// id when Listing is Listed, else 0.
type Episode struct {
	TVDB, TMDB *Statement
	Listing    Listing
	ID         int
}

// EntryEpisode is entry episode Number (from 1) and the AniDB episodes it
// is: one, or every regular episode of a combined part.
type EntryEpisode struct {
	Refs   []AniDBEpisode
	Number int
}

// Routes are the ids an entry reaches through its parts: the TVDB series
// and TMDB shows of every part's anime, and the TMDB movies and IMDb
// titles of the anime a whole-anime part names, each once in part order.
type Routes struct {
	TVDB       []int
	TMDBShows  []int
	TMDBMovies []int
	IMDb       []string
}

// SeriesSeasons is where an entry's episodes land on one TVDB series:
// each season with the entry episodes landing there, and how many land
// nowhere on it or have nothing stated.
type SeriesSeasons struct {
	Seasons  []SeasonEpisodes
	Series   int
	Nowhere  int
	Unstated int
}

// SeasonEpisodes are the entry episodes landing on one season, ascending,
// by their SeasonBasis there: Default when any of an entry episode's
// landings on the season rests on a default alone, else Stated.
type SeasonEpisodes struct {
	Stated  []int
	Default []int
	Season  int
}

// TVDBReading is where one entry episode lands on TVDB: on each series one
// of its AniDB episodes' anime is on, in the order they first name it, and
// Off when one of them is on no TVDB series.
type TVDBReading struct {
	Series []SeriesReading
	Off    bool
}

// SeriesReading is an entry episode on one TVDB series: the seasons it
// lands on, ascending; Unstated when it lands on none there and one of its
// AniDB episodes on the series has nothing stated, and it lands nowhere
// there when it lands on none and is not Unstated.
type SeriesReading struct {
	Seasons  []SeasonBasis
	Series   int
	Unstated bool
}

// SeasonBasis is a season an entry episode lands on: Default when any of
// its landings there rests on a default alone, so not Default means every
// one is stated.
type SeasonBasis struct {
	Season  int
	Default bool
}

// Span is the range of an AniDB anime's regular-episode landings on one
// TVDB series: First to Last, not every number between; Exact when every
// landing is stated, not a default.
type Span struct {
	First, Last TVDBEpisode
	AniDB       int
	Exact       bool
}

// NewIndex indexes doc, which must be non-nil and accepted by Decode; the
// Index then owns it, and the caller must not modify doc afterwards.
func NewIndex(doc *Document) *Index {
	x := &Index{doc: doc, anime: map[int]int{}, entries: map[int]int{}, tvdb: map[int][]int{}, tmdb: map[int][]int{}, movies: map[int][]int{}}
	for i := range doc.Anime {
		a := &doc.Anime[i]
		x.anime[a.AniDBID] = i
		if a.TVDBID > 0 {
			x.tvdb[a.TVDBID] = append(x.tvdb[a.TVDBID], i)
		}
		if a.TMDBTVID > 0 {
			x.tmdb[a.TMDBTVID] = append(x.tmdb[a.TMDBTVID], i)
		}
		for _, m := range a.TMDBMovieIDs {
			x.movies[m] = append(x.movies[m], a.AniDBID)
		}
	}
	for i := range doc.Entries {
		if al := doc.Entries[i].AniListID; al > 0 {
			x.entries[al] = i
		}
	}
	return x
}

// Entry is the entry of AniList id al.
func (x *Index) Entry(al int) (Entry, bool) {
	i, ok := x.entries[al]
	if !ok {
		return Entry{}, false
	}
	return copyEntry(&x.doc.Entries[i]), true
}

// Anime is the row of AniDB id aid.
func (x *Index) Anime(aid int) (Anime, bool) {
	a := x.row(aid)
	if a == nil {
		return Anime{}, false
	}
	return copyAnime(a), true
}

func (x *Index) row(aid int) *Anime {
	if i, ok := x.anime[aid]; ok {
		return &x.doc.Anime[i]
	}
	return nil
}

// Episode applies the reading rule to one AniDB episode. It is false when
// ref's kind is not one of the six or its number is below 1, when its
// anime has no row, or when the row carries no AniDB episode list and no
// run covers the number and it is past the kind's last. Otherwise, on each
// site the anime has an id for, a run covering it gives that run's
// statement, and no run means it lands nowhere; a covering run with ids
// makes it Listed, and else it is NotListed on a row with AniDB's list.
func (x *Index) Episode(ref Ref) (Episode, bool) {
	if ref.Kind.order() < 0 || ref.Number < 1 {
		return Episode{}, false
	}
	a := x.row(ref.AniDB)
	if a == nil {
		return Episode{}, false
	}
	r := covering(a.Episodes, ref.Kind, ref.Number)
	if r == nil && !a.EpisodesListed && ref.Number > a.Last.Of(ref.Kind) {
		return Episode{}, false
	}
	var ep Episode
	switch {
	case r != nil && r.AniDBEpisodeIDs != nil:
		ep.Listing, ep.ID = Listed, r.AniDBEpisodeIDs[ref.Number-r.Start]
	case a.EpisodesListed:
		ep.Listing = NotListed
	}
	if a.TVDBID > 0 {
		ep.TVDB = statement(r, func(r *Run) *[]Target { return r.TVDB }, ref.Number)
	}
	if a.TMDBTVID > 0 {
		ep.TMDB = statement(r, func(r *Run) *[]Target { return r.TMDB }, ref.Number)
	}
	return ep, true
}

// covering is the run of kind k covering n, nil for none.
func covering(runs []Run, k Kind, n int) *Run {
	o := k.order()
	i := sort.Search(len(runs), func(i int) bool {
		ro := runs[i].Kind.order()
		return ro > o || ro == o && runs[i].Start > n
	})
	if i == 0 {
		return nil
	}
	r := &runs[i-1]
	if r.Kind != k || n > r.End {
		return nil
	}
	return r
}

func statement(r *Run, site func(*Run) *[]Target, n int) *Statement {
	if r == nil {
		return &Statement{State: Nowhere}
	}
	ts := site(r)
	switch {
	case ts == nil:
		return &Statement{State: Unstated}
	case len(*ts) == 0:
		return &Statement{State: Nowhere}
	}
	s := &Statement{State: Lands, Targets: make([]Target, 0, len(*ts))}
	for _, t := range *ts {
		t.Episode += n - r.Start
		s.Targets = append(s.Targets, t)
	}
	return s
}

// Episodes expands e's parts into its episodes, in order.
func (x *Index) Episodes(e Entry) []EntryEpisode {
	var out []EntryEpisode
	for _, p := range e.Parts {
		a := x.row(p.AniDBID)
		if a == nil {
			continue
		}
		if p.Combined {
			if a.Last.Regular == 0 {
				continue
			}
			ep := EntryEpisode{Number: len(out) + 1}
			for k := 1; k <= a.Last.Regular; k++ {
				ep.Refs = append(ep.Refs, named(a, Regular, k))
			}
			out = append(out, ep)
			continue
		}
		from, to := p.Start, p.End
		if from == 0 {
			from, to = 1, a.Last.Of(p.Kind)
		}
		for k := from; k <= to; k++ {
			out = append(out, EntryEpisode{Number: len(out) + 1, Refs: []AniDBEpisode{named(a, p.Kind, k)}})
		}
	}
	return out
}

// named is a's episode n of kind k with its AniDB episode id, if listed.
func named(a *Anime, k Kind, n int) AniDBEpisode {
	ep := AniDBEpisode{AniDB: a.AniDBID, Kind: k, Number: n}
	if r := covering(a.Episodes, k, n); r != nil && r.AniDBEpisodeIDs != nil {
		ep.ID = r.AniDBEpisodeIDs[n-r.Start]
	}
	return ep
}

// Routes are the ids e reaches through its parts.
func (x *Index) Routes(e Entry) Routes {
	var r Routes
	for _, p := range e.Parts {
		a := x.row(p.AniDBID)
		if a == nil {
			continue
		}
		r.TVDB = appendNew(r.TVDB, a.TVDBID)
		r.TMDBShows = appendNew(r.TMDBShows, a.TMDBTVID)
		if p.Identity() {
			r.addFilm(a)
		}
	}
	return r
}

// addFilm adds the ids that name the whole of a.
func (r *Routes) addFilm(a *Anime) {
	for _, m := range a.TMDBMovieIDs {
		r.TMDBMovies = appendNew(r.TMDBMovies, m)
	}
	for _, id := range a.IMDbIDs {
		if !slices.Contains(r.IMDb, id) {
			r.IMDb = append(r.IMDb, id)
		}
	}
}

func appendNew(list []int, v int) []int {
	if v <= 0 || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// TVDBSeasons groups e's episodes per TVDB series and season by their
// OnTVDB reading: an entry episode counts once per season it lands on,
// and on a series where it lands on none, as unstated or as nowhere.
func (x *Index) TVDBSeasons(e Entry) []SeriesSeasons {
	out := []SeriesSeasons{}
	at := map[int]int{}
	for _, ep := range x.Episodes(e) {
		for _, sr := range x.OnTVDB(ep).Series {
			i, ok := at[sr.Series]
			if !ok {
				i = len(out)
				at[sr.Series] = i
				out = append(out, SeriesSeasons{Series: sr.Series})
			}
			out[i].add(sr, ep.Number)
		}
	}
	return out
}

func (ss *SeriesSeasons) add(sr SeriesReading, episode int) {
	switch {
	case len(sr.Seasons) == 0 && sr.Unstated:
		ss.Unstated++
	case len(sr.Seasons) == 0:
		ss.Nowhere++
	}
	for _, b := range sr.Seasons {
		i, found := slices.BinarySearchFunc(ss.Seasons, b.Season, func(se SeasonEpisodes, season int) int { return cmp.Compare(se.Season, season) })
		if !found {
			ss.Seasons = slices.Insert(ss.Seasons, i, SeasonEpisodes{Season: b.Season})
		}
		if se := &ss.Seasons[i]; b.Default {
			se.Default = append(se.Default, episode)
		} else {
			se.Stated = append(se.Stated, episode)
		}
	}
}

// OnTVDB reads every AniDB episode of ep on TVDB, the one aggregate of an
// entry episode's landings that TVDBSeasons and other readers derive from.
// ep comes from Episodes; a ref the document does not hold reads as Off.
func (x *Index) OnTVDB(ep EntryEpisode) TVDBReading {
	var r TVDBReading
	at := map[int]int{}
	for _, ref := range ep.Refs {
		st, _ := x.Episode(ref.Ref)
		if st.TVDB == nil {
			r.Off = true
			continue
		}
		series := x.row(ref.AniDB).TVDBID
		i, ok := at[series]
		if !ok {
			i = len(r.Series)
			at[series] = i
			r.Series = append(r.Series, SeriesReading{Series: series})
		}
		r.Series[i].add(st.TVDB)
	}
	for i := range r.Series {
		if len(r.Series[i].Seasons) > 0 {
			r.Series[i].Unstated = false
		}
	}
	return r
}

func (sr *SeriesReading) add(st *Statement) {
	sr.Unstated = sr.Unstated || st.State == Unstated
	for _, t := range st.Targets {
		i, found := slices.BinarySearchFunc(sr.Seasons, t.Season, func(b SeasonBasis, season int) int { return cmp.Compare(b.Season, season) })
		if !found {
			sr.Seasons = slices.Insert(sr.Seasons, i, SeasonBasis{Season: t.Season})
		}
		sr.Seasons[i].Default = sr.Seasons[i].Default || t.Default
	}
}

// AtTVDB is every AniDB episode landing on at, stated or by default, by
// AniDB id, kind and number. Two or more is an ambiguity the caller sees.
func (x *Index) AtTVDB(at TVDBEpisode) []Landing {
	return x.landings(x.tvdb[at.Series], func(r *Run) *[]Target { return r.TVDB }, at.Season, at.Episode)
}

// AtTMDB is every AniDB episode landing on at, as AtTVDB.
func (x *Index) AtTMDB(at TMDBEpisode) []Landing {
	return x.landings(x.tmdb[at.Show], func(r *Run) *[]Target { return r.TMDB }, at.Season, at.Episode)
}

func (x *Index) landings(rows []int, site func(*Run) *[]Target, season, episode int) []Landing {
	var out []Landing
	for _, i := range rows {
		a := &x.doc.Anime[i]
		for j := range a.Episodes {
			if ts := site(&a.Episodes[j]); ts != nil {
				out = runLandings(out, a.AniDBID, &a.Episodes[j], *ts, season, episode)
			}
		}
	}
	slices.SortStableFunc(out, func(p, q Landing) int {
		return cmp.Or(cmp.Compare(p.AniDB, q.AniDB), cmp.Compare(p.Kind.order(), q.Kind.order()), cmp.Compare(p.Number, q.Number))
	})
	return out
}

// runLandings appends the episodes of anime aid's run r whose targets ts
// land on season and episode.
func runLandings(out []Landing, aid int, r *Run, ts []Target, season, episode int) []Landing {
	for _, t := range ts {
		if off := episode - t.Episode; t.Season == season && off >= 0 && off <= r.End-r.Start {
			l := Landing{AniDB: aid, Kind: r.Kind, Number: r.Start + off, Default: t.Default}
			if r.AniDBEpisodeIDs != nil {
				l.ID = r.AniDBEpisodeIDs[off]
			}
			out = append(out, l)
		}
	}
	return out
}

// TMDBMovie is every AniDB anime naming TMDB movie id, ascending.
func (x *Index) TMDBMovie(id int) []int {
	return slices.Clone(x.movies[id])
}

// Series lists the AniDB anime whose regular episodes land on TVDB series,
// in TVDB order: by first landing, default targets included.
func (x *Index) Series(series int) []Span {
	var out []Span
	for _, i := range x.tvdb[series] {
		if sp, ok := span(&x.doc.Anime[i], series); ok {
			out = append(out, sp)
		}
	}
	slices.SortStableFunc(out, func(a, b Span) int {
		return cmp.Or(cmp.Compare(a.First.Season, b.First.Season), cmp.Compare(a.First.Episode, b.First.Episode), cmp.Compare(a.AniDB, b.AniDB))
	})
	return out
}

// span is where a's regular episodes land on series, false for nowhere.
func span(a *Anime, series int) (Span, bool) {
	sp := Span{AniDB: a.AniDBID, Exact: true}
	found := false
	for j := range a.Episodes {
		r := &a.Episodes[j]
		if r.Kind != Regular || r.TVDB == nil {
			continue
		}
		for _, t := range *r.TVDB {
			first := TVDBEpisode{Series: series, Season: t.Season, Episode: t.Episode}
			last := TVDBEpisode{Series: series, Season: t.Season, Episode: t.Episode + r.End - r.Start}
			if !found || episodeBefore(first, sp.First) {
				sp.First = first
			}
			if !found || episodeBefore(sp.Last, last) {
				sp.Last = last
			}
			found = true
			sp.Exact = sp.Exact && !t.Default
		}
	}
	return sp, found
}

func episodeBefore(a, b TVDBEpisode) bool {
	return a.Season < b.Season || a.Season == b.Season && a.Episode < b.Episode
}

func copyEntry(e *Entry) Entry {
	c := *e
	c.Parts = slices.Clone(e.Parts)
	if e.IDs != nil {
		ids := *e.IDs
		ids.AniDB, ids.AnimeCountdown, ids.AniSearch, ids.ANN = slices.Clone(ids.AniDB), slices.Clone(ids.AnimeCountdown), slices.Clone(ids.AniSearch), slices.Clone(ids.ANN)
		ids.Kitsu, ids.LiveChart, ids.MAL, ids.Simkl = slices.Clone(ids.Kitsu), slices.Clone(ids.LiveChart), slices.Clone(ids.MAL), slices.Clone(ids.Simkl)
		ids.AnimePlanet, ids.NotifyMoe = slices.Clone(ids.AnimePlanet), slices.Clone(ids.NotifyMoe)
		c.IDs = &ids
	}
	return c
}

func copyAnime(a *Anime) Anime {
	c := *a
	c.TMDBMovieIDs, c.IMDbIDs = slices.Clone(a.TMDBMovieIDs), slices.Clone(a.IMDbIDs)
	c.Episodes = make([]Run, len(a.Episodes))
	for i, r := range a.Episodes {
		r.TVDB, r.TMDB = copyTargets(r.TVDB), copyTargets(r.TMDB)
		r.AniDBEpisodeIDs = slices.Clone(r.AniDBEpisodeIDs)
		c.Episodes[i] = r
	}
	if a.Episodes == nil {
		c.Episodes = nil
	}
	return c
}

func copyTargets(ts *[]Target) *[]Target {
	if ts == nil {
		return nil
	}
	c := slices.Clone(*ts)
	if c == nil {
		c = []Target{}
	}
	return &c
}
