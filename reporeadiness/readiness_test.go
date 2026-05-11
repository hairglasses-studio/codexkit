package reporeadiness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoreAssignsCleanupAndReviewLanes(t *testing.T) {
	root := t.TempDir()
	writeReadinessFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "active", "category": "hub", "scope": "active_operator", "language": "Go", "baseline_target": false, "go_work_member": false, "lifecycle": "active"},
    {"name": "compat", "category": "emerging", "scope": "compatibility_only", "language": "Go", "baseline_target": false, "go_work_member": false, "lifecycle": "maintenance-only"}
  ]
}`)
	writeReadinessFile(t, root, ".ralph/fleet.toml", "scan_path = \"/tmp\"\n\n[repo_modes]\nactive = \"active\"\n")
	writeReadinessFile(t, root, "active/README.md", "# Active\n")
	writeReadinessFile(t, root, "compat/README.md", "# Compat\n")
	initGitRepo(t, filepath.Join(root, "active"))
	initGitRepo(t, filepath.Join(root, "compat"))
	writeReadinessFile(t, root, "active/dirty.txt", "dirty\n")

	report, err := Score(root, Options{AllScopes: true, GeneratedAt: "2026-05-11T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	byRepo := map[string]RepoScore{}
	for _, repo := range report.Repos {
		byRepo[repo.RepoName] = repo
	}
	if byRepo["active"].Lane != LaneCleanupFirst {
		t.Fatalf("active lane = %q, want cleanup-first; score=%+v", byRepo["active"].Lane, byRepo["active"])
	}
	if byRepo["compat"].Lane != LaneReviewOnly {
		t.Fatalf("compat lane = %q, want review-only", byRepo["compat"].Lane)
	}
	if report.Summary.CleanupFirst != 1 || report.Summary.ReviewOnly != 1 {
		t.Fatalf("summary lanes = %+v", report.Summary)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Repo Mutation Readiness") || !strings.Contains(markdown, "cleanup_first") {
		t.Fatalf("unexpected markdown:\n%s", markdown)
	}
}

func TestParseBranchLine(t *testing.T) {
	branch, ahead, behind := parseBranchLine("## feature/x...origin/feature/x [ahead 2, behind 3]")
	if branch != "feature/x" || ahead != 2 || behind != 3 {
		t.Fatalf("parsed = %q/%d/%d", branch, ahead, behind)
	}
}

func writeReadinessFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	runReadinessGit(t, path, "init")
	runReadinessGit(t, path, "config", "user.email", "test@example.test")
	runReadinessGit(t, path, "config", "user.name", "Test User")
	runReadinessGit(t, path, "add", ".")
	runReadinessGit(t, path, "commit", "-m", "initial")
}

func runReadinessGit(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
