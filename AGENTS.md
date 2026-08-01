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
| `baselineguard` | Validate Codex repo baseline (canonical patterns, project config, skills, agents, protocol compliance) |
| `configindex` | Redacted inventory and policy checks for Claude, Codex, AGY, dotfiles, and provider homes |
| `skillsync` | Sync `.agents/skills/` → `.claude/skills/` + `plugins/` mirrors |
| `mcpsync` | Sync `.mcp.json` → `.codex/config.toml` MCP server blocks |
| `mcpserver` | MCP server — aggregates all ToolModules, deferred tool loading |
| `fleetaudit` | Fleet-wide audit combining baseline, skill sync, and MCP sync checks |
| `reporeadiness` | Score repo mutation readiness lanes from manifest, fleet mode, git state, and baseline status |
| `perfaudit` | Fleet-wide static audit for Codex performance bottlenecks and regression budgets |
| `internal/toml` | Minimal TOML writer (zero external dependencies) |

## Baseline Checks

| Check | Description |
|-------|-------------|
| `required_file` | Required files exist (AGENTS.md, CLAUDE.md, Claude settings, Codex config) |
| `canonical_agents` | AGENTS.md has canonical marker |
| `canonical_claude` | CLAUDE.md references AGENTS.md |
| `agy_hooks_json` | Native `.agents/hooks.json` parses when present |
| `agy_agent_layout` | Native AGY agents use `.agents/agents/<name>/agent.md` |
| `project_local_profiles` | Repo-local `.codex/config.toml` does not define unsupported `[profiles.*]` tables |
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
| `repo_readiness_score` | reporeadiness | Score autonomous mutation readiness lanes for workspace repos |
| `perf_audit` | perfaudit | Scan the workspace for Codex performance bottlenecks |
| `perf_report` | perfaudit | Render the Codex performance audit as Markdown |
| `workspace_config_index` | configindex | Build a redacted provider/dotfiles/home configuration catalog |
| `workspace_config_check` | configindex | Enforce strict Claude/Codex/AGY ownership and operator-selected autonomy defaults |

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
