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
| `anilist_ids` | The AniList ids anime-offline-database links to the node. For reading only. |
| `title` | The Anime-Lists node's name, or the AniList title of an AniList id the entry names. For reading and for issue titles only. |
| `set` | The patched attributes, as Anime-Lists strings. `mapping_list` replaces the whole list with rows in the published shape, and `[]` deletes it. Unnamed attributes stay. |
| `create`, `name` | Set `create` to `true`, with a `name`, to add an entry Anime-Lists does not have. |
| `justification` | One paragraph naming the episodes and dates that prove the change. |
| `evidence` | Links to the AniDB, TVDB, TMDB, AniList or SeaDex pages behind it, https only. |
| `upstream` | The Anime-Lists pull request or issue carrying the same change, or `TODO-PR` until one is filed. Most entries came from [Anime-Lists pull request 629](https://github.com/Anime-Lists/anime-lists/pull/629). |
| `captured` | The fingerprints the drift check compares. `animap overlay capture` writes it. |

`set` may name `tvdbid`, `defaulttvdbseason`, `episodeoffset`, `tmdbtv`, `tmdbseason`, `tmdboffset`, `tmdbid`, `imdbid` and `mapping_list`. An attribute it does not name stays as Anime-Lists has it.

## Fingerprints and drift

`captured` holds the fingerprints taken when the entry was written:

- A SHA-256 of the Anime-Lists node as it was upstream. Titles and notes are left out, so renaming a title is not drift.
- A SHA-256 of TVDB's official episode order for the seasons the entry touches, with the before list itself, read from Sonarr's public metadata service. A film that only TMDB places has no TVDB series and no TVDB fingerprint.

AniDB is not fingerprinted. An entry is proven against AniDB's episode list when it is written. After that, every build reads AniDB's episode lists from the AniDB mirror described under [Episode counts](#episode-counts), so no file in `overlay/` copies them.

A daily run compares the fingerprints against today's upstream, and opens one issue per entry and cause:

- When upstream now carries every value the entry sets, the fix has landed. The issue names the entry to delete.
- When the Anime-Lists node changed in any other way, the issue asks for a review of the entry.
- When TVDB's episode order changed, the issue shows the episode lists before and after.

An entry stays applied while its issue is open. The issue closes on its own once the cause clears. That happens when the entry is deleted, when it is re-captured after review, or when upstream changes back.

## Specials filed under another anime

AniDB files some short specials as episodes of their main series, for example `S1` of that series, rather than as an anime of their own. For such an AniList entry, anime-offline-database has no AniDB id, and no Anime-Lists node reaches it. A file in `overlay/special-of-parent/` bridges the gap. It is named for the AniList id and holds these fields:

| Field | Meaning |
| --- | --- |
| `kind` | Always `special-of-parent`. |
| `anilist_id` | The AniList entry. It must match the file name. |
| `parent_anidb_id` | The AniDB anime that lists the specials. |
| `specials` | The parent's special numbers, one per AniList episode, consecutive. |
| `title` | The AniList title of `anilist_id`. For reading and for issue titles only. |
| `justification`, `evidence` | As for an entry. |
| `captured` | The parent's Anime-Lists node and TVDB's order for the seasons the specials land on. `animap overlay capture-special` writes it and keeps the other fields. |

The parent's specials and their air dates come from the AniDB mirror at build time, so a bridge holds no copy of them.

The record for the AniList id then takes the parent's TVDB id and the TVDB episode each special lands on. It takes no TMDB value. The parent's mapping list places each special, and a special it does not list lands on season 0 episode `S<n>`. A bridge is added only when all of these hold:

- AniList's `PARENT` relation, or failing that `PREQUEL` or `SIDE_STORY`, names an AniList entry that anime-offline-database links to the parent.
- The parent has one run of consecutive specials, one per AniList episode. The first aired within a day of AniList's start date and the last within a day of its end date.
- A special's title matches an AniList title in at least one language, ignoring case and punctuation.
- Each TVDB episode the specials land on aired within a day of the special.
- Where the parent's node has a TMDB show, each TMDB episode the specials land on aired within a day of the special too.
- The collision check passes.

The build skips a bridge whose AniList entry gained an AniDB id of its own, or whose episode count changed, and lists it in its statistics. The daily check opens an issue when the parent's Anime-Lists node or TVDB's order changes. It also opens one when anime-offline-database links the entry itself, so the bridge can go.

## Adding an entry

1. Collect the evidence that meets the bar above, and write `overlay/<anidbid>.json` with everything except `captured`.
2. Keep AniDB's episode lists from the AniDB mirror, at the commit `.github/workflows/publish.yaml` pins as `MIRROR_COMMIT`. The archive goes straight into the command, and only episode numbers and air dates reach the disk:

   ```sh
   mkdir -p .cache
   curl -fsSL https://codeload.github.com/notseteve/AnimeAggregations/tar.gz/<mirror commit> \
     | go run ./cmd/animap mirror extract -commit <mirror commit> -out .cache/anidb.json
   ```

3. Record the entry's fingerprints at the current Anime-Lists commit:

   ```sh
   go run ./cmd/animap overlay capture -entry overlay/544.json -list anime-list-master.xml -commit <sha> -mirror .cache/anidb.json
   ```

   A bridge's fingerprints come from `go run ./cmd/animap overlay capture-special -bridge overlay/special-of-parent/376.json -list anime-list-master.xml -commit <sha> -mirror .cache/anidb.json`.

4. Check it against every other entry on the same series. The check also re-proves every bridge's air dates:

   ```sh
   go run ./cmd/animap overlay check -list anime-list-master.xml -aod anime-offline-database.jsonl -mirror .cache/anidb.json
   ```

5. Open a pull request here, and send the same change to [Anime-Lists](https://github.com/Anime-Lists/anime-lists). When that change is merged, the drift check opens the issue that retires the entry.

## Collisions on other series

Anime-Lists itself has collisions on series no overlay entry touches. `checks/collision-baseline.json` records them by series, target episode and the entries claiming each one. A build publishes with those and stops on any other one. Once a collision is fixed upstream, `go run ./cmd/animap baseline prune` removes it. An overlay entry on a series with a recorded collision makes that collision block, so the series is fixed first.

A collision exists only for the episode counts that placed it. So the baseline names its counting rule in `basis`, and the rule's name changes whenever the way animap counts episodes changes. While the name stays the same, the file only shrinks, and a pull request that adds to it fails a check. When the name changes, `go run ./cmd/animap baseline rebase` measures the baseline again under the new rule, in the same pull request. A build stops on a baseline measured under another rule.

The check needs the regular episode count of every node on a series it checks. A node with no count cannot be checked, because none of its episodes can be placed. The baseline records the nodes like that which Anime-Lists has, and a build stops on any other one. On a series an entry or a bridge touches, a build stops on every such node, and a row in `checks/counts.json` fixes it.

On a series an entry or a bridge touches, the check also places the AniDB specials of every node. A special with no row in a node's mapping list lands on season 0 episode `S<n>`. A build stops when the AniDB mirror lists no episodes for a node there and another node claims a season 0 episode its specials could land on.

## Episode counts

Every regular episode count animap uses comes from one rule, and the collision check and the published `episodes` field use the same number:

1. A row in `checks/counts.json`, when there is one.
2. Otherwise the AniDB mirror's count, when the mirror records the anime's end date and lists at least one regular episode.
3. Otherwise anime-offline-database's count. That covers an anime with no end date yet, whose mirror copy can be two weeks old, an anime the mirror lists with no regular episode, and an anime the mirror does not list.

The AniDB mirror is [AnimeAggregations](https://github.com/notseteve/AnimeAggregations), which republishes AniDB's data as one file per anime, in a new snapshot on the 1st and 15th of each month. `.github/workflows/publish.yaml` pins it to one commit. Each build reads the whole snapshot once. It keeps, for each anime, the air date of each regular episode, each special's number and air date, and whether AniDB records its end date. AniDB can record an end date before the last episode airs. A file that is not JSON, names another anime, or numbers its regular episodes other than 1 to N is refused, and that anime then counts as one the mirror does not list. A special numbered twice, or a date that is not `YYYY-MM-DD`, is refused the same way.

`checks/counts.json` holds the counts no source gets right. It is a JSON list sorted by AniDB id, and it stays empty while the sources count every node correctly. Each row holds:

| Field | Meaning |
| --- | --- |
| `anidb_id` | The node it counts. |
| `regular_episodes` | The number of regular episodes on AniDB's episode list. Specials, credits and trailers are not counted. |
| `evidence` | The AniDB page of that anime, `https://anidb.net/anime/<id>`. |
| `date` | The day the episode list was read, as `YYYY-MM-DD`. |
| `justification` | One line naming what the episode list shows. |

A row is added only from AniDB's own episode list, counting the regular episodes alone. Once a node is counted, the collision check places its episodes like any other node's. A collision that placement reveals stops the build, so the series is fixed upstream or by an overlay entry before the row lands. The next `go run ./cmd/animap baseline prune` takes the counted node off the baseline's list.

A row whose count equals what the sources give without it is named under `counts_redundant` in the build's stats and log. Delete the row then.

A new uncounted node on a series no entry touches has no fix in an overlay entry, because an entry needs a value to correct, and the baseline cannot grow. A `checks/counts.json` row fixes it. Otherwise publishing waits until Anime-Lists corrects or drops the node, or a source counts it. Until then, the previous release stays the latest. After a day, the issue that reports failed publishing opens.
