package mapread

import (
	"cmp"
	"iter"
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
	movies  map[int][]int
	tvdbIDs map[int]TVDBEpisode
}

// TVDBEpisode is an episode of a TVDB series in official order.
type TVDBEpisode struct{ Series, Season, Episode int }

// Placement is a TVDB episode an AniDB episode lands on, as an Index
// answers it: its series resolved, never 0; ID its TVDB episode id, 0 when
// the document does not hold it; and Default when it rests only on a
// default, not on a stated placement.
type Placement struct {
	TVDBEpisode
	ID      int
	Default bool
}

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

// Landing is an AniDB episode found on a TVDB episode or a TMDB movie;
// Default marks a TVDB placement that rests only on a default.
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

// State is what a Statement says of an episode on TVDB.
type State int

const (
	// Unstated is a run that omits tvdb: the document states nothing.
	Unstated State = iota
	// Nowhere is an episode that lands nowhere on TVDB.
	Nowhere
	// Lands is an episode landing on the Statement's Placements.
	Lands
)

// Statement is what the document says about one episode on TVDB.
// Placements is non-empty when State is Lands, and nil otherwise.
type Statement struct {
	Placements []Placement
	State      State
}

// Episode is an AniDB episode's TVDB statement, nil when it is off TVDB,
// the TMDB movies it is, nil for none stated, and what AniDB's list says
// of it: ID is its AniDB episode id when Listing is Listed, else 0.
type Episode struct {
	TVDB       *Statement
	TMDBMovies []int
	Listing    Listing
	ID         int
}

// EntryEpisode is entry episode Number (from 1) and the AniDB episodes it
// is: one, or every regular episode of a combined part.
type EntryEpisode struct {
	Refs   []AniDBEpisode
	Number int
}

// Routes are the ids an entry reaches through its parts, each once in part
// order. TVDB is, per part, the series its episodes land on in landing
// order, or its anime's TVDBID when none of them lands. TMDBMovies is the
// one list every episode of the entry states, the work the entry wholly
// is, and empty when an episode states none or another list. IMDb is the
// IMDb ids of each anime a part names whole (Part.Identity).
type Routes struct {
	TVDB       []int
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
// of its AniDB episodes lands on or whose anime's TVDBID it is, in the
// order they first name it; Off when one of them is off TVDB; and
// Unstated when one of them has nothing stated on TVDB, its Statement
// Unstated. On reads it on any one series.
type TVDBReading struct {
	Series   []SeriesReading
	Off      bool
	Unstated bool
}

// On is r on series: its reading there when it has one, and else one
// landing on no season there, Unstated when r is, so an episode with
// nothing stated is never read as fully stated on a series it names none
// of.
func (r TVDBReading) On(series int) SeriesReading {
	for _, sr := range r.Series {
		if sr.Series == series {
			sr.Seasons = slices.Clone(sr.Seasons)
			return sr
		}
	}
	return SeriesReading{Series: series, Unstated: r.Unstated}
}

// SeriesReading is an entry episode on one TVDB series: the seasons it
// lands on, ascending; Unstated when it lands on none there and one of its
// AniDB episodes has nothing stated on TVDB, and it lands nowhere there
// when it lands on none and is not Unstated.
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
	x := &Index{doc: doc, anime: map[int]int{}, entries: map[int]int{}, tvdb: map[int][]int{}, movies: map[int][]int{}, tvdbIDs: map[int]TVDBEpisode{}}
	for i := range doc.Anime {
		x.addAnime(i)
	}
	for i := range doc.Entries {
		x.entries[doc.Entries[i].AniListID] = i
	}
	return x
}

