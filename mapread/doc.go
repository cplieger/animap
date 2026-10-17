// Package mapread reads and writes animap.json version 2, the anime id
// mapping between AniDB, AniList, TVDB, TMDB and the services
// anime-offline-database lists.
//
// Decode reads a document under caps and refuses one that breaks a rule it
// checks: the types, the caps and the rules docs/schema.md lists under
// "Order and checks", except the order of entries with no AniList id. It
// does not check an entry's parts against its ids.anidb.
// Source.Latest and Release.Fetch download a release and verify it against
// its .sha256 sidecar. An Index answers the lookups: an AniList entry to
// its placements and TVDB seasons, an AniDB episode to its placements and
// its AniDB episode id, a TVDB or TMDB episode to every AniDB episode on
// it, a TMDB movie to its anime, and a TVDB series to its anime in TVDB
// order. The README states the reading rule every lookup applies.
package mapread
