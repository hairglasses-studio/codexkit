---
paths:
  - "mcpsync/**"
---

MCP sync engine:
- Maps `.mcp.json` server definitions to `.codex/config.toml` TOML blocks
- Uses `internal/toml` writer — no external TOML libraries
- `global_sync.go` handles cross-repo MCP sync to `~/.codex/config.toml`
- Generated blocks use fence markers: `# BEGIN GENERATED MCP SERVERS` / `# END GENERATED MCP SERVERS`
- Go implementation is append-only (shell version replaces marked region)
