# Public Boundary

`codexkit` is published as a reusable toolkit for agent-repo hygiene and
provider configuration management. It intentionally demonstrates the generic
mechanics while excluding private workspace state.

## Included

- Repo baseline validation for agent instruction files, Codex config, skills,
  MCP config, A2A discovery, and generated mirrors.
- Skill surface sync from `.agents/skills/` into compatible provider surfaces.
- MCP config sync and provider overlay projection for Codex, Claude, and
  Gemini-compatible clients.
- Workspace source-contract, surface-index, primitive-index, and fleet-audit
  commands that operate on caller-provided paths.
- Public examples that use generic paths such as `/path/to/workspace`.

## Excluded

- Private repo manifests, local workstation inventory, account identifiers,
  tenant data, credentials, OAuth tokens, API keys, browser state, and runtime
  logs.
- Host-specific generated provider overlays, cache directories, and temporary
  agent worktrees.
- Private policy details that only make sense inside a single operator's
  workstation or organization.

## Publication Checks

Run these before increasing visibility or cutting a public release:

```bash
go test -race ./...
go vet ./...
go build ./...
scripts/check-public-boundary.sh
gitleaks detect --source . --no-git --redact
```

The CLI examples should stay generic. If a new example needs real-looking data,
use synthetic names and paths instead of local workspace details. Keep tracked
provider config portable; do not commit absolute workstation paths or symlinks
that point outside the repository.
