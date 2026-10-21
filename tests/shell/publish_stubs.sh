#!/usr/bin/env bash
# Runs the whole of scripts/publish.sh against stubs of everything that
# reaches the network or the clock: gh, curl, go, cosign, date and sleep.
# Each stub logs its argv to $STUB_LOG and refuses an argv the script does
# not send. Every other command the script runs is wrapped, and stubs and
# wrappers alike refuse to run, recording their name in $STUB_DIR/leaked,
# while the export token is in their environment anywhere but gh's GH_TOKEN.
# Sourced by publish_*_test.sh after lib.sh and new_workdir.

# SC2016: the stub bodies stay literal.
# shellcheck disable=SC2016

EXPORT_TOKEN=test-export-token
PUBLIC_TOKEN=test-public-token
SHA=1111111111111111111111111111111111111111
HASH=$(printf '%064d' 0 | tr 0 a)
REPO_COPY="$WORK/repo"
STUB_LOG="$WORK/stub.log"
STUB_DIR="$WORK/stub"
export EXPORT_TOKEN PUBLIC_TOKEN SHA REPO_COPY STUB_LOG STUB_DIR

# setup_publish copies $ENTRYPOINT into a repository of its own, so a dry
# run's ./animap.json lands there, and writes the stubs and the export.
setup_publish() {
  mkdir -p "$REPO_COPY/scripts" "$STUB_DIR/bin" "$STUB_DIR/export" "$STUB_DIR/tags"
  cp "$ENTRYPOINT" "$REPO_COPY/scripts/publish.sh"
  printf '{"version":2,"export":"fixture"}\n' >"$STUB_DIR/export/animap.json"
  (cd "$STUB_DIR/export" && sha256sum animap.json >animap.json.sha256)
  write_stubs
  write_wrappers
  export PATH="$STUB_DIR/bin:$PATH"
}

# The commands publish.sh runs that are neither stubbed nor shell builtins.
WRAPPED="cat cp cut dirname git grep jq mkdir mktemp rm sed sha256sum tr wc"

# token_check [NAME]: the first lines of a stub or wrapper, which let the
# token through only in NAME. It reads its own environment with builtins
# alone, since any command it ran would inherit the token it looks for.
token_check() {
  cat <<EOF
#!/usr/bin/env bash
while IFS= read -r -d '' kv; do
  case "\$kv" in
    EXPORT_TOKEN=* | ${1:-EXPORT_TOKEN}=*) ;;
    *"\$EXPORT_TOKEN"*)
      printf '%s\n' "\${0##*/}" >>"\$STUB_DIR/leaked"
      printf 'stub %s: the export token is in the environment\n' "\${0##*/}" >&2
      exit 95
      ;;
  esac
done </proc/self/environ
EOF
}

