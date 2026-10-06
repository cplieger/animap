#!/usr/bin/env bash
# SC2015: lib.sh verdict helpers return 0. SC2016: child scripts stay literal.
# shellcheck disable=SC2015,SC2016
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null

ENTRYPOINT="${DRIFT_SH:-$REPO_ROOT/scripts/drift.sh}"
INPUTS=$(extract_range '^list=""$' '^done <<<"\$out"$' "$WORK/inputs.sh") || exit 1

mkdir -p "$WORK/repo/scripts"
cat >"$WORK/repo/scripts/fetch-inputs.sh" <<'EOF'
[ -n "${AOD_VERSION:-}" ] && printf 'AOD_PATH=/cache/aod/%s.jsonl\n' "$AOD_VERSION"
printf 'LIST_PATH=/cache/list/%s.xml\n' "$LIST_COMMIT"
EOF

inputs() {
  _rc=0
  _out=$(cd "$WORK/repo" && env "$@" LIST_COMMIT=abc LIST_BLOB=def bash -c 'set -euo pipefail; . "$1"; printf "list=%s\n" "$list"' _ "$INPUTS" 2>"$WORK/err") || _rc=$?
  _err=$(cat "$WORK/err")
}

inputs -u AOD_VERSION
[ "$_rc" -eq 0 ] && [ "$_out" = "list=/cache/list/abc.xml" ] \
  && ok "drift: fetches the Anime-Lists file at the resolved commit" \
  || no "drift: the Anime-Lists file" "rc=$_rc out=$_out err=$_err"

inputs AOD_VERSION=2026-40
[ "$_rc" -eq 0 ] && [ "$_out" = "list=/cache/list/abc.xml" ] \
  && ok "drift: fetches no database even with AOD_VERSION in the environment" \
  || no "drift: AOD_VERSION in the environment" "rc=$_rc out=$_out err=$_err"

report
