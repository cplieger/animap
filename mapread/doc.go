// Package mapread reads and writes animap.json version 2, the anime id
// mapping between AniDB, AniList, MyAnimeList, TheTVDB, TMDB movies and
// IMDb.
//
// Decode reads a document under caps and refuses one that breaks a rule
// the README lists under "Decode rules", or one in the earlier version 2
// shape; Encode writes its canonical form and ContentHash hashes what a
// release publishes as data. Source.Latest and Release.Fetch download a
// release and verify it against its .sha256 sidecar. An Index answers the
// lookups: an AniList entry to its episodes, routes, movies and TVDB
// seasons, an AniDB episode to its TVDB placements with their TVDB episode
// ids, its movies and its AniDB episode id, a TVDB episode, a TVDB episode
// id or a TMDB movie to every AniDB episode on it, and a TVDB series to
// its anime in TVDB order. The README states the reading rule every lookup
// applies.
package mapread
