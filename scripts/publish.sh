#!/usr/bin/env bash
# Publishes the latest mapping export as this repository's signed, dated
# release of animap.json. POSIX sh, so `sh scripts/publish.sh` runs it too.
#
# Usage: publish.sh [download DIR | release DIR]
#   download DIR  download the export into DIR and check its checksum
#   release DIR   verify the export in DIR and release it
#   (none)        both, through a temporary directory
#
# Environment:
#   ANIMAP_EXPORT_TOKEN (download) a token that reads the export repository's
#     releases
#   EXPORT_TAG (download, optional) the export release to read, default the
#     export repository's latest release
#   ACCEPT_SHRINK=true (release, optional) pass -accept-shrink to verify
#   ACCEPT_STALE=1 or true (release, optional) pass -accept-stale to verify;
#     STALE_REASON must then give the reason, which the release notes carry
#   DRY_RUN=1 (release, optional) stop before signing and releasing; unset or
#     empty is 0, and any value but 0 or 1 is read as 1
#   GITHUB_SHA (release) the commit to tag; a dry run defaults to HEAD
set -euo pipefail

EXPORT_REPO=cplieger/animap-source
REPO="${GITHUB_REPOSITORY:-cplieger/animap}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LATEST_URL="https://github.com/${REPO}/releases/latest/download/animap.json"

# read_options reads DRY_RUN, ACCEPT_STALE and STALE_REASON. DRY_RUN
# decides whether a release is created, so a value it cannot read creates
# none; an ACCEPT_STALE it cannot read, or one with no reason, stops the run.
read_options() {
  case "${DRY_RUN:-0}" in
    0 | 1) DRY_RUN="${DRY_RUN:-0}" ;;
    *)
      echo "publish: WARNING DRY_RUN is neither 0 nor 1, so this is a dry run" >&2
      DRY_RUN=1
      ;;
  esac
  case "${ACCEPT_STALE:-}" in
    '' | 0 | false) ACCEPT_STALE=0 ;;
    1 | true) ACCEPT_STALE=1 ;;
    *)
      echo "publish: ERROR ACCEPT_STALE is neither 1, true, 0, false nor empty" >&2
      exit 1
      ;;
  esac
  # One line, as the release notes print it.
  STALE_REASON=$(printf '%s' "${STALE_REASON:-}" | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $//')
  if [ "$ACCEPT_STALE" = 1 ] && [ -z "$STALE_REASON" ]; then
    echo "publish: ERROR ACCEPT_STALE needs STALE_REASON, the reason the release notes give" >&2
    exit 1
  fi
}

# export_gh runs gh with the export token, which no other command sees.
export_gh() {
  GH_TOKEN="$ANIMAP_EXPORT_TOKEN" gh "$@"
}

# export_tag prints the export release to read: EXPORT_TAG, or the export
# repository's latest release, either an export- tag.
export_tag() {
  if [ -n "${EXPORT_TAG:-}" ]; then
    et_tag=$EXPORT_TAG
  else
    et_tag=$(export_gh api "repos/${EXPORT_REPO}/releases/latest" --jq .tag_name)
  fi
  if ! printf '%s' "$et_tag" | grep -Eqx 'export-[A-Za-z0-9._-]+' || [ "$(printf '%s' "$et_tag" | wc -l)" -ne 0 ]; then
    echo "publish: ERROR the export release ${et_tag} is not an export- tag" >&2
    exit 1
  fi
  printf '%s\n' "$et_tag"
}

# check_sum stops the run unless $1/animap.json.sha256 is exactly the
# sha256sum line of $1/animap.json.
check_sum() {
  cs_want="$(sha256sum "$1/animap.json" | cut -d' ' -f1)  animap.json"
  if [ "$(cat "$1/animap.json.sha256")" != "$cs_want" ]; then
    echo "publish: ERROR $1/animap.json does not match its animap.json.sha256" >&2
    exit 1
  fi
}

download() {
  : "${ANIMAP_EXPORT_TOKEN:?set ANIMAP_EXPORT_TOKEN to a token that reads ${EXPORT_REPO} releases}"
  dl_tag=$(export_tag)
  mkdir -p "$1"
  export_gh release download "$dl_tag" --repo "$EXPORT_REPO" --pattern animap.json --pattern animap.json.sha256 --dir "$1" --clobber
  check_sum "$1"
  printf '%s\n' "$dl_tag" >"$1/tag"
  echo "publish: downloaded ${EXPORT_REPO} ${dl_tag}"
}

