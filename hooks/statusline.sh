#!/usr/bin/env bash
# Claude Code statusline for Digitalis/AxonOps bootstrap repos.
#
# Reads the stdin JSON Claude Code sends (model, workspace, context_window) and
# emits a single ANSI-coloured line. Tuned to surface the standards enforced by
# the bootstrap CLAUDE.md: brand, project type, signed commits, CHANGELOG gate,
# KnowledgeRelay MCP, and plugin freshness.
#
# Install: add to .claude/settings.json
#   { "statusLine": { "type": "command", "command": "hooks/statusline.sh" } }
#
# Disable individual segments via env vars:
#   STATUSLINE_DISABLE="changelog mcp plugins"
#
# Cache: ~/.cache/claude-statusline/ (30s TTL on expensive checks)

set -u

# ---------- stdin ----------
INPUT="$(cat)"
jq_get() { printf '%s' "$INPUT" | jq -r "$1 // empty" 2>/dev/null; }

MODEL="$(jq_get '.model.display_name')"
CWD="$(jq_get '.workspace.current_dir')"
SESSION="$(jq_get '.session_id')"
CTX_REMAIN="$(jq_get '.context_window.remaining_percentage')"
[[ -z "${CWD:-}" ]] && CWD="$PWD"
[[ -z "${MODEL:-}" ]] && MODEL="Claude"

# ---------- colours ----------
RESET=$'\e[0m'; DIM=$'\e[2m'; BOLD=$'\e[1m'; BLINK=$'\e[5m'
FG_GREEN=$'\e[32m'; FG_YELLOW=$'\e[33m'; FG_RED=$'\e[31m'
FG_BLUE=$'\e[34m'; FG_CYAN=$'\e[36m'; FG_MAGENTA=$'\e[35m'
FG_ORANGE=$'\e[38;5;208m'; FG_GREY=$'\e[38;5;245m'
SEP="${FG_GREY}│${RESET}"

disabled() { [[ " ${STATUSLINE_DISABLE:-} " == *" $1 "* ]]; }

# ---------- cache ----------
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/claude-statusline"
mkdir -p "$CACHE_DIR" 2>/dev/null
cache_get() {  # $1 key, $2 ttl_seconds
  local f="$CACHE_DIR/$1"
  [[ -f "$f" ]] || return 1
  local age=$(( $(date +%s) - $(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo 0) ))
  (( age < ${2:-30} )) || return 1
  cat "$f"
}
cache_put() { printf '%s' "$2" > "$CACHE_DIR/$1"; }

cd "$CWD" 2>/dev/null || exit 0

# ---------- segment: brand ----------
seg_brand() {
  local origin owner repo
  origin="$(git config --get remote.origin.url 2>/dev/null)"
  if [[ -n "$origin" ]]; then
    owner="$(printf '%s' "$origin" | sed -E 's#.*[:/]([^/]+)/[^/]+(\.git)?$#\1#')"
    repo="$(basename -s .git "$origin")"
  else
    repo="$(basename "$CWD")"
    owner=""
  fi
  if [[ "$owner" == "axonops" || "$repo" == axonops-* ]]; then
    printf '%bAxonOps%b' "${BOLD}${FG_BLUE}" "${RESET}"
  else
    printf '%bDigitalis%b' "${BOLD}${FG_GREEN}" "${RESET}"
  fi
}

# ---------- segment: model ----------
seg_model() { printf '%b%s%b' "$DIM" "$MODEL" "$RESET"; }

# ---------- segment: context meter ----------
seg_context() {
  [[ -z "${CTX_REMAIN:-}" ]] && return
  local used=$(( 100 - ${CTX_REMAIN%.*} ))
  (( used < 0 )) && used=0; (( used > 100 )) && used=100
  local filled=$(( (used + 5) / 10 ))
  local colour="$FG_GREEN" blink=""
  if   (( used >= 80 )); then colour="$FG_RED"; blink="$BLINK"
  elif (( used >= 65 )); then colour="$FG_ORANGE"
  elif (( used >= 50 )); then colour="$FG_YELLOW"
  fi
  local bar=""
  for ((i=0;i<10;i++)); do
    if (( i < filled )); then bar+="▰"; else bar+="▱"; fi
  done
  printf '%b%b%s%b %d%%' "$blink" "$colour" "$bar" "$RESET" "$used"
}

