package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/animap/internal/anidb"
	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/datecheck"
	"github.com/cplieger/animap/internal/drift"
	"github.com/cplieger/animap/internal/offlinedb"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/review"
	"github.com/cplieger/animap/internal/schema"
	"github.com/cplieger/animap/internal/skyhook"
	"github.com/cplieger/animap/internal/source"
	"github.com/cplieger/animap/internal/tmdb"
	"github.com/cplieger/animap/internal/watch"
)

// tmdbName names TMDB in logs, reasons and issues.
const tmdbName = "TMDB"

// tmdbPace is a tenth of TMDB's ceiling of about 40 requests a second
// (https://developer.themoviedb.org/docs/rate-limiting).
const tmdbPace = 250 * time.Millisecond

// reviewDoc is review.json: what one run says about every subject that has
// something to say, and each source's health. A subject it does not list
// is evaluated and clear in every section whose source is Evaluated.
type reviewDoc struct {
	Sources  map[string]*sourceHealth `json:"sources"`
	Inputs   reviewInputs             `json:"inputs"`
	Subjects []*subject               `json:"subjects"`
}

type reviewInputs struct {
	ListCommit   string `json:"list_commit,omitempty"`
	MirrorCommit string `json:"mirror_commit,omitempty"`
	AOD          string `json:"aod,omitempty"`
	Release      string `json:"release,omitempty"`
}

// sourceHealth is one section's source on this run. Evaluated gives it
// authority over every subject the run does not list; a listed subject's
// section says for itself.
type sourceHealth struct {
	UnevaluatedReason string `json:"unevaluated_reason,omitempty"`
	Elapsed           string `json:"elapsed,omitempty"`
	Requests          int64  `json:"requests"`
	Due               int    `json:"due"`
	Failed            int    `json:"failed"`
	Stale             int    `json:"stale"`
	Evaluated         bool   `json:"evaluated"`
	Refused           bool   `json:"refused,omitempty"`
}

type subject struct {
	Sections sections       `json:"sections"`
	Detail   *explainDetail `json:"detail,omitempty"`
	Key      string         `json:"key"`
	Title    string         `json:"title"`
}

type sections struct {
	DateTVDB *dateSection  `json:"date-tvdb,omitempty"`
	DateTMDB *dateSection  `json:"date-tmdb,omitempty"`
	Drift    *driftSection `json:"drift,omitempty"`
	Watch    *watchSection `json:"watch,omitempty"`
}

func (s *sections) date(name string) *dateSection {
	if name == review.DateTVDB {
		return s.DateTVDB
	}
	return s.DateTMDB
}

func (s *sections) setDate(name string, d *dateSection) {
	if name == review.DateTVDB {
		s.DateTVDB = d
	} else {
		s.DateTMDB = d
	}
}

// head is a section's evaluation, whichever section it is.
func (s *sections) head(name string) *review.Section {
	switch name {
	case review.DateTVDB, review.DateTMDB:
		if d := s.date(name); d != nil {
			return &d.Section
		}
	case review.Drift:
		if s.Drift != nil {
			return &s.Drift.Section
		}
	case review.Watch:
		if s.Watch != nil {
			return &s.Watch.Section
		}
	}
	return nil
}

// dateSection is one side's date check of a node. Digests maps each
// finding key to its inputs digest, what an acceptance part records.
type dateSection struct {
	Digests  map[string]string   `json:"digests,omitempty"`
	Node     string              `json:"node,omitempty"`
	Overlay  string              `json:"overlay,omitempty"`
	Slug     string              `json:"slug,omitempty"`
	Findings []datecheck.Finding `json:"findings,omitempty"`
	Movies   []int               `json:"movies,omitempty"`
	// Siblings are the other AniDB ids dead on the same show.
	Siblings []int `json:"siblings,omitempty"`
	review.Section
	Series int  `json:"series,omitempty"`
	Dead   bool `json:"dead,omitempty"`
}

func (d *dateSection) raised() bool { return d.Dead || len(d.Findings) > 0 }

type driftSection struct {
	Findings []drift.Finding `json:"findings,omitempty"`
	review.Section
}

// watchSection holds a subject's watched records with gaps. Unclaimed are
// the watch parts of records with no gap this run.
type watchSection struct {
	EntryURL  string         `json:"entry_url,omitempty"`
	Findings  []watchFinding `json:"findings,omitempty"`
	Unclaimed []review.Match `json:"unclaimed,omitempty"`
	review.Section
}

