# 08 SDK and orchestration

Primary sources: OAI-CX-080, OAI-CX-081, OAI-CX-082, OAI-API-083.

## Verified facts

- The Codex SDK supports programmatic control for coding-focused Codex threads.
- Codex App Server is the interface used for rich clients and exposes conversation, approval, and streamed-event behavior.
- Codex CLI can run as an MCP server and expose tools to start and continue Codex conversations.
- OpenAI's API Agents SDK is a separate orchestration surface from the Codex SDK.

## Inferences for this harness

- Use Codex SDK when the unit of work is a Codex coding thread.
- Use Codex-as-MCP when Codex is one specialist inside a larger OpenAI Agents SDK workflow.
- Treat app-server streamed events as a client integration contract, not as a substitute for CLI transcript parsing, unless the workflow explicitly targets app-server.

## Volatile items

- SDK API surface and event schemas.
- App-server protocol shape.
- MCP tool names and session-persistence behavior.
- Agents SDK handoff and tool-integration guidance.

## Migration checks

- Name the orchestration layer before adding tests or templates.
- Do not conflate Codex SDK, app-server, Codex MCP server, and OpenAI Agents SDK.
- Revalidate examples when model defaults or auth paths change.
