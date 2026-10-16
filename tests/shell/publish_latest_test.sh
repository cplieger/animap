#!/usr/bin/env bash
# scripts/publish.sh's wait for the latest download URL to serve the new tag:
# GitHub's redirect lags the release, so the poll must outlast it, retry a
# failed curl, and fail loudly with how long it waited, curl time included.
# PUBLISH_SH points the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
load_function await_latest
# The child shells below run it with publish.sh's options.
export -f await_latest

LATEST_URL=https://github.com/cplieger/animap/releases/latest/download/animap.json
STUB_ANSWERS="$WORK/answers"
CURL_LOG="$WORK/curl.log"
SLEEP_LOG="$WORK/sleep.log"
CLOCK="$WORK/clock"
export LATEST_URL STUB_ANSWERS CURL_LOG SLEEP_LOG CLOCK

# date +%s prints the fake clock in $CLOCK; any other argv is refused.
mkdir -p "$WORK/bin"
cat >"$WORK/bin/date" <<'EOF'
#!/usr/bin/env bash
if [ "$*" != "+%s" ]; then
  echo "stub date: unexpected argv: $*" >&2
  exit 98
fi
cat "$CLOCK"
EOF
# curl answers its nth call from line n of $STUB_ANSWERS, the last line
# repeating: "fail" exits 28 as a timeout does, "slow:<url>" takes 20 s of
# the fake clock and prints <url>, anything else is printed as the redirect
# -w reports. It refuses any argv but the shipped HEAD request.
cat >"$WORK/bin/curl" <<'EOF'
#!/usr/bin/env bash
want="-sI -o /dev/null -w %{redirect_url} --connect-timeout 10 --max-time 20 $LATEST_URL"
if [ "$*" != "$want" ]; then
  echo "stub curl: unexpected argv: $*" >&2
  exit 99
