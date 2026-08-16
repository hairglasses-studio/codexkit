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

## Enforcement

The freeze is enforced, not merely documented. `scripts/check-freeze.sh` runs
from git's `commit-msg` hook and refuses every commit unless both hold:

1. the commit message carries a `Freeze-Exempt: <reason>` trailer whose reason
   is one of `public-boundary-compat`, `freeze-guard-maintenance`, or
   `deprecation-doc`; and
2. every path the commit changes is in that reason's allowlist.

The trailer must sit in the message's final trailer block (the guard parses it
with `git interpret-trailers --parse`). A blank line between `Freeze-Exempt:`
and any later footer such as `Co-Authored-By:` splits them into two paragraphs
and the guard will not see the exemption — keep all trailers in one block.

Allowlists are allow-only, so an unanticipated path fails closed naming the
path. Private fleet surfaces (`.agents/agents/`, `.agents/profiles/`,
`.agents/rules/`, `.agents/subagents/`, `surface-audit.*`) are refused under
every reason — these are the paths workspace sync tooling sprays into repos,
and they are excluded by `PUBLIC_BOUNDARY.md`.

Install per clone (also done automatically by `make ci`):

```bash
bash scripts/install-freeze-guard.sh
```

The install step is required because git hook wiring lives in per-checkout
local config: a tracked hook file is inert until `core.hooksPath` points at it.
`.githooks/` therefore also delegates `pre-commit`, `pre-push`, and
`post-commit` back to whatever hooks directory the global config names, so
repointing `core.hooksPath` here does not silently disable them.

Note for linked worktrees: `core.hooksPath` is shared across every worktree of
this repository unless `extensions.worktreeConfig` is enabled, so installing
from one worktree also repoints the others. Run `scripts/check-freeze.sh
--verify-install` in a checkout to confirm the guard is live there — it fails
when the configured hooks directory has no executable `commit-msg`.

`scripts/check-freeze.sh --history` runs in CI and catches commits made with
`--no-verify`; it enforces from the commit that introduced the guard onward, so
no baseline sha needs hand-maintaining. `scripts/check-freeze-self-test.sh`
proves both the deny and the allow path with real commits in a throwaway clone,
including a negative control showing the same commit succeeds when the guard is
uninstalled.
