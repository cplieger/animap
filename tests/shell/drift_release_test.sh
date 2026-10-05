#!/usr/bin/env bash
# scripts/drift.sh's read of the latest release: 200 is a release, 404 is
# none yet, and anything else stops the run rather than checking without it.
# DRIFT_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DRIFT_SH:-$REPO_ROOT/scripts/drift.sh}"
READ=$(extract_range '^code=\$(curl' '^esac$' "$WORK/read.sh") || exit 1

REPO=cplieger/animap
STUB_CODE="$WORK/code"
STUB_LOG="$WORK/curl.log"
export REPO STUB_CODE STUB_LOG

# curl prints the status from $STUB_CODE as -w would, and refuses any URL
# but the latest release's animap.json.
mkdir -p "$WORK/bin"
cat >"$WORK/bin/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_LOG"
url="${*: -1}"
if [ "$url" != "https://github.com/${REPO}/releases/latest/download/animap.json" ]; then
  echo "stub curl: unexpected URL: $url" >&2
  exit 99
fi
code=$(cat "$STUB_CODE")
printf '%s' "$code"
[ "$code" = 000 ] && exit 7
exit 0
EOF
chmod +x "$WORK/bin/curl"
export PATH="$WORK/bin:$PATH"

# read_release <code>: run the shipped block in a child with drift.sh's
# options and print the -release arguments it settled on.
read_release() {
  printf '%s' "$1" >"$STUB_CODE"
  : >"$STUB_LOG"
  _rc=0
  _out=$(bash -c 'set -euo pipefail; WORK="$1"; . "$2"; printf "release=%s\n" "${release[*]}"' _ \
    "$WORK" "$READ" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

read_release 200
[ "$_rc" -eq 0 ] && [ "$_out" = "release=-release $WORK/release.json" ] && [ -s "$STUB_LOG" ] \
  && ok "HTTP 200: the latest release is passed on" \
  || no "HTTP 200" "rc=$_rc out=$_out err=$_err log=$(cat "$STUB_LOG")"

read_release 404
[ "$_rc" -eq 0 ] && [ "$_out" = "release=" ] \
  && ok "HTTP 404: no release yet, and the run carries on without one" \
  || no "HTTP 404" "rc=$_rc out=$_out err=$_err"

for code in 503 000; do
  read_release "$code"
  [ "$_rc" -eq 1 ] && [ -z "$_out" ] && [[ "$_err" == *"drift: ERROR reading the latest release answered HTTP $code"* ]] \
    && ok "HTTP $code: the run stops and names the status" \
    || no "HTTP $code" "rc=$_rc out=$_out err=$_err"
done

report