fi
printf 'x\n' >>"$CURL_LOG"
n=$(wc -l <"$CURL_LOG")
total=$(wc -l <"$STUB_ANSWERS")
answer=$(sed -n "$((n < total ? n : total))p" "$STUB_ANSWERS")
case "$answer" in
  fail) exit 28 ;;
  slow:*)
    echo $(($(cat "$CLOCK") + 20)) >"$CLOCK"
    answer=${answer#slow:}
    ;;
esac
printf '%s' "$answer"
EOF
# sleep records its argument and advances the fake clock by it, unless
# CLOCK_FROZEN is set; past 50 calls it fails, so a poll with no bound
# stops instead of hanging the suite.
cat >"$WORK/bin/sleep" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$1" >>"$SLEEP_LOG"
if [ -z "${CLOCK_FROZEN:-}" ]; then
  echo $(($(cat "$CLOCK") + $1)) >"$CLOCK"
fi
if [ "$(wc -l <"$SLEEP_LOG")" -gt 50 ]; then
  echo "stub sleep: more than 50 calls" >&2
  exit 97
fi
EOF
chmod +x "$WORK/bin/date" "$WORK/bin/curl" "$WORK/bin/sleep"
export PATH="$WORK/bin:$PATH"

OLD=https://github.com/cplieger/animap/releases/download/v2026.10.13/animap.json
NEW=https://github.com/cplieger/animap/releases/download/v2026.10.14/animap.json

# await <answer>...: run the shipped await_latest for v2026.10.14 in a child
# with publish.sh's options, curl answering in turn.
await() {
  printf '%s\n' "$@" >"$STUB_ANSWERS"
  : >"$CURL_LOG"
  : >"$SLEEP_LOG"
  echo 1000 >"$CLOCK"
  _rc=0
  bash -c 'set -euo pipefail; await_latest v2026.10.14' 2>"$WORK/err" || _rc=$?
  _err=$(cat "$WORK/err")
  _polls=$(wc -l <"$CURL_LOG")
  _sleeps=$(paste -sd' ' "$SLEEP_LOG")
}

# --- 0. the stubs answer as the cases assume ----------------------------------
printf '%s\n' "$OLD" "slow:$NEW" fail >"$STUB_ANSWERS"
: >"$CURL_LOG"
: >"$SLEEP_LOG"
echo 1000 >"$CLOCK"
first=$(curl -sI -o /dev/null -w '%{redirect_url}' --connect-timeout 10 --max-time 20 "$LATEST_URL")
first_rc=$?
second=$(curl -sI -o /dev/null -w '%{redirect_url}' --connect-timeout 10 --max-time 20 "$LATEST_URL")
after_slow=$(date +%s)
curl -sI -o /dev/null -w '%{redirect_url}' --connect-timeout 10 --max-time 20 "$LATEST_URL" >/dev/null
third_rc=$?
sleep 5
after_sleep=$(date +%s)
date -u >/dev/null 2>&1
date_rc=$?
if [ "$first" != "$OLD" ] || [ "$first_rc" -ne 0 ] || [ "$second" != "$NEW" ] || [ "$after_slow" -ne 1020 ] \
  || [ "$third_rc" -ne 28 ] || [ "$after_sleep" -ne 1025 ] || [ "$date_rc" -ne 98 ]; then
  no "precondition: the stubs answer in turn" "first=$first rc=$first_rc second=$second clock=$after_slow third rc=$third_rc clock=$after_sleep date rc=$date_rc"
  report
  exit 1
fi
ok "precondition: curl answers in turn, costs time and fails on cue; sleep and date run the fake clock"

# --- 1. the redirect moves after several polls --------------------------------
await "$OLD" "$OLD" "$OLD" "$OLD" "$NEW"
[ "$_rc" -eq 0 ] && [ "$_polls" -eq 5 ] && [ "$_sleeps" = "5 10 20 40" ] && [ -z "$_err" ] \
  && ok "the redirect moving on the fifth poll passes, after delays of 5, 10, 20 and 40 s" \
  || no "redirect moves on the fifth poll" "rc=$_rc polls=$_polls sleeps=$_sleeps err=$_err"

await "$NEW"
[ "$_rc" -eq 0 ] && [ "$_polls" -eq 1 ] && [ -z "$_sleeps" ] \
  && ok "a redirect already on the new tag passes without waiting" \
  || no "redirect already moved" "rc=$_rc polls=$_polls sleeps=$_sleeps err=$_err"

# --- 2. a redirect that never moves fails with the error line ----------------
await "$OLD"
want="publish: ERROR released v2026.10.14 but after 14 polls over 615s the latest download URL resolves to: $OLD"
[ "$_rc" -eq 1 ] && [ "$_err" = "$want" ] && [ "$_polls" -eq 14 ] \
  && [ "$_sleeps" = "5 10 20 40 60 60 60 60 60 60 60 60 60" ] \
  && ok "a redirect that never moves fails after about ten minutes, naming the wait and the URL it saw" \
  || no "redirect never moves" "rc=$_rc polls=$_polls sleeps=$_sleeps err=$_err"

# --- 3. a failed curl is retried, never read as a wrong tag ------------------
await fail fail "$NEW"
[ "$_rc" -eq 0 ] && [ "$_polls" -eq 3 ] && [ "$_sleeps" = "5 10" ] \
  && ok "two failed curls are retried and the third poll's new tag passes" \
  || no "failed curl retried" "rc=$_rc polls=$_polls sleeps=$_sleeps err=$_err"

await fail
[ "$_rc" -eq 1 ] && [ "$_polls" -eq 14 ] && [[ "$_err" == *"after 14 polls over 615s"*"resolves to: nothing, curl exited 28" ]] \
  && ok "a curl that always fails is retried to the end and named as a failure" \
  || no "curl always fails" "rc=$_rc polls=$_polls err=$_err"

await ""
[ "$_rc" -eq 1 ] && [[ "$_err" == *"resolves to: no redirect" ]] \
  && ok "a response with no redirect is named as such" \
  || no "no redirect" "rc=$_rc err=$_err"

# --- 4. the bound is elapsed time, curl time included -------------------------
# Each poll costs 20 s, so 11 polls and 10 delays (435 s) reach 655 s.
await "slow:$OLD"
want="publish: ERROR released v2026.10.14 but after 11 polls over 655s the latest download URL resolves to: $OLD"
[ "$_rc" -eq 1 ] && [ "$_err" = "$want" ] && [ "$_polls" -eq 11 ] \
  && ok "slow polls end the wait once 600 s have elapsed, and the error counts the curl time" \
  || no "slow curl" "rc=$_rc polls=$_polls sleeps=$_sleeps err=$_err"

# --- 5. a clock that never moves still stops at the poll backstop -------------
CLOCK_FROZEN=1 await "$OLD"
[ "$_rc" -eq 1 ] && [ "$_polls" -eq 30 ] && [[ "$_err" == *"after 30 polls over 0s"* ]] \
  && ok "a clock that never advances stops after 30 polls with the same error line" \
  || no "poll backstop" "rc=$_rc polls=$_polls err=$_err"

report
