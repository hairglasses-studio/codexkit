// Package harnessplan defines the deterministic, filesystem-free contract
// between reviewed Codex harness rendering and a future privileged installer.
//
// The package deliberately performs no filesystem or network I/O. Callers
// supply the canonical manifest digest, source-inventory digest, and expected
// source SHA again at validation time. This makes a sealed plan insufficient
// by itself to authorize an apply against different reviewed inputs.
package harnessplan
