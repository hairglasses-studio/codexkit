# MCP checkout portability

Global MCP projections relocate a missing absolute checkout path when it has
the same workspace and repository names beneath another user's `/home` or
`/Users` directory. The selected local repository is the destination owner.
The local counterpart must already exist and resolve inside that repository.

This applies to whole command, argument, environment-value and working-directory
paths. Existing paths, unrelated repositories, non-normalized paths, shell
fragments, URLs, missing counterparts and escaping symlinks are preserved.
Relocation does not execute a server, copy credentials, or establish health.

Use `workspace global-mcp-sync <workspace> --json --dry-run` before applying.
Review provider-specific opt-ins and destination scopes. Repo-local Codex MCP
configuration remains authoritative unless a profile explicitly opts into its
global projection. A saved configuration is separate from native discovery,
connection health and invocation evidence.

Claude's native JSON importer discards a `cwd` field. On macOS and Linux,
Claude projections preserve stdio working directories with `/usr/bin/env -C`
and a literal argument vector, without shell interpolation. The unsupported
host case refuses projection instead of silently changing the working directory.
Codex and Gemini retain their native `cwd` configuration. Native import readback
and a real directory-change canary verify these separately from path presence.
