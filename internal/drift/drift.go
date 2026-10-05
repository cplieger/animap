// Package drift decides whether upstream moved under animap's own data: the
// Anime-Lists node an overlay entry patches, the TVDB layout it maps onto,
// and anime-offline-database starting to carry a node checks/counts.json
// counts.
package drift

import (
	"strconv"

	"github.com/cplieger/animap/internal/animelists"
	"github.com/cplieger/animap/internal/counts"
	"github.com/cplieger/animap/internal/overlay"
	"github.com/cplieger/animap/internal/skyhook"
)

// Cause is why an entry or a counts row needs a human.
type Cause string

const (
	// NodeChanged is a node that moved without taking the entry's values.
	NodeChanged Cause = "node-changed"
	// Landed is a node that now carries every value the entry sets.
	Landed Cause = "landed"
	// TVDBLayout is a changed official order, or a series SkyHook dropped.
	TVDBLayout Cause = "tvdb-layout"
	// CountInDatabase is a counted node anime-offline-database now carries.
	CountInDatabase Cause = "count-in-database"
)

// Finding is one cause on one entry or counts row. Before and After are set
// for TVDBLayout, where a nil After means the series is gone from SkyHook;
// Counted and DatabaseEpisodes for CountInDatabase, where a
// DatabaseEpisodes of 0 means the database gives the node no count.
type Finding struct {
	Key              string            `json:"key"`
	Path             string            `json:"path"`
	Cause            Cause             `json:"cause"`
	Title            string            `json:"title"`
	Before           []skyhook.Episode `json:"before,omitempty"`
	After            []skyhook.Episode `json:"after,omitempty"`
	AniDB            int               `json:"anidb"`
	AniList          int               `json:"anilist,omitempty"`
	Linked           int               `json:"linked,omitempty"`
	Counted          int               `json:"counted,omitempty"`
	DatabaseEpisodes int               `json:"database_episodes,omitempty"`
}

// Subject is what one decision is about: an overlay entry, a
// special-of-parent bridge or a counts row. The zero Subject decides
// nothing.
type Subject struct {
	entry  *overlay.Entry
	bridge *overlay.Bridge
	row    *counts.Row
}

// OfEntry is the subject for an overlay entry.
func OfEntry(e *overlay.Entry) Subject { return Subject{entry: e} }

// OfBridge is the subject for a special-of-parent bridge.
func OfBridge(b *overlay.Bridge) Subject { return Subject{bridge: b} }

// OfRow is the subject for a checks/counts.json row.
func OfRow(r *counts.Row) Subject { return Subject{row: r} }

// Observation is what one run saw upstream; each subject reads only its own
// fields. Node nil means the node is absent. Layout nil means SkyHook could
// not be read, unless LayoutAbsent says it answered 404. Linked is the
// AniDB id the latest release gives a bridge's AniList entry. InDatabase
// says anime-offline-database carries a row's AniDB id, DatabaseEpisodes
// is its count there. Each *Read flag says the source was looked at.
type Observation struct {
	Node             *animelists.Node
	Layout           []skyhook.Episode
	Linked           int
	DatabaseEpisodes int
	LayoutRead       bool
	LayoutAbsent     bool
	LinkedRead       bool
	DatabaseRead     bool
	InDatabase       bool
}

// Keys for one entry. They are the issue identities, so they never change.
type Keys struct {
	Node, Landed, TVDB string
}

// All lists the three keys.
func (k Keys) All() []string { return []string{k.Node, k.Landed, k.TVDB} }

// Prefix starts every drift key, and no other key.
const Prefix = "drift:"

// KeysOf returns the keys of the entry for AniDB id anidbID.
func KeysOf(anidbID int) Keys {
	id := strconv.Itoa(anidbID)
	return Keys{Node: Prefix + "node:" + id, Landed: Prefix + "landed:" + id, TVDB: Prefix + "tvdb:" + id}
}

