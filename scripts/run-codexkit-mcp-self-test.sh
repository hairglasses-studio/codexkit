#!/usr/bin/env bash
# Self-test for the cache rules in run-codexkit-mcp.sh.
#
# Runs the REAL launcher (resolve-only and build-only modes, plus a normal launch) against a
# throwaway git repo with a trivial Go module and the REAL go toolchain, so vcs.revision comes
# from `go build`, not a stub. The only stubs are a `go` shim that hangs mid-build (SIGTERM
# case) and failing sha256 tools (fail-closed case).
#
# Red controls: the launcher is re-run with one rule disabled at a time (revision rule, dirty key,
# cross-checkout path, prune's proof of absence, the compile/environment split). Each mutant must produce the WRONG answer for its case; if it does not,
# the fixture cannot tell the rule from the other triggers and the test fails.
#
# What these fixtures cannot cover: a large module, a read-only cache dir, macOS (no flock, no
# timeout), and the real `error obtaining VCS status` failure.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
launcher="$repo_root/scripts/run-codexkit-mcp.sh"
repo_dir="codexkit"
tmp_root="$(mktemp -d)"
trap 'chmod 755 "$tmp_root/locked" 2>/dev/null || true; rm -rf "$tmp_root"' EXIT

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
home="$tmp_root/ho&me" # an `&` in $HOME: bash >= 5.2 expands it in ${var/pat/$repl}
fx="$home/hairglasses-studio/$repo_dir" # the canonical checkout for HOME=$home
cache="$tmp_root/cache"
bin="$cache/codexkit-mcp/bin/codexkit-mcp"
run_env=(env -u CODEXKIT_WORKSPACE_ROOT -u HAIRGLASSES_STUDIO_ROOT -u GIT_OPTIONAL_LOCKS HOME="$home" XDG_CACHE_HOME="$cache" GOCACHE="$gocache" GOWORK=off GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null)
fxgit() { git -C "$fx" -c user.name=selftest -c user.email=selftest@example.com -c commit.gpgsign=false "$@"; }
# The server prints "ran" so a test can tell which binary a launch executed.
setmain() { printf 'package main\n\nimport "os"\n\nfunc main() { _, _ = os.Stdout.WriteString("ran\\n"); _ = %s }\n' "$1" >"$fx/cmd/codexkit-mcp/main.go"; }

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

rm_dir() { # path: remove a scratch dir; an empty, "." or "/" path (an earlier step produced nothing) is skipped, never fatal
	if [[ -n "${1:-}" && "$1" != . && "$1" != / ]]; then rm -rf "$1"; fi
}

prune_scenario() { # launcher-to-install; sets act_prune_kept (1 when a dir whose root is unreachable survives)
	new_fixture "$1"
	mkdir -p "$(dirname "$bin")/wt-0000lock" "$tmp_root/locked/child"
	printf '%s\n' "$tmp_root/locked/child" >"$(dirname "$bin")/wt-0000lock/root"
	chmod 000 "$tmp_root/locked" # the root exists, but a mode-000 parent hides it: test -e says "absent"
	"${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1 || true
	chmod 755 "$tmp_root/locked"
	act_prune_kept="$(find "$(dirname "$bin")" -maxdepth 1 -name 'wt-0000lock' | wc -l)"
}

wt_launch() { # worktree-dir; sets wt_rc wt_out wt_err (extra env via the caller's prefix assignments)
	wt_rc=0
	wt_out="$("${run_env[@]}" bash "$1/scripts/run-codexkit-mcp.sh" </dev/null 2>"$tmp_root/wt_launch.err")" || wt_rc=$?
	wt_err="$(cat "$tmp_root/wt_launch.err")"
}
new_worktree() { # name: a linked worktree of fx with the launcher copied in
	fxgit worktree add -q "$tmp_root/$1" -b "$1"
	mkdir -p "$tmp_root/$1/scripts"
	cp "$launcher_under_test" "$tmp_root/$1/scripts/run-codexkit-mcp.sh"
}

