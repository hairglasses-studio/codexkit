# Architecture

`codexkit` packages repeatable agent-repo hygiene into CLI commands and an MCP
stdio server.

## High-Level Flow

```text
repo or workspace path
  -> command-specific loader
  -> validation/sync/index package
  -> structured report or generated artifact
  -> optional MCP tool wrapper
```

## Main Subsystems

| Area | Purpose |
| --- | --- |
| `baselineguard` | Repo-level agent config and hygiene checks. |
| `skillsync` | Skill mirror generation and drift detection. |
| `mcpsync` | MCP config translation for Codex-compatible profiles. |
| `sourcecontract` | Workspace source-contract validation. |
| `surfaceindex` | Baseline repo surface index artifacts. |
| `primitiveindex` | Wider agent primitive inventory artifacts. |
| `fleetaudit` | Fleet-wide aggregation. |
| `mcpserver` | MCP stdio surface for the same operations. |

## Public Boundary

- Examples use generic paths such as `/path/to/workspace`.
- Commands operate on caller-provided paths.
- Generated artifacts should be reviewed before publication.
- The public repo excludes private inventory, credentials, tenant data, browser
  state, and host-specific generated overlays.

## Extension Rules

- Prefer adding shared package behavior before CLI-only glue.
- Keep CLI output and MCP output aligned.
- Keep dry-run or diff commands available for every sync command.
- Update `PUBLIC_BOUNDARY.md` when adding a new generated artifact type.
