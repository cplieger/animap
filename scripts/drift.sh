#!/usr/bin/env bash
# Daily checks: overlay drift against Anime-Lists HEAD and SkyHook, the
# checks/counts.json rows against the database the latest release was built
# from, the watch set against that release, and the publish health, turned
# into one issue plan and applied.
#
# Environment:
#   CACHE_DIR      (optional) inputs and the HTTP validator cache,
#                  default ./.cache
#   DRY_RUN=1      (optional) write actions.json and apply nothing
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"
REPO="${GITHUB_REPOSITORY:-cplieger/animap}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export CACHE_DIR="${CACHE_DIR:-$ROOT/.cache}"
mkdir -p "$CACHE_DIR"
cd "$ROOT"

LIST_COMMIT=$(gh api repos/Anime-Lists/anime-lists/commits/master --jq .sha)
if ! [[ "$LIST_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "drift: ERROR Anime-Lists master resolved to '${LIST_COMMIT}'" >&2
  exit 1
fi
LIST_BLOB=$(gh api "repos/Anime-Lists/anime-lists/contents/anime-list-master.xml?ref=${LIST_COMMIT}" --jq .sha)
if ! [[ "$LIST_BLOB" =~ ^[0-9a-f]{40}$ ]]; then
  echo "drift: ERROR anime-list-master.xml at ${LIST_COMMIT} has blob '${LIST_BLOB}'" >&2
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
    echo "drift: ERROR reading the latest release answered HTTP ${code}" >&2
    exit 1
    ;;
esac

# The counts rows are checked against the database the latest release was
# built from, so they need a release and at least one row.
AOD_VERSION=""
if [ "${#release[@]}" -gt 0 ] && [ "$(jq length checks/counts.json)" -gt 0 ]; then
  AOD_VERSION=$(jq -r .sources.anime_offline_database.release "$WORK/release.json")
  AOD_SHA256=$(jq -r .sources.anime_offline_database.sha256 "$WORK/release.json")
  if ! [[ "$AOD_VERSION" =~ ^[0-9]{4}-[0-9]{2}$ && "$AOD_SHA256" =~ ^[0-9a-f]{64}$ ]]; then
    echo "drift: ERROR the latest release names database '${AOD_VERSION}' with sha256 '${AOD_SHA256}'" >&2
    exit 1
  fi
  export AOD_SHA256
fi
export AOD_VERSION LIST_COMMIT LIST_BLOB
aod_path="" list=""
out=$(bash scripts/fetch-inputs.sh)
while IFS='=' read -r key value; do
  case "$key" in
    AOD_PATH) aod_path="$value" ;;
    LIST_PATH) list="$value" ;;
    *)
      echo "drift: ERROR unexpected line from fetch-inputs.sh: ${key}" >&2
      exit 1
      ;;
  esac
done <<<"$out"
aod=()
[ -n "$aod_path" ] && aod=(-aod "$aod_path")

if [ -f watch/seadex.json ] && [ "${#release[@]}" -gt 0 ]; then
  go run ./cmd/animap watch -config watch/seadex.json -backlog watch/backlog.json -unmappable checks/unmappable.json \
    "${release[@]}" -cache "$CACHE_DIR/http.json" -out "$WORK/watch.json"
  args+=(-watch "$WORK/watch.json")
elif [ -f watch/seadex.json ]; then
  echo "drift: no published release to watch (HTTP ${code}), skipping the watch set"
fi

go run ./cmd/animap drift -overlay overlay -list "$list" "${release[@]}" "${aod[@]}" -counts checks/counts.json \
  -cache "$CACHE_DIR/http.json" -out "$WORK/drift.json"
args+=(-drift "$WORK/drift.json")

gh run list --repo "$REPO" --workflow publish.yaml --limit 100 --json conclusion,status,createdAt,url >"$WORK/runs.json"
args+=(-runs "$WORK/runs.json")
gh issue list --repo "$REPO" --label animap --state all --limit 1000 --json number,state,title,body >"$WORK/existing.json"

go run ./cmd/animap issues "${args[@]}"
if [ "$DRY_RUN" = "1" ]; then
  echo "drift: DRY RUN, plan at ./actions.json"
  exit 0
fi
bash scripts/apply-issues.sh "$ROOT/actions.json"
