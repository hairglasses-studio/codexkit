// Package harnessmanifest defines the versioned, per-repository declaration
// consumed by Codex harness projection and lifecycle tooling.
//
// The package is deliberately pure: it parses bytes, validates values, and
// produces deterministic bytes. It does not inspect repositories, follow
// symlinks, read verifier registries, or mutate consumer worktrees.
package harnessmanifest
