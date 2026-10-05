#!/usr/bin/env bash
# Apply the issue plan `animap issues` wrote: create, reopen, edit and close
# issues with gh. Every value comes from the plan file, never from the
# command line, so upstream text cannot reach a shell word.
set -euo pipefail

PLAN="${1:?usage: apply-issues.sh actions.json}"
REPO="${GITHUB_REPOSITORY:?set GITHUB_REPOSITORY}"

for label in animap drift watch publish; do
  gh label create "$label" --repo "$REPO" --force --color ededed >/dev/null
done

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
count=$(jq 'length' "$PLAN")
for ((i = 0; i < count; i++)); do
  jq -r ".[$i].body // \"\"" "$PLAN" >"$WORK/body.md"
  kind=$(jq -r ".[$i].kind" "$PLAN")
  number=$(jq -r ".[$i].number // 0" "$PLAN")
  title=$(jq -r ".[$i].title // \"\"" "$PLAN")
  labels=$(jq -r ".[$i].labels // [] | join(\",\")" "$PLAN")
  pin=$(jq -r ".[$i].pin // false" "$PLAN")
  case "$kind" in
    create)
      url=$(gh issue create --repo "$REPO" --title "$title" --body-file "$WORK/body.md" --label "$labels")
      echo "$url"
      if [ "$pin" = "true" ]; then
        gh issue pin "$url" --repo "$REPO"
      fi
      ;;
    reopen)
      gh issue reopen "$number" --repo "$REPO"
      gh issue edit "$number" --repo "$REPO" --title "$title" --body-file "$WORK/body.md"
      if [ "$pin" = "true" ]; then
        gh issue pin "$number" --repo "$REPO"
      fi
      ;;
    edit)
      gh issue edit "$number" --repo "$REPO" --title "$title" --body-file "$WORK/body.md"
      ;;
    close)
      jq -r ".[$i].comment // \"\"" "$PLAN" >"$WORK/comment.md"
      gh issue close "$number" --repo "$REPO" --comment "$(cat "$WORK/comment.md")"
      ;;
    *)
      echo "apply-issues: ERROR unknown action kind '${kind}'" >&2
      exit 1
      ;;
  esac
done
echo "apply-issues: applied ${count} action(s)"
