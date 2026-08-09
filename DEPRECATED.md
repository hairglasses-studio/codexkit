# Codexkit public snapshot status

This repository is a frozen public compatibility snapshot. On 2026-08-09 its
full history was imported under `codexkit/` in the private
`hairglasses-studio/codex-harness-staging` repository. The imported subtree
preserves the public module path:

```text
github.com/hairglasses-studio/codexkit
```

That compatibility keeps existing Go imports and the `v0.1.0` release usable.
The private harness is now authoritative for active internal development,
Codex-native policy, fleet projections, and consumer migration work.

## What remains supported here

- The existing public history, license, documentation, and release tag.
- Pinning an existing release or commit for external consumers.
- Deliberate publication of a sanitized security or generic compatibility fix,
  when maintainers decide it belongs in the public snapshot.

## What no longer belongs here

- New private fleet inventory or workstation-specific policy.
- Codex harness agents, hooks, model/sandbox defaults, or private research.
- Generated binaries, runtime auth, transcripts, caches, or private mirrors.
- Independent feature development that would fork behavior from the private
  harness authority.

## Archive gate

The repository remains unarchived during the compatibility window. It may be
archived only after active workspace consumers resolve the imported subtree,
compatibility documentation and frozen tags are remotely proven, and no public
security or transition fix remains pending.
