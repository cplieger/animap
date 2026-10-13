#!/usr/bin/env bash
# Print the path of the AniDB mirror snapshot for MIRROR_COMMIT under
# CACHE_DIR, extracting it from the mirror's archive when none is kept.
# The archive goes straight into the extractor, so AniDB's titles and
# descriptions never reach the disk; a kept snapshot is validated by
# whatever reads it. A snapshot is kept per mirror commit and extractor
# source, as the workflows key their caches, so a change to what the
# extractor keeps extracts the snapshot again.
set -euo pipefail

MIRROR_COMMIT="${MIRROR_COMMIT:?set MIRROR_COMMIT (AnimeAggregations commit, pinned in publish.yaml)}"
if ! [[ "$MIRROR_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "fetch-mirror: ERROR MIRROR_COMMIT='${MIRROR_COMMIT}' is not a commit SHA" >&2
  exit 1
fi
CACHE_DIR="${CACHE_DIR:?}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

mirror_snapshot() {
  local code out
  code=$(cat "$ROOT"/internal/anidb/*.go | git hash-object --stdin) || return 1
  out="$CACHE_DIR/mirror/${MIRROR_COMMIT}-${code}.json"
  mkdir -p "$CACHE_DIR/mirror"
  if [ ! -f "$out" ]; then
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 20 --max-time 600 \
      --retry 3 --retry-delay 5 -fsSL --max-filesize 1073741824 \
      "https://codeload.github.com/notseteve/AnimeAggregations/tar.gz/${MIRROR_COMMIT}" \
      | (cd "$ROOT" && go run ./cmd/animap mirror extract -commit "$MIRROR_COMMIT" -out "$out") >&2 || return 1
  fi
  printf '%s\n' "$out"
}

mirror_snapshot
