# The watch set

A watch set is a list of AniList ids whose mappings animap checks every day against the latest release. The only watch set is [SeaDex](https://releases.moe)'s catalogue, configured in `watch/seadex.json`. A fork that does not want it can delete the `watch/` folder.

## What fully mapped means

Each rule reads one record of the latest `animap.json` and nothing else. A watched id is fully mapped when its record passes the rule for its type:

| Record | Fully mapped when | Gap otherwise |
| --- | --- | --- |
| No record for the AniList id | never | `no_record` |
| No `anidb_id` | never, because nothing joins it to Anime-Lists | `no_anidb` |
| A `MOVIE` | it has a TMDB movie or IMDb id for Radarr or a TVDB season for Sonarr, and any TVDB special it is filed as also resolves | `movie_no_route`, `movie_special_unresolved` |
| Filed under TVDB season 0, any other type | every AniDB episode from 1 to `episodes` resolves to a TVDB special | `season0_unresolved`, `episode_count_unknown` |
| Any other type | it has a `tvdb_id` and either a `tvdb_season` of 1 or more or `tvdb_absolute` | `no_tvdb`, `no_tvdb_season` |
| `UNKNOWN` or no type, and no `tvdb_id` | never | `unknown_type` |

An AniDB episode filed under season 0 resolves when a `mapping_list` row with `anidb_season` 1 and `tvdb_season` 0 names it, or when the record has a `tvdb_episode_offset`. A row that maps it to no episode, written `[k]` in the file, counts as resolved, because it states that TVDB has no such episode. A record with no offset and no row is a gap, because "the numbers match" and "nobody mapped it" look the same.

The specials of a regular series are not checked, because a record says nothing about them.

## Issues

The run reads SeaDex's whole catalogue at one request a second. It checks that the listing is complete. The ids it collected must equal SeaDex's total, with no repeats. An incomplete listing evaluates nothing that day.

A watched id with a gap that no list holds gets one issue, titled `watch: AniList <id> (<type>) is not fully mapped`. It lists the gaps and links the AniList, SeaDex, AniDB and TVDB pages. A new SeaDex entry with a gap gets its issue on the first run that sees it. The issue closes on its own once a release maps the entry fully. At most 10 issues open per run, and one dashboard issue lists the rest, with counts by type and gap.

`checks/unmappable.json` lists the gaps that cannot be mapped from these sources, each with its reason. Some have no TVDB series anywhere, some have sources that disagree, and for some the fix belongs to anime-offline-database or AniDB. One pinned issue, "Unmappable SeaDex entries", lists them as a checklist. A ticked item is mapped now and can leave the file. These entries get no issue of their own.

`watch/backlog.json` lists gaps that are known and tracked without an issue each. Each line says what the gap waits for. For example, an anime that is still airing waits for its final AniDB episode list. An entry that gains a gap kind its backlog line does not record gets its own issue.

## Closing a gap

A gap is closed by an overlay entry that meets the same accuracy bar as every other entry, with the same drift checks. See [overlay.md](overlay.md). The same change goes to Anime-Lists, and the overlay holds it until that change is merged.

A gap with no AniDB id is often a special that AniDB files under its main series. A special-of-parent bridge closes it, see [overlay.md](overlay.md). Otherwise it is fixed in anime-offline-database or on AniDB.
