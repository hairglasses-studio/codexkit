#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="${XDG_CACHE_HOME:-$HOME/.cache}/codexkit-mcp/bin/codexkit-mcp"
workspace_go_env="${HAIRGLASSES_STUDIO_ROOT:-$HOME/hairglasses-studio}/scripts/go-workspace-env.sh"

if [[ -f "$workspace_go_env" ]]; then
  # shellcheck source=/dev/null
  source "$workspace_go_env"
  hg_go_apply_env "$ROOT"
else
  export GOWORK="${GOWORK:-off}"
  export GOCACHE="${GOCACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/go-build}"
  fallback_tmp="${XDG_CACHE_HOME:-$HOME/.cache}/hairglasses-studio/go-tmp/$(basename "$ROOT")"
  mkdir -p "$GOCACHE" "$fallback_tmp"
  export GOTMPDIR="${GOTMPDIR:-$fallback_tmp}"
  export TMPDIR="${TMPDIR:-$fallback_tmp}"
fi

needs_build=false
if [[ ! -x "$BIN_PATH" ]]; then
  needs_build=true
elif find "$ROOT/cmd" "$ROOT/internal" "$ROOT/go.mod" "$ROOT/go.sum" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$BIN_PATH" -print -quit | grep -q .; then
  needs_build=true
fi

cd "$ROOT"
if [[ "$needs_build" == true ]]; then
  mkdir -p "$(dirname "$BIN_PATH")"
  GOWORK="$GOWORK" go build -o "$BIN_PATH" ./cmd/codexkit-mcp
fi

exec "$BIN_PATH" "$@"
