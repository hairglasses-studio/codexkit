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
HG_STUDIO_ROOT="${HG_STUDIO_ROOT:-$HOME/hairglasses-studio}"
HG_DOTFILES="${DOTFILES_DIR:-$HG_STUDIO_ROOT/dotfiles}"
HG_CODEXKIT="${CODEXKIT_ROOT:-$HG_STUDIO_ROOT/codexkit}"
# Keep the historic surfacekit variable as a compatibility alias while downstream callers migrate.
HG_SURFACEKIT="${SURFACEKIT_DIR:-$HG_STUDIO_ROOT/surfacekit}"
HG_STATE_DIR="${HG_STATE_DIR:-$HOME/.local/state/hg}"
mkdir -p "$HG_STATE_DIR" 2>/dev/null || true