// SpecialKeys are the issue keys of the bridge for AniList id al.
func SpecialKeys(al int) Keys {
	id := Prefix + "special:" + strconv.Itoa(al) + ":"
	return Keys{Node: id + "node", Landed: id + "landed", TVDB: id + "tvdb"}
}

// CountKey is the issue key of the checks/counts.json row for anidbID.
func CountKey(anidbID int) string { return Prefix + "counts:" + strconv.Itoa(anidbID) }

// Decide returns the findings for one subject and the keys this
// observation had authority over. A source that was not read leaves its
// key unevaluated, so an open issue for it is not closed by a run that
// could not look.
func Decide(s Subject, o *Observation) (findings []Finding, evaluated []string) {
	switch {
	case s.entry != nil:
		return decideEntry(s.entry, o)
	case s.bridge != nil:
		return decideBridge(s.bridge, o)
	case s.row != nil:
		return decideRow(s.row, o)
	}
	return nil, nil
}

// decideEntry: an entry with no TVDB fingerprint has authority over that
// key without a read, because nothing it holds can drift there.
func decideEntry(e *overlay.Entry, o *Observation) (findings []Finding, evaluated []string) {
	k := KeysOf(e.AniDBID)
	evaluated = append(make([]string, 0, 3), k.Node, k.Landed)
	if animelists.HashOf(o.Node) != e.Captured.NodeSHA256 {
		cause, key := NodeChanged, k.Node
		if overlay.Landed(o.Node, e) {
			cause, key = Landed, k.Landed
		}
		findings = append(findings, Finding{Key: key, Cause: cause, AniDB: e.AniDBID, Title: e.Title, Path: e.Path()})
	}
	if c := e.Captured.TVDB; c == nil || o.LayoutRead || o.LayoutAbsent {
		evaluated = append(evaluated, k.TVDB)
		if c != nil && skyhook.Hash(o.Layout) != c.SHA256 {
			findings = append(findings, Finding{Key: k.TVDB, Cause: TVDBLayout, AniDB: e.AniDBID, Title: e.Title, Path: e.Path(), Before: c.Episodes, After: o.Layout})
		}
	}
	return findings, evaluated
}

// decideBridge: a bridge has landed when anime-offline-database links the
// AniList entry to an AniDB id of its own, because the join then no longer
// needs it.
func decideBridge(b *overlay.Bridge, o *Observation) (findings []Finding, evaluated []string) {
	k := SpecialKeys(b.AniListID)
	f := func(c Cause, key string) Finding {
		return Finding{Key: key, Cause: c, AniDB: b.ParentAniDBID, AniList: b.AniListID, Title: b.Title, Path: b.Path()}
	}
	evaluated = append(make([]string, 0, 3), k.Node)
	if animelists.HashOf(o.Node) != b.Captured.NodeSHA256 {
		findings = append(findings, f(NodeChanged, k.Node))
	}
	if o.LinkedRead {
		evaluated = append(evaluated, k.Landed)
		if o.Linked > 0 {
			l := f(Landed, k.Landed)
			l.Linked = o.Linked
			findings = append(findings, l)
		}
	}
	if o.LayoutRead || o.LayoutAbsent {
		evaluated = append(evaluated, k.TVDB)
		if skyhook.Hash(o.Layout) != b.Captured.TVDB.SHA256 {
			l := f(TVDBLayout, k.TVDB)
			l.Before, l.After = b.Captured.TVDB.Episodes, o.Layout
			findings = append(findings, l)
		}
	}
	return findings, evaluated
}

// decideRow: a row is drift as soon as the database carries its AniDB id,
// even with no count there, so a human sees the id arrive.
func decideRow(r *counts.Row, o *Observation) (findings []Finding, evaluated []string) {
	if !o.DatabaseRead {
		return nil, nil
	}
	k := CountKey(r.AniDBID)
	if o.InDatabase {
		findings = append(findings, Finding{
			Key: k, Cause: CountInDatabase, AniDB: r.AniDBID, Path: counts.Path,
			Counted: r.RegularEpisodes, DatabaseEpisodes: o.DatabaseEpisodes,
		})
	}
	return findings, []string{k}
}