# fetch_previous writes the latest release's animap.json to $1, which the
# coverage guard and the content hash compare against. A 404 means there is
# none; any other failure stops the run, because verifying without it would
# skip the shrink guard.
fetch_previous() {
  fp_code=$(curl --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 120 --retry 3 -sSL \
    -o "$1" -w '%{http_code}' "$LATEST_URL" || true)
  case "$fp_code" in
    200) ;;
    404) rm -f "$1" ;;
    *)
      echo "publish: ERROR reading the latest release answered HTTP ${fp_code}" >&2
      exit 1
      ;;
  esac
}

# Print the jq filter $2 over GitHub API resource $1, or nothing when the
# resource does not exist. gh prints an error response's body on stdout, so
# the status is read from its stderr; any failure but a 404 aborts the run.
gh_get() {
  gg_rc=0
  gg_err=$(mktemp)
  gg_out=$(gh api "$1" --jq "$2" 2>"$gg_err") || gg_rc=$?
  if [ "$gg_rc" -eq 0 ]; then
    rm -f "$gg_err"
    printf '%s\n' "$gg_out"
    return 0
  fi
  if grep -Eq 'HTTP 404([^0-9]|$)' "$gg_err"; then
    rm -f "$gg_err"
    return 0
  fi
  cat "$gg_err" >&2
  rm -f "$gg_err"
  echo "publish: ERROR reading ${1} from the GitHub API failed (gh exit ${gg_rc})" >&2
  exit 1
}
tag_at() { gh_get "repos/${REPO}/git/ref/tags/$1" .object.sha; }

# pick_tag sets TAG to today's tag, or today's with the time when today's is
# released or at another commit than SHA, and at to the commit an existing
# TAG points at. Each lookup is its own assignment so a failed one stops the
# run: inside a test such as [ -n "$(...)" ] its status is dropped and it
# reads as absent.
pick_tag() {
  TAG="v$(date -u +%Y.%m.%d)"
  at=$(tag_at "$TAG")
  if [ -n "$at" ]; then
    released=$(gh_get "repos/${REPO}/releases/tags/${TAG}" .id)
    if [ "$at" != "$SHA" ] || [ -n "$released" ]; then
      TAG="${TAG}.$(date -u +%H%M)"
      at=$(tag_at "$TAG")
    fi
  fi
}

# check_tag stops the run unless $1 is a tag mapread's release reader takes
# for a data release (tagRE in mapread/fetch.go): readers refuse any other
# latest release.
check_tag() {
  case "$1" in
    *[!v0-9.]*) ct_ok=0 ;;
    *) ct_ok=1 ;;
  esac
  if [ "$ct_ok" -eq 0 ] || ! printf '%s' "$1" | grep -Eqx 'v[0-9]{4}\.[0-9]{2}\.[0-9]{2}(\.[0-9]{4})?'; then
    echo "publish: ERROR ${1} is not a vYYYY.MM.DD or vYYYY.MM.DD.HHMM tag, which readers of the latest release refuse" >&2
    exit 1
  fi
}

