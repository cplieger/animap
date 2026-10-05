#!/usr/bin/env bash
# Build animap.json from the pinned offline database, Anime-Lists master and
# the overlay, and publish it as a dated GitHub release when its content hash
# differs from the latest release's.
#
# Environment:
#   AOD_VERSION   (required) anime-offline-database release tag, e.g. 2026-40
#   AOD_SHA256, LIST_COMMIT, LIST_BLOB
#                 (optional) from scripts/resolve.sh; resolved here when unset
#   CACHE_DIR     (optional) where inputs are kept, default ./.cache
#   ACCEPT_SHRINK=true  (optional) pass -accept-shrink to the build
#   DRY_RUN=1     (optional) build and check only, write ./animap.json, no release
set -euo pipefail

AOD_VERSION="${AOD_VERSION:?set AOD_VERSION (anime-offline-database release tag, e.g. 2026-40)}"
DRY_RUN="${DRY_RUN:-0}"
REPO="${GITHUB_REPOSITORY:-cplieger/animap}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export CACHE_DIR="${CACHE_DIR:-$ROOT/.cache}"

# Read the KEY=value lines the helper scripts print, accepting only the
# names this script expects.
load() {
  local key value out
  out=$(bash "$1")
  while IFS='=' read -r key value; do
    case "$key" in
      AOD_SHA256 | LIST_COMMIT | LIST_BLOB | AOD_PATH | LIST_PATH) printf -v "$key" '%s' "$value" ;;
      *)
        echo "publish: ERROR unexpected line from $1: ${key}" >&2
        exit 1
        ;;
    esac
  done <<<"$out"
}

if [ -z "${AOD_SHA256:-}" ] || [ -z "${LIST_COMMIT:-}" ] || [ -z "${LIST_BLOB:-}" ]; then
  load "$ROOT/scripts/resolve.sh"
fi
export AOD_VERSION AOD_SHA256 LIST_COMMIT LIST_BLOB
load "$ROOT/scripts/fetch-inputs.sh"
[ -n "${AOD_PATH:-}" ] && [ -n "${LIST_PATH:-}" ] || {
  echo "publish: ERROR the inputs were not fetched" >&2
  exit 1
}
echo "publish: database ${AOD_VERSION} (sha256 ${AOD_SHA256}), Anime-Lists ${LIST_COMMIT}"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The previous release is the coverage baseline and the hash to compare
# against. A 404 means there is none yet; any other failure stops the run,
# because building without a baseline would skip the coverage guard.
LATEST_URL="https://github.com/${REPO}/releases/latest/download/animap.json"
code=$(curl --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 120 --retry 3 -sSL \
  -o "$WORK/previous.json" -w '%{http_code}' "$LATEST_URL" || true)
case "$code" in
  200) ;;
  404) rm -f "$WORK/previous.json" ;;
  *)
    echo "publish: ERROR reading the latest release answered HTTP ${code}" >&2
    exit 1
    ;;
esac

shrink=()
[ "${ACCEPT_SHRINK:-false}" = "true" ] && shrink=(-accept-shrink)
(cd "$ROOT" && go run ./cmd/animap build \
  -aod "$AOD_PATH" -aod-release "$AOD_VERSION" -aod-sha256 "$AOD_SHA256" \
  -list "$LIST_PATH" -list-commit "$LIST_COMMIT" -overlay overlay \
  -previous "$WORK/previous.json" -out "$WORK/animap.json" -stats "$WORK/stats.json" "${shrink[@]}")

HASH=$(jq -r .content_hash "$WORK/stats.json")
RECORDS=$(jq -r .populations.records "$WORK/stats.json")
if [ "$DRY_RUN" = "1" ]; then
  cp "$WORK/animap.json" "$ROOT/animap.json"
  echo "publish: DRY RUN, ${RECORDS} records, content hash ${HASH}, changed $(jq -r .changed "$WORK/stats.json"); artifact at ./animap.json"
  exit 0
fi
if [ "$(jq -r .changed "$WORK/stats.json")" != "true" ]; then
  echo "publish: content hash ${HASH} equals the latest release's, nothing to publish"
  exit 0
fi

SHA="${GITHUB_SHA:?set GITHUB_SHA to the commit this file was built from}"

