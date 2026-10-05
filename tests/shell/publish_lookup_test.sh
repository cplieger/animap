#!/usr/bin/env bash
# scripts/publish.sh's GitHub lookups: a 404 is "absent", any other failure
# stops the run. gh prints an error response's body on stdout, so the stub
# does too. PUBLISH_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
GH_GET=$(extract_function gh_get "$WORK/gh_get.sh") || exit 1
TAG_AT=$(extract_function tag_at "$WORK/tag_at.sh") || exit 1
PICK=$(extract_range '^TAG="v' '^fi$' "$WORK/pick.sh") || exit 1

REPO=cplieger/animap
SHA=1111111111111111111111111111111111111111
OTHER=2222222222222222222222222222222222222222
TAGS="repos/$REPO/git/ref/tags"
STUB_TABLE="$WORK/table"
STUB_LOG="$WORK/gh.log"
export REPO SHA STUB_TABLE STUB_LOG

# gh answers from $STUB_TABLE, one "path<TAB>filter<TAB>answer" line per
# resource, and refuses any call it has no line for. A 404 writes the error
# body to stdout and exits 1, as gh 2.96 does.
mkdir -p "$WORK/bin"
cat >"$WORK/bin/gh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_LOG"
if [ "$#" -ne 4 ] || [ "$1" != api ] || [ "$3" != --jq ]; then
  echo "stub gh: unexpected argv: $*" >&2
  exit 99
fi
answer=$(awk -F'\t' -v p="$2" -v f="$4" '$1 == p && $2 == f { print $3 }' "$STUB_TABLE")
case "$answer" in
  value:*) printf '%s\n' "${answer#value:}" ;;
  404)
    printf '{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}\n'
    echo 'gh: Not Found (HTTP 404)' >&2
    exit 1
    ;;
  bare404)
    printf '{"status":"404"}\n'
    echo 'gh: HTTP 404' >&2
    exit 1
    ;;
  500)
    printf '{"message":"Server Error","status":"500"}\n'
    echo 'gh: Server Error (HTTP 500)' >&2
    exit 1
    ;;
  401)
    printf '{"message":"Bad credentials","status":"401"}\n'
    echo 'gh: Bad credentials (HTTP 401)' >&2
    exit 1
    ;;
  *)
    echo "stub gh: no answer for $2 $4" >&2
    exit 98
    ;;
esac
EOF
cat >"$WORK/bin/date" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  '-u +%Y.%m.%d') echo 2026.10.05 ;;
  '-u +%H%M') echo 0959 ;;
  *)
    echo "stub date: unexpected argv: $*" >&2
    exit 99
    ;;
esac
EOF
chmod +x "$WORK/bin/gh" "$WORK/bin/date"
export PATH="$WORK/bin:$PATH"

# table <line>... : the stub's answers for the next call.
table() {
  printf '%s\n' "$@" >"$STUB_TABLE"
  : >"$STUB_LOG"
}

