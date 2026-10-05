#!/usr/bin/env bash
# Resolve both upstreams to content addresses and print them as KEY=value
# lines: the offline-database asset digest for AOD_VERSION, and the
# Anime-Lists master commit with the list file's blob SHA at that commit.
# Every value is validated here, so callers may use them in cache keys.
set -euo pipefail

AOD_VERSION="${AOD_VERSION:?set AOD_VERSION (anime-offline-database release tag, e.g. 2026-40)}"
if ! [[ "$AOD_VERSION" =~ ^[0-9]{4}-[0-9]{2}$ ]]; then
  echo "resolve: ERROR AOD_VERSION='${AOD_VERSION}' is not a YYYY-WW release tag" >&2
  exit 1
fi

digest=$(gh api "repos/cedya77/anime-offline-database/releases/tags/${AOD_VERSION}" \
  --jq '.assets[] | select(.name == "anime-offline-database.jsonl") | .digest')
aod_sha256="${digest#sha256:}"
if ! [[ "$digest" == sha256:* && "$aod_sha256" =~ ^[0-9a-f]{64}$ ]]; then
  echo "resolve: ERROR release ${AOD_VERSION} has no sha256 digest for anime-offline-database.jsonl (got '${digest}')" >&2
  exit 1
fi

list_commit=$(gh api repos/Anime-Lists/anime-lists/commits/master --jq .sha)
if ! [[ "$list_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "resolve: ERROR Anime-Lists master resolved to '${list_commit}', not a commit" >&2
  exit 1
fi
list_blob=$(gh api "repos/Anime-Lists/anime-lists/contents/anime-list-master.xml?ref=${list_commit}" --jq .sha)
if ! [[ "$list_blob" =~ ^[0-9a-f]{40}$ ]]; then
  echo "resolve: ERROR anime-list-master.xml at ${list_commit} has blob '${list_blob}'" >&2
  exit 1
fi

printf 'AOD_SHA256=%s\nLIST_COMMIT=%s\nLIST_BLOB=%s\n' "$aod_sha256" "$list_commit" "$list_blob"
