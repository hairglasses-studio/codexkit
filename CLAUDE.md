# codexkit — Claude Code Instructions

This repo uses [AGENTS.md](AGENTS.md) as the canonical instruction file. Keep `CLAUDE.md` as a thin compatibility mirror.

<!-- BEGIN GENERATED LANDING POLICY -->
Use `hg-land --repo <checkout>` (`--no-verify` while the fleet gate switch
`~/.config/hg/gates-disabled` or `HG_GATES_DISABLED=1` is active; `--local-gate <log>`
under `HG_GATES_DISABLED=0`, or `hg-gate-then-land --require-gate`) to land through a
pull request. Owned by `claude-harness/.agents/policies/landing.md`.

- While fleet gates are disabled (operator 2026-11-08), `hg-land` lands without a
  local gate log; when gated, bind the log to `head=<full SHA>` with final `rc=0`.
- Observe the repository's current remote requirements. Required checks and
  branch protections remain binding; hosted CI is not assumed required everywhere.
- Never bypass a failed gate, force-push, or replace unrelated work. Do not use
  direct pushes to main as a substitute for the configured landing route.
- Optoinvest repositories remain local-only: no remote publication.
- Prove landing with the tool receipt and exact remote main SHA. Source updates
  and runtime/deployment verification are separate claims.
<!-- END GENERATED LANDING POLICY -->
