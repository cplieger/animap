# animap

[![License](https://img.shields.io/github/license/cplieger/animap)](LICENSE)

animap publishes `animap.json`, one file that maps anime ids between AniList, AniDB, MyAnimeList, TheTVDB, TMDB and IMDb, with the TVDB and TMDB seasons, episode offsets and per-episode mappings. Anyone can download it. The data is licensed under the ODbL 1.0 and the code under Apache-2.0.

## Who it is for

It is for tools that sit beside Sonarr and Radarr, such as a SeaDex watcher or a list sync. Those tools need to turn an AniList or AniDB id into the id and season that Sonarr or Radarr uses. animap joins two open sources into one file and adds corrections of its own, so a tool reads one file instead of two:

- [anime-offline-database](https://github.com/cedya77/anime-offline-database) supplies the AniList, AniDB and MyAnimeList ids, the type and the episode count.
- [Anime-Lists](https://github.com/Anime-Lists/anime-lists) supplies the TVDB, TMDB and IMDb ids, the seasons and offsets, and the mapping list that places specials and films.

It does not look ids up on TMDB or anywhere else. Every value comes from those two sources or from the overlay below.

## Downloading it

The newest release is always at this address:

```text
https://github.com/cplieger/animap/releases/latest/download/animap.json
```

The file is about 2.3 MB. Each release also carries `animap.json.sha256` and `animap.json.sigstore.json`, a keyless [cosign](https://docs.sigstore.dev/) signature made by this repository's publish workflow. To check a download:

```sh
sha256sum -c animap.json.sha256
cosign verify-blob animap.json --bundle animap.json.sigstore.json \
  --certificate-identity https://github.com/cplieger/animap/.github/workflows/publish.yaml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The format is described in [docs/schema.md](docs/schema.md), with a JSON Schema in [docs/animap.schema.json](docs/animap.schema.json). Each record leaves out the fields it has no value for. Read `version` first. It changes only when the format breaks.

## How often it changes

A workflow checks both sources every 3 hours. It builds a new file and compares a hash of its records with the latest release, and it publishes only when they differ. Releases are tagged with the date, such as `v2026.10.05`, with the time added for a second release that day.

The anime-offline-database release is pinned in `.github/workflows/publish.yaml`. Renovate proposes a bump when a new release appears, about once a week, and the merge publishes. Anime-Lists has no releases, so each run reads its newest commit.

To avoid downloading the file when nothing changed, send the `ETag` from your last download back as `If-None-Match`, or compare the release tag first.

A build stops before publishing when one of these checks fails, and the previous release stays the latest:

- Any of five counts falls below 90% of the latest release. The five are records, AniList ids with an AniDB id, AniDB ids with a TVDB id, records with a TMDB id, and records with a mapping list.
- Two Anime-Lists nodes on one TVDB series claim the same TVDB or TMDB episode. Collisions Anime-Lists already had when animap started are recorded in `checks/collision-baseline.json` and do not stop a build, except on a series the overlay touches.
- An Anime-Lists node on a TVDB series it shares has no episode count, so the check cannot place its episodes. The same baseline records the ones Anime-Lists had when animap started. A count proven from AniDB's episode list can go in `checks/counts.json`, and the check then places that node's episodes.
- An input is larger than its bound, does not parse, or does not match its published SHA-256.

## The overlay and the watch set

The overlay in `overlay/` holds corrections to Anime-Lists, one file per entry. Each is proven against AniDB's episode list, TVDB's official episode order and TMDB, and each is also proposed to Anime-Lists. A daily check opens an issue when Anime-Lists changes the node it patches, when the fix lands upstream and the entry can go, or when TVDB's episode order changes under it. It also opens one when anime-offline-database starts to list an Anime-Lists node that `checks/counts.json` counts, so the row can go once the database counts it. [docs/overlay.md](docs/overlay.md) explains the format and the bar an entry must meet.

Some specials are listed on AniDB as episodes of their main series. A file in `overlay/special-of-parent/` maps such an AniList entry onto the main series' specials, so it gets a TVDB episode too.

The watch set is a list of AniList ids that animap checks every day for complete mappings. It holds every title on [SeaDex](https://releases.moe). A new watched special or film that does not map fully to a TVDB episode or a TMDB film gets an issue. The gap is then fixed with an overlay entry and the same change goes to Anime-Lists. Gaps that no source can fix are listed in `checks/unmappable.json` and in one pinned issue. [docs/watch.md](docs/watch.md) defines what "fully mapped" means for each type.

## Reporting a bad mapping

Open an issue with the AniList or AniDB id, what the file says, and what it should say, with links to the AniDB, TVDB or TMDB pages that show it. A pull request adding an overlay entry is also welcome. Most mappings come from Anime-Lists, so a fix there helps everyone who uses it.

## Building it locally

From the repository root, with Go, `curl`, `jq` and an authenticated `gh` CLI:

```sh
AOD_VERSION=$(sed -n 's/^  AOD_VERSION: //p' .github/workflows/publish.yaml) DRY_RUN=1 bash scripts/publish.sh
```

`DRY_RUN=1` builds and checks the file and writes `./animap.json` without creating a release. `gh` is used only to read release and commit details.

## Data sources and licence

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](LICENSE-DATA), and the file carries the same notice in its `attribution` member. If you publish a database built from it, the ODbL asks you to keep that notice and to share it under the same licence.

It contains information from [anime-offline-database](https://github.com/cedya77/anime-offline-database), made available under the ODbL 1.0 and the DbCL 1.0, and from [Anime-Lists](https://github.com/Anime-Lists/anime-lists). The episode orders in overlay fingerprints are read from TheTVDB through Sonarr's metadata service. The file holds no AniDB titles or descriptions, only AniDB ids and episode numbers. [docs/sources.md](docs/sources.md) covers each source and its licence.

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).

Third-party attributions are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
