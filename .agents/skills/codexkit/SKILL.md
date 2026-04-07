---
name: codexkit
description: Codex fleet management — baseline validation, skill sync, MCP sync, and fleet audit.
allowed-tools:
  - codexkit_baseline
  - codexkit_skills
  - codexkit_mcp
  - codexkit_fleet
---

# codexkit Workflows

Codex-native entrypoint for fleet management operations.

## Primary workflows

- Baseline: validate repo Codex readiness (canonical patterns, profiles, agents, skills).
- Skills: sync canonical `.agents/skills/` to `.claude/skills/` and `plugins/` mirrors.
- MCP: sync `.mcp.json` into `.codex/config.toml` server blocks.
- Fleet: audit and sync across all repos in ~/hairglasses-studio.

## Working rules

1. Always run checks in dry-run mode before applying changes.
2. Report structured results (JSON) for programmatic consumption.
3. Never modify files outside the target repo unless explicitly requested.