compile_scenario() { # launcher-to-install; sets act_compile_* (worktree without a binary) and act_own_* (with its own earlier binary)
	launcher_under_test="$1"
	new_fixture "$1"
	setmain 1
	fxgit commit -q -am ran
	"${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1
	new_worktree wtc
	printf 'package main\n\nfunc main() { broken\n' >"$tmp_root/wtc/cmd/codexkit-mcp/main.go"
	wt_launch "$tmp_root/wtc"
	act_compile_rc="$wt_rc" act_compile_out="$wt_out" act_compile_err="$wt_err"
	# The same worktree, now with a good build of its own and then a compile break.
	git -C "$tmp_root/wtc" checkout -q -- cmd
	"${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wtc/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1
	printf 'package main\n\nfunc main() { broken\n' >"$tmp_root/wtc/cmd/codexkit-mcp/main.go"
	wt_launch "$tmp_root/wtc"
	act_own_rc="$wt_rc" act_own_out="$wt_out" act_own_err="$wt_err"
	wt_launch "$tmp_root/wtc" # the failure is now recorded: a repeat launch must not serve the earlier binary either
	act_own_rc2="$wt_rc" act_own_out2="$wt_out"
	fxgit worktree remove --force "$tmp_root/wtc"
}

amp_scenario() { # launcher-to-install; a worktree whose ../dep sibling is rewritten to a path containing "&"
	launcher_under_test="$1"
	new_fixture "$1"
	dep="$home/hairglasses-studio/dep"
	mkdir -p "$dep"
	printf 'module example.com/dep\n\ngo 1.20\n' >"$dep/go.mod"
	printf 'package dep\n' >"$dep/dep.go"
	printf 'module example.com/fx\n\ngo 1.20\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ../dep\n' >"$fx/go.mod"
	printf 'package main\n\nimport _ "example.com/dep"\n\nfunc main() {}\n' >"$fx/cmd/codexkit-mcp/main.go"
	fxgit commit -q -am amp
	new_worktree wta
	act_amp_out="$("${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wta/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 || true)"
	fxgit worktree remove --force "$tmp_root/wta"
}

