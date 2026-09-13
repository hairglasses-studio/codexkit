// Package harnessrender verifies a reviewed harness plan against its canonical
// manifest, exact source inventory, and caller-supplied material bytes.
// ValidateAuthority performs the complete material-independent preflight;
// Build reuses that preflight before inspecting material values.
//
// The package is deliberately pure. It performs no filesystem or network I/O,
// consults no ambient clock or environment, and accepts no callbacks. Its
// artifacts are values for a separately privileged, journaled installer.
package harnessrender
