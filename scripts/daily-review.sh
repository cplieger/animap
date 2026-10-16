#!/usr/bin/env bash
# Environment:
#   CACHE_DIR (optional) inputs, the AniDB mirror snapshot, the HTTP validator
#     cache and both providers' layouts, default ./.cache
#   TMDB_TOKEN (optional) TMDB read access token; unset leaves every TMDB
#     date section unchecked, never clear
#   DRY_RUN=1 (optional) write review.json and actions.json and apply nothing;
#     unset or empty is 0, and any value but 0 or 1 is read as 1
set -euo pipefail

# DRY_RUN decides whether issues are written, so a value it cannot read
# applies nothing rather than everything.
case "${DRY_RUN:-0}" in
  0 | 1) DRY_RUN="${DRY_RUN:-0}" ;;
  *)
    echo "daily-review: WARNING DRY_RUN is neither 0 nor 1, so this is a dry run" >&2
    DRY_RUN=1
    ;;
esac
REPO="${GITHUB_REPOSITORY:-cplieger/animap}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export CACHE_DIR="${CACHE_DIR:-$ROOT/.cache}"
mkdir -p "$CACHE_DIR"
cd "$ROOT"

LIST_COMMIT=$(gh api repos/Anime-Lists/anime-lists/commits/master --jq .sha)
if ! [[ "$LIST_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "daily-review: ERROR Anime-Lists master resolved to '${LIST_COMMIT}'" >&2
  exit 1
fi
LIST_BLOB=$(gh api "repos/Anime-Lists/anime-lists/contents/anime-list-master.xml?ref=${LIST_COMMIT}" --jq .sha)
if ! [[ "$LIST_BLOB" =~ ^[0-9a-f]{40}$ ]]; then
  echo "daily-review: ERROR anime-list-master.xml at ${LIST_COMMIT} has blob '${LIST_BLOB}'" >&2
  exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
args=(-existing "$WORK/existing.json" -out "$ROOT/actions.json")

code=$(curl --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 120 --retry 3 -sSL \
  -o "$WORK/release.json" -w '%{http_code}' "https://github.com/${REPO}/releases/latest/download/animap.json" || true)
release=()
case "$code" in
  200) release=(-release "$WORK/release.json") ;;
  404) ;;
  *)
    echo "daily-review: ERROR reading the latest release answered HTTP ${code}" >&2
    exit 1
    ;;
esac

# An overlay link's landed check reads the anime-offline-database release
# the latest animap release was built from; with no release, none is fetched.
aod_version="" aod_sha=""
if [ "${#release[@]}" -gt 0 ]; then
  aod_version=$(jq -r '.sources.anime_offline_database.release' "$WORK/release.json")
  aod_sha=$(jq -r '.sources.anime_offline_database.sha256' "$WORK/release.json")
  if ! [[ "$aod_version" =~ ^[0-9]{4}-[0-9]{2}$ && "$aod_sha" =~ ^[0-9a-f]{64}$ ]]; then
    echo "daily-review: ERROR the latest release names anime-offline-database '${aod_version}' with sha256 '${aod_sha}'" >&2
    exit 1
  fi
fi
list="" aod=""
out=$(AOD_VERSION="$aod_version" AOD_SHA256="$aod_sha" LIST_COMMIT="$LIST_COMMIT" LIST_BLOB="$LIST_BLOB" bash scripts/fetch-inputs.sh)
while IFS='=' read -r key value; do
  case "$key" in
    AOD_PATH) aod="$value" ;;
    LIST_PATH) list="$value" ;;
    *)
      echo "daily-review: ERROR unexpected line from fetch-inputs.sh: ${key}" >&2
      exit 1
      ;;
  esac
done <<<"$out"

# The date checks and the links' AniDB check read the AniDB mirror at the
# commit the build is pinned to; a mirror that cannot be read leaves them
# unevaluated and the rest of the review goes on.
MIRROR_COMMIT=$(sed -n 's/^  MIRROR_COMMIT: //p' .github/workflows/publish.yaml)
mirror=$(MIRROR_COMMIT="$MIRROR_COMMIT" bash scripts/fetch-mirror.sh) || mirror=""

review_inputs=()
[ -n "$aod" ] && review_inputs+=(-aod "$aod")
if [ -n "$mirror" ]; then
  review_inputs+=(-mirror "$mirror")
else
  echo "daily-review: WARNING the AniDB mirror could not be read, the date checks are left as they are" >&2
fi
review_inputs+=("${release[@]}")
go run ./cmd/animap review -list "$list" -list-commit "$LIST_COMMIT" -overlay overlay "${review_inputs[@]}" \
  -layouts-tvdb "$CACHE_DIR/layouts-tvdb.json" -layouts-tmdb "$CACHE_DIR/layouts-tmdb.json" \
  -cache "$CACHE_DIR/http.json" -out "$ROOT/review.json"
args+=(-review "$ROOT/review.json")

gh run list --repo "$REPO" --workflow publish.yaml --limit 100 --json conclusion,status,createdAt,url >"$WORK/runs.json"
args+=(-runs "$WORK/runs.json")
# Every page of every animap issue, open and closed: an issue a capped
# listing left out would have its key opened a second time.
gh api --paginate --slurp "repos/${REPO}/issues?labels=animap&state=all&per_page=100" \
  | jq '[.[][] | select(.pull_request == null) | {number, state: (.state | ascii_upcase), title, body: (.body // "")}]' \
    >"$WORK/existing.json"

go run ./cmd/animap issues "${args[@]}"
if [ "$DRY_RUN" = "1" ]; then
  echo "daily-review: DRY RUN, plan at ./actions.json, review at ./review.json"
else
  bash scripts/apply-issues.sh "$ROOT/actions.json"
fi

# A token TMDB refuses is a broken credential: the plan above still
# applies, with every TMDB section carried as it was, and the run goes red.
if jq -e '.sources["date-tmdb"].refused == true' "$ROOT/review.json" >/dev/null; then
  echo "daily-review: ERROR TMDB refused TMDB_TOKEN; replace the repository secret" >&2
  exit 1
fi
