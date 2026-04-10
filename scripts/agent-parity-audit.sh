#!/usr/bin/env bash
set -euo pipefail

ROOT="${HG_STUDIO_ROOT:-}"
DOCS_ROOT="${DOCS_ROOT:-}"
SCOPE_MANIFEST="${HG_PARITY_SCOPE_MANIFEST:-}"
WORKSPACE_MANIFEST="${HG_WORKSPACE_MANIFEST:-}"
WRITE_WORKSPACE_CACHE=0
WRITE_WIKI_DOCS=0
WRITE_JSON=0

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/hg-agent-parity.sh"
ROOT="${HG_STUDIO_ROOT}"
export HG_STUDIO_ROOT="$ROOT"
DOCS_ROOT="${DOCS_ROOT:-${HG_DOCS_ROOT:-$ROOT/docs}}"
SCOPE_MANIFEST="${SCOPE_MANIFEST:-$DOCS_ROOT/projects/agent-parity/repo-scope.json}"
WORKSPACE_MANIFEST="${WORKSPACE_MANIFEST:-$ROOT/workspace/manifest.json}"
WORKSPACE_OWNER="${HG_WORKSPACE_OWNER:-$(hg_workspace_owner "$ROOT")}"
WORKSPACE_HOME="${HG_WORKSPACE_HOME:-$(hg_workspace_owner_home "$WORKSPACE_OWNER" "$ROOT")}"

workspace_cache_dir() {
  printf '%s\n' "$DOCS_ROOT/agent-parity"
}

wiki_docs_dir() {
  printf '%s\n' "$DOCS_ROOT/projects/agent-parity"
}

usage() {
  cat <<'EOF'
Usage: agent-parity-audit.sh [--write-docs|--write-workspace-cache] [--write-wiki-docs] [--write-json]

Options:
  --write-docs            Deprecated alias for --write-workspace-cache
  --write-workspace-cache Write generated cache files to docs/agent-parity/
  --write-wiki-docs       Write canonical docs to docs/projects/agent-parity/
  --write-json            Write JSON and CSV inventory outputs
  -h, --help              Show this help
EOF
}

for arg in "$@"; do
  case "$arg" in
    --write-docs|--write-workspace-cache)
      WRITE_WORKSPACE_CACHE=1
      ;;
    --write-wiki-docs)
      WRITE_WIKI_DOCS=1
      ;;
    --write-json)
      WRITE_JSON=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $arg" >&2
      exit 1
      ;;
  esac
done

hg_require rg jq

if [[ ! -f "$SCOPE_MANIFEST" ]]; then
  echo "Scope manifest missing: $SCOPE_MANIFEST" >&2
  exit 1
fi

EXCLUDE_GLOBS=(
  "--glob=!whiteclaw/**"
  "--glob=!.git/**"
  "--glob=!node_modules/**"
  "--glob=!.claude/worktrees/**"
  "--glob=!.ralph/worktrees/**"
  "--glob=!.github/docs/**"
  "--glob=!.venv/**"
  "--glob=!venv/**"
  "--glob=!venv_test/**"
  "--glob=!__pycache__/**"
  "--glob=!.pytest_cache/**"
  "--glob=!.mypy_cache/**"
  "--glob=!.ruff_cache/**"
  "--glob=!htmlcov/**"
  "--glob=!_salvage/**"
  "--glob=!bin/**"
  "--glob=!build/**"
  "--glob=!dist/**"
)

build_repo_rg_args() {
  local repo="$1"
  REPO_RG_ARGS=("${EXCLUDE_GLOBS[@]}")

  case "$(basename "$repo")" in
    jobb)
      REPO_RG_ARGS+=("--glob=!python/**")
      ;;
    ralphglasses)
      REPO_RG_ARGS+=("--glob=!cmd/runmylife/**")
      ;;
  esac
}

inventory_csv=""
inventory_json_rows=""
json_separator=""
inventory_md=$'| Repo | `claude mcp` | `.claude/settings.json` refs | `claude_desktop_config.json` | `AGENTS.md` | `AGENTS.override.md` | `CLAUDE.md` | `GEMINI.md` | `copilot-instructions.md` | `.codex/config.toml` | full profiles | `.agents/skills/surface.yaml` | canonical `.agents/skills/*` | generated `.claude/skills/*` | generated plugin skills | `skills` | `roadmap` | `.ralph` | `.mcp.json` | repo MCP servers | MCP discovery contract | MCP resources | MCP prompts | MCP server health | full MCP contract | MCP policy files | generated MCP configs | unmanaged MCP blocks | example-only `.mcp.json` | Codex MCP servers | curated Codex MCP servers | raw Codex MCP servers | legacy `gpt-5.4-xhigh` | `.mcp.json` without policy | `.mcp.json` without curated Codex | `.codex/agents/*.toml` | `.codex-plugin` | Codex workflows | `codex_hooks = true` | root `.claude/settings.json` | root `.gemini/settings.json` | legacy `.gemini/config.yaml` | generated `.gemini/settings.json` | Gemini MCP servers | Gemini translated hooks | Claude-only hook gaps | Gemini extensions | provider MCP bridge | provider hook bridge | provider drift | Ollama support | Ollama profile source | provider runtime drift |\n|------|--------------:|-----------------------------:|-----------------------------:|-----------:|--------------------:|-----------:|-----------:|--------------------------:|---------------------:|--------------:|-------------------------------:|----------------------------:|----------------------------:|------------------------:|-----------:|-----------------:|-----------------------:|----------------:|--------------:|--------------------:|--------------------:|-----------------:|----------------------:|---------------------:|----------------------:|------------------:|--------------------------:|---------------------:|------------------------:|--------------------------:|-------------------------------:|-------------------------:|----------------:|----------------:|----------------------:|------------------------------:|------------------------------:|-------------------------------:|---------------------------------:|-------------------:|------------------------:|----------------------:|------------------:|--------------------:|---------------------:|----------------------:|-----------------------:|--------------------------------:|\n'

count_matches() {
  local repo="$1"
  local pattern="$2"
  local count
  build_repo_rg_args "$repo"
  count=$(rg -n --hidden "$pattern" "$repo" "${REPO_RG_ARGS[@]}" 2>/dev/null | wc -l | tr -d ' ')
  printf '%s' "$count"
}

scope_default() {
  jq -r '.default_scope // "active_first_party"' "$SCOPE_MANIFEST"
}

scope_override_bool() {
  local repo="$1"
  local key="$2"
  local default="$3"
  jq -r \
    --arg repo "$repo" \
    --arg key "$key" \
    --argjson def "$default" \
    '
      if ((.repo_overrides[$repo] // {}) | has($key)) then
        if .repo_overrides[$repo][$key] then 1 else 0 end
      else
        $def
      end
    ' \
    "$SCOPE_MANIFEST"
}

repo_scope() {
  local repo="$1"
  local manifest_scope
  manifest_scope="$(manifest_repo_field "$repo" "scope" "")"
  if [[ -n "$manifest_scope" ]]; then
    printf '%s' "$manifest_scope"
    return
  fi
  jq -r --arg repo "$repo" '(.repo_overrides[$repo].scope // .default_scope // "active_first_party")' "$SCOPE_MANIFEST"
}

manifest_repo_field() {
  local repo="$1"
  local field="$2"
  local default="${3:-}"
  if [[ ! -f "$WORKSPACE_MANIFEST" ]]; then
    printf '%s' "$default"
    return
  fi
  local value
  value=$(jq -r \
    --arg repo "$repo" \
    --arg field "$field" \
    '((.repos // []) | map(select(.name == $repo)) | .[0][$field]) // ""' \
    "$WORKSPACE_MANIFEST" 2>/dev/null || true)
  if [[ -z "$value" || "$value" == "null" ]]; then
    printf '%s' "$default"
    return
  fi
  printf '%s' "$value"
}

scope_is_active() {
  local scope="$1"
  case "$scope" in
    active_operator|active_first_party)
      printf '1'
      ;;
    *)
      printf '0'
      ;;
  esac
}

find_repo() {
  local repo="$1"
  shift
  find "$repo" \
    \( \
      -path '*/.git' -o \
      -path '*/node_modules' -o \
      -path '*/.claude/worktrees' -o \
      -path '*/.ralph/worktrees' -o \
      -path '*/.github/docs' -o \
      -path '*/.venv' -o \
      -path '*/venv' -o \
      -path '*/venv_test' -o \
      -path '*/__pycache__' -o \
      -path '*/.pytest_cache' -o \
      -path '*/.mypy_cache' -o \
      -path '*/.ruff_cache' -o \
      -path '*/htmlcov' -o \
      -path '*/_salvage' -o \
      -path '*/bin' -o \
      -path '*/build' -o \
      -path '*/dist' \
    \) -prune -o "$@"
}

count_files() {
  local repo="$1"
  local path_pattern="$2"
  local count
  count=$(find_repo "$repo" -path "$path_pattern" -print 2>/dev/null | wc -l | tr -d ' ')
  printf '%s' "$count"
}

count_named_files() {
  local repo="$1"
  shift
  local count=0
  local name
  for name in "$@"; do
    local matches
    matches=$(find_repo "$repo" -type f -name "$name" -print 2>/dev/null | wc -l | tr -d ' ')
    count=$((count + matches))
  done
  printf '%s' "$count"
}

count_generated_skill_files() {
  local repo="$1"
  local path_pattern="$2"
  local count=0
  local file
  while IFS= read -r file; do
    [[ -f "$file" ]] || continue
    if grep -q 'GENERATED BY hg-skill-surface-sync.sh' "$file"; then
      count=$((count + 1))
    fi
  done < <(find_repo "$repo" -path "$path_pattern" -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_generated_gemini_settings() {
  local repo="$1"
  hg_parity_generated_gemini_settings_count "$repo"
}

workflow_dir_dirty() {
  local repo="$1"
  if [[ ! -d "$repo/.github/workflows" ]]; then
    printf '0'
    return
  fi
  local status
  status=$(git -C "$repo" status --porcelain -- .github/workflows 2>/dev/null || true)
  if [[ -n "$status" ]]; then
    printf '1'
  else
    printf '0'
  fi
}

infer_workflow_family() {
  local repo="$1"
  if [[ ! -d "$repo/.github/workflows" ]]; then
    printf 'none'
    return
  fi
  if rg -n -q '(setup-go|go test|golangci|gofmt|go vet)' "$repo/.github/workflows" 2>/dev/null; then
    printf 'go-ci'
    return
  fi
  if rg -n -q '(setup-node|npm |pnpm|yarn )' "$repo/.github/workflows" 2>/dev/null; then
    printf 'node-ci'
    return
  fi
  if rg -n -q '(setup-python|pytest|python -m)' "$repo/.github/workflows" 2>/dev/null; then
    printf 'python-ci'
    return
  fi
  printf 'misc-ci'
}

compute_workflow_status() {
  local repo="$1"
  local workflow_policy="$2"
  local workflow_family="$3"
  local has_workflows=0
  [[ -d "$repo/.github/workflows" ]] && has_workflows=1

  case "$workflow_policy" in
    repo_owned)
      if [[ "$has_workflows" -eq 0 ]]; then
        printf 'missing_owned_workflow'
        return
      fi
      if [[ -z "$workflow_family" || "$workflow_family" == "none" ]]; then
        printf 'unexpected_workflow'
        return
      fi
      if [[ "$(workflow_dir_dirty "$repo")" -eq 1 ]]; then
        printf 'repo_owned_drift'
        return
      fi
      printf 'clean'
      ;;
    local_only)
      printf 'clean'
      ;;
    *)
      if [[ "$has_workflows" -eq 0 ]]; then
        printf 'clean'
        return
      fi
      printf 'retired_residue'
      ;;
  esac
}

sum_gemini_settings_metadata_field() {
  local repo="$1"
  local field="$2"
  case "$field" in
    gemini_mcp_server_count)
      hg_parity_gemini_mcp_server_count "$repo"
      ;;
    translated_hook_rules)
      hg_parity_supported_source_hook_rule_count "$repo"
      ;;
    unsupported_claude_hook_rules)
      hg_parity_unsupported_source_hook_rule_count "$repo"
      ;;
    *)
      printf '0'
      ;;
  esac
}

