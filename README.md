# codexkit

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![MCP](https://img.shields.io/badge/MCP-2025--11--25-blue)](https://modelcontextprotocol.io/specification/2025-11-25)

Fleet management toolkit for AI agent repos — baseline validation, skill surface sync, and MCP profile management.

> **Engineering context:** codexkit automates the operational overhead of maintaining consistent agent configurations across a fleet of repositories. The same patterns that production platforms need for fleet-wide config validation, drift detection, and automated remediation.

## What It Does

- **Baseline validation** — 14 checks ensuring every repo has canonical agent configs, required profiles, valid skill surfaces, and protocol compliance
- **Skill sync** — mirrors `.agents/skills/` to `.claude/skills/` and `plugins/` directories with drift detection
- **MCP sync** — translates `.mcp.json` server entries to `.codex/config.toml` blocks
- **Fleet audit** — runs baseline + skill + MCP checks across all repos in a workspace
- **Performance audit** — scans for Codex performance bottlenecks and regression budgets
- **12 MCP tools** — all operations available via MCP server for AI agent consumption

## Install

```bash
go install github.com/hairglasses-studio/codexkit/cmd/codexkit@latest
go install github.com/hairglasses-studio/codexkit/cmd/codexkit-mcp@latest
```

## Usage

### CLI

```bash
# Validate a single repo
codexkit baseline check ~/hairglasses-studio/mcpkit

# Audit the entire fleet
codexkit fleet audit ~/hairglasses-studio

# Show skill drift without applying
codexkit skill diff ~/hairglasses-studio/ralphglasses

# Sync MCP configs
codexkit mcp sync ~/hairglasses-studio/dotfiles
```

### MCP Server

```json
{
  "mcpServers": {
    "codexkit": {
      "command": "codexkit-mcp",
      "args": ["--workspace", "~/hairglasses-studio"]
    }
  }
}
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `baseline_check` | Validate repo baseline against 14 canonical checks |
| `baseline_check_all` | Fleet-wide baseline validation |
| `skill_sync` | Sync skills from `.agents/skills/` to mirrors |
| `skill_diff` | Show skill drift (dry-run) |
| `skill_list` | List skills from `surface.yaml` |
| `mcp_sync` | Sync MCP server config to Codex format |
| `mcp_diff` | Show MCP config drift (dry-run) |
| `mcp_list` | List registered MCP servers |
| `fleet_audit` | Full audit across all workspace repos |
| `fleet_report` | Summary report of fleet health |
| `perf_audit` | Scan for performance bottlenecks |
| `perf_report` | Render performance audit as Markdown |

## Architecture

```
codexkit/
├── baselineguard/   # 14-check repo validation engine
├── skillsync/       # Skill surface mirror management
├── mcpsync/         # MCP-to-Codex config translation
├── fleetaudit/      # Fleet-wide audit orchestration
├── perfaudit/       # Performance bottleneck scanner
├── mcpserver/       # MCP server (JSON-RPC over stdio)
├── internal/toml/   # Zero-dependency TOML writer
├── cmd/codexkit/    # CLI entrypoint
└── cmd/codexkit-mcp/ # MCP server entrypoint
```

All packages implement the `ToolModule` interface for uniform registration and aggregation.

## Build

```bash
go build ./...           # Build all
go vet ./...             # Lint
go test -count=1 -race ./...  # Test
```

## License

MIT
