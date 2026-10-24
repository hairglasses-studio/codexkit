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
CANON_BIN="$BIN_PATH"
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
# untracked file, not only Go inputs. A build that failed for this exact key within the last
# FAILED_TTL_MIN minutes is not retried (older markers expire, so a timeout, a network error or a
# full disk never pins a stale binary for good).
FAILED_TTL_MIN="${CODEXKIT_MCP_FAILED_TTL_MIN:-10}"
have_fallback() { [[ -x "$BIN_PATH" || -x "$CANON_BIN" ]]; }
build_class="" # env | compile: why the last build for this key failed (see classify_failure)
decide() {
  local key_unknown=false
  failed_known=false
  build_class=""
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
  if [[ -n "$reason" && -n "$want_key" && "$build_only" != true ]] && have_fallback &&
    [[ -n "$(find "$BIN_PATH.failed-$want_key" -maxdepth 0 -mmin "-$FAILED_TTL_MIN" 2>/dev/null)" ]]; then
    reason=""
    failed_known=true
    build_class="$(cat "$BIN_PATH.failed-$want_key" 2>/dev/null || true)"
    [[ "$build_class" == env ]] || build_class=compile
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
last_rc=0
tmpd="" # scratch dir for the build: $TMPDIR if usable, else <cache bin dir>/.tmp
modfile_dir=""
MODFLAG=()
# Runs on every exit path except exec: a terminated launcher kills its build and leaves no temp files.
cleanup() {
  if [[ -n "$bpid" ]]; then
    kill "$bpid" 2>/dev/null || true
    wait "$bpid" 2>/dev/null || true
  fi
  if [[ -n "$tmp_bin" ]]; then rm -f "$tmp_bin" "$BIN_PATH.key.$$"; fi
  if [[ -n "$build_err" ]]; then rm -f "$build_err"; fi
  if [[ -n "$modfile_dir" ]]; then rm -rf "$modfile_dir"; fi
}

# The relative path of a go.mod line's `=> ../x` replace, or nothing. A one-line replace and a line
# inside a `replace ( ... )` block match alike; a // comment line, a `=>` after a // comment, a
# quoted path and a path with a version or spaces are left alone; a trailing // comment is allowed.
relative_replace() { # go.mod-line
  local line="$1" re='=>[[:space:]]*(\.\.?/[^[:space:]"]+)[[:space:]]*(//.*)?$'
  if [[ "$line" =~ ^[[:space:]]*// || "${line%%=>*}" == *//* ]]; then return 0; fi
  if [[ "$line" =~ $re ]]; then printf '%s\n' "${BASH_REMATCH[1]}"; fi
}

# A linked worktree has no sibling checkout for a relative `replace ... => ../x`. When one is missing
# there but exists under $HOME/hairglasses-studio, build with a temp modfile in which EVERY relative
# replace is absolute (the existing sibling, else the one under $HOME/hairglasses-studio), so none
# depends on where the modfile lives. A no-op when no sibling is missing, or no temp dir exists.
prepare_modfile() {
  local line target abs pre post need=false
  while IFS= read -r line || [[ -n "$line" ]]; do
    target="$(relative_replace "$line")"
    if [[ -n "$target" && ! -d "$ROOT/$target" && -d "$USER_HOME/hairglasses-studio/$(basename "$target")" ]]; then need=true; fi
  done <"$ROOT/go.mod"
  if [[ "$need" != true ]]; then return 0; fi
  modfile_dir="$(TMPDIR="$tmpd" mktemp -d 2>/dev/null)" || modfile_dir=""
  if [[ -z "$modfile_dir" ]]; then return 0; fi # never write through an empty path
  while IFS= read -r line || [[ -n "$line" ]]; do
    target="$(relative_replace "$line")"
    if [[ -n "$target" ]]; then
      abs=""
      if [[ -d "$ROOT/$target" ]]; then
        abs="$(cd "$ROOT/$target" && pwd -P)"
      elif [[ -d "$USER_HOME/hairglasses-studio/$(basename "$target")" ]]; then
        abs="$USER_HOME/hairglasses-studio/$(basename "$target")"
      fi
      if [[ -n "$abs" ]]; then
        pre="${line%%=>*}=>"
        post="${line#*=>}"
        # No ${post/pat/$abs}: with bash >= 5.2 an `&` in $abs would expand to the matched text.
        line="$pre${post%%"$target"*}$abs${post#*"$target"}"
      fi
    fi
    printf '%s\n' "$line"
  done <"$ROOT/go.mod" >"$modfile_dir/go.mod"
  cp "$ROOT/go.sum" "$modfile_dir/go.sum" 2>/dev/null || true
  MODFLAG=("-modfile=$modfile_dir/go.mod")
}
trap cleanup EXIT
trap 'exit 143' TERM INT HUP

# The build runs in the background so a signal reaches the launcher at once; fd 9 (the build
# lock) is closed for it, otherwise a stray compiler would keep the lock after we exit.
go_build() { # extra go build flags
  ${TIMEOUT[@]+"${TIMEOUT[@]}"} env GOWORK="$GOWORK" TMPDIR="$tmpd" go build ${MODFLAG[@]+"${MODFLAG[@]}"} "$@" -o "$tmp_bin" ./cmd/codexkit-mcp 2>"$build_err" 9>&- &
  bpid=$!
  local rc=0
  wait "$bpid" || rc=$?
  bpid=""
  last_rc="$rc"
  return "$rc"
}

# Why a build failed. "env" (the canonical binary may stand in for a checkout that cannot build): a
# timeout, no toolchain, an unresolvable replace sibling, network or disk trouble. Everything else,
# every Go compile or vet error included, is "compile": a real break that must stay visible. When in
# doubt it is "compile". Output that has an env line AND a file.go:line: error elsewhere is "compile".
classify_failure() { # rc build-output-file
  local env_re='replacement directory .* does not exist|no space left on device|disk quota exceeded|dial tcp|no such host|i/o timeout|TLS handshake timeout|toolchain not available|cannot find GOROOT|creating work dir|signal: killed|cannot allocate memory|resource temporarily unavailable|read-only file system|(go-build|pkg/mod|GOCACHE|GOMODCACHE).*permission denied|permission denied.*(go-build|pkg/mod|GOCACHE|GOMODCACHE)' rest
  # 124 timeout, 127 no toolchain, >= 128 killed by a signal (OOM killer, kill -9): never the source's fault.
  if [[ "$1" == 124 || "$1" == 127 || "$1" -ge 128 ]]; then echo env; return 0; fi
  if grep -Eq "$env_re" "$2" 2>/dev/null; then
    rest="$(grep -Ev "$env_re" "$2" || true)"
    if grep -Eq '\.go:[0-9]+(:[0-9]+)?:' <<<"$rest"; then echo compile; else echo env; fi
    return 0
  fi
  echo compile
}

# Per-checkout dirs record their root in a `root` file; one whose recorded root is provably gone is
# removed: the root's parent exists and is searchable, and the root is absent. Anything else (no root
# file, unreadable parent, missing parent, a flaky mount) keeps the dir. Accepted residual: a root
# that is the unmounted mountpoint itself, under a searchable parent, looks absent and is pruned.
prune_wt_dirs() {
  local d r p
  for d in "$(dirname "$CANON_BIN")"/wt-*/; do
    [[ -s "$d/root" ]] || continue
    r="$(cat "$d/root" 2>/dev/null || true)"
    [[ "$r" == /* ]] || continue
    p="$(dirname "$r")"
    if [[ -d "$p" && -x "$p" && ! -e "$r" && ! -L "$r" ]]; then rm -rf -- "${d%/}"; fi
  done
}

rebuild() { # 0 = cache refreshed; the build lock is held (or unavailable)
  local ok=false
  if [[ "$BIN_PATH" != "$CANON_BIN" ]]; then printf '%s\n' "$ROOT_REAL" >"$(dirname "$BIN_PATH")/root" || true; fi
  prune_wt_dirs
  tmp_bin="$BIN_PATH.tmp.$$"
  # Scratch files live in $TMPDIR when that is usable, else in a cache-local dir. With neither there
  # is nothing to build with: an environmental failure, decided before any build and never recorded.
  # The cache-local dir is tried whenever $TMPDIR is unusable OR mktemp fails in it (full fs, quota).
  tmpd="${TMPDIR:-/tmp}"
  build_err=""
  if [[ -d "$tmpd" && -w "$tmpd" && -x "$tmpd" ]]; then build_err="$(TMPDIR="$tmpd" mktemp 2>/dev/null)" || build_err=""; fi
  if [[ -z "$build_err" ]]; then
    tmpd="$(dirname "$BIN_PATH")/.tmp"
    if mkdir -p "$tmpd" 2>/dev/null; then build_err="$(TMPDIR="$tmpd" mktemp 2>/dev/null)" || build_err=""; else tmpd=""; fi
  fi
  record_failure=true
  if [[ -z "$build_err" ]]; then
    echo "run-codexkit-mcp.sh: no usable temp dir for the build" >&2
    build_class="env"
    record_failure=false
  else
    prepare_modfile
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
    if [[ "$ok" != true ]]; then build_class="$(classify_failure "$last_rc" "$build_err")"; fi
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
  elif [[ -n "$want_key" && "$record_failure" == true ]]; then
    rm -f "$BIN_PATH".failed-*
    printf '%s\n' "$build_class" >"$BIN_PATH.failed-$want_key" || true
  fi
  rm -f "$tmp_bin" "$build_err"
  tmp_bin=""
  build_err=""
  if [[ -n "$modfile_dir" ]]; then rm -rf "$modfile_dir"; fi
  modfile_dir=""
  MODFLAG=()
  [[ "$ok" == true ]]
}

if [[ -n "$reason" ]]; then
  mkdir -p "$(dirname "$BIN_PATH")" 2>/dev/null || true
  skip_build=false
  # Without flock (macOS) concurrent launchers may each build: temp files are per pid and the install
  # is a rename, so that is only wasteful, not unsafe.
  if command -v flock >/dev/null 2>&1 && { exec 9>>"$BIN_PATH.lock"; } 2>/dev/null; then
    if ! flock -n 9; then
      if have_fallback && [[ "$build_only" != true ]]; then
        echo "run-codexkit-mcp.sh: another launcher is rebuilding; serving the existing binary" >&2
        skip_build=true
      elif ! flock -w "${CODEXKIT_MCP_LOCK_WAIT_SEC:-120}" 9; then
        echo "run-codexkit-mcp.sh: build lock still held after the wait" >&2
        if have_fallback; then skip_build=true; fi
      fi
    fi
  fi
  if [[ "$skip_build" != true ]]; then
    decide # the sources, or the binary, may have changed while we waited for the lock
    if [[ -n "$reason" ]]; then
      echo "run-codexkit-mcp.sh: rebuilding: $reason" >&2
      if ! rebuild; then
        if [[ "$build_only" == true ]]; then
          echo "run-codexkit-mcp.sh: build failed" >&2
          exit 1
        elif [[ "$build_class" == compile && "$BIN_PATH" != "$CANON_BIN" ]]; then
          echo "run-codexkit-mcp.sh: build failed; this checkout does not compile, not serving its earlier or the canonical binary" >&2
          exit 1
        elif [[ -x "$BIN_PATH" ]]; then
          echo "run-codexkit-mcp.sh: build failed; serving the existing binary" >&2
        elif [[ "$build_class" != env || ! -x "$CANON_BIN" ]]; then
          echo "run-codexkit-mcp.sh: build failed" >&2
          exit 1
        fi
      fi
    fi
  fi
  exec 9>&-
fi
if [[ "$failed_known" == true ]]; then
  echo "run-codexkit-mcp.sh: the build for ${want_key:0:12} already failed ($build_class); not retrying for ${FAILED_TTL_MIN} min (remove $BIN_PATH.failed-* to retry)" >&2
fi
# A compile error in a non-canonical checkout's own code stays an error: neither its earlier binary
# nor the canonical one stands in (only the canonical root keeps serving its own earlier binary).
if [[ "$build_only" != true && "$build_class" == compile && "$BIN_PATH" != "$CANON_BIN" ]]; then
  echo "run-codexkit-mcp.sh: this checkout does not compile; not serving its earlier or the canonical binary" >&2
  exit 1
fi
# A checkout with no usable build of its own serves the canonical binary, after an environmental
# failure or a held lock.
if [[ ! -x "$BIN_PATH" && "$build_only" != true ]]; then
  if [[ -x "$CANON_BIN" ]]; then
    echo "run-codexkit-mcp.sh: no usable build for this checkout; serving the canonical binary $CANON_BIN" >&2
    BIN_PATH="$CANON_BIN"
  else
    echo "run-codexkit-mcp.sh: no usable build and no canonical binary to serve" >&2
    exit 1
  fi
fi

if [[ "$build_only" == true ]]; then
  printf 'binary=%s\nrevision=%s\n' "$BIN_PATH" "${want_key:-unknown}"
  exit 0
fi

trap - EXIT TERM INT HUP
exec "$BIN_PATH" "$@"
