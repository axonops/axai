#!/usr/bin/env bash
# PreToolUse(Bash) hook: enforce bootstrap commit standards on `git commit`.
#
# Rules (from /Users/sergio.rua/.claude/CLAUDE.md + project CLAUDE.md):
#   1. Commit MUST be GPG/SSH signed     → require `-S` or `commit.gpgsign=true`
#   2. Commit MUST be DCO signed-off      → require `-s` / `--signoff`
#   3. `--no-gpg-sign` / `--no-verify` / `--no-signoff` are forbidden
#   4. Author identity MUST match repo brand:
#        owner == "axonops" or repo == axonops-* → @axonops.com
#        anything else                          → @digitalis.io
#   5. Subject line MUST be ≤72 chars
#   6. Author MUST NOT be "Bootstrap CI" or similar bot identity
#
# Install via .claude/settings.json:
#   "PreToolUse": [{ "matcher": "Bash",
#     "hooks": [{ "type": "command",
#                  "command": "${CLAUDE_PROJECT_DIR}/hooks/validate-commit.sh" }] }]
#
# Exit codes:
#   0 = allow
#   2 = block (Claude Code shows stderr to the model, prevents execution)

set -u

INPUT="$(cat)"
CMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)"

# Fast path: not a git commit command → silent allow.
[[ -z "$CMD" ]] && exit 0
[[ "$CMD" != *"commit"* ]] && exit 0
[[ "$CMD" != *"git"* ]] && exit 0

block() {
  local code="$1" reason="$2"
  jq -n --arg c "$code" --arg r "$reason" \
    '{decision:"block", code:$c, reason:$r}' 2>/dev/null
  echo "❌ commit blocked: $reason" >&2
  exit 2
}

# Tokenise the command honouring quotes/escapes.
if ! command -v python3 >/dev/null 2>&1; then
  echo "validate-commit: python3 required for safe parsing" >&2
  exit 0
fi
mapfile -t TOKENS < <(python3 -c '
import shlex, sys
try:
    for t in shlex.split(sys.argv[1]):
        print(t)
except ValueError:
    pass
' "$CMD")

# Find `git ... commit` sequence. Tokens between `git` and `commit` must be
# `-c key=val` pairs only; anything else (e.g. `gh commit-status`) → not us.
git_idx=-1; commit_idx=-1
for i in "${!TOKENS[@]}"; do
  if [[ "${TOKENS[$i]}" == "git" ]]; then git_idx=$i; fi
  if (( git_idx >= 0 )) && [[ "${TOKENS[$i]}" == "commit" ]]; then commit_idx=$i; break; fi
done
(( git_idx < 0 || commit_idx <= git_idx )) && exit 0
# Verify only -c pairs sit between git and commit.
j=$((git_idx+1))
while (( j < commit_idx )); do
  if [[ "${TOKENS[$j]}" == "-c" ]]; then ((j+=2)); continue; fi
  exit 0  # something else → not our case
done

# Collect `-c key=value` overrides between `git` and `commit`.
declare -A CFG=()
i=$((git_idx+1))
while (( i < commit_idx )); do
  if [[ "${TOKENS[$i]}" == "-c" ]] && (( i+1 < commit_idx )); then
    kv="${TOKENS[$((i+1))]}"
    CFG["${kv%%=*}"]="${kv#*=}"
    ((i+=2)); continue
  fi
  ((i++))
done

# Inspect post-`commit` args.
HAVE_S=0; HAVE_SIGNOFF=0; AMEND=0
MSG=""; MSG_FROM_FLAG=0
i=$((commit_idx+1))
while (( i < ${#TOKENS[@]} )); do
  t="${TOKENS[$i]}"
  case "$t" in
    -S|--gpg-sign|--gpg-sign=*)   HAVE_S=1 ;;
    --no-gpg-sign)                block "COMMIT_NO_GPG_SIGN" "--no-gpg-sign forbidden. All commits must be GPG/SSH signed." ;;
    --no-verify)                  block "COMMIT_NO_VERIFY" "--no-verify forbidden. Hooks must run." ;;
    --no-signoff)                 block "COMMIT_NO_SIGNOFF" "--no-signoff forbidden. DCO sign-off is mandatory." ;;
    -s|--signoff)                 HAVE_SIGNOFF=1 ;;
    --amend)                      AMEND=1 ;;
    -m|--message)
      if (( i+1 < ${#TOKENS[@]} )); then
        MSG="${TOKENS[$((i+1))]}"; MSG_FROM_FLAG=1; ((i++))
      fi ;;
    -m=*|--message=*)             MSG="${t#*=}"; MSG_FROM_FLAG=1 ;;
    -F|--file|-F=*|--file=*)      MSG_FROM_FLAG=1 ;;  # external file, skip subject check
  esac
  ((i++))
done

# --- Rule 1: signing ------------------------------------------------------
if (( ! HAVE_S )); then
  gpgsign_cfg="${CFG[commit.gpgsign]:-$(git config --get commit.gpgsign 2>/dev/null)}"
  if [[ "${gpgsign_cfg,,}" != "true" ]]; then
    block "COMMIT_UNSIGNED" "Missing -S and commit.gpgsign is not true. All commits must be GPG/SSH signed (see CLAUDE.md)."
  fi
fi

# --- Rule 2: sign-off -----------------------------------------------------
if (( ! HAVE_SIGNOFF )); then
  block "COMMIT_NO_DCO" "Missing -s/--signoff. DCO sign-off trailer is mandatory (see CLAUDE.md)."
fi

# --- Rule 4 & 6: brand-correct identity ----------------------------------
origin="$(git config --get remote.origin.url 2>/dev/null)"
if [[ -n "$origin" ]]; then
  owner="$(printf '%s' "$origin" | sed -E 's#.*[:/]([^/]+)/[^/]+(\.git)?$#\1#')"
  repo="$(basename -s .git "$origin")"
  if [[ "$owner" == "axonops" || "$repo" == axonops-* ]]; then
    expected="@axonops.com"
  else
    expected="@digitalis.io"
  fi
  email="${CFG[user.email]:-$(git config --get user.email 2>/dev/null)}"
  name="${CFG[user.name]:-$(git config --get user.name 2>/dev/null)}"
  if [[ -n "$email" && "$email" != *"$expected" ]]; then
    block "COMMIT_WRONG_BRAND_EMAIL" "Author email '$email' does not match repo brand (expected *${expected}). Override with: git -c user.email=sergio.rua${expected} -c user.name='Sergio Rua' commit -S -s ..."
  fi
  case "${name,,}" in
    *"bootstrap ci"*|*"ci bot"*|*"github-actions"*|*"renovate"*|*"dependabot"*)
      block "COMMIT_BOT_IDENTITY" "Author '$name' is a CI/bot identity. Commits must be authored by a real human." ;;
  esac
fi

# --- Rule 5: subject ≤72 chars -------------------------------------------
if (( MSG_FROM_FLAG )) && [[ -n "$MSG" ]]; then
  subject="${MSG%%$'\n'*}"
  if (( ${#subject} > 72 )); then
    block "COMMIT_SUBJECT_TOO_LONG" "Subject line is ${#subject} chars (max 72): '${subject:0:80}…'"
  fi
fi

exit 0
