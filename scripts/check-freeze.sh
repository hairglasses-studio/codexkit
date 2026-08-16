#!/usr/bin/env bash
# Freeze guard for the standalone codexkit compatibility snapshot.
#
# DEPRECATED.md declares this repository frozen: active development moved into
# the centralized Codex harness, and only public-compatibility work belongs
# here. This script enforces that by refusing every commit except an
# explicitly allowlisted class.
#
# A commit is allowed only when BOTH hold:
#   1. its message carries a `Freeze-Exempt: <reason>` trailer whose reason is
#      in the closed vocabulary below, and
#   2. every path it changes matches that reason's path allowlist.
#
# The allowlists are allow-only (never deny-lists) so an unanticipated path
# fails closed with a message naming the path.
#
# Modes:
#   --commit-msg <file>   hook mode: judge the pending commit
#   --history [<range>]   CI mode: judge every commit since the freeze anchor
#   --verify-install      assert the hook is actually wired for this checkout
#
# The guard runs from git's `commit-msg` hook, which is the earliest stage that
# can see the commit message. `pre-commit` cannot: git has not composed the
# message yet, and .git/COMMIT_EDITMSG still holds the PREVIOUS commit's text
# at that point, so reading it there would allow a stale trailer through.

set -euo pipefail

readonly EXEMPT_TRAILER="Freeze-Exempt"
readonly HOOKS_DIR=".githooks"

usage() {
	cat <<'EOF'
usage: check-freeze.sh --commit-msg <file>
       check-freeze.sh --history [<range>]
       check-freeze.sh --verify-install

Exempt reasons and the paths each may touch:

  public-boundary-compat   Go sources, cmd/, public docs/examples, go.mod/sum,
                           CHANGELOG.md, scripts/check-public-boundary.sh
  freeze-guard-maintenance the freeze guard itself (.githooks/, its scripts)
  deprecation-doc          DEPRECATED.md, PUBLIC_BOUNDARY.md, README.md,
                           CHANGELOG.md, CONTRIBUTING.md
EOF
}

die() {
	printf 'freeze-guard: %s\n' "$*" >&2
	exit 1
}

repo_root() {
	git rev-parse --show-toplevel 2>/dev/null || die "not inside a git repository"
}

# Reason vocabulary. Unknown reasons fail closed.
reason_known() {
	case "$1" in
	public-boundary-compat | freeze-guard-maintenance | deprecation-doc) return 0 ;;
	*) return 1 ;;
	esac
}

# path_allowed <reason> <path>
path_allowed() {
	local reason="$1" path="$2"

	# Private fleet surfaces are never committable here under any reason.
	# PUBLIC_BOUNDARY.md excludes private overlays and generated mirrors, and
	# these are exactly the paths the fleet sprayers write into.
	case "$path" in
	.agents/agents/* | .agents/profiles/* | .agents/rules/* | .agents/subagents/*) return 1 ;;
	surface-audit.*) return 1 ;;
	esac

	case "$reason" in
	public-boundary-compat)
		case "$path" in
		*.go) return 0 ;;
		cmd/* | internal/* | examples/* | docs/*) return 0 ;;
		go.mod | go.sum) return 0 ;;
		CHANGELOG.md) return 0 ;;
		scripts/check-public-boundary.sh) return 0 ;;
		*) return 1 ;;
		esac
		;;
	freeze-guard-maintenance)
		case "$path" in
		"$HOOKS_DIR"/*) return 0 ;;
		scripts/check-freeze.sh | scripts/check-freeze-self-test.sh) return 0 ;;
		scripts/install-freeze-guard.sh) return 0 ;;
		scripts/local-ci.sh) return 0 ;;
		DEPRECATED.md | CHANGELOG.md) return 0 ;;
		*) return 1 ;;
		esac
		;;
	deprecation-doc)
		case "$path" in
		DEPRECATED.md | PUBLIC_BOUNDARY.md | README.md | CHANGELOG.md | CONTRIBUTING.md) return 0 ;;
		*) return 1 ;;
		esac
		;;
	*) return 1 ;;
	esac
}

# extract_reason: read a commit message on stdin, echo the trailer value (may be
# empty). git interpret-trailers handles comment stripping and continuation
# lines, so a `Freeze-Exempt:` mention in the body is not mistaken for a
# trailer.
extract_reason() {
	git interpret-trailers --parse |
		command sed -n "s/^${EXEMPT_TRAILER}:[[:space:]]*//p" |
		command sed -n '$p'
}

