package mapread

import (
	"errors"
	"fmt"
)

// validate checks what one row cannot: the table orders, the runs against
// their row, every part against the anime it names, and the episodes the
// parts name against lim.MaxEpisodes.
func validate(doc *Document, lim Limits) error {
	byID := make(map[int]*Anime, len(doc.Anime))
	for i := range doc.Anime {
		a := &doc.Anime[i]
		if i > 0 && a.AniDBID <= doc.Anime[i-1].AniDBID {
			return invalid("anime", i, "anidb_id %d is not after %d", a.AniDBID, doc.Anime[i-1].AniDBID)
		}
		if err := validateRuns(a); err != nil {
			return invalid("anime", i, "%v", err)
		}
		byID[a.AniDBID] = a
	}
	return validateEntries(doc.Entries, byID, lim)
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
	switch {
	case p.Combined:
		return a.Last.Regular
	case p.Start > 0:
		return p.End - p.Start + 1
	}
	return a.Last.Of(p.Kind)
}

func invalid(table string, i int, format string, args ...any) error {
	return fmt.Errorf("%w: %s[%d]: %s", ErrInvalid, table, i, fmt.Sprintf(format, args...))
}

// entryOrder holds the AniList rows first, ascending. The rows with no
// AniList id are not checked: their sort key, the encoded ids, takes in
// services a newer producer may add, which this reader cannot see.
func entryOrder(entries []Entry, i int) error {
	if i == 0 {
		return nil
	}
	e, prev := &entries[i], &entries[i-1]
	switch {
	case e.AniListID > 0 && prev.AniListID == 0:
		return fmt.Errorf("AniList %d follows an entry with no AniList id", e.AniListID)
	case e.AniListID > 0 && e.AniListID <= prev.AniListID:
		return fmt.Errorf("AniList %d is not after %d", e.AniListID, prev.AniListID)
	}
	return nil
}

// validateRuns holds a's runs to their row. A row with neither site id
// has only runs that list AniDB's episodes: any other would say a site's
// member without its id, or land nowhere on every site.
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
	listed := r.AniDBEpisodeIDs != nil
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
	switch {
	case r.TVDB != nil && a.TVDBID == 0:
		return errors.New("states tvdb on a row with no tvdb_id")
	case r.TMDB != nil && a.TMDBTVID == 0:
		return errors.New("states tmdb on a row with no tmdb_tv_id")
	}
	if !listed && nowhere(r.TVDB, a.TVDBID) && nowhere(r.TMDB, a.TMDBTVID) {
		return errors.New("lists no AniDB episode and lands nowhere on every site, which no run says")
	}
	return nil
}

// runRange holds r's ids and end to its row: ids only with AniDB's list,
// and an end past last only on regular episodes AniDB lists past the
// count, which state no site.
func runRange(a *Anime, r *Run) error {
	listed := r.AniDBEpisodeIDs != nil
	pastCount := r.Kind == Regular && listed && r.TVDB == nil && r.TMDB == nil
	switch last := a.Last.Of(r.Kind); {
	case listed && !a.EpisodesListed:
		return errors.New("lists AniDB episode ids on a row without episodes_listed")
	case r.End > last && !pastCount:
		return fmt.Errorf("ends past last.%s %d, which only a regular run of listed episodes stating no site may", r.Kind, last)
	}
	return nil
}

// nowhere reports whether a site says lands nowhere, or has no id.
func nowhere(site *[]Target, id int) bool {
	return id == 0 || site != nil && len(*site) == 0
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
