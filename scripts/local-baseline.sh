#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

report_path=$(mktemp)
error_path=$(mktemp)
trap 'rm -f "$report_path" "$error_path"' EXIT

if GOWORK=off go run ./cmd/codexkit baseline check . --json >"$report_path" 2>"$error_path"; then
	cat "$report_path"
	exit 0
fi

# Managed Codex launchers may overlay approval defaults in the worktree's
# .codex/config.toml. Accept only that exact unstaged overlay; every other
# baseline finding remains fatal.
python3 - "$report_path" <<'PY'
import json
import pathlib
import sys

reports = json.loads(pathlib.Path(sys.argv[1]).read_text())
failures = [
    finding
    for report in reports
    for finding in report["findings"]
    if not finding["passed"]
]
if not failures or any(finding["check"] != "mcp_sync" for finding in failures):
    print(json.dumps(reports, indent=2))
    raise SystemExit("baseline failed outside the launcher-owned MCP projection")
PY

if git diff --quiet -- .codex/config.toml; then
	cat "$report_path"
	cat "$error_path" >&2
	echo "mcp_sync failed without a launcher-owned .codex/config.toml overlay" >&2
	exit 1
fi
if ! git diff --cached --quiet -- .codex/config.toml; then
	cat "$error_path" >&2
	echo ".codex/config.toml must never be staged by the local baseline exception" >&2
	exit 1
fi

python3 - <<'PY'
import subprocess

diff = subprocess.check_output(
    ["git", "diff", "--unified=0", "--", ".codex/config.toml"], text=True
)
changes = [
    line
    for line in diff.splitlines()
    if line.startswith(("+", "-")) and not line.startswith(("+++", "---"))
]
allowed = {
    '-default_tools_approval_mode = "never"',
    '+default_tools_approval_mode = "approve"',
}
if not changes or any(line not in allowed for line in changes):
    raise SystemExit(".codex/config.toml differs beyond the recognized launcher approval overlay")
if changes.count('-default_tools_approval_mode = "never"') != changes.count('+default_tools_approval_mode = "approve"'):
    raise SystemExit("launcher approval overlay must replace each never value with approve")
PY

cat "$report_path"
echo "accepted only the unstaged launcher-owned .codex/config.toml approval overlay"
