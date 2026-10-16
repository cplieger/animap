# The watch set

A watch set is a list of AniList ids whose mappings animap checks every day against the latest release. The only watch set is [SeaDex](https://seadex.moe)'s catalogue, configured in `watch/seadex.json`. A fork that does not want it can delete the `watch/` folder.

## What fully mapped means

Each rule reads one record of the latest `animap.json` and nothing else. A watched id is fully mapped when seadex-scout can find its release in Sonarr or Radarr and tell which episodes are the record's. The rules follow how seadex-scout routes and scopes a record (its `internal/mapping`, `internal/match` and `internal/align` packages), first the route, then the scope:

| Record | Route | Fully mapped when | Gap otherwise |
| --- | --- | --- | --- |
| No record for the AniList id | none | never | `no_record` |
| A `MOVIE` with a `tmdb_movie_ids` or `imdb_ids` value | Radarr | always: Radarr decides a movie's scope, whatever its TVDB season says | none |
| Any record with a `tvdb_id`, a `MOVIE` with no Radarr id included | Sonarr | see the scope rows below | see below |
| Any other type with no `tvdb_id` and a `tmdb_movie_ids` value | Radarr | always. Its `imdb_ids` never route it, because TVDB reuses a film's IMDb id on its parent series | none |
| No route, and no `anidb_id` and no `anidb_parent` | none | never, because nothing joins it to Anime-Lists. Without an `anidb_id`, only an [overlay link](overlay.md#entries-filed-under-another-anime) gives a record a `tvdb_id` | `no_anidb` |
| No route, a `MOVIE` | none | never | `movie_no_route` |
| No route, `UNKNOWN` or no type | none | never | `unknown_type` |
| No route, any other type | none | never | `no_tvdb` |

On Sonarr, the scope:

| Record | Fully mapped when | Gap otherwise |
| --- | --- | --- |
| A `tvdb_season` of 1 or more, or `tvdb_absolute` | always, unless its `tvdb_placement` lands every episode on none, when seadex-scout falls back to the whole season | `lands_nowhere` |
| A `tvdb_season` of 0 | it has a `tvdb_placement` that puts every episode on a TVDB special or on none, and at least one on a special | `lands_nowhere` when every episode lands on none; else `season0_unresolved` (`movie_special_unresolved` for a `MOVIE`), or `episode_count_unknown` when the record has no episode count |
| No `tvdb_season` and not `tvdb_absolute` | never | `no_tvdb_season` |

A record filed under season 0 is fully mapped when the release gives it a `tvdb_placement` on TVDB specials. The release does that only when every regular episode has a TVDB episode or is stated to have none. [TVDB placement](schema.md#tvdb-placement) explains the rule. An episode that needs an offset the record does not state leaves a gap, because "the numbers match" and "nobody mapped it" look the same. A placement that lands an episode on a regular TVDB season leaves a gap, because the record's season 0 is then wrong.

The specials of a regular series are not checked, because a record says nothing about them.

## Issues

The run reads SeaDex's whole catalogue at one request a second. It checks that the listing is complete. The ids it collected must equal SeaDex's total, with no repeats. An incomplete listing evaluates nothing that day, so every watch section stays as it was.

A watched record with a gap is a section in its anime's issue of the [daily review](review.md): the issue of its AniDB id, or of its AniList id when it has no AniDB id or an overlay link names it. The section lists each record's gaps and links the AniList, SeaDex and TVDB pages. It clears on its own once a release maps the record fully.

A gap no source can close, because no source carries the work or the published format cannot say it, is [accepted](review.md#accepting-a-finding) as `unmappable` or `inexpressible`. The acceptance lapses when the published record or its gaps change, and the summary issue lists every accepted gap with its reason.

## Closing a gap

A gap is closed by an overlay entry that meets the same accuracy bar as every other entry, with the same drift checks. See [overlay.md](overlay.md). The same change goes to Anime-Lists, and the overlay holds it until that change is merged.

A gap with no AniDB id is often an entry that AniDB files as part of another anime. It can be a special of its main series, a promotion or "other" episode of it, one film of an AniDB anime that holds several films, or a compilation of several AniDB anime. An overlay link to those episodes closes it, see [Entries filed under another anime](overlay.md#entries-filed-under-another-anime). Otherwise it is fixed in anime-offline-database or on AniDB.
