# animap.json schema

`animap.json` is one minified JSON document. This page describes version 1. A machine-readable copy is [animap.schema.json](animap.schema.json), a JSON Schema (draft 2020-12).

## Top level

| Member | Type | Meaning |
| --- | --- | --- |
| `version` | integer | The schema version, `1`. A breaking change to the format raises it. |
| `generated_at` | string | When the file was built, in UTC (RFC 3339). |
| `sources` | object | Exactly what the build read. See below. |
| `attribution` | object | The licence of the data and the notice it requires. |
| `records` | array | One object per anime id. See below. |

`sources` names four inputs:

- `anime_offline_database`: the repository, the release tag, the asset name and the asset's SHA-256.
- `anime_lists`: the repository, the commit and the file name of `anime-list-master.xml`.
- `anidb_mirror`: the repository and the commit of the AniDB mirror that the episode counts were read from.
- `overlay`: how many overlay entries were applied, how many special-of-parent bridges as `special_of_parent`, which is absent when there are none, and a SHA-256 of the whole overlay set.

`attribution` carries `license` (`ODbL-1.0`), `contents_license` (`DbCL-1.0`), a URL for each, and `notice`, the sentence to show wherever you redistribute the data.

## Records

Every field except the record's id is left out when it has no value. Four integer fields are "present or absent" rather than zero by default, because `0` means something for each of them: `tvdb_season`, `tvdb_episode_offset`, `tmdb_season` and `tmdb_episode_offset`. Test for presence, never for a zero value.

