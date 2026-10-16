# The overlay

The overlay is animap's own set of corrections to Anime-Lists. Each file in `overlay/` patches one Anime-Lists node, named for its AniDB id, for example `overlay/544.json`. The fields it sets use Anime-Lists' own attribute names, so each entry is also the change to propose upstream.

## The accuracy bar

An entry is added only when every value it changes is proven:

- A season or episode mapping is proven when TVDB's official order and AniDB's own episode list agree episode by episode. That means the same count and the same air dates, within a day for time zones.
- A TMDB value changes only where TMDB's season page shows the same episodes.
- A specials mapping is proven only from AniDB's list of specials.
- After the change, no episode the change puts on the patched node is claimed by another node.

An entry that rests on a guess, a precedent that does not match exactly, or a source that could not be read is not added.

## What an entry holds

| Field | Meaning |
| --- | --- |
| `anidb_id` | The Anime-Lists node it patches. It must match the file name. |
| `anilist_ids` | The AniList ids anime-offline-database links to the node. For reading only. |
| `title` | The Anime-Lists node's name, or the AniList title of an AniList id the entry names. For reading and for issue titles only. |
| `set` | The patched attributes, as Anime-Lists strings. `mapping_list` replaces the whole list with rows in the published shape, and `[]` deletes it. A row's `offset` of `0` is kept, for a row Anime-Lists writes with `offset="0"`. Unnamed attributes stay. |
| `create`, `name` | Set `create` to `true`, with a `name`, to add an entry Anime-Lists does not have. |
| `justification` | One paragraph naming the episodes and dates that prove the change. |
| `evidence` | Links to the AniDB, TVDB, TMDB, AniList or SeaDex pages behind it, https only, with no user name or password. |
| `upstream` | The Anime-Lists pull request or issue carrying the same change, or `TODO-PR` until one is filed. Most entries came from [Anime-Lists pull request 629](https://github.com/Anime-Lists/anime-lists/pull/629). |
| `captured` | The fingerprints the drift check compares. `animap overlay capture` writes it. |

`set` may name `tvdbid`, `defaulttvdbseason`, `episodeoffset`, `tmdbtv`, `tmdbseason`, `tmdboffset`, `tmdbid`, `imdbid` and `mapping_list`. An attribute it does not name stays as Anime-Lists has it. `tvdbid` is never empty, and `defaulttvdbseason` is empty only beside `tvdbid` `movie`, which is how Anime-Lists writes a film it maps to no TVDB series.

## Fingerprints and drift

`captured` holds the fingerprints taken when the entry was written:

- A SHA-256 of the Anime-Lists node as it was upstream. Titles and notes are left out, so renaming a title is not drift.
- A SHA-256 of TVDB's official episode order for the seasons the entry touches, with the before list itself, read from Sonarr's public metadata service. A film that only TMDB places has no TVDB series and no TVDB fingerprint.

AniDB is not fingerprinted. An entry is proven against AniDB's episode list when it is written. After that, every build reads AniDB's episode lists from the AniDB mirror described under [Episode counts](#episode-counts), so no file in `overlay/` copies them.

The [daily review](review.md) compares the fingerprints against today's upstream, and writes an overlay drift section in the anime's issue, one paragraph per cause:

- When upstream now carries every value the entry sets, the fix has landed. The section names the entry to delete.
- When the Anime-Lists node changed in any other way, the section asks for a review of the entry.
- When TVDB's episode order changed, the section shows the episode lists before and after.

The TVDB order comes from the review's own copy of each series, which it fetches again on every daily run for every series an entry captures. A run that could not fetch the series leaves the TVDB part of the section as it was.

An entry stays applied while its section is open. The section clears on its own once the cause clears. That happens when the entry is deleted, when it is re-captured after review, or when upstream changes back.

## Entries filed under another anime

AniDB files some AniList entries as part of another anime. A special can be `S1` of its main series. A prologue can be a promotion of its series, and a re-edited TV run the "other" episodes of an anime holding its films. A film can be one episode of an anime holding a franchise's films, and a compilation every episode of three AniDB anime. Such an entry has no AniDB id in anime-offline-database, so no Anime-Lists node reaches it. A link in `overlay/links/<anilist_id>.json` names the AniDB episodes the entry is, and holds nothing else.

| Field | Meaning |
| --- | --- |
| `anilist_id` | The AniList entry. It must match the file name, so an AniList id has one link at most. |
| `title` | The AniList title. For reading and for issue titles only. |
| `parts` | The AniDB episodes, in AniList's order. Each part names an `anidb_id` and one of `specials`, `episodes` or `whole`. |
| `justification`, `evidence` | As for an entry. |
| `captured` | The day, the AniDB mirror commit and a SHA-256 of each linked episode's number and titles. `animap overlay capture-link` writes it. |

Each number in `specials` or `episodes` is one AniList episode. A number in `specials` is the episode's number in an Anime-Lists node's season 0, as the node's mapping rows write it: a special keeps its own number, and AniDB's other episode kinds are numbered from 101 for credits, 201 for trailers and promotions, 301 for parodies and 401 for other episodes. `[401, 402]` names AniDB's O1 and O2. A `"whole": true` part is one AniList episode made of every regular episode of that anime. No AniDB episode is named twice.

A link of one part listing every regular episode of its anime, 1 to N, is an identity. The AniList record then takes that AniDB id, its count and its node's facts, and the AniDB-only record of the anime goes.

Any other link gives the AniList record the TVDB series and season, one `mapping_list` row and the `tvdb_placement` of its episodes, and no TMDB, movie or IMDb id. A link to season 0 episodes of one anime, specials or the other kinds, also sets `anidb_parent`. Where anime-offline-database has no record for the AniList id, the build creates one with the linked episode count.

Each linked episode lands where its anime's patched node places it on TVDB, the default included, since the title proof stands in for an explicit offset. On a node in TVDB's absolute order, the number resolves through the TVDB order that anime's overlay entry captured, and a link with no such entry is refused. Each AniList episode lands on one TVDB episode, and the link on one series and season.

A link is added only when all of these hold:

- AniList's `PARENT` relation, or failing that `PREQUEL` or `SIDE_STORY`, names an AniList entry that anime-offline-database links to the anime, or anime-offline-database carries the anime's AniDB id without an AniList link.
- The parts name one AniDB episode, or one whole anime, per AniList episode, in AniList's order.
- Each episode's title matches an AniList title in at least one language, ignoring case and punctuation.
- Each episode lands on the TVDB episode with the same title, and the episodes keep their order on TVDB.
- The collision check passes.

Titles and order decide. Air dates are evidence for the justification and never a condition, because AniDB, AniList and TVDB can date one episode differently, for example by its disc release and by its broadcast, or leave it undated.

`animap overlay check` and every build stop on a link the AniDB mirror and the patched list refute. AniDB must list each episode with its kind, and a regular one must be within the [episode count](#episode-counts). No AniDB episode may be in two links, and no TVDB episode taken by two linked AniList episodes. An identity's AniDB id must not be one anime-offline-database gives another AniList id. The collision check then covers the linked episodes.

The build skips a link, and lists it in its statistics, when anime-offline-database has since given the AniList id an AniDB id, or when its record counts other than the linked episodes.

The daily review raises an overlay drift section in the AniList id's issue when anime-offline-database links the AniList id itself, so the link can go, and when the AniDB mirror renumbers, retitles or drops a linked episode, so the link is proven again and re-captured. The date check covers a linked regular episode or special as it covers every mapped one, on TVDB and TMDB, and also raises a linked special or other episode whose default target does not exist. A linked credit, promotion, parody or other episode has no air date in the mirror, so the date check only checks that its target exists; the link's own fingerprint covers its number and titles. A date check finding keeps a digest of its targets' titles, so a retitle under an accepted finding raises it again; overlay drift compares no titles, so a TVDB rename that keeps the number and air date raises no drift.

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

   A link's fingerprint comes from `go run ./cmd/animap overlay capture-link -link overlay/links/376.json -mirror .cache/anidb.json`.

4. Check it against every other entry on the same series. It also checks every link against the AniDB mirror and the patched list:

   ```sh
   go run ./cmd/animap overlay check -list anime-list-master.xml -aod anime-offline-database.jsonl -mirror .cache/anidb.json
   ```

5. Open a pull request here, and send the same change to [Anime-Lists](https://github.com/Anime-Lists/anime-lists). When that change is merged, the drift check opens the issue that retires the entry.

## Collisions Anime-Lists already has

A node is changed when an overlay entry patches or creates it, or when a link that is not an identity names its episodes. A series is touched when a changed node sits on it. A changed node's claim on an episode is new when the overlay put it there:

- every claim of a node an entry creates, or moves to another TVDB series
- a claim on an episode where Anime-Lists' own node placed a different episode of that anime, or none
- every TVDB episode a linked episode lands on

A collision stops the build when one of its claims is new, even when the baseline records it. A collision the overlay leaves exactly as Anime-Lists has it belongs to Anime-Lists, whether or not its nodes are changed.

`checks/collision-baseline.json` records the collisions Anime-Lists has, by series, target episode and the nodes claiming each one. A build publishes with those and stops on any other one. The check measures them from regular episodes and the specials a mapping list row places, the same way on every series, so the baseline does not depend on which series the overlay touches. An absolute-numbered node's episodes are placed through the TVDB episode list an entry on the same series captured, and are not placed on a series with no such entry. Once a collision is fixed, upstream or by an entry, `go run ./cmd/animap baseline prune` removes it.

A collision exists only for the episode counts that placed it. So the baseline names its counting rule in `basis`, and the rule's name changes whenever the way animap counts episodes changes. While the name stays the same, the file only shrinks, and a pull request that adds to it fails a check. When the name changes, `go run ./cmd/animap baseline rebase` measures the baseline again under the new rule, in the same pull request. A build stops on a baseline measured under another rule.

The check needs the regular episode count of every node on a series it checks. A node with no count cannot be checked, because none of its episodes can be placed. The baseline records the nodes like that which Anime-Lists has, and a build stops on any other one. On a touched series a build stops on every such node, as it does on the TMDB show of a changed node with no TVDB series. A row in `checks/counts.json` fixes it.

The check also places the AniDB specials of every node on a touched series, and of every node claiming an episode of a TMDB show that a node of a touched series, or a changed node with no TVDB series, sits on. A special with no row in a node's mapping list lands on season 0 episode `S<n>`. A special that lands on an episode a changed node newly claims stops the build, and so does a changed node's special that an entry newly places on an episode another node claims. A collision through a special that no row places is not recorded in the baseline, because the baseline reads no specials from the AniDB mirror. So a row an entry adds is a new claim even where it places a special on the episode the special already defaulted to, and a collision through that row stops the build. A build also stops when the AniDB mirror lists no episodes for a node there, and the node is changed or a new claim takes a season 0 episode its specials could land on.

## Episode counts

Every regular episode count animap uses comes from one rule, and the collision check and the published `episodes` field use the same number:

1. A row in `checks/counts.json`, when there is one.
2. Otherwise the AniDB mirror's count, when the mirror records the anime's end date and lists at least one regular episode.
3. Otherwise anime-offline-database's count. That covers an anime with no end date yet, whose mirror copy can be two weeks old, an anime the mirror lists with no regular episode, and an anime the mirror does not list.

The AniDB mirror is [AnimeAggregations](https://github.com/notseteve/AnimeAggregations), which republishes AniDB's data as one file per anime, in a new snapshot on the 1st and 15th of each month. `.github/workflows/publish.yaml` pins it to one commit. Each build reads the whole snapshot once. It keeps, for each anime, the air date of each regular episode, each special's number and air date, the number of each credit, promotion, parody and other episode under its Anime-Lists number without its air date (see [Entries filed under another anime](#entries-filed-under-another-anime)), and whether AniDB records its end date. A credit, promotion or parody past its kind's 100th, and an episode of these kinds on a special's number, numbered twice or below 1, cannot be told apart in that numbering and is not kept; unlike a bad special, it does not refuse the file. AniDB can record an end date before the last episode airs. A file that is not JSON, names another anime, or numbers its regular episodes other than 1 to N is refused, and that anime then counts as one the mirror does not list. A special numbered twice, or a date that is not `YYYY-MM-DD`, is refused the same way.

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
