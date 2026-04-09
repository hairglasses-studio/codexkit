# codexkit Roadmap

Last updated: 2026-04-08.

## Current State

Codex fleet management toolkit for baseline validation, skill sync, and MCP profile generation.

- Tier: `tier-1`
- Lifecycle: `active`
- Language profile: `Go`
- Visibility / sensitivity: `PRIVATE` / `internal`
<!-- whiteclaw-rollout:start -->
## Whiteclaw-Derived Overhaul (2026-04-08)

This tranche applies the highest-value whiteclaw findings that fit this repo's real surface: engineer briefs, bounded skills/runbooks, searchable provenance, scoped MCP packaging, and explicit verification ladders.

### Strategic Focus
- Use whiteclaw to harden this repo's own self-hosted control plane, not to create more handwritten MCP plumbing.
- The biggest value is typed contracts, a real self-explorer surface, and durable telemetry for routing/profile decisions.
- Keep the repo positioned as reusable infrastructure rather than a pile of bespoke JSON-RPC handlers.

### Recommended Work
- [ ] [Typed MCP core] Replace handwritten `map[string]any` tool contracts and manual JSON-RPC plumbing with a shared typed handler/core approach.
- [ ] [Self-hosting] Keep `.mcp.json`, CLI entrypoints, and any server front door aligned so the repo can introspect itself consistently.
- [ ] [Explorer] Add a discovery-first contract front door for commands, profiles, skills, providers, and generated config artifacts.
- [ ] [Telemetry] Record routing, model/profile selection, and verification outcomes in a searchable store instead of transient CLI output only.
- [ ] [Prompt packs] Add a small prompt/runbook pack for config generation, baseline validation, and recovery flows.

### Rationale Snapshot
- Tier / lifecycle: `tier-1` / `active`
- Language profile: `Go`
- Visibility / sensitivity: `PRIVATE` / `internal`
- Surface baseline: AGENTS=yes, skills=yes, codex=yes, mcp_manifest=configured, ralph=no, roadmap=yes
- Whiteclaw transfers in scope: typed handler core, self-explorer contract, profile telemetry, prompt/runbook pack
- Live repo notes: AGENTS, skills, Codex config, configured .mcp.json, 9 workflow(s)

<!-- whiteclaw-rollout:end -->
