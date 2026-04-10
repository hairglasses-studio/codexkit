package workspace

import (
	"path/filepath"
	"testing"
)

func TestConsolidationMatrixPathUsesDocsRootOverride(t *testing.T) {
	t.Setenv("DOCS_ROOT", "/tmp/docs-worktree")

	got := ConsolidationMatrixPath("/tmp/studio")
	want := filepath.Join("/tmp/docs-worktree", "inventory", "repo-consolidation-matrix.json")
	if got != want {
		t.Fatalf("ConsolidationMatrixPath() = %q, want %q", got, want)
	}
}
