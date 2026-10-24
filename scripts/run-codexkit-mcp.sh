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

# Mode switches are read once, then unset so neither the build nor the exec'd server sees them.
resolve_only=false
if [[ "${CODEXKIT_MCP_RESOLVE_ONLY:-}" == 1 ]]; then resolve_only=true; fi
build_only=false
if [[ "${CODEXKIT_MCP_BUILD_ONLY:-}" == 1 ]]; then build_only=true; fi
unset CODEXKIT_MCP_RESOLVE_ONLY CODEXKIT_MCP_BUILD_ONLY

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi; }

# One cache path per checkout: only the canonical checkout ($HOME/hairglasses-studio/codexkit) uses
# the shared BIN_PATH; any other ROOT (worktree, clone) gets bin/wt-<hash of its realpath>/<name>,
# so two checkouts at different HEADs never rebuild over each other's binary.
ROOT_REAL="$(cd "$ROOT" && pwd -P)"
CANON_ROOT="$(cd "$USER_HOME/hairglasses-studio/codexkit" 2>/dev/null && pwd -P || true)"
if [[ "$ROOT_REAL" != "$CANON_ROOT" ]]; then
  wt_hash="$(printf '%s' "$ROOT_REAL" | sha256 2>/dev/null | cut -c1-8 || true)"
  [[ -n "$wt_hash" ]] || wt_hash="$(printf '%s' "$ROOT_REAL" | cksum | cut -d' ' -f1)"
  BIN_PATH="$(dirname "$BIN_PATH")/wt-$wt_hash/$(basename "$BIN_PATH")"
fi

# Source key: HEAD, plus a hash of uncommitted Go inputs ("<head>+<hash>") when the tree is dirty.
# Prints nothing when ROOT is not a git checkout (the mtime check below then decides alone) and
# fails when the dirty state cannot be hashed (the caller then rebuilds: unknown never matches).
SRC_PATHS=('*.go' go.mod go.sum)
source_key() {
  local head dirty material h
  head="$(env GIT_OPTIONAL_LOCKS=0 git -C "$ROOT" rev-parse HEAD 2>/dev/null)" || return 0
  dirty="$(env GIT_OPTIONAL_LOCKS=0 git -C "$ROOT" status --porcelain --untracked-files=normal -- "${SRC_PATHS[@]}" 2>/dev/null)" || return 1
  if [[ -n "$dirty" ]]; then
    material="$({
      env GIT_OPTIONAL_LOCKS=0 git -C "$ROOT" diff --binary HEAD -- "${SRC_PATHS[@]}" || exit 1
      env GIT_OPTIONAL_LOCKS=0 git -C "$ROOT" ls-files -z -o --exclude-standard -- "${SRC_PATHS[@]}" | xargs -0 -r env GIT_OPTIONAL_LOCKS=0 git -C "$ROOT" hash-object -- || exit 1
    } 2>/dev/null)" || return 1
    [[ -n "$material" ]] || return 1
    h="$(printf '%s' "$material" | sha256 2>/dev/null | cut -c1-12)" || return 1
    [[ "$h" =~ ^[0-9a-f]{12}$ ]] || return 1
    head+="+$h"
  fi
  printf '%s\n' "$head"
}

# Sets want_key/reason: rebuild when the cached binary is missing, has no vcs.revision, was built
# from another HEAD, or (dirty tree) from other uncommitted inputs. The key sidecar covers dirty
# trees and -buildvcs=false builds; vcs.modified is not used because Go sets it for any dirty or
# untracked file, not only Go inputs. A build that already failed for this exact key is not retried.
decide() {
  local key_unknown=false
  failed_known=false
  want_key="$(source_key)" || { key_unknown=true; want_key=""; }
  local want_rev="${want_key%%+*}"
  local have_key cached_rev=""
  have_key="$(cat "$BIN_PATH.key" 2>/dev/null || true)"
  if [[ -x "$BIN_PATH" ]]; then
    cached_rev="$(go version -m "$BIN_PATH" 2>/dev/null | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p' || true)"
  fi
  cached_revision="$cached_rev"
  reason=""
  if [[ ! -x "$BIN_PATH" ]]; then
    reason="no cached binary"
  elif [[ "$key_unknown" == true ]]; then
    reason="cannot hash the uncommitted changes"
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
  if [[ -n "$reason" && -n "$want_key" && "$build_only" != true && -x "$BIN_PATH" && -e "$BIN_PATH.failed-$want_key" ]]; then
    reason=""
    failed_known=true
  fi
}

decide

if [[ "$resolve_only" == true ]]; then
  action=cached
  [[ -z "$reason" ]] || action=rebuild
  printf 'binary=%s\nrevision=%s\ncached_revision=%s\naction=%s\n' "$BIN_PATH" "${want_key:-unknown}" "${cached_revision:-none}" "$action"
  exit 0
fi

cd "$ROOT"

