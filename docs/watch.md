# The watch set

A watch set is a list of AniList ids whose mappings animap checks every day against the latest release. The only watch set is [SeaDex](https://seadex.moe)'s catalogue, configured in `watch/seadex.json`. A fork that does not want it can delete the `watch/` folder.

## What fully mapped means

Each rule reads one AniList entry of the latest `animap.json`, and the anime rows its parts name, and nothing else. A watched id is fully mapped when seadex-scout can find its release in Sonarr or Radarr and tell which episodes are the entry's. The rules follow how seadex-scout routes and scopes an entry (its `internal/mapping`, `internal/match` and `internal/align` packages), first the route, then the scope. The entry's TVDB series and TMDB show are those of the anime its parts name, and its TMDB movie and IMDb ids are those of an anime a part names whole:

| Entry | Route | Fully mapped when | Gap otherwise |
| --- | --- | --- | --- |
| No entry for the AniList id | none | never | `no_record` |
| A `MOVIE` with a TMDB movie or IMDb id | Radarr | always: Radarr decides a movie's scope, whatever its TVDB placement says | none |
| Any entry with exactly one TVDB series, a `MOVIE` with no Radarr id included | Sonarr | see the scope rows below | see below |
| Any other type with no single TVDB series and a TMDB movie id | Radarr | always. Its IMDb ids never route it, because TVDB reuses a film's IMDb id on its parent series | none |
| No Radarr route, and two or more TVDB series | none | never: seadex-scout searches Sonarr by one series | `several_tvdb_series` |
| No route, and no parts | none | never, because nothing joins it to Anime-Lists: anime-offline-database names no AniDB id, or two, and no [overlay link](overlay.md#entries-filed-under-another-anime) names its episodes | `no_anidb` |
| No route, a `MOVIE` | none | never | `movie_no_route` |
| No route, `UNKNOWN` or no type | none | never | `unknown_type` |
| No route, any other type | none | never | `no_tvdb` |

On Sonarr, the scope reads each of the entry's episodes on TVDB, as [Reading an episode](schema.md#reading-an-episode) describes. An entry episode made of several AniDB episodes, a combined part, lands wherever any of them lands, rests on a default on a season when any of them lands there by default, and has nothing stated when none lands and one has nothing stated:

| Entry's episodes | Fully mapped when | Gap otherwise |
| --- | --- | --- |
| None, because nothing counts the anime's episodes yet | never | `episode_count_unknown` |
| Every one lands nowhere | never | `lands_nowhere` |
| Any lands on a season of 1 or more, by a mapping or by default | always | none |
| Some have nothing stated and none lands anywhere | always: seadex-scout compares the whole series | none |
| They land only on season 0 | every episode is stated, not by default, on a TVDB special or nowhere | `season0_unresolved`, or `movie_special_unresolved` for a `MOVIE` |

An entry on TVDB specials is fully mapped only when every episode has a stated TVDB special or is stated to land nowhere. A special placed only by Anime-Lists' default leaves a gap, because "the numbers match" and "nobody mapped it" look the same.

The specials of a regular series are not checked, because the entry is its regular episodes.

## Issues

The run reads SeaDex's whole catalogue at one request a second. It checks that the listing is complete. The ids it collected must equal SeaDex's total, with no repeats. An incomplete listing evaluates nothing that day, so every watch section stays as it was. So does a run without the AniDB mirror or anime-offline-database while overlay links exist, because which links the build applies decides each section's issue.

A watched entry with a gap is a section in its anime's issue of the [daily review](review.md): the issue of the AniDB anime it is, or of its AniList id when it is not one whole anime or an overlay link the build applies names it. The section lists each entry's gaps and links the AniList, SeaDex and TVDB pages. It clears on its own once a release maps the entry fully.

A gap no source can close, because no source carries the work or the published format cannot say it, is [accepted](review.md#accepting-a-finding) as `unmappable` or `inexpressible`. The acceptance lapses when what the watch reads of the entry or its gaps change, and the summary issue lists every accepted gap with its reason.

## Closing a gap

A gap is closed by an overlay entry that meets the same accuracy bar as every other entry, with the same drift checks. See [overlay.md](overlay.md). The same change goes to Anime-Lists, and the overlay holds it until that change is merged.

A gap with no AniDB id is often an entry that AniDB files as part of another anime. It can be a special of its main series, a promotion or "other" episode of it, one film of an AniDB anime that holds several films, or a compilation of several AniDB anime. An overlay link to those episodes closes it, see [Entries filed under another anime](overlay.md#entries-filed-under-another-anime). Otherwise it is fixed in anime-offline-database or on AniDB.
