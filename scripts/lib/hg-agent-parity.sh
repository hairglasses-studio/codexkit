#!/usr/bin/env bash
# shellcheck shell=bash
# hg-agent-parity.sh — Shared helpers for provider parity sync and audit.

HG_AGENT_PARITY_LIB_DIR="${HG_AGENT_PARITY_LIB_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"

if ! command -v hg_require >/dev/null 2>&1; then
  source "$HG_AGENT_PARITY_LIB_DIR/hg-core.sh"
fi

HG_AGENT_PARITY_SURFACEKIT_ROOT="${HG_AGENT_PARITY_SURFACEKIT_ROOT:-${HG_AGENT_PARITY_ROOT:-$(cd "$HG_AGENT_PARITY_LIB_DIR/../.." && pwd)}}"

hg_parity_require_tools() {
  hg_require jq diff awk mktemp
}

hg_parity_toml_quote() {
  local s="$1"
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  printf '"%s"' "$s"
}

hg_parity_objectives_path() {
  if [[ -n "${HG_PARITY_OBJECTIVES_PATH:-}" ]]; then
    printf '%s\n' "$HG_PARITY_OBJECTIVES_PATH"
    return 0
  fi

  local docs_root
  docs_root="${DOCS_ROOT:-${HG_DOCS_ROOT:-$HG_STUDIO_ROOT/docs}}"
  local canonical historical
  canonical="$docs_root/projects/agent-parity/parity-objectives.json"
  historical="$docs_root/projects/codex-migration/parity-objectives.json"

  if [[ -f "$canonical" ]]; then
    printf '%s\n' "$canonical"
  else
    printf '%s\n' "$historical"
  fi
}

hg_parity_manifest_path() {
  printf '%s\n' "$HG_STUDIO_ROOT/workspace/manifest.json"
}

hg_parity_repo_scope() {
  local repo_name="$1"
  local manifest
  manifest="$(hg_parity_manifest_path)"
  if [[ ! -f "$manifest" ]]; then
    printf 'active_first_party\n'
    return 0
  fi

  jq -r --arg repo "$repo_name" 'first(.repos[] | select(.name == $repo) | .scope) // "active_first_party"' "$manifest"
}

