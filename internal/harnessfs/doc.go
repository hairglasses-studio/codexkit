// Package harnessfs provides the containment primitives used by the harness
// installer. It is intentionally smaller than a general-purpose filesystem
// abstraction: callers can inspect and read regular files, stage a regular
// file or contained relative symlink beside a target, and perform a small set
// of recoverable rename operations. Cleanup is non-destructive: objects leave
// public names only by transfer into descriptor-pinned, journalable quarantine
// records.
//
// Write operations are supported on Linux and Darwin. Linux paths are resolved
// with openat2(2); Darwin paths are walked from an already-open directory
// descriptor with openat(2) and O_NOFOLLOW. Both backends reject symlinks in
// parent components and use same-directory atomic rename operations. Darwin
// currently refuses symlink leaves as unsupported because it does not pin their
// identity through the full transaction contract.
package harnessfs
