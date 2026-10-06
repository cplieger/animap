#!/usr/bin/env bash
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
PIN=$(extract_range '^MIRROR_COMMIT="\${MIRROR_COMMIT' '^fi$' "$WORK/pin.sh") || exit 1

GOOD=7dd9b8eafad24eaeaf84fe7a13c988dbb03e9983

pin() {
  _rc=0
  if [ "$#" -eq 0 ]; then
    _out=$(env -u MIRROR_COMMIT bash -c 'set -euo pipefail; . "$1"; printf "ok=%s\n" "$MIRROR_COMMIT"' _ "$PIN" 2>"$WORK/err") || _rc=$?
  else
    _out=$(MIRROR_COMMIT="$1" bash -c 'set -euo pipefail; . "$1"; printf "ok=%s\n" "$MIRROR_COMMIT"' _ "$PIN" 2>"$WORK/err") || _rc=$?
  fi
  _err=$(cat "$WORK/err")
}

pin "$GOOD"
[ "$_rc" -eq 0 ] && [ "$_out" = "ok=$GOOD" ] \
  && ok "publish: a 40-hex MIRROR_COMMIT is accepted" \
  || no "publish: a 40-hex MIRROR_COMMIT" "rc=$_rc out=$_out err=$_err"

for bad in main "../../x" "${GOOD^^}" "${GOOD}0" "${GOOD:1}"; do
  pin "$bad"
  [ "$_rc" -eq 1 ] && [ -z "$_out" ] && [[ "$_err" == *"publish: ERROR MIRROR_COMMIT='${bad}' is not a commit SHA"* ]] \
    && ok "publish: MIRROR_COMMIT='$bad' is refused by name" \
    || no "publish: MIRROR_COMMIT='$bad'" "rc=$_rc out=$_out err=$_err"
done

pin
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [[ "$_err" == *"set MIRROR_COMMIT"* ]] \
  && ok "publish: an unset MIRROR_COMMIT stops the run" \
  || no "publish: an unset MIRROR_COMMIT" "rc=$_rc out=$_out err=$_err"

report
