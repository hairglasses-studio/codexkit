#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

echo "==> parse tracked JSON and TOML"
python3 - <<'PY'
import json
import pathlib
import subprocess
import tomllib

paths = subprocess.check_output(
    ["git", "ls-files", "*.json", "*.toml"], text=True
).splitlines()
for raw in paths:
    path = pathlib.Path(raw)
    data = path.read_bytes()
    if path.suffix == ".json":
        json.loads(data)
    else:
        tomllib.loads(data.decode())
print(f"parsed {len(paths)} tracked JSON/TOML files")
PY

echo "==> verify deprecated manual-only workflows"
python3 - <<'PY'
import pathlib

paths = sorted(pathlib.Path(".github/workflows").glob("*.y*ml"))
if not paths:
    raise SystemExit("no tracked workflow diagnostics found")
for path in paths:
    lines = path.read_text().splitlines()
    try:
        start = lines.index("on:") + 1
        end = lines.index("permissions:")
    except ValueError as exc:
        raise SystemExit(f"{path}: malformed trigger/permissions block") from exc
    triggers = [line.strip()[:-1] for line in lines[start:end] if line.startswith("  ") and line.strip().endswith(":")]
    if triggers != ["workflow_dispatch"]:
        raise SystemExit(f"{path}: triggers must be workflow_dispatch only, got {triggers}")
    content = "\n".join(lines)
    if "Deprecated:" not in content or "diagnostic:" not in content:
        raise SystemExit(f"{path}: workflow must be clearly deprecated and diagnostic-only")
    if "uses:" in content:
        raise SystemExit(f"{path}: diagnostic workflow must not execute reusable actions")
    run_lines = [line.strip() for line in lines if line.strip().startswith("run:")]
    if not run_lines or any(not line.startswith("run: echo ") for line in run_lines):
        raise SystemExit(f"{path}: diagnostic workflow may only echo local guidance")
print(f"verified {len(paths)} manual-only diagnostic workflows")
PY

echo "==> gofmt"
unformatted=$(gofmt -l -- $(git ls-files '*.go'))
if [[ -n "$unformatted" ]]; then
  printf 'gofmt required:\n%s\n' "$unformatted" >&2
  exit 1
fi

echo "==> module tidy check"
GOWORK=off go mod tidy -diff

echo "==> vet"
GOWORK=off go vet ./...

echo "==> build"
GOWORK=off go build ./...

echo "==> race tests"
GOWORK=off go test -count=1 -race ./...

echo "==> public boundary"
bash scripts/check-public-boundary.sh

echo "==> repo baseline"
bash scripts/local-baseline.sh
