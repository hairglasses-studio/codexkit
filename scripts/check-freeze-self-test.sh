#!/usr/bin/env bash
# Self-test for the freeze guard.
#
# This test runs REAL `git commit` invocations in a throwaway clone against the
# REAL hook mechanism (core.hooksPath -> .githooks -> commit-msg). It does not
# call check-freeze.sh directly for the commit scenarios, because a guard that
# passes when invoked by hand but never runs during an actual commit is exactly
# the false green this repo's fleet has shipped before.
#
# It includes a negative control: the same refused commit is first proven to
# SUCCEED with the guard uninstalled. Without that control, a deny result could
# come from a broken fixture rather than from the guard.
#
# What these fixtures cannot cover: they exercise a small, freshly created
# history, so they say nothing about behavior that depends on repository size
# or on concurrent commits.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT

clone="${tmp_root}/clone"
passes=0
failures=0

ok() {
	printf '  PASS %s\n' "$*"
	passes=$((passes + 1))
}

bad() {
	printf '  FAIL %s\n' "$*" >&2
	failures=$((failures + 1))
}

head_sha() {
	git -C "$clone" rev-parse HEAD
}

# expect_commit <expect:allow|deny> <label> <message>
# Stages nothing itself; the caller stages first.
expect_commit() {
	local expect="$1" label="$2" message="$3"
	local before after status out
	before="$(head_sha)"
	set +e
	out="$(git -C "$clone" commit -m "$message" 2>&1)"
	status=$?
	set -e
	after="$(head_sha)"

	case "$expect" in
	allow)
		if [[ "$status" -eq 0 && "$before" != "$after" ]]; then
			ok "$label (commit created)"
		else
			bad "$label (expected commit, status=$status)"
			printf '%s\n' "$out" >&2
		fi
		;;
	deny)
		if [[ "$status" -ne 0 && "$before" == "$after" ]]; then
			if printf '%s' "$out" | command grep -q 'freeze-guard: REFUSED'; then
				ok "$label (refused by freeze guard, HEAD unmoved)"
			else
				bad "$label (rejected, but not by the freeze guard)"
				printf '%s\n' "$out" >&2
			fi
		else
			bad "$label (expected refusal, status=$status)"
			printf '%s\n' "$out" >&2
		fi
		;;
	esac
}

reset_to() {
	git -C "$clone" reset --hard --quiet "$1"
	git -C "$clone" clean -fdq
}

printf '== setting up throwaway clone ==\n'
git clone --quiet --no-hardlinks "$repo_root" "$clone"
git -C "$clone" config user.email "freeze-test@example.com"
git -C "$clone" config user.name "Freeze Guard Self Test"

# Install the CURRENT working-tree guard (committed or not) into the clone, so
# the test always exercises the code under development.
mkdir -p "${clone}/.githooks" "${clone}/scripts"
cp -a "${repo_root}/.githooks/." "${clone}/.githooks/"
cp "${repo_root}/scripts/check-freeze.sh" "${clone}/scripts/check-freeze.sh"
cp "${repo_root}/scripts/install-freeze-guard.sh" "${clone}/scripts/install-freeze-guard.sh"

# Seed commit installs the guard files under test. It carries the exempt
# trailer and touches only allowlisted paths so that --history stays valid
# whether the seed IS the freeze anchor (guard not yet committed upstream) or a
# descendant of it (guard already in HEAD). --no-verify because the hook is not
# installed yet; --allow-empty because the copy is a no-op once the working-tree
# guard matches HEAD.
git -C "$clone" add -A
if git -C "$clone" diff --cached --quiet; then
	# The copy was a no-op: HEAD already carries the guard under test, so the
	# freeze anchor is upstream and no seed commit is needed.
	printf 'seed: working-tree guard matches HEAD; no seed commit needed\n'
else
	git -C "$clone" commit --quiet --no-verify \
		-m "$(printf 'seed: install freeze guard files\n\nFreeze-Exempt: freeze-guard-maintenance')"
fi
anchor="$(head_sha)"
printf 'anchor: %s\n' "$anchor"

printf '\n== negative control: guard NOT installed ==\n'
printf 'control\n' >>"${clone}/CHANGELOG.md"
git -C "$clone" add CHANGELOG.md
expect_commit allow "plain commit succeeds while guard is uninstalled" "chore: no trailer, no guard"
reset_to "$anchor"

if bash "${clone}/scripts/check-freeze.sh" --verify-install >/dev/null 2>&1; then
	bad "--verify-install should fail before installation"
else
	ok "--verify-install fails before installation"
fi

printf '\n== installing guard ==\n'
(cd "$clone" && bash scripts/install-freeze-guard.sh)

