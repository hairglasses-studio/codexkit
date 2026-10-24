#!/usr/bin/env bash
# Self-test for the cache rules in run-codexkit-mcp.sh.
#
# Runs the REAL launcher (resolve-only and build-only modes, plus a normal launch) against a
# throwaway git repo with a trivial Go module and the REAL go toolchain, so vcs.revision comes
# from `go build`, not a stub. The only stubs are a `go` shim that hangs mid-build (SIGTERM
# case) and failing sha256 tools (fail-closed case).
#
# Red controls: the launcher is re-run with one rule disabled at a time (revision rule, dirty key,
# cross-checkout path). Each mutant must produce the WRONG answer for its case; if it does not,
# the fixture cannot tell the rule from the other triggers and the test fails.
#
# What these fixtures cannot cover: a large module, a read-only cache dir, macOS (no flock, no
# timeout), and the real `error obtaining VCS status` failure.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
launcher="$repo_root/scripts/run-codexkit-mcp.sh"
repo_dir="codexkit"
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

real_go="$(command -v go)"
gocache="${GOCACHE:-$(go env GOCACHE)}"
home="$tmp_root/home"
fx="$home/hairglasses-studio/$repo_dir" # the canonical checkout for HOME=$home
cache="$tmp_root/cache"
bin="$cache/codexkit-mcp/bin/codexkit-mcp"
run_env=(env -u CODEXKIT_WORKSPACE_ROOT -u HAIRGLASSES_STUDIO_ROOT -u GIT_OPTIONAL_LOCKS HOME="$home" XDG_CACHE_HOME="$cache" GOCACHE="$gocache" GOWORK=off GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null)
fxgit() { git -C "$fx" -c user.name=selftest -c user.email=selftest@example.com -c commit.gpgsign=false "$@"; }
setmain() { printf 'package main\n\nfunc main() { _ = %s }\n' "$1" >"$fx/cmd/codexkit-mcp/main.go"; }

