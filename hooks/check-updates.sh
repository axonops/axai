#!/usr/bin/env bash
# SessionStart hook: warn when the bootstrap repo, the claude-skills
# marketplace, or installed plugins look stale.
#
# Output contract (Claude Code SessionStart hook):
#   stdout = JSON { "hookSpecificOutput": { "hookEventName": "SessionStart",
#                   "additionalContext": "<text>" } }
# Empty additionalContext = no warning surfaced.
#
# Runs daily at most per check (cached under ~/.cache/claude-bootstrap-updates).
# Override TTL with STATUSLINE_UPDATE_TTL (seconds, default 86400).
# Disable individual checks via STATUSLINE_UPDATE_DISABLE="repo marketplace plugins cli".

set -u

CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/claude-bootstrap-updates"
mkdir -p "$CACHE_DIR" 2>/dev/null
TTL="${STATUSLINE_UPDATE_TTL:-86400}"

disabled() { [[ " ${STATUSLINE_UPDATE_DISABLE:-} " == *" $1 "* ]]; }

cache_fresh() {
  local f="$CACHE_DIR/$1"
  [[ -f "$f" ]] || return 1
  local age=$(( $(date +%s) - $(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo 0) ))
  (( age < TTL ))
}
cache_write() { printf '%s' "$2" > "$CACHE_DIR/$1"; }
cache_read()  { cat "$CACHE_DIR/$1" 2>/dev/null; }

# Read stdin (Claude Code passes session JSON) but ignore — we only emit context.
read -r -d '' _STDIN <<< "$(cat)" || true

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

warnings=()

# ---------- 1. bootstrap repo behind origin ----------
check_repo() {
  disabled repo && return
  git -C "$PROJECT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return
  if cache_fresh repo; then
    local v; v="$(cache_read repo)"
    [[ -n "$v" ]] && warnings+=("$v")
    return
  fi
  # 5s timeout so a flaky network never hangs the session.
  if ! timeout 5 git -C "$PROJECT_DIR" fetch --quiet origin 2>/dev/null; then
    cache_write repo ""
    return
  fi
  local branch behind
  branch="$(git -C "$PROJECT_DIR" symbolic-ref --short HEAD 2>/dev/null || echo main)"
  behind="$(git -C "$PROJECT_DIR" rev-list --count "HEAD..origin/${branch}" 2>/dev/null || echo 0)"
  local msg=""
  if (( behind > 0 )); then
    msg="📦 bootstrap repo: ${behind} commit(s) behind origin/${branch} — \`git pull\`"
  fi
  cache_write repo "$msg"
  [[ -n "$msg" ]] && warnings+=("$msg")
}

# ---------- 2. claude-skills marketplace age ----------
check_marketplace() {
  disabled marketplace && return
  local mp="$HOME/.claude/plugins/marketplaces/axonops-claude-skills"
  if [[ ! -d "$mp" ]]; then
    warnings+=("⚠️  axonops-claude-skills marketplace not installed — run: \`/plugin marketplace add git@bitbucket.org:digitalisio/claude-skills.git\`")
    return
  fi
  local mtime now age
  mtime="$(stat -f %m "$mp" 2>/dev/null || stat -c %Y "$mp" 2>/dev/null || echo 0)"
  now="$(date +%s)"
  age=$(( (now - mtime) / 86400 ))
  if (( age >= 14 )); then
    warnings+=("🔄 claude-skills marketplace ${age}d stale — run: \`/plugin marketplace update axonops-claude-skills && /plugin update\`")
  fi
}

# ---------- 3. installed plugin set vs expected ----------
check_plugins() {
  disabled plugins && return
  local plug_dir="$HOME/.claude/plugins/installed"
  [[ -d "$plug_dir" ]] || return
  local required=("engineering-agents")
  # Detect what stack this repo wants.
  local repo_root="$PROJECT_DIR"
  [[ -d "$repo_root/ansible"   || -f "$repo_root/meta/main.yml" ]]    && required+=("ansible-bootstrap")
  shopt -s nullglob; local _tf=( "$repo_root"/*.tf "$repo_root"/terraform/*.tf ); shopt -u nullglob
  (( ${#_tf[@]} > 0 ))                                                && required+=("terraform-bootstrap")
  [[ -f "$repo_root/go.mod" ]]                                        && required+=("go-bootstrap")
  [[ -f "$repo_root/pyproject.toml" ]]                                && required+=("python-bootstrap")
  [[ -f "$repo_root/package.json" ]]                                  && required+=("frontend-bootstrap")
  local missing=()
  for p in "${required[@]}"; do
    if ! ls -d "$plug_dir"/*"$p"* >/dev/null 2>&1; then
      missing+=("$p")
    fi
  done
  if (( ${#missing[@]} > 0 )); then
    warnings+=("🧩 missing plugins: ${missing[*]} — run: \`/plugin install <name>@axonops-claude-skills\`")
  fi
}

# ---------- 4. KnowledgeRelay MCP ----------
check_mcp() {
  disabled mcp && return
  command -v claude >/dev/null 2>&1 || return
  if cache_fresh mcp; then
    local v; v="$(cache_read mcp)"
    [[ -n "$v" ]] && warnings+=("$v")
    return
  fi
  local msg=""
  if ! timeout 5 claude mcp list 2>/dev/null | grep -qi knowledgerelay; then
    msg="⚠️  KnowledgeRelay MCP not detected — internal AxonOps/Digitalis/customer answers will degrade. Install per https://bitbucket.org/digitalisio/knowledgerelay"
  fi
  cache_write mcp "$msg"
  [[ -n "$msg" ]] && warnings+=("$msg")
}

# ---------- 5. claude CLI version ----------
check_cli() {
  disabled cli && return
  command -v claude >/dev/null 2>&1 || return
  if cache_fresh cli; then
    local v; v="$(cache_read cli)"
    [[ -n "$v" ]] && warnings+=("$v")
    return
  fi
  local current latest msg=""
  current="$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
  latest="$(timeout 5 npm view @anthropic-ai/claude-code version 2>/dev/null)"
  if [[ -n "$current" && -n "$latest" && "$current" != "$latest" ]]; then
    msg="🆙 claude CLI ${current} → ${latest} available — \`npm i -g @anthropic-ai/claude-code\`"
  fi
  cache_write cli "$msg"
  [[ -n "$msg" ]] && warnings+=("$msg")
}

check_repo
check_marketplace
check_plugins
check_mcp
check_cli

if (( ${#warnings[@]} == 0 )); then
  printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":""}}'
  exit 0
fi

context="### Bootstrap session update check"$'\n\n'
for w in "${warnings[@]}"; do
  context+="- ${w}"$'\n'
done
context+=$'\n'"_Cached daily. Override TTL via \`STATUSLINE_UPDATE_TTL\`; disable checks via \`STATUSLINE_UPDATE_DISABLE\`._"

# Emit JSON with proper escaping.
escaped="$(printf '%s' "$context" | jq -Rs .)"
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}' "$escaped"
