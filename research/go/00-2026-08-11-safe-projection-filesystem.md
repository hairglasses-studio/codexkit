# Safe projection filesystem decision, 2026-08-11

## Decision

The first write-capable harness installer will require Go 1.26.5 or newer and
will support writes on Linux only. Other platforms may parse manifests and
plans, but must return a typed unsupported-capability error for mutation until
they have an equivalent containment and conditional-rename implementation.

The engine will not authorize a write by resolving a path and then opening it.
Every parent is opened relative to an anchored root descriptor without
following symlinks; leaves are inspected without following them; staged files
are created exclusively in the destination directory; and create, replace,
and detach transitions use conditional or exchange renames. Schema permission
never substitutes for a runtime ownership marker and an exact reviewed
precondition.

## Current official evidence

- The Go vulnerability database says `os.Root` before Go 1.26.5 on the 1.26
  line can follow an outside-root final symlink when the requested path ends in
  `/`. The report names the affected `os.Root` APIs and the fixed version:
  [GO-2026-4970](https://pkg.go.dev/vuln/GO-2026-4970).
- The Go release history records Go 1.26.5 as the July 7, 2026 minor release
  containing `os` security fixes:
  [Go release history](https://go.dev/doc/devel/release).
- The Go traversal-resistant-filesystem guidance explains why
  `EvalSymlinks` followed by a later open is subject to time-of-check/time-of-use
  races, and documents both the protection and platform limitations of
  `os.Root`:
  [Traversal-resistant file APIs](https://go.dev/blog/osroot).

The development host reports `go1.26.5-X:nodwarf5 linux/amd64`. The module
floor is therefore raised to `go 1.26.5` before any write-capable package is
integrated.

## Required behavior before activation

1. Strict, bounded, duplicate-key-rejecting manifest, source-evidence, and
   plan documents with canonical SHA-256 identities.
2. Source authority limited to exact remote-tree evidence or a clean fresh
   worktree whose requested and tested SHAs agree.
3. Descriptor-relative Linux operations that reject absolute paths, parent
   traversal, symlink parents, unexpected leaf types, and no-replace races.
4. Durable staged content, journals, receipts, and ownership ledgers with
   file and parent-directory synchronization.
5. Backup-first replace and tombstone-only removal; no recursive deletion and
   no mutation of an unmanaged destination even when its bytes happen to
   match.
6. Hermetic regressions for the trailing-slash advisory, symlink escapes,
   special files, create/replace/remove races, crash recovery, rollback drift,
   and unrelated-file preservation.

Until the complete lifecycle and recovery suite passes, repository, user, and
fleet projection commands remain dry-run/read-only. This decision does not
authorize changes to launchers, global Codex configuration, consumer
repositories, or runtime/auth state.
