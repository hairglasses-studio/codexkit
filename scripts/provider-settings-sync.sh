#!/usr/bin/env bash
# provider-settings-sync.sh — Sync provider-native parity settings for any local repo.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/hg-core.sh"
source "$SCRIPT_DIR/lib/hg-agent-parity.sh"

MODE="write"
REPO_PATH=""
REPO_NAME=""
ALLOW_DIRTY=false
INCLUDE_CODEX_CONFIG=false
FAILED=0
CREATED=0
UPDATED=0
CURRENT=0

usage() {
  cat <<'EOF'
Usage: provider-settings-sync.sh <repo_path> [options]

Sync:
- root .claude/settings.json
- root .gemini/settings.json
- Gemini extension scaffolds when repo objectives require them

Options:
  --repo-name <name>  Repo name override used for manifest-backed objectives
  --include-codex-config  Also enforce the generated local-model block in .codex/config.toml
  --dry-run           Show drift without writing
  --check             Exit non-zero when drift exists
  --write             Apply changes (default)
  --allow-dirty       Overwrite dirty provider settings files
  -h, --help          Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-name)
      [[ $# -ge 2 ]] || hg_die "--repo-name requires a value"
      REPO_NAME="$2"
      shift 2
      ;;
    --dry-run)
      MODE="dry-run"
      shift
      ;;
    --include-codex-config)
      INCLUDE_CODEX_CONFIG=true
      shift
      ;;
    --check)
      MODE="check"
      shift
      ;;
    --write)
      MODE="write"
      shift
      ;;
    --allow-dirty)
      ALLOW_DIRTY=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      hg_die "Unknown argument: $1"
      ;;
    *)
      [[ -z "$REPO_PATH" ]] || hg_die "Only one repo path may be provided"
      REPO_PATH="$1"
      shift
      ;;
  esac
done

[[ -n "$REPO_PATH" ]] || {
  usage >&2
  exit 1
}

hg_parity_require_tools
hg_require git
REPO_PATH="$(cd "$REPO_PATH" && pwd)"
[[ -e "$REPO_PATH/.git" ]] || hg_die "Not a git repo: $REPO_PATH"
REPO_NAME="${REPO_NAME:-$(basename "$REPO_PATH")}"

repo_file_dirty() {
  local repo_path="$1"
  local target_rel="$2"
  [[ -n "$(git -C "$repo_path" status --porcelain --untracked-files=all -- "$target_rel" 2>/dev/null)" ]]
}

mark_failure() {
  FAILED=1
}

report_current() {
  local label="$1"
  printf "%s%-20s %s (current)%s\n" "$HG_DIM" "$REPO_NAME" "$label" "$HG_RESET"
  CURRENT=$((CURRENT + 1))
}

report_missing_or_drift() {
  local label="$1"
  printf "%s%-20s %s%s\n" "$HG_YELLOW" "$REPO_NAME" "$label" "$HG_RESET"
  mark_failure
}

write_text_file() {
  local target_rel="$1"
  local expected="$2"
  local label="$3"

  local target="$REPO_PATH/$target_rel"
  if [[ -f "$target" ]] && diff -u <(printf '%s\n' "$expected") "$target" >/dev/null 2>&1; then
    report_current "$label"
    return 0
  fi

  case "$MODE" in
    dry-run)
      if [[ -f "$target" ]]; then
        printf "%s%-20s %s (would update)%s\n" "$HG_YELLOW" "$REPO_NAME" "$label" "$HG_RESET"
      else
        printf "%s%-20s %s (would create)%s\n" "$HG_YELLOW" "$REPO_NAME" "$label" "$HG_RESET"
      fi
      mark_failure
      return 0
      ;;
    check)
      if [[ -f "$target" ]]; then
        report_missing_or_drift "$label (drift)"
      else
        report_missing_or_drift "$label (missing)"
      fi
      return 0
      ;;
  esac

  if ! $ALLOW_DIRTY && repo_file_dirty "$REPO_PATH" "$target_rel"; then
    hg_warn "$REPO_NAME: skipping dirty $label ($target_rel)"
    mark_failure
    return 0
  fi

  mkdir -p "$(dirname "$target")"
  printf '%s\n' "$expected" >"$target"
  if git -C "$REPO_PATH" ls-files --error-unmatch "$target_rel" >/dev/null 2>&1; then
    printf "%s%-20s %s (updated)%s\n" "$HG_GREEN" "$REPO_NAME" "$label" "$HG_RESET"
    UPDATED=$((UPDATED + 1))
  else
    printf "%s%-20s %s (created)%s\n" "$HG_GREEN" "$REPO_NAME" "$label" "$HG_RESET"
    CREATED=$((CREATED + 1))
  fi
}

