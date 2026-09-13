# 12 Codex official refresh, 2026-08-09

Retrieval date: 2026-08-09.

Allowed source domains: `learn.chatgpt.com`, `developers.openai.com`, `platform.openai.com`.

This note records the focused refresh requested for current Codex harness work. It is a source-backed summary, not a mirror of the upstream manuals.

## Sources checked

| Source ID | URL | Refresh result |
| --- | --- | --- |
| OAI-CX-000 | https://learn.chatgpt.com/docs/changelog | Current Aug. 7 Codex CLI 0.147.0 release metadata captured in the register. |
| OAI-CX-022 | https://learn.chatgpt.com/docs/config-file/config-reference | Confirmed project-scoped config limits and that app/MCP tool approval modes are `auto`, `prompt`, `writes`, or `approve`, distinct from top-level command approval policy. |
| OAI-CX-030 | https://learn.chatgpt.com/docs/agent-configuration/agents-md | Confirmed AGENTS hierarchy, override order, fallback filenames, and default project document byte cap. |
| OAI-CX-032 | https://learn.chatgpt.com/docs/build-skills | Confirmed skill progressive-disclosure model, package layout, plugin distribution path, and invocation metadata. |
| OAI-CX-044 | https://learn.chatgpt.com/docs/permissions | Confirmed permission profiles apply to local sandboxed command execution, with connectors, MCP, browser/computer-use, Codex cloud, and approved escalations controlled separately. |
| OAI-CX-051 | https://learn.chatgpt.com/docs/extend/mcp | Confirmed local Codex MCP sharing, supported host features, server instructions, and hosted plugin-tool caveats. |
| OAI-CX-052 | https://learn.chatgpt.com/docs/build-plugins | Confirmed plugin packages can include skills and/or MCP and can be tested through local marketplaces before universal-directory submission. |
| OAI-CX-053 | https://learn.chatgpt.com/docs/plugins | Added as a standalone source for plugin catalog, availability, and installed-plugin behavior. |

## Findings for harness work

- Codex CLI 0.147.0 on 2026-08-07 is the current release anchor for portable Agent Plugins, catalog search across local/personal/workspace/remote sources, `--approve-for-me`, imported Cursor-managed skills, imported Claude/Cursor conversation sync, opt-in MCP 2026-07-28, secret redaction, project trust enforcement, plugin isolation hardening, and the removal of deprecated `codex exec --full-auto`.
- Plugin coverage should be modeled as four distinct surfaces: catalog discovery, installation/package ownership, connector permissions, and bundled skill/MCP exposure.
- `--approve-for-me` belongs in the auto-review/approval policy family. It should not be treated as a sandbox broadening mechanism.
- Imported skills and imported conversations are migration surfaces, not canonical authoring surfaces. Harness checks should detect duplicate imports and preserve the canonical owner when clear.
- MCP 2026-07-28 support changes discovery and turn mechanics enough that MCP smoke tests should include paginated discovery, multi-round requests, and server-startup latency behavior.
- AGENTS.md discovery remains hierarchical and proximity-based, while generated compatibility mirrors should be verified behaviorally instead of by exact text identity.
- Skill metadata can constrain implicit invocation, but explicit skill invocation still works. Harness routing tests should cover both paths.
- `default_tools_approval_mode` and per-tool `approval_mode` for apps and MCP
  servers use the four-value `auto | prompt | writes | approve` enum. Do not
  copy top-level command `approval_policy = "never"` into those fields.

## Freshness metadata

- Snapshot owner: `research/openai/sources.json`
- Snapshot date: 2026-08-09
- Volatile categories touched: critical release notes, config policy, AGENTS discovery, skills, permissions, MCP, plugins.
- Next recommended refresh: before adopting another Codex CLI/app release or changing harness defaults for plugin, approval, MCP, or instruction-loading behavior.
