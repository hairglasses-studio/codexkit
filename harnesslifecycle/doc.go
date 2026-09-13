// Package harnesslifecycle carries the durable state a privileged Codex
// harness apply must write so that an interrupted run is detectable and an
// unowned file can never be deleted.
//
// Three contracts live here.
//
// A Journal is written before any mutation. It records every intended
// transition, where it lands, and the no-follow prior state actually observed
// at journal time — not the state the reviewed plan merely claimed. Each
// committed transition is recorded by its own atomically created Step
// document, so a crash leaves a journal whose step set is shorter than its
// intent set and whose Receipt is absent. That asymmetry is the recovery
// signal; this package detects it and never repairs it. Replay, rollback, and
// uninstall belong to the next slice.
//
// A Receipt is the sanitized post-apply record. Every field is a portable
// relative path, a content digest, a timestamp, or a value from a closed
// enumeration. Free-form text is not representable, so a receipt cannot carry
// payload bytes, secret values, or host-absolute paths by construction rather
// than by a scrubbing pass.
//
// A Ledger records what the harness owns. Ownership is the sole authority for
// replacement and removal: a destination absent from the ledger, or already
// tombstoned in it, cannot be replaced or removed. Deletion is tombstone-only.
// Rows are never dropped, removal appends a Tombstone naming the retained
// quarantine object, and the underlying substrate never unlinks anything.
//
// The contract half of this package (types, canonical JSON, digests, ledger
// semantics, journal and receipt construction) is pure: no filesystem, no
// network, no ambient clock. The apply engine and its state store are Linux
// only, for the same descriptor-relative containment reasons as
// internal/harnessfs, and fail closed everywhere else.
package harnesslifecycle