type watchFinding struct {
	Acceptance *review.Match `json:"acceptance,omitempty"`
	watch.Entry
}

type reviewConfig struct {
	listPath, listCommit, overlayDir, mirrorPath, aodPath, releasePath string
	watchConfig, layoutsTVDB, layoutsTMDB, cachePath, reviewedDir, out string
	maxAge, budgetTVDB, budgetTMDB                                     time.Duration
}

func reviewFlags(fs *flag.FlagSet, c *reviewConfig) {
	fs.StringVar(&c.listPath, "list", "", "anime-list-master.xml at Anime-Lists HEAD")
	fs.StringVar(&c.listCommit, "list-commit", "", "Anime-Lists commit of -list, for the report")
	fs.StringVar(&c.overlayDir, "overlay", "overlay", "overlay directory")
	fs.StringVar(&c.mirrorPath, "mirror", "", "AniDB mirror snapshot; none leaves the date checks and links' AniDB check unevaluated")
	fs.StringVar(&c.aodPath, "aod", "", "anime-offline-database .jsonl; none leaves links' landed check unevaluated")
	fs.StringVar(&c.releasePath, "release", "", "published animap.json; none leaves the watch unevaluated")
	fs.StringVar(&c.watchConfig, "watch-config", "watch/seadex.json", "watch-set config")
	fs.StringVar(&c.layoutsTVDB, "layouts-tvdb", "", "TVDB layouts kept between runs")
	fs.StringVar(&c.layoutsTMDB, "layouts-tmdb", "", "TMDB layouts kept between runs")
	fs.StringVar(&c.cachePath, "cache", "", "HTTP validator cache file, for the watch listing")
	fs.StringVar(&c.reviewedDir, "reviewed", review.Dir, "acceptance records directory")
	fs.DurationVar(&c.maxAge, "max-age", datecheck.RefetchAge, "refetch a TVDB layout older than this")
	fs.DurationVar(&c.budgetTVDB, "budget-tvdb", 90*time.Minute, "stop fetching SkyHook after this long")
	fs.DurationVar(&c.budgetTMDB, "budget-tmdb", 60*time.Minute, "stop fetching TMDB after this long")
}

func runReview(ctx context.Context, args []string, log *slog.Logger) error {
	var c reviewConfig
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	reviewFlags(fs, &c)
	fs.StringVar(&c.out, "out", "review.json", "output path")
	if err := parse(fs, args); err != nil {
		return err
	}
	if err := required(fs, "list"); err != nil {
		return err
	}
	token, err := tmdbToken()
	if err != nil {
		return err
	}
	start := time.Now()
	r, err := loadReview(&c, token, log)
	if err != nil {
		return err
	}
	if token == "" {
		log.Warn("review: TMDB_TOKEN is not set, TMDB dates are not checked this run")
	}
	r.readWatch(ctx, &c, log)
	r.refresh(ctx, &c, log)
	r.save(ctx, &c, log)
	doc := r.evaluate(time.Now())
	log.Info("review: done", "subjects", len(doc.Subjects), "elapsed", time.Since(start).Round(time.Second).String())
	return writeJSON(ctx, c.out, doc)
}

// reviewRun is one run's inputs and what the refreshes left in the stores.
// stale maps a due id the run did not refresh to why.
type reviewRun struct {
	list        *animelists.List
	patched     map[int]*animelists.Node
	facts       *anidb.Facts
	aod         map[int][]int
	release     *schema.Document
	records     map[string]review.Record
	tvdb, tmdb  *datecheck.Store
	health      map[string]*sourceHealth
	linked      map[int]map[[2]int]int
	tvdbStale   map[int]string
	tmdbStale   map[int]string
	movieStale  map[int]string
	linkedAL    map[int]bool
	watchCfg    *watch.Config
	listCommit  string
	token       string
	entries     []overlay.Entry
	links       []overlay.Link
	watchIDs    []int
	tvdbClient  getterCount
	tmdbClient  getterCount
	captured    map[int]bool
	tvdbSeries  []int
	tmdbShows   []int
	tmdbMovies  []int
	watchListed bool
}

// getterCount counts the requests a provider's client made.
type getterCount struct {
	g skyhook.Getter
	n *atomic.Int64
}

