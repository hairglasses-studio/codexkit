# Getting Started

## Requirements

- Go 1.26 or newer
- Optional: `gitleaks` for local secret scanning

## Five-Minute Public Review

Use this path to evaluate the public toolkit without depending on a private
workspace checkout or host-specific generated artifacts.

```bash
git clone https://github.com/hairglasses-studio/codexkit.git
cd codexkit
GOWORK=off go mod download
GOWORK=off go vet ./...
GOWORK=off go test ./...
GOWORK=off go run ./cmd/codexkit baseline check .
```

Expected baseline shape:

```text
.                    PASS (31 checks)
```

## What To Inspect

1. `README.md` for the public feature map.
2. `PUBLIC_BOUNDARY.md` for the include/exclude contract.
3. `docs/ARCHITECTURE.md` for the main command and package boundaries.
4. `cmd/codexkit` for CLI routing.
5. `mcpserver` for the MCP stdio entrypoint.

## Workspace Commands

Workspace commands intentionally require caller-provided paths. For public
review, run them only against sample or local checkouts you control.

```bash
GOWORK=off go run ./cmd/codexkit workspace source-contract-check /path/to/workspace --skill-validator=off
GOWORK=off go run ./cmd/codexkit workspace surface-index /path/to/workspace --skill-validator=off
GOWORK=off go run ./cmd/codexkit workspace primitive-index /path/to/workspace
```
