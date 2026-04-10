#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CODEXKIT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

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

(
  cd "$CODEXKIT_ROOT"
  GOWORK=off go run ./cmd/codexkit mcp "$MODE" "$REPO_PATH"
)
