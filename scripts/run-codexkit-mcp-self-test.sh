#!/usr/bin/env bash
# Self-test for the cache-invalidation rule in run-codexkit-mcp.sh.
#
# Runs the REAL launcher (resolve-only and build-only modes) against a throwaway git repo with a
# trivial Go module and the REAL go toolchain, so vcs.revision comes from `go build`, not a stub.
#
# Red control: the same three cases are re-run against a copy of the launcher with the
# revision rule disabled (want_key forced empty, leaving only the old mtime trigger). The
# unstamped and stale-revision cases must then wrongly report "cached"; if they do not, the
# fixture cannot tell the rule from the mtime trigger and the test fails.
#
# What these fixtures cannot cover: a large module, concurrent launchers, or a read-only cache dir.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
launcher="$repo_root/scripts/run-codexkit-mcp.sh"
tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT

passes=0
failures=0
ok() {
	printf '  PASS %s\n' "$*"
	passes=$((passes + 1))
}
bad() {
	printf '  FAIL %s\n' "$*"
	failures=$((failures + 1))
}
expect() { # name want got
	if [[ "$2" == "$3" ]]; then ok "$1 -> $3"; else bad "$1: want $2, got $3"; fi
}

gocache="${GOCACHE:-$(go env GOCACHE)}"
fx="$tmp_root/fx"
cache="$tmp_root/cache"
bin="$cache/codexkit-mcp/bin/codexkit-mcp"
run_env=(env -u CODEXKIT_WORKSPACE_ROOT -u HAIRGLASSES_STUDIO_ROOT HOME="$tmp_root/home" XDG_CACHE_HOME="$cache" GOCACHE="$gocache" GOWORK=off GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null)
fxgit() { git -C "$fx" -c user.name=selftest -c user.email=selftest@example.com -c commit.gpgsign=false "$@"; }

new_fixture() { # launcher-to-install
	rm -rf "$fx" "$cache" "${tmp_root:?}/home"
	mkdir -p "$fx/scripts" "$fx/cmd/codexkit-mcp" "$tmp_root/home"
	cp "$1" "$fx/scripts/run-codexkit-mcp.sh"
	printf 'module example.com/fx\n\ngo 1.20\n' >"$fx/go.mod"
	printf 'package main\n\nfunc main() {}\n' >"$fx/cmd/codexkit-mcp/main.go"
	git -C "$fx" init -q
	fxgit add go.mod cmd
	fxgit commit -q -m one
}
build_cached() { # go-build-flags...
	mkdir -p "$(dirname "$bin")"
	(cd "$fx" && "${run_env[@]}" go build "$@" -o "$bin" ./cmd/codexkit-mcp)
	touch -d '1 minute' "$bin" # newer than every source file: only the revision rule may trigger a rebuild
}
resolve() { "${run_env[@]}" CODEXKIT_MCP_RESOLVE_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" | sed -n 's/^action=//p'; }
build_only() { # name
	if "${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1; then ok "$1"; else bad "$1"; fi
}
cached_revision() { go version -m "$bin" | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p'; }

scenarios() { # launcher-to-install; sets act_unstamped act_head act_stale
	new_fixture "$1"
	build_cached -buildvcs=false
	act_unstamped="$(resolve)"
	build_cached
	act_head="$(resolve)"
	printf 'package main\n\nfunc main() { _ = 1 }\n' >"$fx/cmd/codexkit-mcp/main.go"
	fxgit commit -q -am two
	touch -d '1 minute' "$bin"
	act_stale="$(resolve)"
}

echo "launcher rules"
scenarios "$launcher"
expect "unstamped cached binary" rebuild "$act_unstamped"
expect "stamped, revision equals HEAD" cached "$act_head"
expect "stamped, revision differs from HEAD" rebuild "$act_stale"

# Same fixture, continued: dirty tree and the build-only path.
printf 'package main\n\nfunc main() { _ = 2 }\n' >"$fx/cmd/codexkit-mcp/main.go"
expect "dirty tree differs from cached key" rebuild "$(resolve)"
build_only "build-only exits 0"
expect "dirty tree after build-only" cached "$(resolve)"
printf 'package main\n\nfunc main() { _ = 3 }\n' >"$fx/cmd/codexkit-mcp/main.go"
expect "dirty tree edited again" rebuild "$(resolve)"
fxgit commit -q -am three
build_only "build-only after commit exits 0"
expect "cache revision equals HEAD after build" "$(fxgit rev-parse HEAD)" "$(cached_revision)"
expect "clean tree after build-only" cached "$(resolve)"
leftovers="$(find "$(dirname "$bin")" -name '*.tmp.*' | wc -l)"
expect "no temp files left in the cache dir" 0 "$leftovers"
printf 'package main\n\nfunc main() { broken\n' >"$fx/cmd/codexkit-mcp/main.go"
fxgit commit -q -am broken
fallback_note="$("${run_env[@]}" bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 >/dev/null || echo "rc=$?")"
case "$fallback_note" in
*"serving the existing binary"*) ok "failed build falls back to the existing binary" ;;
*) bad "failed build did not fall back: $fallback_note" ;;
esac

echo "red control: revision rule disabled"
mutant="$tmp_root/mutant.sh"
# shellcheck disable=SC2016
sed 's/^want_key="\$(source_key)"$/want_key=""/' "$launcher" >"$mutant"
if cmp -s "$launcher" "$mutant"; then
	bad "mutation did not apply (launcher text changed?)"
else
	scenarios "$mutant"
	expect "red control: unstamped binary goes unnoticed" cached "$act_unstamped"
	expect "red control: stale revision goes unnoticed" cached "$act_stale"
fi

printf '%d passed, %d failed\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]
