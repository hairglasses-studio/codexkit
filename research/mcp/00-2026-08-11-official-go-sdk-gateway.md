# Official MCP Go SDK research-gateway decision, 2026-08-11

Retrieval and implementation date: 2026-08-11.

This note separates current official MCP facts from harness-owned design
choices. It is a cited decision summary, not a copy of the specification or
SDK documentation.

## Official facts

- `github.com/modelcontextprotocol/go-sdk` is the official Go SDK. Stable
  release `v1.7.0` supports the `2026-07-28` protocol while preserving legacy
  protocol compatibility. The module tag and checksum are pinned in
  `codexkit/go.mod` and `codexkit/go.sum`.
  [SDK releases](https://github.com/modelcontextprotocol/go-sdk/releases)
  [SDK server guide](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/docs/server.md)
- MCP `2026-07-28` introduces the stateless discovery lifecycle. Clients use
  `server/discover` to learn supported versions and capabilities, while
  legacy clients can continue to negotiate through the older initialization
  lifecycle when the SDK and transport support it.
  [Discovery specification](https://modelcontextprotocol.io/specification/2026-07-28/server/discover)
  [SDK protocol guide](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/docs/protocol.md)
- Resources are application-controlled contextual data, prompts are
  user-controlled templates, and tools are model-controlled actions. List
  surfaces are paginated and callers must follow returned cursors rather than
  assume one response contains the complete catalog.
  [Resources](https://modelcontextprotocol.io/specification/2026-07-28/server/resources)
  [Prompts](https://modelcontextprotocol.io/specification/2026-07-28/server/prompts)
  [Tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools)
  [Pagination](https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/pagination)
- Authorization guidance applies to network transports and requires the MCP
  server to behave as its own protected resource. Tokens received by an MCP
  server must not be passed through to upstream services.
  [Authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)
  [Security considerations](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations)

## Harness decisions

The following are local architecture choices, not MCP requirements:

- Managed-harness `codexkit-mcp` stdio serving uses the official Go SDK. The
  prior in-repository JSON-RPC server remains the standalone default during the
  compatibility runway so legacy framing clients and public installs stay
  usable; `CODEXKIT_MCP_RUNTIME=sdk` is the explicit standalone opt-in.
- The research gateway is offline-first. It indexes explicitly registered,
  harness-contained local summaries plus body-free upstream receipts. MCP
  callers receive stable IDs and cursors, never arbitrary filesystem paths.
- Discovery, catalog, search, detail, and bounded reads are read-only and
  closed-world. MCP exposes no research networking or receipt mutation. The
  separately named, exact-allowlist refresh is an explicit operator CLI
  command; it is not part of the production MCP module set.
- Upstream manuals remain external. The durable repository stores citations,
  verified facts, hashes, freshness metadata, and local decisions rather than
  copied provider documentation.
- Stdio requires no bearer-token layer because it is a local child process.
  Any future Streamable HTTP deployment must add protected-resource metadata,
  audience validation, HTTPS, origin protections, and tests before exposure.
- Gateway adapters keep responses bounded and paginated even when the
  underlying SDK can list a larger surface. New research formats plug into a
  typed, offline loader registry instead of adding provider switches to one
  monolithic parser.

## Verification contract

The landing gate is an in-memory official-SDK round trip that negotiates MCP
`2026-07-28`, lists tools, calls a tool with structured content, reads a
resource, and gets a prompt. Research-gateway tests additionally require
deterministic ordering and cursors, strict root containment, symlink escape
rejection, bounded UTF-8 reads, freshness provenance, and no network access.
