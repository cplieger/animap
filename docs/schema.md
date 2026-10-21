# animap.json schema

`animap.json` is one minified UTF-8 JSON document. This page describes version 2. A machine-readable copy is [animap.schema.json](animap.schema.json), a JSON Schema (draft 2020-12). The Go module [mapread](../mapread/README.md) reads and checks the file, verifies a download and answers the lookups below.

The file holds two tables. `anime` has one row per AniDB anime: its TheTVDB series, its MyAnimeList and IMDb ids, the episodes AniDB lists with their AniDB episode ids, where each episode lands on TheTVDB and which TMDB movie it is. `entries` has one row per AniList id: its AniDB and MyAnimeList ids, and which AniDB episodes it is.

Earlier releases of version 2 have another shape, which this page and mapread no longer describe: their `sources` named their inputs, anime rows carried TMDB TV ids, and entries without an AniList id carried ids on other services. A reader of this shape refuses an earlier file by its `sources`.

## Top level

The members appear in this order, and `version` is always first, so a reader can refuse an unknown version before it reads a table.

| Member | Type | Meaning |
| --- | --- | --- |
| `version` | integer | The format version, `2`. Only a breaking change raises it. A member added within version 2 is extra, and a reader ignores members it does not know, at every level. |
| `generated_at` | string | When the file was built: an RFC 3339 time in UTC, written with `Z`. |
| `sources` | object | What the file was built from. See below. |
| `attribution` | object | The licence of the data and the notice it requires. |
| `anime` | array | One row per AniDB anime, by `anidb_id`. See [Anime rows](#anime-rows). |
| `entries` | array | One row per AniList id, by `anilist_id`. See [Entries](#entries). |

`sources` holds two members:

- `mapping`: `revision`, an identifier of the mapping database's state the file was built from. It is opaque, not empty and at most 100 bytes.
- `anidb`: `titles_dump`, the date, written `yyyy-mm-dd`, of the AniDB anime title list the anime rows reflect.

`sources` never names `anime_offline_database`, `anime_lists`, `anidb_mirror` or `overlay`, the members of the earlier shape.

`attribution` carries `license` (`ODbL-1.0`), `contents_license` (`DbCL-1.0`), a URL for each, and `notice`, the text to show wherever you redistribute the data.

## Anime rows

Every member except `anidb_id` is left out when it has no value, and a row always has something to say beyond its id.

| Field | Type | Meaning |
| --- | --- | --- |
| `anidb_id` | integer | The AniDB id. |
| `type` | string | `TV`, `MOVIE`, `OVA`, `ONA`, `SPECIAL` or `UNKNOWN`. |
| `tvdb_id` | integer | The TheTVDB series the anime is on. Every target without its own `series` is on it. |
| `mal_ids` | array of integers | The MyAnimeList ids AniDB lists for the anime, ascending. |
| `imdb_ids` | array of strings | IMDb ids, each `tt` followed by 7 to 10 digits. Each names the whole anime. |
| `last` | object | The highest episode number the file places, per kind: `regular`, `special`, `credit`, `promotion`, `parody` and `other`. |
| `episodes_listed` | `true` | AniDB's episode list for the anime was read, so the runs name every episode AniDB lists with its AniDB episode id. |
| `episodes` | array of runs | The episodes AniDB lists and where episodes land. See [Runs](#runs). |

Episode numbers are AniDB's own, per kind: credit 1 is `{"kind": "credit", "start": 1}`. `last` is a highest number, not a count, because AniDB's numbering can skip. AniDB can list regular episodes past `last.regular`, for example on an airing anime. A row with `episodes` has `last` or `episodes_listed`.

## Runs

A run is consecutive AniDB episodes of one kind that land together:

```json
{ "kind": "regular", "start": 13, "end": 15,
  "anidb_episode_ids": [ 98113, 98114, 98115 ],
  "tvdb": [ { "season": 3, "episode": 13, "episode_ids": [ 7001, 7002, 7003 ] } ] }
```

| Field | Type | Meaning |
| --- | --- | --- |
| `kind` | string | `regular`, `special`, `credit`, `promotion`, `parody` or `other`. |
| `start`, `end` | integer | The first and last AniDB episode number, in that kind, with `start` at most `end`. `end` is at most `last` of the kind, except on a run of regular episodes AniDB lists past `last.regular`, which states no landing. |
| `anidb_episode_ids` | array of integers | One AniDB episode id per episode, `start` first, when AniDB lists the run's episodes. Absent when it lists none of them. A run never mixes the two. |
| `tvdb` | array of targets | Where the episodes land on TheTVDB. `[]` means they land nowhere there. |
| `tmdb_movies` | array of integers | The TMDB movies each episode of the run is, ascending. Several ids are records of one work on TMDB, never different works. Absent means no movie is stated, and the list is never empty. |

### Targets

A target is `{"season": S, "episode": E}` in TheTVDB's official order, never an absolute number. Episode `start` plus `i` lands on season `S`, episode `E` plus `i`, for every target in the list.

| Field | Type | Meaning |
| --- | --- | --- |
| `series` | integer | The TheTVDB series the target is on, when it is not the row's `tvdb_id`. It is never the row's `tvdb_id`, and on a row with no `tvdb_id` every target has one. |
| `season`, `episode` | integer | The season, 0 for specials, and the episode `start` lands on. |
| `episode_ids` | array of integers | One TheTVDB episode id per episode of the run, `start` first. Absent when the file holds none. |
| `default` | `true` | The target rests only on a default carried over from Anime-Lists, with no stated placement behind it. |

- Two or more targets mean each episode spans that many TheTVDB episodes, as with a double episode, or lands on two series.
- Two AniDB episodes on one TheTVDB episode appear as two runs that name it, on one anime or on two. A reader sees the ambiguity and can refuse it.
- One TheTVDB episode has one episode id in the whole file, and one id names one TheTVDB episode.

A reader that trusts only stated placements reads a `default` target as "season S, episode not stated".

### Reading an episode

For episode `n` of kind `k` of an anime:

| The file has | On TheTVDB it means |
| --- | --- |
| no anime row | animap knows nothing about the anime |
| a run covering `n`, with targets | it lands there, by default where a target says `default` |
| a run covering `n`, with `"tvdb": []` | it lands nowhere on TheTVDB |
| a run covering `n`, with no `tvdb` | nothing is stated on the row's series, or, on a row with no `tvdb_id`, the episode is not on TheTVDB |
| no run covering `n`, and `n` from 1 to `last[k]` | it lands nowhere on the row's series, or, on a row with no `tvdb_id`, it is not on TheTVDB |
| no run covering `n`, `n` past `last[k]` | with `episodes_listed`, AniDB lists no such episode, which is read as in the row above. Without it, not an episode the file places |

An episode is a TMDB movie only where a run covering it states `tmdb_movies`. An anime with no `tvdb_id` is on TheTVDB only where a run lands, on the series its targets name.

What AniDB's list says of the same episode:

| The file has | It means |
| --- | --- |
| a run covering `n` with `anidb_episode_ids` | AniDB lists the episode, and its AniDB episode id is `anidb_episode_ids[n - start]` |
| `episodes_listed`, and no such run | AniDB lists no episode `n` of kind `k`, a gap in its numbering or a number past it |
| no `episodes_listed` | the file holds no list for the anime, so it cannot say |

So on a row with `episodes_listed` a reader tells four answers apart: AniDB lists the episode and it lands somewhere, AniDB lists it and it lands nowhere, AniDB lists it and nothing is stated, and AniDB lists no such episode.

Runs never overlap within a kind, and are maximal: two consecutive numbers of one kind share a run when AniDB lists both or neither, their `tvdb` statements have the same form with every target keeping its series, season and `default` flag, moving on by one episode and holding `episode_ids` on both or neither, and their `tmdb_movies` are equal. A run that lists no AniDB episode always states a landing: a target, or `tmdb_movies`.

## Entries

One entry per AniList id, sorted by `anilist_id`. Every member except `anilist_id` is left out when it has no value.

| Field | Type | Meaning |
| --- | --- | --- |
| `anilist_id` | integer | The AniList id. |
| `type` | string | `TV`, `MOVIE`, `OVA`, `ONA`, `SPECIAL` or `UNKNOWN`. |
| `ids` | object | `anidb`, the AniDB anime the entry may be, and `mal`, its MyAnimeList ids, each an ascending list present only when it has an id. When `anidb` names two or more anime, the entry is ambiguous and has no parts. |
| `parts` | array of parts | The AniDB episodes the entry is, in the entry's episode order. See [Parts](#parts). |

An entry has no TheTVDB, TMDB or IMDb id of its own: those are reached through `parts` and the anime rows.

## Parts

A part is `{"anidb_id": A, "kind": K}`, with `start` and `end` together or not at all, and `"combined": true` on a part of every regular episode of `A` as one entry episode.

- **One anime.** An entry that is one whole anime has one part, `{"anidb_id": A, "kind": "regular"}` with no range. A part with no range names episodes 1 to `last[K]` of `A`, and none when the anime has no `last` of that kind yet.
- **Some episodes.** An entry that is part of an anime, or episodes of several, has one ranged part per run of the AniDB episodes it is, in order.
- **Ambiguous or unknown.** No parts, never a guess.

Each AniDB episode a part names is one episode of the entry, except that a combined part is one entry episode made of all of them, and none while the anime has no regular `last`.

## Order and checks

- `anime` is sorted by `anidb_id` and `entries` by `anilist_id`. Runs are sorted by kind in the order of `last`, then `start`. A run's targets are sorted by series (a target's `series`, else the row's `tvdb_id`), then season, then episode, each once.
- `mal_ids`, `tmdb_movies` and each list in `ids` are ascending and unique. `imdb_ids` are unique.
- The JSON Schema cannot state these rules, which the build checks before it publishes and mapread checks when it reads:
  - the bytes UTF-8
  - no key repeated in an object, even in another case
  - the member order, with `version` first and only once
  - every integer written with digits alone, with no fraction or exponent
  - `generated_at` in UTC, with the offset `Z` or `+00:00`
  - `sources.mapping.revision` at most 100 bytes
  - `start` at most `end`, in a run and in a part
  - every target at most 1,048,576 at the run's last episode: its `episode` plus `end` minus `start`
  - the order of the anime, of the entries, of the runs, of the targets and of each id list
  - every run maximal, so no two runs could be one
  - `end` within `last`, except on a run of regular episodes AniDB lists that states no landing
  - one AniDB episode id per episode of a run, and ids only on a row with `episodes_listed`
  - one TVDB episode id per episode of its run in a target's `episode_ids`
  - a target's `series` other than its row's `tvdb_id`, and on a row with no `tvdb_id`, a `series` on every target and no empty `tvdb`
  - no run that lists no AniDB episode and states no landing
  - one TVDB episode id per TVDB episode, and one TVDB episode per id, across the rows
  - every part naming an anime row
  - every ranged part within its anime's `last`
  - a combined part regular and with no range
- Episode numbers, seasons and counts are at most 1,048,576, which no anime comes near.
- mapread also refuses an integer that Go's `int` cannot hold: one above 9,223,372,036,854,775,807, or above 2,147,483,647 on a 32-bit platform.
- A reader keeps an anime row or an `ids` object whose only members are ones it does not know: a newer file may write one. To that reader the row says nothing beyond its id.

## When a release changes

Each release's notes carry a `content_hash`, a SHA-256 over `version`, `attribution`, `anime` and `entries`. It leaves out `generated_at` and `sources`, so a rebuild that changes no row publishes nothing. mapread's `ContentHash` computes it.

## Size

The file is about 10.6 MB, 1.9 MB compressed, with about 16,300 anime rows, 21,300 entries and 294,000 AniDB episode ids. The build refuses a file over 32 MiB, and mapread reads one of up to 32 MiB by default. Decoded into mapread's types, it takes about 1.7 bytes of memory per byte of file, and 2.9 with mapread's index.

## Reading it

Find where AniList 21's episodes land on TheTVDB with `jq`:

```sh
jq --argjson al 21 '(.entries[] | select(.anilist_id == $al) | .parts) as $p
  | .anime[] | select(.anidb_id as $a | $p | any(.anidb_id == $a)) | {anidb_id, tvdb_id, episodes}' animap.json
```

In Go, [mapread](../mapread/README.md) decodes the file and applies the reading rule above for you.
