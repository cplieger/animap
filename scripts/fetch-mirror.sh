#!/usr/bin/env bash
# Print the path of the AniDB mirror snapshot for MIRROR_COMMIT under
# CACHE_DIR, extracting it when none is kept; the archive goes straight into
# the extractor, so AniDB's titles and descriptions never reach the disk, and
# whatever reads a kept snapshot validates it. With `key` it prints only the
# snapshot's key, which names the file and both workflows' cache entries.
set -euo pipefail

MIRROR_COMMIT="${MIRROR_COMMIT:?set MIRROR_COMMIT (AnimeAggregations commit, pinned in publish.yaml)}"
if ! [[ "$MIRROR_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "fetch-mirror: ERROR MIRROR_COMMIT='${MIRROR_COMMIT}' is not a commit SHA" >&2
  exit 1
fi
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Every input that can change the snapshot, by path and content; a test
# file changes nothing extracted.
mirror_key() {
  local f h code
  code=$(
    cd "$ROOT" || exit 1
    for f in internal/anidb/*.go internal/strictjson/*.go cmd/animap/mirror.go go.mod go.sum; do
      [[ "$f" == *_test.go ]] && continue
      if [ ! -f "$f" ]; then
        printf 'fetch-mirror: ERROR extractor input %s is missing\n' "$f" >&2
        exit 1
      fi
      h=$(git hash-object -- "$f") || exit 1
      printf '%s %s\n' "$h" "$f"
    done | git hash-object --stdin
  ) || return 1
  printf '%s-%s\n' "$MIRROR_COMMIT" "$code"
}

mirror_snapshot() {
  local key out
  key=$(mirror_key) || return 1
  out="$CACHE_DIR/mirror/${key}.json"
  mkdir -p "$CACHE_DIR/mirror"
  if [ ! -f "$out" ]; then
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 20 --max-time 600 \
      --retry 3 --retry-delay 5 -fsSL --max-filesize 1073741824 \
      "https://codeload.github.com/notseteve/AnimeAggregations/tar.gz/${MIRROR_COMMIT}" \
      | (cd "$ROOT" && go run ./cmd/animap mirror extract -commit "$MIRROR_COMMIT" -out "$out") >&2 || return 1
  fi
  printf '%s\n' "$out"
}

case "${1:-}" in
  key) mirror_key ;;
  "")
    CACHE_DIR="${CACHE_DIR:?}"
    mirror_snapshot
    ;;
  *)
    printf "fetch-mirror: ERROR unknown argument '%s' (only key)\n" "$1" >&2
    exit 2
    ;;
esac
