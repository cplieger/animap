#!/usr/bin/env bash
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
FN=$(extract_function mirror_snapshot "$WORK/mirror_snapshot.sh") || exit 1

COMMIT=7dd9b8eafad24eaeaf84fe7a13c988dbb03e9983
mkdir -p "$WORK/bin" "$WORK/root"
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
    bash -c 'set -euo pipefail; . "$1"; MIRROR_PATH=$(mirror_snapshot); printf "%s\n" "$MIRROR_PATH"' _ "$FN" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

want="$WORK/cache/mirror/${COMMIT}.json"

snapshot EXTRACT_FAILS=1
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ ! -f "$want" ] \
  && ok "publish: a failed extract stops the run and keeps no snapshot" \
  || no "publish: a failed extract" "rc=$_rc out=$_out"

snapshot CURL_FAILS=1
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [ ! -f "$want" ] \
  && ok "publish: a failed archive download stops the run" \
  || no "publish: a failed download" "rc=$_rc out=$_out"

snapshot
[ "$_rc" -eq 0 ] && [ "$_out" = "$want" ] && [ "$(cat "$want")" = "snapshot of archive-bytes" ] \
  && grep -q "https://codeload.github.com/notseteve/AnimeAggregations/tar.gz/${COMMIT}" "$WORK/curl.log" \
  && grep -q "^run ./cmd/animap mirror extract -commit ${COMMIT} -out ${want}$" "$WORK/go.log" \
  && ok "publish: the pinned commit's archive is piped into mirror extract" \
  || no "publish: the first snapshot" "rc=$_rc out=$_out err=$_err curl=$(cat "$WORK/curl.log" 2>/dev/null) go=$(cat "$WORK/go.log" 2>/dev/null)"

snapshot
[ "$_rc" -eq 0 ] && [ "$_out" = "$want" ] && [ ! -f "$WORK/curl.log" ] && [ ! -f "$WORK/go.log" ] \
  && ok "publish: a kept snapshot for the commit is reused without a download" \
  || no "publish: a kept snapshot" "rc=$_rc out=$_out"

report
