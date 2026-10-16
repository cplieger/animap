// Package drift decides whether upstream moved under animap's own data: the
// Anime-Lists node an overlay entry patches and the TVDB layout it maps
// onto, and the AniDB episodes and anime-offline-database state an overlay
// link rests on.
package drift

import (
	"strconv"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

// cause is why an entry or a link needs a human.
type cause string

const (
	// NodeChanged is a node that moved without taking the entry's values.
	NodeChanged cause = "node-changed"
	// Landed is a node that now carries every value the entry sets, or an
	// AniList id anime-offline-database now links to AniDB itself.
	Landed cause = "landed"
	// TVDBLayout is a changed official order, or a series SkyHook dropped.
	TVDBLayout cause = "tvdb-layout"
	// AniDBChanged is a linked AniDB episode renumbered, retitled or gone.
	AniDBChanged cause = "anidb-changed"
)

// Finding is one cause on one entry or link. Before and After are set for
// TVDBLayout; Gone says SkyHook no longer serves the series. For a link,
// AniDBIDs are the anime it names, Linked the AniDB id
// anime-offline-database now gives the AniList id, and Mirror the mirror
// commit an AniDBChanged was read at.
type Finding struct {
	Key      string            `json:"key"`
	Path     string            `json:"path"`
	Cause    cause             `json:"cause"`
	Title    string            `json:"title"`
	Mirror   string            `json:"mirror,omitempty"`
	Before   []skyhook.Episode `json:"before,omitempty"`
	After    []skyhook.Episode `json:"after,omitempty"`
	AniDBIDs []int             `json:"anidb_ids,omitempty"`
	AniDB    int               `json:"anidb"`
	AniList  int               `json:"anilist,omitempty"`
	Linked   int               `json:"linked,omitempty"`
	Gone     bool              `json:"gone,omitempty"`
}

// Subject is what one decision is about: an overlay entry or an overlay
// link. The zero Subject decides nothing.
type Subject struct {
	entry *overlay.Entry
	link  *overlay.Link
}

// OfEntry is the subject for an overlay entry.
func OfEntry(e *overlay.Entry) Subject { return Subject{entry: e} }

// OfLink is the subject for an overlay link.
func OfLink(l *overlay.Link) Subject { return Subject{link: l} }

// Observation is what one run saw upstream; each subject reads only its own
// fields. Node nil means the node is absent. Layout is the captured seasons
// of the run's TVDB layout; LayoutRead false means the run did not read it,
// unless LayoutAbsent says SkyHook answered 404. AOD is the AniDB
// ids anime-offline-database gives a link's AniList id, and AniDB the
// link's AniDBDigest at mirror commit Mirror. Each *Read flag says the
// source was looked at.
type Observation struct {
	Node         *animelists.Node
	AniDB        string
	Mirror       string
	Layout       []skyhook.Episode
	AOD          []int
	LayoutRead   bool
	LayoutAbsent bool
	AODRead      bool
	AniDBRead    bool
}

// Keys for one entry or link; a link has no Node or TVDB key, an entry no
// AniDB key. They are the issue identities, so they never change.
type Keys struct {
	Node, Landed, TVDB, AniDB string
}

// All lists the keys that are set.
func (k Keys) All() []string {
	var out []string
	for _, key := range []string{k.Node, k.Landed, k.TVDB, k.AniDB} {
		if key != "" {
			out = append(out, key)
		}
	}
	return out
}

// prefix starts every drift key, and no other key.
const prefix = "drift:"

// KeysOf returns the keys of the entry for AniDB id anidbID.
func KeysOf(anidbID int) Keys {
	id := strconv.Itoa(anidbID)
	return Keys{Node: prefix + "node:" + id, Landed: prefix + "landed:" + id, TVDB: prefix + "tvdb:" + id}
}

// LinkKeys are the issue keys of the link for AniList id al.
func LinkKeys(al int) Keys {
	id := prefix + "link:" + strconv.Itoa(al) + ":"
	return Keys{Landed: id + "landed", AniDB: id + "anidb"}
}

// Decide returns the findings for one subject and the keys this
// observation had authority over. A source that was not read leaves its
// key unevaluated, so an open issue for it is not closed by a run that
// could not look.
func Decide(s Subject, o *Observation) (findings []Finding, evaluated []string) {
	switch {
	case s.entry != nil:
		return decideEntry(s.entry, o)
	case s.link != nil:
		return decideLink(s.link, o)
	}
	return nil, nil
}

// decideEntry: an entry with no TVDB fingerprint has authority over that
// key without a read, because nothing it holds can drift there.
func decideEntry(e *overlay.Entry, o *Observation) (findings []Finding, evaluated []string) {
	k := KeysOf(e.AniDBID)
	evaluated = append(make([]string, 0, 3), k.Node, k.Landed)
	if animelists.HashOf(o.Node) != e.Captured.NodeSHA256 {
		c, key := NodeChanged, k.Node
		if overlay.Landed(o.Node, e) {
			c, key = Landed, k.Landed
		}
		findings = append(findings, Finding{Key: key, Cause: c, AniDB: e.AniDBID, Title: e.Title, Path: e.Path()})
	}
	if c := e.Captured.TVDB; c == nil || o.LayoutRead || o.LayoutAbsent {
		evaluated = append(evaluated, k.TVDB)
		if c != nil && skyhook.Hash(o.Layout) != c.SHA256 {
			findings = append(findings, Finding{Key: k.TVDB, Cause: TVDBLayout, AniDB: e.AniDBID, Title: e.Title, Path: e.Path(), Before: c.Episodes, After: o.Layout, Gone: o.LayoutAbsent})
		}
	}
	return findings, evaluated
}

// decideLink: a link has landed when anime-offline-database links the
// AniList id to an AniDB id of its own, because the join then no longer
// needs it.
func decideLink(l *overlay.Link, o *Observation) (findings []Finding, evaluated []string) {
	k := LinkKeys(l.AniListID)
	ids := l.AniDBIDs()
	f := func(c cause, key string) Finding {
		return Finding{Key: key, Cause: c, AniDB: ids[0], AniDBIDs: ids, AniList: l.AniListID, Title: l.Title, Path: l.Path()}
	}
	if o.AODRead {
		evaluated = append(evaluated, k.Landed)
		if len(o.AOD) > 0 {
			x := f(Landed, k.Landed)
			x.Linked = o.AOD[0]
			findings = append(findings, x)
		}
	}
	if o.AniDBRead {
		evaluated = append(evaluated, k.AniDB)
		if o.AniDB != l.Captured.AniDBSHA256 {
			x := f(AniDBChanged, k.AniDB)
			x.Mirror = o.Mirror
			findings = append(findings, x)
		}
	}
	return findings, evaluated
}