check_required_extension() {
  local target_rel="$1"
  local expected="$2"

  local target="$REPO_PATH/$target_rel"
  if [[ -f "$target" ]] && diff -u <(printf '%s\n' "$expected") "$target" >/dev/null 2>&1; then
    report_current "gemini-extension"
    return 0
  fi

  if [[ ! -f "$target" ]]; then
    report_missing_or_drift "gemini-extension (required)"
  else
    report_missing_or_drift "gemini-extension (drift)"
  fi
}

sync_provider_settings() {
  if $INCLUDE_CODEX_CONFIG; then
    local codex_expected
    codex_expected="$(hg_parity_render_codex_config "$REPO_PATH")"
    write_text_file ".codex/config.toml" "$codex_expected" "codex-config"
  fi

  local claude_expected
  claude_expected="$(hg_parity_render_claude_settings "$REPO_PATH")"
  write_text_file ".claude/settings.json" "$claude_expected" "claude-settings"

  case "$MODE" in
    dry-run)
      if hg_parity_gemini_settings_current "$REPO_PATH"; then
        report_current "gemini-settings"
      else
        report_missing_or_drift "gemini-settings"
      fi
      ;;
    check)
      if hg_parity_gemini_settings_current "$REPO_PATH"; then
        report_current "gemini-settings"
      else
        report_missing_or_drift "gemini-settings"
      fi
      ;;
    write)
      if hg_parity_gemini_settings_current "$REPO_PATH"; then
        report_current "gemini-settings"
      elif ! $ALLOW_DIRTY && {
        repo_file_dirty "$REPO_PATH" ".gemini/settings.json" \
        || repo_file_dirty "$REPO_PATH" ".gemini/config.yaml" \
        || repo_file_dirty "$REPO_PATH" ".gemini/.hg-gemini-settings-sync.json";
      }; then
        hg_warn "$REPO_NAME: skipping dirty gemini-settings (.gemini/settings.json, .gemini/config.yaml)"
        mark_failure
      elif hg_parity_gemini_settings_sync "$REPO_PATH" "$ALLOW_DIRTY"; then
        printf "%s%-20s %s (synced)%s\n" "$HG_GREEN" "$REPO_NAME" "gemini-settings" "$HG_RESET"
        UPDATED=$((UPDATED + 1))
      else
        hg_warn "$REPO_NAME: gemini-settings sync failed"
        mark_failure
      fi
      ;;
  esac

  if hg_parity_repo_requires_gemini_extension "$REPO_PATH" "$REPO_NAME"; then
    local extension_rel extension_expected
    extension_rel="$(hg_parity_gemini_extension_relpath "$REPO_NAME")"
    extension_expected="$(hg_parity_render_gemini_extension "$REPO_PATH" "$REPO_NAME")"
    if [[ "$MODE" == "check" ]]; then
      check_required_extension "$extension_rel" "$extension_expected"
    else
      write_text_file "$extension_rel" "$extension_expected" "gemini-extension"
    fi
  else
    report_current "gemini-extension (not managed)"
  fi
}

sync_provider_settings

if [[ "$MODE" == "write" ]]; then
  hg_ok "Provider parity sync complete — ${CREATED} created, ${UPDATED} updated, ${CURRENT} current"
else
  hg_info "Provider parity check complete — ${CURRENT} current"
fi

exit "$FAILED"
