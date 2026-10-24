#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

echo "==> freeze guard"
# This repository is frozen (DEPRECATED.md). Install rather than merely check:
# git hook wiring is per-checkout local config, so a tracked hook is inert
# until core.hooksPath points at it. Making CI perform the install is what
# keeps "CI is green" from meaning only "the guard exists somewhere".
bash scripts/install-freeze-guard.sh
bash scripts/check-freeze.sh --history
bash scripts/check-freeze-self-test.sh
bash scripts/run-codexkit-mcp-self-test.sh

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

echo "==> gofmt"
# vendor/ is third-party code, formatted by its own upstream gofmt version.
unformatted=$(gofmt -l -- $(git ls-files '*.go' | grep -v '^vendor/'))
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