count_root_mcp_servers() {
  local repo="$1"
  local root_mcp="$repo/.mcp.json"
  if [[ ! -f "$root_mcp" ]]; then
    printf '0'
    return
  fi

  local repo_name count=0 name command args
  repo_name=$(basename "$repo")

  while IFS=$'\t' read -r name command args; do
    [[ -n "$name" ]] || continue
    if is_owned_mcp_entry "$repo" "$repo_name" "$command" "$args"; then
      count=$((count + 1))
    fi
  done < <(jq -r '
    if (.mcpServers | type == "object") then
      .mcpServers
      | to_entries[]?
      | select(.key | startswith("_") | not)
      | [.key, (.value.command // ""), ((.value.args // []) | map(tostring) | join(" "))]
      | @tsv
    else
      empty
    end
  ' "$root_mcp" 2>/dev/null)

  printf '%s' "$count"
}

is_owned_mcp_entry() {
  local repo="$1"
  local repo_name="$2"
  local command="$3"
  local args="$4"
  local combined
  combined="${command} ${args}"

  if [[ "$combined" == *"./"* ||
        "$combined" == *"\$HOME/hairglasses-studio/${repo_name}/"* ||
        "$combined" == *"${repo}/"* ||
        "$combined" == *"go run ."* ||
        "$combined" == *"go run ./"* ]]; then
    return 0
  fi

  return 1
}

count_full_profile_configs() {
  local repo="$1"
  local count=0
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    [[ -r "$config" ]] || continue
    if grep -q '^\[profiles\.readonly_quiet\]' "$config" &&
       grep -q '^\[profiles\.review\]' "$config" &&
       grep -q '^\[profiles\.workspace_auto\]' "$config" &&
       grep -q '^\[profiles\.ci_json\]' "$config"; then
      count=$((count + 1))
    fi
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_mcp_server_blocks_in_file() {
  local config="$1"
  [[ -r "$config" ]] || {
    printf '0'
    return 0
  }
  grep -Ec '^\[mcp_servers\.[A-Za-z0-9_-]+\]$' "$config" 2>/dev/null || true
}

count_curated_mcp_server_blocks_in_file() {
  local config="$1"
  [[ -r "$config" ]] || {
    printf '0'
    return 0
  }
  awk '
    /^\[mcp_servers\.[A-Za-z0-9_-]+\]$/ {
      if (in_server && curated) {
        count++
      }
      in_server = 1
      curated = 0
      next
    }
    /^\[/ {
      if (in_server && curated) {
        count++
      }
      in_server = 0
      curated = 0
      next
    }
    in_server && /^[[:space:]]*(enabled_tools|disabled_tools)[[:space:]]*=/ {
      curated = 1
    }
    END {
      if (in_server && curated) {
        count++
      }
      print count + 0
    }
  ' "$config"
}

count_unmanaged_mcp_server_blocks_in_file() {
  local config="$1"
  [[ -r "$config" ]] || {
    printf '0'
    return 0
  }
  awk '
    /^# BEGIN GENERATED MCP SERVERS: / {
      in_generated = 1
      next
    }
    /^# END GENERATED MCP SERVERS: / {
      in_generated = 0
      next
    }
    !in_generated && /^\[mcp_servers\.[A-Za-z0-9_-]+\]$/ {
      count++
    }
    END {
      print count + 0
    }
  ' "$config"
}

count_codex_mcp_servers() {
  local repo="$1"
  local count=0
  local config
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    count=$((count + $(count_mcp_server_blocks_in_file "$config")))
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_curated_codex_mcp_servers() {
  local repo="$1"
  local count=0
  local config
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    count=$((count + $(count_curated_mcp_server_blocks_in_file "$config")))
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_unmanaged_codex_mcp_servers() {
  local repo="$1"
  local count=0
  local config
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    count=$((count + $(count_unmanaged_mcp_server_blocks_in_file "$config")))
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_generated_mcp_config_files() {
  local repo="$1"
  local count=0
  local config
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    [[ -r "$config" ]] || continue
    if grep -q '^# BEGIN GENERATED MCP SERVERS: codex-mcp-sync$' "$config"; then
      count=$((count + 1))
    fi
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

count_matches_in_codex_configs() {
  local repo="$1"
  local pattern="$2"
  local count=0
  local config
  while IFS= read -r config; do
    [[ -f "$config" ]] || continue
    count=$((count + $(rg -n "$pattern" "$config" 2>/dev/null | wc -l | tr -d ' ')))
  done < <(find_repo "$repo" -path '*/.codex/config.toml' -type f -print 2>/dev/null | sort)
  printf '%s' "$count"
}

has_mcp_discovery_contract() {
  local repo="$1"
  local root_mcp_servers="$2"
  if [[ "$root_mcp_servers" -eq 0 ]]; then
    printf '0'
    return
  fi

  build_repo_rg_args "$repo"

  if rg -n -q '_tool_schema["'\'']' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null \
    && rg -n -q '_tool_stats["'\'']' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null \
    && rg -n -q '(_tool_(discover|search|catalog|groups|help)|_load_tool_group)["'\'']' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null; then
    printf '1'
    return
  fi

  if rg -n -q '(tools/list|handleToolsList)' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null \
    && rg -n -q '(include_schemas|deferredLoading|ListTools\()' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null; then
    printf '1'
    return
  fi
  printf '0'
}

has_mcp_resource_contract() {
  local repo="$1"
  local root_mcp_servers="$2"
  if [[ "$root_mcp_servers" -eq 0 ]]; then
    printf '0'
    return
  fi

  build_repo_rg_args "$repo"

  if rg -n -q 'NewResourceRegistry\(|RegisterResources\(|AddResource\(|AddResourceTemplate\(|registerResource\(|@mcp\.resource\(|\.(resource|addResource)\(|RESOURCE_DEFINITIONS|resources/list|_handle_resources_list|_handle_resources_read' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null; then
    printf '1'
  else
    printf '0'
  fi
}

has_mcp_prompt_contract() {
  local repo="$1"
  local root_mcp_servers="$2"
  if [[ "$root_mcp_servers" -eq 0 ]]; then
    printf '0'
    return
  fi

  build_repo_rg_args "$repo"

  if rg -n -q 'NewPromptRegistry\(|RegisterPrompts\(|AddPrompt\(|AddPrompts\(|registerPrompt\(|@mcp\.prompt\(|\.(prompt|addPrompt)\(|PROMPT_DEFINITIONS|prompts/list|prompts/get|_handle_prompts_list|_handle_prompts_get' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null; then
    printf '1'
  else
    printf '0'
  fi
}

has_mcp_server_health_tool() {
  local repo="$1"
  local root_mcp_servers="$2"
  if [[ "$root_mcp_servers" -eq 0 ]]; then
    printf '0'
    return
  fi

  build_repo_rg_args "$repo"

  if rg -n -q '(_server_health["'\'']|["'\''](ping|doctor|health_check|server_stats)["'\'']|_[a-z0-9]+_health(_full)?["'\''])' "$repo" "${REPO_RG_ARGS[@]}" --glob '*.{go,py,ts,js,mjs}' 2>/dev/null; then
    printf '1'
  else
    printf '0'
  fi
}

discover_repos() {
  local candidate
  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    if [[ -e "$candidate/.git" ]]; then
      printf '%s\n' "$candidate"
    fi
  done < <(find "$ROOT" -mindepth 1 -maxdepth 1 \( -type d -o -type l \) -print | sort)
}

repos=()
while IFS= read -r repo; do
  repos+=("$repo")
done < <(discover_repos)

total_claude_mcp=0
total_claude_settings=0
total_claude_desktop=0
total_missing_agents=0
total_missing_root_claude_settings=0
total_missing_root_gemini_settings=0
total_legacy_gemini_config_files=0
total_generated_gemini_settings=0
total_gemini_mcp_servers=0
total_gemini_translated_hook_rules=0
total_claude_only_hook_gaps=0
total_missing_codex=0
total_missing_plugins=0
total_missing_copilot=0
total_missing_gemini=0
total_missing_skill_surfaces=0
total_with_full_profiles=0
total_with_codex_agents=0
total_with_codex_workflows=0
total_with_agents_override=0
total_with_codex_hooks=0
total_with_canonical_skills=0
total_with_generated_claude_skills=0
total_with_generated_plugin_skills=0
total_mcp_json=0
total_repo_mcp_servers=0
total_codex_mcp_servers=0
total_curated_codex_mcp_servers=0
total_raw_codex_mcp_servers=0
total_unmanaged_codex_mcp_servers=0
total_legacy_model_tokens=0
total_repos_with_mcp_json=0
total_repos_with_codex_mcp_servers=0
total_repos_with_curated_codex_mcp_servers=0
total_repos_with_raw_codex_mcp_servers=0
total_repos_with_policy_managed_mcp=0
total_repos_with_generated_codex_mcp=0
total_repos_with_unmanaged_codex_mcp=0
total_repos_with_mcp_without_policy=0
total_repos_with_example_only_mcp_json=0
total_repos_with_legacy_model_tokens=0
total_repos_with_mcp_without_curated_codex=0
total_repos_with_gemini_extensions=0
total_repos_with_provider_mcp_bridge=0
total_repos_with_provider_hook_bridge=0
total_repos_with_provider_drift=0
total_repos_with_ollama_support=0
total_repos_with_ollama_session_provider=0
total_repos_with_ollama_docs_only=0
total_repos_with_provider_runtime_drift=0
total_provider_runtime_drift_count=0
total_repos_with_mcp_discovery_contract=0
total_repos_with_mcp_resource_contract=0
total_repos_with_mcp_prompt_contract=0
total_repos_with_mcp_server_health=0
total_repos_with_full_mcp_contract=0
total_repos_with_legacy_claude_commands=0
total_repos_with_unported_legacy_commands=0
total_legacy_claude_command_count=0
total_active_scope_repos=0
total_active_operator_repos=0
total_active_first_party_repos=0
total_excluded_repos=0
total_active_missing_agents=0
total_active_missing_gemini=0
total_active_missing_copilot=0
total_active_missing_codex=0
total_active_missing_root_claude_settings=0
total_active_missing_root_gemini_settings=0
total_active_missing_full_profiles=0
total_active_missing_codex_agents=0
total_active_missing_codex_workflows=0
total_active_missing_codex_plugins=0
total_active_mcp_repos=0
total_active_mcp_repos_missing_full_contract=0
total_active_missing_codex_hooks=0
total_active_mcp_repos_missing_provider_bridge=0
total_active_repos_missing_provider_hook_bridge=0
total_workflow_clean=0
total_workflow_repo_owned_drift=0
total_workflow_retired_residue=0
total_workflow_unexpected_workflow=0
total_workflow_missing_owned_workflow=0
scanned_repos=0
declare -A workflow_family_totals=()
declare -A workflow_family_clean=()
declare -A workflow_family_governance=()

for repo in "${repos[@]}"; do
  name=$(basename "$repo")
  if [[ "$name" == "whiteclaw" ]]; then
    continue
  fi

  scanned_repos=$((scanned_repos + 1))

  claude_mcp=$(count_matches "$repo" 'claude mcp')
  claude_settings=$(count_matches "$repo" '\.claude/settings\.json')
  claude_desktop=$(count_matches "$repo" 'claude_desktop_config\.json')
  agents_md=$(count_files "$repo" '*/AGENTS.md')
  agents_override=$(count_files "$repo" '*/AGENTS.override.md')
  claude_md=$(count_files "$repo" '*/CLAUDE.md')
  gemini_md=$(count_files "$repo" '*/GEMINI.md')
  copilot_instructions=$(count_files "$repo" '*/.github/copilot-instructions.md')
  codex_config=$(count_files "$repo" '*/.codex/config.toml')
  codex_full_profiles=$(count_full_profile_configs "$repo")
  skill_surface_manifest=$(count_files "$repo" '*/.agents/skills/surface.yaml')
  canonical_skills=$(count_files "$repo" '*/.agents/skills/*/SKILL.md')
  generated_claude_skills=$(count_generated_skill_files "$repo" '*/.claude/skills/*/SKILL.md')
  generated_plugin_skills=$(count_generated_skill_files "$repo" '*/plugins/*/skills/*/SKILL.md')
  has_skills=0
  hg_parity_repo_has_skills "$repo" && has_skills=1
  has_roadmap=0
  hg_parity_repo_has_roadmap "$repo" && has_roadmap=1
  has_ralph=0
  hg_parity_repo_has_ralph "$repo" && has_ralph=1
  repo_mcp_servers=$(count_root_mcp_servers "$repo")
  mcp_json=0
  mcp_policy=$(count_files "$repo" '*/.codex/mcp-profile-policy.json')
  if [[ "$repo_mcp_servers" -gt 0 ]]; then
    mcp_json=1
  fi
  example_only_mcp_json=0
  if [[ -f "$repo/.mcp.json" && "$repo_mcp_servers" -eq 0 ]]; then
    example_only_mcp_json=1
  fi
  mcp_discovery_contract=$(has_mcp_discovery_contract "$repo" "$repo_mcp_servers")
  mcp_resource_contract=$(has_mcp_resource_contract "$repo" "$repo_mcp_servers")
  mcp_prompt_contract=$(has_mcp_prompt_contract "$repo" "$repo_mcp_servers")
  mcp_server_health=$(has_mcp_server_health_tool "$repo" "$repo_mcp_servers")
  full_mcp_contract=0
  if [[ "$mcp_discovery_contract" -eq 1 && "$mcp_resource_contract" -eq 1 && "$mcp_prompt_contract" -eq 1 && "$mcp_server_health" -eq 1 ]]; then
    full_mcp_contract=1
  fi
  codex_mcp_servers=$(count_codex_mcp_servers "$repo")
  codex_curated_mcp_servers=$(count_curated_codex_mcp_servers "$repo")
  codex_raw_mcp_servers=$((codex_mcp_servers - codex_curated_mcp_servers))
  codex_unmanaged_mcp_servers=$(count_unmanaged_codex_mcp_servers "$repo")
  generated_mcp_configs=$(count_generated_mcp_config_files "$repo")
  legacy_model_tokens=$(count_matches_in_codex_configs "$repo" 'gpt-5\.4-xhigh')
  mcp_without_policy=0
  if [[ "$repo_mcp_servers" -gt 0 && "$mcp_policy" -eq 0 ]]; then
    mcp_without_policy=1
  fi
  mcp_without_curated_codex=0
  if [[ "$repo_mcp_servers" -gt 0 && "$codex_curated_mcp_servers" -eq 0 ]]; then
    mcp_without_curated_codex=1
  fi
  codex_agents=$(count_files "$repo" '*/.codex/agents/*.toml')
  codex_plugin=$(count_files "$repo" '*/.codex-plugin/plugin.json')
  codex_workflows=$(count_named_files "$repo/.github/workflows" 'codex-*.yml' 'codex-*.yaml' 'ai-dispatch.yml')
  codex_hooks=$(count_matches "$repo" 'codex_hooks\s*=\s*true')
  root_claude_settings=0
  [[ -f "$repo/.claude/settings.json" ]] && root_claude_settings=1
  root_gemini_settings=0
  [[ -f "$repo/.gemini/settings.json" ]] && root_gemini_settings=1
  legacy_gemini_config=$(count_files "$repo" '*/.gemini/config.yaml')
  generated_gemini_settings=$(count_generated_gemini_settings "$repo")
  gemini_mcp_servers=$(sum_gemini_settings_metadata_field "$repo" "gemini_mcp_server_count")
  gemini_translated_hook_rules=$(sum_gemini_settings_metadata_field "$repo" "translated_hook_rules")
  claude_only_hook_gaps=$(sum_gemini_settings_metadata_field "$repo" "unsupported_claude_hook_rules")
  gemini_extensions=$(count_files "$repo" '*/.gemini/extensions/*/gemini-extension.json')
  provider_mcp_bridge=$(hg_parity_provider_mcp_bridge_ok "$repo")
  provider_hook_bridge=$(hg_parity_provider_hook_bridge_ok "$repo" "$name")
  provider_drift=$(hg_parity_provider_drift_count "$repo" "$name")
  ollama_support_mode=$(hg_parity_ollama_support_mode "$repo" "$name")
  ollama_profile_source=$(hg_parity_ollama_profile_source "$repo" "$name")
  provider_runtime_drift=$(hg_parity_provider_runtime_drift_count "$repo" "$name")
  legacy_claude_commands=$(count_files "$repo" '*/.claude/commands/*.md')
  legacy_commands_unported=0
  if [[ "$legacy_claude_commands" -gt 0 && "$skill_surface_manifest" -eq 0 ]]; then
    legacy_commands_unported=$legacy_claude_commands
  fi
  baseline_profile=$(manifest_repo_field "$name" "baseline_profile" "")
  workflow_policy=$(manifest_repo_field "$name" "workflow_policy" "")
  if [[ -z "$workflow_policy" ]]; then
    if [[ -d "$repo/.github/workflows" ]]; then
      workflow_policy="repo_owned"
    else
      workflow_policy="retired"
    fi
  fi
  workflow_family=$(manifest_repo_field "$name" "workflow_family" "")
  if [[ -z "$workflow_family" ]]; then
    workflow_family=$(infer_workflow_family "$repo")
  fi
  workflow_status=$(compute_workflow_status "$repo" "$workflow_policy" "$workflow_family")
  scope=$(repo_scope "$name")
  active_scope=$(scope_is_active "$scope")
  expected_codex_baseline=$active_scope
  expected_full_profiles=0
  expected_codex_agents=0
  expected_codex_workflows=0
  if [[ "$scope" == "active_operator" ]]; then
    expected_full_profiles=1
    expected_codex_agents=1
  fi
  expected_codex_plugin=$(scope_override_bool "$name" "expect_codex_plugin" 0)
  expected_codex_hooks=$(scope_override_bool "$name" "expect_codex_hooks" 0)
  expected_mcp_contract=0
  if [[ "$active_scope" -eq 1 && "$repo_mcp_servers" -gt 0 ]]; then
    expected_mcp_contract=1
  fi
  expected_provider_hook_bridge=$active_scope
  expected_provider_hook_bridge=$(scope_override_bool "$name" "expect_provider_hook_bridge" "$expected_provider_hook_bridge")

  total_claude_mcp=$((total_claude_mcp + claude_mcp))
  total_claude_settings=$((total_claude_settings + claude_settings))
  total_claude_desktop=$((total_claude_desktop + claude_desktop))
  total_mcp_json=$((total_mcp_json + mcp_json))
  total_repo_mcp_servers=$((total_repo_mcp_servers + repo_mcp_servers))
  total_legacy_gemini_config_files=$((total_legacy_gemini_config_files + legacy_gemini_config))
  total_generated_gemini_settings=$((total_generated_gemini_settings + generated_gemini_settings))
  total_gemini_mcp_servers=$((total_gemini_mcp_servers + gemini_mcp_servers))
  total_gemini_translated_hook_rules=$((total_gemini_translated_hook_rules + gemini_translated_hook_rules))
  total_claude_only_hook_gaps=$((total_claude_only_hook_gaps + claude_only_hook_gaps))
  total_codex_mcp_servers=$((total_codex_mcp_servers + codex_mcp_servers))
  total_curated_codex_mcp_servers=$((total_curated_codex_mcp_servers + codex_curated_mcp_servers))
  total_raw_codex_mcp_servers=$((total_raw_codex_mcp_servers + codex_raw_mcp_servers))
  total_unmanaged_codex_mcp_servers=$((total_unmanaged_codex_mcp_servers + codex_unmanaged_mcp_servers))
  total_legacy_model_tokens=$((total_legacy_model_tokens + legacy_model_tokens))

  [[ "$agents_md" -eq 0 ]] && total_missing_agents=$((total_missing_agents + 1))
  [[ "$root_claude_settings" -eq 0 ]] && total_missing_root_claude_settings=$((total_missing_root_claude_settings + 1))
  [[ "$root_gemini_settings" -eq 0 ]] && total_missing_root_gemini_settings=$((total_missing_root_gemini_settings + 1))
  [[ "$codex_config" -eq 0 ]] && total_missing_codex=$((total_missing_codex + 1))
  [[ "$codex_plugin" -eq 0 ]] && total_missing_plugins=$((total_missing_plugins + 1))
  [[ "$copilot_instructions" -eq 0 ]] && total_missing_copilot=$((total_missing_copilot + 1))
  [[ "$gemini_md" -eq 0 ]] && total_missing_gemini=$((total_missing_gemini + 1))
  [[ "$skill_surface_manifest" -eq 0 ]] && total_missing_skill_surfaces=$((total_missing_skill_surfaces + 1))
  [[ "$codex_full_profiles" -gt 0 ]] && total_with_full_profiles=$((total_with_full_profiles + 1))
  [[ "$canonical_skills" -gt 0 ]] && total_with_canonical_skills=$((total_with_canonical_skills + 1))
  [[ "$generated_claude_skills" -gt 0 ]] && total_with_generated_claude_skills=$((total_with_generated_claude_skills + 1))
  [[ "$generated_plugin_skills" -gt 0 ]] && total_with_generated_plugin_skills=$((total_with_generated_plugin_skills + 1))
  [[ "$gemini_extensions" -gt 0 ]] && total_repos_with_gemini_extensions=$((total_repos_with_gemini_extensions + 1))
  [[ "$provider_mcp_bridge" -eq 1 ]] && total_repos_with_provider_mcp_bridge=$((total_repos_with_provider_mcp_bridge + 1))
  [[ "$provider_hook_bridge" -eq 1 ]] && total_repos_with_provider_hook_bridge=$((total_repos_with_provider_hook_bridge + 1))
  [[ "$provider_drift" -gt 0 ]] && total_repos_with_provider_drift=$((total_repos_with_provider_drift + 1))
  [[ "$ollama_support_mode" != "none" ]] && total_repos_with_ollama_support=$((total_repos_with_ollama_support + 1))
  [[ "$ollama_support_mode" == "session_provider" ]] && total_repos_with_ollama_session_provider=$((total_repos_with_ollama_session_provider + 1))
  [[ "$ollama_support_mode" == "docs_only" ]] && total_repos_with_ollama_docs_only=$((total_repos_with_ollama_docs_only + 1))
  if [[ "$provider_runtime_drift" -gt 0 ]]; then
    total_repos_with_provider_runtime_drift=$((total_repos_with_provider_runtime_drift + 1))
    total_provider_runtime_drift_count=$((total_provider_runtime_drift_count + provider_runtime_drift))
  fi
  [[ "$mcp_json" -gt 0 ]] && total_repos_with_mcp_json=$((total_repos_with_mcp_json + 1))
  [[ "$codex_mcp_servers" -gt 0 ]] && total_repos_with_codex_mcp_servers=$((total_repos_with_codex_mcp_servers + 1))
  [[ "$codex_curated_mcp_servers" -gt 0 ]] && total_repos_with_curated_codex_mcp_servers=$((total_repos_with_curated_codex_mcp_servers + 1))
  [[ "$codex_raw_mcp_servers" -gt 0 ]] && total_repos_with_raw_codex_mcp_servers=$((total_repos_with_raw_codex_mcp_servers + 1))
  [[ "$mcp_policy" -gt 0 ]] && total_repos_with_policy_managed_mcp=$((total_repos_with_policy_managed_mcp + 1))
  [[ "$generated_mcp_configs" -gt 0 ]] && total_repos_with_generated_codex_mcp=$((total_repos_with_generated_codex_mcp + 1))
  [[ "$codex_unmanaged_mcp_servers" -gt 0 ]] && total_repos_with_unmanaged_codex_mcp=$((total_repos_with_unmanaged_codex_mcp + 1))
  [[ "$mcp_without_policy" -gt 0 ]] && total_repos_with_mcp_without_policy=$((total_repos_with_mcp_without_policy + 1))
  [[ "$example_only_mcp_json" -gt 0 ]] && total_repos_with_example_only_mcp_json=$((total_repos_with_example_only_mcp_json + 1))
  [[ "$legacy_model_tokens" -gt 0 ]] && total_repos_with_legacy_model_tokens=$((total_repos_with_legacy_model_tokens + 1))
  [[ "$mcp_without_curated_codex" -gt 0 ]] && total_repos_with_mcp_without_curated_codex=$((total_repos_with_mcp_without_curated_codex + 1))
  [[ "$mcp_discovery_contract" -gt 0 ]] && total_repos_with_mcp_discovery_contract=$((total_repos_with_mcp_discovery_contract + 1))
  [[ "$mcp_resource_contract" -gt 0 ]] && total_repos_with_mcp_resource_contract=$((total_repos_with_mcp_resource_contract + 1))
  [[ "$mcp_prompt_contract" -gt 0 ]] && total_repos_with_mcp_prompt_contract=$((total_repos_with_mcp_prompt_contract + 1))
  [[ "$mcp_server_health" -gt 0 ]] && total_repos_with_mcp_server_health=$((total_repos_with_mcp_server_health + 1))
  [[ "$full_mcp_contract" -gt 0 ]] && total_repos_with_full_mcp_contract=$((total_repos_with_full_mcp_contract + 1))
  [[ "$codex_agents" -gt 0 ]] && total_with_codex_agents=$((total_with_codex_agents + 1))
  [[ "$codex_workflows" -gt 0 ]] && total_with_codex_workflows=$((total_with_codex_workflows + 1))
  [[ "$agents_override" -gt 0 ]] && total_with_agents_override=$((total_with_agents_override + 1))
  [[ "$codex_hooks" -gt 0 ]] && total_with_codex_hooks=$((total_with_codex_hooks + 1))
  if [[ "$legacy_claude_commands" -gt 0 ]]; then
    total_repos_with_legacy_claude_commands=$((total_repos_with_legacy_claude_commands + 1))
    total_legacy_claude_command_count=$((total_legacy_claude_command_count + legacy_claude_commands))
  fi
  [[ "$legacy_commands_unported" -gt 0 ]] && total_repos_with_unported_legacy_commands=$((total_repos_with_unported_legacy_commands + 1))
  case "$workflow_status" in
    clean)
      total_workflow_clean=$((total_workflow_clean + 1))
      ;;
    repo_owned_drift)
      total_workflow_repo_owned_drift=$((total_workflow_repo_owned_drift + 1))
      ;;
    retired_residue)
      total_workflow_retired_residue=$((total_workflow_retired_residue + 1))
      ;;
    unexpected_workflow)
      total_workflow_unexpected_workflow=$((total_workflow_unexpected_workflow + 1))
      ;;
    missing_owned_workflow)
      total_workflow_missing_owned_workflow=$((total_workflow_missing_owned_workflow + 1))
      ;;
  esac
  workflow_family_key="$workflow_family"
  [[ -z "$workflow_family_key" ]] && workflow_family_key="none"
  workflow_family_totals["$workflow_family_key"]=$(( ${workflow_family_totals["$workflow_family_key"]:-0} + 1 ))
  if [[ "$workflow_status" == "clean" ]]; then
    workflow_family_clean["$workflow_family_key"]=$(( ${workflow_family_clean["$workflow_family_key"]:-0} + 1 ))
  else
    workflow_family_governance["$workflow_family_key"]=$(( ${workflow_family_governance["$workflow_family_key"]:-0} + 1 ))
  fi

  if [[ "$scope" == "active_operator" ]]; then
    total_active_operator_repos=$((total_active_operator_repos + 1))
  fi
  if [[ "$scope" == "active_first_party" ]]; then
    total_active_first_party_repos=$((total_active_first_party_repos + 1))
  fi
  if [[ "$active_scope" -eq 1 ]]; then
    total_active_scope_repos=$((total_active_scope_repos + 1))
    [[ "$agents_md" -eq 0 ]] && total_active_missing_agents=$((total_active_missing_agents + 1))
    [[ "$root_claude_settings" -eq 0 ]] && total_active_missing_root_claude_settings=$((total_active_missing_root_claude_settings + 1))
    [[ "$root_gemini_settings" -eq 0 ]] && total_active_missing_root_gemini_settings=$((total_active_missing_root_gemini_settings + 1))
    [[ "$gemini_md" -eq 0 ]] && total_active_missing_gemini=$((total_active_missing_gemini + 1))
    [[ "$copilot_instructions" -eq 0 ]] && total_active_missing_copilot=$((total_active_missing_copilot + 1))
    [[ "$codex_config" -eq 0 ]] && total_active_missing_codex=$((total_active_missing_codex + 1))
    [[ "$expected_full_profiles" -eq 1 && "$codex_full_profiles" -eq 0 ]] && total_active_missing_full_profiles=$((total_active_missing_full_profiles + 1))
    [[ "$expected_codex_agents" -eq 1 && "$codex_agents" -eq 0 ]] && total_active_missing_codex_agents=$((total_active_missing_codex_agents + 1))
    [[ "$expected_codex_workflows" -eq 1 && "$codex_workflows" -eq 0 ]] && total_active_missing_codex_workflows=$((total_active_missing_codex_workflows + 1))
    [[ "$expected_codex_plugin" -eq 1 && "$codex_plugin" -eq 0 ]] && total_active_missing_codex_plugins=$((total_active_missing_codex_plugins + 1))
    [[ "$expected_codex_hooks" -eq 1 && "$codex_hooks" -eq 0 ]] && total_active_missing_codex_hooks=$((total_active_missing_codex_hooks + 1))
    [[ "$expected_provider_hook_bridge" -eq 1 && "$provider_hook_bridge" -eq 0 ]] && total_active_repos_missing_provider_hook_bridge=$((total_active_repos_missing_provider_hook_bridge + 1))
    if [[ "$expected_mcp_contract" -eq 1 ]]; then
      total_active_mcp_repos=$((total_active_mcp_repos + 1))
      [[ "$full_mcp_contract" -eq 0 ]] && total_active_mcp_repos_missing_full_contract=$((total_active_mcp_repos_missing_full_contract + 1))
      [[ "$provider_mcp_bridge" -eq 0 ]] && total_active_mcp_repos_missing_provider_bridge=$((total_active_mcp_repos_missing_provider_bridge + 1))
    fi
  else
    total_excluded_repos=$((total_excluded_repos + 1))
  fi

  inventory_csv+="${name},${claude_mcp},${claude_settings},${claude_desktop},${agents_md},${agents_override},${claude_md},${gemini_md},${copilot_instructions},${codex_config},${codex_full_profiles},${skill_surface_manifest},${canonical_skills},${generated_claude_skills},${generated_plugin_skills},${mcp_json},${repo_mcp_servers},${mcp_discovery_contract},${mcp_resource_contract},${mcp_prompt_contract},${mcp_server_health},${full_mcp_contract},${mcp_policy},${generated_mcp_configs},${codex_unmanaged_mcp_servers},${example_only_mcp_json},${codex_mcp_servers},${codex_curated_mcp_servers},${codex_raw_mcp_servers},${legacy_model_tokens},${mcp_without_policy},${mcp_without_curated_codex},${codex_agents},${codex_plugin},${codex_workflows},${codex_hooks},${root_claude_settings},${root_gemini_settings},${legacy_gemini_config},${generated_gemini_settings},${gemini_mcp_servers},${gemini_translated_hook_rules},${claude_only_hook_gaps},${gemini_extensions},${provider_mcp_bridge},${provider_hook_bridge},${provider_drift},${ollama_support_mode},${ollama_profile_source},${provider_runtime_drift},${scope},${active_scope},${expected_codex_baseline},${expected_full_profiles},${expected_codex_agents},${expected_codex_workflows},${expected_codex_plugin},${expected_mcp_contract},${expected_provider_hook_bridge},${expected_codex_hooks}"$'\n'
  inventory_json_rows+="${json_separator}"$'    {\n'
  inventory_json_rows+="      \"repo\": \"${name}\","$'\n'
  inventory_json_rows+="      \"claude_mcp_mentions\": ${claude_mcp},"$'\n'
  inventory_json_rows+="      \"claude_settings_mentions\": ${claude_settings},"$'\n'
  inventory_json_rows+="      \"claude_desktop_config_mentions\": ${claude_desktop},"$'\n'
  inventory_json_rows+="      \"agents_md_count\": ${agents_md},"$'\n'
  inventory_json_rows+="      \"agents_override_md_count\": ${agents_override},"$'\n'
  inventory_json_rows+="      \"claude_md_count\": ${claude_md},"$'\n'
  inventory_json_rows+="      \"gemini_md_count\": ${gemini_md},"$'\n'
  inventory_json_rows+="      \"copilot_instructions_count\": ${copilot_instructions},"$'\n'
  inventory_json_rows+="      \"codex_config_count\": ${codex_config},"$'\n'
  inventory_json_rows+="      \"codex_full_profile_pack_count\": ${codex_full_profiles},"$'\n'
  inventory_json_rows+="      \"skill_surface_manifest_count\": ${skill_surface_manifest},"$'\n'
  inventory_json_rows+="      \"canonical_skill_count\": ${canonical_skills},"$'\n'
  inventory_json_rows+="      \"generated_claude_skill_count\": ${generated_claude_skills},"$'\n'
  inventory_json_rows+="      \"generated_plugin_skill_count\": ${generated_plugin_skills},"$'\n'
  inventory_json_rows+="      \"has_skills\": ${has_skills},"$'\n'
  inventory_json_rows+="      \"has_roadmap\": ${has_roadmap},"$'\n'
  inventory_json_rows+="      \"has_ralph\": ${has_ralph},"$'\n'
  inventory_json_rows+="      \"mcp_json_count\": ${mcp_json},"$'\n'
  inventory_json_rows+="      \"repo_mcp_server_count\": ${repo_mcp_servers},"$'\n'
  inventory_json_rows+="      \"mcp_discovery_contract\": ${mcp_discovery_contract},"$'\n'
  inventory_json_rows+="      \"mcp_resource_contract\": ${mcp_resource_contract},"$'\n'
  inventory_json_rows+="      \"mcp_prompt_contract\": ${mcp_prompt_contract},"$'\n'
  inventory_json_rows+="      \"mcp_server_health_contract\": ${mcp_server_health},"$'\n'
  inventory_json_rows+="      \"full_mcp_contract\": ${full_mcp_contract},"$'\n'
  inventory_json_rows+="      \"mcp_policy_count\": ${mcp_policy},"$'\n'
  inventory_json_rows+="      \"generated_codex_mcp_config_count\": ${generated_mcp_configs},"$'\n'
  inventory_json_rows+="      \"unmanaged_codex_mcp_server_count\": ${codex_unmanaged_mcp_servers},"$'\n'
  inventory_json_rows+="      \"example_only_mcp_json\": ${example_only_mcp_json},"$'\n'
  inventory_json_rows+="      \"codex_mcp_server_count\": ${codex_mcp_servers},"$'\n'
  inventory_json_rows+="      \"codex_curated_mcp_server_count\": ${codex_curated_mcp_servers},"$'\n'
  inventory_json_rows+="      \"codex_raw_mcp_server_count\": ${codex_raw_mcp_servers},"$'\n'
  inventory_json_rows+="      \"legacy_codex_model_token_count\": ${legacy_model_tokens},"$'\n'
  inventory_json_rows+="      \"mcp_without_policy\": ${mcp_without_policy},"$'\n'
  inventory_json_rows+="      \"mcp_without_curated_codex\": ${mcp_without_curated_codex},"$'\n'
  inventory_json_rows+="      \"codex_agent_count\": ${codex_agents},"$'\n'
  inventory_json_rows+="      \"codex_plugin_count\": ${codex_plugin},"$'\n'
  inventory_json_rows+="      \"codex_workflow_count\": ${codex_workflows},"$'\n'
  inventory_json_rows+="      \"codex_hooks_enabled_count\": ${codex_hooks},"$'\n'
  inventory_json_rows+="      \"root_claude_settings\": ${root_claude_settings},"$'\n'
  inventory_json_rows+="      \"root_gemini_settings\": ${root_gemini_settings},"$'\n'
  inventory_json_rows+="      \"legacy_gemini_config_count\": ${legacy_gemini_config},"$'\n'
  inventory_json_rows+="      \"generated_gemini_settings_count\": ${generated_gemini_settings},"$'\n'
  inventory_json_rows+="      \"gemini_mcp_server_count\": ${gemini_mcp_servers},"$'\n'
  inventory_json_rows+="      \"gemini_translated_hook_rule_count\": ${gemini_translated_hook_rules},"$'\n'
  inventory_json_rows+="      \"claude_only_hook_gap_count\": ${claude_only_hook_gaps},"$'\n'
  inventory_json_rows+="      \"gemini_extension_count\": ${gemini_extensions},"$'\n'
  inventory_json_rows+="      \"provider_mcp_bridge\": ${provider_mcp_bridge},"$'\n'
  inventory_json_rows+="      \"provider_hook_bridge\": ${provider_hook_bridge},"$'\n'
  inventory_json_rows+="      \"provider_drift_count\": ${provider_drift},"$'\n'
  inventory_json_rows+="      \"ollama_support_mode\": \"${ollama_support_mode}\","$'\n'
  inventory_json_rows+="      \"ollama_profile_source\": \"${ollama_profile_source}\","$'\n'
  inventory_json_rows+="      \"provider_runtime_drift_count\": ${provider_runtime_drift},"$'\n'
  inventory_json_rows+="      \"baseline_profile\": \"${baseline_profile}\","$'\n'
  inventory_json_rows+="      \"workflow_policy\": \"${workflow_policy}\","$'\n'
  inventory_json_rows+="      \"workflow_family\": \"${workflow_family}\","$'\n'
  inventory_json_rows+="      \"workflow_status\": \"${workflow_status}\","$'\n'
  inventory_json_rows+="      \"scope\": \"${scope}\","$'\n'
  inventory_json_rows+="      \"active_scope\": ${active_scope},"$'\n'
  inventory_json_rows+="      \"expected_codex_baseline\": ${expected_codex_baseline},"$'\n'
  inventory_json_rows+="      \"expected_full_profile_pack\": ${expected_full_profiles},"$'\n'
  inventory_json_rows+="      \"expected_codex_agents\": ${expected_codex_agents},"$'\n'
  inventory_json_rows+="      \"expected_codex_workflows\": ${expected_codex_workflows},"$'\n'
  inventory_json_rows+="      \"expected_codex_plugin\": ${expected_codex_plugin},"$'\n'
  inventory_json_rows+="      \"expected_mcp_contract\": ${expected_mcp_contract},"$'\n'
  inventory_json_rows+="      \"expected_provider_hook_bridge\": ${expected_provider_hook_bridge},"$'\n'
  inventory_json_rows+="      \"expected_codex_hooks\": ${expected_codex_hooks},"$'\n'
  inventory_json_rows+="      \"legacy_claude_command_count\": ${legacy_claude_commands},"$'\n'
  inventory_json_rows+="      \"legacy_commands_unported\": ${legacy_commands_unported}"$'\n'
  inventory_json_rows+=$'    }\n'
  json_separator=$',\n'
  inventory_md+="| ${name} | ${claude_mcp} | ${claude_settings} | ${claude_desktop} | ${agents_md} | ${agents_override} | ${claude_md} | ${gemini_md} | ${copilot_instructions} | ${codex_config} | ${codex_full_profiles} | ${skill_surface_manifest} | ${canonical_skills} | ${generated_claude_skills} | ${generated_plugin_skills} | ${has_skills} | ${has_roadmap} | ${has_ralph} | ${mcp_json} | ${repo_mcp_servers} | ${mcp_discovery_contract} | ${mcp_resource_contract} | ${mcp_prompt_contract} | ${mcp_server_health} | ${full_mcp_contract} | ${mcp_policy} | ${generated_mcp_configs} | ${codex_unmanaged_mcp_servers} | ${example_only_mcp_json} | ${codex_mcp_servers} | ${codex_curated_mcp_servers} | ${codex_raw_mcp_servers} | ${legacy_model_tokens} | ${mcp_without_policy} | ${mcp_without_curated_codex} | ${codex_agents} | ${codex_plugin} | ${codex_workflows} | ${codex_hooks} | ${root_claude_settings} | ${root_gemini_settings} | ${legacy_gemini_config} | ${generated_gemini_settings} | ${gemini_mcp_servers} | ${gemini_translated_hook_rules} | ${claude_only_hook_gaps} | ${gemini_extensions} | ${provider_mcp_bridge} | ${provider_hook_bridge} | ${provider_drift} | ${ollama_support_mode} | ${ollama_profile_source} | ${provider_runtime_drift} |"$'\n'
done

workflow_family_summary_text=""
workflow_family_summary_md=""
workflow_family_summary_json=""
workflow_family_json_sep=""
if [[ "${#workflow_family_totals[@]}" -gt 0 ]]; then
  while IFS= read -r family; do
    [[ -n "$family" ]] || continue
    family_total="${workflow_family_totals[$family]:-0}"
    family_clean="${workflow_family_clean[$family]:-0}"
    family_governance="${workflow_family_governance[$family]:-0}"
    workflow_family_summary_text+=$'\n'"- ${family}: repos=${family_total} clean=${family_clean} governance=${family_governance}"
    workflow_family_summary_md+=$'\n'"- \`${family}\`: repos=${family_total}, clean=${family_clean}, governance=${family_governance}"
    workflow_family_summary_json+="${workflow_family_json_sep}"$'\n'"    {"$'\n'"      \"family\": \"${family}\","$'\n'"      \"repos\": ${family_total},"$'\n'"      \"clean\": ${family_clean},"$'\n'"      \"governance\": ${family_governance}"$'\n'"    }"
    workflow_family_json_sep=","
  done < <(printf '%s\n' "${!workflow_family_totals[@]}" | sort)
fi
if [[ -z "$workflow_family_summary_text" ]]; then
  workflow_family_summary_text=$'\n'"- none"
  workflow_family_summary_md=$'\n'"- none"
fi

global_claude_commands=$(find "$WORKSPACE_HOME/.claude/commands" -name '*.md' 2>/dev/null | wc -l | tr -d ' ')
global_claude_skills=$(find "$WORKSPACE_HOME/.claude/skills" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
global_agents_skills=$(find "$WORKSPACE_HOME/.agents/skills" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
global_codex_skills=$(find "$WORKSPACE_HOME/.codex/skills" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
workspace_global_sync_ok=false
workspace_global_home_overlay_ok=false
if [[ -x "$ROOT/dotfiles/scripts/hg-workspace-global-sync.sh" ]] && env HOME="$WORKSPACE_HOME" HG_WORKSPACE_OWNER="$WORKSPACE_OWNER" HG_WORKSPACE_HOME="$WORKSPACE_HOME" "$ROOT/dotfiles/scripts/hg-workspace-global-sync.sh" --root "$ROOT" --source-check >/dev/null 2>&1; then
  workspace_global_sync_ok=true
fi
if [[ -x "$ROOT/dotfiles/scripts/hg-workspace-global-sync.sh" ]] && env HOME="$WORKSPACE_HOME" HG_WORKSPACE_OWNER="$WORKSPACE_OWNER" HG_WORKSPACE_HOME="$WORKSPACE_HOME" "$ROOT/dotfiles/scripts/hg-workspace-global-sync.sh" --root "$ROOT" --check >/dev/null 2>&1; then
  workspace_global_home_overlay_ok=true
fi
antigravity_home_overlay_ok=false
antigravity_metadata_path="$WORKSPACE_HOME/.gemini/antigravity/.hg-antigravity-sync.json"
antigravity_total_mcp_servers=0
antigravity_root_shared_servers=0
antigravity_managed_workflows=0
antigravity_repo_workflows=0
antigravity_skill_workflows=0
antigravity_workspace_workflows=0
antigravity_global_workflows=0
antigravity_workspace_skills=0
antigravity_global_skills=0
antigravity_mcp_name_collisions=0
antigravity_workflow_name_collisions=0
antigravity_imported_env_vars="none"
antigravity_missing_env_vars="OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY, GEMINI_API_KEY"
antigravity_env_bridge_mode="unknown"
antigravity_global_gemini_md=false
antigravity_ecosystem_metadata_path="$WORKSPACE_HOME/.gemini/antigravity/.hg-antigravity-ecosystem.json"
antigravity_archived_skill_sources=0
antigravity_archived_workflow_sources=0
antigravity_installed_extensions="none"
antigravity_installed_extension_count=0
antigravity_sidecar_count=0
if [[ -x "$ROOT/dotfiles/scripts/hg-antigravity-sync.sh" ]] && env HOME="$WORKSPACE_HOME" HG_WORKSPACE_OWNER="$WORKSPACE_OWNER" HG_WORKSPACE_HOME="$WORKSPACE_HOME" "$ROOT/dotfiles/scripts/hg-antigravity-sync.sh" --root "$ROOT" --check >/dev/null 2>&1; then
  antigravity_home_overlay_ok=true
fi
if [[ -f "$antigravity_metadata_path" ]]; then
  antigravity_total_mcp_servers=$(jq -r '.total_mcp_server_count // 0' "$antigravity_metadata_path")
  antigravity_root_shared_servers=$(jq -r '.root_shared_server_count // 0' "$antigravity_metadata_path")
  antigravity_managed_workflows=$(jq -r '.managed_workflow_count // 0' "$antigravity_metadata_path")
  antigravity_repo_workflows=$(jq -r '.repo_workflow_count // 0' "$antigravity_metadata_path")
  antigravity_skill_workflows=$(jq -r '.skill_workflow_count // 0' "$antigravity_metadata_path")
  antigravity_workspace_workflows=$(jq -r '.workspace_workflow_count // .managed_workflow_count // 0' "$antigravity_metadata_path")
  antigravity_global_workflows=$(jq -r '.global_workflow_count // 0' "$antigravity_metadata_path")
  antigravity_workspace_skills=$(jq -r '.workspace_skill_count // 0' "$antigravity_metadata_path")
  antigravity_global_skills=$(jq -r '.global_skill_count // 0' "$antigravity_metadata_path")
  antigravity_mcp_name_collisions=$(jq -r '.mcp_name_collision_count // 0' "$antigravity_metadata_path")
  antigravity_workflow_name_collisions=$(jq -r '.workflow_name_collision_count // 0' "$antigravity_metadata_path")
  antigravity_imported_env_vars=$(jq -r '(.imported_env_vars // []) | if length == 0 then "none" else join(", ") end' "$antigravity_metadata_path")
  antigravity_missing_env_vars=$(jq -r '(.missing_env_vars // []) | if length == 0 then "none" else join(", ") end' "$antigravity_metadata_path")
  antigravity_env_bridge_mode=$(jq -r '.env_bridge_mode // "unknown"' "$antigravity_metadata_path")
  antigravity_global_gemini_md=$(jq -r '.global_gemini_md_present // false' "$antigravity_metadata_path")
fi
if [[ -f "$antigravity_ecosystem_metadata_path" ]]; then
  antigravity_archived_skill_sources=$(jq -r '.archived_skill_source_count // 0' "$antigravity_ecosystem_metadata_path")
  antigravity_archived_workflow_sources=$(jq -r '.archived_workflow_source_count // 0' "$antigravity_ecosystem_metadata_path")
  antigravity_installed_extensions=$(jq -r '(.installed_extensions // []) | if length == 0 then "none" else join(", ") end' "$antigravity_ecosystem_metadata_path")
  antigravity_installed_extension_count=$(jq -r '.installed_extension_count // 0' "$antigravity_ecosystem_metadata_path")
  antigravity_sidecar_count=$(jq -r '.sidecar_count // 0' "$antigravity_ecosystem_metadata_path")
fi

cat <<EOF
Agent parity audit root: $ROOT
Repos scanned: ${scanned_repos}
claude mcp matches: $total_claude_mcp
.claude/settings.json matches: $total_claude_settings
claude_desktop_config.json matches: $total_claude_desktop
repos missing AGENTS.md: $total_missing_agents
repos missing root .claude/settings.json: $total_missing_root_claude_settings
repos missing root .gemini/settings.json: $total_missing_root_gemini_settings
legacy .gemini/config.yaml files: $total_legacy_gemini_config_files
generated .gemini/settings.json files: $total_generated_gemini_settings
Gemini MCP server entries: $total_gemini_mcp_servers
Gemini translated hook rules: $total_gemini_translated_hook_rules
Claude-only hook gaps: $total_claude_only_hook_gaps
repos missing GEMINI.md: $total_missing_gemini
repos missing .github/copilot-instructions.md: $total_missing_copilot
repos missing .codex/config.toml: $total_missing_codex
repos missing .codex-plugin/plugin.json: $total_missing_plugins
repos missing .agents/skills/surface.yaml: $total_missing_skill_surfaces
repos with full Codex profile packs: $total_with_full_profiles
repos with canonical .agents/skills: $total_with_canonical_skills
repos with generated .claude/skills: $total_with_generated_claude_skills
repos with generated plugin skills: $total_with_generated_plugin_skills
repo-local .mcp.json files with real servers: $total_mcp_json
repos with .mcp.json: $total_repos_with_mcp_json
repos with example-only .mcp.json: $total_repos_with_example_only_mcp_json
repo MCP server entries: $total_repo_mcp_servers
Codex MCP server blocks: $total_codex_mcp_servers
curated Codex MCP server blocks: $total_curated_codex_mcp_servers
raw Codex MCP server blocks: $total_raw_codex_mcp_servers
unmanaged Codex MCP server blocks: $total_unmanaged_codex_mcp_servers
repos with repo-local Codex MCP servers: $total_repos_with_codex_mcp_servers
repos with curated Codex MCP servers: $total_repos_with_curated_codex_mcp_servers
repos with raw Codex MCP servers: $total_repos_with_raw_codex_mcp_servers
repos with MCP policy files: $total_repos_with_policy_managed_mcp
repos with generated Codex MCP configs: $total_repos_with_generated_codex_mcp
repos with unmanaged Codex MCP blocks: $total_repos_with_unmanaged_codex_mcp
repos with MCP discovery contract: $total_repos_with_mcp_discovery_contract
repos with MCP resource contract: $total_repos_with_mcp_resource_contract
repos with MCP prompt contract: $total_repos_with_mcp_prompt_contract
repos with MCP server health tool: $total_repos_with_mcp_server_health
repos with full MCP server contract: $total_repos_with_full_mcp_contract
legacy gpt-5.4-xhigh token matches: $total_legacy_model_tokens
repos with legacy gpt-5.4-xhigh tokens: $total_repos_with_legacy_model_tokens
repos with .mcp.json but no MCP policy file: $total_repos_with_mcp_without_policy
repos with .mcp.json but no curated Codex MCP servers: $total_repos_with_mcp_without_curated_codex
repos with .codex/agents/*.toml: $total_with_codex_agents
repos with Codex workflows: $total_with_codex_workflows
repos with AGENTS.override.md: $total_with_agents_override
repos with codex_hooks enabled: $total_with_codex_hooks
repos with Gemini extension scaffolds: $total_repos_with_gemini_extensions
repos with provider MCP bridge: $total_repos_with_provider_mcp_bridge
repos with provider hook bridge: $total_repos_with_provider_hook_bridge
repos with provider drift: $total_repos_with_provider_drift
active-scope repos: $total_active_scope_repos
active operator repos: $total_active_operator_repos
active first-party repos: $total_active_first_party_repos
excluded repos: $total_excluded_repos
active repos missing AGENTS.md: $total_active_missing_agents
active repos missing root .claude/settings.json: $total_active_missing_root_claude_settings
active repos missing root .gemini/settings.json: $total_active_missing_root_gemini_settings
active repos missing GEMINI.md: $total_active_missing_gemini
active repos missing .github/copilot-instructions.md: $total_active_missing_copilot
active repos missing .codex/config.toml: $total_active_missing_codex
active operator repos missing full profile packs: $total_active_missing_full_profiles
active operator repos missing .codex/agents/*.toml: $total_active_missing_codex_agents
active repos missing expected hosted Codex workflows: $total_active_missing_codex_workflows
active repos missing expected Codex plugins: $total_active_missing_codex_plugins
active repos missing expected codex_hooks: $total_active_missing_codex_hooks
active MCP repos missing full contract: $total_active_mcp_repos_missing_full_contract
active MCP repos missing provider bridge: $total_active_mcp_repos_missing_provider_bridge
active repos missing expected provider hook bridge: $total_active_repos_missing_provider_hook_bridge
workflow governance clean repos: $total_workflow_clean
workflow governance repo-owned drift repos: $total_workflow_repo_owned_drift
workflow governance retired residue repos: $total_workflow_retired_residue
workflow governance unexpected workflow repos: $total_workflow_unexpected_workflow
workflow governance missing owned workflow repos: $total_workflow_missing_owned_workflow
workflow family summary:${workflow_family_summary_text}
repos with legacy .claude/commands: $total_repos_with_legacy_claude_commands
total legacy .claude/commands files: $total_legacy_claude_command_count
repos with unported legacy commands (no surface.yaml): $total_repos_with_unported_legacy_commands
workspace owner: $WORKSPACE_OWNER
workspace home: $WORKSPACE_HOME
workspace-owner skills in ~/.claude/commands: $global_claude_commands
workspace-owner skills in ~/.claude/skills: $global_claude_skills
workspace-owner skills in ~/.agents/skills: $global_agents_skills
workspace-owner skills in ~/.codex/skills: $global_codex_skills
workspace global source contract up to date: $workspace_global_sync_ok
workspace global home overlay up to date: $workspace_global_home_overlay_ok
Antigravity home overlay up to date: $antigravity_home_overlay_ok
Antigravity MCP servers: $antigravity_total_mcp_servers ($antigravity_root_shared_servers root shared)
Antigravity managed workflows: $antigravity_managed_workflows ($antigravity_repo_workflows repo + $antigravity_skill_workflows skill, $antigravity_global_workflows global)
Antigravity workspace skills: $antigravity_workspace_skills
Antigravity global skills: $antigravity_global_skills
Antigravity imported env vars: $antigravity_imported_env_vars
Antigravity missing env vars: $antigravity_missing_env_vars
Antigravity env bridge mode: $antigravity_env_bridge_mode
Antigravity global GEMINI.md present: $antigravity_global_gemini_md
Antigravity archived sources: skills=$antigravity_archived_skill_sources workflows=$antigravity_archived_workflow_sources
Antigravity installed extensions: $antigravity_installed_extensions
Antigravity sidecar tools provisioned: $antigravity_sidecar_count
Antigravity naming collisions: mcp=$antigravity_mcp_name_collisions workflows=$antigravity_workflow_name_collisions
EOF

inventory_json="{
  \"generated_on\": \"$(date +%Y-%m-%d)\",
  \"root\": \"${ROOT}\",
  \"summary\": {
    \"repos_scanned\": ${scanned_repos},
    \"claude_mcp_matches\": ${total_claude_mcp},
    \"claude_settings_matches\": ${total_claude_settings},
    \"claude_desktop_config_matches\": ${total_claude_desktop},
    \"repos_missing_agents_md\": ${total_missing_agents},
    \"repos_missing_root_claude_settings\": ${total_missing_root_claude_settings},
    \"repos_missing_root_gemini_settings\": ${total_missing_root_gemini_settings},
    \"legacy_gemini_config_files\": ${total_legacy_gemini_config_files},
    \"generated_gemini_settings_files\": ${total_generated_gemini_settings},
    \"gemini_mcp_server_entries\": ${total_gemini_mcp_servers},
    \"gemini_translated_hook_rules\": ${total_gemini_translated_hook_rules},
    \"claude_only_hook_gaps\": ${total_claude_only_hook_gaps},
    \"repos_missing_gemini_md\": ${total_missing_gemini},
    \"repos_missing_copilot_instructions\": ${total_missing_copilot},
    \"repos_missing_codex_config\": ${total_missing_codex},
    \"repos_missing_codex_plugin\": ${total_missing_plugins},
    \"repos_missing_skill_surface_manifest\": ${total_missing_skill_surfaces},
    \"repos_with_full_profile_pack\": ${total_with_full_profiles},
    \"repos_with_canonical_skills\": ${total_with_canonical_skills},
    \"repos_with_generated_claude_skills\": ${total_with_generated_claude_skills},
    \"repos_with_generated_plugin_skills\": ${total_with_generated_plugin_skills},
    \"mcp_json_files\": ${total_mcp_json},
    \"repos_with_mcp_json\": ${total_repos_with_mcp_json},
    \"repos_with_example_only_mcp_json\": ${total_repos_with_example_only_mcp_json},
    \"repo_mcp_server_entries\": ${total_repo_mcp_servers},
    \"codex_mcp_server_blocks\": ${total_codex_mcp_servers},
    \"curated_codex_mcp_server_blocks\": ${total_curated_codex_mcp_servers},
    \"raw_codex_mcp_server_blocks\": ${total_raw_codex_mcp_servers},
    \"unmanaged_codex_mcp_server_blocks\": ${total_unmanaged_codex_mcp_servers},
    \"repos_with_codex_mcp_servers\": ${total_repos_with_codex_mcp_servers},
    \"repos_with_curated_codex_mcp_servers\": ${total_repos_with_curated_codex_mcp_servers},
    \"repos_with_raw_codex_mcp_servers\": ${total_repos_with_raw_codex_mcp_servers},
    \"repos_with_policy_managed_mcp\": ${total_repos_with_policy_managed_mcp},
    \"repos_with_generated_codex_mcp\": ${total_repos_with_generated_codex_mcp},
    \"repos_with_unmanaged_codex_mcp\": ${total_repos_with_unmanaged_codex_mcp},
    \"repos_with_mcp_discovery_contract\": ${total_repos_with_mcp_discovery_contract},
    \"repos_with_mcp_resource_contract\": ${total_repos_with_mcp_resource_contract},
    \"repos_with_mcp_prompt_contract\": ${total_repos_with_mcp_prompt_contract},
    \"repos_with_mcp_server_health\": ${total_repos_with_mcp_server_health},
    \"repos_with_full_mcp_contract\": ${total_repos_with_full_mcp_contract},
    \"legacy_codex_model_token_matches\": ${total_legacy_model_tokens},
    \"repos_with_legacy_codex_model_tokens\": ${total_repos_with_legacy_model_tokens},
    \"repos_with_mcp_without_policy\": ${total_repos_with_mcp_without_policy},
    \"repos_with_mcp_without_curated_codex\": ${total_repos_with_mcp_without_curated_codex},
    \"repos_with_codex_agents\": ${total_with_codex_agents},
    \"repos_with_codex_workflows\": ${total_with_codex_workflows},
    \"repos_with_agents_override_md\": ${total_with_agents_override},
    \"repos_with_codex_hooks_enabled\": ${total_with_codex_hooks},
    \"repos_with_gemini_extensions\": ${total_repos_with_gemini_extensions},
    \"repos_with_provider_mcp_bridge\": ${total_repos_with_provider_mcp_bridge},
    \"repos_with_provider_hook_bridge\": ${total_repos_with_provider_hook_bridge},
    \"repos_with_provider_drift\": ${total_repos_with_provider_drift},
    \"total_repos_with_ollama_support\": ${total_repos_with_ollama_support},
    \"total_repos_with_ollama_session_provider\": ${total_repos_with_ollama_session_provider},
    \"total_repos_with_ollama_docs_only\": ${total_repos_with_ollama_docs_only},
    \"total_repos_with_provider_runtime_drift\": ${total_repos_with_provider_runtime_drift},
    \"total_provider_runtime_drift_count\": ${total_provider_runtime_drift_count},
    \"active_scope_repos\": ${total_active_scope_repos},
    \"active_operator_repos\": ${total_active_operator_repos},
    \"active_first_party_repos\": ${total_active_first_party_repos},
    \"excluded_repos\": ${total_excluded_repos},
    \"active_repos_missing_agents_md\": ${total_active_missing_agents},
    \"active_repos_missing_root_claude_settings\": ${total_active_missing_root_claude_settings},
    \"active_repos_missing_root_gemini_settings\": ${total_active_missing_root_gemini_settings},
    \"active_repos_missing_gemini_md\": ${total_active_missing_gemini},
    \"active_repos_missing_copilot_instructions\": ${total_active_missing_copilot},
    \"active_repos_missing_codex_config\": ${total_active_missing_codex},
    \"active_operator_repos_missing_full_profile_pack\": ${total_active_missing_full_profiles},
    \"active_operator_repos_missing_codex_agents\": ${total_active_missing_codex_agents},
    \"active_operator_repos_missing_codex_workflows\": ${total_active_missing_codex_workflows},
    \"active_repos_missing_expected_codex_plugin\": ${total_active_missing_codex_plugins},
    \"active_repos_missing_expected_codex_hooks\": ${total_active_missing_codex_hooks},
    \"active_mcp_repos\": ${total_active_mcp_repos},
    \"active_mcp_repos_missing_full_contract\": ${total_active_mcp_repos_missing_full_contract},
    \"active_mcp_repos_missing_provider_bridge\": ${total_active_mcp_repos_missing_provider_bridge},
    \"active_repos_missing_provider_hook_bridge\": ${total_active_repos_missing_provider_hook_bridge},
    \"workflow_governance_clean\": ${total_workflow_clean},
    \"workflow_governance_repo_owned_drift\": ${total_workflow_repo_owned_drift},
    \"workflow_governance_retired_residue\": ${total_workflow_retired_residue},
    \"workflow_governance_unexpected_workflow\": ${total_workflow_unexpected_workflow},
    \"workflow_governance_missing_owned_workflow\": ${total_workflow_missing_owned_workflow},
    \"repos_with_legacy_claude_commands\": ${total_repos_with_legacy_claude_commands},
    \"total_legacy_claude_command_count\": ${total_legacy_claude_command_count},
    \"repos_with_unported_legacy_commands\": ${total_repos_with_unported_legacy_commands},
    \"workspace_owner\": $(jq -cn --arg v "$WORKSPACE_OWNER" '$v'),
    \"workspace_home\": $(jq -cn --arg v "$WORKSPACE_HOME" '$v'),
    \"global_claude_commands\": ${global_claude_commands},
    \"global_claude_skills\": ${global_claude_skills},
    \"global_agents_skills\": ${global_agents_skills},
    \"global_codex_skills\": ${global_codex_skills},
    \"workspace_global_sync_up_to_date\": ${workspace_global_sync_ok},
    \"workspace_global_home_overlay_up_to_date\": ${workspace_global_home_overlay_ok},
    \"antigravity_home_overlay_up_to_date\": ${antigravity_home_overlay_ok},
    \"antigravity_total_mcp_servers\": ${antigravity_total_mcp_servers},
    \"antigravity_root_shared_servers\": ${antigravity_root_shared_servers},
    \"antigravity_managed_workflows\": ${antigravity_managed_workflows},
    \"antigravity_repo_workflows\": ${antigravity_repo_workflows},
    \"antigravity_skill_workflows\": ${antigravity_skill_workflows},
    \"antigravity_workspace_workflows\": ${antigravity_workspace_workflows},
    \"antigravity_global_workflows\": ${antigravity_global_workflows},
    \"antigravity_workspace_skills\": ${antigravity_workspace_skills},
    \"antigravity_global_skills\": ${antigravity_global_skills},
    \"antigravity_mcp_name_collisions\": ${antigravity_mcp_name_collisions},
    \"antigravity_workflow_name_collisions\": ${antigravity_workflow_name_collisions},
    \"antigravity_imported_env_vars\": $(jq -cn --arg v "$antigravity_imported_env_vars" '$v'),
    \"antigravity_missing_env_vars\": $(jq -cn --arg v "$antigravity_missing_env_vars" '$v'),
    \"antigravity_env_bridge_mode\": $(jq -cn --arg v "$antigravity_env_bridge_mode" '$v'),
    \"antigravity_global_gemini_md_present\": ${antigravity_global_gemini_md},
    \"antigravity_archived_skill_sources\": ${antigravity_archived_skill_sources},
    \"antigravity_archived_workflow_sources\": ${antigravity_archived_workflow_sources},
    \"antigravity_installed_extension_count\": ${antigravity_installed_extension_count},
    \"antigravity_installed_extensions\": $(jq -cn --arg v "$antigravity_installed_extensions" '$v'),
    \"antigravity_sidecar_count\": ${antigravity_sidecar_count}
  },
  \"workflow_families\": [${workflow_family_summary_json}
  ],
  \"repos\": [
${inventory_json_rows}
  ]
}"

write_workspace_cache() {
  local docs_dir
  docs_dir="$(workspace_cache_dir)"
  mkdir -p "$docs_dir"

  cat >"$docs_dir/repo-inventory.csv" <<EOF
repo,claude_mcp_mentions,claude_settings_mentions,claude_desktop_config_mentions,agents_md_count,agents_override_md_count,claude_md_count,gemini_md_count,copilot_instructions_count,codex_config_count,codex_full_profile_pack_count,skill_surface_manifest_count,canonical_skill_count,generated_claude_skill_count,generated_plugin_skill_count,mcp_json_count,repo_mcp_server_count,mcp_discovery_contract,mcp_resource_contract,mcp_prompt_contract,mcp_server_health_contract,full_mcp_contract,mcp_policy_count,generated_codex_mcp_config_count,unmanaged_codex_mcp_server_count,example_only_mcp_json,codex_mcp_server_count,codex_curated_mcp_server_count,codex_raw_mcp_server_count,legacy_codex_model_token_count,mcp_without_policy,mcp_without_curated_codex,codex_agent_count,codex_plugin_count,codex_workflow_count,codex_hooks_enabled_count,root_claude_settings,root_gemini_settings,legacy_gemini_config_count,generated_gemini_settings_count,gemini_mcp_server_count,gemini_translated_hook_rule_count,claude_only_hook_gap_count,gemini_extension_count,provider_mcp_bridge,provider_hook_bridge,provider_drift_count,ollama_support_mode,ollama_profile_source,provider_runtime_drift_count,scope,active_scope,expected_codex_baseline,expected_full_profile_pack,expected_codex_agents,expected_codex_workflows,expected_codex_plugin,expected_mcp_contract,expected_provider_hook_bridge,expected_codex_hooks
${inventory_csv%$'\n'}
EOF

  cat >"$docs_dir/repo-inventory.md" <<EOF
# Repo Inventory

Generated by \`codexkit/scripts/agent-parity-audit.sh --write-workspace-cache\` on $(date +%Y-%m-%d).

Summary from the latest audit:

- Repos scanned: ${scanned_repos}
- \`claude mcp\` matches: ${total_claude_mcp}
- \`.claude/settings.json\` matches: ${total_claude_settings}
- \`claude_desktop_config.json\` matches: ${total_claude_desktop}
- Repos missing \`AGENTS.md\`: ${total_missing_agents}
- Repos missing root \`.claude/settings.json\`: ${total_missing_root_claude_settings}
- Repos missing root \`.gemini/settings.json\`: ${total_missing_root_gemini_settings}
- Legacy \`.gemini/config.yaml\` files: ${total_legacy_gemini_config_files}
- Generated \`.gemini/settings.json\` files: ${total_generated_gemini_settings}
- Gemini MCP server entries: ${total_gemini_mcp_servers}
- Gemini translated hook rules: ${total_gemini_translated_hook_rules}
- Claude-only hook gaps: ${total_claude_only_hook_gaps}
- Repos missing \`GEMINI.md\`: ${total_missing_gemini}
- Repos missing \`.github/copilot-instructions.md\`: ${total_missing_copilot}
- Repos missing \`.codex/config.toml\`: ${total_missing_codex}
- Repos missing \`.codex-plugin/plugin.json\`: ${total_missing_plugins}
- Repos missing \`.agents/skills/surface.yaml\`: ${total_missing_skill_surfaces}
- Repos with full Codex profile packs: ${total_with_full_profiles}
- Repos with canonical \`.agents/skills\`: ${total_with_canonical_skills}
- Repos with generated \`.claude/skills\`: ${total_with_generated_claude_skills}
- Repos with generated plugin skills: ${total_with_generated_plugin_skills}
- Repo-local \`.mcp.json\` files with real servers: ${total_mcp_json}
- Repos with \`.mcp.json\`: ${total_repos_with_mcp_json}
- Repos with example-only \`.mcp.json\`: ${total_repos_with_example_only_mcp_json}
- Repo MCP server entries: ${total_repo_mcp_servers}
- Codex MCP server blocks: ${total_codex_mcp_servers}
- Curated Codex MCP server blocks: ${total_curated_codex_mcp_servers}
- Raw Codex MCP server blocks: ${total_raw_codex_mcp_servers}
- Unmanaged Codex MCP server blocks: ${total_unmanaged_codex_mcp_servers}
- Repos with repo-local Codex MCP servers: ${total_repos_with_codex_mcp_servers}
- Repos with curated Codex MCP servers: ${total_repos_with_curated_codex_mcp_servers}
- Repos with raw Codex MCP servers: ${total_repos_with_raw_codex_mcp_servers}
- Repos with MCP policy files: ${total_repos_with_policy_managed_mcp}
- Repos with generated Codex MCP configs: ${total_repos_with_generated_codex_mcp}
- Repos with unmanaged Codex MCP blocks: ${total_repos_with_unmanaged_codex_mcp}
- Repos with MCP discovery contract: ${total_repos_with_mcp_discovery_contract}
- Repos with MCP resource contract: ${total_repos_with_mcp_resource_contract}
- Repos with MCP prompt contract: ${total_repos_with_mcp_prompt_contract}
- Repos with MCP server health tool: ${total_repos_with_mcp_server_health}
- Repos with full MCP server contract: ${total_repos_with_full_mcp_contract}
- Legacy \`gpt-5.4-xhigh\` token matches: ${total_legacy_model_tokens}
- Repos with legacy \`gpt-5.4-xhigh\` tokens: ${total_repos_with_legacy_model_tokens}
- Repos with \`.mcp.json\` but no MCP policy file: ${total_repos_with_mcp_without_policy}
- Repos with \`.mcp.json\` but no curated Codex MCP servers: ${total_repos_with_mcp_without_curated_codex}
- Repos with \`.codex/agents/*.toml\`: ${total_with_codex_agents}
- Repos with hosted Codex workflows (legacy): ${total_with_codex_workflows}
- Repos with \`AGENTS.override.md\`: ${total_with_agents_override}
- Repos with \`codex_hooks = true\`: ${total_with_codex_hooks}
- Repos with Gemini extension scaffolds: ${total_repos_with_gemini_extensions}
- Repos with provider MCP bridge: ${total_repos_with_provider_mcp_bridge}
- Repos with provider hook bridge: ${total_repos_with_provider_hook_bridge}
- Repos with provider drift: ${total_repos_with_provider_drift}
- Repos with any Ollama support: ${total_repos_with_ollama_support}
- Repos with Ollama session-provider support: ${total_repos_with_ollama_session_provider}
- Repos with docs-only Ollama support: ${total_repos_with_ollama_docs_only}
- Repos with provider-runtime drift: ${total_repos_with_provider_runtime_drift} (${total_provider_runtime_drift_count} total drift points)
- Active-scope repos: ${total_active_scope_repos}
- Active operator repos: ${total_active_operator_repos}
- Active first-party repos: ${total_active_first_party_repos}
- Excluded repos: ${total_excluded_repos}
- Active repos missing \`AGENTS.md\`: ${total_active_missing_agents}
- Active repos missing root \`.claude/settings.json\`: ${total_active_missing_root_claude_settings}
- Active repos missing root \`.gemini/settings.json\`: ${total_active_missing_root_gemini_settings}
- Active repos missing \`GEMINI.md\`: ${total_active_missing_gemini}
- Active repos missing \`.github/copilot-instructions.md\`: ${total_active_missing_copilot}
- Active repos missing \`.codex/config.toml\`: ${total_active_missing_codex}
- Active operator repos missing full Codex profile packs: ${total_active_missing_full_profiles}
- Active operator repos missing \`.codex/agents/*.toml\`: ${total_active_missing_codex_agents}
- Active repos missing expected hosted Codex workflows: ${total_active_missing_codex_workflows}
- Active repos missing expected Codex plugins: ${total_active_missing_codex_plugins}
- Active repos missing expected \`codex_hooks = true\`: ${total_active_missing_codex_hooks}
- Active MCP repos missing the full contract: ${total_active_mcp_repos_missing_full_contract}
- Active MCP repos missing provider bridge: ${total_active_mcp_repos_missing_provider_bridge}
- Active repos missing expected provider hook bridge: ${total_active_repos_missing_provider_hook_bridge}
- Workflow governance clean repos: ${total_workflow_clean}
- Workflow governance repo-owned drift repos: ${total_workflow_repo_owned_drift}
- Workflow governance retired residue repos: ${total_workflow_retired_residue}
- Workflow governance unexpected workflow repos: ${total_workflow_unexpected_workflow}
- Workflow governance missing owned workflow repos: ${total_workflow_missing_owned_workflow}
- Repos with legacy \`.claude/commands\`: ${total_repos_with_legacy_claude_commands} (${total_legacy_claude_command_count} files)
- Repos with unported legacy commands (no surface.yaml): ${total_repos_with_unported_legacy_commands}
- Workspace owner: \`${WORKSPACE_OWNER}\`
- Workspace home: \`${WORKSPACE_HOME}\`
- Workspace-owner skills: ${global_claude_commands} commands / ${global_claude_skills} Claude skills / ${global_agents_skills} Agents / ${global_codex_skills} Codex
- Workspace global source contract up to date: ${workspace_global_sync_ok}
- Workspace global home overlay up to date: ${workspace_global_home_overlay_ok}
- Antigravity home overlay up to date: ${antigravity_home_overlay_ok}
- Antigravity MCP servers: ${antigravity_total_mcp_servers} (${antigravity_root_shared_servers} root shared)
- Antigravity managed workflows: ${antigravity_managed_workflows} (${antigravity_repo_workflows} repo + ${antigravity_skill_workflows} skill, ${antigravity_global_workflows} global)
- Antigravity workspace skills: ${antigravity_workspace_skills}
- Antigravity global skills: ${antigravity_global_skills}
- Antigravity imported env vars: ${antigravity_imported_env_vars}
- Antigravity missing env vars: ${antigravity_missing_env_vars}
- Antigravity env bridge mode: ${antigravity_env_bridge_mode}
- Antigravity global GEMINI.md present: ${antigravity_global_gemini_md}
- Antigravity archived sources: skills=${antigravity_archived_skill_sources}, workflows=${antigravity_archived_workflow_sources}
- Antigravity installed extensions: ${antigravity_installed_extensions}
- Antigravity sidecar tools provisioned: ${antigravity_sidecar_count}
- Antigravity naming collisions: mcp=${antigravity_mcp_name_collisions}, workflows=${antigravity_workflow_name_collisions}

## Workflow governance families
${workflow_family_summary_md}

${inventory_md}
EOF
}

write_wiki_docs() {
  local docs_dir
  docs_dir="$(wiki_docs_dir)"
  mkdir -p "$docs_dir"

  cat >"$docs_dir/repo-inventory.csv" <<EOF
repo,claude_mcp_mentions,claude_settings_mentions,claude_desktop_config_mentions,agents_md_count,agents_override_md_count,claude_md_count,gemini_md_count,copilot_instructions_count,codex_config_count,codex_full_profile_pack_count,skill_surface_manifest_count,canonical_skill_count,generated_claude_skill_count,generated_plugin_skill_count,mcp_json_count,repo_mcp_server_count,mcp_discovery_contract,mcp_resource_contract,mcp_prompt_contract,mcp_server_health_contract,full_mcp_contract,mcp_policy_count,generated_codex_mcp_config_count,unmanaged_codex_mcp_server_count,example_only_mcp_json,codex_mcp_server_count,codex_curated_mcp_server_count,codex_raw_mcp_server_count,legacy_codex_model_token_count,mcp_without_policy,mcp_without_curated_codex,codex_agent_count,codex_plugin_count,codex_workflow_count,codex_hooks_enabled_count,root_claude_settings,root_gemini_settings,legacy_gemini_config_count,generated_gemini_settings_count,gemini_mcp_server_count,gemini_translated_hook_rule_count,claude_only_hook_gap_count,gemini_extension_count,provider_mcp_bridge,provider_hook_bridge,provider_drift_count,ollama_support_mode,ollama_profile_source,provider_runtime_drift_count,scope,active_scope,expected_codex_baseline,expected_full_profile_pack,expected_codex_agents,expected_codex_workflows,expected_codex_plugin,expected_mcp_contract,expected_provider_hook_bridge,expected_codex_hooks
${inventory_csv%$'\n'}
EOF

  cat >"$docs_dir/repo-inventory.md" <<EOF
# Repo Inventory

Generated by \`codexkit/scripts/agent-parity-audit.sh --write-wiki-docs\` on $(date +%Y-%m-%d).

Summary from the latest audit:

- Repos scanned: ${scanned_repos}
- \`claude mcp\` matches: ${total_claude_mcp}
- \`.claude/settings.json\` matches: ${total_claude_settings}
- \`claude_desktop_config.json\` matches: ${total_claude_desktop}
- Repos missing \`AGENTS.md\`: ${total_missing_agents}
- Repos missing root \`.claude/settings.json\`: ${total_missing_root_claude_settings}
- Repos missing root \`.gemini/settings.json\`: ${total_missing_root_gemini_settings}
- Legacy \`.gemini/config.yaml\` files: ${total_legacy_gemini_config_files}
- Generated \`.gemini/settings.json\` files: ${total_generated_gemini_settings}
- Gemini MCP server entries: ${total_gemini_mcp_servers}
- Gemini translated hook rules: ${total_gemini_translated_hook_rules}
- Claude-only hook gaps: ${total_claude_only_hook_gaps}
- Repos missing \`GEMINI.md\`: ${total_missing_gemini}
- Repos missing \`.github/copilot-instructions.md\`: ${total_missing_copilot}
- Repos missing \`.codex/config.toml\`: ${total_missing_codex}
- Repos missing \`.codex-plugin/plugin.json\`: ${total_missing_plugins}
- Repos with full Codex profile packs: ${total_with_full_profiles}
- Repo-local \`.mcp.json\` files with real servers: ${total_mcp_json}
- Repos with \`.mcp.json\`: ${total_repos_with_mcp_json}
- Repos with example-only \`.mcp.json\`: ${total_repos_with_example_only_mcp_json}
- Repo MCP server entries: ${total_repo_mcp_servers}
- Codex MCP server blocks: ${total_codex_mcp_servers}
- Curated Codex MCP server blocks: ${total_curated_codex_mcp_servers}
- Raw Codex MCP server blocks: ${total_raw_codex_mcp_servers}
- Unmanaged Codex MCP server blocks: ${total_unmanaged_codex_mcp_servers}
- Repos with repo-local Codex MCP servers: ${total_repos_with_codex_mcp_servers}
- Repos with curated Codex MCP servers: ${total_repos_with_curated_codex_mcp_servers}
- Repos with raw Codex MCP servers: ${total_repos_with_raw_codex_mcp_servers}
- Repos with MCP policy files: ${total_repos_with_policy_managed_mcp}
- Repos with generated Codex MCP configs: ${total_repos_with_generated_codex_mcp}
- Repos with unmanaged Codex MCP blocks: ${total_repos_with_unmanaged_codex_mcp}
- Repos with MCP discovery contract: ${total_repos_with_mcp_discovery_contract}
- Repos with MCP resource contract: ${total_repos_with_mcp_resource_contract}
- Repos with MCP prompt contract: ${total_repos_with_mcp_prompt_contract}
- Repos with MCP server health tool: ${total_repos_with_mcp_server_health}
- Repos with full MCP server contract: ${total_repos_with_full_mcp_contract}
- Legacy \`gpt-5.4-xhigh\` token matches: ${total_legacy_model_tokens}
- Repos with legacy \`gpt-5.4-xhigh\` tokens: ${total_repos_with_legacy_model_tokens}
- Repos with \`.mcp.json\` but no MCP policy file: ${total_repos_with_mcp_without_policy}
- Repos with \`.mcp.json\` but no curated Codex MCP servers: ${total_repos_with_mcp_without_curated_codex}
- Repos with \`.codex/agents/*.toml\`: ${total_with_codex_agents}
- Repos with hosted Codex workflows (legacy): ${total_with_codex_workflows}
- Repos with \`AGENTS.override.md\`: ${total_with_agents_override}
- Repos with \`codex_hooks = true\`: ${total_with_codex_hooks}
- Repos with Gemini extension scaffolds: ${total_repos_with_gemini_extensions}
- Repos with provider MCP bridge: ${total_repos_with_provider_mcp_bridge}
- Repos with provider hook bridge: ${total_repos_with_provider_hook_bridge}
- Repos with provider drift: ${total_repos_with_provider_drift}
- Repos with any Ollama support: ${total_repos_with_ollama_support}
- Repos with Ollama session-provider support: ${total_repos_with_ollama_session_provider}
- Repos with docs-only Ollama support: ${total_repos_with_ollama_docs_only}
- Repos with provider-runtime drift: ${total_repos_with_provider_runtime_drift} (${total_provider_runtime_drift_count} total drift points)
- Active-scope repos: ${total_active_scope_repos}
- Active operator repos: ${total_active_operator_repos}
- Active first-party repos: ${total_active_first_party_repos}
- Excluded repos: ${total_excluded_repos}
- Active repos missing \`AGENTS.md\`: ${total_active_missing_agents}
- Active repos missing root \`.claude/settings.json\`: ${total_active_missing_root_claude_settings}
- Active repos missing root \`.gemini/settings.json\`: ${total_active_missing_root_gemini_settings}
- Active repos missing \`GEMINI.md\`: ${total_active_missing_gemini}
- Active repos missing \`.github/copilot-instructions.md\`: ${total_active_missing_copilot}
- Active repos missing \`.codex/config.toml\`: ${total_active_missing_codex}
- Active operator repos missing full Codex profile packs: ${total_active_missing_full_profiles}
- Active operator repos missing \`.codex/agents/*.toml\`: ${total_active_missing_codex_agents}
- Active repos missing expected hosted Codex workflows: ${total_active_missing_codex_workflows}
- Active repos missing expected Codex plugins: ${total_active_missing_codex_plugins}
- Active repos missing expected \`codex_hooks = true\`: ${total_active_missing_codex_hooks}
- Active MCP repos missing the full contract: ${total_active_mcp_repos_missing_full_contract}
- Active MCP repos missing provider bridge: ${total_active_mcp_repos_missing_provider_bridge}
- Active repos missing expected provider hook bridge: ${total_active_repos_missing_provider_hook_bridge}
- Workflow governance clean repos: ${total_workflow_clean}
- Workflow governance repo-owned drift repos: ${total_workflow_repo_owned_drift}
- Workflow governance retired residue repos: ${total_workflow_retired_residue}
- Workflow governance unexpected workflow repos: ${total_workflow_unexpected_workflow}
- Workflow governance missing owned workflow repos: ${total_workflow_missing_owned_workflow}
- Workspace owner: \`${WORKSPACE_OWNER}\`
- Workspace home: \`${WORKSPACE_HOME}\`
- Workspace-owner skills: ${global_claude_commands} commands / ${global_claude_skills} Claude skills / ${global_agents_skills} Agents / ${global_codex_skills} Codex
- Workspace global source contract up to date: ${workspace_global_sync_ok}
- Workspace global home overlay up to date: ${workspace_global_home_overlay_ok}
- Antigravity home overlay up to date: ${antigravity_home_overlay_ok}
- Antigravity MCP servers: ${antigravity_total_mcp_servers} (${antigravity_root_shared_servers} root shared)
- Antigravity managed workflows: ${antigravity_managed_workflows} (${antigravity_repo_workflows} repo + ${antigravity_skill_workflows} skill, ${antigravity_global_workflows} global)
- Antigravity workspace skills: ${antigravity_workspace_skills}
- Antigravity global skills: ${antigravity_global_skills}
- Antigravity imported env vars: ${antigravity_imported_env_vars}
- Antigravity missing env vars: ${antigravity_missing_env_vars}
- Antigravity env bridge mode: ${antigravity_env_bridge_mode}
- Antigravity global GEMINI.md present: ${antigravity_global_gemini_md}
- Antigravity archived sources: skills=${antigravity_archived_skill_sources}, workflows=${antigravity_archived_workflow_sources}
- Antigravity installed extensions: ${antigravity_installed_extensions}
- Antigravity sidecar tools provisioned: ${antigravity_sidecar_count}
- Antigravity naming collisions: mcp=${antigravity_mcp_name_collisions}, workflows=${antigravity_workflow_name_collisions}

## Workflow governance families
${workflow_family_summary_md}

${inventory_md}
EOF
}

write_json_outputs() {
  local workspace_dir wiki_dir
  wiki_dir="$(wiki_docs_dir)"
  mkdir -p "$wiki_dir"
  printf '%s\n' "$inventory_json" >"$wiki_dir/repo-inventory.json"
  if [[ "$WRITE_WORKSPACE_CACHE" -eq 1 ]]; then
    workspace_dir="$(workspace_cache_dir)"
    mkdir -p "$workspace_dir"
    printf '%s\n' "$inventory_json" >"$workspace_dir/repo-inventory.json"
  fi
}

if [[ "$WRITE_WORKSPACE_CACHE" -eq 1 ]]; then
  write_workspace_cache
fi

if [[ "$WRITE_WIKI_DOCS" -eq 1 ]]; then
  write_wiki_docs
fi

if [[ "$WRITE_JSON" -eq 1 ]]; then
  write_json_outputs
fi
