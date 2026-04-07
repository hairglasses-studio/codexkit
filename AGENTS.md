# codexkit — Agent Instructions

> Canonical instructions: AGENTS.md

Codex fleet management toolkit — config generation, baseline validation, skill surface sync, and MCP profile management for hairglasses-studio repos.

## Build & Test

- `go build ./...` — build all binaries
- `go test -race ./...` — run tests
- `go vet ./...` — lint

## Architecture

- Go 1.26.1
- All packages implement the `ToolModule` interface (see `module.go`)
- Module registry aggregates tools (`registry.go`)
- CLI: `cmd/codexkit/main.go` — registry-based dispatch
- MCP server: `cmd/codexkit-mcp/main.go` — JSON-RPC over stdio (MCP 2025-11 spec)

## Packages

| Package | Purpose |
|---------|---------|
| `baselineguard` | Validate Codex repo baseline (canonical patterns, profiles, skills, agents, protocol compliance) |
| `skillsync` | Sync `.agents/skills/` → `.claude/skills/` + `plugins/` mirrors |
| `mcpsync` | Sync `.mcp.json` → `.codex/config.toml` MCP server blocks |
| `mcpserver` | MCP server — aggregates all ToolModules, deferred tool loading |
| `fleetaudit` | Fleet-wide audit combining baseline, skill sync, and MCP sync checks |
| `internal/toml` | Minimal TOML writer (zero external dependencies) |

## Baseline Checks

| Check | Description |
|-------|-------------|
| `required_file` | Required files exist (AGENTS.md, CLAUDE.md, GEMINI.md, copilot-instructions.md, config.toml) |
| `canonical_agents` | AGENTS.md has canonical marker |
| `canonical_claude` | CLAUDE.md references AGENTS.md |
| `canonical_gemini` | GEMINI.md references AGENTS.md |
| `canonical_copilot` | copilot-instructions.md mentions AGENTS.md |
| `profile` | Required Codex profiles defined (readonly_quiet, review, workspace_auto, ci_json) |
| `agent_naming` | Agent filenames use underscore_case |
| `skill_surface` | surface.yaml is valid |
| `skill_file` | Each skill has SKILL.md |
| `skill_sync` | .claude/skills/ mirrors are in sync with .agents/skills/ |
| `mcp_sync` | .mcp.json servers have corresponding .codex/config.toml entries |
| `mcp_discovery` | HTTP MCP servers have .well-known/mcp.json |
| `a2a_awareness` | .well-known/agent.json is valid if present |
| `skill_portability` | SKILL.md frontmatter uses only portable keys per Agent Skills standard |

## MCP Tools

The MCP server (`cmd/codexkit-mcp`) exposes these tools:

| Tool | Module | Description |
|------|--------|-------------|
| `baseline_check` | baselineguard | Validate repo baseline |
| `baseline_check_all` | baselineguard | Fleet-wide validation |
| `skill_sync` | skillsync | Sync skills to mirrors |
| `skill_diff` | skillsync | Show skill drift (dry-run) |
| `skill_list` | skillsync | List skills from surface.yaml |
| `mcp_sync` | mcpsync | Sync MCP server config |
| `mcp_diff` | mcpsync | Show MCP config drift (dry-run) |
| `mcp_list` | mcpsync | List MCP servers |
| `fleet_audit` | fleetaudit | Run full audit on all repos |
| `fleet_report` | fleetaudit | Summary report of fleet health |

## Protocol Support

- **MCP 2025-11**: stdio transport, deferred tool loading, server discovery via `.well-known/mcp.json`
- **Agent Skills open standard** (Dec 2025): portable frontmatter validation, hot-reloading (`reload: true`)
- **Agent2Agent (A2A)**: `.well-known/agent.json` validation
- **ToolModule interface**: modeled after claudekit's pattern for module aggregation

## Key Conventions

- All packages implement `ToolModule` with typed handlers
- All validation functions return structured results, not just pass/fail
- File operations are non-destructive by default (dry-run support)
- Fleet operations iterate repos from a configurable scan path
- Zero external dependencies