env_scenario() { # launcher-to-install; env failures of a worktree that has no binary of its own
	launcher_under_test="$1"
	new_fixture "$1"
	setmain 1
	fxgit commit -q -am ran
	"${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$fx/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1
	new_worktree wte
	wte_bindir="$(dirname "$("${run_env[@]}" CODEXKIT_MCP_RESOLVE_ONLY=1 bash "$tmp_root/wte/scripts/run-codexkit-mcp.sh" | sed -n 's/^binary=//p')")"
	# a) unusable $TMPDIR: the build uses a cache-local scratch dir instead
	act_tmp_a="$("${run_env[@]}" TMPDIR=/nonexistent CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wte/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 | tail -n 3 || true)"
	rm_dir "$wte_bindir"
	# b) no scratch dir anywhere ($TMPDIR unusable and the cache-local .tmp cannot be created)
	mkdir -p "$wte_bindir"
	: >"$wte_bindir/.tmp"
	TMPDIR=/nonexistent wt_launch "$tmp_root/wte"
	act_tmp_rc="$wt_rc" act_tmp_out="$wt_out" act_tmp_err="$wt_err"
	act_tmp_marker="$(find "$wte_bindir" -name '*.failed-*' | wc -l)"
	act_tmp_nonexistent=0
	if [[ -e /nonexistent ]]; then act_tmp_nonexistent=1; fi
	act_tmp_buildonly_rc=0
	"${run_env[@]}" TMPDIR=/nonexistent CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wte/scripts/run-codexkit-mcp.sh" </dev/null >/dev/null 2>&1 || act_tmp_buildonly_rc=$?
	rm_dir "$wte_bindir"
	# c) the build is killed by a signal (rc 137, no output) / runs out of memory (rc 1, message)
	SHIM_BUILD_EXIT=137 PATH="$shim_go:$PATH" wt_launch "$tmp_root/wte"
	act_kill_rc="$wt_rc" act_kill_out="$wt_out"
	rm_dir "$wte_bindir"
	SHIM_BUILD_EXIT=1 SHIM_BUILD_MSG="fork/exec /usr/lib/go/pkg/tool/compile: cannot allocate memory" PATH="$shim_go:$PATH" wt_launch "$tmp_root/wte"
	act_oom_rc="$wt_rc" act_oom_out="$wt_out"
	rm_dir "$wte_bindir"
	# d) $TMPDIR is a usable directory but mktemp fails in it (full fs, quota): the cache-local dir is used
	full_tmp="$tmp_root/fulltmp"
	mkdir -p "$full_tmp"
	act_full_rc=0
	act_full_out="$(TMPDIR="$full_tmp" SHIM_MKTEMP_FAIL_DIR="$full_tmp" PATH="$shim_mktemp:$PATH" "${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$tmp_root/wte/scripts/run-codexkit-mcp.sh" </dev/null 2>&1)" || act_full_rc=$?
	rm_dir "$wte_bindir"
	fxgit worktree remove --force "$tmp_root/wte"
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
	if [[ -n "\${SHIM_BUILD_EXIT:-}" ]]; then
		if [[ -n "\${SHIM_BUILD_MSG:-}" ]]; then echo "\$SHIM_BUILD_MSG" >&2; fi
		exit "\$SHIM_BUILD_EXIT"
	fi
	while [[ \$# -gt 0 ]]; do
		if [[ "\$1" == -o ]]; then out="\$2"; fi
		shift
	done
	head -c 100000 /dev/zero >"\$out"
	if [[ "\${SHIM_HANG_EXEC:-}" == 1 ]]; then exec sleep 60; fi
	sleep 60
fi
exec "$real_go" "\$@"
EOF
chmod +x "$shim_go/go"
# A mktemp that fails only inside one directory, as a full filesystem or an exhausted quota would.
shim_mktemp="$tmp_root/shim-mktemp"
mkdir -p "$shim_mktemp"
cat >"$shim_mktemp/mktemp" <<EOF
#!/usr/bin/env bash
if [[ "\${TMPDIR:-}" == "\${SHIM_MKTEMP_FAIL_DIR:-@none@}" ]]; then
	echo "mktemp: failed to create file via template: No space left on device" >&2
	exit 1
fi
exec "$(command -v mktemp)" "\$@"
EOF
chmod +x "$shim_mktemp/mktemp"
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

# A worktree whose build times out (environmental) serves the canonical binary.
fxgit worktree add -q "$tmp_root/wtt" -b wtt
mkdir -p "$tmp_root/wtt/scripts"
cp "$launcher" "$tmp_root/wtt/scripts/run-codexkit-mcp.sh"
wtt_rc=0
wtt_out="$(PATH="$shim_go:$PATH" "${run_env[@]}" SHIM_HANG_EXEC=1 CODEXKIT_MCP_BUILD_TIMEOUT_SEC=1 bash "$tmp_root/wtt/scripts/run-codexkit-mcp.sh" </dev/null 2>"$tmp_root/wtt.err")" || wtt_rc=$?
expect "worktree build timeout -> canonical binary served (rc)" 0 "$wtt_rc"
expect "...and the canonical binary ran" "env:||" "$wtt_out"
case "$(cat "$tmp_root/wtt.err")" in
*"serving the canonical binary"*) ok "...with a note" ;;
*) bad "timeout fallback note: $(cat "$tmp_root/wtt.err")" ;;
esac
fxgit worktree remove --force "$tmp_root/wtt"

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

# A worktree whose own code does not compile is an error, never masked by the canonical binary.
fxgit worktree add -q "$tmp_root/wt2" -b wt2
mkdir -p "$tmp_root/wt2/scripts"
cp "$launcher" "$tmp_root/wt2/scripts/run-codexkit-mcp.sh"
wt2_run() { wt2_rc=0; wt2_out="$("${run_env[@]}" bash "$tmp_root/wt2/scripts/run-codexkit-mcp.sh" </dev/null 2>"$tmp_root/wt2.err")" || wt2_rc=$?; }
wt2_run
expect "worktree syntax error exits 1" 1 "$wt2_rc"
expect "...and does not exec the canonical binary" "" "$wt2_out"
case "$(cat "$tmp_root/wt2.err")" in
*"main.go"*"does not compile"*) ok "...with the build error on stderr" ;;
*) bad "worktree compile error output: $(cat "$tmp_root/wt2.err")" ;;
esac
wt2_dir="$(dirname "$bin")/wt-$(printf '%s' "$(cd "$tmp_root/wt2" && pwd -P)" | sha256sum | cut -c1-8)"
expect "...the worktree dir records its root" "$(cd "$tmp_root/wt2" && pwd -P)" "$(cat "$wt2_dir/root")"
wt2_marker=("$wt2_dir"/codexkit-mcp.failed-*)
expect "...and the failure is recorded as a compile failure" compile "$(cat "${wt2_marker[0]}")"
wt2_run
expect "worktree repeat launch still exits 1" 1 "$wt2_rc"
case "$(cat "$tmp_root/wt2.err")" in
*"already failed (compile)"*) ok "...and skips the known-failed build" ;;
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

