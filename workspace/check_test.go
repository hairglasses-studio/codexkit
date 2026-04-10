package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheck_FailsWhenRequiredRepoDirectoryMissing(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{
				Name:         "active-repo",
				Scope:        "active_first_party",
				GoWorkMember: false,
			},
		},
	}

	report := Check(root, manifest)
	if report.Passed {
		t.Fatal("expected workspace check to fail when required repo directory is missing")
	}
	if !hasFinding(report.Findings, "repo_directory", "active-repo", false, "missing directory") {
		t.Fatal("expected missing directory finding for active-repo")
	}
}

func TestCheck_AllowsMissingArchivedCompatibilityClone(t *testing.T) {
	root := t.TempDir()
	docsRoot := filepath.Join(root, "docs-source")
	t.Setenv("DOCS_ROOT", docsRoot)
	if err := os.MkdirAll(filepath.Join(docsRoot, "inventory"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	matrix := ConsolidationMatrix{
		Decisions: []ConsolidationDecision{
			{
				Repo:             "legacy-repo",
				State:            "merged_out_of_active_surface",
				WorkspaceScope:   "compatibility_only",
				ArchiveCandidate: true,
			},
		},
	}
	data, err := json.Marshal(matrix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsRoot, "inventory", "repo-consolidation-matrix.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{
				Name:           "legacy-repo",
				Scope:          "compatibility_only",
				BaselineTarget: false,
				GoWorkMember:   false,
			},
		},
	}

	report := Check(root, manifest)
	if !report.Passed {
		t.Fatalf("expected workspace check to pass, got failed report: %+v", report.Findings)
	}
	if !hasFinding(report.Findings, "repo_directory", "legacy-repo", true, "optional archived compatibility clone absent") {
		t.Fatal("expected optional archived compatibility clone finding for legacy-repo")
	}
}

func hasFinding(findings []Finding, check, repo string, passed bool, message string) bool {
	for _, finding := range findings {
		if finding.Check == check && finding.Repo == repo && finding.Passed == passed && finding.Message == message {
			return true
		}
	}
	return false
}