# write_wrappers puts a wrapper for each of $WRAPPED on PATH that runs the
# real command once its token check passes.
write_wrappers() {
  local name real
  for name in $WRAPPED; do
    real=$(command -v "$name") || {
      no "precondition: $name is installed" "PATH=$PATH"
      report
      exit 1
    }
    token_check >"$STUB_DIR/bin/$name"
    printf 'exec %q "$@"\n' "$real" >>"$STUB_DIR/bin/$name"
  done
  chmod +x "$STUB_DIR"/bin/*
}

verdict() {
  printf '{"content_hash":"%s","previous":"%s","changed":%s,%s"generated_at":"2026-10-20T08:30:00Z","revision":"rev-1","titles_dump":"2026-10-19","notice":"the notice","census":{"entries":21000,"entries_with_parts":13000,"rows_on_tvdb":7000,"episodes_on_tvdb":140000,"episodes_with_movies":2900,"anidb_episode_ids":290000}}\n' \
    "$HASH" "$1" "$2" "${3:+\"stale\":$3,}" >"$STUB_DIR/verdict.json"
}

write_stubs() {
  token_check GH_TOKEN >"$STUB_DIR/bin/gh"
  cat >>"$STUB_DIR/bin/gh" <<'EOF'
case "${GH_TOKEN:-}" in
  "$EXPORT_TOKEN") token=export ;;
  "$PUBLIC_TOKEN") token=public ;;
  *) token=none ;;
esac
unset GH_TOKEN
printf 'gh %s [%s]\n' "$*" "$token" >>"$STUB_LOG"
public=repos/cplieger/animap
case "$*" in
  "api repos/cplieger/animap-source/releases/latest --jq .tag_name")
    [ "$token" = export ] || exit 97
    cat "$STUB_DIR/latest-export"
    ;;
  "release download "*" --repo cplieger/animap-source --pattern animap.json --pattern animap.json.sha256 --dir "*" --clobber")
    [ "$token" = export ] && [ "$#" -eq 12 ] || exit 97
    cp "$STUB_DIR/export/animap.json" "$STUB_DIR/export/animap.json.sha256" "${11}"
    ;;
  "api $public/git/ref/tags/"*" --jq .object.sha" | "api $public/releases/tags/"*" --jq .id")
    [ "$token" = public ] && [ "$#" -eq 4 ] || exit 97
    answer="$STUB_DIR/tags/$(basename "$2")-$4"
    if [ -f "$answer" ]; then
      cat "$answer"
    else
      printf '{"message":"Not Found","status":"404"}\n'
      echo 'gh: Not Found (HTTP 404)' >&2
      exit 1
    fi
    ;;
  "api $public/git/refs -f ref=refs/tags/"*" -f sha=$SHA")
    [ "$token" = public ] && [ "$#" -eq 6 ] || exit 97
    printf '{}\n'
    ;;
  "release create "*" animap.json animap.json.sha256 animap.json.sigstore.json --repo cplieger/animap --title "*" --latest --verify-tag --notes-file "*)
    [ "$token" = public ] && [ "$#" -eq 14 ] && [ "${10}" = "$3" ] || exit 97
    cmp -s animap.json "$STUB_DIR/export/animap.json" && [ -s animap.json.sigstore.json ] || exit 96
    printf '%s\n' "$3" >"$STUB_DIR/created"
    cp "${14}" "$STUB_DIR/notes.created"
    ;;
  *)
    echo "stub gh: unexpected argv: $*" >&2
    exit 99
    ;;
esac
EOF
  token_check >"$STUB_DIR/bin/curl"
  cat >>"$STUB_DIR/bin/curl" <<'EOF'
printf 'curl %s\n' "$*" >>"$STUB_LOG"
latest=https://github.com/cplieger/animap/releases/latest/download/animap.json
if [ "$#" -eq 15 ] && [ "$*" = "--proto =https --tlsv1.2 --connect-timeout 20 --max-time 120 --retry 3 -sSL -o ${12} -w %{http_code} $latest" ]; then
  if [ -f "$STUB_DIR/previous.json" ]; then
    cp "$STUB_DIR/previous.json" "${12}"
    printf 200
  else
    printf 404
  fi
elif [ "$*" = "-sI -o /dev/null -w %{redirect_url} --connect-timeout 10 --max-time 20 $latest" ]; then
  printf 'https://github.com/cplieger/animap/releases/download/%s/animap.json' "$(cat "$STUB_DIR/created" 2>/dev/null)"
else
  echo "stub curl: unexpected argv: $*" >&2
  exit 99
fi
EOF
  token_check >"$STUB_DIR/bin/go"
  cat >>"$STUB_DIR/bin/go" <<'EOF'
printf 'go %s\n' "$*" >>"$STUB_LOG"
[ "$PWD" = "$REPO_COPY" ] || exit 97
case "$*" in
  "run ./cmd/animap verify -in "*"/animap.json -previous "*"/previous.json" | \
    "run ./cmd/animap verify -in "*"/animap.json -previous "*"/previous.json -accept-shrink" | \
    "run ./cmd/animap verify -in "*"/animap.json -previous "*"/previous.json -accept-stale" | \
    "run ./cmd/animap verify -in "*"/animap.json -previous "*"/previous.json -accept-shrink -accept-stale") ;;
  *)
    echo "stub go: unexpected argv: $*" >&2
    exit 99
    ;;
esac
cmp -s "$5" "$STUB_DIR/export/animap.json" || exit 96
if [ -n "${STUB_GO_FAILS:-}" ]; then
  echo "verify: the export is stale" >&2
  exit 1
fi
cat "$STUB_DIR/verdict.json"
EOF
  token_check >"$STUB_DIR/bin/cosign"
  cat >>"$STUB_DIR/bin/cosign" <<'EOF'
printf 'cosign %s\n' "$*" >>"$STUB_LOG"
[ "$*" = "sign-blob --yes animap.json --bundle animap.json.sigstore.json" ] || exit 99
printf '{"bundle":true}\n' >animap.json.sigstore.json
EOF
  token_check >"$STUB_DIR/bin/date"
  cat >>"$STUB_DIR/bin/date" <<'EOF'
case "$*" in
  '-u +%Y.%m.%d') cat "$STUB_DIR/day" ;;
  '-u +%H%M') echo 1205 ;;
  '+%s') echo 1000 ;;
  *)
    echo "stub date: unexpected argv: $*" >&2
    exit 99
    ;;
esac
EOF
  token_check >"$STUB_DIR/bin/sleep"
  cat >>"$STUB_DIR/bin/sleep" <<'EOF'
printf 'sleep %s\n' "$*" >>"$STUB_LOG"
exit 98
EOF
  chmod +x "$STUB_DIR"/bin/*
}

# publish [NAME=value...] [-- ARG...]: run the copied script in its
# repository with only the given options set and the given arguments, under
# $PUBLISH_SHELL (bash by default), setting _rc, _out, _err and _log. Each
# run starts from the default answers: the latest export is
# export-2026-10-20, there is no previous release and no tag today, and
# verify finds a changed export after an earlier-shape release.
# $BEFORE_PUBLISH, when set, names a function that changes them.
publish() {
  local assign=() arg
  while [ "$#" -gt 0 ]; do
    arg=$1
    shift
    [ "$arg" = -- ] && break
    assign+=("$arg")
  done
  printf 'export-2026-10-20\n' >"$STUB_DIR/latest-export"
  printf '2026.10.21\n' >"$STUB_DIR/day"
  rm -f "$STUB_DIR/leaked" "$STUB_DIR/created" "$STUB_DIR/notes.created" "$STUB_DIR/previous.json" "$STUB_DIR"/tags/* "$REPO_COPY/animap.json"
  verdict earlier-shape true
  [ -z "${BEFORE_PUBLISH:-}" ] || "$BEFORE_PUBLISH"
  : >"$STUB_LOG"
  _rc=0
  (cd "$REPO_COPY" && env -u DRY_RUN -u ACCEPT_STALE -u STALE_REASON -u ACCEPT_SHRINK -u EXPORT_TAG -u GITHUB_SHA \
    -u ANIMAP_EXPORT_TOKEN -u GH_TOKEN -u GITHUB_TOKEN -u GITHUB_REPOSITORY \
    "${assign[@]}" "${PUBLISH_SHELL:-bash}" scripts/publish.sh "$@") >"$WORK/out" 2>"$WORK/err" || _rc=$?
  _out=$(cat "$WORK/out")
  _err=$(cat "$WORK/err")
  _log=$(cat "$STUB_LOG")
}

# logged <pattern>: whether a stub logged a line matching the ERE.
logged() { grep -Eq -- "$1" "$STUB_LOG"; }
