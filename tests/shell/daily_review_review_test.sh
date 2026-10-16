#!/usr/bin/env bash
# scripts/daily-review.sh's mirror read and review call: the mirror is the
# pin publish.yaml holds, the review reads it, the release's database and
# the release when each is there, and a failed mirror read leaves the date
# checks out while the review still runs. DAILY_REVIEW_SH points the suite
# at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DAILY_REVIEW_SH:-$REPO_ROOT/scripts/daily-review.sh}"
BLOCK=$(extract_range '^MIRROR_COMMIT=\$(sed' '^args+=(-review ' "$WORK/block.sh") || exit 1
grep -q 'animap review' "$BLOCK" && grep -q '^mirror=\$(MIRROR_COMMIT' "$BLOCK" \
  || {
    no "precondition: the mirror read and the review call extract" "$(cat "$BLOCK")"
    report
    exit 1
  }

mkdir -p "$WORK/repo/.github/workflows" "$WORK/repo/scripts" "$WORK/bin"
cp "$REPO_ROOT/.github/workflows/publish.yaml" "$WORK/repo/.github/workflows/publish.yaml"
PIN=$(sed -n 's/^  MIRROR_COMMIT: //p' "$REPO_ROOT/.github/workflows/publish.yaml")
[[ "$PIN" =~ ^[0-9a-f]{40}$ ]] \
  && ok "daily-review: publish.yaml carries one 40-hex MIRROR_COMMIT pin" \
  || no "daily-review: publish.yaml's MIRROR_COMMIT pin" "pin=$PIN"

# fetch-mirror.sh logs its pin and fails on demand; go logs the review call
# and refuses any other command.
cat >"$WORK/repo/scripts/fetch-mirror.sh" <<'EOF'
printf '%s\n' "$MIRROR_COMMIT" >"$WORK/fetch.log"
[ "${MIRROR_FAILS:-0}" = 1 ] && exit 1
printf '%s/mirror/%s.json\n' "$CACHE_DIR" "$MIRROR_COMMIT"
EOF
cat >"$WORK/bin/go" <<'EOF'
#!/usr/bin/env bash
case "$1 $2 $3" in
  "run ./cmd/animap review") printf '%s\n' "$*" >"$WORK/go.log" ;;
  *) exit 99 ;;
esac
EOF
chmod +x "$WORK/bin/go"

check() {
  _rc=0
  rm -f "$WORK/fetch.log" "$WORK/go.log"
  _out=$(cd "$WORK/repo" && env PATH="$WORK/bin:$PATH" WORK="$WORK" CACHE_DIR=/c ROOT=/r LIST_COMMIT=abc "$@" \
    bash -c 'set -euo pipefail; args=(-existing x); list=/l.xml; aod=${AOD:-}; release=(); [ -n "${REL:-}" ] && release=(-release "$REL"); . "$1"; printf "%s\n" "${args[*]}"' _ "$BLOCK" 2>"$WORK/err") || _rc=$?
  _go=$(cat "$WORK/go.log" 2>/dev/null)
  _err=$(cat "$WORK/err")
}

TAIL="-layouts-tvdb /c/layouts-tvdb.json -layouts-tmdb /c/layouts-tmdb.json -cache /c/http.json -out /r/review.json"

check AOD=/a.jsonl REL=/rel.json
[ "$_rc" -eq 0 ] && [ "$_out" = "-existing x -review /r/review.json" ] && [ "$(cat "$WORK/fetch.log")" = "$PIN" ] \
  && [ "$_go" = "run ./cmd/animap review -list /l.xml -list-commit abc -overlay overlay -aod /a.jsonl -mirror /c/mirror/${PIN}.json -release /rel.json $TAIL" ] \
  && ok "daily-review: the review reads the pinned mirror, the release's database and the release, and joins the issue plan" \
  || no "daily-review: a review with every input" "rc=$_rc out=$_out go=$_go err=$_err"

check
[ "$_rc" -eq 0 ] && [ "$_go" = "run ./cmd/animap review -list /l.xml -list-commit abc -overlay overlay -mirror /c/mirror/${PIN}.json $TAIL" ] \
  && ok "daily-review: with no release, the review reads the mirror alone" \
  || no "daily-review: a review with no release" "rc=$_rc go=$_go err=$_err"

check MIRROR_FAILS=1 AOD=/a.jsonl
[ "$_rc" -eq 0 ] && [ "$_out" = "-existing x -review /r/review.json" ] \
  && [ "$_go" = "run ./cmd/animap review -list /l.xml -list-commit abc -overlay overlay -aod /a.jsonl $TAIL" ] \
  && [[ "$_err" == *"daily-review: WARNING the AniDB mirror could not be read"* ]] \
  && ok "daily-review: a mirror that cannot be read leaves it out of the review, and the run goes on" \
  || no "daily-review: a failed mirror read" "rc=$_rc out=$_out go=$_go err=$_err"

report