if bash "${clone}/scripts/check-freeze.sh" --verify-install >/dev/null; then
	ok "--verify-install passes after installation"
else
	bad "--verify-install should pass after installation"
fi

printf '\n== DENY paths ==\n'
printf 'nope\n' >>"${clone}/CHANGELOG.md"
git -C "$clone" add CHANGELOG.md
expect_commit deny "commit without a Freeze-Exempt trailer" "docs: update changelog"
reset_to "$anchor"

mkdir -p "${clone}/.agents/agents"
printf 'sprayed\n' >"${clone}/.agents/agents/sprayed.md"
git -C "$clone" add .agents/agents/sprayed.md
expect_commit deny "valid trailer but private sprayed path" \
	"$(printf 'fix: compat\n\nFreeze-Exempt: public-boundary-compat')"
reset_to "$anchor"

printf 'nope\n' >>"${clone}/CHANGELOG.md"
git -C "$clone" add CHANGELOG.md
expect_commit deny "unknown exempt reason" \
	"$(printf 'fix: compat\n\nFreeze-Exempt: because-i-said-so')"
reset_to "$anchor"

printf 'package skillsync\n' >"${clone}/scripts/not-a-go-file.sh"
git -C "$clone" add scripts/not-a-go-file.sh
expect_commit deny "path outside the reason allowlist" \
	"$(printf 'fix: compat\n\nFreeze-Exempt: deprecation-doc')"
reset_to "$anchor"

printf '\n== ALLOW paths ==\n'
printf '\n// freeze-guard self-test marker\n' >>"${clone}/module.go"
git -C "$clone" add module.go
expect_commit allow "public-boundary-compat touching a Go source file" \
	"$(printf 'fix(mcpserver): port approval enum\n\nFreeze-Exempt: public-boundary-compat')"
allowed_sha="$(head_sha)"

if git -C "$clone" log -1 --format=%B "$allowed_sha" |
	git -C "$clone" interpret-trailers --parse |
	command grep -q '^Freeze-Exempt: public-boundary-compat$'; then
	ok "allowed commit carries the parsed trailer"
else
	bad "allowed commit is missing the parsed trailer"
fi

printf '# maintenance touch\n' >>"${clone}/scripts/check-freeze.sh"
git -C "$clone" add scripts/check-freeze.sh
expect_commit allow "freeze-guard-maintenance touching the guard itself" \
	"$(printf 'chore(freeze): tune guard\n\nFreeze-Exempt: freeze-guard-maintenance')"

printf '\n== amend (message-only commit) ==\n'
# `git commit --amend` stages nothing, so a naive staged-paths check sees an
# empty set. An exempt commit must stay amendable; a non-exempt reason must not
# sneak in through the rewrite.
before_amend="$(head_sha)"
if git -C "$clone" commit --amend --quiet \
	-m "$(printf 'chore(freeze): tune guard, reworded\n\nFreeze-Exempt: freeze-guard-maintenance')" 2>&1; then
	if [[ "$(head_sha)" != "$before_amend" ]]; then
		ok "amend of an exempt commit is allowed"
	else
		bad "amend reported success but HEAD did not move"
	fi
else
	bad "amend of an exempt commit should be allowed"
fi

before_amend="$(head_sha)"
set +e
amend_out="$(git -C "$clone" commit --amend \
	-m "$(printf 'chore: launder\n\nFreeze-Exempt: because-i-said-so')" 2>&1)"
amend_status=$?
set -e
if [[ "$amend_status" -ne 0 && "$(head_sha)" == "$before_amend" ]] &&
	printf '%s' "$amend_out" | command grep -q 'freeze-guard: REFUSED'; then
	ok "amend cannot launder an unknown exempt reason"
else
	bad "amend with an unknown reason should be refused"
	printf '%s\n' "$amend_out" >&2
fi

printf '\n== history mode ==\n'
if (cd "$clone" && bash scripts/check-freeze.sh --history >/dev/null); then
	ok "--history clean over allowlisted commits"
else
	bad "--history should pass over allowlisted commits"
fi

clean_sha="$(head_sha)"
printf 'bypassed\n' >>"${clone}/CHANGELOG.md"
git -C "$clone" add CHANGELOG.md
git -C "$clone" commit --quiet --no-verify -m "chore: sneak past the hook"
if (cd "$clone" && bash scripts/check-freeze.sh --history >/dev/null 2>&1); then
	bad "--history should catch a --no-verify bypass"
else
	ok "--history catches a --no-verify bypass"
fi
reset_to "$clean_sha"

printf '\n== summary ==\n'
printf 'passed: %d  failed: %d\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]
