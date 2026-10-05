# animap

[![License](https://img.shields.io/github/license/cplieger/animap)](LICENSE)

animap turns an AniList or AniDB anime id into its TVDB or TMDB series, season and episodes, and its MyAnimeList and IMDb ids, from one JSON file.

It replaces joining [anime-offline-database](https://github.com/cedya77/anime-offline-database) and [Anime-Lists](https://github.com/Anime-Lists/anime-lists) yourself. There is no package to install. A tool downloads `animap.json`, about 2.3 MB and 22,000 records, and reads it with any JSON parser. The data is licensed under the ODbL 1.0 and the code under Apache-2.0.

## Why use it

animap is built for tools beside Sonarr and Radarr that need the id and season those apps use, such as a SeaDex watcher or a list sync.

- Records carry the TVDB and TMDB season, the episode offset and a per-episode mapping list for specials and films.
- The file is rebuilt every 3 hours and released only when its version, attribution or records change.
- A build is not released when a count drops by over 10% or two entries newly claim the same episode.
- Each correction to Anime-Lists is proven against AniDB, TVDB and TMDB, and links its Anime-Lists pull request once one is filed.
- Every [SeaDex](https://releases.moe) title is checked daily for a complete mapping.

Every value comes from anime-offline-database, Anime-Lists or animap's corrections, never from another site.

Consider [Fribb/anime-lists](https://github.com/Fribb/anime-lists) if you also need Kitsu or Anime-Planet ids. Consider [arm-server](https://github.com/BeeeQueue/arm-server) if you want lookups through an HTTP API.

## Install

```sh
curl -fLO https://github.com/cplieger/animap/releases/latest/download/animap.json
```

## Usage

The records sit in one `records` array, and each leaves out the fields it has no value for. This is the record for AniList id 1:

```json
{
  "anilist_id": 1,
  "anidb_id": 23,
  "mal_id": 1,
  "type": "TV",
  "episodes": 26,
  "tvdb_id": 76885,
  "tvdb_season": 1,
  "tmdb_tv_id": 30991,
  "tmdb_season": 1,
  "mapping_list": [
    { "anidb_season": 0, "tvdb_season": 0, "start": 1, "end": 3, "offset": 1, "episodes": [[4]] }
  ]
}
```

Its regular episodes are on TVDB season 1 and TMDB season 1. Its specials follow the mapping list. AniDB specials 1 to 3 are TVDB specials 2 to 4, an offset of 1, and AniDB special 4 has no TVDB episode. Find a record with `jq`:

```sh
jq '.records[] | select(.anilist_id == 1)' animap.json
```

Records are sorted by `anilist_id`. Records with an AniDB id and no AniList id come last. An AniList id with no AniDB id still has a record with its type, episode count and MyAnimeList id. Read `version` first. It changes only when the format breaks.

Each release also carries `animap.json.sha256` and `animap.json.sigstore.json`, a keyless [cosign](https://docs.sigstore.dev/) signature made by this repository's publish workflow. To check a download:

```sh
sha256sum -c animap.json.sha256
cosign verify-blob animap.json --bundle animap.json.sigstore.json \
  --certificate-identity https://github.com/cplieger/animap/.github/workflows/publish.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

To build the file yourself, run this from the repository root with Go, `curl`, `jq` and an authenticated `gh` CLI:

```sh
AOD_VERSION=$(sed -n 's/^  AOD_VERSION: //p' .github/workflows/publish.yaml) DRY_RUN=1 bash scripts/publish.sh
```

`DRY_RUN=1` builds and checks the file and writes `./animap.json` without creating a release. `gh` is used only to read release and commit details.

## API

The format is described in [docs/schema.md](docs/schema.md), with a JSON Schema in [docs/animap.schema.json](docs/animap.schema.json).

- Top level: `version`, `generated_at`, `sources` with the exact inputs, `attribution` with the licence notice, and `records`.
- Ids: `anilist_id`, `anidb_id`, `anidb_parent`, `mal_id`, `tvdb_id`, `tmdb_tv_id`, `tmdb_movie_ids` and `imdb_ids`.
- Placement: `type`, `episodes`, `tvdb_season`, `tvdb_absolute`, `tvdb_episode_offset`, `tmdb_season` and `tmdb_episode_offset`.
- Per-episode rows: `mapping_list`, where each row is a range with an offset or a list of single episodes.

Four fields are present or absent rather than zero by default, because `0` is a real value for each: `tvdb_season`, `tvdb_episode_offset`, `tmdb_season` and `tmdb_episode_offset`. Test them for presence.

## How often it changes

A workflow builds a new file every 3 hours. It publishes the file only when a hash of its version, attribution and records differs from the latest release. Release tags are dates, such as `v2026.10.05`, with the time added for a second release that day. Each release's notes list the five counts below.

The anime-offline-database release is pinned in `.github/workflows/publish.yaml`. Renovate proposes each new weekly release, and the merge publishes. Anime-Lists has no releases, so each run reads its newest commit.

To skip an unchanged download, send your last `ETag` as `If-None-Match`, or compare the release tag.

A build stops before publishing when one of these checks fails, and the previous release stays the latest:

- Any of five counts falls below 90% of the latest release, unless the maintainer accepts the drop on a manual run. The five are records, AniList ids with an AniDB id, AniDB ids with a TVDB id, records with a TMDB id, and records with a mapping list.
- Two Anime-Lists entries on one TVDB series claim the same TVDB or TMDB episode.
- An Anime-Lists entry on a shared TVDB series has no episode count in any source, so its episodes cannot be placed.
- An input is larger than its limit, does not parse, or does not match its published SHA-256.

Anime-Lists already had some of these collisions and entries with no episode count when animap started. `checks/collision-baseline.json` lists them, and they stop a build only on a series animap corrects, as [docs/overlay.md](docs/overlay.md#collisions-on-other-series) explains.

## Data sources and licence

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](LICENSE-DATA), and the file carries the same notice in its `attribution` member. If you publish a database built from it, the ODbL asks you to keep that notice and to share it under the same licence.

It contains information from [anime-offline-database](https://github.com/cedya77/anime-offline-database), made available under the ODbL 1.0 and the DbCL 1.0, and from [Anime-Lists](https://github.com/Anime-Lists/anime-lists). Anime-Lists publishes no licence. anime-offline-database supplies the AniList, AniDB and MyAnimeList ids, the type and the episode count. Anime-Lists supplies the TVDB, TMDB and IMDb ids, the seasons and offsets, and the mapping lists. The way the two are joined follows [Fribb/anime-lists-generator](https://github.com/Fribb/anime-lists-generator), where anime-offline-database gives the identity fields and Anime-Lists fills the rest.

To notice when TheTVDB changes an episode order, each correction on a TVDB series stores a fingerprint of that order, read through the public metadata service Sonarr uses. The file holds no AniDB titles or descriptions, only AniDB ids and episode numbers. [docs/sources.md](docs/sources.md) covers each source and its licence.

## Documentation

- [The animap.json schema](docs/schema.md) lists every field, which records exist, and how to read the file in Go.
- [The overlay](docs/overlay.md) is animap's set of corrections to Anime-Lists, with the bar each one meets and how to add one.
- [The watch set](docs/watch.md) defines what "fully mapped" means for each type, and how a gap becomes an issue.
- [Data sources and licences](docs/sources.md) covers each source and the licence that applies.

## Contributing

Issues and pull requests are welcome. To report a bad mapping, give the AniList or AniDB id, what the file says and should say, and the AniDB, TVDB or TMDB page that shows it. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).

Third-party attributions are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