# lookup <tag>: run the shipped tag_at in a child with publish.sh's options,
# capturing stdout in _out, stderr in _err and the status in _rc.
lookup() {
  _rc=0
  _out=$(bash -c 'set -euo pipefail; . "$1"; . "$2"; tag_at "$3"' _ \
    "$GH_GET" "$TAG_AT" "$1" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

# pick: run the shipped tag-selection block the same way and print the
# TAG and at it settled on.
pick() {
  _rc=0
  _out=$(bash -c 'set -euo pipefail; . "$1"; . "$2"; . "$3"; printf "TAG=%s at=%s\n" "$TAG" "$at"' _ \
    "$GH_GET" "$TAG_AT" "$PICK" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

# --- 0. the 404 bait exits 1 with the error body on stdout --------------------
table "$TAGS/v2099.01.01	.object.sha	404"
bait=$(gh api "$TAGS/v2099.01.01" --jq .object.sha 2>/dev/null)
bait_rc=$?
if [ "$bait_rc" -ne 1 ] || [ -z "$bait" ]; then
  no "precondition: the 404 stub exits 1 with a body on stdout" "rc=$bait_rc stdout=$bait"
  report
  exit 1
fi
ok "precondition: the 404 stub exits 1 with the error body on stdout"

# --- 1. a missing tag is empty, and the call is the exact ref lookup ----------
table "$TAGS/v2099.01.01	.object.sha	404"
lookup v2099.01.01
[ "$_rc" -eq 0 ] && [ -z "$_out" ] && [ "$(cat "$STUB_LOG")" = "api $TAGS/v2099.01.01 --jq .object.sha" ] \
  && ok "tag_at on a 404 prints nothing and succeeds" \
  || no "tag_at on a 404" "rc=$_rc out=$_out log=$(cat "$STUB_LOG") err=$_err"

table "$TAGS/v2099.01.01	.object.sha	bare404"
lookup v2099.01.01
[ "$_rc" -eq 0 ] && [ -z "$_out" ] \
  && ok "tag_at on a 404 with no message prints nothing and succeeds" \
  || no "tag_at on a bare 404" "rc=$_rc out=$_out err=$_err"

# --- 2. an existing tag prints its commit --------------------------------------
table "$TAGS/v2026.10.05	.object.sha	value:$SHA"
lookup v2026.10.05
[ "$_rc" -eq 0 ] && [ "$_out" = "$SHA" ] \
  && ok "tag_at on an existing tag prints the commit it points at" \
  || no "tag_at on an existing tag" "rc=$_rc out=$_out err=$_err"

# --- 3. any other failure is loud ----------------------------------------------
for code in 500 401; do
  table "$TAGS/v2026.10.05	.object.sha	$code"
  lookup v2026.10.05
  [ "$_rc" -eq 1 ] && [ -z "$_out" ] \
    && [[ "$_err" == *"(HTTP $code)"* ]] \
    && [[ "$_err" == *"publish: ERROR reading $TAGS/v2026.10.05 from the GitHub API failed"* ]] \
    && ok "tag_at on HTTP $code exits 1, prints nothing, and names the lookup and gh's error" \
    || no "tag_at on HTTP $code" "rc=$_rc out=$_out err=$_err"
done

# --- 4. the tag the release run settles on --------------------------------------
table "$TAGS/v2026.10.05	.object.sha	404"
pick
[ "$_rc" -eq 0 ] && [ "$_out" = "TAG=v2026.10.05 at=" ] \
  && ok "no tag today: the run takes today's tag and creates it" \
  || no "no tag today" "rc=$_rc out=$_out err=$_err"

table "$TAGS/v2026.10.05	.object.sha	value:$SHA" \
  "repos/$REPO/releases/tags/v2026.10.05	.id	404"
pick
[ "$_rc" -eq 0 ] && [ "$_out" = "TAG=v2026.10.05 at=$SHA" ] \
  && ok "today's tag at this commit with no release: a rerun reuses it" \
  || no "rerun reuses today's tag" "rc=$_rc out=$_out err=$_err"

table "$TAGS/v2026.10.05	.object.sha	value:$SHA" \
  "repos/$REPO/releases/tags/v2026.10.05	.id	value:42" \
  "$TAGS/v2026.10.05.0959	.object.sha	404"
pick
[ "$_rc" -eq 0 ] && [ "$_out" = "TAG=v2026.10.05.0959 at=" ] \
  && ok "today's tag already released: the run takes the HHMM tag" \
  || no "released today" "rc=$_rc out=$_out err=$_err"

table "$TAGS/v2026.10.05	.object.sha	value:$OTHER" \
  "repos/$REPO/releases/tags/v2026.10.05	.id	404" \
  "$TAGS/v2026.10.05.0959	.object.sha	404"
pick
[ "$_rc" -eq 0 ] && [ "$_out" = "TAG=v2026.10.05.0959 at=" ] \
  && ok "today's tag at another commit: the run takes the HHMM tag" \
  || no "tag at another commit" "rc=$_rc out=$_out err=$_err"

# Each failing lookup must stop the block, never read as absent.
table "$TAGS/v2026.10.05	.object.sha	500"
pick
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [[ "$_err" == *"publish: ERROR"* ]] \
  && ok "a failed tag lookup stops the run" \
  || no "failed tag lookup" "rc=$_rc out=$_out err=$_err"

table "$TAGS/v2026.10.05	.object.sha	value:$SHA" \
  "repos/$REPO/releases/tags/v2026.10.05	.id	500"
pick
[ "$_rc" -ne 0 ] && [ -z "$_out" ] && [[ "$_err" == *"publish: ERROR reading repos/$REPO/releases/tags/v2026.10.05"* ]] \
  && ok "a failed release lookup stops the run" \
  || no "failed release lookup" "rc=$_rc out=$_out err=$_err"

report
