#!/usr/bin/env bash
# scripts/daily-review.sh's DRY_RUN reading and its tail: the issue plan
# applies only when DRY_RUN is 0 or unset, and a TMDB token the review saw
# refused then turns the run red, after the plan, never before it.
# DAILY_REVIEW_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DAILY_REVIEW_SH:-$REPO_ROOT/scripts/daily-review.sh}"
PARSE=$(extract_range '^case "\${DRY_RUN' '^esac$' "$WORK/parse.sh") || exit 1
PLAN=$(extract_range '^go run \.\/cmd\/animap issues' '^fi$' "$WORK/plan.sh") || exit 1
REFUSAL=$(extract_range '^if jq -e .*refused' '^fi$' "$WORK/refusal.sh") || exit 1
BLOCK="$WORK/block.sh"
cat "$PARSE" "$PLAN" "$REFUSAL" >"$BLOCK"
grep -q 'DRY_RUN=1' "$PARSE" && grep -q 'apply-issues.sh' "$PLAN" && grep -q 'exit 1' "$REFUSAL" \
  || {
    no "precondition: the DRY_RUN reading, the plan and the refusal check extract" "$(cat "$BLOCK")"
    report
    exit 1
  }

mkdir -p "$WORK/repo/scripts" "$WORK/bin"
cat >"$WORK/bin/go" <<'EOF'
#!/usr/bin/env bash
[ "$1 $2 $3" = "run ./cmd/animap issues" ] || exit 99
printf 'planned\n' >>"$WORK/order.log"
EOF
chmod +x "$WORK/bin/go"
cat >"$WORK/repo/scripts/apply-issues.sh" <<'EOF'
printf 'applied %s\n' "$1" >>"$WORK/order.log"
EOF

# tail_of <refused> <DRY_RUN|unset>: run the shipped DRY_RUN reading and
# tail on a review.json whose TMDB source is refused or not.
tail_of() {
  _rc=0
  rm -f "$WORK/order.log"
  printf '{"sources":{"date-tmdb":{"evaluated":%s,"refused":%s}}}' "$([ "$1" = true ] && echo false || echo true)" "$1" >"$WORK/review.json"
  _env=(PATH="$WORK/bin:$PATH" WORK="$WORK" ROOT="$WORK")
  [ "$2" = unset ] || _env+=(DRY_RUN="$2")
  (cd "$WORK/repo" && env -u DRY_RUN "${_env[@]}" \
    bash -c 'set -euo pipefail; args=(-existing x); . "$1"' _ "$BLOCK") >"$WORK/out" 2>"$WORK/err" || _rc=$?
  _order=$(tr '\n' '|' <"$WORK/order.log" 2>/dev/null)
  _err=$(cat "$WORK/err")
}

tail_of false 0
[ "$_rc" -eq 0 ] && [ "$_order" = "planned|applied $WORK/actions.json|" ] \
  && ok "daily-review: the plan applies and the run passes" \
  || no "daily-review: a run TMDB answered" "rc=$_rc order=$_order err=$_err"

tail_of true 0
[ "$_rc" -eq 1 ] && [ "$_order" = "planned|applied $WORK/actions.json|" ] \
  && [[ "$_err" == *"daily-review: ERROR TMDB refused TMDB_TOKEN"* ]] \
  && ok "daily-review: a refused token fails the run after the plan applies" \
  || no "daily-review: a refused token" "rc=$_rc order=$_order err=$_err"

tail_of true 1
[ "$_rc" -eq 1 ] && [ "$_order" = "planned|" ] \
  && ok "daily-review: a dry run applies nothing and still reports the refused token" \
  || no "daily-review: a dry run with a refused token" "rc=$_rc order=$_order err=$_err"

tail_of false 1
[ "$_rc" -eq 0 ] && [ "$_order" = "planned|" ] \
  && ok "daily-review: a dry run applies nothing" \
  || no "daily-review: a dry run" "rc=$_rc order=$_order err=$_err"

tail_of false unset
[ "$_rc" -eq 0 ] && [ "$_order" = "planned|applied $WORK/actions.json|" ] && [ -z "$_err" ] \
  && ok "daily-review: with DRY_RUN unset the plan applies" \
  || no "daily-review: DRY_RUN unset" "rc=$_rc order=$_order err=$_err"

for bad in true yes 2 01 ' 0' '1 '; do
  tail_of false "$bad"
  [ "$_rc" -eq 0 ] && [ "$_order" = "planned|" ] \
    && [[ "$_err" == *"daily-review: WARNING DRY_RUN is neither 0 nor 1, so this is a dry run"* ]] \
    && ok "daily-review: DRY_RUN='$bad' applies nothing and says why" \
    || no "daily-review: DRY_RUN='$bad'" "rc=$_rc order=$_order err=$_err"
done

report
