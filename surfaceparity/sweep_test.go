package surfaceparity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSurfaceParityAuditAndSync(t *testing.T) {
	tempRoot := t.TempDir()

	manifestJSON := `{
		"version": 1,
		"repos": [
			{"name": "repo-a", "category": "hub", "scope": "active_first_party", "language": "Go", "baseline_target": true},
			{"name": "repo-b", "category": "tooling", "scope": "active_operator", "language": "Python", "baseline_target": false}
		]
	}`
	writeTestFile(t, filepath.Join(tempRoot, "workspace", "manifest.json"), manifestJSON)

	repoAPath := filepath.Join(tempRoot, "repo-a")
	writeTestFile(t, filepath.Join(repoAPath, "AGENTS.md"), "# AGENTS.md — repo-a\n\nCanonical instructions.\n")
	writeTestFile(t, filepath.Join(repoAPath, "CLAUDE.md"), "# repo-a\n\nSee [AGENTS.md](AGENTS.md).\n")
	writeTestFile(t, filepath.Join(repoAPath, "scripts", "common.sh"), "#!/bin/sh\necho common\n")

	repoBPath := filepath.Join(tempRoot, "repo-b")
	writeTestFile(t, filepath.Join(repoBPath, "scripts", "common.sh"), "#!/bin/sh\necho common\n")

	report, err := RunSweep(SweepOptions{
		WorkspaceRoot: tempRoot,
		Sync:          false,
	})
	if err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}

	if report.TotalRepos != 2 {
		t.Fatalf("expected 2 repos, got %d", report.TotalRepos)
	}

	stateA := report.Repos["repo-a"]
	if !stateA.HasAgentsMD || !stateA.HasClaudeMD {
		t.Errorf("repo-a should have both AGENTS.md and CLAUDE.md")
	}

	stateB := report.Repos["repo-b"]
	if stateB.HasAgentsMD || stateB.HasClaudeMD {
		t.Errorf("repo-b should be missing AGENTS.md and CLAUDE.md")
	}
	if stateB.InSync {
		t.Errorf("repo-b should not be in sync")
	}

	if len(report.DuplicateLibs) != 1 {
		t.Errorf("expected 1 duplicate library group, got %d", len(report.DuplicateLibs))
	} else if report.DuplicateLibs[0].BaseName != "common.sh" {
		t.Errorf("expected common.sh duplicate, got %s", report.DuplicateLibs[0].BaseName)
	}

	// Test Sync Mode
	syncReport, err := RunSweep(SweepOptions{
		WorkspaceRoot: tempRoot,
		Sync:          true,
	})
	if err != nil {
		t.Fatalf("RunSweep with sync failed: %v", err)
	}

	stateBSynced := syncReport.Repos["repo-b"]
	if !stateBSynced.HasAgentsMD || !stateBSynced.HasClaudeMD {
		t.Errorf("repo-b should have AGENTS.md and CLAUDE.md created after sync")
	}
	if !stateBSynced.InSync {
		t.Errorf("repo-b should be in sync after sync: %v, %v", stateBSynced.AgentsMDIssues, stateBSynced.ClaudeMDIssues)
	}

	md := syncReport.Markdown()
	if !strings.Contains(md, "Multi-Repo Surface Parity Audit Report") {
		t.Errorf("markdown report missing header")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncNeverWritesThroughClaudeMDSymlink(t *testing.T) {
	tempRoot := t.TempDir()
	writeTestFile(t, filepath.Join(tempRoot, "workspace", "manifest.json"),
		`{"version": 1, "repos": [{"name": "repo-l", "category": "hub", "scope": "active_first_party", "language": "Go"}]}`)
	repo := filepath.Join(tempRoot, "repo-l")
	agents := "# repo-l guidelines\n\nNo self-reference here.\n"
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), agents)
	if err := os.Symlink("AGENTS.md", filepath.Join(repo, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := RunSweep(SweepOptions{WorkspaceRoot: tempRoot, Sync: true}); err != nil {
		t.Fatalf("RunSweep failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != agents {
		t.Fatalf("sync wrote through the CLAUDE.md symlink; AGENTS.md is now:\n%s", got)
	}
}