new_fixture() { # launcher-to-install
	rm -rf "$fx" "$cache"
	mkdir -p "$fx/scripts" "$fx/cmd/codexkit-mcp"
	cp "$1" "$fx/scripts/run-codexkit-mcp.sh"
	printf 'module example.com/fx\n\ngo 1.20\n' >"$fx/go.mod"
	setmain 0
	git -C "$fx" init -q
	fxgit add go.mod cmd
	fxgit commit -q -m one
}
build_cached() { # go-build-flags...
	mkdir -p "$(dirname "$bin")"
	(cd "$fx" && "${run_env[@]}" go build "$@" -o "$bin" ./cmd/codexkit-mcp)
	touch -d '1 minute' "$bin" # newer than every source file: only the key rules may trigger a rebuild
}
resolve_all() { "${run_env[@]}" CODEXKIT_MCP_RESOLVE_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh"; }
resolve() { resolve_all | sed -n 's/^action=//p'; }
build_only() { # name
	if "${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1; then ok "$1"; else bad "$1"; fi
}
cached_revision() { go version -m "$bin" | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p'; }
mutate() { # sed-expr out
	sed "$1" "$launcher" >"$2"
	if cmp -s "$launcher" "$2"; then bad "mutation '$1' did not apply (launcher text changed?)"; return 1; fi
}

scenarios() { # launcher-to-install; sets act_unstamped act_head act_stale act_dirtykey act_wt
	new_fixture "$1"
	build_cached -buildvcs=false
	act_unstamped="$(resolve)"
	build_cached
	act_head="$(resolve)"
	setmain 1
	fxgit commit -q -am two
	touch -d '1 minute' "$bin"
	act_stale="$(resolve)"
	# Dirty key alone: build the dirty tree, make the binary newer than the sources, then edit again
	# and touch the binary once more, so the mtime trigger cannot fire.
	setmain 2
	build_only "dirty build-only"
	setmain 3
	touch -d '2 minutes' "$bin"
	act_dirtykey="$(resolve)"
	# A second checkout of the same repo must not share the canonical binary path.
	fxgit worktree add -q "$tmp_root/wt" -b wt
	cp "$1" "$tmp_root/wt/scripts/run-codexkit-mcp.sh" 2>/dev/null || { mkdir -p "$tmp_root/wt/scripts"; cp "$1" "$tmp_root/wt/scripts/run-codexkit-mcp.sh"; }
	act_wt="$("${run_env[@]}" CODEXKIT_MCP_RESOLVE_ONLY=1 bash "$tmp_root/wt/scripts/run-codexkit-mcp.sh" | sed -n 's/^binary=//p')"
	fxgit worktree remove --force "$tmp_root/wt"
}

echo "launcher rules"
scenarios "$launcher"
expect "unstamped cached binary" rebuild "$act_unstamped"
expect "stamped, revision equals HEAD" cached "$act_head"
expect "stamped, revision differs from HEAD" rebuild "$act_stale"
expect "dirty key alone (binary newer than sources)" rebuild "$act_dirtykey"
if [[ "$act_wt" != "$bin" && "$act_wt" =~ /codexkit-mcp/bin/wt-[0-9a-f]{8}/codexkit-mcp$ ]]; then ok "worktree root gets its own path -> $act_wt"; else bad "worktree root path: $act_wt"; fi
expect "canonical root keeps the shared path" "$bin" "$(resolve_all | sed -n 's/^binary=//p')"

# Same fixture, continued.
setmain 4
build_only "build-only on a dirty tree exits 0"
touch -d '3 minutes' "$bin"
expect "dirty tree after build-only" cached "$(resolve)"
fxgit commit -q -am four
expect "committing the built tree changes the key" rebuild "$(resolve)"
build_only "build-only after commit exits 0"
expect "cache revision equals HEAD after build" "$(fxgit rev-parse HEAD)" "$(cached_revision)"
expect "clean tree after build-only" cached "$(resolve)"
expect "key sidecar equals HEAD" "$(fxgit rev-parse HEAD)" "$(cat "$bin.key")"
expect "no temp files left in the cache dir" 0 "$(find "$(dirname "$bin")" -name '*.tmp.*' | wc -l)"

# Mode switches: only "1" counts, and none of them (nor GIT_OPTIONAL_LOCKS) reaches the server.
printf 'package main\n\nimport (\n\t"fmt"\n\t"os"\n)\n\nfunc main() {\n\tfmt.Printf("env:%%s|%%s|%%s\\n", os.Getenv("GIT_OPTIONAL_LOCKS"), os.Getenv("CODEXKIT_MCP_BUILD_ONLY"), os.Getenv("CODEXKIT_MCP_RESOLVE_ONLY"))\n}\n' >"$fx/cmd/codexkit-mcp/main.go"
fxgit commit -q -am envprobe
build_only "build the env probe"
leak="$("${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=0 CODEXKIT_MCP_RESOLVE_ONLY=0 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>/dev/null || true)"
expect "=0 mode switches start the server and are not leaked to it" "env:||" "$leak"
leak="$("${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>/dev/null)"
expect "BUILD_ONLY=1 prints the binary and starts no server" "binary=$bin" "${leak%%$'\n'*}" # no head in a pipeline: SIGPIPE under pipefail

# Fail closed: a dirty tree whose hash cannot be computed rebuilds; a clean tree needs no hash.
shim_sha="$tmp_root/shim-sha"
mkdir -p "$shim_sha"
printf '#!/bin/sh\nexit 1\n' >"$shim_sha/sha256sum"
cp "$shim_sha/sha256sum" "$shim_sha/shasum"
chmod +x "$shim_sha/sha256sum" "$shim_sha/shasum"
expect "clean tree with no working sha256 stays cached" cached "$(PATH="$shim_sha:$PATH" resolve)"
setmain 5
touch -d '4 minutes' "$bin"
expect "dirty tree with no working sha256 rebuilds" rebuild "$(PATH="$shim_sha:$PATH" resolve)"
expect "...and reports the revision as unknown" unknown "$(PATH="$shim_sha:$PATH" resolve_all | sed -n 's/^revision=//p')"
fxgit checkout -q -- cmd

# Lock held by another launcher: serve the existing binary and leave the cache alone.
if command -v flock >/dev/null 2>&1; then
	build_cached -buildvcs=false
	rm -f "$bin.key"
	( exec 9>>"$bin.lock"; flock -n 9; exec sleep 60 ) &
	holder=$!
	sleep 0.5
	note="$("${run_env[@]}" bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 >/dev/null || true)"
	kill "$holder" 2>/dev/null || true
	wait "$holder" 2>/dev/null || true
	case "$note" in
	*"another launcher is rebuilding"*) ok "lock held: existing binary is served" ;;
	*) bad "lock held: no fallback note: $note" ;;
	esac
	expect "lock held: cached binary untouched" "" "$(cached_revision)"