# write_notes prints the release notes from verify's verdict in $1.
write_notes() {
  jq -r '
    "content_hash: \(.content_hash)",
    "revision: \(.revision)",
    "titles_dump: \(.titles_dump)",
    "",
    "AniList entries: \(.census.entries). AniList entries with AniDB parts: \(.census.entries_with_parts). Anime on TVDB: \(.census.rows_on_tvdb). AniDB episodes on TVDB: \(.census.episodes_on_tvdb). AniDB episodes with a TMDB movie: \(.census.episodes_with_movies). AniDB episode ids: \(.census.anidb_episode_ids).",
    ""' "$1"
  if [ "$ACCEPT_STALE" = 1 ]; then
    printf 'Published with the freshness checks overridden: %s\n\n' "$STALE_REASON"
  fi
  if [ "$(jq -r .previous "$1")" = earlier-shape ]; then
    printf 'This release changes the file to a new shape of format version 2, described in [docs/schema.md](https://github.com/%s/blob/main/docs/schema.md). Entries without an AniList id were removed by the change.\n\n' "$REPO"
  fi
  jq -r '.notice, ""' "$1"
  cat <<EOF
Verify the download:

\`\`\`sh
sha256sum -c animap.json.sha256
cosign verify-blob animap.json --bundle animap.json.sigstore.json \\
  --certificate-identity https://github.com/${REPO}/.github/workflows/publish.yaml@refs/heads/main \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
\`\`\`
EOF
}

# Poll $LATEST_URL until its redirect names tag $1. GitHub's latest-download
# redirect is cached and lags the release by a minute or more
# (https://github.com/cplieger/animap/actions/runs/38617059716), so the delay
# grows to 60 s and the poll gives up once 600 s have elapsed, curl time
# included, or after 30 polls. A failed curl is retried, never read as
# another tag.
await_latest() {
  al_delay=5
  al_polls=0
  al_start=$(date +%s)
  while :; do
    al_rc=0
    al_location=$(curl -sI -o /dev/null -w '%{redirect_url}' --connect-timeout 10 --max-time 20 "$LATEST_URL") || al_rc=$?
    al_polls=$((al_polls + 1))
    al_elapsed=$(($(date +%s) - al_start))
    if [ "$al_rc" -ne 0 ]; then
      al_location="nothing, curl exited ${al_rc}"
    else
      case "$al_location" in
        *"/$1/"*) return 0 ;;
      esac
    fi
    if [ "$al_elapsed" -ge 600 ] || [ "$al_polls" -ge 30 ]; then
      echo "publish: ERROR released $1 but after ${al_polls} polls over ${al_elapsed}s the latest download URL resolves to: ${al_location:-no redirect}" >&2
      return 1
    fi
    sleep "$al_delay"
    al_delay=$((al_delay * 2 > 60 ? 60 : al_delay * 2))
  done
}

release() {
  rl_dir=$1
  check_sum "$rl_dir"
  fetch_previous "$WORK/previous.json"
  set -- -in "$rl_dir/animap.json" -previous "$WORK/previous.json"
  if [ "${ACCEPT_SHRINK:-false}" = true ]; then
    set -- "$@" -accept-shrink
  fi
  if [ "$ACCEPT_STALE" = 1 ]; then
    set -- "$@" -accept-stale
  fi
  (cd "$ROOT" && go run ./cmd/animap verify "$@") >"$WORK/stats.json"
  HASH=$(jq -r .content_hash "$WORK/stats.json")
  ENTRIES=$(jq -r .census.entries "$WORK/stats.json")
  echo "publish: verified ${ENTRIES} AniList entries, content hash ${HASH}, previous release $(jq -r .previous "$WORK/stats.json")"
  if [ "$DRY_RUN" = 1 ]; then
    cp "$rl_dir/animap.json" "$ROOT/animap.json"
  fi
  if [ "$(jq -r .changed "$WORK/stats.json")" != true ]; then
    echo "publish: content hash ${HASH} equals the latest release's, nothing to publish"
    exit 0
  fi

  SHA="${GITHUB_SHA:-}"
  if [ -z "$SHA" ] && [ "$DRY_RUN" = 1 ]; then
    SHA=$(git -C "$ROOT" rev-parse HEAD)
  fi
  : "${SHA:?set GITHUB_SHA to the commit to tag}"
  pick_tag
  check_tag "$TAG"
  write_notes "$WORK/stats.json" >"$WORK/notes.md"
  if [ "$DRY_RUN" = 1 ]; then
    echo "publish: DRY RUN, would release ${TAG} at ${SHA}; artifact at ./animap.json; the notes:"
    cat "$WORK/notes.md"
    exit 0
  fi

  mkdir "$WORK/release"
  cp "$rl_dir/animap.json" "$WORK/release/animap.json"
  cd "$WORK/release"
  sha256sum animap.json >animap.json.sha256
  cosign sign-blob --yes animap.json --bundle animap.json.sigstore.json

  # The tag is created first, at the commit this run verified with: a tag
  # made by gh release create lands on main's head at release time, and the
  # token cannot create one at an older commit once main moved past changes
  # under .github/workflows/. A tag already at this commit is a rerun and is
  # reused.
  if [ -z "$at" ]; then
    if ! gh api "repos/${REPO}/git/refs" -f ref="refs/tags/${TAG}" -f sha="$SHA" >/dev/null 2>"$WORK/tagerr"; then
      cat "$WORK/tagerr" >&2
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
    --repo "$REPO" --title "$TAG" --latest --verify-tag --notes-file "$WORK/notes.md"

  await_latest "$TAG"
  echo "publish: released ${TAG} (${ENTRIES} AniList entries); latest pointer verified"
}

usage() {
  echo "usage: publish.sh [download DIR | release DIR]" >&2
  exit 2
}

main() {
  WORK=$(mktemp -d)
  trap 'rm -rf "$WORK"' EXIT
  case "${1:-}" in
    download)
      [ "$#" -eq 2 ] || usage
      download "$2"
      ;;
    release)
      [ "$#" -eq 2 ] || usage
      read_options
      release "$2"
      ;;
    '')
      [ "$#" -eq 0 ] || usage
      read_options
      download "$WORK/export"
      unset ANIMAP_EXPORT_TOKEN
      release "$WORK/export"
      ;;
    *) usage ;;
  esac
}

main "$@"
