# animap.json schema

`animap.json` is one minified UTF-8 JSON document. This page describes version 2. A machine-readable copy is [animap.schema.json](animap.schema.json), a JSON Schema (draft 2020-12). The Go module [mapread](../mapread/README.md) reads and checks the file, verifies a download and answers the lookups below.

The file holds two tables. `anime` has one row per AniDB anime: its ids on TVDB, TMDB and IMDb, the episodes AniDB lists with their AniDB episode ids, and where each episode lands on TVDB and on TMDB. `entries` has one row per AniList id that anime-offline-database or an overlay link names, merging every database entry that names it, then one row per database entry with no AniList id and at least one published service id. Each row holds its ids on every service the database lists, and which AniDB episodes it is.

## Top level

The members appear in this order, and `version` is always first, so a reader can refuse an unknown version before it reads a table.

| Member | Type | Meaning |
| --- | --- | --- |
| `version` | integer | The format version, `2`. Only a breaking change raises it. A member added within version 2 is extra, and a reader ignores members it does not know, at every level. |
| `generated_at` | string | When the file was built: an RFC 3339 time in UTC. The build writes it with `Z`. |
| `sources` | object | Exactly what the build read. See below. |
| `attribution` | object | The licence of the data and the notice it requires. |
| `anime` | array | One row per AniDB anime, by `anidb_id`. See [Anime rows](#anime-rows). |
| `entries` | array | The entries. See [Entries](#entries). |

`sources` names four inputs:

- `anime_offline_database`: the repository, the release tag, the asset name and the asset's SHA-256.
- `anime_lists`: the repository, the commit and the file name of `anime-list-master.xml`.
- `anidb_mirror`: the repository and the commit of the AniDB mirror read.
- `overlay`: how many overlay entries were applied, the number of overlay links applied as `links`, which is absent when there are none, and a SHA-256 of the whole overlay set.

`attribution` carries `license` (`ODbL-1.0`), `contents_license` (`DbCL-1.0`), a URL for each, and `notice`, the sentence to show wherever you redistribute the data.

## Anime rows

An AniDB id has a row when anime-offline-database, the AniDB mirror, Anime-Lists or an overlay link names it, and the row has something to say beyond its id. Every member except `anidb_id` is left out when it has no value.

| Field | Type | Meaning |
| --- | --- | --- |
| `anidb_id` | integer | The AniDB id. |
| `type` | string | `TV`, `MOVIE`, `OVA`, `ONA`, `SPECIAL` or `UNKNOWN`, from the first anime-offline-database entry that names this AniDB id. |
| `tvdb_id` | integer | The TheTVDB series id. Anime-Lists markers such as `movie` or `OVA` give no `tvdb_id`. |
| `tmdb_tv_id` | integer | The TMDB TV series id. |
| `tmdb_movie_ids` | array of integers | TMDB movie ids, in Anime-Lists' order. Each names the whole anime. |
| `imdb_ids` | array of strings | IMDb ids, each `tt` followed by 7 to 10 digits. Each names the whole anime. |
| `last` | object | The highest episode number the file places, per kind: `regular`, `special`, `credit`, `promotion`, `parody` and `other`. |
| `episodes_listed` | `true` | The AniDB mirror lists this anime's episodes, so the runs name every episode AniDB lists with its AniDB episode id. Absent when the mirror has no list for the anime, or a file animap refuses, for example one listing an episode twice. |
| `episodes` | array of runs | The episodes AniDB lists and where episodes land. See [Runs](#runs). |

Episode numbers are AniDB's own, per kind: credit 1 is `{"kind": "credit", "start": 1}`. `last.regular` is the regular episode count from the rule in [Episode counts](overlay.md#episode-counts): a `checks/counts.json` row, else AniDB's count for an anime whose end date AniDB records, else anime-offline-database's. AniDB can record an end date before the last episode airs, and can list regular episodes past that count, for example an airing anime whose count comes from anime-offline-database. For every other kind, `last` is the highest number the AniDB mirror lists in that kind, or the highest an Anime-Lists row names, whichever is higher. A highest number, not a count, because AniDB's numbering can skip.

## Runs

A run is consecutive AniDB episodes of one kind that land together:

```json
{ "kind": "regular", "start": 13, "end": 15,
  "anidb_episode_ids": [ 98113, 98114, 98115 ],
  "tvdb": [ { "season": 3, "episode": 13 } ],
  "tmdb": [ { "season": 3, "episode": 13 } ] }
```

| Field | Type | Meaning |
| --- | --- | --- |
| `kind` | string | `regular`, `special`, `credit`, `promotion`, `parody` or `other`. |
| `start`, `end` | integer | The first and last AniDB episode number, in that kind, with `start` at most `end`. `end` is at most `last` of the kind, except on a run of regular episodes AniDB lists past `last.regular`, which states no site. |
| `anidb_episode_ids` | array of integers | One AniDB episode id per episode, `start` first, when AniDB lists the run's episodes. Absent when it lists none of them. A run never mixes the two. |
| `tvdb` | array of targets | Where the episodes land on the row's `tvdb_id`. `[]` means they land nowhere there. Never present on a row with no `tvdb_id`. |
| `tmdb` | array of targets | The same on the row's `tmdb_tv_id`. |

A target is `{"season": S, "episode": E}` in the site's official order, never an absolute number. Episode `start` plus `i` lands on season `S`, episode `E` plus `i`, for every target in the list. A target carries `"default": true` when it rests only on Anime-Lists' default: the node's default season with no offset written and no mapping row, or, for a special AniDB lists or a row names, its own number in season 0 with no row covering it. Anime-Lists reads an absent offset as "the numbers match". animap publishes that guess flagged, never as proven.

- Two or more targets mean each episode spans that many site episodes, as with a double episode.
- Two AniDB episodes on one site episode appear as two runs that name it, on one anime or on two. A reader sees the ambiguity and can refuse it.

### Reading an episode

For episode `n` of kind `k` of an anime, on a site the row has an id for:

| The file has | It means |
| --- | --- |
| no anime row | animap knows nothing about the anime |
| a run covering `n`, with targets | it lands there: stated by a mapping row, a written offset, an overlay link or an overlay entry's captured TVDB order, or by default where a target says `default` |
| a run covering `n`, with `[]` for the site | it lands nowhere on that site |
| a run covering `n`, with no member for the site | nothing is stated for that site |
| no run covering `n`, and `n` from 1 to `last[k]` | it lands nowhere, on every site the row has an id for |
| no run covering `n`, `n` past `last[k]` | with `episodes_listed`, AniDB lists no such episode and it lands nowhere. Without it, not an episode the file places |

What AniDB's list says of the same episode:

| The file has | It means |
| --- | --- |
| a run covering `n` with `anidb_episode_ids` | AniDB lists the episode, and its AniDB episode id is `anidb_episode_ids[n - start]` |
| `episodes_listed`, and no such run | AniDB lists no episode `n` of kind `k`, a gap in its numbering or a number past it |
| no `episodes_listed` | the AniDB mirror has no list for the anime, so the file cannot say |

So on a row with `episodes_listed` a reader tells four answers apart: AniDB lists the episode and it lands somewhere, AniDB lists it and it lands nowhere, AniDB lists it and nothing is stated, and AniDB lists no such episode. An episode AniDB does not list can still have a run when an Anime-Lists row names it or `last.regular` counts it, and its statements are read the same way.

On a site the row has no id for, nothing is placed. "Nothing stated" covers an episode Anime-Lists places only by an absolute number that no overlay entry resolves to a season, a regular episode on a node with no default season, a mapping-list row whose target is before episode 1, and a regular episode AniDB lists past `last.regular`. "Lands nowhere" covers a row that says `-0`, a node's default `episodeoffset` or `tmdboffset` that puts a regular episode no row names before 1, every credit, promotion, parody and other episode no row names, since Anime-Lists has no default for those kinds, and a special or later number AniDB does not list and no row names. Anime-Lists numbers credits from 101, so a row's number past 100 names a special only where AniDB lists a special of that number, and otherwise the credit or later episode the number addresses. A reader that trusts only stated placements reads a `default` target as "season S, episode not stated".

Runs never overlap within a kind, and are maximal: two consecutive numbers of one kind share a run when AniDB lists both or neither and each site's statement has the same form, every target keeping its season and `default` flag and moving on by one episode. A run of episodes AniDB does not list exists only where some site says more than "lands nowhere", so it never says `[]` on every site its row has an id for. A run of episodes AniDB lists says `[]` for each site they land nowhere on.

## Entries

The entries with an AniList id come first, sorted by `anilist_id`: every AniList id anime-offline-database lists, and every AniList id an overlay link names. Then one entry per anime-offline-database entry that names no AniList id and lists an id on a service below, sorted by its encoded `ids` member. A reader does not check that order, since a service added within version 2 changes the key. Every member is left out when it has no value.

| Field | Type | Meaning |
| --- | --- | --- |
| `anilist_id` | integer | The AniList id. Absent on an entry with no AniList id. |
| `type` | string | The type, from the first anime-offline-database entry naming the AniList id. An AniList id only an overlay link names takes the linked anime's type when the link names every regular episode of one anime, and has none otherwise. |
| `episodes` | integer | anime-offline-database's episode count for the entry. It places nothing. It shows where the entry is only part of an anime, and it is the only episode fact of an entry with no parts. |
| `ids` | object | Every id anime-offline-database lists for the entry. See below. |
| `parts` | array of parts | The AniDB episodes the entry is, in the entry's episode order. See [Parts](#parts). |

`ids` holds one sorted list per service, each present only when it has an id: `anidb`, `animecountdown`, `anisearch`, `ann` (Anime News Network), `kitsu`, `livechart`, `mal` (MyAnimeList) and `simkl` as integers, and `animeplanet` and `notifymoe` as each service's own URL key. They are anime-offline-database's statements as it makes them. `ids.anidb` lists every AniDB candidate the database names, so an entry with two keeps both there and has no parts.

An entry has no TVDB, TMDB or IMDb id of its own: those are reached through `parts` and the anime rows.

## Parts

A part is `{"anidb_id": A, "kind": K}`, with `start` and `end` together or not at all, and `"combined": true` on a part of every regular episode of `A` as one entry episode.

- **One anime.** When the AniList id's entries name exactly one AniDB id, or an overlay link names every regular episode of one anime, the entry has one part, `{"anidb_id": A, "kind": "regular"}` with no range. A part with no range names episodes 1 to `last[K]` of `A`, and none when the anime has no `last` of that kind yet.
- **A link.** An overlay link gives the entry one ranged part per run of the AniDB episodes it names, in order. A link applies only to an AniList id with no AniDB id of its own, and a non-identity link only when anime-offline-database's episode count for the entry, if it has one, equals the link's.
- **Ambiguous.** When the database entries name two or more AniDB ids there are no parts, never a guess.
- **No identity.** No parts and no `ids.anidb`.

Each AniDB episode a part names is one episode of the entry, except that a combined part is one entry episode made of all of them, and none while the anime has no regular `last`.

## Order and checks

- `anime` is sorted by `anidb_id`. Runs are sorted by kind in the order of `last`, then `start`. Targets are sorted by season, then episode, each once.
- Inside `ids`, each list is ascending. `tmdb_movie_ids` and `imdb_ids` keep Anime-Lists' order.
- Two builds from the same inputs give the same file, `generated_at` aside.
- The JSON Schema cannot state these rules, which the build checks before it publishes and mapread checks when it reads:
  - the bytes UTF-8
  - no key repeated in an object, even in another case
  - the member order, with `version` first and only once
  - every integer written with digits alone, with no fraction or exponent
  - `generated_at` in UTC, with the offset `Z` or `+00:00`
  - `start` at most `end`, in a run and in a part
  - every target at most 1,048,576 at the run's last episode: its `episode` plus `end` minus `start`
  - the order of the anime, of the AniList entries, of the runs, of the targets and of each `ids` list
  - every run maximal, so no two runs could be one
  - `end` within `last`, except on a run of regular episodes AniDB lists that states no site
  - one AniDB episode id per episode of a run, and ids only on a row with `episodes_listed`
  - `tvdb` only with `tvdb_id`, and `tmdb` only with `tmdb_tv_id`
  - no run AniDB lists none of on a row with neither, and no such run that lands nowhere on every site
  - every part naming an anime row
  - every ranged part within its anime's `last`
  - a combined part regular and with no range
- Episode numbers, seasons and counts are at most 1,048,576, which no anime comes near.
- mapread also refuses an integer that Go's `int` cannot hold: one above 9,223,372,036,854,775,807, or above 2,147,483,647 on a 32-bit platform.
- A reader keeps an anime row or an `ids` object whose only members are ones it does not know: a newer file may write one. To that reader the row says nothing beyond its id.

## When a release changes

Each release's notes carry a `content_hash`, a SHA-256 over `version`, `attribution`, `anime` and `entries`. It leaves out `generated_at` and `sources`, so a new Anime-Lists commit that changes no row publishes nothing.

## Size

The file is about 13.4 MB, 2.3 MB compressed, with about 16,300 anime rows, 38,300 entries and 294,000 AniDB episode ids. The build refuses a file over 32 MiB, and mapread reads one of up to 32 MiB by default. Decoded into mapread's types, it takes about 2.1 bytes of memory per byte of file, and 2.2 with mapread's index.

## Reading it

Find where AniList 21's episodes land on TVDB with `jq`:

```sh
jq --argjson al 21 '(.entries[] | select(.anilist_id == $al) | .parts) as $p
  | .anime[] | select(.anidb_id as $a | $p | any(.anidb_id == $a)) | {anidb_id, tvdb_id, episodes}' animap.json
```

In Go, [mapread](../mapread/README.md) decodes the file and applies the reading rule above for you.
