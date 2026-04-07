# codexkit — Agent Instructions

> Canonical instructions: AGENTS.md

Codex fleet management toolkit — config generation, baseline validation, skill surface sync, and MCP profile management for hairglasses-studio repos.

## Build & Test

- `go build ./...` — build all binaries
- `go test -race ./...` — run tests
- `go vet ./...` — lint

## Architecture

- Go 1.26.1, Cobra CLI
- Core packages: `baselineguard`, `skillsync`, `mcpsync`
- CLI: `cmd/codexkit/main.go`
- MCP server: `cmd/codexkit-mcp/main.go` (future)

## Packages

| Package | Purpose |
|---------|---------|
| `baselineguard` | Validate Codex repo baseline (canonical patterns, profiles, skills, agents) |
| `skillsync` | Sync `.agents/skills/` → `.claude/skills/` + `plugins/` mirrors |
| `mcpsync` | Sync `.mcp.json` → `.codex/config.toml` MCP server blocks |

## Key Conventions

- All validation functions return structured results, not just pass/fail
- File operations are non-destructive by default (dry-run support)
- Fleet operations iterate repos from a configurable scan path
