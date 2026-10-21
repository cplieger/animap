package mapread

import (
	"cmp"
	"errors"
	"fmt"
)

// validate checks what one row cannot: the table orders, the runs against
// their row, the TVDB episode ids across rows, every part against the
// anime it names, and against lim.MaxEpisodes the episodes the parts name
// and those the runs' movies name.
func validate(doc *Document, lim Limits) error {
	byID := make(map[int]*Anime, len(doc.Anime))
	ids := tvdbIDs{byEpisode: map[TVDBEpisode]int{}, byID: map[int]TVDBEpisode{}}
	movies := 0
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if i > 0 && a.AniDBID <= doc.Anime[i-1].AniDBID {
			return invalid("anime", i, "anidb_id %d is not after %d", a.AniDBID, doc.Anime[i-1].AniDBID)
		}
		if err := validateRuns(a); err != nil {
			return invalid("anime", i, "%v", err)
		}
		if err := ids.add(a); err != nil {
			return invalid("anime", i, "%v", err)
		}
		var ok bool
		if movies, ok = addMovieEpisodes(movies, a, lim.MaxEpisodes); !ok {
			return fmt.Errorf("%w: the runs' tmdb_movies name more than %d AniDB episodes", ErrTooLarge, lim.MaxEpisodes)
		}
		byID[a.AniDBID] = a
	}
	return validateEntries(doc.Entries, byID, lim)
}

// tvdbIDs holds a document to one id per TVDB episode and one TVDB episode
// per id, so a lookup by id finds one episode.
type tvdbIDs struct {
	byEpisode map[TVDBEpisode]int
	byID      map[int]TVDBEpisode
}

func (m *tvdbIDs) add(a *Anime) error {
	for ep, id := range tvdbEpisodeIDs(a) {
		if had, ok := m.byEpisode[ep]; ok && had != id {
			return fmt.Errorf("TVDB %d S%dE%d has the episode ids %d and %d", ep.Series, ep.Season, ep.Episode, had, id)
		}
		if had, ok := m.byID[id]; ok && had != ep {
			return fmt.Errorf("TVDB episode id %d names %d S%dE%d and %d S%dE%d", id, had.Series, had.Season, had.Episode, ep.Series, ep.Season, ep.Episode)
		}
		m.byEpisode[ep], m.byID[id] = id, ep
	}
	return nil
}

// addMovieEpisodes adds to named each episode of a's runs once per movie
// it states, what Index.AtTMDBMovie expands over every movie, false when
// that is over limit.
func addMovieEpisodes(named int, a *Anime, limit int) (int, bool) {
	for i := range a.Episodes {
		r := &a.Episodes[i]
		for range r.TMDBMovies {
			var ok bool
			if named, ok = addEpisodes(named, r.End-r.Start+1, limit); !ok {
				return named, false
			}
		}
	}
	return named, true
}

// validateEntries checks the entries' order and parts, and the episodes
// the parts name against lim.MaxEpisodes.
func validateEntries(entries []Entry, byID map[int]*Anime, lim Limits) error {
	named := 0
	for i := range entries {
		if err := entryOrder(entries, i); err != nil {
			return invalid("entries", i, "%v", err)
		}
		for j, p := range entries[i].Parts {
			if err := validatePart(p, byID[p.AniDBID]); err != nil {
				return invalid("entries", i, "parts[%d]: %v", j, err)
			}
			var ok bool
			if named, ok = addEpisodes(named, partEpisodes(p, byID[p.AniDBID]), lim.MaxEpisodes); !ok {
				return fmt.Errorf("%w: the entries name more than %d AniDB episodes", ErrTooLarge, lim.MaxEpisodes)
			}
		}
	}
	return nil
}

// addEpisodes is named+n, false when that is over limit. It compares
// before adding: on a 32-bit int the sum itself can wrap.
func addEpisodes(named, n, limit int) (int, bool) {
	if n > limit-named {
		return named, false
	}
	return named + n, true
}

// partEpisodes is how many AniDB episodes p names, as Index.Episodes
// expands it.
func partEpisodes(p Part, a *Anime) int {
	_, from, to := partRange(a, p)
	return to - from + 1
}

func invalid(table string, i int, format string, args ...any) error {
	return fmt.Errorf("%w: %s[%d]: %s", ErrInvalid, table, i, fmt.Sprintf(format, args...))
}

