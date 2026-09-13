// Package harnessfs provides the containment primitives used by the harness
// installer. It is intentionally smaller than a general-purpose filesystem
// abstraction: callers can inspect and read regular files, stage a regular
// file or contained relative symlink beside a target, and perform a small set
// of recoverable rename operations. Cleanup is non-destructive: objects leave
// public names only by transfer into descriptor-pinned, journalable quarantine
// records.
//
// Write operations are supported on Linux only. Linux paths are resolved from
// an already-open directory descriptor with openat2(2); symlinks are forbidden
// in every parent component and regular-file leaf operations use O_NOFOLLOW.
package harnessfs
