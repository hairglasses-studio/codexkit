#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  if [[ -x /usr/local/go/bin/go ]]; then
    export PATH="/usr/local/go/bin:$PATH"
  fi
fi
if [[ -d "/usr/local/go/bin" && ":$PATH:" != *":/usr/local/go/bin:"* ]]; then
  export PATH="/usr/local/go/bin:$PATH"
fi

USER_HOME="${HOME:-$(getent passwd "$(id -un 2>/dev/null || echo "")" | cut -d: -f6 2>/dev/null || echo "/home/$(id -un 2>/dev/null || echo "")")}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="${XDG_CACHE_HOME:-$USER_HOME/.cache}/codexkit-mcp/bin/codexkit-mcp"
workspace_root="${CODEXKIT_WORKSPACE_ROOT:-${HAIRGLASSES_STUDIO_ROOT:-}}"
workspace_go_env=""
if [[ -n "$workspace_root" ]]; then
  workspace_go_env="$workspace_root/scripts/go-workspace-env.sh"
fi

if [[ -n "$workspace_go_env" && -f "$workspace_go_env" ]]; then
  # shellcheck source=/dev/null
  source "$workspace_go_env"
  hg_go_apply_env "$ROOT"
else
  export GOWORK="${GOWORK:-off}"
  export GOCACHE="${GOCACHE:-${XDG_CACHE_HOME:-$USER_HOME/.cache}/go-build}"
  fallback_tmp="${XDG_CACHE_HOME:-$USER_HOME/.cache}/codexkit/go-tmp/$(basename "$ROOT")"
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
  # Stamp vcs.revision/vcs.modified (go version -m); drop to -buildvcs=false only when Go cannot read VCS status.
  build_err="$(mktemp)"
  if GOWORK="$GOWORK" go build -o "$BIN_PATH" ./cmd/codexkit-mcp 2>"$build_err"; then
    cat "$build_err" >&2
  elif grep -q 'error obtaining VCS status' "$build_err"; then
    echo "run-codexkit-mcp.sh: go cannot obtain VCS status here; rebuilding with -buildvcs=false (binary will carry no vcs.revision)" >&2
    GOWORK="$GOWORK" go build -buildvcs=false -o "$BIN_PATH" ./cmd/codexkit-mcp
  else
    cat "$build_err" >&2
    rm -f "$build_err"
    exit 1
  fi
  rm -f "$build_err"
fi

exec "$BIN_PATH" "$@"
