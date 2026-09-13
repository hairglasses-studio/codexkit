// Package launchercontract defines the pure discovery and evidence contract
// between the Codex harness and its externally owned launcher.
//
// The package deliberately performs no environment lookup, filesystem access,
// Git invocation, process launch, or network operation. Callers collect those
// observations separately and pass values to Resolve. A successful Resolution
// is therefore an activation authorization over supplied evidence, not an
// ambient discovery mechanism.
package launchercontract