| Field | Type | Meaning |
| --- | --- | --- |
| `anilist_id` | integer | The AniList id. Absent on a record keyed by its AniDB id. |
| `anidb_id` | integer | The AniDB id, when the AniList id meets exactly one. |
| `anidb_parent` | object | Another anime's `anidb_id` and `specials`, one special number per AniList episode in order, when AniDB files this entry as that anime's specials. Never set with `anidb_id`. |
| `mal_id` | integer | The MyAnimeList id, when there is exactly one. |
| `type` | string | `TV`, `MOVIE`, `OVA`, `ONA`, `SPECIAL` or `UNKNOWN`, from anime-offline-database. A build that meets any other value publishes nothing. |
| `episodes` | integer | The number of regular episodes. With an `anidb_id`, it is the count from the rule in [Episode counts](overlay.md#episode-counts): a `checks/counts.json` row, else AniDB's count from the AniDB mirror for an anime whose end date AniDB records, else anime-offline-database's. AniDB can record an end date before the last episode airs. Without an `anidb_id`, it is anime-offline-database's count. |
| `tvdb_id` | integer | The TheTVDB series id. Anime-Lists markers such as `movie` or `OVA` give no `tvdb_id`. |
| `tvdb_season` | integer | The TVDB season the title is filed under. `0` means the series' specials. |
| `tvdb_absolute` | boolean | `true` when the title follows TVDB's absolute numbering instead of one season. Never set with `tvdb_season`. |
| `tvdb_episode_offset` | integer | Add it to an AniDB episode number to get the TVDB episode number. An explicit `0` says the numbers match. |
| `tmdb_tv_id` | integer | The TMDB TV series id. |
| `tmdb_season` | integer | The TMDB season. |
| `tmdb_episode_offset` | integer | The same offset for TMDB. |
| `tmdb_movie_ids` | array of integers | TMDB movie ids. |
| `imdb_ids` | array of strings | IMDb ids, each `tt` followed by 7 to 10 digits. |
| `mapping_list` | array of rows | Per-episode mappings. Present only with a `tvdb_id` or a `tmdb_tv_id`. |
| `tvdb_placement` | array of segments | The TVDB episode each regular episode lands on. See [TVDB placement](#tvdb-placement). |

A row in `mapping_list` says where some of the title's AniDB episodes land:

| Field | Type | Meaning |
| --- | --- | --- |
| `anidb_season` | integer | `1` for regular AniDB episodes, `0` for AniDB specials. |
| `tvdb_season` | integer | The TVDB season these episodes land on. Absent on a TMDB-only row. |
| `tmdb_season` | integer | The TMDB season, on a TMDB row. |
| `start`, `end` | integer | A range of AniDB episodes. With no `end`, a regular range runs to the end of the title. |
| `offset` | integer | Add it to each episode in the range. |
| `episodes` | array of arrays | Single episodes, each `[anidb, target]`. `[anidb, a, b]` means one AniDB episode spans two target episodes, and a longer pair spans more. `[anidb]` alone means the episode has no counterpart on that side. |

A row takes priority over the record's default season and offset for the episodes it names.

## TVDB placement

`tvdb_placement` answers, for each regular episode from 1 to `episodes`, which TVDB episode it is. animap works it out from the record's own fields, with the rule its collision check uses:

1. The first `mapping_list` row with `anidb_season` 1 and a `tvdb_season` that names the episode decides it. A `[k]` pair says it has no TVDB episode.
2. Otherwise the episode lands on `tvdb_season`, at its number plus `tvdb_episode_offset`.

The field is present only when every regular episode has an answer, and every target is episode 1 or later. So a record has no `tvdb_placement` when an episode needs the offset and the record has none. An absent offset does not say that the numbers match. A record with `tvdb_absolute` and no rows for every episode has none either.

Each segment covers a run of AniDB episodes:

| Field | Type | Meaning |
| --- | --- | --- |
| `start`, `end` | integer | The first and last AniDB regular episode of the run. |
| `season` | integer | The TVDB season of the run. Absent when these episodes have no TVDB episode. |
| `episode` | integer | The TVDB episode that `start` lands on. `start` plus one lands on `episode` plus one, and so on to `end`. Absent with `season`. |

An AniDB episode that spans several TVDB episodes appears in one segment for each. For example, `[{"start":1,"end":2,"season":0,"episode":9},{"start":3,"end":3}]` puts episodes 1 and 2 on TVDB specials 9 and 10, and says episode 3 has no TVDB episode.

`tvdb_placement` is optional in version 1. A reader may ignore it, and every other field keeps its meaning.

## Which records exist

There is one record per AniList id in anime-offline-database, sorted by `anilist_id`. An AniList id with no AniDB id still gets a record with its `type`, `episodes` and `mal_id`. When one AniList id meets two different AniDB ids, the record carries no `anidb_id`, because two candidates is not an answer. Each of those AniDB ids then has a record of its own.

A record with `anidb_parent` takes its `tvdb_id` from that parent anime. Its `tvdb_season` and one `mapping_list` row place each episode on the TVDB episode the parent's special lands on. It carries no TMDB ids.

After the AniList records come records keyed by `anidb_id` alone, sorted by it. They cover every AniDB id that no AniList record carries, when anime-offline-database lists it or Anime-Lists has a TVDB, TMDB or IMDb id for it.

## When a release changes

Each release's notes carry a `content_hash`, a SHA-256 over `version`, `attribution` and `records`. It leaves out `generated_at` and `sources`, so a new Anime-Lists commit that changes no record publishes nothing. Two builds from the same inputs give the same records byte for byte.

## Reading it

Find the TVDB season of AniList 21 with `jq`:

```sh
jq '.records[] | select(.anilist_id == 21) | {tvdb_id, tvdb_season, tvdb_absolute}' animap.json
```

In Go, decode into structs whose present-or-absent fields are pointers:

```go
type Record struct {
	AniListID         int    `json:"anilist_id"`
	AniDBID           int    `json:"anidb_id"`
	Type              string `json:"type"`
	TVDBID            int    `json:"tvdb_id"`
	TVDBSeason        *int   `json:"tvdb_season"`
	TVDBAbsolute      bool   `json:"tvdb_absolute"`
	TVDBEpisodeOffset *int   `json:"tvdb_episode_offset"`
	TMDBMovieIDs      []int  `json:"tmdb_movie_ids"`
}

var doc struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}
```

Check `doc.Version` before you use the records.
