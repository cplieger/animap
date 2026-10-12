#!/usr/bin/env bash
# scripts/drift.sh's date check: it reads the mirror pin publish.yaml holds,
# and any failure leaves it out of the issue plan without stopping the run.
# DRIFT_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DRIFT_SH:-$REPO_ROOT/scripts/drift.sh}"
BLOCK=$(extract_range '^MIRROR_COMMIT=\$(sed' '^fi$' "$WORK/datecheck.sh") || exit 1

mkdir -p "$WORK/repo/.github/workflows" "$WORK/repo/scripts" "$WORK/bin"
cp "$REPO_ROOT/.github/workflows/publish.yaml" "$WORK/repo/.github/workflows/publish.yaml"
PIN=$(sed -n 's/^  MIRROR_COMMIT: //p' "$REPO_ROOT/.github/workflows/publish.yaml")
[[ "$PIN" =~ ^[0-9a-f]{40}$ ]] \
  && ok "drift: publish.yaml carries one 40-hex MIRROR_COMMIT pin" \
  || no "drift: publish.yaml's MIRROR_COMMIT pin" "pin=$PIN"

# fetch-mirror.sh and go log their argv and fail on demand; go refuses any
# command but the date check.
cat >"$WORK/repo/scripts/fetch-mirror.sh" <<'EOF'
printf '%s\n' "$MIRROR_COMMIT" >"$WORK/fetch.log"
[ "${MIRROR_FAILS:-0}" = 1 ] && exit 1
printf '%s/mirror/%s.json\n' "$CACHE_DIR" "$MIRROR_COMMIT"
EOF
cat >"$WORK/bin/go" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$WORK/go.log"
[ "$1 $2 $3" = "run ./cmd/animap datecheck" ] || exit 99
[ "${CHECK_FAILS:-0}" = 1 ] && exit 1
exit 0
EOF
chmod +x "$WORK/bin/go"

check() {
  _rc=0
  rm -f "$WORK/fetch.log" "$WORK/go.log"
  _out=$(cd "$WORK/repo" && env PATH="$WORK/bin:$PATH" WORK="$WORK" CACHE_DIR=/c "$@" \
    bash -c 'set -euo pipefail; args=(-existing x); list=/l.xml; . "$1"; printf "%s\n" "${args[*]}"' _ "$BLOCK" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

check
[ "$_rc" -eq 0 ] && [ "$_out" = "-existing x -datecheck $WORK/datecheck.json" ] \
  && [ "$(cat "$WORK/fetch.log")" = "$PIN" ] \
  && [ "$(cat "$WORK/go.log")" = "run ./cmd/animap datecheck -list /l.xml -overlay overlay -mirror /c/mirror/${PIN}.json -layouts /c/layouts.json -out $WORK/datecheck.json" ] \
  && ok "drift: the date check reads the pinned mirror and joins the issue plan" \
  || no "drift: a date check that runs" "rc=$_rc out=$_out err=$_err fetch=$(cat "$WORK/fetch.log" 2>/dev/null) go=$(cat "$WORK/go.log" 2>/dev/null)"

check MIRROR_FAILS=1
[ "$_rc" -eq 0 ] && [ "$_out" = "-existing x" ] && [ ! -f "$WORK/go.log" ] \
  && [[ "$_err" == *"drift: WARNING the date check did not run"* ]] \
  && ok "drift: a mirror that cannot be read skips the date check and the run goes on" \
  || no "drift: a failed mirror read" "rc=$_rc out=$_out err=$_err"

check CHECK_FAILS=1
[ "$_rc" -eq 0 ] && [ "$_out" = "-existing x" ] \
  && [[ "$_err" == *"drift: WARNING the date check did not run"* ]] \
  && ok "drift: a failed date check stays out of the issue plan and the run goes on" \
  || no "drift: a failed date check" "rc=$_rc out=$_out err=$_err"

report
