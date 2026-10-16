#!/usr/bin/env bash
# scripts/daily-review.sh lists every animap issue for the planner, past any page or
# listing cap, so an old issue is never missed and its key opened twice.
# DAILY_REVIEW_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DAILY_REVIEW_SH:-$REPO_ROOT/scripts/daily-review.sh}"
BLOCK=$(extract_range '^gh run list' 'existing.json"$' "$WORK/listing.sh") || exit 1

# 12 pages of 100: 1,150 open issues, newest first, the oldest carrying a lead
# key, and one pull request, which the issues API also returns.
jq -n '[range(1150; 0; -1) | {number: ., state: "open", title: "t\(.)", body: (if . == 1 then "<!-- animap:key=anime:anidb:1 -->" else null end)}]
  + [{number: 2000, state: "open", title: "pr", body: "<!-- animap:key=anime:anidb:9 -->", pull_request: {}}]
  | [range(0; length; 100) as $i | .[$i:$i + 100]]' >"$WORK/pages.json"

# gh answers the paginated API from the pages, and gh issue list the way gh
# does: newest first, at most --limit.
mkdir -p "$WORK/bin"
cat >"$WORK/bin/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
  "run list") echo '[]' ;;
  "api --paginate")
    printf '%s\n' "$*" >"$WORK/gh.log"
    [ "$3" = "--slurp" ] || exit 98
    cat "$WORK/pages.json"
    ;;
  "issue list")
    state=all limit=30
    while [ $# -gt 0 ]; do
      case "$1" in
        --state) state=$2 ;;
        --limit) limit=$2 ;;
      esac
      shift
    done
    jq --arg s "$state" --argjson n "$limit" '[.[][] | select(.pull_request == null) | select($s == "all" or .state == $s)
      | {number, state: (.state | ascii_upcase), title, body: (.body // "")}] | sort_by(-.number) | .[:$n]' "$WORK/pages.json"
    ;;
  *) exit 99 ;;
esac
EOF
chmod +x "$WORK/bin/gh"

_rc=0
(cd "$WORK" && env PATH="$WORK/bin:$PATH" WORK="$WORK" REPO=cplieger/animap \
  bash -c 'set -euo pipefail; args=(); . "$1"' _ "$BLOCK") 2>"$WORK/err" || _rc=$?

[ "$_rc" -eq 0 ] && [ "$(jq length "$WORK/existing.json" 2>/dev/null)" = 1150 ] \
  && [ "$(jq -c '[.[] | select(.number == 1)] | .[0] | [.state, .body]' "$WORK/existing.json")" = '["OPEN","<!-- animap:key=anime:anidb:1 -->"]' ] \
  && ok "daily-review: the oldest of 1,150 open issues reaches the planner with its key" \
  || no "daily-review: a listing past every page" "rc=$_rc err=$(cat "$WORK/err") count=$(jq length "$WORK/existing.json" 2>/dev/null)"

[ "$_rc" -eq 0 ] && [ "$(jq '[.[] | select(.number == 2000)] | length' "$WORK/existing.json" 2>/dev/null)" = 0 ] \
  && [ "$(jq '[.[] | select(.number == 2) | .body] == [""]' "$WORK/existing.json" 2>/dev/null)" = true ] \
  && ok "daily-review: pull requests stay out of the listing and an empty body reads as text" \
  || no "daily-review: the listing's shape" "rc=$_rc err=$(cat "$WORK/err")"

[ "$(cat "$WORK/gh.log" 2>/dev/null)" = "api --paginate --slurp repos/cplieger/animap/issues?labels=animap&state=all&per_page=100" ] \
  && ok "daily-review: the listing asks for every animap issue in either state" \
  || no "daily-review: the listing's request" "gh=$(cat "$WORK/gh.log" 2>/dev/null)"

report
