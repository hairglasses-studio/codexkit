# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows the tag history in `git tag` rather than a strict
SemVer cadence.

## [Unreleased]

### Deprecated
- The standalone repository is now a public compatibility snapshot. Active
  private development moved into the centralized Codex harness with this module
  nested under `codexkit/`; the module path remains
  `github.com/hairglasses-studio/codexkit` during the compatibility window.

### Added
- Performance audit module (`perfaudit`) with supporting script improvements.
- Projection diff preview helper for global MCP overlay changes.
- Smoke tests for the `fleetaudit` package and unit tests for `llmreduction`.
- Public portfolio surface: `PUBLIC_BOUNDARY.md`, public CI quickstart, public
  review docs, and portfolio proof notes.
- `scripts/check-public-boundary.sh` public-boundary checker, wired into CI.

### Changed
- Removed the tracked built `codexkit` binary from source control; build the CLI
  from `cmd/codexkit` instead.
- Retired hosted Claude/Codex workflows in favor of the fleet-local baseline
  guard and public CI paths.
- Refined unification and performance-reduction heuristics; bounded Gemini
  MCP allowlist generation; used the fast PR lane in CI.
- Centralized `.claude/` and symlinked `copilot-instructions.md`,
  `.editorconfig`, `CLAUDE.md`, and `GEMINI.md` to canonical fleet-docs
  locations (cluster shrinkage / fleet-trim).
- Hardened the public boundary: removed private markers from the public
  slice, harder-scoped Codex skill/MCP surface audits, clarified Codex MCP
  projection policy, filtered non-stdio MCP profiles from generated Codex
  config.
- Reworked CI runner assignment: `ci.yml`, `codex-baseline-guard.yml`,
  `dependabot-auto-merge.yml`, `perf.yml`, and `public-ci.yml` now run on
  GitHub-hosted `ubuntu-latest` runners instead of the private
  `Internal-Arch` self-hosted runner group, which had gone offline and left
  every workflow permanently red. No self-hosted runner is required to build,
  test, lint, or validate this repo.

### Fixed
- Ignored retired Cline hooks and stale audit state in `unificationaudit`.
- Honored repo-local source-contract checks; aligned workspace projection
  checks.
- Suppressed a false-positive `errcheck` lint finding on `os.Remove` calls in
  `mcpsync` (matching `ralphglasses` convention).
- Auto-commit unification loop bug: consolidated `cr8-cli` hooks and fixed a
  hook-classification bug.

## [0.1.0] - 2026-04-16

Initial public release of codexkit — a fleet management toolkit for AI agent
repos.

### Added
- Baseline validation (`baselineguard`) for canonical agent configs, Codex
  config, skill surfaces, and protocol compliance, including support for
  YAML-format `surface.yaml`.
- `ToolModule` architecture, `skillsync`, `mcpsync`, MCP server, fleet audit,
  and protocol-compliance checks.
- Skill sync that generates `.claude/skills/` from canonical
  `.agents/skills/`, including 21 global Codex skills.
- Global Codex MCP sync, MCP ping support, Gemini MCP stdio support, MCP
  connection validation, and global MCP policy controls.
- `workspace check` command enforcing source-contract and consolidation
  matrix drift.
- Surfacekit bridge absorption: parity refresh, bridge checks, and later
  removal of the deprecated surfacekit-bridge compatibility shim once its
  functionality was fully absorbed.
- Ollama rollout fields and archive-check hardening in the parity runtime.
- Provider drift objective overrides and non-portable sync-wrapper flagging.
- MIT `LICENSE` and `.editorconfig`.
- `README.md` and `CONTRIBUTING.md` for the public release.

### Changed
- Aligned `baselineguard` with tri-provider (Codex/Claude/Gemini) parity.
- Enforced MCP launcher portability; made MCP launchers repo-local.
- Organization-wide runner migration and health sync; studio-scale
  optimization sync; published codexkit parity surfaces.
- Improved Codex startup performance; preferred `gpt-5.4-xhigh`; dropped
  Ollama inventory columns from the parity audit; moved the `codexkit-mcp`
  cache to a user-owned `XDG_CACHE_HOME` path.

### Fixed
- JSON-RPC notification handling, marshal errors, quoted YAML server names,
  and hyphenated MCP server names (post-review hardening).
- Relative repo paths in parity wrappers; preserved empty MCP `cwd` when the
  source omitted it.
- Hyphenated skill aliases emitted during sync; aligned skill-sync guidance
  and tests.
- Ignored example-only MCP servers; audited workspace-owned home state.
- Stabilized codexkit self-audit metadata labels; narrowed the codexkit
  template label rewrite; counted only legacy command files.

[Unreleased]: https://github.com/hairglasses-studio/codexkit/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/hairglasses-studio/codexkit/releases/tag/v0.1.0