func (c getterCount) Get(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	c.n.Add(1)
	return c.g.Get(ctx, url, maxBytes)
}

func loadReview(c *reviewConfig, token string, log *slog.Logger) (*reviewRun, error) {
	list, err := readList(c.listPath)
	if err != nil {
		return nil, err
	}
	entries, links, err := loadOverlay(c.overlayDir)
	if err != nil {
		return nil, err
	}
	records, err := review.LoadDir(c.reviewedDir)
	if err != nil {
		return nil, err
	}
	r := &reviewRun{
		list: list, entries: entries, links: links, records: records, token: token, listCommit: c.listCommit,
		patched: overlay.Apply(list.Nodes, entries), tvdbStale: map[int]string{}, tmdbStale: map[int]string{}, movieStale: map[int]string{},
		health: map[string]*sourceHealth{
			review.DateTVDB: {Evaluated: true}, review.DateTMDB: {Evaluated: true},
			review.Drift: {Evaluated: true}, review.Watch: {},
		},
		linkedAL: map[int]bool{}, captured: map[int]bool{},
	}
	for i := range links {
		r.linkedAL[links[i].AniListID] = true
	}
	if srcErr := r.loadSources(c); srcErr != nil {
		return nil, srcErr
	}
	if r.tvdb, err = loadStore(c.layoutsTVDB, log); err != nil {
		return nil, err
	}
	if r.tmdb, err = loadStore(c.layoutsTMDB, log); err != nil {
		return nil, err
	}
	r.planIDs()
	return r, nil
}

func (r *reviewRun) loadSources(c *reviewConfig) error {
	if c.mirrorPath != "" {
		snap, err := anidb.LoadSnapshot(c.mirrorPath)
		if err != nil {
			return err
		}
		r.facts = anidb.NewFacts(snap, nil, nil)
		r.linked = linkedEpisodes(r.links, r.facts)
	} else {
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			r.health[name] = &sourceHealth{UnevaluatedReason: "the AniDB mirror was not read"}
		}
	}
	if c.aodPath != "" {
		aod, err := offlinedb.Load(c.aodPath, offlinedb.DefaultLimits)
		if err != nil {
			return err
		}
		r.aod = aniDBByAniList(aod)
	}
	if c.releasePath != "" {
		f, err := os.Open(c.releasePath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if r.release, err = schema.Decode(f); err != nil {
			return err
		}
	}
	if r.token == "" && r.facts != nil {
		r.health[review.DateTMDB] = &sourceHealth{UnevaluatedReason: "TMDB_TOKEN is not set"}
	}
	return nil
}

// loadStore reads a layouts file; one it cannot use starts empty, so its
// shows are fetched again.
func loadStore(path string, log *slog.Logger) (*datecheck.Store, error) {
	if path == "" {
		return datecheck.NewStore(), nil
	}
	s, err := datecheck.LoadStore(path)
	if err != nil && s == nil {
		return nil, err
	}
	if err != nil {
		log.Warn("review: layouts unusable, refetching them", "path", path, "error", err)
	}
	return s, nil
}

// planIDs lists the ids each store must hold: TVDB series of the nodes
// AniDB lists episodes for and of every captured overlay entry, TMDB shows
// of the listed nodes and every node's TMDB movies.
func (r *reviewRun) planIDs() {
	tvdb, shows, movies := map[int]bool{}, map[int]bool{}, map[int]bool{}
	for i := range r.entries {
		if tv := r.entries[i].Captured.TVDB; tv != nil {
			tvdb[tv.Series], r.captured[tv.Series] = true, true
		}
	}
	if r.facts != nil {
		for aid, n := range r.patched {
			_, listed := r.facts.Anime(aid)
			addShow(tvdb, n, animelists.TVDB, listed)
			addShow(shows, n, animelists.TMDB, listed)
			for _, m := range datecheck.Movies(n) {
				movies[m] = true
			}
		}
	}
	r.tvdbSeries = slices.Sorted(maps.Keys(tvdb))
	r.tmdbShows, r.tmdbMovies = slices.Sorted(maps.Keys(shows)), slices.Sorted(maps.Keys(movies))
}

// addShow adds n's show on side to set when AniDB lists n's episodes.
func addShow(set map[int]bool, n *animelists.Node, side animelists.Side, listed bool) {
	if s, ok := datecheck.Series(n, side); ok && listed {
		set[s] = true
	}
}

