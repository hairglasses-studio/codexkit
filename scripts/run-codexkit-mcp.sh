#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

export GOWORK="${GOWORK:-off}"
cd "$ROOT"
exec go run ./cmd/codexkit-mcp "$@"
