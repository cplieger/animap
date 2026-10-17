#!/usr/bin/env bash
# scripts/publish.sh's DRY_RUN reading and its dry-run exit: the run goes
# on to the release steps only when DRY_RUN is 0, empty or unset, and any
# other value stops at ./animap.json. PUBLISH_SH points the suite at another
# copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
PARSE=$(extract_range '^case "\${DRY_RUN' '^esac$' "$WORK/parse.sh") || exit 1
DRY=$(extract_range '^if \[ "\$DRY_RUN" = "1" \]' '^fi$' "$WORK/dry.sh") || exit 1
BLOCK="$WORK/block.sh"
# The line after the dry-run exit stands for the release steps.
{
  cat "$PARSE" "$DRY"
  echo 'echo "release steps reached"'
} >"$BLOCK"
grep -q '^esac$' "$PARSE" && grep -q 'exit 0' "$DRY" \
  || {
    no "precondition: the DRY_RUN reading and the dry-run exit extract" "$(cat "$BLOCK")"
    report
    exit 1
  }

mkdir -p "$WORK/run" "$WORK/root"
printf '{"version":2}\n' >"$WORK/run/animap.json"
printf '{"changed":true}\n' >"$WORK/run/stats.json"

# run_with <DRY_RUN|unset>: run the shipped reading and dry-run exit with
# publish.sh's options.
run_with() {
  _rc=0
  rm -f "$WORK/root/animap.json"
  _env=(WORK="$WORK/run" ROOT="$WORK/root" HASH=h ENTRIES=1)
  [ "$1" = unset ] || _env+=(DRY_RUN="$1")
  env -u DRY_RUN "${_env[@]}" bash -c 'set -euo pipefail; . "$1"' _ "$BLOCK" >"$WORK/out" 2>"$WORK/err" || _rc=$?
  _out=$(cat "$WORK/out")
  _err=$(cat "$WORK/err")
}

for live in unset 0 ''; do
  run_with "$live"
  [ "$_rc" -eq 0 ] && [ "$_out" = "release steps reached" ] && [ -z "$_err" ] && [ ! -e "$WORK/root/animap.json" ] \
    && ok "publish: with DRY_RUN '$live' the run reaches the release steps" \
    || no "publish: DRY_RUN '$live'" "rc=$_rc out=$_out err=$_err"
done

run_with 1
[ "$_rc" -eq 0 ] && [[ "$_out" == "publish: DRY RUN, 1 AniList entries"* ]] && [[ "$_out" != *"release steps reached"* ]] \
  && cmp -s "$WORK/run/animap.json" "$WORK/root/animap.json" && [ -z "$_err" ] \
  && ok "publish: DRY_RUN=1 writes ./animap.json and stops before the release steps" \
  || no "publish: DRY_RUN=1" "rc=$_rc out=$_out err=$_err"

for bad in true yes 2 01 ' 0' '1 '; do
  run_with "$bad"
  [ "$_rc" -eq 0 ] && [[ "$_out" != *"release steps reached"* ]] && [ -e "$WORK/root/animap.json" ] \
    && [ "$_err" = "publish: WARNING DRY_RUN is neither 0 nor 1, so this is a dry run" ] \
    && ok "publish: DRY_RUN='$bad' stops before the release steps and says why" \
    || no "publish: DRY_RUN='$bad'" "rc=$_rc out=$_out err=$_err"
done

report
