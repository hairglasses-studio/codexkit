package main

import (
	"path/filepath"
	"testing"
)

// macOS aliases /var to /private/var. Canonicalize fixture roots without
// permitting symlinks in the skill projection paths being tested.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
