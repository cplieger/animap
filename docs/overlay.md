# The overlay

The overlay is animap's own set of corrections to Anime-Lists. Each file in `overlay/` patches one Anime-Lists node, named for its AniDB id, for example `overlay/544.json`. The fields it sets use Anime-Lists' own attribute names, so each entry is also the change to propose upstream.

## The accuracy bar

An entry is added only when every value it changes is proven:

- A season or episode mapping is proven when TVDB's official order and AniDB's own episode list agree episode by episode. That means the same count and the same air dates, within a day for time zones.
- A TMDB value changes only where TMDB's season page shows the same episodes.
- A specials mapping is proven only from AniDB's list of specials.
- After the change, no two entries on one TVDB series may claim the same TVDB or TMDB episode.

An entry that rests on a guess, a precedent that does not match exactly, or a source that could not be read is not added.

## What an entry holds

| Field | Meaning |
| --- | --- |
| `anidb_id` | The Anime-Lists node it patches. It must match the file name. |
| `anilist_ids`, `title` | For reading and for issue titles only. |
| `set` | The patched attributes, as Anime-Lists strings. `mapping_list` replaces the whole list with rows in the published shape, and `[]` deletes it. Unnamed attributes stay. |
| `create`, `name` | Set `create` to `true`, with a `name`, to add an entry Anime-Lists does not have. |
| `justification` | One paragraph naming the episodes and dates that prove the change. |
| `evidence` | Links to the AniDB, TVDB, TMDB, AniList or SeaDex pages behind it, https only. |
| `upstream` | The Anime-Lists pull request or issue carrying the same change, or `TODO-PR` until one is filed. Most entries came from [Anime-Lists pull request 629](https://github.com/Anime-Lists/anime-lists/pull/629). |
| `episodes`, `siblings` | The AniDB episode counts and specials of this entry and of every other entry on the same TVDB series. The collision check reads them. |
| `captured` | The fingerprints the drift check compares. `animap overlay capture` writes it. |

`set` may name `tvdbid`, `defaulttvdbseason`, `episodeoffset`, `tmdbtv`, `tmdbseason`, `tmdboffset`, `tmdbid`, `imdbid` and `mapping_list`. An attribute it does not name stays as Anime-Lists has it.

## Fingerprints and drift

`captured` holds the fingerprints taken when the entry was written:

- A SHA-256 of the Anime-Lists node as it was upstream. Titles and notes are left out, so renaming a title is not drift.
- A SHA-256 of TVDB's official episode order for the seasons the entry touches, with the before list itself, read from Sonarr's public metadata service. A film that only TMDB places has no TVDB series and no TVDB fingerprint.

AniDB is not fingerprinted. Its episode list is the evidence an entry is proven against when it is written, and nothing after that reads AniDB.

A daily run compares the fingerprints against today's upstream, and opens one issue per entry and cause:

- When upstream now carries every value the entry sets, the fix has landed. The issue names the entry to delete.
- When the Anime-Lists node changed in any other way, the issue asks for a review of the entry.
- When TVDB's episode order changed, the issue shows the episode lists before and after.

The same run opens one more kind of issue, for `checks/counts.json` (see below), when anime-offline-database starts to list a node that file counts.

An entry stays applied while its issue is open. The issue closes on its own once the cause clears. That happens when the entry is deleted, when it is re-captured after review, or when upstream changes back.

## Specials filed under another anime

AniDB files some short specials as episodes of their main series, for example `S1` of that series, rather than as an anime of their own. For such an AniList entry, anime-offline-database has no AniDB id, and no Anime-Lists node reaches it. A file in `overlay/special-of-parent/` bridges the gap. It is named for the AniList id and holds these fields:

| Field | Meaning |
| --- | --- |
| `kind` | Always `special-of-parent`. |
| `anilist_id` | The AniList entry. It must match the file name. |
| `parent_anidb_id` | The AniDB anime that lists the specials. |
| `specials` | The parent's special numbers, one per AniList episode, consecutive. |
| `parent_anidb_specials` | The parent's AniDB specials with their air dates, copied by the author from AniDB's episode list as `[2, number, "YYYY-MM-DD"]`. It must hold every special in `specials`. |
| `title`, `justification`, `evidence` | As for an entry. |
| `captured` | The parent's Anime-Lists node and TVDB's order for the seasons the specials land on. `animap overlay capture-special` writes it and keeps the other fields. |

The record for the AniList id then takes the parent's TVDB id and the TVDB episode each special lands on. The parent's mapping list places each special, and a special it does not list lands on season 0 episode `S<n>`. A bridge is added only when all of these hold:

- AniList's `PARENT` relation, or failing that `PREQUEL` or `SIDE_STORY`, names an AniList entry that anime-offline-database links to the parent.
- The parent has one run of consecutive specials, one per AniList episode. The first aired within a day of AniList's start date and the last within a day of its end date.
- A special's title matches an AniList title in at least one language, ignoring case and punctuation.
- Each TVDB episode the specials land on aired within a day of the special.
- The collision check passes.

The build skips a bridge whose AniList entry gained an AniDB id of its own, or whose episode count changed, and lists it in its statistics. The daily check opens an issue when the parent's Anime-Lists node or TVDB's order changes. It also opens one when anime-offline-database links the entry itself, so the bridge can go.

## Adding an entry

1. Collect the evidence that meets the bar above, and write `overlay/<anidbid>.json` with everything except `captured`.
2. Record its fingerprints at the current Anime-Lists commit:

   ```sh
   go run ./cmd/animap overlay capture -entry overlay/544.json -list anime-list-master.xml -commit <sha>
   ```

3. Check it against every other entry on the same series:

   ```sh
   go run ./cmd/animap overlay check -list anime-list-master.xml -aod anime-offline-database.jsonl
   ```

   The check also re-proves every bridge's air dates. A bridge's fingerprints come from `go run ./cmd/animap overlay capture-special -bridge overlay/special-of-parent/376.json -list anime-list-master.xml -commit <sha>`.

4. Open a pull request here, and send the same change to [Anime-Lists](https://github.com/Anime-Lists/anime-lists). When that change is merged, the drift check opens the issue that retires the entry.

## Collisions on other series

Anime-Lists itself has collisions on series no overlay entry touches. `checks/collision-baseline.json` records the ones it had when animap started, by series, target episode and the entries claiming it. A build publishes with those and stops on any other one. The file only shrinks, and a pull request that adds to it fails a check. Once a collision is fixed upstream, `go run ./cmd/animap baseline prune` removes it. An overlay entry on a series with a recorded collision makes that collision block, so the series is fixed first.

The check needs the regular episode count of every node on a series it checks. It takes the count from an overlay entry first, then from `checks/counts.json`, then from anime-offline-database. A node with none of the three cannot be checked, because none of its episodes can be placed.

The baseline records the nodes like that which Anime-Lists had when animap started, and a build stops on any other one. On a series an entry touches, a build stops on every such node. Add the node to the entry's `siblings` with its AniDB count, or add a row to `checks/counts.json`, to fix it.

On a series an entry touches, the check also needs the AniDB specials of every node. A special with no row in a node's mapping list lands on season 0 episode `S<n>`. A build stops when a node's specials were never read from AniDB and another node claims a season 0 episode they could land on. Add the node to the entry's `siblings` with its AniDB specials to fix it.

## Episode counts

`checks/counts.json` holds regular episode counts for Anime-Lists nodes whose AniDB id anime-offline-database does not list. It is a JSON list sorted by AniDB id, and each row holds:

| Field | Meaning |
| --- | --- |
| `anidb_id` | The node it counts. |
| `regular_episodes` | The number of regular episodes on AniDB's episode list. Specials, credits and trailers are not counted. |
| `evidence` | The AniDB page of that anime, `https://anidb.net/anime/<id>`. |
| `date` | The day the episode list was read, as `YYYY-MM-DD`. |
| `justification` | One line naming what the episode list shows. |

A row is added only from AniDB's own episode list, counting the regular episodes alone. Once a node is counted, the collision check places its episodes like any other node's. A collision that placement reveals stops the build, so the series is fixed upstream or by an overlay entry before the row lands. The next `go run ./cmd/animap baseline prune` takes the counted node off the baseline's list.

When anime-offline-database starts to list a counted node, the daily check opens an issue titled `counts: AniDB <id> now in anime-offline-database`. It says whether the two counts agree. Remove the row, and the issue closes on the next run. When they disagree, check AniDB's episode list first, because the database's count is used once the row is gone. When the database lists the node with no episode count, the issue says to keep the row, and it updates once the database counts the node.

A new uncounted node on a series no entry touches has no fix in an overlay entry, because an entry needs a value to correct, and the baseline cannot grow. A `checks/counts.json` row fixes it. Otherwise publishing waits until Anime-Lists corrects or drops the node, or anime-offline-database lists its AniDB id. Until then, the previous release stays the latest. After a day, the issue that reports failed publishing opens.