// sideOf is the provider a date section reads.
func sideOf(section string) animelists.Side {
	if section == review.DateTMDB {
		return animelists.TMDB
	}
	return animelists.TVDB
}

func (r *reviewRun) readWatch(ctx context.Context, c *reviewConfig, log *slog.Logger) {
	h := r.health[review.Watch]
	if r.release == nil {
		h.UnevaluatedReason = "no published release to watch"
		return
	}
	cfg, err := watch.LoadConfig(c.watchConfig)
	if err != nil {
		h.UnevaluatedReason = "the watch-set config is unreadable"
		log.Warn("review: watch-set config unreadable, the watch is not evaluated", "error", err)
		return
	}
	client, err := source.New(c.cachePath, pace, log)
	if err != nil {
		h.UnevaluatedReason = "the HTTP cache is unreadable"
		log.Warn("review: HTTP cache unreadable, the watch is not evaluated", "error", err)
		return
	}
	ids, err := watch.FetchIDs(ctx, client, cfg)
	if saveErr := client.Save(ctx); saveErr != nil {
		log.Warn("review: cache not saved", "error", saveErr)
	}
	if err != nil {
		h.UnevaluatedReason = "the watch-set listing was incomplete"
		log.Warn("review: watch listing unusable, the watch is not evaluated", "set", cfg.Name, "error", err)
		return
	}
	r.watchCfg, r.watchIDs, r.watchListed, h.Evaluated = cfg, ids, true, true
}

// refresh fetches the due layouts of both providers concurrently, each
// paced per host and within its own budget.
func (r *reviewRun) refresh(ctx context.Context, c *reviewConfig, log *slog.Logger) {
	now := time.Now()
	var wg sync.WaitGroup
	wg.Go(func() {
		start := time.Now()
		defer func() { r.health[review.DateTVDB].Elapsed = time.Since(start).Round(time.Second).String() }()
		client := r.tvdbGetter(source.NewMemory(pace, log, source.WithoutCache()))
		due := r.tvdb.Series.Due(r.tvdbSeries, r.captured, c.maxAge, now)
		fetchAll(ctx, &refreshJob{
			name: "SkyHook", into: r.tvdb.Series, due: due, stale: r.tvdbStale, budget: c.budgetTVDB, health: r.health[review.DateTVDB], count: client.n,
			fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
				show, err := skyhook.Fetch(ctx, client, id)
				if err != nil {
					return datecheck.Layout{}, err
				}
				return datecheck.LayoutOf(show, time.Now().UTC()), nil
			},
		}, log)
	})
	if r.facts != nil && r.token != "" {
		wg.Go(func() {
			r.tmdbClient = getterCount{g: source.NewMemory(tmdbPace, log, source.WithoutCache(), source.WithBearer(tmdb.Host, r.token)), n: &atomic.Int64{}}
			r.refreshTMDB(ctx, c.budgetTMDB, now, log)
		})
	}
	wg.Wait()
}

func (r *reviewRun) tvdbGetter(g skyhook.Getter) getterCount {
	r.tvdbClient = getterCount{g: g, n: &atomic.Int64{}}
	return r.tvdbClient
}

// refreshTMDB fetches the due TMDB shows, then the due movies, through
// r.tmdbClient within budget; a refused token leaves both unread.
func (r *reviewRun) refreshTMDB(ctx context.Context, budget time.Duration, now time.Time, log *slog.Logger) {
	client := r.tmdbClient
	h := r.health[review.DateTMDB]
	start := time.Now()
	defer func() { h.Elapsed = time.Since(start).Round(time.Second).String() }()
	stop := start.Add(budget)
	shows := &refreshJob{
		name: tmdbName, into: r.tmdb.Series, due: r.tmdb.Series.DueStaggered(r.tmdbShows, now), stale: r.tmdbStale, health: h, count: client.n, stop: stop,
		fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
			show, err := tmdb.Fetch(ctx, client, id)
			if err != nil {
				return datecheck.Layout{}, err
			}
			return datecheck.LayoutOfTMDB(show, time.Now().UTC()), nil
		},
	}
	movies := r.tmdb.Movies.DueStaggered(r.tmdbMovies, now)
	if fetchAll(ctx, shows, log) {
		markLeft(r.movieStale, movies, "TMDB refused the token")
		h.Due, h.Stale = h.Due+len(movies), h.Stale+len(movies)
		return
	}
	fetchAll(ctx, &refreshJob{
		name: tmdbName, into: r.tmdb.Movies, due: movies, stale: r.movieStale, health: h, count: client.n, stop: stop,
		fetch: func(ctx context.Context, id int) (datecheck.Layout, error) {
			if err := tmdb.Movie(ctx, client, id); err != nil {
				return datecheck.Layout{}, err
			}
			return datecheck.Layout{Fetched: time.Now().UTC()}, nil
		},
	}, log)
}

