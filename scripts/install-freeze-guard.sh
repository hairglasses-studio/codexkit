#!/usr/bin/env bash
# Install the freeze guard for this checkout.
#
# Git hook wiring is per-checkout local config, so it cannot be shipped in the
# tree alone. This script performs the one mutation that makes the tracked hook
# live, then verifies git actually resolves it — an installed-but-inert hook is
# the failure mode this repo's fleet has hit repeatedly.
#
# HAZARD (linked worktrees): unless extensions.worktreeConfig is enabled,
# core.hooksPath is repo-local config SHARED by every worktree of this
# repository. Installing from a worktree whose branch carries .githooks/ also
# repoints checkouts whose branch does NOT, and git silently runs no hooks at
# all when the configured directory is missing. Run `check-freeze.sh
# --verify-install` in any checkout you care about: it fails loudly when the
# configured directory does not actually contain an executable commit-msg.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

previous="$(git config --get core.hooksPath || true)"
printf 'freeze-guard: core.hooksPath was: %s\n' "${previous:-<unset, i.e. .git/hooks>}"

git config --local core.hooksPath .githooks
chmod +x .githooks/commit-msg .githooks/pre-commit

printf 'freeze-guard: core.hooksPath now: %s\n' "$(git config --get core.hooksPath)"
printf 'freeze-guard: fleet hooks remain reachable via .githooks delegation\n'

bash scripts/check-freeze.sh --verify-install