else
	echo "  SKIP lock-held case (flock not installed)"
fi

# SIGTERM mid-build: no temp files, no stray build process.
shim_go="$tmp_root/shim-go"
mkdir -p "$shim_go"
cat >"$shim_go/go" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == build ]]; then
	while [[ \$# -gt 0 ]]; do
		if [[ "\$1" == -o ]]; then out="\$2"; fi
		shift
	done
	head -c 100000 /dev/zero >"\$out"
	sleep 60
fi
exec "$real_go" "\$@"
EOF
chmod +x "$shim_go/go"
rm -f "$bin" "$bin.key"
PATH="$shim_go:$PATH" "${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1 &
launcher_pid=$!
for _ in $(seq 1 100); do
	if [[ -n "$(find "$(dirname "$bin")" -name '*.tmp.*' 2>/dev/null)" ]]; then break; fi
	sleep 0.1
done
expect "mid-build: temp file exists" 1 "$(find "$(dirname "$bin")" -name '*.tmp.*' | wc -l)"
kill -TERM "$launcher_pid"
wait "$launcher_pid" 2>/dev/null || true
expect "SIGTERM mid-build leaves no temp files" 0 "$(find "$(dirname "$bin")" -name '*.tmp.*' | wc -l)"
expect "SIGTERM mid-build leaves no cache binary" 0 "$(find "$(dirname "$bin")" -name 'codexkit-mcp' | wc -l)"
build_only "rebuild after the killed build"

# Negative cache: a failed build is not retried for the same key, and a new key retries.
setmain 6
fxgit commit -q -am six
build_only "build the good tree"
printf 'package main\n\nfunc main() { broken\n' >"$fx/cmd/codexkit-mcp/main.go"
fxgit commit -q -am broken
note="$("${run_env[@]}" bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 >/dev/null || true)"
case "$note" in
*"serving the existing binary"*) ok "failed build falls back to the existing binary" ;;
*) bad "failed build did not fall back: $note" ;;
esac
if [[ -e "$bin.failed-$(fxgit rev-parse HEAD)" ]]; then ok "failure recorded for the key"; else bad "no failure marker"; fi
expect "known-failed key does not rebuild" cached "$(resolve)"
note="$("${run_env[@]}" bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 >/dev/null || true)"
case "$note" in
*"already failed"*) ok "known-failed key serves the existing binary with one note" ;;
*) bad "no negative-cache note: $note" ;;
esac
touch -d '20 minutes ago' "$bin.failed-$(fxgit rev-parse HEAD)"
expect "an expired failure marker (transient failure) retries" rebuild "$(resolve)"
touch "$bin.failed-$(fxgit rev-parse HEAD)"

# A worktree whose own build fails serves the canonical binary (here: the env probe's successor).
fxgit worktree add -q "$tmp_root/wt2" -b wt2
mkdir -p "$tmp_root/wt2/scripts"
cp "$launcher" "$tmp_root/wt2/scripts/run-codexkit-mcp.sh"
wt2_run() { "${run_env[@]}" bash "$tmp_root/wt2/scripts/run-codexkit-mcp.sh" </dev/null 2>"$tmp_root/wt2.err" || true; }
wt2_out="$(wt2_run)"
case "$(cat "$tmp_root/wt2.err")" in
*"serving the canonical binary"*) ok "worktree build fails -> canonical binary served" ;;
*) bad "worktree fallback: $(cat "$tmp_root/wt2.err")" ;;
esac
expect "...and the canonical binary ran (built from the earlier good tree: no env line)" "" "$wt2_out"
wt2_dir="$(dirname "$bin")/wt-$(printf '%s' "$(cd "$tmp_root/wt2" && pwd -P)" | sha256sum | cut -c1-8)"
expect "...the worktree dir records its root" "$(cd "$tmp_root/wt2" && pwd -P)" "$(cat "$wt2_dir/root")"
wt2_out="$(wt2_run)"
case "$(cat "$tmp_root/wt2.err")" in
*"already failed"*"canonical binary"* | *"canonical binary"*"already failed"*) ok "worktree: a repeat launch skips the failed build" ;;
*) bad "worktree negative cache: $(cat "$tmp_root/wt2.err")" ;;
esac