// refreshJob is one provider's due list. stop, when set, is a deadline the
// job shares with another; else the job has budget of its own.
type refreshJob struct {
	stop   time.Time
	into   datecheck.Layouts
	stale  map[int]string
	health *sourceHealth
	count  *atomic.Int64
	fetch  func(context.Context, int) (datecheck.Layout, error)
	name   string
	due    []int
	budget time.Duration
}

// fetchAll refreshes j's due ids in order until its budget is spent, and
// reports whether the provider refused the token. A due id it did not
// refresh keeps its older layout for a later run, but no node is judged on
// it this run.
func fetchAll(ctx context.Context, j *refreshJob, log *slog.Logger) (refused bool) {
	if j.stop.IsZero() {
		j.stop = time.Now().Add(j.budget)
	}
	h := j.health
	h.Due += len(j.due)
	defer func() { h.Requests, h.Stale = j.count.Load(), h.Stale+countLeft(j) }()
	for i, id := range j.due {
		if ctx.Err() != nil || time.Now().After(j.stop) {
			log.Warn("review: fetch budget spent, the shows left are not checked this run", "source", j.name, "left", len(j.due)-i)
			markLeft(j.stale, j.due[i:], j.name+" fetch budget spent")
			return false
		}
		lay, err := j.fetch(ctx, id)
		switch {
		case errors.Is(err, source.ErrNotFound):
			j.into[id] = datecheck.Layout{Fetched: time.Now().UTC(), Absent: true}
		case errors.Is(err, source.ErrRefused):
			log.Error("review: the provider refused the token, its side is not checked this run", "source", j.name)
			markLeft(j.stale, j.due[i:], j.name+" refused the token")
			h.Refused, h.Evaluated, h.UnevaluatedReason = true, false, j.name+" refused the token"
			return true
		case err != nil:
			h.Failed++
			j.stale[id] = j.name + " could not be read"
			log.Warn("review: show unreadable, not checked this run", "source", j.name, "id", id, "error", err)
		default:
			j.into[id] = lay
		}
	}
	return false
}

func markLeft(stale map[int]string, ids []int, reason string) {
	for _, id := range ids {
		stale[id] = reason
	}
}

func countLeft(j *refreshJob) int {
	n := 0
	for _, id := range j.due {
		if j.stale[id] != "" {
			n++
		}
	}
	return n
}

// save prunes each store to the ids the run reads and writes it back.
// Without the mirror the run knows only the captured series, so it prunes
// nothing and keeps the TMDB store as it was.
func (r *reviewRun) save(ctx context.Context, c *reviewConfig, log *slog.Logger) {
	stores := []struct {
		s    *datecheck.Store
		path string
	}{{r.tvdb, c.layoutsTVDB}}
	if r.facts != nil {
		r.tvdb.Series.Prune(r.tvdbSeries)
	}
	if r.facts != nil && r.token != "" {
		r.tmdb.Series.Prune(r.tmdbShows)
		r.tmdb.Movies.Prune(r.tmdbMovies)
		stores = append(stores, struct {
			s    *datecheck.Store
			path string
		}{r.tmdb, c.layoutsTMDB})
	}
	for _, st := range stores {
		if st.path == "" {
			continue
		}
		b, err := st.s.Encode()
		if err == nil {
			err = writeFile(ctx, st.path, b)
		}
		if err != nil {
			log.Warn("review: layouts not saved", "path", st.path, "error", err)
		}
	}
}