# ---------- segment: project type ----------
seg_type() {
  local types=()
  [[ -f meta/main.yml || -f meta/main.yaml || -d tasks ]] && types+=("ansible")
  shopt -s nullglob; _tf=( *.tf ); shopt -u nullglob
  (( ${#_tf[@]} > 0 )) && types+=("terraform")
  [[ -f go.mod ]] && types+=("go")
  [[ -f pyproject.toml || -f setup.py ]] && types+=("python")
  if [[ -f package.json ]]; then
    if grep -q '"playwright"\|"next"' package.json 2>/dev/null; then types+=("frontend"); else types+=("node"); fi
  fi
  local out
  case "${#types[@]}" in
    0) out="other";;
    1) out="${types[0]}";;
    *) out="$(IFS=+; echo "${types[*]}")";;
  esac
  printf '%b%s%b' "$FG_CYAN" "$out" "$RESET"
}

# ---------- segment: git ----------
seg_git() {
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return
  local branch dirty=""
  branch="$(git symbolic-ref --short HEAD 2>/dev/null || git rev-parse --short HEAD 2>/dev/null)"
  [[ -z "$branch" ]] && return
  [[ -n "$(git status --porcelain 2>/dev/null | head -1)" ]] && dirty="${FG_YELLOW}●${RESET}"
  # signed-commit check on HEAD
  local sigflag sig=""
  sigflag="$(git log -1 --pretty=%G? 2>/dev/null)"
  case "$sigflag" in
    G|U) sig="${FG_GREEN}✓${RESET}";;
    B|X|Y|R) sig="${FG_RED}✗${RESET}";;
    N|*) sig="${FG_RED}✗${RESET}";;
  esac
  printf '%b%s%b %s%s' "$FG_MAGENTA" "$branch" "$RESET" "$sig" "$dirty"
}

# ---------- segment: jira key from branch ----------
seg_jira() {
  local branch key
  branch="$(git symbolic-ref --short HEAD 2>/dev/null)" || return
  key="$(printf '%s' "$branch" | grep -oE '[A-Z]{2,8}-[0-9]+' | head -1)"
  [[ -n "$key" ]] && printf '%b%s%b' "$FG_BLUE" "$key" "$RESET"
}

# ---------- segment: changelog gate ----------
seg_changelog() {
  disabled changelog && return
  [[ -f CHANGELOG.md ]] || return
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return
  local branch base
  branch="$(git symbolic-ref --short HEAD 2>/dev/null)" || return
  [[ "$branch" == "main" || "$branch" == "master" ]] && return
  base="$(git rev-parse --verify --quiet origin/main 2>/dev/null || git rev-parse --verify --quiet origin/master 2>/dev/null || echo main)"
  local key="changelog-${branch//\//_}"
  local hit; hit="$(cache_get "$key" 30)" && { [[ -n "$hit" ]] && printf '%s' "$hit"; return; }
  local out=""
  if ! git diff --quiet "$base"...HEAD -- CHANGELOG.md 2>/dev/null; then
    : # changed, no warning
  else
    # any commits ahead?
    if [[ -n "$(git log --oneline "$base"..HEAD 2>/dev/null | head -1)" ]]; then
      out="${FG_RED}${BOLD}CHG!${RESET}"
    fi
  fi
  cache_put "$key" "$out"
  printf '%s' "$out"
}

# ---------- segment: knowledgerelay MCP ----------
seg_mcp() {
  disabled mcp && return
  local hit; hit="$(cache_get mcp 300)" && { [[ -n "$hit" ]] && printf '%s' "$hit"; return; }
  local out=""
  if command -v claude >/dev/null 2>&1; then
    if ! claude mcp list 2>/dev/null | grep -qi knowledgerelay; then
      out="${FG_YELLOW}KR?${RESET}"
    fi
  fi
  cache_put mcp "$out"
  printf '%s' "$out"
}

# ---------- segment: plugin freshness ----------
seg_plugins() {
  disabled plugins && return
  local mp="$HOME/.claude/plugins/marketplaces/axonops-claude-skills"
  [[ -d "$mp" ]] || return
  local mtime now age
  mtime="$(stat -f %m "$mp" 2>/dev/null || stat -c %Y "$mp" 2>/dev/null || echo 0)"
  now="$(date +%s)"
  age=$(( (now - mtime) / 86400 ))
  (( age >= 14 )) && printf '%b↻plugins(%dd)%b' "$FG_YELLOW" "$age" "$RESET"
}

# ---------- segment: dir ----------
seg_dir() {
  local base; base="$(basename "$CWD")"
  printf '%b%s%b' "$DIM" "$base" "$RESET"
}

# ---------- compose ----------
parts=()
for seg in brand model context type git jira changelog mcp plugins dir; do
  out="$(seg_"$seg" 2>/dev/null)"
  [[ -n "$out" ]] && parts+=("$out")
done

# join with separator
line=""
for p in "${parts[@]}"; do
  [[ -n "$line" ]] && line+=" $SEP "
  line+="$p"
done
printf '%s' "$line"
