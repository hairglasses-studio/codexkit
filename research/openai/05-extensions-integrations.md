# 05 Extensions and integrations

Primary sources: OAI-CX-050, OAI-CX-051, OAI-CX-052, OAI-CX-053.

## Verified facts

- Hooks let Codex run deterministic scripts at lifecycle events.
- Multiple matching command hooks for the same event are launched concurrently.
- Non-managed command hooks require review and trust before they run.
- Local Codex clients share MCP configuration for the same Codex host.
- Plugin-provided MCP servers are owned through plugin manifests rather than normal user transport commands.
- The official 2026-08-09 snapshot documents plugins as reusable ChatGPT and Codex packages that can add skills, connectors, and MCP tools to supported new chats.
- ChatGPT and Codex share a universal plugin directory; Codex CLI also has a plugin browser for Codex environments.
- The 2026-08-07 Codex CLI 0.147.0 changelog adds opt-in MCP 2026-07-28 support, including paginated discovery, multi-round requests, and non-blocking server startup.

## Inferences for this harness

- Hooks are code execution surfaces. Treat hook installation, trust, and matching behavior as safety-critical.
- MCP config should distinguish user-configured servers from plugin-provided servers because ownership, transport, and policy differ.
- Hook tests should include concurrency assumptions and failure behavior; a single hook should not be relied on to serialize or block another matching hook.
- Plugin tests should distinguish catalog discovery, installation, connector authorization, bundled skill activation, and bundled MCP tool exposure.

## Volatile items

- Hook event names, payload schema, and output behavior.
- Managed hook restrictions.
- MCP supported features, OAuth behavior, and tool approval policy.
- Plugin manifest and submission requirements.
- Universal-directory and local-marketplace behavior.
- MCP protocol-version support.

## Provider-surface availability

| Surface | Notes |
| --- | --- |
| CLI | Uses shared local config for hooks and MCP. |
| IDE | Shares the same Codex host configuration. |
| Desktop app | Shares local config and can also host plugin-provided MCP. |
| Hosted plugin tools | May differ from locally configured MCP servers. |