# judge <label> <reason> <paths-file>
# Emits findings and returns non-zero when the commit must be refused.
judge() {
	local label="$1" reason="$2" paths_file="$3"
	local path
	local bad=0

	if [[ -z "$reason" ]]; then
		printf 'freeze-guard: REFUSED %s\n' "$label" >&2
		printf '  This repository is FROZEN (see DEPRECATED.md).\n' >&2
		# shellcheck disable=SC2016  # backticks are literal markdown here
		printf '  Commits require a `%s: <reason>` trailer.\n' "$EXEMPT_TRAILER" >&2
		printf '  Active development belongs in the centralized Codex harness.\n' >&2
		usage >&2
		return 1
	fi

	if ! reason_known "$reason"; then
		printf 'freeze-guard: REFUSED %s\n' "$label" >&2
		printf '  Unknown exempt reason: %s\n' "$reason" >&2
		usage >&2
		return 1
	fi

	if [[ ! -s "$paths_file" ]]; then
		printf 'freeze-guard: REFUSED %s\n' "$label" >&2
		printf '  No changed paths; an exempt commit must change something.\n' >&2
		return 1
	fi

	while IFS= read -r path; do
		[[ -n "$path" ]] || continue
		if ! path_allowed "$reason" "$path"; then
			printf 'freeze-guard: path not allowed under %s: %s\n' "$reason" "$path" >&2
			bad=1
		fi
	done <"$paths_file"

	if [[ "$bad" -ne 0 ]]; then
		printf 'freeze-guard: REFUSED %s\n' "$label" >&2
		printf '  Every changed path must be in the %s allowlist.\n' "$reason" >&2
		usage >&2
		return 1
	fi

	printf 'freeze-guard: allowed %s (%s)\n' "$label" "$reason"
	return 0
}

mode_commit_msg() {
	local msg_file="${1:-}"
	[[ -n "$msg_file" ]] || die "--commit-msg requires the message file path"
	[[ -f "$msg_file" ]] || die "commit message file not found: $msg_file"

	local reason paths_file
	reason="$(extract_reason <"$msg_file")"
	paths_file="$(mktemp)"
	# shellcheck disable=SC2064  # expand paths_file now, not at trap time
	trap "rm -f '$paths_file'" RETURN

	git diff --cached --name-only >"$paths_file"

	local label="pending commit"
	if [[ ! -s "$paths_file" ]]; then
		# Nothing staged: this is a message-only commit, in practice
		# `git commit --amend` fixing wording. Judge the paths the commit
		# being rewritten already carries, so an exempt commit stays
		# amendable. The trailer is still re-validated, so an amend cannot
		# launder a non-exempt reason in.
		git diff --name-only HEAD^1 HEAD >"$paths_file" 2>/dev/null || true
		label="pending commit (message-only/amend)"
	fi

	judge "$label" "$reason" "$paths_file"
}

# freeze_anchor: the commit that introduced this guard. Enforcement starts
# after it, so the installing commit does not have to exempt itself and no
# hand-maintained baseline sha can drift. Moving or deleting this file is
# itself a commit the guard must clear.
freeze_anchor() {
	git log --diff-filter=A --format=%H -- scripts/check-freeze.sh |
		command sed -n '$p'
}

