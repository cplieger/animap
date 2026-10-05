# Contributing to animap

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) apply here, apart from their release table.

## Rules

A new or changed field in `animap.json` lands in one change in every place that describes it:

- the struct in `internal/schema`
- `docs/animap.schema.json`
- `docs/schema.md`
- the README's [API](README.md#api) list, for a top-level or record field

The tests compare only the JSON Schema's member names with the struct, so nothing catches stale prose in either page.

## Checks

Pull request CI never builds `animap.json` from the real inputs. Before you merge a change to a path that starts the [publish workflow](.github/workflows/publish.yaml), `go.mod` and the build scripts included, run the README's [dry-run build](README.md#usage).

Without it, a change that fails a build check, such as a count drop or a new collision, merges green. Every publish run after the merge then fails, and the previous release stays the latest.

## Releases

Commit types cut no release here, although the repository carries the synced `cliff.toml`.
