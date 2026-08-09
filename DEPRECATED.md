# Codexkit Deprecation Notice

Standalone `codexkit` is deprecated and frozen as a public compatibility
source.

Active private development has moved to
`/home/hg/hairglasses-studio/codex-harness-staging/codexkit`. This public
repository remains available for historical reference, compatibility checks,
and portfolio review, but it is no longer the primary development lane.

## Support Policy

- Keep the module and CLI functional.
- Keep public baseline, build, vet, and test checks passing.
- Accept compatibility, security, and documentation fixes when they preserve
  the current public surface.
- Do not remove code, archive the repository, or publish private harness
  internals here.
- Do not add new feature development to this standalone repository.

Use [PUBLIC_BOUNDARY.md](PUBLIC_BOUNDARY.md) before changing examples,
generated artifacts, or docs that might imply private workspace state.