mode_history() {
	local range="${1:-}"
	local anchor
	if [[ -z "$range" ]]; then
		anchor="$(freeze_anchor)"
		if [[ -z "$anchor" ]]; then
			printf 'freeze-guard: no freeze anchor in history yet; nothing to enforce\n'
			return 0
		fi
		range="${anchor}..HEAD"
	fi

	local shas failures=0 sha reason paths_file subject
	shas="$(git rev-list "$range")"
	if [[ -z "$shas" ]]; then
		printf 'freeze-guard: no commits after the freeze anchor\n'
		return 0
	fi

	paths_file="$(mktemp)"
	# shellcheck disable=SC2064
	trap "rm -f '$paths_file'" RETURN

	while IFS= read -r sha; do
		[[ -n "$sha" ]] || continue
		reason="$(git log -1 --format=%B "$sha" | extract_reason)"
		subject="$(git log -1 --format=%s "$sha")"
		# First-parent diff: a merge is judged on what it brings in.
		git diff --name-only "${sha}^1" "$sha" >"$paths_file" 2>/dev/null ||
			git show --pretty=format: --name-only "$sha" >"$paths_file"
		# A commit that changes no file cannot breach the public boundary.
		# Skipping it here keeps a stray empty commit from wedging CI
		# permanently; commit-msg mode still refuses to create one.
		if [[ ! -s "$paths_file" ]]; then
			printf 'freeze-guard: skipped %s %s (no file changes)\n' \
				"$(git rev-parse --short "$sha")" "$subject"
			continue
		fi
		if ! judge "$(git rev-parse --short "$sha") ${subject}" "$reason" "$paths_file"; then
			failures=$((failures + 1))
		fi
	done <<<"$shas"

	if [[ "$failures" -ne 0 ]]; then
		printf 'freeze-guard: %d commit(s) since the freeze anchor violate the freeze\n' "$failures" >&2
		return 1
	fi
	printf 'freeze-guard: history clean since the freeze anchor\n'
	return 0
}

# The guard is only real if git will actually run it. core.hooksPath is set
# fleet-wide to a shared dotfiles directory, which OVERRIDES .git/hooks — so a
# hook dropped in .git/hooks here would be silently inert. This mode fails
# unless the wiring that git itself resolves points at our tracked hook.
mode_verify_install() {
	local root configured hook
	root="$(repo_root)"
	configured="$(git config --get core.hooksPath || true)"

	if [[ "$configured" != "$HOOKS_DIR" ]]; then
		printf 'freeze-guard: NOT INSTALLED\n' >&2
		printf '  core.hooksPath resolves to: %s\n' "${configured:-<unset, i.e. .git/hooks>}" >&2
		printf '  expected: %s\n' "$HOOKS_DIR" >&2
		printf '  install with: bash scripts/install-freeze-guard.sh\n' >&2
		return 1
	fi

	hook="${root}/${HOOKS_DIR}/commit-msg"
	if [[ ! -x "$hook" ]]; then
		printf 'freeze-guard: NOT INSTALLED\n' >&2
		printf '  %s/commit-msg is missing or not executable\n' "$HOOKS_DIR" >&2
		return 1
	fi

	printf 'freeze-guard: installed (core.hooksPath=%s, commit-msg executable)\n' "$HOOKS_DIR"
	return 0
}

main() {
	# Anchor on the script's own location, not the caller's CWD: invoked by
	# path from another directory, a CWD-relative resolution would silently
	# audit whatever repository the caller happened to be standing in.
	cd "$(dirname "${BASH_SOURCE[0]}")"
	cd "$(repo_root)"
	case "${1:---help}" in
	--commit-msg) mode_commit_msg "${2:-}" ;;
	--history) mode_history "${2:-}" ;;
	--verify-install) mode_verify_install ;;
	-h | --help)
		usage
		;;
	*) die "unknown mode: $1" ;;
	esac
}

main "$@"