# Print the jq filter $2 over GitHub API resource $1, or nothing when the
# resource does not exist. gh prints an error response's body on stdout, so
# the status is read from its stderr; any failure but a 404 aborts the run.
gh_get() {
  local out err rc=0
  err=$(mktemp)
  out=$(gh api "$1" --jq "$2" 2>"$err") || rc=$?
  if [ "$rc" -eq 0 ]; then
    rm -f "$err"
    printf '%s\n' "$out"
    return 0
  fi
  if grep -Eq 'HTTP 404([^0-9]|$)' "$err"; then
    rm -f "$err"
    return 0
  fi
  cat "$err" >&2
  rm -f "$err"
  echo "publish: ERROR reading ${1} from the GitHub API failed (gh exit ${rc})" >&2
  exit 1
}
tag_at() { gh_get "repos/${REPO}/git/ref/tags/$1" .object.sha; }

# Each lookup is its own assignment so a failed one stops the run: inside a
# test such as [ -n "$(...)" ] its exit status is dropped and it reads as absent.
TAG="v$(date -u +%Y.%m.%d)"
at=$(tag_at "$TAG")
if [ -n "$at" ]; then
  released=$(gh_get "repos/${REPO}/releases/tags/${TAG}" .id)
  if [ "$at" != "$SHA" ] || [ -n "$released" ]; then
    TAG="${TAG}.$(date -u +%H%M)"
    at=$(tag_at "$TAG")
  fi
fi
cd "$WORK"
sha256sum animap.json >animap.json.sha256
cosign sign-blob --yes animap.json --bundle animap.json.sigstore.json

jq -r --arg tag "$TAG" --arg repo "$REPO" '
  "content_hash: \(.content_hash)",
  "",
  "Records: \(.populations.records). AniList ids with an AniDB id: \(.populations.anilist_with_anidb). AniDB ids with a TVDB id: \(.populations.anidb_with_tvdb). Records with a TMDB id: \(.populations.with_tmdb). Records with a mapping list: \(.populations.with_mapping_list). Overlay entries applied: \(.overlay_entries).",
  ""' stats.json >notes.md
jq -r '
  "Sources: anime-offline-database \(.sources.anime_offline_database.release) (sha256 \(.sources.anime_offline_database.sha256)), Anime-Lists commit \(.sources.anime_lists.commit), overlay sha256 \(.sources.overlay.sha256).",
  "",
  .attribution.notice,
  ""' animap.json >>notes.md
cat >>notes.md <<EOF
Verify the download:

\`\`\`sh
sha256sum -c animap.json.sha256
cosign verify-blob animap.json --bundle animap.json.sigstore.json \\
  --certificate-identity https://github.com/${REPO}/.github/workflows/publish.yaml@refs/heads/main \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
\`\`\`
EOF

# The tag is created first, at the commit that built this file: a tag made
# by gh release create lands on main's head at release time, and the token
# cannot create one at an older commit once main moved past changes under
# .github/workflows/. A tag already at this commit is a rerun and is reused.
if [ -z "$at" ]; then
  if ! gh api "repos/${REPO}/git/refs" -f ref="refs/tags/${TAG}" -f sha="$SHA" >/dev/null 2>tagerr; then
    cat tagerr >&2
    head=$(gh_get "repos/${REPO}/git/ref/heads/main" .object.sha)
    if [ -n "$head" ] && [ "$head" != "$SHA" ]; then
      echo "publish: main moved from ${SHA} to ${head} and ${TAG} could not be tagged here; the run on the newer commit publishes"
      exit 0
    fi
    echo "publish: ERROR could not create tag ${TAG} at ${SHA}" >&2
    exit 1
  fi
elif [ "$at" != "$SHA" ]; then
  echo "publish: ERROR tag ${TAG} already exists at ${at}, not at ${SHA}" >&2
  exit 1
fi

# --latest is explicit: dated tags are not semver, so GitHub's automatic
# choice of the latest release cannot be trusted.
gh release create "$TAG" animap.json animap.json.sha256 animap.json.sigstore.json \
  --repo "$REPO" --title "$TAG" --latest --verify-tag --notes-file notes.md

# GitHub repoints the latest download URL a moment after the release
# exists, so poll until it serves this tag.
attempt=1
while :; do
  location=$(curl -sI -o /dev/null -w '%{redirect_url}' --connect-timeout 10 --max-time 20 "$LATEST_URL" || true)
  case "$location" in
    *"/${TAG}/"*)
      echo "publish: released ${TAG} (${RECORDS} records); latest pointer verified"
      break
      ;;
  esac
  if [ "$attempt" -ge 10 ]; then
    echo "publish: ERROR released ${TAG} but the latest download URL resolves to: ${location}" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 3
done
