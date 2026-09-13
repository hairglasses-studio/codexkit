# Fleet provider repair evidence

Verified 2026-09-06 against current official documentation and the local fleet.

- [Codex skills](https://learn.chatgpt.com/docs/build-skills): repository skills use progressive discovery; initial catalogs are budgeted. Missing descriptions and discovery traversal errors are separate from catalog truncation.
- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference): command approval and MCP/app tool approval use different enums. MCP startup budgets must survive source-to-profile projection.
- [AGY subagents](https://antigravity.google/docs/cli/subagents): both `.agents/agents/<name>.md` and `.agents/agents/<name>/agent.md` are native layouts. Accept either but reject duplicate identities.
- [Claude skills](https://code.claude.com/docs/en/skills): `disallowed-tools` is supported native metadata. `user-invocable: false` hides a skill from user invocation without disabling model invocation.
- [Agent Skills specification](https://agentskills.io/specification): portable metadata permits namespaced string values. The fleet uses `hg.claude.disallowed-tools` containing a JSON string array and `hg.claude.user-invocable` / `hg.claude.disable-model-invocation` containing `true` or `false` strings. Claude and Claude-plugin mirrors decode these into native YAML fields. Unsupported extensions, malformed values, and duplicate native settings fail validation before rendering.

Regression coverage verifies comments and nested metadata, native restrictions, idempotence, both AGY layouts, identity collisions, and MCP timeout inheritance. Existing canonical-source extensions remain compatible; this repair does not reinterpret arbitrary metadata as provider configuration.