// evaluate judges every subject on what the stores hold now.
func (r *reviewRun) evaluate(now time.Time) *reviewDoc {
	doc := &reviewDoc{Sources: r.health, Inputs: reviewInputs{ListCommit: r.listCommit}}
	if r.facts != nil {
		doc.Inputs.MirrorCommit = r.facts.Commit()
	}
	if r.release != nil {
		doc.Inputs.AOD, doc.Inputs.Release = r.release.Sources.AnimeOfflineDatabase.Release, r.release.GeneratedAt
	}
	subjects := map[string]*subject{}
	r.addDates(subjects, now)
	r.addDrift(subjects)
	r.addWatch(subjects)
	r.addAcceptance(subjects)
	r.fillUnevaluated(subjects)
	for _, k := range slices.Sorted(maps.Keys(subjects)) {
		doc.Subjects = append(doc.Subjects, subjects[k])
	}
	return doc
}

func (r *reviewRun) subjectOf(subjects map[string]*subject, key string) *subject {
	s := subjects[key]
	if s == nil {
		s = &subject{Key: key, Title: r.titleOf(key)}
		subjects[key] = s
	}
	return s
}

// titleOf is the name an issue title shows for key: the node's, else the
// overlay's, else the acceptance record's.
func (r *reviewRun) titleOf(key string) string {
	sub, _ := review.ParseKey(key)
	if sub.AniDB() {
		if n := r.patched[sub.ID]; n != nil && n.Name != "" {
			return n.Name
		}
	} else if i := slices.IndexFunc(r.links, func(l overlay.Link) bool { return l.AniListID == sub.ID }); i >= 0 {
		return r.links[i].Title
	}
	if rec, ok := r.records[key]; ok {
		return rec.Title
	}
	return ""
}

func (r *reviewRun) addDates(subjects map[string]*subject, now time.Time) {
	paths := make(map[int]string, len(r.entries))
	for i := range r.entries {
		paths[r.entries[i].AniDBID] = r.entries[i].Path()
	}
	for _, aid := range slices.Sorted(maps.Keys(r.patched)) {
		key := review.AniDBKey(aid)
		_, accepted := r.records[key]
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			if !r.health[name].Evaluated {
				continue
			}
			d := r.dateSection(name, aid, now)
			if d == nil || (!d.raised() && d.Evaluated && !accepted) {
				continue
			}
			if d.raised() {
				d.Node, d.Overlay = r.patched[aid].Canonical(), paths[aid]
			}
			r.subjectOf(subjects, key).Sections.setDate(name, d)
		}
	}
	linkDeadSiblings(subjects)
}

// linkDeadSiblings gives each dead section the other nodes dead on its
// show: one dead show raises every node mapped to it.
func linkDeadSiblings(subjects map[string]*subject) {
	type show struct {
		name   string
		series int
	}
	type dead struct {
		d   *dateSection
		aid int
	}
	byShow := map[show][]dead{}
	for k, s := range subjects {
		sub, _ := review.ParseKey(k)
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			if d := s.Sections.date(name); d != nil && d.Dead {
				byShow[show{name, d.Series}] = append(byShow[show{name, d.Series}], dead{d: d, aid: sub.ID})
			}
		}
	}
	for _, ds := range byShow {
		ids := make([]int, 0, len(ds))
		for _, x := range ds {
			ids = append(ids, x.aid)
		}
		slices.Sort(ids)
		for _, x := range ds {
			x.d.Siblings = slices.DeleteFunc(slices.Clone(ids), func(id int) bool { return id == x.aid })
		}
	}
}

// dateSection judges node aid on one side, nil when the side does not
// apply: no show AniDB lists episodes for, and no TMDB movie.
func (r *reviewRun) dateSection(name string, aid int, now time.Time) *dateSection {
	n := r.patched[aid]
	side, store, stale := sideOf(name), r.tvdb, r.tvdbStale
	var movies []int
	if name == review.DateTMDB {
		store, stale, movies = r.tmdb, r.tmdbStale, datecheck.Movies(n)
	}
	a, listed := r.facts.Anime(aid)
	series, mapped := datecheck.Series(n, side)
	show := mapped && listed
	if !show && len(movies) == 0 {
		return nil
	}
	d := &dateSection{Applicable: true, Movies: movies}
	lead := datecheck.Lead{Series: series, Side: side}
	if show {
		d.Series = series
		lay, ok := store.Series[series]
		if d.Reason = unreadReason(stale[series], ok, "no layout fetched yet"); d.Reason != "" {
			return d
		}
		d.Slug = lay.Slug
		if l := datecheck.Check(n, side, &a, &lay, now, r.linked[aid]); l != nil {
			lead = *l
		}
	}
	if len(movies) > 0 {
		fs, ok := datecheck.CheckMovies(n, store.Movies)
		if d.Reason = unreadReason(r.staleMovie(movies), ok, "no movie layout fetched yet"); d.Reason != "" {
			return d
		}
		lead.Findings = append(lead.Findings, fs...)
	}
	d.Evaluated, d.Dead, d.Findings = true, lead.Dead, lead.Findings
	if d.raised() {
		d.Digests = lead.Digests()
	}
	return d
}