# A worktree has no ../dep sibling: the build points the replace at the canonical sibling checkout,
# for a one-line replace and for a `replace ( ... )` block alike.
dep="$home/hairglasses-studio/dep"
mkdir -p "$dep"
printf 'module example.com/dep\n\ngo 1.20\n' >"$dep/go.mod"
printf 'package dep\n' >"$dep/dep.go"
# dep2 exists next to every checkout (canonical and worktree): its replace must stay resolvable
# from the temp modfile even though that file lives elsewhere (mixed rewritten and existing siblings).
for d2 in "$home/hairglasses-studio/dep2" "$tmp_root/dep2"; do
	mkdir -p "$d2"
	printf 'module example.com/dep2\n\ngo 1.20\n' >"$d2/go.mod"
	printf 'package dep2\n' >"$d2/dep2.go"
done
printf 'package main\n\nimport (\n\t"os"\n\n\t_ "example.com/dep"\n\t_ "example.com/dep2"\n)\n\nfunc main() { _, _ = os.Stdout.WriteString("ran\\n") }\n' >"$fx/cmd/codexkit-mcp/main.go"
wt_n=3
wt_replace_case() { # label go.mod-replace-text
	printf 'module example.com/fx\n\ngo 1.20\n\nrequire (\n\texample.com/dep v0.0.0\n\texample.com/dep2 v0.0.0\n)\n\n%s\n' "$2" >"$fx/go.mod"
	fxgit commit -q -am "withdep $1"
	build_only "canonical build with a $1 replace"
	wt_n=$((wt_n + 1))
	wt="$tmp_root/wt$wt_n"
	fxgit worktree add -q "$wt" -b "wt$wt_n"
	mkdir -p "$wt/scripts"
	cp "$launcher" "$wt/scripts/run-codexkit-mcp.sh"
	wt_out="$("${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$wt/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 || true)"
	case "$wt_out" in
	*"binary="*"/wt-"*) ok "worktree builds when ../dep is missing, $1 replace (temp modfile)" ;;
	*) bad "worktree modfile build, $1 replace: $wt_out" ;;
	esac
	wt_bin="$(printf '%s\n' "$wt_out" | sed -n 's/^binary=//p')"
	expect "...and its binary is stamped with the worktree HEAD" "$(git -C "$wt" rev-parse HEAD)" "$(go version -m "$wt_bin" | sed -n 's/^[[:space:]]*build[[:space:]]*vcs\.revision=//p')"
	rm_dir "$(dirname "${wt_bin:-.}")" # wt_bin is empty when the worktree build failed: dirname "" is ".", and rm -rf . aborts the run
}
wt_replace_case "one-line" $'replace example.com/dep => ../dep\nreplace example.com/dep2 => ../dep2'
wt_replace_case "block" $'replace (\n\texample.com/dep => ../dep\n\texample.com/dep2 => ../dep2\n)'
wt_replace_case "block with a commented-out line and trailing comments" $'replace (\n\t// example.com/gone => ../gone\n\texample.com/dep => ../dep // the sibling checkout\n\texample.com/dep2 => ../dep2 // exists in both\n)'

