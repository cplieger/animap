# animap

[![License](https://img.shields.io/github/license/cplieger/animap)](LICENSE)

animap turns an AniList or AniDB anime id into its TVDB or TMDB series, season and episodes, and its ids on the other anime sites, from one JSON file.

It replaces joining [anime-offline-database](https://github.com/cedya77/anime-offline-database) and [Anime-Lists](https://github.com/Anime-Lists/anime-lists) yourself. A tool downloads `animap.json`, about 13 MB, and reads it with any JSON parser, or in Go with the [mapread](mapread/README.md) module. The data is licensed under the ODbL 1.0 and the code under Apache-2.0.

## Why use it

animap is built for tools beside Sonarr and Radarr that need the id, season and episodes those apps use, such as a SeaDex watcher, a list sync or a subtitle fetcher.

- Each AniDB episode says where it lands on TVDB and TMDB, specials and films included, and whether a written mapping or only a default puts it there.
- It answers both ways: an AniList entry to its TVDB episodes, a TVDB episode to every AniDB episode on it, and a TMDB movie to its AniDB anime.
- Each entry carries every id anime-offline-database lists, from MyAnimeList and Kitsu to Anime-Planet and LiveChart.
- The file is rebuilt every 3 hours, and a build is not released when a count drops by over 10% or two entries newly claim the same episode.
- Each correction to Anime-Lists is proven against AniDB, TVDB and TMDB, and a daily review checks every mapping's air dates, the season 0 episodes no mapping claims and every [SeaDex](https://seadex.moe) title.

Consider [arm-server](https://github.com/BeeeQueue/arm-server) if you want lookups through an HTTP API.

## Install

```sh
curl -fLO https://github.com/cplieger/animap/releases/latest/download/animap.json
```

## Usage

The file has two tables. `entries` says which AniDB episodes an AniList entry is, and `anime` says which episodes AniDB lists and where each one lands. This is the entry for AniList id 1:

```json
{
  "anilist_id": 1, "type": "TV", "episodes": 26,
  "ids": { "anidb": [23], "animecountdown": [37089], "animeplanet": ["cowboy-bebop"], "anisearch": [1572], "ann": [13], "kitsu": [1], "livechart": [3418], "mal": [1], "simkl": [37089] },
  "parts": [ { "anidb_id": 23, "kind": "regular" } ]
}
```

Its part is every regular episode of AniDB 23, whose row holds 14 runs, among them these:

```json
{
  "anidb_id": 23, "type": "TV", "tvdb_id": 76885, "tmdb_tv_id": 30991,
  "last": { "regular": 26, "special": 14, "credit": 4, "promotion": 10, "parody": 1 },
  "episodes_listed": true,
  "episodes": [
    { "kind": "regular", "start": 1, "end": 1, "anidb_episode_ids": [881], "tvdb": [{ "season": 1, "episode": 13 }], "tmdb": [{ "season": 1, "episode": 13 }] },
    { "kind": "regular", "start": 2, "end": 3, "anidb_episode_ids": [879, 877], "tvdb": [{ "season": 1, "episode": 1 }], "tmdb": [{ "season": 1, "episode": 1 }] },
    { "kind": "regular", "start": 19, "end": 26, "anidb_episode_ids": [858, 883, 882, 880, 878, 876, 874, 872], "tvdb": [{ "season": 1, "episode": 19, "default": true }], "tmdb": [{ "season": 1, "episode": 19, "default": true }] },
    { "kind": "special", "start": 1, "end": 2, "anidb_episode_ids": [7274, 127868], "tvdb": [{ "season": 0, "episode": 1 }], "tmdb": [{ "season": 0, "episode": 1, "default": true }] },
    { "kind": "promotion", "start": 1, "end": 1, "anidb_episode_ids": [22748], "tvdb": [], "tmdb": [] },
    { "kind": "promotion", "start": 6, "end": 10, "anidb_episode_ids": [36634, 46674, 46675, 36590, 155750], "tvdb": [], "tmdb": [] }
  ]
}
```

AniDB episode 1, AniDB episode id 881, is TVDB and TMDB season 1, episode 13. Episodes 19 to 26 land on episodes 19 to 26 by Anime-Lists' default, not a written mapping. The promotions land nowhere on either site, and AniDB lists no promotions 2 to 5. Find an entry with `jq`:

```sh
jq '.entries[] | select(.anilist_id == 1)' animap.json
```

Read `version` first. It changes only when the format breaks.

Each release also carries `animap.json.sha256` and `animap.json.sigstore.json`, a keyless [cosign](https://docs.sigstore.dev/) signature made by this repository's publish workflow. To check a download:

```sh
sha256sum -c animap.json.sha256
cosign verify-blob animap.json --bundle animap.json.sigstore.json \
  --certificate-identity https://github.com/cplieger/animap/.github/workflows/publish.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

To build the file yourself, run this from the repository root with Go, `curl`, `jq` and an authenticated `gh` CLI:

```sh
export AOD_VERSION=$(sed -n 's/^  AOD_VERSION: //p' .github/workflows/publish.yaml)
export MIRROR_COMMIT=$(sed -n 's/^  MIRROR_COMMIT: //p' .github/workflows/publish.yaml)
DRY_RUN=1 bash scripts/publish.sh
```

`DRY_RUN=1` builds and checks the file and writes `./animap.json` without creating a release. `DRY_RUN` unset or empty publishes, and any value but `0` or `1` is read as a dry run.

## API

The format is described in [docs/schema.md](docs/schema.md), with a JSON Schema in [docs/animap.schema.json](docs/animap.schema.json). The Go module [mapread](mapread/README.md) reads it and answers the lookups.

- Top level: `version`, `generated_at`, `sources`, `attribution`, `anime` and `entries`.
- Anime rows: `anidb_id`, `type`, `tvdb_id`, `tmdb_tv_id`, `tmdb_movie_ids`, `imdb_ids`, `last`, `episodes_listed` and `episodes`, the runs.
- Runs: `kind`, `start`, `end`, `anidb_episode_ids`, and `tvdb` and `tmdb` targets, each a season and an episode, flagged `default` when it rests only on Anime-Lists' default.
- Entries: `anilist_id`, `type`, `episodes`, `ids` per service, and `parts`, the AniDB episodes the entry is.

A run with `[]` for a site lands nowhere there, and a run with no member for a site states nothing for it. An episode no run names lands nowhere, on every site the row has an id for, when its number is from 1 to the kind's `last` or when the row has `episodes_listed` (there it is also one AniDB does not list); past `last` on a row without `episodes_listed`, the file does not place it.

## How often it changes

A workflow builds a new file every 3 hours. It publishes the file only when a hash of its version, attribution and tables differs from the latest release. Release tags are dates, such as `v2026.10.05`, with the time added for a second release that day. Each release's notes list the seven counts below.

The anime-offline-database release and the AniDB mirror commit are pinned in `.github/workflows/publish.yaml`, and Renovate proposes each new one. Anime-Lists has no releases, so each run reads its newest commit.

To skip an unchanged download, compare the release tag `https://github.com/cplieger/animap/releases/latest` redirects to with the one you hold, or send your last `ETag` as `If-None-Match`.

A build stops before publishing when one of these checks fails, and the previous release stays the latest:

- Any of seven counts falls below 90% of the latest release, unless the maintainer accepts the drop on a manual run. The seven are AniList entries, AniList entries with parts, anime with a TVDB id, anime with a TMDB id, AniDB episodes with a TVDB target, AniDB episodes with a TMDB target and AniDB episode ids. With no latest release of the same version, each count must reach a fixed floor instead.
- Two Anime-Lists entries on one TVDB series claim the same TVDB or TMDB episode.
- An Anime-Lists entry on a shared TVDB series has no episode count in any source, so its episodes cannot be placed.
- An input is larger than its limit, does not parse, or does not match its published SHA-256.

The collisions and uncounted entries Anime-Lists already had are listed in `checks/collision-baseline.json`, and stop a build only where animap changes them, as [docs/overlay.md](docs/overlay.md#collisions-anime-lists-already-has) explains.

## Data sources and licence

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](LICENSE-DATA), and the file carries the same notice in its `attribution` member. If you publish a database built from it, the ODbL asks you to keep that notice and to share it under the same licence.

It contains information from [anime-offline-database](https://github.com/cedya77/anime-offline-database), made available under the ODbL 1.0 and the DbCL 1.0, from [Anime-Lists](https://github.com/Anime-Lists/anime-lists), which publishes no licence, and from [AniDB](https://anidb.net) which episodes exist, with their kind, number and AniDB episode id. AniDB's data is licensed under CC BY-NC-SA 4.0. anime-offline-database supplies every entry's ids, type and episode count, and Anime-Lists the TVDB, TMDB and IMDb ids and where each episode lands, joined as [Fribb/anime-lists-generator](https://github.com/Fribb/anime-lists-generator) joins them.

AniDB's data is read through [AnimeAggregations](https://github.com/notseteve/AnimeAggregations), a mirror of AniDB's data. `animap.json` carries only which episodes exist, their kind and number, and their AniDB ids. It carries none of the content AniDB holds for an episode: no titles, summaries, air dates, lengths, ratings or any other description.

Each correction on a TVDB series stores a fingerprint of its episode order, read through the metadata service Sonarr uses, and the daily review keeps only numbers, dates and title digests from TheTVDB and TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB. [docs/sources.md](docs/sources.md) covers each source and its licence.

## Documentation

- [The animap.json schema](docs/schema.md) lists every field and how to read an episode's placement.
- [mapread](mapread/README.md) is the Go module that reads, verifies and looks up the file.
- [The overlay](docs/overlay.md) is animap's set of corrections to Anime-Lists, and how to add one.
- [The daily review](docs/review.md) keeps one issue per anime, and explains how to accept a finding.
- [The watch set](docs/watch.md) defines what "fully mapped" means for each type.
- [The date check](docs/date-check.md) flags mappings that AniDB air dates contradict.
- [Data sources and licences](docs/sources.md) covers each source and its licence.

## Contributing

Issues and pull requests are welcome. To report a bad mapping, give the AniList or AniDB id, what the file says and should say, and the AniDB, TVDB or TMDB page that shows it. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).

Third-party attributions are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
