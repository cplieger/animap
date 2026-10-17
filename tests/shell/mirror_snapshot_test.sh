#!/usr/bin/env bash
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${FETCH_MIRROR_SH:-$REPO_ROOT/scripts/fetch-mirror.sh}"
KEY_FN=$(extract_function mirror_key "$WORK/mirror_key.sh") || exit 1
FN=$(extract_function mirror_snapshot "$WORK/mirror_snapshot.sh") || exit 1
WORKFLOWS="${MIRROR_WORKFLOWS:-$REPO_ROOT/.github/workflows/publish.yaml $REPO_ROOT/.github/workflows/daily-review.yaml}"

COMMIT=7dd9b8eafad24eaeaf84fe7a13c988dbb03e9983
mkdir -p "$WORK/bin" "$WORK/root/internal/anidb" "$WORK/root/internal/strictjson" "$WORK/root/cmd/animap" "$WORK/root/scripts"
printf 'package anidb\n' >"$WORK/root/internal/anidb/snapshot.go"
printf 'package strictjson\n' >"$WORK/root/internal/strictjson/strictjson.go"
printf 'package main\n' >"$WORK/root/cmd/animap/mirror.go"
printf 'module example\n' >"$WORK/root/go.mod"
printf 'example v1 h1:\n' >"$WORK/root/go.sum"
cp "$ENTRYPOINT" "$WORK/root/scripts/fetch-mirror.sh"
cat >"$WORK/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$WORK/curl.log"
[ "${CURL_FAILS:-0}" = 1 ] && exit 22
printf 'archive-bytes'
STUB
cat >"$WORK/bin/go" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$WORK/go.log"
body=$(cat)
[ "${EXTRACT_FAILS:-0}" = 1 ] || [ "$body" != archive-bytes ] && exit 1
out=""
while [ "$#" -gt 0 ]; do
  [ "$1" = "-out" ] && out=$2
  shift
done
printf 'snapshot of %s\n' "$body" >"$out"
STUB
chmod +x "$WORK/bin/curl" "$WORK/bin/go"

snapshot() {
  _rc=0
  rm -f "$WORK/curl.log" "$WORK/go.log"
  _out=$(env PATH="$WORK/bin:$PATH" WORK="$WORK" ROOT="$WORK/root" CACHE_DIR="$WORK/cache" MIRROR_COMMIT="$COMMIT" "$@" \
    bash -c 'set -euo pipefail; . "$1"; . "$2"; MIRROR_PATH=$(mirror_snapshot); printf "%s\n" "$MIRROR_PATH"' _ "$KEY_FN" "$FN" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

kept() { find "$WORK/cache/mirror" -type f 2>/dev/null | wc -l; }
shape="^$WORK/cache/mirror/${COMMIT}-[0-9a-f]{40}\.json$"

snapshot EXTRACT_FAILS=1
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ "$(kept)" -eq 0 ] \
  && ok "fetch-mirror: a failed extract stops the run and keeps no snapshot" \
  || no "fetch-mirror: a failed extract" "rc=$_rc out=$_out"

snapshot CURL_FAILS=1
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ "$(kept)" -eq 0 ] \
  && ok "fetch-mirror: a failed archive download stops the run" \
  || no "fetch-mirror: a failed download" "rc=$_rc out=$_out"

snapshot
first=$_out
[ "$_rc" -eq 0 ] && [[ "$first" =~ $shape ]] && [ "$(cat "$first")" = "snapshot of archive-bytes" ] \
  && grep -q "https://codeload.github.com/notseteve/AnimeAggregations/tar.gz/${COMMIT}" "$WORK/curl.log" \
  && grep -q "^run ./cmd/animap mirror extract -commit ${COMMIT} -out ${first}$" "$WORK/go.log" \
  && ok "fetch-mirror: the pinned commit's archive is piped into mirror extract" \
  || no "fetch-mirror: the first snapshot" "rc=$_rc out=$_out err=$_err curl=$(cat "$WORK/curl.log" 2>/dev/null) go=$(cat "$WORK/go.log" 2>/dev/null)"
