#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CODEXKIT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
workspace_root="${CODEXKIT_WORKSPACE_ROOT:-${HAIRGLASSES_STUDIO_ROOT:-}}"
workspace_go_env=""
if [[ -n "$workspace_root" ]]; then
  workspace_go_env="$workspace_root/scripts/go-workspace-env.sh"
fi

MODE="sync"
REPO_PATH=""

usage() {
  cat <<'EOF'
Usage: codex-mcp-sync.sh <repo_path> [--dry-run|--check]

Compatibility wrapper for codexkit MCP sync commands.

Options:
  --dry-run   Show pending generated MCP block drift
  --check     Exit non-zero when generated MCP block drift exists
  -h, --help  Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      MODE="diff"
      shift
      ;;
    --check)
      MODE="check"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
    *)
      [[ -z "$REPO_PATH" ]] || {
        echo "Only one repo path may be provided" >&2
        exit 1
      }
      REPO_PATH="$1"
      shift
      ;;
  esac
done

[[ -n "$REPO_PATH" ]] || {
  usage >&2
  exit 1
}

REPO_PATH="$(cd "$REPO_PATH" && pwd)"

if [[ -n "$workspace_go_env" && -f "$workspace_go_env" ]]; then
  # shellcheck source=/dev/null
  source "$workspace_go_env"
  hg_go_apply_env "$CODEXKIT_ROOT"
else
  export GOWORK="${GOWORK:-off}"
  export GOCACHE="${GOCACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/go-build}"
  fallback_tmp="${XDG_CACHE_HOME:-$HOME/.cache}/codexkit/go-tmp/$(basename "$CODEXKIT_ROOT")"
  mkdir -p "$GOCACHE" "$fallback_tmp"
  export GOTMPDIR="${GOTMPDIR:-$fallback_tmp}"
  export TMPDIR="${TMPDIR:-$fallback_tmp}"
fi

(
  cd "$CODEXKIT_ROOT"
  GOWORK=off go run ./cmd/codexkit mcp "$MODE" "$REPO_PATH"
)
