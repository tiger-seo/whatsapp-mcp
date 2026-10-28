#!/usr/bin/env bash
# Claude Code hook: refuses the next step of a feature until owner-judge has reviewed what that step builds on.
#
#   /speckit-implement            needs a design review of the feature directory (spec, plan, tasks, ...)
#   gh pr create / gh pr merge    needs a code review of the branch
#
# A review is a row in the "Owner reviews" table of specs/NNN-*/verification.md:
#
#   | Stage | Date | Commit | Verdict | Owner decision |
#   | design | 2026-10-28 | 8c54a83 | PASS | |
#
# The last row of a stage counts. It passes when the verdict is PASS, or CONCERNS with the owner's decision
# filled in, and nothing it covers has changed since the commit it names. verification.md itself is never
# covered, so recording a verdict does not make it stale.
#
# A BLOCK also passes when the owner parked its findings (constitution, Development Workflow): the stage has at
# least 3 BLOCK rows, the decision reads "Parked at <commit>: <findings>", that commit changed nothing the
# review covers except the feature's tasks.md, where the parked findings are listed, and staleness is then
# measured from that commit.
#
# Wired as a PreToolUse hook (Skill, Bash) and a UserPromptSubmit hook in .claude/settings.json. It reads the
# hook's JSON on stdin and answers with JSON on stdout; it never changes anything.

set -u

event_json=$(cat)
cd "$(dirname "$0")/../.." || exit 0

if ! command -v jq >/dev/null 2>&1; then
  # Without jq the input cannot be read; let a person decide rather than block everything.
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"owner-review gate: jq is not installed, so the owner-judge review could not be checked."}}'
  exit 0
fi

event=$(jq -r '.hook_event_name // empty' <<<"$event_json")
tool=$(jq -r '.tool_name // empty' <<<"$event_json")