// unreadReason is why a layout cannot be judged on: the run did not
// refresh it, or the store has none; "" when it can.
func unreadReason(stale string, stored bool, missing string) string {
	switch {
	case stale != "":
		return stale
	case !stored:
		return missing
	}
	return ""
}

func (r *reviewRun) staleMovie(movies []int) string {
	for _, m := range movies {
		if r.movieStale[m] != "" {
			return r.movieStale[m]
		}
	}
	return ""
}

func (r *reviewRun) addDrift(subjects map[string]*subject) {
	for i := range r.entries {
		e := &r.entries[i]
		obs := r.observe(r.list.Nodes[e.AniDBID], e.Captured.TVDB)
		f, ev := drift.Decide(drift.OfEntry(e), &obs)
		reason := ""
		if tv := e.Captured.TVDB; tv != nil {
			reason = "SkyHook was not read for TVDB " + strconv.Itoa(tv.Series) + " this run"
		}
		r.putDrift(subjects, review.AniDBKey(e.AniDBID), drift.KeysOf(e.AniDBID).All(), f, ev, reason)
	}
	for i := range r.links {
		l := &r.links[i]
		var obs drift.Observation
		reason := ""
		if r.aod != nil {
			obs.AOD, obs.AODRead = r.aod[l.AniListID], true
		} else {
			reason = "anime-offline-database was not read this run"
		}
		if r.facts != nil {
			obs.AniDB, obs.Mirror, obs.AniDBRead = l.AniDBDigest(r.facts), r.facts.Commit(), true
		} else {
			reason = "the AniDB mirror was not read this run"
		}
		f, ev := drift.Decide(drift.OfLink(l), &obs)
		r.putDrift(subjects, review.AniListKey(l.AniListID), drift.LinkKeys(l.AniListID).All(), f, ev, reason)
	}
}

func (r *reviewRun) putDrift(subjects map[string]*subject, key string, keys []string, f []drift.Finding, ev []string, reason string) {
	evaluated := !slices.ContainsFunc(keys, func(k string) bool { return !slices.Contains(ev, k) })
	if len(f) == 0 && evaluated {
		return
	}
	d := &driftSection{Applicable: true, Evaluated: evaluated, Findings: f}
	if !evaluated {
		d.Reason = reason
	}
	s := r.subjectOf(subjects, key)
	if s.Sections.Drift == nil {
		s.Sections.Drift = d
		return
	}
	s.Sections.Drift.Findings = append(s.Sections.Drift.Findings, f...)
	if !evaluated {
		s.Sections.Drift.Evaluated, s.Sections.Drift.Reason = false, reason
	}
}

// observe reads tv's series from the TVDB store; a series the run did not
// refresh leaves the layout unread.
func (r *reviewRun) observe(node *animelists.Node, tv *overlay.CapturedTVDB) drift.Observation {
	obs := drift.Observation{Node: node}
	if tv == nil {
		return obs
	}
	lay, ok := r.tvdb.Series[tv.Series]
	switch {
	case !ok || r.tvdbStale[tv.Series] != "":
	case lay.Absent:
		obs.LayoutAbsent = true
	default:
		obs.Layout, obs.LayoutRead = skyhook.Layout(&skyhook.Show{Episodes: lay.Official()}, tv.Seasons), true
	}
	return obs
}

// watchKey is the subject a watched record's gap belongs to: its AniDB
// node's, unless it has no AniDB id or a link names it.
func (r *reviewRun) watchKey(e *watch.Entry) string {
	if e.AniDBID == 0 || r.linkedAL[e.AniListID] {
		return review.AniListKey(e.AniListID)
	}
	return review.AniDBKey(e.AniDBID)
}

