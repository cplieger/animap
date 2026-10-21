# Contributing to animap

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) apply here, apart from their release table.

## Scope

The mapping is kept in a private database, not in this repository, so a correction is an issue rather than a pull request. Give the AniList or AniDB id, what the file says and should say, and the AniDB, TheTVDB or TMDB page that shows it. Each new file published here carries the corrections made since the last one.

## Rules

A new or changed field in `animap.json` lands in one change in every place that describes it:

- the types in `mapread`, with their check in `mapread`'s decoder
- `docs/animap.schema.json`
- `docs/schema.md`
- the README's [API](README.md#api) list, and `mapread/README.md` when a lookup changes

The file is built with `mapread`'s encoder, so a format change reaches the file only after a `mapread` release. The tests compare the JSON Schema's member names with the types, and the schema with the decoder over `mapread`'s fixtures, but nothing catches stale prose in either page.

## Checks

Pull request CI never reads the published export. Before you merge a change to a path that starts the [publish workflow](.github/workflows/publish.yaml), run a dry run from the repository root with Go, `curl`, `jq` and an authenticated `gh` CLI:

```sh
DRY_RUN=1 ANIMAP_EXPORT_TOKEN=<token> sh scripts/publish.sh
```

The token needs read access to the mapping database's releases. The dry run downloads the latest export, checks its checksum, verifies it against the latest release and prints the tag and notes it would publish, without signing or releasing. `EXPORT_TAG` reads another export release, and `ACCEPT_STALE=1` with a `STALE_REASON` skips the freshness checks. A manual run of the publish workflow with `dry_run` does the same in CI.

Without it, a change that fails a check merges green. Every publish run after the merge then fails, and the previous release stays the latest.

## Releases

Commit types cut no release here, although the repository carries the synced `cliff.toml`. The `mapread` Go module is released by a `mapread/vX.Y.Z` tag on `main`, never as a GitHub release, so the latest release stays the data file.