stage=""
explicit_target=""
case "$event" in
  UserPromptSubmit)
    prompt=$(jq -r '.prompt // empty' <<<"$event_json")
    [[ $prompt =~ ^[[:space:]]*/speckit-implement([[:space:]]|$) ]] && stage=design
    ;;
  PreToolUse)
    if [[ $tool == Skill ]]; then
      skill=$(jq -r '.tool_input.skill // empty' <<<"$event_json")
      [[ $skill == speckit-implement ]] && stage=design
    elif [[ $tool == Bash ]]; then
      cmd=$(jq -r '.tool_input.command // empty' <<<"$event_json")
      # Only gh in command position: at the start or after ; & | ( or a newline, not inside a quoted string.
      if [[ $cmd =~ (^|[\;\&\|\(]|$'\n')[[:space:]]*gh[[:space:]]+pr[[:space:]]+(create|merge)([[:space:]]|$) ]]; then
        stage=code
        # "gh pr merge 12" or a URL may name another branch's pull request; this gate only knows HEAD.
        if [[ ${BASH_REMATCH[2]} == merge && $cmd =~ pr[[:space:]]+merge[[:space:]]+(-[^[:space:]]+[[:space:]]+)*([0-9]+|https?://[^[:space:]]+) ]]; then
          explicit_target=${BASH_REMATCH[2]}
        fi
      fi
    fi
    ;;
esac

[[ -z $stage ]] && exit 0

# decide <allow|ask|deny> <reason>
decide() {
  local decision=$1 reason="owner-review gate ($stage review): $2"
  [[ $decision == allow ]] && exit 0
  if [[ $event == UserPromptSubmit ]]; then
    # A prompt can only be blocked or let through; "ask" lets it through with the reason as context.
    if [[ $decision == deny ]]; then
      jq -n --arg r "$reason" '{decision: "block", reason: $r}'
    else
      jq -n --arg r "$reason" '{hookSpecificOutput: {hookEventName: "UserPromptSubmit", additionalContext: $r}}'
    fi
  else
    jq -n --arg d "$decision" --arg r "$reason" \
      '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: $d, permissionDecisionReason: $r}}'
  fi
  exit 0
}

[[ -n $explicit_target ]] &&
  decide ask "this merges $explicit_target, which may not be the current branch. Confirm that its code review in verification.md passed."

branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || decide ask "not a git checkout."
prefix=""
[[ $branch =~ ^([0-9]{3})- ]] && prefix=${BASH_REMATCH[1]}
feature_dir=""
if [[ -n $prefix ]]; then
  for d in specs/"$prefix"-*/; do
    [[ -d $d ]] && feature_dir=${d%/} && break
  done
fi

if [[ -z $feature_dir ]]; then
  [[ $stage == design ]] && decide deny "branch $branch has no specs/NNN-* directory to review."
  decide ask "branch $branch has no feature directory, so there is no verification.md to check. Confirm that owner-judge reviewed this change and that the verdict is in the pull request description."
fi

record="$feature_dir/verification.md"
how="Run the owner-judge agent in $stage mode, commit what it reviewed, and add its row to the \"Owner reviews\" table in $record."
[[ -f $record ]] || decide deny "$record does not exist. $how"

row=$(grep -E "^\|[[:space:]]*$stage[[:space:]]*\|" "$record" | tail -n 1)
[[ -n $row ]] || decide deny "no $stage review is recorded in $record. $how"

field() { awk -F'|' -v n="$1" '{ gsub(/^[[:space:]]+|[[:space:]]+$/, "", $n); print $n }' <<<"$row"; }
commit=$(field 4 | tr -d '`')
verdict=$(field 5)
owner=$(field 6)

case "$verdict" in
  PASS) ;;
  CONCERNS)
    [[ -z $owner || $owner == - || $owner == — ]] &&
      decide deny "the last $stage review is CONCERNS and the owner's decision is not recorded in $record."
    ;;
  BLOCK)
    [[ $owner == Parked* ]] ||
      decide deny "the last $stage review in $record is BLOCK. Fix the findings and review again."
    blocks=$(grep -cE "^\|[[:space:]]*$stage[[:space:]]*\|[^|]*\|[^|]*\|[[:space:]]*BLOCK[[:space:]]*\|" "$record")
    ((blocks >= 3)) ||
      decide deny "the last $stage review is BLOCK with parked findings after only $blocks BLOCK rounds; parking needs at least 3."
    [[ $owner =~ ^Parked[[:space:]]+at[[:space:]]+\`?([0-9a-f]{7,40})\`?:[[:space:]]*[^[:space:]] ]] ||
      decide deny "the parked $stage review's decision must read \"Parked at <commit>: <findings>\", naming the commit that lists them in tasks.md."
    parked_at=${BASH_REMATCH[1]}
    ;;
  *) decide deny "the last $stage review in $record has verdict \"$verdict\"; expected PASS, CONCERNS or BLOCK." ;;
esac

git rev-parse --verify --quiet "$commit^{commit}" >/dev/null ||
  decide deny "the last $stage review names commit \"$commit\", which is not in this repository."

if [[ $stage == design ]]; then
  paths=("$feature_dir" ":(exclude)$feature_dir/verification.md" ":(exclude)$feature_dir/checklists")
else
  paths=("." ":(exclude)specs/*/verification.md")
fi
base=$commit
if [[ -n ${parked_at:-} ]]; then
  git rev-parse --verify --quiet "$parked_at^{commit}" >/dev/null && git merge-base --is-ancestor "$commit" "$parked_at" ||
    decide deny "the parking commit $parked_at is not in this repository or does not follow the reviewed commit $commit."
  # The parking commit may only list the parked findings; anything else it changes is unreviewed.
  extra=$(git diff --name-only "$commit" "$parked_at" -- "${paths[@]}" | grep -vxF "$feature_dir/tasks.md" | head -n 5)
  [[ -z $extra ]] ||
    decide deny "the parking commit $parked_at changes more than $feature_dir/tasks.md since the review: $(tr '\n' ' ' <<<"$extra")"
  grep -q '^## Parked findings' "$feature_dir/tasks.md" ||
    decide deny "findings are parked, but $feature_dir/tasks.md has no \"## Parked findings\" section."
  base=$parked_at
fi
changed=$( { git diff --name-only "$base" -- "${paths[@]}"; git ls-files --others --exclude-standard -- "${paths[@]}"; } | head -n 5)
[[ -z $changed ]] ||
  decide deny "files changed since the reviewed commit $base, so the review is stale: $(tr '\n' ' ' <<<"$changed")$how"

decide allow ""
