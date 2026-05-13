package fleetaudit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudit_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	report := Audit(dir)

	if report.ScanPath != dir {
		t.Errorf("ScanPath = %q, want %q", report.ScanPath, dir)
	}
	if report.TotalRepos != 0 {
		t.Errorf("TotalRepos = %d, want 0", report.TotalRepos)
	}
	if len(report.Repos) != 0 {
		t.Errorf("Repos = %d entries, want 0", len(report.Repos))
	}
}

func TestAudit_NonexistentDir(t *testing.T) {
	report := Audit("/nonexistent-path-that-does-not-exist")

	if report.TotalRepos != 0 {
		t.Errorf("TotalRepos = %d, want 0 for nonexistent dir", report.TotalRepos)
	}
}

func TestAudit_SkipsNonGitDirs(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory without .git
	if err := os.MkdirAll(filepath.Join(dir, "not-a-repo"), 0755); err != nil {
		t.Fatal(err)
	}

	report := Audit(dir)
	if report.TotalRepos != 0 {
		t.Errorf("TotalRepos = %d, want 0 (should skip non-git dirs)", report.TotalRepos)
	}
}

func TestAudit_SkipsFiles(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file in scan dir
	if err := os.WriteFile(filepath.Join(dir, "somefile.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	report := Audit(dir)
	if report.TotalRepos != 0 {
		t.Errorf("TotalRepos = %d, want 0 (should skip files)", report.TotalRepos)
	}
}

func TestAudit_DetectsGitRepo(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "test-repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	report := Audit(dir)
	if report.TotalRepos != 1 {
		t.Fatalf("TotalRepos = %d, want 1", report.TotalRepos)
	}
	if report.Repos[0].RepoName != "test-repo" {
		t.Errorf("RepoName = %q, want %q", report.Repos[0].RepoName, "test-repo")
	}
	if report.Repos[0].RepoPath != repoDir {
		t.Errorf("RepoPath = %q, want %q", report.Repos[0].RepoPath, repoDir)
	}
}

func TestAudit_PassedCountsMatchRepos(t *testing.T) {
	dir := t.TempDir()
	// Create two mock repos
	for _, name := range []string{"repo-a", "repo-b"} {
		if err := os.MkdirAll(filepath.Join(dir, name, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	report := Audit(dir)
	if report.TotalRepos != 2 {
		t.Fatalf("TotalRepos = %d, want 2", report.TotalRepos)
	}
	if report.Passed+report.Failed != report.TotalRepos {
		t.Errorf("Passed(%d) + Failed(%d) != TotalRepos(%d)",
			report.Passed, report.Failed, report.TotalRepos)
	}
}

func TestModule_NameAndInit(t *testing.T) {
	m := Module()
	if m.Name() != "fleetaudit" {
		t.Errorf("Name() = %q, want %q", m.Name(), "fleetaudit")
	}
	if err := m.Init(); err != nil {
		t.Errorf("Init() = %v, want nil", err)
	}
}

func TestModule_ToolCount(t *testing.T) {
	m := Module()
	tools := m.Tools()
	if len(tools) != 2 {
		t.Errorf("len(Tools()) = %d, want 2", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"fleet_audit", "fleet_report"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestModule_FleetAuditHandler(t *testing.T) {
	dir := t.TempDir()
	m := Module()
	tools := m.Tools()

	var auditTool func(map[string]any) (any, error)
	for _, tool := range tools {
		if tool.Name == "fleet_audit" {
			auditTool = tool.Handler
			break
		}
	}
	if auditTool == nil {
		t.Fatal("fleet_audit tool not found")
	}

	result, err := auditTool(map[string]any{"scan_path": dir})
	if err != nil {
		t.Fatalf("fleet_audit handler error: %v", err)
	}
	report, ok := result.(FleetReport)
	if !ok {
		t.Fatalf("fleet_audit returned %T, want FleetReport", result)
	}
	if report.ScanPath != dir {
		t.Errorf("ScanPath = %q, want %q", report.ScanPath, dir)
	}
}

func TestModule_FleetReportHandler(t *testing.T) {
	dir := t.TempDir()
	m := Module()
	tools := m.Tools()

	var reportTool func(map[string]any) (any, error)
	for _, tool := range tools {
		if tool.Name == "fleet_report" {
			reportTool = tool.Handler
			break
		}
	}
	if reportTool == nil {
		t.Fatal("fleet_report tool not found")
	}

	result, err := reportTool(map[string]any{"scan_path": dir})
	if err != nil {
		t.Fatalf("fleet_report handler error: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("fleet_report returned %T, want string", result)
	}
	if s == "" {
		t.Error("fleet_report returned empty string")
	}
}
