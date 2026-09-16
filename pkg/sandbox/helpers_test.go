package sandbox

import (
	"os/exec"
	"testing"
)

// requireBwrap skips the current test if the bubblewrap (bwrap) executable
// is not installed in PATH or unavailable on the host platform.
func requireBwrap(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not found: capability required")
	}
	return path
}