// addAnime indexes row i under its id, each series it may land on, each
// TVDB episode id its targets give and each movie its runs state.
func (x *Index) addAnime(i int) {
	a := &x.doc.Anime[i]
	x.anime[a.AniDBID] = i
	for _, s := range a.TVDBSeries() {
		x.tvdb[s] = append(x.tvdb[s], i)
	}
	for ep, id := range tvdbEpisodeIDs(a) {
		x.tvdbIDs[id] = ep
	}
	for j := range a.Episodes {
		for _, m := range a.Episodes[j].TMDBMovies {
			if rows := x.movies[m]; len(rows) == 0 || rows[len(rows)-1] != i {
				x.movies[m] = append(rows, i)
			}
		}
	}
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
// run covers the number and it is past the kind's last. Otherwise a
// covering run gives its TVDB statement and movies, and no run means it
// lands nowhere on TVDB and is no movie; an anime with no TVDBID is on
// TVDB only where a covering run lands. A covering run with ids makes it
// Listed, and else it is NotListed on a row with AniDB's list.
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
	ep := Episode{TVDB: tvdbStatement(a, r, ref.Number)}
	switch {
	case r != nil && r.AniDBEpisodeIDs != nil:
		ep.Listing, ep.ID = Listed, r.AniDBEpisodeIDs[ref.Number-r.Start]
	case a.EpisodesListed:
		ep.Listing = NotListed
	}
	if r != nil {
		ep.TMDBMovies = slices.Clone(r.TMDBMovies)
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

// tvdbStatement is what r, nil for none, says of a's episode n on TVDB,
// each placement on its series with its TVDB episode id; nil is off TVDB.
func tvdbStatement(a *Anime, r *Run, n int) *Statement {
	switch {
	case a.TVDBID == 0 && (r == nil || r.TVDB == nil):
		return nil
	case r == nil:
		return &Statement{State: Nowhere}
	case r.TVDB == nil:
		return &Statement{State: Unstated}
	case len(*r.TVDB) == 0:
		return &Statement{State: Nowhere}
	}
	off := n - r.Start
	s := &Statement{State: Lands, Placements: make([]Placement, 0, len(*r.TVDB))}
	for _, t := range *r.TVDB {
		p := Placement{TVDBEpisode: TVDBEpisode{t.on(a.TVDBID), t.Season, t.Episode + off}, Default: t.Default}
		if t.EpisodeIDs != nil {
			p.ID = t.EpisodeIDs[off]
		}
		s.Placements = append(s.Placements, p)
	}
	return s
}

// partRange is the kind and the numbers from to to that p names on a: a
// combined part names every regular episode, and a part with no range 1 to
// the kind's last.
func partRange(a *Anime, p Part) (k Kind, from, to int) {
	switch {
	case p.Combined:
		return Regular, 1, a.Last.Regular
	case p.Start > 0:
		return p.Kind, p.Start, p.End
	}
	return p.Kind, 1, a.Last.Of(p.Kind)
}

// Episodes expands e's parts into its episodes, in order.
func (x *Index) Episodes(e Entry) []EntryEpisode {
	var out []EntryEpisode
	for _, p := range e.Parts {
		a := x.row(p.AniDBID)
		if a == nil {
			continue
		}
		k, from, to := partRange(a, p)
		if p.Combined {
			if to == 0 {
				continue
			}
			ep := EntryEpisode{Number: len(out) + 1}
			for n := from; n <= to; n++ {
				ep.Refs = append(ep.Refs, named(a, k, n))
			}
			out = append(out, ep)
			continue
		}
		for n := from; n <= to; n++ {
			out = append(out, EntryEpisode{Number: len(out) + 1, Refs: []AniDBEpisode{named(a, k, n)}})
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
	var movies agreement
	for _, p := range e.Parts {
		a := x.row(p.AniDBID)
		if a == nil {
			continue
		}
		r.addPart(a, p, &movies)
		if p.Identity() {
			for _, id := range a.IMDbIDs {
				if !slices.Contains(r.IMDb, id) {
					r.IMDb = append(r.IMDb, id)
				}
			}
		}
	}
	r.TMDBMovies = movies.list()
	return r
}

// addPart adds the series p's episodes land on, else a's TVDBID, and
// gives m each episode's movies.
func (r *Routes) addPart(a *Anime, p Part, m *agreement) {
	k, from, to := partRange(a, p)
	landed := false
	for n := from; n <= to; n++ {
		run := covering(a.Episodes, k, n)
		if run == nil {
			m.add(nil)
			continue
		}
		m.add(run.TMDBMovies)
		if run.TVDB != nil {
			for _, t := range *run.TVDB {
				r.TVDB, landed = appendNew(r.TVDB, t.on(a.TVDBID)), true
			}
		}
	}
	if !landed {
		r.TVDB = appendNew(r.TVDB, a.TVDBID)
	}
}

// agreement is the one movie list every episode added states. A union
// would hand a reader distinct works as if they were aliases of one.
type agreement struct {
	movies []int
	seen   bool
	split  bool
}

func (g *agreement) add(movies []int) {
	switch {
	case !g.seen:
		g.movies, g.seen = movies, true
	case !slices.Equal(g.movies, movies):
		g.split = true
	}
}

// list is the agreed list, nil when no episode was added or two differ.
func (g *agreement) list() []int {
	if g.split {
		return nil
	}
	return slices.Clone(g.movies)
}

// Movies is the TMDB movies of e's episodes: one list per distinct work,
// that is per distinct list an episode states, in episode order. An
// episode stating none adds nothing.
func (x *Index) Movies(e Entry) [][]int {
	var out [][]int
	for _, p := range e.Parts {
		a := x.row(p.AniDBID)
		if a == nil {
			continue
		}
		k, from, to := partRange(a, p)
		for n := from; n <= to; n++ {
			r := covering(a.Episodes, k, n)
			if r == nil || r.TMDBMovies == nil || slices.ContainsFunc(out, func(l []int) bool { return slices.Equal(l, r.TMDBMovies) }) {
				continue
			}
			out = append(out, slices.Clone(r.TMDBMovies))
		}
	}
	return out
}

// TVDBSeasons groups e's episodes per TVDB series and season by their
// OnTVDB reading, on each series one of them names, in the order they
// first name it. Every entry episode counts on every such series, read as
// TVDBReading.On reads it there: once per season it lands on, and where
// it lands on none, as unstated or as nowhere.
func (x *Index) TVDBSeasons(e Entry) []SeriesSeasons {
	out := []SeriesSeasons{}
	at := map[int]int{}
	var all tally
	var landed []tally
	for _, ep := range x.Episodes(e) {
		r := x.OnTVDB(ep)
		all.add(r.Unstated)
		for _, sr := range r.Series {
			i, ok := at[sr.Series]
			if !ok {
				i = len(out)
				at[sr.Series] = i
				out = append(out, SeriesSeasons{Series: sr.Series})
				landed = append(landed, tally{})
			}
			if len(sr.Seasons) > 0 {
				landed[i].add(r.Unstated)
				out[i].add(sr, ep.Number)
			}
		}
	}
	for i := range out {
		out[i].Nowhere, out[i].Unstated = all.stated-landed[i].stated, all.unstated-landed[i].unstated
	}
	return out
}

// tally counts entry episodes apart by whether one of their AniDB episodes
// has nothing stated. TVDBReading.On reads one landing on no season of a
// series as unstated there exactly when it has, so a series' unstated and
// nowhere counts are the entry's tallies less the episodes landing there.
type tally struct{ stated, unstated int }

func (t *tally) add(unstated bool) {
	if unstated {
		t.unstated++
	} else {
		t.stated++
	}
}

func (ss *SeriesSeasons) add(sr SeriesReading, episode int) {
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
// Each placement counts on its series, and each AniDB episode of an anime
// with a TVDBID also reads on that series, so one landing only elsewhere
// lands on no season there. ep comes from Episodes; a ref the document
// does not hold reads as Off.
func (x *Index) OnTVDB(ep EntryEpisode) TVDBReading {
	var r TVDBReading
	at := map[int]int{}
	for _, ref := range ep.Refs {
		st, _ := x.Episode(ref.Ref)
		if st.TVDB == nil {
			r.Off = true
			continue
		}
		r.Unstated = r.Unstated || st.TVDB.State == Unstated
		if a := x.row(ref.AniDB); a.TVDBID > 0 {
			r.reading(at, a.TVDBID)
		}
		for _, p := range st.TVDB.Placements {
			r.reading(at, p.Series).add(p)
		}
	}
	for i := range r.Series {
		r.Series[i].Unstated = len(r.Series[i].Seasons) == 0 && r.Unstated
	}
	return r
}

// reading is r's reading on series, added last when r has none; at maps
// each series to its reading's index.
func (r *TVDBReading) reading(at map[int]int, series int) *SeriesReading {
	i, ok := at[series]
	if !ok {
		i = len(r.Series)
		at[series] = i
		r.Series = append(r.Series, SeriesReading{Series: series})
	}
	return &r.Series[i]
}

func (sr *SeriesReading) add(p Placement) {
	i, found := slices.BinarySearchFunc(sr.Seasons, p.Season, func(b SeasonBasis, season int) int { return cmp.Compare(b.Season, season) })
	if !found {
		sr.Seasons = slices.Insert(sr.Seasons, i, SeasonBasis{Season: p.Season})
	}
	sr.Seasons[i].Default = sr.Seasons[i].Default || p.Default
}

// AtTVDB is every AniDB episode landing on at, stated or by default, by
// AniDB id, kind and number. Two or more is an ambiguity the caller sees.
func (x *Index) AtTVDB(at TVDBEpisode) []Landing {
	var out []Landing
	for _, i := range x.tvdb[at.Series] {
		a := &x.doc.Anime[i]
		for j := range a.Episodes {
			out = runLandings(out, a, &a.Episodes[j], at)
		}
	}
	slices.SortStableFunc(out, func(p, q Landing) int {
		return cmp.Or(cmp.Compare(p.AniDB, q.AniDB), cmp.Compare(p.Kind.order(), q.Kind.order()), cmp.Compare(p.Number, q.Number))
	})
	return out
}

// runLandings appends the episodes of a's run r whose targets land on at.
func runLandings(out []Landing, a *Anime, r *Run, at TVDBEpisode) []Landing {
	if r.TVDB == nil {
		return out
	}
	for _, t := range *r.TVDB {
		if off := at.Episode - t.Episode; t.on(a.TVDBID) == at.Series && t.Season == at.Season && off >= 0 && off <= r.End-r.Start {
			out = append(out, Landing{AniDBEpisode: episodeAt(a.AniDBID, r, off), Default: t.Default})
		}
	}
	return out
}

// episodeAt is anime aid's episode off places into r, with its AniDB
// episode id when r lists one.
func episodeAt(aid int, r *Run, off int) AniDBEpisode {
	ep := AniDBEpisode{AniDB: aid, Kind: r.Kind, Number: r.Start + off}
	if r.AniDBEpisodeIDs != nil {
		ep.ID = r.AniDBEpisodeIDs[off]
	}
	return ep
}

// AtTVDBEpisodeID is every AniDB episode landing on the TVDB episode of id,
// as AtTVDB finds them there, whether or not each placement holds the id;
// nil when no target gives id. A reader holding a TVDB episode by id, in
// whatever order its series is numbered, reaches the official one here.
func (x *Index) AtTVDBEpisodeID(id int) []Landing {
	ep, ok := x.tvdbIDs[id]
	if !ok {
		return nil
	}
	return x.AtTVDB(ep)
}

// tvdbEpisodeIDs yields each TVDB episode a's targets give an id, with it.
func tvdbEpisodeIDs(a *Anime) iter.Seq2[TVDBEpisode, int] {
	return func(yield func(TVDBEpisode, int) bool) {
		for i := range a.Episodes {
			for _, t := range a.Episodes[i].targets() {
				for off, id := range t.EpisodeIDs {
					if !yield(TVDBEpisode{t.on(a.TVDBID), t.Season, t.Episode + off}, id) {
						return
					}
				}
			}
		}
	}
}

// targets is r's TVDB targets, none when it states nothing there.
func (r *Run) targets() []Target {
	if r.TVDB == nil {
		return nil
	}
	return *r.TVDB
}

// AtTMDBMovie is every AniDB episode stated to be TMDB movie id, by AniDB
// id, kind and number.
func (x *Index) AtTMDBMovie(id int) []Landing {
	var out []Landing
	for _, i := range x.movies[id] {
		a := &x.doc.Anime[i]
		for j := range a.Episodes {
			r := &a.Episodes[j]
			if _, ok := slices.BinarySearch(r.TMDBMovies, id); !ok {
				continue
			}
			for off := range r.End - r.Start + 1 {
				out = append(out, Landing{AniDBEpisode: episodeAt(a.AniDBID, r, off)})
			}
		}
	}
	return out
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
			if t.on(a.TVDBID) == series {
				sp.widen(TVDBEpisode{series, t.Season, t.Episode}, r.End-r.Start, t.Default, !found)
				found = true
			}
		}
	}
	return sp, found
}

// widen takes in the landings from first to more episodes after it, or
// only them when fresh.
func (sp *Span) widen(first TVDBEpisode, more int, byDefault, fresh bool) {
	last := first
	last.Episode += more
	if fresh || episodeBefore(first, sp.First) {
		sp.First = first
	}
	if fresh || episodeBefore(sp.Last, last) {
		sp.Last = last
	}
	sp.Exact = sp.Exact && !byDefault
}

func episodeBefore(a, b TVDBEpisode) bool {
	return a.Season < b.Season || a.Season == b.Season && a.Episode < b.Episode
}

func appendNew(list []int, v int) []int {
	if v <= 0 || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func copyEntry(e *Entry) Entry {
	c := *e
	c.Parts = slices.Clone(e.Parts)
	if e.IDs != nil {
		c.IDs = &IDs{AniDB: slices.Clone(e.IDs.AniDB), MAL: slices.Clone(e.IDs.MAL)}
	}
	return c
}

func copyAnime(a *Anime) Anime {
	c := *a
	c.MALIDs, c.IMDbIDs = slices.Clone(a.MALIDs), slices.Clone(a.IMDbIDs)
	c.Episodes = make([]Run, len(a.Episodes))
	for i, r := range a.Episodes {
		r.TVDB = copyTargets(r.TVDB)
		r.AniDBEpisodeIDs, r.TMDBMovies = slices.Clone(r.AniDBEpisodeIDs), slices.Clone(r.TMDBMovies)
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
	for i := range c {
		c[i].EpisodeIDs = slices.Clone(c[i].EpisodeIDs)
	}
	return &c
}
