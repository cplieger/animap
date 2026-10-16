#!/usr/bin/env bash
# scripts/daily-review.sh's inputs: the Anime-Lists file at the resolved commit,
# and the anime-offline-database release the latest animap release names,
# none with no release. DAILY_REVIEW_SH points the suite at another copy for a
# red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DAILY_REVIEW_SH:-$REPO_ROOT/scripts/daily-review.sh}"
INPUTS=$(extract_range '^aod_version="" aod_sha=""$' '^done <<<"\$out"$' "$WORK/inputs.sh") || exit 1

mkdir -p "$WORK/repo/scripts"
cat >"$WORK/repo/scripts/fetch-inputs.sh" <<'EOF'
[ -n "${AOD_VERSION:-}" ] && printf 'AOD_PATH=/cache/aod/%s-%s.jsonl\n' "$AOD_VERSION" "${AOD_SHA256:?}"
printf 'LIST_PATH=/cache/list/%s.xml\n' "$LIST_COMMIT"
EOF

SHA=$(printf 'a%.0s' $(seq 64))

# inputs <release.json body or "">: run the shipped block in a child with
# daily-review.sh's options, a release present when the body is given.
inputs() {
  local body=$1
  shift
  _rc=0
  printf '%s' "$body" >"$WORK/release.json"
  _out=$(cd "$WORK/repo" && env "$@" WORK="$WORK" BODY="$body" LIST_COMMIT=abc LIST_BLOB=def bash -c '
    set -euo pipefail
    release=()
    [ -n "$BODY" ] && release=(-release "$WORK/release.json")
    . "$1"
    printf "list=%s aod=%s\n" "$list" "$aod"' _ "$INPUTS" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

release() {
  printf '{"sources":{"anime_offline_database":{"release":"%s","sha256":"%s"}}}' "$1" "$2"
}

inputs "" -u AOD_VERSION
[ "$_rc" -eq 0 ] && [ "$_out" = "list=/cache/list/abc.xml aod=" ] \
  && ok "daily-review: with no release, fetches the Anime-Lists file and no database" \
  || no "daily-review: no release" "rc=$_rc out=$_out err=$_err"

inputs "" AOD_VERSION=2026-40 AOD_SHA256="$SHA"
[ "$_rc" -eq 0 ] && [ "$_out" = "list=/cache/list/abc.xml aod=" ] \
  && ok "daily-review: fetches no database from AOD_VERSION in the environment" \
  || no "daily-review: AOD_VERSION in the environment" "rc=$_rc out=$_out err=$_err"

inputs "$(release 2026-42 "$SHA")"
[ "$_rc" -eq 0 ] && [ "$_out" = "list=/cache/list/abc.xml aod=/cache/aod/2026-42-${SHA}.jsonl" ] \
  && ok "daily-review: fetches the database the latest release was built from" \
  || no "daily-review: the release's database" "rc=$_rc out=$_out err=$_err"

for bad in "2026-4 $SHA" "latest $SHA" "2026-42 ${SHA:1}" "2026-42 ${SHA^^}"; do
  read -r tag sha <<<"$bad"
  inputs "$(release "$tag" "$sha")"
  [ "$_rc" -eq 1 ] && [ -z "$_out" ] && [[ "$_err" == *"daily-review: ERROR the latest release names anime-offline-database '${tag}' with sha256 '${sha}'"* ]] \
    && ok "daily-review: a release naming database '${tag}' with sha256 '${sha:0:8}…' stops the run" \
    || no "daily-review: bad database fields ${tag}" "rc=$_rc out=$_out err=$_err"
done

inputs '{"sources":{}}'
[ "$_rc" -eq 1 ] && [[ "$_err" == *"names anime-offline-database 'null' with sha256 'null'"* ]] \
  && ok "daily-review: a release with no database source stops the run" \
  || no "daily-review: no database source" "rc=$_rc out=$_out err=$_err"

report
