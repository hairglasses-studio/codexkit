package sandbox

import (
	"os/exec"
	"testing"
)

// requireBwrap skips the calling test unless bubblewrap can actually be run, and
// returns the path the sandbox itself would use.
//
// Absence of bubblewrap is a missing host capability, not a defect in this
// package. Without this guard 25 tests across six files in this package failed
// on a host where bwrap was unusable, which reads as a broken sandbox rather
// than as an unsupported host and trains readers to ignore a red suite.
//
// Two details make this stricter than a bare exec.LookPath("bwrap"):
//
// It resolves through DefaultConfig so it agrees with the code under test.
// DefaultConfig falls back to /usr/bin/bwrap when the PATH lookup fails, so a
// LookPath-only guard would skip on a host with a sanitized PATH where the
// sandbox would in fact have run.
//
// It then executes --version, because resolving a path proves nothing about
// being able to run it. A present-but-not-executable bwrap is exactly what a
// masked or partially-installed bubblewrap looks like, and New does not catch
// it: New only looks bwrap up when cfg.BwrapPath is empty, which never happens
// for a DefaultConfig thanks to that same fallback, so the failure would
// otherwise surface late as an opaque exec error from deep inside a Run call.
func requireBwrap(t *testing.T) string {
	t.Helper()

	path := DefaultConfig().BwrapPath
	if path == "" {
		t.Skip("bubblewrap not available: not on PATH and no fallback path configured")
	}
	if out, err := exec.Command(path, "--version").CombinedOutput(); err != nil {
		t.Skipf("bubblewrap at %s is not usable (%v): %s", path, err, out)
	}
	return path
}
