# Codex 0.147.0 compatibility

This repository targets the following explicit current contract:

- `gpt-5.6-sol` is the flagship Codex model; the API alias is `gpt-5.6`.
- `gpt-5.6-terra` is the balanced route for broad scans and research.
- `gpt-5.6-luna` is the efficient route for narrow, low-cost work.
- `apps`, `goals`, `hooks`, `multi_agent`, `plugins`, `skill_search`, and
  `tool_suggest` are stable in Codex 0.147.0.
- Tool Search is available; its old feature toggle is a removed compatibility
  no-op. The client sets `defer_loading` on function namespaces or MCP server
  definitions.
- This server actively negotiates MCP `2025-11-25`. Codex's
  `mcp_2026_07_28` feature remains under development and disabled, so this repo
  does not claim that preview protocol is active.

## Discovery and approvals

`tools/list` returns complete JSON Schema object inputs and supports cursor
pagination. The `tool_catalog` and `tool_search` tools group compact entries by
their registered module namespace; `tool_schema` loads one complete schema.
Discovery filters accept no more than 64 `allowed_tools` entries and are not an
authorization grant.

Generated Codex MCP profiles bound their `enabled_tools` selection to 512 unique
names (above the largest current curated fleet profile) and reject overlap with
`disabled_tools`. MCP annotations and catalog
`approval_hint` values remain advisory. The OpenAI client or operator owns the
actual `require_approval`/approval policy, especially for remote or sensitive
tools.

## Primary references

- [OpenAI latest-model guide](https://developers.openai.com/api/docs/guides/latest-model)
- [OpenAI Tool Search guide](https://developers.openai.com/api/docs/guides/tools-tool-search)
- [OpenAI MCP and connectors guide](https://developers.openai.com/api/docs/guides/tools-connectors-mcp)
- [Codex 0.147.0 release](https://github.com/openai/codex/releases/tag/rust-v0.147.0)
- [Codex 0.147.0 feature registry](https://github.com/openai/codex/blob/rust-v0.147.0/codex-rs/features/src/lib.rs)
- [MCP Go SDK v1.7.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- [MCP Go SDK v1.7.0 server implementation](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/server.go)
