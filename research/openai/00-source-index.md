# 00 Source index and taxonomy

Snapshot date: 2026-08-09.

This lane is a durable source register for Codex harness decisions. It records official OpenAI and ChatGPT documentation URLs, short verified facts, volatility, and migration impact. It is intentionally not a mirror of the documentation.

Primary machine-readable register: `sources.json`.

## Taxonomy

| Lane | Purpose | Primary source IDs |
| --- | --- | --- |
| 01 entry/prompting | Entry surfaces, CLI orientation, prompting guidance | OAI-CX-010, OAI-CX-011, OAI-CX-012 |
| 02 config/policy | Config precedence, trust, profiles, managed requirements | OAI-CX-020..OAI-CX-024 |
| 03 instructions/skills | AGENTS hierarchy, subagents, skills, rules | OAI-CX-030..OAI-CX-033 |
| 04 safety/auth | Authentication, sandboxing, approvals, auto-review, permissions | OAI-CX-040..OAI-CX-044 |
| 05 extensions/integrations | Hooks, MCP, plugins | OAI-CX-050..OAI-CX-053 |
| 06 app/IDE/cloud/worktree | Local, worktree, cloud, IDE, environment setup | OAI-CX-060..OAI-CX-064 |
| 07 automation/CI | `codex exec`, GitHub Action, security CI | OAI-CX-070..OAI-CX-072 |
| 08 SDK/orchestration | Codex SDK, app server, Codex-as-MCP, Agents SDK | OAI-CX-080..OAI-API-083 |
| 09 models/reasoning | Model catalog, reasoning controls, pricing/plan implications | OAI-API-090..OAI-CX-092 |
| 10 observability/evals | Evals and observability-adjacent surfaces | OAI-API-100..OAI-CX-102 |
| 11 reference/support | Glossary, troubleshooting, security plugin changelog | OAI-CX-110..OAI-CX-112 |

## Release sources

- OAI-CX-000 `https://learn.chatgpt.com/docs/changelog` is the first stop for dated behavior changes.
- OAI-CX-001 `https://learn.chatgpt.com/docs/whats-new` is the first stop for newly announced features and availability caveats.
- OAI-CX-002 `https://learn.chatgpt.com/docs/feature-maturity` explains maturity labels; use it before treating a feature as stable.
- OAI-CX-003 `https://learn.chatgpt.com/docs/open-source` records official open-source component status without relying on repository folklore.

## Migration-impact summary

- High-risk migration surface: config precedence, project trust, sandbox/approval controls, hooks, MCP tool policy, non-interactive mode, SDK/app-server events, model defaults, and security-plugin version drift.
- Medium-risk migration surface: IDE/cloud/worktree UX, local environment setup, feature maturity, glossary terminology, and troubleshooting commands.
- Low-risk surface: static conceptual orientation pages, unless the changelog or What's New page marks a behavior change.

## Fact labels

- Verified fact: directly supported by one or more source records in `sources.json`.
- Inference: local harness conclusion derived from verified facts; do not treat as OpenAI policy.
- Volatile item: a fact that should be rechecked before changing defaults or publishing guidance.
- Provider-surface availability: whether a behavior is documented for desktop app, CLI, IDE, cloud, SDK, or another surface.

## Refresh cadence

- Critical: weekly, and before any harness default changes.
- High: every two weeks, and before adopting a new app/CLI/SDK release.
- Medium: monthly.
- Low: quarterly.

## Snapshot procedure

1. Refresh OAI-CX-000 and OAI-CX-001 first.
2. Refresh the lane-specific source IDs affected by the release note.
3. Update `sources.json` verified facts, volatility, and watch points.
4. Update only the lane pages affected by the source delta.
5. Run JSON validation and URL syntax validation before staging.

## Current refresh note

The 2026-08-09 refresh rechecked the current official changelog, AGENTS.md, config reference, build-skills, build-plugins, plugins, MCP, and permissions pages. It captured the 2026-08-07 Codex CLI 0.147.0 release deltas in OAI-CX-000 and updated the affected source records without copying the upstream manuals.