# Run without timeout where it does not exist (macOS) rather than failing every build with rc 127.
TIMEOUT=()
if command -v timeout >/dev/null 2>&1; then TIMEOUT=(timeout "${CODEXKIT_MCP_BUILD_TIMEOUT_SEC:-300}s"); fi
tmp_bin=""
build_err=""
bpid=""
# Runs on every exit path except exec: a terminated launcher kills its build and leaves no temp files.
cleanup() {
  if [[ -n "$bpid" ]]; then
    kill "$bpid" 2>/dev/null || true
    wait "$bpid" 2>/dev/null || true
  fi
  if [[ -n "$tmp_bin" ]]; then rm -f "$tmp_bin" "$BIN_PATH.key.$$"; fi
  if [[ -n "$build_err" ]]; then rm -f "$build_err"; fi
}
trap cleanup EXIT
trap 'exit 143' TERM INT HUP

# The build runs in the background so a signal reaches the launcher at once; fd 9 (the build
# lock) is closed for it, otherwise a stray compiler would keep the lock after we exit.
go_build() { # extra go build flags
  ${TIMEOUT[@]+"${TIMEOUT[@]}"} env GOWORK="$GOWORK" go build "$@" -o "$tmp_bin" ./cmd/codexkit-mcp 2>"$build_err" 9>&- &
  bpid=$!
  local rc=0
  wait "$bpid" || rc=$?
  bpid=""
  return "$rc"
}

rebuild() { # 0 = cache refreshed; the build lock is held (or unavailable)
  local ok=false
  tmp_bin="$BIN_PATH.tmp.$$"
  build_err="$(mktemp)"
  # Stamp vcs.revision/vcs.modified (go version -m); drop to -buildvcs=false only when Go cannot read VCS status.
  if go_build; then
    cat "$build_err" >&2
    ok=true
  elif grep -q 'error obtaining VCS status' "$build_err"; then
    echo "run-codexkit-mcp.sh: go cannot obtain VCS status here; rebuilding with -buildvcs=false (binary will carry no vcs.revision)" >&2
    if go_build -buildvcs=false; then ok=true; else cat "$build_err" >&2; fi
  else
    cat "$build_err" >&2
  fi
  if [[ "$ok" == true ]]; then
    # Install: drop the old key, rename the binary over the cache path (running servers keep the old
    # inode), then publish the new key. A key sidecar therefore only ever vouches for a binary that
    # was installed before it; a crash in between leaves no key, which costs one extra rebuild.
    rm -f "$BIN_PATH.key"
    mv -f "$tmp_bin" "$BIN_PATH" || ok=false
  fi
  if [[ "$ok" == true ]]; then
    rm -f "$BIN_PATH".failed-*
    if [[ -n "$want_key" ]]; then
      printf '%s\n' "$want_key" >"$BIN_PATH.key.$$" && mv -f "$BIN_PATH.key.$$" "$BIN_PATH.key"
    fi
  elif [[ -n "$want_key" ]]; then
    rm -f "$BIN_PATH".failed-*
    : >"$BIN_PATH.failed-$want_key" || true
  fi
  rm -f "$tmp_bin" "$build_err"
  tmp_bin=""
  build_err=""
  [[ "$ok" == true ]]
}

if [[ -n "$reason" ]]; then
  mkdir -p "$(dirname "$BIN_PATH")" 2>/dev/null || true
  skip_build=false
  if command -v flock >/dev/null 2>&1 && { exec 9>>"$BIN_PATH.lock"; } 2>/dev/null; then
    if ! flock -n 9; then
      if [[ -x "$BIN_PATH" && "$build_only" != true ]]; then
        echo "run-codexkit-mcp.sh: another launcher is rebuilding; serving the existing binary" >&2
        skip_build=true
      elif ! flock -w "${CODEXKIT_MCP_LOCK_WAIT_SEC:-120}" 9; then
        echo "run-codexkit-mcp.sh: build lock still held after the wait" >&2
        if [[ -x "$BIN_PATH" ]]; then skip_build=true; fi
      fi
    fi
  fi
  if [[ "$skip_build" != true ]]; then
    decide # the sources, or the binary, may have changed while we waited for the lock
    if [[ -n "$reason" ]]; then
      echo "run-codexkit-mcp.sh: rebuilding: $reason" >&2
      if ! rebuild; then
        if [[ "$build_only" == true || ! -x "$BIN_PATH" ]]; then
          echo "run-codexkit-mcp.sh: build failed" >&2
          exit 1
        fi
        echo "run-codexkit-mcp.sh: build failed; serving the existing binary $BIN_PATH" >&2
      fi
    fi
  fi
  exec 9>&-
fi
if [[ "$failed_known" == true ]]; then
  echo "run-codexkit-mcp.sh: the build for ${want_key:0:12} already failed; serving the existing binary (remove $BIN_PATH.failed-* to retry)" >&2
fi

if [[ "$build_only" == true ]]; then
  printf 'binary=%s\nrevision=%s\n' "$BIN_PATH" "${want_key:-unknown}"
  exit 0
fi

trap - EXIT TERM INT HUP
exec "$BIN_PATH" "$@"
