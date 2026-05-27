# codexkit

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![MCP](https://img.shields.io/badge/MCP-2025--11--25-blue)](https://modelcontextprotocol.io/specification/2025-11-25)

Fleet management toolkit for AI agent repos — baseline validation, skill surface sync, and MCP profile management.

> **Engineering context:** codexkit automates the operational overhead of maintaining consistent agent configurations across a fleet of repositories. The same patterns that production platforms need for fleet-wide config validation, drift detection, and automated remediation.

## What It Does

- **Baseline validation** — checks ensuring every repo has canonical agent configs, valid project-local Codex config, valid skill surfaces, and protocol compliance
- **Skill sync** — mirrors `.agents/skills/` to `.claude/skills/` and `plugins/` directories with drift detection
- **MCP sync** — translates `.mcp.json` server entries to `.codex/config.toml` blocks
- **Source contract check** — validates workspace manifest, skill mirrors, MCP generated blocks, runtime inventory artifacts, and global MCP projections behind one command
- **Surface index** — maps baseline repos to canonical skills, MCP sources, generated mirrors, runtime projection, and consolidation decisions
- **Global MCP projection** — generates Codex, Claude, and Gemini workspace-global MCP provider overlays from repo-local policies
- **Fleet audit** — runs baseline + skill + MCP checks across all repos in a workspace
- **Performance audit** — scans for Codex performance bottlenecks and regression budgets
- **19 MCP tools** — all operations available via MCP server for AI agent consumption

## Install

```bash
go install github.com/hairglasses-studio/codexkit/cmd/codexkit@latest
go install github.com/hairglasses-studio/codexkit/cmd/codexkit-mcp@latest
```

Repo-local wrapper scripts (`scripts/run-codexkit-mcp.sh`, `scripts/skill-surface-sync.sh`, `scripts/codex-mcp-sync.sh`) now source `~/hairglasses-studio/scripts/go-workspace-env.sh` when it is available. By default they reuse the shared `~/.cache/go-build` and stage temp work under the short repo roots at `~/.gt/<repo-id>` instead of `/tmp`.

## Usage

### CLI

```bash
# Validate a single repo
codexkit baseline check ~/hairglasses-studio/mcpkit

# Audit the entire fleet
codexkit fleet audit ~/hairglasses-studio

# Show skill drift without applying
codexkit skills diff ~/hairglasses-studio/ralphglasses

# Sync MCP configs
codexkit mcp sync ~/hairglasses-studio/dotfiles

# Validate all repo-controlled workspace source contracts
codexkit workspace source-contract-check ~/hairglasses-studio

# Write and later verify a diffable source-contract artifact
codexkit workspace source-contract-check ~/hairglasses-studio \
  --skill-validator=off \
  --json-out ~/hairglasses-studio/docs/inventory/workspace-source-contract-2026-05-10.json
codexkit workspace source-contract-check ~/hairglasses-studio \
  --skill-validator=off \
  --json-path ~/hairglasses-studio/docs/inventory/workspace-source-contract-2026-05-10.json

# Generate and verify the baseline repo surface index
codexkit workspace surface-index ~/hairglasses-studio \
  --skill-validator=off \
  --json-out ~/hairglasses-studio/docs/inventory/repo-surface-index-2026-05-10.json \
  --markdown-out ~/hairglasses-studio/docs/inventory/repo-surface-index-2026-05-10.md
codexkit workspace surface-index-check ~/hairglasses-studio --skill-validator=off

# Generate and verify the workspace-global MCP provider projection
codexkit workspace global-mcp-projection ~/hairglasses-studio \
  --json-out ~/hairglasses-studio/docs/inventory/workspace-global-mcp-projection-2026-05-10.json \
  --markdown-out ~/hairglasses-studio/docs/inventory/workspace-global-mcp-projection-2026-05-10.md
codexkit workspace global-mcp-projection-check ~/hairglasses-studio

# Generate and verify the wider agent primitive index
codexkit workspace primitive-index ~/hairglasses-studio \
  --json-out ~/hairglasses-studio/docs/inventory/workspace-agent-primitives-2026-05-10.json \
  --markdown-out ~/hairglasses-studio/docs/inventory/workspace-agent-primitives-2026-05-10.md
codexkit workspace primitive-index-check ~/hairglasses-studio
```

### Workspace Source Contract

`codexkit workspace source-contract-check` validates the studio-wide repo-controlled source contract from a full workspace checkout, not from this repository alone. On self-hosted CI, set `HG_STUDIO_ROOT` or keep the canonical checkout at `~/hairglasses-studio`; the path must contain `codexkit/`, `workspace/manifest.json`, and `workspace/mcp-global-policy.json`.

For stricter skill checks, install either the npm `skills-ref` CLI or the PyPI `skills-ref` package, which provides the `agentskills` CLI. `codexkit skills check` and `codexkit workspace source-contract-check` detect both validator entrypoints. Use `--skill-validator=host` to require an installed validator, `--skill-validator=pinned` to use pinned `uvx`/`npx` fallback commands, or `--skill-validator=off` to suppress external validation.

Use `--json-out <path>` to write the stable JSON source-contract artifact, and `--json-path <path>` to fail when a saved artifact no longer matches the live workspace report. The self-hosted baseline workflow uses `--skill-validator=off` for artifact comparison so optional host-installed validators do not change the saved report shape.

`codexkit workspace surface-index` generates `repo-surface-index-*.json` and `.md`, one row per baseline repo. It records canonical skill files, generated skill mirrors, MCP source/config/profile files, runtime projection aliases, source-contract status, and consolidation matrix state. The companion `surface-index-check` command fails when those generated artifacts drift.

`codexkit workspace global-mcp-projection` generates `workspace-global-mcp-projection-*.json` and `.md`. It records the runtime MCP source set plus Codex, Claude, and Gemini provider overlay entries derived from `.mcp.json` and `.codex/mcp-profile-policy.json`. The companion `global-mcp-projection-check` command fails when those generated artifacts drift.

Codex workspace-global overlays are intentionally opt-in: repo profiles must set `global_codex: true` or come from workspace-root foundational servers before they are written to user-scope Codex config. Claude and Gemini continue to project review/research profiles by default, so a zero Codex entry count can be a valid policy state when repo-local `.codex/config.toml` files are the Codex source of truth.

`codexkit workspace primitive-index` generates `workspace-agent-primitives-*.json` and `.md`. It records Claude hooks/settings, local overlays, provider agents, output styles, plugin manifests, nested MCP files, and instruction files that are outside the narrower skill/MCP source contract. Canonical and generated hook wiring failures fail `primitive-index-check`; local overlays remain audit-visible warnings.

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
| `mcp_runtime_inventory` | Build a manifest-backed MCP runtime projection inventory |
| `workspace_global_mcp_projection` | Build the Codex, Claude, and Gemini workspace-global MCP provider projection |
| `fleet_audit` | Full audit across all workspace repos |
| `fleet_report` | Summary report of fleet health |
| `perf_audit` | Scan for performance bottlenecks |
| `perf_report` | Render performance audit as Markdown |
| `workspace_manifest_generate` | Generate `workspace/manifest.json` from live repos and docs metadata |
| `workspace_check` | Validate workspace manifest, repo directories, go.work, and consolidation matrix |
| `workspace_source_contract_check` | Validate workspace source contracts behind one read-only report |
| `workspace_surface_index` | Build a baseline repo index of agent surfaces and runtime projections |
| `workspace_primitive_index` | Build a workspace index of hooks, provider agents, plugin manifests, nested MCP files, and related agent primitives |

## Architecture

```
codexkit/
├── baselineguard/   # 14-check repo validation engine
├── skillsync/       # Skill surface mirror management
├── mcpsync/         # MCP-to-Codex config translation
├── sourcecontract/  # Workspace-wide source-contract orchestration
├── surfaceindex/    # Baseline repo surface index artifacts
├── primitiveindex/  # Workspace-wide agent primitive inventory artifacts
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
