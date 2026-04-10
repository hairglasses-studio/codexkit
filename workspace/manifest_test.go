package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeWorkspaceFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifestAndFilter(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "mcpkit", Scope: "active_operator", BaselineTarget: true, GoWorkMember: true},
			{Name: "dotfiles", Scope: "active_operator", BaselineTarget: true},
			{Name: "prompt-improver", Scope: "compatibility_only", BaselineTarget: true, GoWorkMember: true},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "workspace/manifest.json", string(data))

	loaded, err := LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if loaded.Version != 1 {
		t.Fatalf("Version = %d, want 1", loaded.Version)
	}

	got := loaded.RepoNames(Filter{Scope: "active_operator", BaselineOnly: true})
	want := []string{"mcpkit", "dotfiles"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoNames() = %v, want %v", got, want)
	}

	got = loaded.RepoNames(Filter{GoWorkOnly: true})
	want = []string{"mcpkit", "prompt-improver"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoNames(go_work) = %v, want %v", got, want)
	}
}

func TestParseGoWorkModules(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "go.work", `go 1.26.1

use (
	./mcpkit
	./docs
)
`)

	got, err := ParseGoWorkModules(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatalf("ParseGoWorkModules() error = %v", err)
	}
	want := []string{"./docs", "./mcpkit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseGoWorkModules() = %v, want %v", got, want)
	}
}

func TestCheckWorkspacePasses(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "mcpkit", GoWorkMember: true},
			{Name: "docs", GoWorkMember: true},
			{Name: "dotfiles"},
		},
	}

	writeWorkspaceFile(t, root, "mcpkit/go.mod", "module github.com/hairglasses-studio/mcpkit\n")
	writeWorkspaceFile(t, root, "docs/go.mod", "module github.com/hairglasses-studio/docs\n")
	if err := os.MkdirAll(filepath.Join(root, "dotfiles"), 0755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "go.work", `go 1.26.1

use (
	./docs
	./mcpkit
)
`)

	report := Check(root, manifest)
	if !report.Passed {
		t.Fatalf("Check() passed = false, findings = %#v", report.Findings)
	}
}

func TestCheckWorkspaceFlagsMissingAndUnexpectedGoWorkMembers(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "mcpkit", GoWorkMember: true},
			{Name: "docs", GoWorkMember: true},
		},
	}

	writeWorkspaceFile(t, root, "mcpkit/go.mod", "module github.com/hairglasses-studio/mcpkit\n")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "rogue/go.mod", "module github.com/hairglasses-studio/rogue\n")
	writeWorkspaceFile(t, root, "go.work", `go 1.26.1

use (
	./mcpkit
	./rogue
)
`)

	report := Check(root, manifest)
	if report.Passed {
		t.Fatalf("Check() passed = true, want false")
	}

	var missingDocs, extraRogue, missingDocsModule bool
	for _, finding := range report.Findings {
		switch {
		case finding.Check == "go_work_member" && finding.Repo == "docs" && !finding.Passed:
			missingDocs = true
		case finding.Check == "go_work_member" && finding.Repo == "rogue" && !finding.Passed:
			extraRogue = true
		case finding.Check == "go_module" && finding.Repo == "docs" && !finding.Passed:
			missingDocsModule = true
		}
	}
	if !missingDocs || !extraRogue || !missingDocsModule {
		t.Fatalf("expected missing docs member/module and rogue extra, got %#v", report.Findings)
	}
}

func TestCheckWorkspaceFlagsMergedOutRepoStillActive(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "sway-mcp-go", Scope: "active_first_party", BaselineTarget: true, GoWorkMember: true, Lifecycle: "canonical"},
			{Name: "docs", GoWorkMember: true},
		},
	}

	writeWorkspaceFile(t, root, "sway-mcp-go/go.mod", "module github.com/hairglasses-studio/sway-mcp-go\n")
	writeWorkspaceFile(t, root, "docs/go.mod", "module github.com/hairglasses-studio/docs\n")
	writeWorkspaceFile(t, root, "go.work", `go 1.26.1

use (
	./docs
	./sway-mcp-go
)
`)
	writeWorkspaceFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {"repo": "sway-mcp-go", "state": "merged_out_of_active_surface"}
  ]
}`)

	report := Check(root, manifest)
	if report.Passed {
		t.Fatalf("Check() passed = true, want false")
	}

	var scopeFail, goWorkFail, baselineFail bool
	for _, finding := range report.Findings {
		switch {
		case finding.Check == "consolidation_scope" && finding.Repo == "sway-mcp-go" && !finding.Passed:
			scopeFail = true
		case finding.Check == "consolidation_go_work_member" && finding.Repo == "sway-mcp-go" && !finding.Passed:
			goWorkFail = true
		case finding.Check == "consolidation_baseline_target" && finding.Repo == "sway-mcp-go" && !finding.Passed:
			baselineFail = true
		}
	}
	if !scopeFail || !goWorkFail || !baselineFail {
		t.Fatalf("expected consolidation drift failures, got %#v", report.Findings)
	}
}

func TestCheckWorkspacePassesMergedOutRepoWhenDemoted(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "sway-mcp-go", Scope: "compatibility_only", BaselineTarget: false, GoWorkMember: false, Lifecycle: "compatibility"},
			{Name: "docs", GoWorkMember: true},
		},
	}

	if err := os.MkdirAll(filepath.Join(root, "sway-mcp-go"), 0755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "docs/go.mod", "module github.com/hairglasses-studio/docs\n")
	writeWorkspaceFile(t, root, "go.work", `go 1.26.1

use (
	./docs
)
`)
	writeWorkspaceFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {"repo": "sway-mcp-go", "state": "merged_out_of_active_surface"}
  ]
}`)

	report := Check(root, manifest)
	if !report.Passed {
		t.Fatalf("Check() passed = false, findings = %#v", report.Findings)
	}
}