# Environmental failure (the sibling cannot be found anywhere): the canonical binary stands in, but
# an explicit build-only still fails. The last worktree from the block case is reused.
mv "$dep" "$dep.off"
wt_out="$("${run_env[@]}" CODEXKIT_MCP_BUILD_ONLY=1 bash "$wt/scripts/run-codexkit-mcp.sh" </dev/null 2>&1 || true)"
case "$wt_out" in
*"build failed"*) ok "build-only in a worktree with no sibling fails" ;;
*) bad "expected a failed build-only: $wt_out" ;;
esac
if [[ -n "$wt_bin" ]]; then rm -f "$(dirname "$wt_bin")"/*.failed-*; fi
wt_rc=0
wt_out="$("${run_env[@]}" bash "$wt/scripts/run-codexkit-mcp.sh" </dev/null 2>"$tmp_root/wt.err")" || wt_rc=$?
expect "worktree with a missing sibling exits 0" 0 "$wt_rc"
expect "...and the canonical binary ran" ran "$wt_out"
case "$(cat "$tmp_root/wt.err")" in
*"serving the canonical binary"*) ok "...with a note" ;;
*) bad "missing-sibling fallback note: $(cat "$tmp_root/wt.err")" ;;
esac
mv "$dep.off" "$dep"
fxgit worktree remove --force "$wt"

prune_scenario "$launcher"
expect "prune keeps a dir whose root sits behind an unsearchable (mode 000) parent" 1 "$act_prune_kept"
compile_scenario "$launcher"
expect "compile error in a worktree: exit status" 1 "$act_compile_rc"
expect "compile error in a worktree: the canonical binary is not exec'd" "" "$act_compile_out"
case "$act_compile_err" in
*"main.go"*"does not compile"*) ok "compile error in a worktree: build error on stderr" ;;
*) bad "compile error output: $act_compile_err" ;;
esac
expect "compile error in a worktree with its own earlier binary: exit status" 1 "$act_own_rc"
expect "...the earlier binary is not served" "" "$act_own_out"
case "$act_own_err" in
*"does not compile"*) ok "...with the build error on stderr" ;;
*) bad "own-binary compile error output: $act_own_err" ;;
esac
expect "...and a repeat launch (failure recorded) still exits 1" 1 "$act_own_rc2"
expect "...without serving the earlier binary" "" "$act_own_out2"

env_scenario "$launcher"
case "$act_tmp_a" in
*"binary="*"/wt-"*) ok "TMPDIR=/nonexistent: the build falls back to a cache-local scratch dir" ;;
*) bad "TMPDIR=/nonexistent build: $act_tmp_a" ;;
esac
expect "no scratch dir at all: worktree without a binary serves the canonical one (rc)" 0 "$act_tmp_rc"
expect "...and the canonical binary ran" ran "$act_tmp_out"
case "$act_tmp_err" in
*"no usable temp dir"*"serving the canonical binary"*) ok "...with both notes" ;;
*) bad "no-scratch-dir notes: $act_tmp_err" ;;
esac
expect "...and no failure marker is written" 0 "$act_tmp_marker"
expect "...and nothing is created at the unusable TMPDIR" 0 "$act_tmp_nonexistent"
expect "...and build-only exits 1" 1 "$act_tmp_buildonly_rc"
expect "a build killed by a signal (rc 137, silent) is environmental: canonical served" 0 "$act_kill_rc"
expect "...and the canonical binary ran" ran "$act_kill_out"
expect "an out-of-memory build failure is environmental: canonical served" 0 "$act_oom_rc"
expect "...and the canonical binary ran" ran "$act_oom_out"

expect "TMPDIR is a directory but mktemp fails in it: the cache-local dir is tried (exit status)" 0 "$act_full_rc"
case "$act_full_out" in
*"binary="*"/wt-"*) ok "...and the worktree built" ;;
*) bad "mktemp-failure fallback: $act_full_out" ;;
esac
amp_scenario "$launcher"
case "$act_amp_out" in
*"binary="*"/wt-"*) ok "a sibling path containing & is substituted verbatim (HOME has an &)" ;;
*) bad "& in the sibling path: $act_amp_out" ;;
esac

echo "red control: mktemp failure in a usable TMPDIR skips the cache-local dir"
mutant="$tmp_root/mutant.sh"
# shellcheck disable=SC2016
if mutate '/The cache-local dir is tried/,/record_failure=true/ s/^  if \[\[ -z "\$build_err" \]\]; then$/  if [[ ! -d "$tmpd" || ! -w "$tmpd" || ! -x "$tmpd" ]]; then/' "$mutant"; then
	env_scenario "$mutant"
	expect "red control: the build is not attempted (exit status)" 1 "$act_full_rc"
fi

echo "red control: & in the sibling path is expanded by the substitution"
# shellcheck disable=SC2016
if mutate 's|^        line="\$pre\${post%%"\$target"\*}\$abs\${post#\*"\$target"}"$|        line="$pre${post/"$target"/$abs}"|' "$mutant"; then
	amp_scenario "$mutant"
	case "$act_amp_out" in
	*"replacement directory"*"does not exist"*) ok "red control: with the & expanded, the replace points nowhere and the build fails" ;;
	*) bad "red control: the & substitution bug went unnoticed: $act_amp_out" ;;
	esac
fi

echo "red control: no scratch dir is a compile failure"
mutant="$tmp_root/mutant.sh"
# shellcheck disable=SC2016
if mutate 's/^    build_class="env"$/    build_class="compile"/' "$mutant"; then
	env_scenario "$mutant"
	expect "red control: the canonical binary is not served (rc)" 1 "$act_tmp_rc"
fi

echo "red control: signal kills are not environmental"
# shellcheck disable=SC2016
if mutate 's/ || "\$1" -ge 128 \]\]/ ]]/' "$mutant"; then
	env_scenario "$mutant"
	expect "red control: rc 137 counts as a compile failure (rc)" 1 "$act_kill_rc"
fi

echo "red control: a non-canonical compile break is masked by an earlier binary"
# shellcheck disable=SC2016
if mutate 's/"\$build_class" == compile && "\$BIN_PATH" != "\$CANON_BIN"/"$build_class" == never/' "$mutant"; then
	compile_scenario "$mutant"
	expect "red control: the earlier binary is served after a compile break (rc)" 0 "$act_own_rc"
	expect "red control: ...and it ran" ran "$act_own_out"
	expect "red control: ...on a repeat launch too (rc)" 0 "$act_own_rc2"
fi

echo "red control: prune proves absence"
mutant="$tmp_root/mutant.sh"
# shellcheck disable=SC2016
if mutate 's/-d "\$p" && -x "\$p" && //' "$mutant"; then
	prune_scenario "$mutant"
	expect "red control: without the parent check the unreachable root's dir is deleted" 0 "$act_prune_kept"
fi

echo "red control: compile failures are not environmental"
# shellcheck disable=SC2016
if mutate 's/build_class="\$(classify_failure "\$last_rc" "\$build_err")"/build_class=env/' "$mutant"; then
	compile_scenario "$mutant"
	expect "red control: a compile error is masked by the canonical binary (exit status)" 0 "$act_compile_rc"
	expect "red control: ...which ran" ran "$act_compile_out"
fi

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
