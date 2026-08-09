# Deprecation notice

This standalone `codexkit` repository is now a public compatibility snapshot.

Active private development has moved into the centralized Codex harness, where
the module is nested under `codexkit/`. The public module path remains
`github.com/hairglasses-studio/codexkit` during the compatibility window so
existing installs, imports, examples, and public documentation can keep working.

What stays here:

- Source needed to build the public compatibility CLI and MCP server.
- Public examples, docs, tests, and boundary checks.
- Compatibility tags and changelog entries.

What does not move here:

- Private harness implementation details.
- Private workspace inventory, operator state, account data, credentials,
  runtime logs, or generated provider overlays.
- New Codex-harness-specific agents, hooks, policies, or research surfaces.

Do not archive this repository until downstream consumers no longer require the
standalone checkout path and the compatibility window has been explicitly
closed.