# Pruning: dirs whose recorded root is gone go; live roots and dirs with no root file stay.
mkdir -p "$(dirname "$bin")/wt-0000dead" "$(dirname "$bin")/wt-0000live" "$(dirname "$bin")/wt-0000none"
printf '%s\n' "$tmp_root/gone" >"$(dirname "$bin")/wt-0000dead/root"
printf '%s\n' "$tmp_root" >"$(dirname "$bin")/wt-0000live/root"
fxgit worktree remove --force "$tmp_root/wt2"
setmain 7
fxgit commit -q -am seven
expect "a new key retries" rebuild "$(resolve)"
build_only "rebuild prunes"
expect "prune: dir with a vanished root is removed" 0 "$(find "$(dirname "$bin")" -maxdepth 1 -name 'wt-0000dead' | wc -l)"
expect "prune: the removed worktree's dir is removed" 0 "$(find "$(dirname "$bin")" -maxdepth 1 -name "$(basename "$wt2_dir")" | wc -l)"
expect "prune: dir with a live root stays" 1 "$(find "$(dirname "$bin")" -maxdepth 1 -name 'wt-0000live' | wc -l)"
expect "prune: dir with no root file stays" 1 "$(find "$(dirname "$bin")" -maxdepth 1 -name 'wt-0000none' | wc -l)"
build_only "build after the fix"
expect "failure marker cleared on success" 0 "$(find "$(dirname "$bin")" -name '*.failed-*' | wc -l)"

# A worktree has no ../dep sibling: the build points the replace at the canonical sibling checkout.
dep="$home/hairglasses-studio/dep"
mkdir -p "$dep"
printf 'module example.com/dep\n\ngo 1.20\n' >"$dep/go.mod"
printf 'package dep\n' >"$dep/dep.go"
printf 'module example.com/fx\n\ngo 1.20\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n' >"$fx/go.mod"
printf 'package main\n\nimport _ "example.com/dep"\n\nfunc main() {}\n' >"$fx/cmd/codexkit-mcp/main.go"
fxgit commit -q -am withdep
build_only "canonical build with a ../dep replace"
fxgit worktree add -q "$tmp_root/wt3" -b wt3
mkdir -p "$tmp_root/wt3/scripts"
cp "$launcher" "$tmp_root/wt3/scripts/run-codexkit-mcp.sh"
wt3_build() { "${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wt3/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 | tail -n 3 || true; }
wt3_out="$(wt3_build)"
case "$wt3_out" in
*"binary="*"/wt-"*) ok "worktree builds when ../dep is missing (temp modfile)" ;;
*) bad "worktree modfile build: $wt3_out" ;;
esac
wt3_bin="$(printf '%s\n' "$wt3_out" | sed -n 's/^binary=//p')"
expect "...and its binary is stamped with the worktree HEAD" "$(git -C "$tmp_root/wt3" rev-parse HEAD)" "$(go version -m "$wt3_bin" | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p')"
# Control: without the sibling to point at, the same build fails.
rm -rf "$(dirname "$wt3_bin")"
mv "$dep" "$dep.off"
wt3_out="$(wt3_build)"
case "$wt3_out" in
*"build failed"*) ok "red control: worktree build fails when the sibling is missing" ;;
*) bad "red control: expected a failed build: $wt3_out" ;;
esac
mv "$dep.off" "$dep"
fxgit worktree remove --force "$tmp_root/wt3"

echo "red control: revision rule disabled"
mutant="$tmp_root/mutant.sh"
# shellcheck disable=SC2016
if mutate 's/^  want_key="\$(source_key)" || .*$/  want_key=""; key_unknown=false/' "$mutant"; then
	scenarios "$mutant"
	expect "red control: unstamped binary goes unnoticed" cached "$act_unstamped"
	expect "red control: stale revision goes unnoticed" cached "$act_stale"
fi

echo "red control: dirty key disabled"
# shellcheck disable=SC2016
if mutate 's/\[\[ "\$have_key" == "\$want_key" \]\] || reason="uncommitted/true || reason="uncommitted/' "$mutant"; then
	scenarios "$mutant"
	expect "red control: dirty key alone goes unnoticed" cached "$act_dirtykey"
fi

echo "red control: cross-checkout rule disabled"
# shellcheck disable=SC2016
if mutate 's/^if \[\[ "\$ROOT_REAL" != "\$CANON_ROOT" \]\]; then$/if false; then/' "$mutant"; then
	scenarios "$mutant"
	expect "red control: worktree shares the canonical path" "$bin" "$act_wt"
fi

printf '%d passed, %d failed\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]
