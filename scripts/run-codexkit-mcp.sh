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

# Source key: HEAD, plus a hash of uncommitted Go inputs ("<head>+<hash>") when the tree is dirty.
# Empty when ROOT is not a usable git checkout; the mtime check below then decides alone.
SRC_PATHS=('*.go' go.mod go.sum)
export GIT_OPTIONAL_LOCKS=0
source_key() {
  local head dirty
  head="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null)" || return 0
  dirty="$(git -C "$ROOT" status --porcelain --untracked-files=normal -- "${SRC_PATHS[@]}" 2>/dev/null || true)"
  if [[ -n "$dirty" ]]; then
    dirty="$({ git -C "$ROOT" diff --binary HEAD -- "${SRC_PATHS[@]}"; git -C "$ROOT" ls-files -z -o --exclude-standard -- "${SRC_PATHS[@]}" | xargs -0 -r git -C "$ROOT" hash-object --; } 2>/dev/null | sha256sum | cut -c1-12 || true)"
    head+="+$dirty"
  fi
  printf '%s\n' "$head"
}

want_key="$(source_key)"
want_rev="${want_key%%+*}"
have_key="$(cat "$BIN_PATH.key" 2>/dev/null || true)"
cached_rev=""
if [[ -x "$BIN_PATH" ]]; then
  cached_rev="$(go version -m "$BIN_PATH" 2>/dev/null | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p' || true)"
fi

# Rebuild when the cached binary is not built from the current source. The key sidecar covers
# dirty trees and builds that carry no vcs.revision (the -buildvcs=false fallback); vcs.modified
# is not used because Go sets it for any dirty or untracked file, not only Go inputs.
reason=""
if [[ ! -x "$BIN_PATH" ]]; then
  reason="no cached binary"
elif [[ -n "$want_key" ]]; then
  if [[ -z "$cached_rev" ]]; then
    [[ "$have_key" == "$want_key" ]] || reason="cached binary has no vcs.revision"
  elif [[ "$cached_rev" != "$want_rev" ]]; then
    reason="cached vcs.revision ${cached_rev:0:12} != source HEAD ${want_rev:0:12}"
  elif [[ -n "$have_key" || "$want_key" != "$want_rev" ]]; then
    [[ "$have_key" == "$want_key" ]] || reason="uncommitted changes differ from the cached build"
  fi
fi
if [[ -z "$reason" && -n "$(find "$ROOT/cmd" "$ROOT/internal" "$ROOT/go.mod" "$ROOT/go.sum" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$BIN_PATH" -print -quit 2>/dev/null)" ]]; then
  reason="sources are newer than the cached binary"
fi

if [[ "${CODEXKIT_MCP_RESOLVE_ONLY:-}" == 1 ]]; then
  action=cached
  [[ -z "$reason" ]] || action=rebuild
  printf 'binary=%s\nrevision=%s\ncached_revision=%s\naction=%s\n' "$BIN_PATH" "${want_key:-unknown}" "${cached_rev:-none}" "$action"
  exit 0
fi

cd "$ROOT"
if [[ -n "$reason" ]]; then
  echo "run-codexkit-mcp.sh: rebuilding: $reason" >&2
  mkdir -p "$(dirname "$BIN_PATH")"
  # Build beside the cache path and rename over it: running servers keep the old inode.
  tmp_bin="$BIN_PATH.tmp.$$"
  build_ok=false
  build_err="$(mktemp)"
  # Stamp vcs.revision/vcs.modified (go version -m); drop to -buildvcs=false only when Go cannot read VCS status.
  if timeout "${CODEXKIT_MCP_BUILD_TIMEOUT_SEC:-300}s" env GOWORK="$GOWORK" go build -o "$tmp_bin" ./cmd/codexkit-mcp 2>"$build_err"; then
    cat "$build_err" >&2
    build_ok=true
  elif grep -q 'error obtaining VCS status' "$build_err"; then
    echo "run-codexkit-mcp.sh: go cannot obtain VCS status here; rebuilding with -buildvcs=false (binary will carry no vcs.revision)" >&2
    if timeout "${CODEXKIT_MCP_BUILD_TIMEOUT_SEC:-300}s" env GOWORK="$GOWORK" go build -buildvcs=false -o "$tmp_bin" ./cmd/codexkit-mcp; then
      build_ok=true
    fi
  else
    cat "$build_err" >&2
  fi
  rm -f "$build_err"
  if [[ "$build_ok" == true ]] && mv -f "$tmp_bin" "$BIN_PATH"; then
    printf '%s\n' "$want_key" >"$BIN_PATH.key.$$" && mv -f "$BIN_PATH.key.$$" "$BIN_PATH.key"
  else
    rm -f "$tmp_bin"
    if [[ -n "${CODEXKIT_MCP_BUILD_ONLY:-}" || ! -x "$BIN_PATH" ]]; then
      echo "run-codexkit-mcp.sh: build failed" >&2
      exit 1
    fi
    echo "run-codexkit-mcp.sh: build failed; serving the existing binary $BIN_PATH" >&2
  fi
fi

if [[ -n "${CODEXKIT_MCP_BUILD_ONLY:-}" ]]; then
  printf 'binary=%s\nrevision=%s\n' "$BIN_PATH" "${want_key:-unknown}"
  exit 0
fi

exec "$BIN_PATH" "$@"
