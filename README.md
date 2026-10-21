# animap

[![License](https://img.shields.io/github/license/cplieger/animap)](LICENSE)

animap turns an AniList or AniDB anime id into its TheTVDB series, seasons and episodes, its TMDB movies and its MyAnimeList and IMDb ids, from one JSON file.

The mapping lives in a database the project maintains privately and checks against AniDB's episode lists, TheTVDB's official episode order and TMDB, and a new file is built from it each week. A tool downloads `animap.json`, about 11 MB, and reads it with any JSON parser, or in Go with the [mapread](mapread/README.md) module. The data is licensed under the ODbL 1.0 and the code under Apache-2.0.

## Why use it

animap is built for tools beside Sonarr and Radarr that need the id, season and episodes those apps use, such as a SeaDex watcher, a list sync or a subtitle fetcher.

- Each AniDB episode says where it lands on TheTVDB and its TheTVDB episode id where known, specials filed under another show included.
- Each film and special names its TMDB movie, where TMDB holds one.
- It answers both ways, from an AniList entry to its TheTVDB episodes, and from a TheTVDB episode or a TMDB movie to every AniDB episode on it.
- A placement that rests only on a default carried over from Anime-Lists is flagged.
- A release is published only once it passes the checks under [How often it changes](#how-often-it-changes).

Consider [arm-server](https://github.com/BeeeQueue/arm-server) if you want lookups through an HTTP API.

## Install

```sh
curl -fLO https://github.com/cplieger/animap/releases/latest/download/animap.json
```

## Usage

The file has two tables. `entries` says which AniDB episodes an AniList entry is, and `anime` says which episodes AniDB lists and where each one lands. This is the entry for AniList id 1:

```json
{ "anilist_id": 1, "type": "TV", "ids": { "anidb": [23], "mal": [1] }, "parts": [ { "anidb_id": 23, "kind": "regular" } ] }
```

Its part is every regular episode of AniDB 23, whose 17 runs include these:

```json
{
  "anidb_id": 23, "type": "TV", "tvdb_id": 76885, "mal_ids": [1, 4037, 17205],
  "last": { "regular": 26, "special": 14, "credit": 4, "promotion": 10, "parody": 1 },
  "episodes_listed": true,
  "episodes": [
    { "kind": "regular", "start": 1, "end": 1, "anidb_episode_ids": [881], "tvdb": [{ "season": 1, "episode": 13, "episode_ids": [219120] }] },
    { "kind": "regular", "start": 2, "end": 3, "anidb_episode_ids": [879, 877], "tvdb": [{ "season": 1, "episode": 1, "episode_ids": [219121, 219122] }] },
    { "kind": "regular", "start": 19, "end": 19, "anidb_episode_ids": [858], "tvdb": [{ "season": 1, "episode": 19, "episode_ids": [300641], "default": true }] },
    { "kind": "special", "start": 4, "end": 4, "anidb_episode_ids": [23130], "tvdb": [] },
    { "kind": "promotion", "start": 1, "end": 1, "anidb_episode_ids": [22748], "tvdb": [] },
    { "kind": "promotion", "start": 6, "end": 10, "anidb_episode_ids": [36634, 46674, 46675, 36590, 155750], "tvdb": [] }
  ]
}
```

AniDB episode 1, AniDB episode id 881, is TheTVDB season 1, episode 13, TheTVDB episode id 219120. Episode 19 lands on episode 19 by Anime-Lists' default, not a stated placement. Special 4 and the promotions land nowhere on TheTVDB, and AniDB lists no promotions 2 to 5. A film states its TMDB movie on its episodes: AniDB 28, which AniDB lists in six parts, has `{ "kind": "regular", "start": 1, "end": 6, ..., "tmdb_movies": [149] }`. Find an entry with `jq`:

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

To run the publish checks on a file yourself, run this with Go from the repository root:

```sh
go run ./cmd/animap verify -in animap.json
```

It prints the content hash and the counts the release notes carry, and exits 1 when a check fails. Add `-previous <file>` to compare the counts and the hash with another release.

## API

The format is described in [docs/schema.md](docs/schema.md), with a JSON Schema in [docs/animap.schema.json](docs/animap.schema.json). The Go module [mapread](mapread/README.md) reads it and answers the lookups.

- Top level: `version`, `generated_at`, `sources`, `attribution`, `anime` and `entries`.
- Anime rows: `anidb_id`, `type`, `tvdb_id`, `mal_ids`, `imdb_ids`, `last`, `episodes_listed` and `episodes`, the runs.
- Runs: `kind`, `start`, `end`, `anidb_episode_ids`, `tvdb` targets and `tmdb_movies`. A target is a season and an episode, with its own `series` when it is not on the row's `tvdb_id`, its TheTVDB `episode_ids`, and `default` when it rests only on Anime-Lists' default.
- Entries: `anilist_id`, `type`, `ids`, holding `anidb` and `mal`, and `parts`, the AniDB episodes the entry is.

A run with `"tvdb": []` lands nowhere on TheTVDB, and a run with no `tvdb` states nothing there. An episode no run names lands nowhere on the row's series when its number is from 1 to the kind's `last`. It also lands nowhere when the row has `episodes_listed`, and is then one AniDB does not list. Past `last` on a row without `episodes_listed`, the file does not place it. An episode is a TMDB movie only where its run states `tmdb_movies`.

## How often it changes

The mapping database builds a new file once a week. A workflow here runs every day at 12:00 UTC, reads the newest build, and publishes it only when a hash of its version, attribution and tables differs from the latest release. Release tags are dates, such as `v2026.10.05`, with the time added for a second release that day. Each release's notes carry the build's `revision` and AniDB title list date, and the six counts below.

To skip an unchanged download, compare the release tag `https://github.com/cplieger/animap/releases/latest` redirects to with the one you hold, or send your last `ETag` as `If-None-Match`.

A run stops before publishing when one of these checks fails, and the previous release stays the latest:

- The download does not match its SHA-256, or mapread refuses the file.
- The file was built more than 8 days ago, or its AniDB title list is more than 14 days old. The maintainer can accept an old file on a manual run, and the release notes then say so and why.
- Any of six counts falls below 90% of the latest release, unless the maintainer accepts the drop on a manual run. The six are AniList entries, AniList entries with parts, anime on TheTVDB, AniDB episodes on TheTVDB, AniDB episodes with a TMDB movie and AniDB episode ids. With no latest release of this shape, each count must reach a fixed floor instead.

## Data sources and licence

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](LICENSE-DATA), and the file carries the same notice in its `attribution` member. If you publish a database built from it, the ODbL asks you to keep that notice and to share it under the same licence.

The mapping database started from earlier releases of this file. Those joined [anime-offline-database](https://github.com/cedya77/anime-offline-database) and [Anime-Lists](https://github.com/Anime-Lists/anime-lists), so the database still contains their information. anime-offline-database is made available under the ODbL 1.0 and the DbCL 1.0 and gave the initial AniList links. Anime-Lists, which publishes no licence, gave the initial TheTVDB and TMDB mappings.

From [AniDB](https://anidb.net), it holds which episodes exist, with their kind, number and AniDB episode id, the anime types and the MyAnimeList links. AniDB's data is licensed under CC BY-NC-SA 4.0. `animap.json` carries none of the content AniDB holds for an episode, such as titles, summaries, air dates, lengths or ratings. The AniList ids are from [AniList](https://anilist.co).

TVDB data is provided by [TheTVDB](https://thetvdb.com), which gives the series ids, season and episode numbers and episode ids. This product uses the TMDB API but is not endorsed or certified by TMDB. `animap.json` holds no TheTVDB or TMDB titles or dates. [docs/sources.md](docs/sources.md) covers each source and its licence.

## Documentation

- [The animap.json schema](docs/schema.md) lists every field and how to read an episode's placement.
- [mapread](mapread/README.md) is the Go module that reads, verifies and looks up the file.
- [Data sources and licences](docs/sources.md) covers each source and its licence.

## Contributing

Pull requests are welcome for the code and docs. A bad mapping is reported in an issue with the AniList or AniDB id, what the file says and should say, and the page that shows it. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).

Third-party attributions are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
