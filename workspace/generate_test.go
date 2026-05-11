package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateManifestUsesLiveReposAndDocsMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docs/inventory/repo-catalog.json", `{
  "repos": [
    {"name": "live-app", "group": "application", "lifecycle": "active", "language_profile": "Go", "local_path": "live-app"},
    {"name": "absent-app", "group": "application", "lifecycle": "active", "language_profile": "Go", "local_path": "absent-app"}
  ]
}`)
	writeTestFile(t, root, "docs/agent-parity/repo-inventory.json", `{
  "repos": [
    {"repo": "live-app", "scope": "active_operator", "expected_codex_baseline": 1}
  ]
}`)
	writeTestFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{"decisions": []}`)

	makeGitRepo(t, root, "live-app")
	writeTestFile(t, root, "live-app/AGENTS.md", "# live-app\n")
	writeTestFile(t, root, "live-app/.codex/config.toml", "")
	writeTestFile(t, root, "live-app/go.mod", "module example.com/live-app\n\ngo 1.26.1\n")

	makeGitRepo(t, root, "live-only")
	writeTestFile(t, root, "live-only/AGENTS.md", "# live-only\n")
	writeTestFile(t, root, "live-only/.mcp.json", `{"mcpServers": {}}`)

	makeGitRepo(t, root, "vault")
	writeTestFile(t, root, "vault/.ralphrc", `mode = "passive"`)

	report, err := GenerateManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.RepoCount != 3 {
		t.Fatalf("RepoCount = %d, want 3", report.RepoCount)
	}
	if findRepo(report.Manifest, "absent-app") != nil {
		t.Fatal("absent catalog repo should not be included in current workspace manifest")
	}

	liveApp := findRepo(report.Manifest, "live-app")
	if liveApp == nil {
		t.Fatal("missing live-app")
	}
	if liveApp.Category != "application" || liveApp.Scope != "active_operator" || !liveApp.BaselineTarget {
		t.Fatalf("live-app metadata = %+v", *liveApp)
	}

	liveOnly := findRepo(report.Manifest, "live-only")
	if liveOnly == nil {
		t.Fatal("missing live-only")
	}
	if liveOnly.Category != "local" || liveOnly.Scope != "active_first_party" || !liveOnly.BaselineTarget {
		t.Fatalf("live-only metadata = %+v", *liveOnly)
	}

	vault := findRepo(report.Manifest, "vault")
	if vault == nil {
		t.Fatal("missing vault")
	}
	if vault.Scope != "compatibility_only" || vault.BaselineTarget {
		t.Fatalf("vault metadata = %+v", *vault)
	}
}

func TestGenerateManifestDecisionScopeOverridesParity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docs/inventory/repo-catalog.json", `{
  "repos": [
    {"name": "device-mcp", "group": "emerging", "lifecycle": "active", "language_profile": "Go", "local_path": "device-mcp"}
  ]
}`)
	writeTestFile(t, root, "docs/agent-parity/repo-inventory.json", `{
  "repos": [
    {"repo": "device-mcp", "scope": "active_first_party", "expected_codex_baseline": 1}
  ]
}`)
	writeTestFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {"repo": "device-mcp", "workspace_scope": "compatibility_only", "baseline_target": false, "go_work_member": false}
  ]
}`)
	makeGitRepo(t, root, "device-mcp")
	writeTestFile(t, root, "device-mcp/AGENTS.md", "# device-mcp\n")
	writeTestFile(t, root, "device-mcp/.mcp.json", `{"mcpServers": {}}`)

	report, err := GenerateManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := findRepo(report.Manifest, "device-mcp")
	if repo == nil {
		t.Fatal("missing device-mcp")
	}
	if repo.Scope != "compatibility_only" || repo.BaselineTarget {
		t.Fatalf("device-mcp metadata = %+v", *repo)
	}
}

func TestGenerateManifestOmitsRemovedActiveBaselineRepos(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docs/inventory/repo-catalog.json", `{
  "repos": [
    {"name": "python-marathon", "group": "local", "lifecycle": "active", "language_profile": "Shell/Config", "local_path": "python-marathon"}
  ]
}`)
	writeTestFile(t, root, "docs/agent-parity/repo-inventory.json", `{"repos": []}`)
	writeTestFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {
      "repo": "python-marathon",
      "state": "removed_from_active_baseline",
      "workspace_scope": "compatibility_only",
      "baseline_target": false,
      "go_work_member": false,
      "archive_candidate": true
    }
  ]
}`)
	makeGitRepo(t, root, "python-marathon")
	writeTestFile(t, root, "python-marathon/AGENTS.md", "# python-marathon\n")
	writeTestFile(t, root, "python-marathon/.codex/config.toml", "")

	report, err := GenerateManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if findRepo(report.Manifest, "python-marathon") != nil {
		t.Fatal("removed active baseline repo should not be included in generated workspace manifest")
	}
	if report.RepoCount != 0 {
		t.Fatalf("RepoCount = %d, want 0", report.RepoCount)
	}
}

func findRepo(manifest Manifest, name string) *Repo {
	for i := range manifest.Repos {
		if manifest.Repos[i].Name == name {
			return &manifest.Repos[i]
		}
	}
	return nil
}

func makeGitRepo(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
