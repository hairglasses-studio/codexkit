# codexkit Roadmap

Last updated: 2026-05-27.

## Current State

Codex fleet management toolkit for baseline validation, skill sync, and MCP profile generation.

- Tier: `tier-1`
- Lifecycle: `active`
- Language profile: `Go`
- Visibility / sensitivity: `PUBLIC` / `sanitized`

## Public Portfolio Readiness

- [x] Keep README examples generic so public readers can evaluate the tool without seeing private workspace paths.
- [x] Add repository-local community, security, issue, and pull request templates because the organization `.github` defaults are private.
- [x] Pause automatic GitHub Actions triggers while the public org repo is constrained to the private `Internal-Arch` runner path.
- [ ] Re-enable automatic pull request and push checks after organization Actions billing and runner access are healthy.

## MCP Ecosystem Currency Notes (2026-08-08)

Findings from a best-practices research pass, recorded here rather than acted on directly (cheap/verified items only; the rest needs follow-up work):

- [x] Verified `mcp_spec_target`'s "current"/"fleet_target" values are not hardcoded in `baselineguard` — they are read entirely from the external `workspace/mcp-spec-target.json` registry (deliberately outside any single repo, per `guard.go`'s `mcpSpecTargetRegistry`), and that registry file does not exist yet in this workspace, so the check is currently a fleet-wide no-op. `guard_test.go`'s fixture registry already correctly models `current: 2026-07-28` / `fleet_target: 2025-11-25`, matching the MCP spec's 2026-07-28 final release (stateless protocol, no `initialize` handshake/session id, extensions framework, Roots/Sampling/Logging deprecated) superseding 2025-11-25 — nothing to change in this repo's code or tests for this.
- [ ] Populate `workspace/mcp-spec-target.json` (owned outside this repo, at the workspace root) with the 2026-07-28 current / 2025-11-25 fleet_target values so the `mcp_spec_target` check actually activates fleet-wide instead of silently skipping every repo.
- [ ] Claude Code enforces `MAX_MCP_OUTPUT_TOKENS=25,000` per tool call (warns at 10K). `repo_readiness_score`'s single-line JSON output has been observed near ~58KB, which blows this ceiling on every call. Needs a compact/summary response mode plus pagination, the same pattern already used elsewhere in this repo's MCP tools.
- [ ] Native `ToolSearch`-style retrieval matches on tool NAME + DESCRIPTION only — description quality ("Call this when...") is the practical tuning lever for how discoverable this repo's MCP tools are. Related hazard: dynamic content (counts, timestamps) embedded in a tool description invalidates the whole prompt cache on every call; keep descriptions static and put dynamic state in tool output instead.

## Unification Loop Improvements

- [x] Add repo-scoped remediation hints to `codexkit baseline check` failure output, including exact `baseline check`, `skills sync`, `mcp sync`, and `provider sync` commands where the failing check maps to a generator.
- [x] Move remediation hint metadata into structured baseline findings so CLI, MCP, and unification reports use the same recovery contract.
- [x] Make `codexkit baseline check --json` suppress human text by default so automation receives machine-readable JSON.
- [x] Teach unification reports to display structured baseline remediation commands when the baseline queue is non-empty.
- [x] Extend unification cycle notes to include the first baseline remediation command when the baseline queue is non-empty.
- [x] Move workspace-global Claude/Codex/Gemini MCP overlay rendering into `codexkit workspace global-mcp-sync`, so desktop/workspace automation repos delegate provider overlay sync to one Go-owned contract.
- [x] Align repo-local wrapper scripts with the shared workspace Go env contract, so `run-codexkit-mcp.sh`, `skill-surface-sync.sh`, and `codex-mcp-sync.sh` default to shared `GOCACHE` plus repo-scoped temp roots instead of `/tmp`.

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
- [x] [Prompt packs] Add a small prompt/runbook pack for config generation, baseline validation, and recovery flows.

### Rationale Snapshot
- Tier / lifecycle: `tier-1` / `active`
- Language profile: `Go`
- Visibility / sensitivity: `PUBLIC` / `sanitized`
- Surface baseline: AGENTS=yes, skills=yes, codex=yes, mcp_manifest=configured, ralph=no, roadmap=yes
- Whiteclaw transfers in scope: typed handler core, self-explorer contract, profile telemetry, prompt/runbook pack
- Live repo notes: AGENTS, skills, Codex config, configured .mcp.json, 9 workflow(s)

<!-- whiteclaw-rollout:end -->
---

## Crosspollinate Suggestion: Adopt go-mcp-server pattern

> **Source:** private workspace pattern note, summarized here for the public roadmap.
> **Proposed:** 2026-05-07 (cycle 0, refined cycle 13)
> **How to dismiss:** delete this section. Future crosspollinate cycles will detect the deletion and downgrade the recommendation.
> **Updated 2026-05-08:** internal workspace consolidation reduced the source cluster; only the public, reusable recommendation is retained here.

The crosspollinate loop synthesized a canonical pattern for Go MCP servers across the internal workspace based on context7 docs, MCP SDK behavior, and private control-plane exemplars. The public recommendations below are the reusable subset that fits this repository.

Key recommendations relevant to this repo:

- **Dual-SDK build tags** with separate handler files (`handler_mcpgo.go` vs `handler_officialsdk.go`) — the two SDK signatures differ and cannot share handler bodies.
- **mcp-go error pattern**: validation/business errors → `mcp.NewToolResultError(msg), nil`; system errors → `nil, fmt.Errorf(...)`. Three cases, not one.
- **Deferred-loading tool group registry** instead of eager registration. Keeps cold-start memory bounded.
- **Discovery surfaces are MCP resources**, not tools (`<server>:///catalog/server`).

See the pattern doc for the full `# Adoption checklist` and `# Anti-patterns` sections.
