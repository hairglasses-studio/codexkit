// Package sourceref defines the independent, fail-closed exact-ref inventory
// used to bind fleet observations to repository identity and Git evidence.
//
// The package is deliberately filesystem- and network-free. Callers collect
// evidence elsewhere, then use Load, Validate, and Marshal with an explicit
// validation clock. This keeps source-ref proof independent of the later
// projection and installation engines.
package sourceref