func (r *reviewRun) addWatch(subjects map[string]*subject) {
	if !r.watchListed {
		return
	}
	entries := watch.Evaluate(r.watchIDs, r.release)
	for i := range entries {
		s := r.subjectOf(subjects, r.watchKey(&entries[i]))
		if s.Sections.Watch == nil {
			s.Sections.Watch = &watchSection{Applicable: true, Evaluated: true, EntryURL: r.watchCfg.EntryURL}
		}
		s.Sections.Watch.Findings = append(s.Sections.Watch.Findings, watchFinding{Entry: entries[i]})
	}
}

// addAcceptance matches every acceptance record's parts against the run.
// A part on a section the run evaluated as clear, or not applicable, is
// stale: it matches, and the summary lists it for deletion.
func (r *reviewRun) addAcceptance(subjects map[string]*subject) {
	for _, key := range slices.Sorted(maps.Keys(r.records)) {
		rec := r.records[key]
		s := r.subjectOf(subjects, key)
		file := review.File(key)
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			part := rec.Date(name)
			if part == nil {
				continue
			}
			d := s.Sections.date(name)
			if d == nil {
				h := r.health[name]
				d = &dateSection{Evaluated: h.Evaluated, Reason: h.UnevaluatedReason}
				s.Sections.setDate(name, d)
			}
			m := &review.Match{File: file, Part: name, Verdict: part.Verdict, Reason: part.Reason}
			if d.Evaluated {
				m.Causes = part.Changes(d.Digests)
				m.Matches = len(m.Causes) == 0
			}
			d.Acceptance = m
		}
		r.matchWatch(s, &rec, file)
	}
}

func (r *reviewRun) matchWatch(s *subject, rec *review.Record, file string) {
	if len(rec.Parts.Watch) == 0 {
		return
	}
	w := s.Sections.Watch
	if w == nil {
		w = &watchSection{Evaluated: r.watchListed, Reason: r.health[review.Watch].UnevaluatedReason}
		if r.watchListed {
			w.EntryURL = r.watchCfg.EntryURL
		}
		s.Sections.Watch = w
	}
	for i := range rec.Parts.Watch {
		part := &rec.Parts.Watch[i]
		m := review.Match{File: file, Part: review.Watch + ":" + strconv.Itoa(part.AniListID), Verdict: part.Verdict, Reason: part.Reason}
		i := slices.IndexFunc(w.Findings, func(f watchFinding) bool { return f.AniListID == part.AniListID })
		if i < 0 {
			m.Matches = w.Evaluated
			w.Unclaimed = append(w.Unclaimed, m)
			continue
		}
		f := &w.Findings[i]
		m.Causes = part.Changes(f.RecordSHA256, f.GapNames())
		m.Matches = len(m.Causes) == 0
		f.Acceptance = &m
	}
}

// fillUnevaluated adds, to every listed AniDB subject, the date sections
// whose source could not judge any node this run, so its issue says so.
func (r *reviewRun) fillUnevaluated(subjects map[string]*subject) {
	for key, s := range subjects {
		sub, _ := review.ParseKey(key)
		n := r.patched[sub.ID]
		if !sub.AniDB() || n == nil {
			continue
		}
		for _, name := range []string{review.DateTVDB, review.DateTMDB} {
			if h := r.health[name]; !h.Evaluated && s.Sections.date(name) == nil && dateApplies(n, name) {
				s.Sections.setDate(name, &dateSection{Applicable: true, Reason: h.UnevaluatedReason})
			}
		}
	}
}

// dateApplies reports whether n maps anywhere a date section reads.
func dateApplies(n *animelists.Node, section string) bool {
	_, ok := datecheck.Series(n, sideOf(section))
	return ok || section == review.DateTMDB && len(datecheck.Movies(n)) > 0
}

// linkedEpisodes maps each AniDB id to the [season, number] of its episodes
// an overlay link names, and each to the link's AniList id.
func linkedEpisodes(links []overlay.Link, facts *anidb.Facts) map[int]map[[2]int]int {
	out := map[int]map[[2]int]int{}
	for i := range links {
		for _, s := range links[i].Sources(facts) {
			if out[s.AniDB] == nil {
				out[s.AniDB] = map[[2]int]int{}
			}
			out[s.AniDB][[2]int{s.AniDBSeason, s.Number}] = links[i].AniListID
		}
	}
	return out
}
