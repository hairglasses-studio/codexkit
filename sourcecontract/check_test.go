package sourcecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

func TestCheck_PassesWithSyncedSources(t *testing.T) {
	root := t.TempDir()
	writeSourceContractManifest(t, root, "app")
	writeSourceContractSkill(t, root, "app")
	writeSourceContractMCP(t, root, "app")
	if report := skillsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("skill sync errors: %v", report.Errors)
	}
	if report := mcpsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("mcp sync errors: %v", report.Errors)
	}

	report, err := Check(root, CheckOptions{
		SkipRuntimeInventory: true,
		SkillValidatorMode:   skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected source contract to pass: %+v", report)
	}
	if report.Summary.SkillSurfaceReposChecked != 1 {
		t.Fatalf("SkillSurfaceReposChecked = %d, want 1", report.Summary.SkillSurfaceReposChecked)
	}
	if report.Summary.MCPReposChecked != 1 {
		t.Fatalf("MCPReposChecked = %d, want 1", report.Summary.MCPReposChecked)
	}
	if report.Options.SkillValidatorMode != string(skillsync.ValidatorOff) {
		t.Fatalf("SkillValidatorMode = %q, want off", report.Options.SkillValidatorMode)
	}
}

func TestCheck_FailsOnSkillDrift(t *testing.T) {
	root := t.TempDir()
	writeSourceContractManifest(t, root, "app")
	writeSourceContractSkill(t, root, "app")

	report, err := Check(root, CheckOptions{
		SkipRuntimeInventory: true,
		SkillValidatorMode:   skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected source contract to fail on skill drift")
	}
	if len(report.Repos) != 1 || report.Repos[0].SkillSync == nil || !report.Repos[0].SkillSync.PendingChanges {
		t.Fatalf("expected skill pending changes in repo report: %+v", report.Repos)
	}
}

func TestCheck_FailsOnMCPDrift(t *testing.T) {
	root := t.TempDir()
	writeSourceContractManifest(t, root, "app")
	writeSourceContractMCP(t, root, "app")

	report, err := Check(root, CheckOptions{
		ToolsOnly:            true,
		SkipRuntimeInventory: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected source contract to fail on MCP drift")
	}
	if len(report.Repos) != 1 || report.Repos[0].MCPSync == nil || !report.Repos[0].MCPSync.PendingChanges {
		t.Fatalf("expected MCP pending changes in repo report: %+v", report.Repos)
	}
}

func TestCheck_FailsOnWorkspaceManifestDrift(t *testing.T) {
	root := t.TempDir()
	writeSourceContractManifest(t, root, "missing-app")

	report, err := Check(root, CheckOptions{SkipRuntimeInventory: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected source contract to fail when workspace check fails")
	}
	if report.Workspace.Passed {
		t.Fatal("expected embedded workspace report to fail")
	}
}

func TestArtifactRoundTripAndDrift(t *testing.T) {
	root := t.TempDir()
	writeSourceContractManifest(t, root, "app")
	writeSourceContractSkill(t, root, "app")
	if report := skillsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("skill sync errors: %v", report.Errors)
	}

	report, err := Check(root, CheckOptions{
		SkipRuntimeInventory: true,
		SkillValidatorMode:   skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "inventory", "workspace-source-contract.json")
	if err := WriteArtifact(report, path); err != nil {
		t.Fatal(err)
	}
	if check := CheckArtifact(report, path); !check.Passed {
		t.Fatalf("expected artifact check to pass: %+v", check)
	}

	report.Summary.Warnings++
	if check := CheckArtifact(report, path); check.Passed {
		t.Fatalf("expected artifact check to fail after report mutation: %+v", check)
	}
}

func writeSourceContractManifest(t *testing.T, root, repoName string) {
	t.Helper()
	manifest := map[string]any{
		"version": 1,
		"repos": []map[string]any{{
			"name":            repoName,
			"category":        "application",
			"scope":           "active_first_party",
			"language":        "Go",
			"baseline_target": true,
			"go_work_member":  false,
			"lifecycle":       "active",
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeSourceContractFile(t, root, "workspace/manifest.json", string(data))
}

func writeSourceContractSkill(t *testing.T, root, repoName string) {
	t.Helper()
	writeSourceContractFile(t, root, filepath.Join(repoName, ".agents/skills/surface.yaml"), `{"version":1,"skills":[{"name":"demo"}]}`)
	writeSourceContractFile(t, root, filepath.Join(repoName, ".agents/skills/demo/SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n")
}

func writeSourceContractMCP(t *testing.T, root, repoName string) {
	t.Helper()
	writeSourceContractFile(t, root, filepath.Join(repoName, ".mcp.json"), `{"mcpServers":{"demo":{"command":"bash","args":["-lc","echo demo"]}}}`)
	writeSourceContractFile(t, root, filepath.Join(repoName, ".codex/config.toml"), "")
}

func writeSourceContractFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