// entryOrder holds the entries ascending by AniList id.
func entryOrder(entries []Entry, i int) error {
	if i > 0 && entries[i].AniListID <= entries[i-1].AniListID {
		return fmt.Errorf("AniList %d is not after %d", entries[i].AniListID, entries[i-1].AniListID)
	}
	return nil
}

// validateRuns holds a's runs to their row.
func validateRuns(a *Anime) error {
	for i := range a.Episodes {
		r := &a.Episodes[i]
		if err := validateRun(a, i); err != nil {
			return fmt.Errorf("episodes[%d] (%s %d-%d): %w", i, r.Kind, r.Start, r.End, err)
		}
	}
	return nil
}

func validateRun(a *Anime, i int) error {
	r := &a.Episodes[i]
	if err := runRange(a, r); err != nil {
		return err
	}
	if i > 0 {
		p := &a.Episodes[i-1]
		if pk, k := p.Kind.order(), r.Kind.order(); pk > k || pk == k && r.Start <= p.End {
			return fmt.Errorf("is not after %s %d-%d", p.Kind, p.Start, p.End)
		}
		if p.ExtendedBy(r) {
			return fmt.Errorf("continues %s %d-%d, so the two are one run", p.Kind, p.Start, p.End)
		}
	}
	if err := runTargets(a.TVDBID, r.TVDB); err != nil {
		return err
	}
	noTVDB := r.TVDB == nil && a.TVDBID == 0 || r.TVDB != nil && len(*r.TVDB) == 0
	if r.AniDBEpisodeIDs == nil && r.TMDBMovies == nil && noTVDB {
		return errors.New("lists no AniDB episode and states no landing, which no run says")
	}
	return nil
}

// runTargets holds a run's tvdb to its row on series row: ascending by
// series, season and episode, each once; a series member names a series
// other than row's; and a row on no series states only targets that each
// name theirs, so never [], since nowhere needs a series to be nowhere on.
func runTargets(row int, tvdb *[]Target) error {
	if tvdb == nil {
		return nil
	}
	if row == 0 && len(*tvdb) == 0 {
		return errors.New("states tvdb [] on a row with no tvdb_id")
	}
	for j, t := range *tvdb {
		switch {
		case t.Series == 0 && row == 0:
			return fmt.Errorf("tvdb[%d] names no series on a row with no tvdb_id", j)
		case t.Series > 0 && t.Series == row:
			return fmt.Errorf("tvdb[%d] names series %d, the row's tvdb_id", j, t.Series)
		case j > 0 && !targetBefore((*tvdb)[j-1], t, row):
			return fmt.Errorf("tvdb[%d] is not after the target before it", j)
		}
	}
	return nil
}

// targetBefore orders targets of a row on series row by series, season,
// then episode.
func targetBefore(a, b Target, row int) bool {
	return cmp.Or(cmp.Compare(a.on(row), b.on(row)), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Episode, b.Episode)) < 0
}

// runRange holds r's ids and end to its row: ids only with AniDB's list,
// and an end past last only on regular episodes AniDB lists past the
// count, which state no landing.
func runRange(a *Anime, r *Run) error {
	listed := r.AniDBEpisodeIDs != nil
	pastCount := r.Kind == Regular && listed && r.TVDB == nil && r.TMDBMovies == nil
	switch last := a.Last.Of(r.Kind); {
	case listed && !a.EpisodesListed:
		return errors.New("lists AniDB episode ids on a row without episodes_listed")
	case r.End > last && !pastCount:
		return fmt.Errorf("ends past last.%s %d, which only a regular run of listed episodes stating no landing may", r.Kind, last)
	}
	return nil
}

// validatePart holds p to the anime it names. A part with no range may
// name a kind the anime has no last of yet, and then names no episodes.
func validatePart(p Part, a *Anime) error {
	switch {
	case a == nil:
		return fmt.Errorf("names AniDB %d, which has no anime row", p.AniDBID)
	case p.Start > 0 && p.End > a.Last.Of(p.Kind):
		return fmt.Errorf("ends at %s %d, past AniDB %d's last %d", p.Kind, p.End, p.AniDBID, a.Last.Of(p.Kind))
	}
	return nil
}