[ -n "$first" ] || exit 1

snapshot
[ "$_rc" -eq 0 ] && [ "$_out" = "$first" ] && [ ! -f "$WORK/curl.log" ] && [ ! -f "$WORK/go.log" ] \
  && ok "fetch-mirror: a kept snapshot for the commit is reused without a download" \
  || no "fetch-mirror: a kept snapshot" "rc=$_rc out=$_out"

_key=$(env -u CACHE_DIR MIRROR_COMMIT="$COMMIT" bash "$WORK/root/scripts/fetch-mirror.sh" key 2>"$WORK/err") || _key="failed: $(cat "$WORK/err")"
[ "$WORK/cache/mirror/${_key}.json" = "$first" ] \
  && ok "fetch-mirror: key prints the kept snapshot's name and needs no CACHE_DIR" \
  || no "fetch-mirror: key" "key=$_key first=$first"

_rc=0
env -u CACHE_DIR MIRROR_COMMIT="$COMMIT" bash "$WORK/root/scripts/fetch-mirror.sh" keys >"$WORK/out" 2>"$WORK/err" || _rc=$?
[ "$_rc" -eq 2 ] && [ ! -s "$WORK/out" ] && grep -q "unknown argument 'keys'" "$WORK/err" \
  && ok "fetch-mirror: an unknown argument is refused by name" \
  || no "fetch-mirror: an unknown argument" "rc=$_rc err=$(cat "$WORK/err")"

printf 'package anidb\n' >"$WORK/root/internal/anidb/snapshot_test.go"
snapshot
[ "$_rc" -eq 0 ] && [ "$_out" = "$first" ] && [ ! -f "$WORK/go.log" ] \
  && ok "fetch-mirror: a test file changes nothing extracted" \
  || no "fetch-mirror: a test file" "rc=$_rc out=$_out first=$first"

prev=$first
for input in internal/anidb/snapshot.go internal/strictjson/strictjson.go cmd/animap/mirror.go go.mod go.sum; do
  printf '// changed\n' >>"$WORK/root/$input"
  snapshot
  [ "$_rc" -eq 0 ] && [[ "$_out" =~ $shape ]] && [ "$_out" != "$prev" ] && [ -f "$WORK/go.log" ] \
    && [ "$(cat "$_out")" = "snapshot of archive-bytes" ] \
    && ok "fetch-mirror: a change to ${input} extracts the snapshot again" \
    || no "fetch-mirror: a changed ${input}" "rc=$_rc out=$_out prev=$prev go=$(cat "$WORK/go.log" 2>/dev/null)"
  prev=$_out
done

rm -rf "$WORK/root/internal/strictjson"
snapshot
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ ! -f "$WORK/curl.log" ] && [[ "$_err" == *"extractor input internal/strictjson/*.go is missing"* ]] \
  && ok "fetch-mirror: a missing extractor input stops the run before a download" \
  || no "fetch-mirror: a missing extractor input" "rc=$_rc out=$_out err=$_err"

rm -rf "$WORK/root/internal/anidb"
snapshot
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ ! -f "$WORK/curl.log" ] \
  && ok "fetch-mirror: no extractor source stops the run before a download" \
  || no "fetch-mirror: no extractor source" "rc=$_rc out=$_out"

for wf in $WORKFLOWS; do
  name=$(basename "$wf")
  keys=$(awk '/path: \.cache\/mirror$/ { getline; print }' "$wf")
  n=$(printf '%s\n' "$keys" | grep -c 'key:')
  bad=$(printf '%s\n' "$keys" | grep -vcE '^ +key: (drift-)?anidb-\$\{\{ steps\.mirror-key\.outputs\.KEY \}\}$')
  [ "$n" -eq 2 ] && [ "$bad" -eq 0 ] && grep -q 'bash scripts/fetch-mirror.sh key)$' "$wf" \
    && ok "fetch-mirror: ${name} keys its mirror cache by fetch-mirror.sh key" \
    || no "fetch-mirror: ${name}'s mirror cache key" "keys=$keys"
done

report
