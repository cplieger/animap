#!/usr/bin/env bash
# scripts/publish.sh's options and its dry run: DRY_RUN creates a release
# only when it is 0, empty or unset; ACCEPT_STALE needs a reason; and a dry
# run downloads, checks and verifies the export, picks the tag and writes
# the notes, then stops before signing or releasing. PUBLISH_SH points the
# suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null
ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/publish_stubs.sh"
load_function read_options
export -f read_options
setup_publish

# options <NAME=value|unset NAME>...: run the shipped read_options in a child
# with publish.sh's options and print what it settled on.
options() {
  _rc=0
  _out=$(env -u DRY_RUN -u ACCEPT_STALE -u STALE_REASON "$@" bash -c \
    'set -euo pipefail; read_options; printf "DRY_RUN=%s ACCEPT_STALE=%s STALE_REASON=%s\n" "$DRY_RUN" "$ACCEPT_STALE" "$STALE_REASON"' \
    2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

# --- 0. the stubs answer as the cases assume -----------------------------------
publish DRY_RUN=1 ANIMAP_EXPORT_TOKEN="$EXPORT_TOKEN" GH_TOKEN="$PUBLIC_TOKEN" GITHUB_SHA="$SHA"
if [ "$_rc" -ne 0 ] || ! logged '^gh api repos/cplieger/animap-source/releases/latest --jq .tag_name \[export\]$' \
  || ! logged '^go run ./cmd/animap verify '; then
  no "precondition: a dry run reaches the stubs" "rc=$_rc out=$_out err=$_err log=$_log"
  report
  exit 1
fi
ok "precondition: a dry run reaches the export, verify and tag stubs"
rm -f "$STUB_DIR/leaked"
_rc=0
env LEAK="x${EXPORT_TOKEN}x" dirname /a/b >/dev/null 2>&1 || _rc=$?
if [ "$_rc" -ne 95 ] || [ "$(cat "$STUB_DIR/leaked" 2>/dev/null)" != dirname ]; then
  no "precondition: a wrapped command refuses the export token" "rc=$_rc"
  report
  exit 1
fi
ok "precondition: a wrapped command refuses to run with the export token in its environment"

# --- 1. DRY_RUN ------------------------------------------------------------------
for live in unset 0 ''; do
  if [ "$live" = unset ]; then options; else options DRY_RUN="$live"; fi
  [ "$_rc" -eq 0 ] && [ "$_out" = "DRY_RUN=0 ACCEPT_STALE=0 STALE_REASON=" ] && [ -z "$_err" ] \
    && ok "DRY_RUN '$live' is a release run" \
    || no "DRY_RUN '$live'" "rc=$_rc out=$_out err=$_err"
done
options DRY_RUN=1
[ "$_rc" -eq 0 ] && [ "$_out" = "DRY_RUN=1 ACCEPT_STALE=0 STALE_REASON=" ] && [ -z "$_err" ] \
  && ok "DRY_RUN=1 is a dry run" \
  || no "DRY_RUN=1" "rc=$_rc out=$_out err=$_err"
for bad in true yes 2 01 ' 0' '1 '; do
  options DRY_RUN="$bad"
  [ "$_rc" -eq 0 ] && [[ "$_out" == "DRY_RUN=1 "* ]] \
    && [ "$_err" = "publish: WARNING DRY_RUN is neither 0 nor 1, so this is a dry run" ] \
    && ok "DRY_RUN='$bad' is a dry run, and the run says why" \
    || no "DRY_RUN='$bad'" "rc=$_rc out=$_out err=$_err"
done

# --- 2. ACCEPT_STALE and STALE_REASON ------------------------------------------
for off in '' 0 false; do
  options ACCEPT_STALE="$off" STALE_REASON=ignored
  [ "$_rc" -eq 0 ] && [[ "$_out" == *" ACCEPT_STALE=0 "* ]] \
    && ok "ACCEPT_STALE '$off' keeps the freshness checks" \
    || no "ACCEPT_STALE '$off'" "rc=$_rc out=$_out err=$_err"
done
for on in 1 true; do
  options ACCEPT_STALE="$on" STALE_REASON="$(printf 'rolled back\n to v2026.10.19.2223 ')"
  [ "$_rc" -eq 0 ] && [ "$_out" = "DRY_RUN=0 ACCEPT_STALE=1 STALE_REASON=rolled back to v2026.10.19.2223" ] \
    && ok "ACCEPT_STALE=$on overrides the freshness checks, with the reason on one line" \
    || no "ACCEPT_STALE=$on" "rc=$_rc out=$_out err=$_err"
done
for reason in unset '' '  ' "$(printf '\n\t')"; do
  if [ "$reason" = unset ]; then options ACCEPT_STALE=1; else options ACCEPT_STALE=1 STALE_REASON="$reason"; fi
  [ "$_rc" -eq 1 ] && [ -z "$_out" ] \
    && [ "$_err" = "publish: ERROR ACCEPT_STALE needs STALE_REASON, the reason the release notes give" ] \
    && ok "ACCEPT_STALE=1 with the reason '$reason' stops the run" \
    || no "ACCEPT_STALE=1 with the reason '$reason'" "rc=$_rc out=$_out err=$_err"
done
for bad in yes 2 TRUE ' 1'; do
  options ACCEPT_STALE="$bad" STALE_REASON=x
  [ "$_rc" -eq 1 ] && [ "$_err" = "publish: ERROR ACCEPT_STALE is neither 1, true, 0, false nor empty" ] \
    && ok "ACCEPT_STALE='$bad' stops the run" \
    || no "ACCEPT_STALE='$bad'" "rc=$_rc out=$_out err=$_err"
done

# --- 3. a dry run without ACCEPT_STALE ------------------------------------------
dry=(DRY_RUN=1 ANIMAP_EXPORT_TOKEN="$EXPORT_TOKEN" GH_TOKEN="$PUBLIC_TOKEN" GITHUB_SHA="$SHA")
publish "${dry[@]}"
want_log="gh api repos/cplieger/animap-source/releases/latest --jq .tag_name [export]
gh release download export-2026-10-20 --repo cplieger/animap-source --pattern animap.json --pattern animap.json.sha256 --dir @WORK@/export --clobber [export]
curl --proto =https --tlsv1.2 --connect-timeout 20 --max-time 120 --retry 3 -sSL -o @WORK@/previous.json -w %{http_code} https://github.com/cplieger/animap/releases/latest/download/animap.json
go run ./cmd/animap verify -in @WORK@/export/animap.json -previous @WORK@/previous.json
gh api repos/cplieger/animap/git/ref/tags/v2026.10.21 --jq .object.sha [public]"
got_log=$(sed -E 's#/tmp/tmp\.[A-Za-z0-9]+#@WORK@#g' "$STUB_LOG")
[ "$_rc" -eq 0 ] && [ "$got_log" = "$want_log" ] \
  && ok "a dry run reads the latest export with the export token alone, verifies it without the token and looks up today's tag, in that order" \
  || no "dry run calls" "rc=$_rc log=$got_log err=$_err"
[ "$_rc" -eq 0 ] && [ ! -e "$STUB_DIR/leaked" ] \
  && ok "no command the dry run spawns but gh, under GH_TOKEN, sees the export token, dirname included" \
  || no "export token leaked" "rc=$_rc leaked=$(cat "$STUB_DIR/leaked" 2>/dev/null) err=$_err"
[[ "$_out" == *"publish: DRY RUN, would release v2026.10.21 at $SHA; artifact at ./animap.json; the notes:"* ]] \
  && [[ "$_out" == *"content_hash: $HASH"*"revision: rev-1"*"titles_dump: 2026-10-19"*"AniList entries: 21000."*"the notice"*"Verify the download:"* ]] \
  && [[ "$_out" != *"freshness checks overridden"* ]] \
  && cmp -s "$REPO_COPY/animap.json" "$STUB_DIR/export/animap.json" \
  && [ ! -e "$STUB_DIR/created" ] && ! logged '^cosign ' && ! logged '/git/refs ' \
  && ok "a dry run prints the tag and the notes, keeps ./animap.json, and signs, tags and releases nothing" \
  || no "dry run outcome" "rc=$_rc out=$_out log=$_log"

publish "${dry[@]}" publish_export_token=inherited
[ "$_rc" -eq 0 ] && [[ "$_out" == *"publish: DRY RUN"* ]] && [ ! -e "$STUB_DIR/leaked" ] \
  && ok "a variable of the token copy's name in the environment does not carry the token to any command" \
  || no "inherited token copy" "rc=$_rc err=$_err"

# --- 4. a dry run with ACCEPT_STALE ---------------------------------------------
stale_export() { verdict earlier-shape true '["titles_dump is 20.0 days old","generated_at is 9.0 days old"]'; }
BEFORE_PUBLISH=stale_export publish "${dry[@]}" ACCEPT_STALE=1 STALE_REASON=held
[ "$_rc" -eq 0 ] && logged '^go run ./cmd/animap verify -in [^ ]+/export/animap.json -previous [^ ]+/previous.json -accept-stale$' \
  && [[ "$_out" == *"publish: DRY RUN, would release v2026.10.21"* ]] \
  && grep -qx 'Published with the freshness checks overridden: held (titles_dump is 20.0 days old; generated_at is 9.0 days old)' "$WORK/out" \
  && [ ! -e "$STUB_DIR/created" ] && ! logged '^cosign ' \
  && ok "a dry run with ACCEPT_STALE passes -accept-stale and the notes give the reason and what was out of bound" \
  || no "dry run with ACCEPT_STALE" "rc=$_rc out=$_out log=$_log err=$_err"

publish "${dry[@]}" ACCEPT_STALE=1 STALE_REASON=held
[ "$_rc" -eq 0 ] && logged ' -accept-stale$' && [[ "$_out" == *"publish: DRY RUN"* ]] \
  && [[ "$_out" != *"overridden"* ]] \
  && ok "ACCEPT_STALE on a fresh export overrides nothing, and the notes say nothing of it" \
  || no "ACCEPT_STALE on a fresh export" "rc=$_rc out=$_out err=$_err"

publish "${dry[@]}" ACCEPT_STALE=1 STALE_REASON=
[ "$_rc" -eq 1 ] && [ -z "$_log" ] && [[ "$_err" == *"publish: ERROR ACCEPT_STALE needs STALE_REASON"* ]] \
  && ok "ACCEPT_STALE with an empty reason stops before any download" \
  || no "ACCEPT_STALE with an empty reason" "rc=$_rc log=$_log err=$_err"

STUB_GO_FAILS=1 publish "${dry[@]}"
[ "$_rc" -eq 1 ] && [[ "$_err" == *"verify: the export is stale"* ]] && ! logged '/git/ref/tags/' \
  && ok "a verify that refuses the export stops the run before the tag lookup" \
  || no "verify refuses" "rc=$_rc log=$_log err=$_err"

publish "${dry[@]}" ACCEPT_SHRINK=true ACCEPT_STALE=true STALE_REASON=x
[ "$_rc" -eq 0 ] && logged '^go run ./cmd/animap verify -in [^ ]+ -previous [^ ]+ -accept-shrink -accept-stale$' \
  && ok "ACCEPT_SHRINK=true passes -accept-shrink" \
  || no "ACCEPT_SHRINK" "rc=$_rc log=$_log err=$_err"

# --- 5. which export --------------------------------------------------------------
publish "${dry[@]}" EXPORT_TAG=export-2026-10-13.2
[ "$_rc" -eq 0 ] && ! logged 'animap-source/releases/latest' \
  && logged '^gh release download export-2026-10-13.2 --repo cplieger/animap-source ' \
  && ok "EXPORT_TAG names the export release read, with no latest lookup" \
  || no "EXPORT_TAG" "rc=$_rc log=$_log err=$_err"

for bad in v2026.10.19.2223 'export-../x' 'export-a b' "$(printf 'export-a\nexport-b')"; do
  publish "${dry[@]}" EXPORT_TAG="$bad"
  [ "$_rc" -eq 1 ] && [ -z "$_log" ] && [[ "$_err" == *"publish: ERROR the export release "*" is not an export- tag"* ]] \
    && ok "EXPORT_TAG '$bad' stops the run before any download" \
    || no "EXPORT_TAG '$bad'" "rc=$_rc log=$_log err=$_err"
done

latest_is_a_data_release() { printf 'v2026.10.19\n' >"$STUB_DIR/latest-export"; }
BEFORE_PUBLISH=latest_is_a_data_release publish "${dry[@]}"
[ "$_rc" -eq 1 ] && ! logged 'release download' && [[ "$_err" == *"the export release v2026.10.19 is not an export- tag"* ]] \
  && ok "a latest export release that is not an export- tag stops the run" \
  || no "latest not an export" "rc=$_rc log=$_log err=$_err"

latest_lookup_fails() { rm -f "$STUB_DIR/latest-export"; }
BEFORE_PUBLISH=latest_lookup_fails publish "${dry[@]}"
[ "$_rc" -eq 1 ] && ! logged 'release download' && [[ "$_err" != *"is not an export- tag"* ]] \
  && ok "a failed latest export lookup stops the run as that failure" \
  || no "latest lookup fails" "rc=$_rc log=$_log err=$_err"

publish DRY_RUN=1 GH_TOKEN="$PUBLIC_TOKEN" GITHUB_SHA="$SHA"
[ "$_rc" -ne 0 ] && [ -z "$_log" ] && [[ "$_err" == *"set ANIMAP_EXPORT_TOKEN"* ]] \
  && ok "no ANIMAP_EXPORT_TOKEN stops the run before any call" \
  || no "no token" "rc=$_rc log=$_log err=$_err"

corrupt_sidecar() { printf '%064d  animap.json\n' 0 >"$STUB_DIR/export/animap.json.sha256"; }
restore_sidecar() { (cd "$STUB_DIR/export" && sha256sum animap.json >animap.json.sha256); }
BEFORE_PUBLISH=corrupt_sidecar publish "${dry[@]}"
restore_sidecar
[ "$_rc" -eq 1 ] && ! logged '^go ' && [[ "$_err" == *"animap.json does not match its animap.json.sha256"* ]] \
  && ok "an export that does not match its checksum stops the run before verify" \
  || no "checksum mismatch" "rc=$_rc log=$_log err=$_err"

two_line_sidecar() { (cd "$STUB_DIR/export" && sha256sum animap.json animap.json >animap.json.sha256); }
BEFORE_PUBLISH=two_line_sidecar publish "${dry[@]}"
restore_sidecar
[ "$_rc" -eq 1 ] && ! logged '^go ' \
  && ok "a checksum file of more than the one line stops the run" \
  || no "two-line sidecar" "rc=$_rc log=$_log err=$_err"

# --- 6. the comparison with the latest release ---------------------------------
unchanged() { verdict comparable false; }
BEFORE_PUBLISH=unchanged publish "${dry[@]}"
[ "$_rc" -eq 0 ] && [[ "$_out" == *"publish: content hash $HASH equals the latest release's, nothing to publish"* ]] \
  && ! logged '/git/ref/tags/' && [[ "$_out" != *"DRY RUN"* ]] \
  && ok "an export equal to the latest release publishes nothing and looks up no tag" \
  || no "unchanged" "rc=$_rc out=$_out log=$_log"

previous_release() {
  printf '{"version":2}\n' >"$STUB_DIR/previous.json"
  verdict comparable true
}
BEFORE_PUBLISH=previous_release publish "${dry[@]}"
[ "$_rc" -eq 0 ] && [[ "$_out" != *"new shape"* ]] && [[ "$_out" == *"publish: DRY RUN"* ]] \
  && ok "a release after a comparable one does not announce the shape change" \
  || no "comparable previous" "rc=$_rc out=$_out"
publish "${dry[@]}"
[[ "$_out" == *"This release changes the file to a new shape of format version 2, described in [docs/schema.md](https://github.com/cplieger/animap/blob/main/docs/schema.md). Entries without an AniList id were removed by the change."* ]] \
  && ok "the first release after an earlier-shape one announces the shape change once" \
  || no "earlier-shape previous" "out=$_out"

# --- 7. the shell the local command line uses -----------------------------------
if command -v dash >/dev/null 2>&1; then
  PUBLISH_SHELL=dash BEFORE_PUBLISH=stale_export publish "${dry[@]}" ACCEPT_STALE=1 STALE_REASON=held
  [ "$_rc" -eq 0 ] && [[ "$_out" == *"publish: DRY RUN, would release v2026.10.21"*"overridden: held (titles_dump"* ]] \
    && [ ! -e "$STUB_DIR/leaked" ] \
    && ok "under dash, as sh runs it, the dry run behaves the same and no command but gh sees the export token" \
    || no "dry run under dash" "rc=$_rc out=$_out err=$_err"
else
  skip "dry run under dash" "dash is not installed"
fi

report
