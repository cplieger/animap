#!/usr/bin/env bash
# scripts/publish.sh's release run, in the two steps the workflow runs:
# download with the export token, then release with the repository's own.
# The tag it creates must be one mapread's release reader accepts, since
# readers refuse any other latest release, and a tag that would not be is
# never created. PUBLISH_SH points the suite at another copy for a
# red-check.
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null
ENTRYPOINT="${PUBLISH_SH:-$REPO_ROOT/scripts/publish.sh}"
# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/publish_stubs.sh"
setup_publish

# The pattern mapread/fetch.go compiles as tagRE, as an ERE.
TAG_RE=$(sed -n 's/^[[:space:]]*tagRE[[:space:]]*= regexp.MustCompile(`\(.*\)`)$/\1/p' "$REPO_ROOT/mapread/fetch.go" | sed 's/\\d/[0-9]/g')
EXPORT_DIR="$WORK/export"

# download_step: the workflow's download step, with the export token alone.
download_step() {
  rm -rf "$EXPORT_DIR"
  publish ANIMAP_EXPORT_TOKEN="$EXPORT_TOKEN" -- download "$EXPORT_DIR"
}

# release_step [NAME=value...]: the workflow's release step over
# $EXPORT_DIR, with the repository's token alone.
release_step() {
  publish GH_TOKEN="$PUBLIC_TOKEN" GITHUB_SHA="$SHA" "$@" -- release "$EXPORT_DIR"
}

# --- 0. the pattern and the download step ---------------------------------------
if [ "$TAG_RE" != '^v[0-9]{4}\.[0-9]{2}\.[0-9]{2}(\.[0-9]{4})?$' ]; then
  no "precondition: mapread's tagRE reads as an ERE" "got '$TAG_RE'"
  report
  exit 1
fi
download_step
if [ "$_rc" -ne 0 ] || ! cmp -s "$EXPORT_DIR/animap.json" "$STUB_DIR/export/animap.json"; then
  no "precondition: the download step fetches the export" "rc=$_rc out=$_out err=$_err log=$_log"
  report
  exit 1
fi
ok "precondition: the download step fetches and checks the export"
[ "$(grep -c '^gh ' "$STUB_LOG")" -eq 2 ] && ! logged '^gh .*\[(public|none)\]$' && ! logged '^(go|curl|cosign) ' \
  && ok "the download step calls gh twice, both with the export token, and nothing else" \
  || no "download step calls" "log=$_log"

# --- 1. the release step creates a tag mapread's reader accepts ----------------
release_step
created=$(cat "$STUB_DIR/created" 2>/dev/null)
[ "$_rc" -eq 0 ] && [ "$created" = v2026.10.21 ] && printf '%s' "$created" | grep -Eq "$TAG_RE" \
  && logged "^gh api repos/cplieger/animap/git/refs -f ref=refs/tags/v2026.10.21 -f sha=$SHA \[public\]$" \
  && logged '^cosign sign-blob --yes animap.json --bundle animap.json.sigstore.json$' \
  && [[ "$_out" == *"publish: released v2026.10.21 (21000 AniList entries); latest pointer verified"* ]] \
  && ok "the release step tags this commit and releases v2026.10.21, which mapread's tagRE accepts" \
  || no "release step" "rc=$_rc created=$created out=$_out err=$_err log=$_log"
! logged '\[export\]$' \
  && ok "the release step never holds the export token" \
  || no "export token in the release step" "log=$_log"
notes=$(cat "$STUB_DIR/notes.created" 2>/dev/null)
[[ "$notes" == "content_hash: $HASH"$'\n'"revision: rev-1"$'\n'"titles_dump: 2026-10-19"$'\n\n'"AniList entries: 21000."* ]] \
  && [[ "$notes" == *"the notice"*"--certificate-identity https://github.com/cplieger/animap/.github/workflows/publish.yaml@refs/heads/main"* ]] \
  && [[ "$notes" != *"overridden"* ]] \
  && ok "the notes open with the content hash, revision and dump date lines, then the census, notice and verification" \
  || no "release notes" "notes=$notes"

released_today() {
  printf '%s\n' "$SHA" >"$STUB_DIR/tags/v2026.10.21-.object.sha"
  printf '42\n' >"$STUB_DIR/tags/v2026.10.21-.id"
}
BEFORE_PUBLISH=released_today release_step
created=$(cat "$STUB_DIR/created" 2>/dev/null)
[ "$_rc" -eq 0 ] && [ "$created" = v2026.10.21.1205 ] && printf '%s' "$created" | grep -Eq "$TAG_RE" \
  && ok "a second release the same day takes v2026.10.21.1205, which mapread's tagRE accepts" \
  || no "second release" "rc=$_rc created=$created err=$_err log=$_log"

stale_export() { verdict earlier-shape true '["titles_dump is 20.0 days old"]'; }
BEFORE_PUBLISH=stale_export release_step ACCEPT_STALE=1 STALE_REASON="the export is held"
[ "$_rc" -eq 0 ] && grep -qx 'Published with the freshness checks overridden: the export is held (titles_dump is 20.0 days old)' "$STUB_DIR/notes.created" \
  && ok "a release with ACCEPT_STALE says so in one line of its notes, with the reason" \
  || no "stale release notes" "rc=$_rc err=$_err"

# --- 2. a tag the reader would refuse is never created --------------------------
bad_day() { printf '2026.10.5\n' >"$STUB_DIR/day"; }
BEFORE_PUBLISH=bad_day release_step
[ "$_rc" -eq 1 ] && [ ! -e "$STUB_DIR/created" ] && ! logged '/git/refs ' && ! logged '^cosign ' \
  && [[ "$_err" == *"publish: ERROR v2026.10.5 is not a vYYYY.MM.DD or vYYYY.MM.DD.HHMM tag"* ]] \
  && ok "a tag mapread's reader would refuse stops the run before signing, tagging or releasing" \
  || no "malformed tag" "rc=$_rc err=$_err log=$_log"

# --- 3. a failed step publishes nothing ----------------------------------------
no_export() { rm -f "$EXPORT_DIR/animap.json"; }
BEFORE_PUBLISH=no_export release_step
[ "$_rc" -ne 0 ] && [ ! -e "$STUB_DIR/created" ] && ! logged '^go ' \
  && ok "a release step with no downloaded export publishes nothing" \
  || no "no export" "rc=$_rc err=$_err log=$_log"

publish GH_TOKEN="$PUBLIC_TOKEN" -- release
[ "$_rc" -eq 2 ] && [ -z "$_log" ] && [ "$_err" = "usage: publish.sh [download DIR | release DIR]" ] \
  && ok "a step with no directory is a usage error" \
  || no "usage" "rc=$_rc err=$_err"

report