hg_parity_repo_objective_bool() {
  local repo_name="$1"
  local field="$2"
  local default_value="${3:-false}"
  local objectives
  objectives="$(hg_parity_objectives_path)"
  if [[ ! -f "$objectives" ]]; then
    printf '%s\n' "$default_value"
    return 0
  fi

  local repo_scope
  repo_scope="$(hg_parity_repo_scope "$repo_name")"

  jq -r \
    --arg repo "$repo_name" \
    --arg scope "$repo_scope" \
    --arg field "$field" \
    --argjson default "$default_value" '
      if ((.repo_overrides[$repo] // {}) | has($field)) then
        .repo_overrides[$repo][$field]
      elif ((.scope_defaults[$scope] // {}) | has($field)) then
        .scope_defaults[$scope][$field]
      elif ((.defaults // {}) | has($field)) then
        .defaults[$field]
      else
        $default
      end
    ' "$objectives"
}

hg_parity_object_json() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    printf '{}\n'
    return 0
  fi

  jq -c 'if type == "object" then . else {} end' "$file"
}

hg_parity_active_mcp_servers_json() {
  local repo_path="$1"
  if [[ ! -f "$repo_path/.mcp.json" ]]; then
    printf '{}\n'
    return 0
  fi

  jq -cS '
    (.mcpServers // {})
    | if type != "object" then {} else . end
    | with_entries(select(.key | startswith("_") | not))
    | with_entries(
        if (.value | type) == "object" then
          .value |= with_entries(
            select(
              .key == "command"
              or .key == "args"
              or .key == "env"
              or .key == "cwd"
              or .key == "url"
              or .key == "httpUrl"
              or .key == "headers"
              or .key == "timeout"
            )
          )
        else
          .
        end
      )
  ' "$repo_path/.mcp.json"
}

hg_parity_repo_has_mcp_json() {
  local repo_path="$1"
  [[ -f "$repo_path/.mcp.json" ]] || return 1
  jq -e 'length > 0' <<<"$(hg_parity_active_mcp_servers_json "$repo_path")" >/dev/null 2>&1
}

hg_parity_kebab_case_server_name() {
  local name="$1"
  printf '%s' "$name" \
    | tr '[:upper:]' '[:lower:]' \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g'
}

hg_parity_claude_mcp_servers_json() {
  local repo_path="$1"
  hg_parity_active_mcp_servers_json "$repo_path"
}

hg_parity_gemini_mcp_servers_json() {
  local repo_path="$1"
  jq -cS '
    .
    | with_entries(
        .key |= (
          ascii_downcase
          | gsub("[^a-z0-9]+"; "-")
          | gsub("^-+"; "")
          | gsub("-+$"; "")
          | gsub("-+"; "-")
        )
      )
  ' <<<"$(hg_parity_active_mcp_servers_json "$repo_path")"
}

hg_parity_repo_has_claude_hooks() {
  local repo_path="$1"
  if [[ -d "$repo_path/.claude/hooks" ]] && find "$repo_path/.claude/hooks" -type f | grep -q .; then
    return 0
  fi

  if [[ -f "$repo_path/.claude/settings.json" ]] && \
     jq -e '(.hooks // {}) | type == "object" and (keys | length > 0)' \
       "$repo_path/.claude/settings.json" >/dev/null 2>&1; then
    return 0
  fi

  return 1
}

hg_parity_repo_has_skills() {
  local repo_path="$1"
  [[ -d "$repo_path/.agents/skills" || -d "$repo_path/.claude/skills" ]] || return 1
  find "$repo_path/.agents/skills" "$repo_path/.claude/skills" -maxdepth 2 -name "SKILL.md" -print 2>/dev/null | grep -q .
}

hg_parity_repo_has_roadmap() {
  local repo_path="$1"
  [[ -f "$repo_path/ROADMAP.md" || -f "$repo_path/TODO.md" ]]
}

hg_parity_repo_has_ralph() {
  local repo_path="$1"
  [[ -f "$repo_path/.ralphrc" || -d "$repo_path/.ralph" ]]
}

hg_parity_gemini_extension_name() {
  local repo_name="$1"
  printf '%s-workspace\n' "$(hg_parity_kebab_case_server_name "$repo_name")"
}

hg_parity_gemini_extension_relpath() {
  local repo_name="$1"
  local ext_name
  ext_name="$(hg_parity_gemini_extension_name "$repo_name")"
  printf '.gemini/extensions/%s/gemini-extension.json\n' "$ext_name"
}

hg_parity_gemini_owner_path() {
  local repo_path="$1"
  printf '%s\n' "$repo_path/.gemini/.hg-gemini-settings-sync.json"
}

hg_parity_metadata_path_label() {
  local repo_path="$1"
  local abs="$2"
  local repo_root parity_root dir base

  repo_root="$repo_path"
  parity_root="$HG_AGENT_PARITY_SURFACEKIT_ROOT"
  if [[ -d "$repo_root" ]]; then
    repo_root="$(cd "$repo_root" && pwd)"
  fi
  if [[ -d "$parity_root" ]]; then
    parity_root="$(cd "$parity_root" && pwd)"
  fi

  dir="$(dirname "$abs")"
  base="$(basename "$abs")"
  if [[ -d "$dir" ]]; then
    abs="$(cd "$dir" && pwd)/$base"
  fi

  if [[ "$abs" == "$repo_root/"* ]]; then
    printf '%s\n' "${abs#"$repo_root/"}"
    return
  fi
  if [[ "$abs" == "$repo_root" ]]; then
    printf '.\n'
    return
  fi
  if [[ "$abs" == "$parity_root/"* ]]; then
    printf 'codexkit/%s\n' "${abs#"$parity_root/"}"
    return
  fi
  if [[ "$abs" == "$parity_root" ]]; then
    printf 'codexkit\n'
    return
  fi
  printf '%s\n' "$abs"
}

hg_parity_gemini_sync_script() {
  printf '%s\n' "$HG_AGENT_PARITY_SURFACEKIT_ROOT/scripts/gemini-settings-sync.sh"
}

hg_parity_generated_gemini_settings_count() {
  local repo_path="$1"
  if hg_parity_gemini_settings_current "$repo_path"; then
    printf '1\n'
  else
    printf '0\n'
  fi
}

hg_parity_gemini_mcp_server_count() {
  local repo_path="$1"
  jq -r 'keys | length' <<<"$(hg_parity_gemini_mcp_servers_json "$repo_path")"
}

hg_parity_gemini_sync_metadata_json() {
  local repo_path="$1"
  local metadata
  metadata="$(hg_parity_gemini_owner_path "$repo_path")"
  if [[ ! -f "$metadata" ]]; then
    printf '{}\n'
    return 0
  fi

  jq -c 'if type == "object" then . else {} end' "$metadata"
}

hg_parity_gemini_translated_hook_rule_count() {
  local repo_path="$1"
  local metadata
  metadata="$(hg_parity_gemini_owner_path "$repo_path")"
  if [[ ! -f "$metadata" ]]; then
    printf '0\n'
    return 0
  fi

  jq -r '.translated_hook_rules // 0' "$metadata"
}

hg_parity_gemini_unsupported_hook_rule_count() {
  local repo_path="$1"
  local metadata
  metadata="$(hg_parity_gemini_owner_path "$repo_path")"
  if [[ ! -f "$metadata" ]]; then
    printf '0\n'
    return 0
  fi

  jq -r '.unsupported_claude_hook_rules // 0' "$metadata"
}

hg_parity_source_hooks_json() {
  local repo_path="$1"
  local settings_path="$repo_path/.claude/settings.json"
  if [[ ! -f "$settings_path" ]]; then
    printf '{}\n'
    return 0
  fi

  jq -cS '
    (.hooks // {}) as $hooks
    | {
        SessionStart: ($hooks.SessionStart // []),
        BeforeTool: (($hooks.BeforeTool // []) + ($hooks.PreToolUse // [])),
        AfterTool: (($hooks.AfterTool // []) + ($hooks.PostToolUse // [])),
        Notification: ($hooks.Notification // []),
        BeforeAgent: (($hooks.BeforeAgent // []) + ($hooks.UserPromptSubmit // []))
      }
    | with_entries(select((.value | type == "array") and (.value | length > 0)))
  ' "$settings_path"
}

hg_parity_supported_source_hook_rule_count() {
  local repo_path="$1"
  local settings_path="$repo_path/.claude/settings.json"
  if [[ ! -f "$settings_path" ]]; then
    printf '0\n'
    return 0
  fi

  jq -r '
    [
      (.hooks.SessionStart // []),
      (.hooks.BeforeTool // []),
      (.hooks.PreToolUse // []),
      (.hooks.AfterTool // []),
      (.hooks.PostToolUse // []),
      (.hooks.Notification // []),
      (.hooks.BeforeAgent // []),
      (.hooks.UserPromptSubmit // [])
    ]
    | flatten
    | length
  ' "$settings_path"
}

hg_parity_unsupported_source_hook_rule_count() {
  local repo_path="$1"
  local settings_path="$repo_path/.claude/settings.json"
  if [[ ! -f "$settings_path" ]]; then
    printf '0\n'
    return 0
  fi

  jq -r '
    [
      (.hooks.Stop // []),
      (.hooks.PostToolUseFailure // []),
      (.hooks.PostCompact // []),
      (.hooks.PreCompact // []),
      (.hooks.SubagentStart // []),
      (.hooks.SubagentStop // []),
      (.hooks.SessionEnd // [])
    ]
    | flatten
    | length
  ' "$settings_path"
}

hg_parity_repo_requires_gemini_extension() {
  local repo_path="$1"
  local repo_name="$2"
  [[ "$(hg_parity_repo_objective_bool "$repo_name" "gemini_extension_scaffold" "false")" == "true" ]]
}

hg_parity_render_claude_settings() {
  local repo_path="$1"
  local current_json generated_mcp has_mcp_file
  current_json="$(hg_parity_object_json "$repo_path/.claude/settings.json")"
  generated_mcp="$(hg_parity_claude_mcp_servers_json "$repo_path")"
  if [[ -f "$repo_path/.mcp.json" ]]; then
    has_mcp_file=true
  else
    has_mcp_file=false
  fi

  jq -S -n \
    --argjson current "$current_json" \
    --argjson generated "$generated_mcp" \
    --argjson has_mcp "$has_mcp_file" '
      ($current | if type == "object" then . else {} end) as $cfg
      | if $has_mcp then
          ($cfg + {mcpServers: $generated})
        elif ($generated | length) > 0 then
          $cfg + {mcpServers: (($cfg.mcpServers // {}) + $generated)}
        else
          $cfg
        end
    '
}

hg_parity_render_gemini_settings() {
  local repo_path="$1"
  local template_path template_json generated_mcp normalized_hooks
  template_path="$HG_AGENT_PARITY_SURFACEKIT_ROOT/templates/gemini-settings.standard.json"
  template_json='{}'
  if [[ -f "$template_path" ]]; then
    template_json="$(jq -c 'if type == "object" then . else {} end' "$template_path" 2>/dev/null || printf '{}\n')"
  fi
  generated_mcp="$(hg_parity_gemini_mcp_servers_json "$repo_path")"
  normalized_hooks="$(hg_parity_source_hooks_json "$repo_path")"

  jq -S -n \
    --argjson template "$template_json" \
    --argjson generated "$generated_mcp" \
    --argjson hooks "$normalized_hooks" '
      ($template | if type == "object" then . else {} end) as $base
      | (
          if (($base.context.fileName // null) | type) == "array" then
            ($base.context.fileName | unique | sort) as $file_names
            | ($base + {context: (($base.context // {}) + {fileName: $file_names})})
          else
            $base
          end
        ) as $normalized
      | ($normalized + {mcpServers: $generated}) as $with_servers
      | (
          if ($generated | length) > 0 then
            $with_servers + {
              mcp: (($with_servers.mcp // {}) + {
                allowed: (($generated | keys) | sort)
              })
            }
          else
            ($with_servers | del(.mcp))
          end
        )
      | if ($hooks | length) > 0 then
          . + {hooks: $hooks}
        else
          del(.hooks)
        end
    '
}

hg_parity_render_gemini_extension() {
  local repo_path="$1"
  local repo_name="$2"
  if ! hg_parity_repo_requires_gemini_extension "$repo_path" "$repo_name"; then
    printf '{}\n'
    return 0
  fi

  local ext_name
  ext_name="$(hg_parity_gemini_extension_name "$repo_name")"

  jq -S -n \
    --arg name "$ext_name" '
      {
        name: $name,
        version: "1.0.0",
        contextFileName: "GEMINI.md"
      }
    '
}

hg_parity_dotfiles_root() {
  printf '%s\n' "${HG_DOTFILES:-$HG_STUDIO_ROOT/dotfiles}"
}

hg_parity_local_llm_lib() {
  printf '%s\n' "$(hg_parity_dotfiles_root)/scripts/lib/hg-local-llm.sh"
}

hg_parity_load_local_llm_defaults() {
  local local_llm_lib
  local_llm_lib="$(hg_parity_local_llm_lib)"

  if [[ -f "$local_llm_lib" ]]; then
    # shellcheck disable=SC1090
    source "$local_llm_lib"
    hg_local_llm_export_env
    return 0
  fi

  export OLLAMA_BASE_URL="${OLLAMA_BASE_URL:-http://127.0.0.1:11434}"
  export OLLAMA_CHAT_MODEL="${OLLAMA_CHAT_MODEL:-qwen3:8b}"
  export OLLAMA_FAST_MODEL="${OLLAMA_FAST_MODEL:-qwen2.5-coder:7b}"
  export OLLAMA_CODE_MODEL="${OLLAMA_CODE_MODEL:-devstral-small-2}"
  export OLLAMA_HEAVY_CODE_MODEL="${OLLAMA_HEAVY_CODE_MODEL:-devstral-2}"
  export OLLAMA_HIGH_CONTEXT_CODE_MODEL="${OLLAMA_HIGH_CONTEXT_CODE_MODEL:-qwen3-coder-next}"
  export OLLAMA_API_KEY="${OLLAMA_API_KEY:-ollama}"
  export OLLAMA_KEEP_ALIVE="${OLLAMA_KEEP_ALIVE:-15m}"
}

hg_parity_codex_ollama_start_marker() {
  printf '# BEGIN GENERATED OLLAMA PROFILES: provider-settings-sync\n'
}

hg_parity_codex_ollama_end_marker() {
  printf '# END GENERATED OLLAMA PROFILES: provider-settings-sync\n'
}

hg_parity_render_codex_ollama_profiles() {
  hg_parity_load_local_llm_defaults

  local v1_url env_instruction
  v1_url="${OLLAMA_BASE_URL%/}/v1"
  env_instruction='source "$HOME/hairglasses-studio/dotfiles/scripts/lib/hg-local-llm.sh" && hg_local_llm_export_env'

  cat <<EOF
$(hg_parity_codex_ollama_start_marker)
# Generated by codexkit/scripts/provider-settings-sync.sh from dotfiles shared local-model defaults.
# Refresh this block after updating dotfiles Ollama defaults or host settings.

[model_providers.ollama_local]
name = "Local Ollama"
base_url = $(hg_parity_toml_quote "$v1_url")
env_key = "OLLAMA_API_KEY"
env_key_instructions = $(hg_parity_toml_quote "$env_instruction")
requires_openai_auth = false
wire_api = "responses"

[profiles.ollama_chat]
model_provider = "ollama_local"
model = $(hg_parity_toml_quote "$OLLAMA_CHAT_MODEL")
model_reasoning_effort = "low"

[profiles.ollama_code]
model_provider = "ollama_local"
model = $(hg_parity_toml_quote "$OLLAMA_CODE_MODEL")
model_reasoning_effort = "medium"

[profiles.ollama_heavy]
model_provider = "ollama_local"
model = $(hg_parity_toml_quote "$OLLAMA_HEAVY_CODE_MODEL")
model_reasoning_effort = "high"

[profiles.ollama_high_context]
model_provider = "ollama_local"
model = $(hg_parity_toml_quote "$OLLAMA_HIGH_CONTEXT_CODE_MODEL")
model_reasoning_effort = "high"

$(hg_parity_codex_ollama_end_marker)
EOF
}

hg_parity_render_codex_base_config() {
  local repo_path="$1"
  local template_path

  if [[ -f "$repo_path/.codex/config.toml" ]]; then
    cat "$repo_path/.codex/config.toml"
    return 0
  fi

  template_path="$HG_AGENT_PARITY_SURFACEKIT_ROOT/templates/codex-config.standard.toml"
  if [[ -f "$template_path" ]]; then
    cat "$template_path"
    return 0
  fi

  printf 'approval_policy = "never"\nsandbox_mode = "workspace-write"\n'
}

hg_parity_upsert_generated_block_text() {
  local content="$1"
  local start_marker="$2"
  local end_marker="$3"
  local block="$4"
  local tmpdir input_path block_path output_path python_cmd

  tmpdir="$(mktemp -d)"
  input_path="$tmpdir/input"
  block_path="$tmpdir/block"
  output_path="$tmpdir/output"
  printf '%s\n' "$content" >"$input_path"
  printf '%s\n' "$block" >"$block_path"

  python_cmd="$(command -v python3 || command -v python || true)"
  [[ -n "$python_cmd" ]] || hg_die "python3 or python is required to update generated parity blocks"

  "$python_cmd" - "$input_path" "$block_path" "$output_path" "$start_marker" "$end_marker" <<'PY'
from pathlib import Path
import sys

input_path = Path(sys.argv[1])
block_path = Path(sys.argv[2])
output_path = Path(sys.argv[3])
start_marker = sys.argv[4]
end_marker = sys.argv[5]

content = input_path.read_text(encoding="utf-8")
block = block_path.read_text(encoding="utf-8")
lines = content.splitlines()
out = []
replaced = False
skip = False

for line in lines:
    if not skip and line.startswith(start_marker):
        if not replaced:
            out.append(block.rstrip("\n"))
            replaced = True
        skip = True
        continue
    if skip and line == end_marker:
        skip = False
        continue
    if skip:
        continue
    out.append(line)

rendered = "\n".join(out).rstrip("\n")
if not replaced:
    if rendered:
        rendered += "\n\n"
    rendered += block.rstrip("\n")

output_path.write_text(rendered + "\n", encoding="utf-8")
PY

  cat "$output_path"
  rm -rf "$tmpdir"
}

hg_parity_render_codex_config() {
  local repo_path="$1"
  local base_config ollama_block start_marker end_marker
  base_config="$(hg_parity_render_codex_base_config "$repo_path")"
  ollama_block="$(hg_parity_render_codex_ollama_profiles)"
  start_marker="$(hg_parity_codex_ollama_start_marker)"
  start_marker="${start_marker%$'\n'}"
  end_marker="$(hg_parity_codex_ollama_end_marker)"
  end_marker="${end_marker%$'\n'}"

  hg_parity_upsert_generated_block_text "$base_config" "$start_marker" "$end_marker" "$ollama_block"
}

hg_parity_gemini_settings_current() {
  local repo_path="$1"
  local expected expected_metadata
  expected="$(hg_parity_render_gemini_settings "$repo_path")"
  expected_metadata="$(hg_parity_render_gemini_settings_metadata "$repo_path" \
    "$HG_AGENT_PARITY_SURFACEKIT_ROOT/templates/gemini-settings.standard.json" \
    "$repo_path/.mcp.json" \
    "$repo_path/.claude/settings.json")"
  hg_parity_compare_expected_file "$expected" "$repo_path/.gemini/settings.json" \
    && hg_parity_compare_expected_file "$expected_metadata" "$(hg_parity_gemini_owner_path "$repo_path")"
}

hg_parity_gemini_settings_sync() {
  local repo_path="$1"
  local allow_dirty="${2:-false}"
  local sync_script
  sync_script="$(hg_parity_gemini_sync_script)"
  [[ -f "$sync_script" ]] || return 1

  local args=("$repo_path")
  if [[ "$allow_dirty" == "true" ]]; then
    args+=("--allow-dirty")
  fi

  bash "$sync_script" "${args[@]}" >/dev/null 2>&1
}

hg_parity_compare_expected_file() {
  local expected="$1"
  local path="$2"
  [[ -f "$path" ]] || return 1
  diff -u <(printf '%s\n' "$expected") "$path" >/dev/null 2>&1
}

hg_parity_render_gemini_settings_metadata() {
  local repo_path="$1"
  local template_path="$2"
  local mcp_json_path="$3"
  local claude_settings_path="$4"
  local translated_hook_rules unsupported_hook_rules gemini_mcp_server_count

  translated_hook_rules="$(hg_parity_supported_source_hook_rule_count "$repo_path")"
  unsupported_hook_rules="$(hg_parity_unsupported_source_hook_rule_count "$repo_path")"
  gemini_mcp_server_count="$(hg_parity_gemini_mcp_server_count "$repo_path")"

  jq -n \
    --arg generator "codexkit/scripts/gemini-settings-sync.sh" \
    --arg template "$(hg_parity_metadata_path_label "$repo_path" "$template_path")" \
    --arg mcp_json "$(hg_parity_metadata_path_label "$repo_path" "$mcp_json_path")" \
    --arg claude_settings "$(hg_parity_metadata_path_label "$repo_path" "$claude_settings_path")" \
    --argjson translated_hook_rules "$translated_hook_rules" \
    --argjson unsupported_hook_rules "$unsupported_hook_rules" \
    --argjson gemini_mcp_server_count "$gemini_mcp_server_count" \
    '{
      generator: $generator,
      template: $template,
      source_mcp_json: $mcp_json,
      source_claude_settings: $claude_settings,
      translated_hook_rules: $translated_hook_rules,
      unsupported_claude_hook_rules: $unsupported_hook_rules,
      gemini_mcp_server_count: $gemini_mcp_server_count
    }'
}

hg_parity_provider_mcp_bridge_ok() {
  local repo_path="$1"
  if ! hg_parity_repo_has_mcp_json "$repo_path"; then
    printf '1\n'
    return 0
  fi

  local expected_claude expected_gemini
  expected_claude="$(hg_parity_claude_mcp_servers_json "$repo_path")"
  expected_gemini="$(hg_parity_gemini_mcp_servers_json "$repo_path")"

  if [[ ! -f "$repo_path/.claude/settings.json" || ! -f "$repo_path/.gemini/settings.json" ]]; then
    printf '0\n'
    return 0
  fi

  if jq -e \
      --argjson required "$expected_claude" '
        . as $cfg
        | ($required | keys) as $keys
        | $keys | all(. as $k | ($cfg.mcpServers[$k] != null))
      ' "$repo_path/.claude/settings.json" >/dev/null 2>&1 && \
     jq -e \
      --argjson required "$expected_gemini" '
        . as $cfg
        | ($required | keys) as $keys
        | $keys | all(. as $k | ($cfg.mcpServers[$k] != null))
      ' "$repo_path/.gemini/settings.json" >/dev/null 2>&1; then
    printf '1\n'
  else
    printf '0\n'
  fi
}

hg_parity_provider_hook_bridge_ok() {
  local repo_path="$1"
  local repo_name="$2"
  local unsupported translated source_hooks gemini_hooks
  unsupported="$(hg_parity_unsupported_source_hook_rule_count "$repo_path")"
  translated="$(hg_parity_supported_source_hook_rule_count "$repo_path")"
  source_hooks="$(hg_parity_source_hooks_json "$repo_path")"

  if ! hg_parity_repo_has_claude_hooks "$repo_path"; then
    printf '1\n'
    return 0
  fi

  if [[ ! -f "$repo_path/.gemini/settings.json" ]]; then
    printf '0\n'
    return 0
  fi

  gemini_hooks="$(jq -cS '(.hooks // {}) | if type == "object" then . else {} end' "$repo_path/.gemini/settings.json")"

  if [[ "$unsupported" -ne 0 ]]; then
    printf '0\n'
    return 0
  fi

  if [[ "$translated" -eq 0 ]]; then
    if [[ "$gemini_hooks" == "{}" ]]; then
      printf '1\n'
    else
      printf '0\n'
    fi
    return 0
  fi

  if [[ "$source_hooks" == "$gemini_hooks" ]]; then
    printf '1\n'
  else
    printf '0\n'
  fi
}

hg_parity_provider_drift_count() {
  local repo_path="$1"
  local count=0
  local expected

  expected="$(hg_parity_render_claude_settings "$repo_path")"
  hg_parity_compare_expected_file "$expected" "$repo_path/.claude/settings.json" || count=$((count + 1))

  hg_parity_gemini_settings_current "$repo_path" || count=$((count + 1))

  printf '%s\n' "$count"
}
