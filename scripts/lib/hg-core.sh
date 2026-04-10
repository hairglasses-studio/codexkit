#!/usr/bin/env bash
# hg-core.sh — Shared framework for codexkit parity shell entrypoints.
# Source this file: source "$(dirname "$0")/lib/hg-core.sh"

# ── Snazzy palette (24-bit true color) ─────────
HG_CYAN=$'\033[38;2;87;199;255m'
HG_GREEN=$'\033[38;2;90;247;142m'
HG_MAGENTA=$'\033[38;2;255;106;193m'
HG_YELLOW=$'\033[38;2;243;249;157m'
HG_RED=$'\033[38;2;255;92;87m'
HG_DIM=$'\033[38;5;243m'
HG_BOLD=$'\033[1m'
HG_RESET=$'\033[0m'

# ── Formatted output ──────────────────────────
hg_info()  { printf "%s[info]%s  %s\n" "$HG_CYAN"   "$HG_RESET" "$1"; }
hg_ok()    { printf "%s[ok]%s    %s\n" "$HG_GREEN"  "$HG_RESET" "$1"; }
hg_warn()  { printf "%s[warn]%s  %s\n" "$HG_YELLOW" "$HG_RESET" "$1"; }
hg_error() { printf "%s[err]%s   %s\n" "$HG_RED"    "$HG_RESET" "$1" >&2; }
hg_die()   { hg_error "$1"; exit "${2:-1}"; }

# ── Require commands ───────────────────────────
hg_require() {
  for cmd in "$@"; do
    command -v "$cmd" &>/dev/null || hg_die "$cmd is required but not installed"
  done
}

# ── Paths ──────────────────────────────────────
_hg_core_path="${BASH_SOURCE[0]:-$0}"
_hg_core_dir="$(cd "$(dirname "$_hg_core_path")" && pwd)"
_hg_core_codexkit="$(cd "$_hg_core_dir/../.." && pwd)"
_hg_core_studio="$(cd "$_hg_core_codexkit/.." && pwd)"

_hg_core_codexkit_is_valid() {
  local root="${1:-}"
  [[ -n "$root" ]] && [[ -d "$root/scripts" ]] && [[ -f "$root/AGENTS.md" ]]
}

_hg_core_studio_is_valid() {
  local root="${1:-}"
  _hg_core_codexkit_is_valid "$root/codexkit"
}

if [[ -n "${HG_STUDIO_ROOT:-}" ]] && _hg_core_studio_is_valid "${HG_STUDIO_ROOT}"; then
  HG_STUDIO_ROOT="$(cd "${HG_STUDIO_ROOT}" && pwd)"
  HG_CODEXKIT="${HG_STUDIO_ROOT}/codexkit"
elif [[ -n "${CODEXKIT_ROOT:-}" ]] && _hg_core_codexkit_is_valid "${CODEXKIT_ROOT}"; then
  HG_CODEXKIT="$(cd "${CODEXKIT_ROOT}" && pwd)"
  HG_STUDIO_ROOT="$(cd "${HG_CODEXKIT}/.." && pwd)"
elif _hg_core_codexkit_is_valid "$_hg_core_codexkit"; then
  HG_STUDIO_ROOT="$_hg_core_studio"
  HG_CODEXKIT="$_hg_core_codexkit"
else
  HG_STUDIO_ROOT="$HOME/hairglasses-studio"
  HG_CODEXKIT="$HG_STUDIO_ROOT/codexkit"
fi

if [[ -n "${DOTFILES_DIR:-}" ]] && [[ -d "${DOTFILES_DIR}/scripts" ]] && [[ -f "${DOTFILES_DIR}/AGENTS.md" ]]; then
  HG_DOTFILES="$(cd "${DOTFILES_DIR}" && pwd)"
else
  HG_DOTFILES="$HG_STUDIO_ROOT/dotfiles"
fi

# Keep the historic surfacekit variable as a compatibility alias while downstream callers migrate.
if [[ -n "${SURFACEKIT_DIR:-}" ]] && [[ -d "${SURFACEKIT_DIR}/scripts" ]] && [[ -f "${SURFACEKIT_DIR}/AGENTS.md" ]]; then
  HG_SURFACEKIT="$(cd "${SURFACEKIT_DIR}" && pwd)"
else
  HG_SURFACEKIT="$HG_STUDIO_ROOT/surfacekit"
fi
HG_STATE_DIR="${HG_STATE_DIR:-$HOME/.local/state/hg}"
mkdir -p "$HG_STATE_DIR" 2>/dev/null || true
