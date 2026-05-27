package mcpsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRuntimeInventory(t *testing.T) {
	root := setupRuntimeInventoryWorkspace(t)

	inventory, err := BuildRuntimeInventory(RuntimeInventoryOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-08T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Projection.ServerCount != 1 {
		t.Fatalf("ServerCount = %d, want 1", inventory.Projection.ServerCount)
	}
	if inventory.Projection.ReadyCount != 1 {
		t.Fatalf("ReadyCount = %d, want 1", inventory.Projection.ReadyCount)
	}
	if inventory.Projection.SkippedCount != 1 {
		t.Fatalf("SkippedCount = %d, want 1", inventory.Projection.SkippedCount)
	}
	if inventory.Servers[0].SourceFile != "app/.mcp.json" {
		t.Fatalf("SourceFile = %q, want app/.mcp.json", inventory.Servers[0].SourceFile)
	}
	if !strings.Contains(RenderRuntimeInventoryMarkdown(inventory), "Policy-skipped servers | 1") {
		t.Fatal("markdown summary did not include skipped server count")
	}
}

func TestCheckRuntimeInventoryPassesWithAllowedSkippedAndArtifacts(t *testing.T) {
	root := setupRuntimeInventoryWorkspace(t)
	jsonPath := filepath.Join(root, "docs/inventory/mcp-runtime-inventory-2026-05-08.json")
	markdownPath := filepath.Join(root, "docs/inventory/mcp-runtime-inventory-2026-05-08.md")

	inventory, err := BuildRuntimeInventory(RuntimeInventoryOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-08T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRuntimeInventory(inventory, jsonPath, markdownPath); err != nil {
		t.Fatal(err)
	}

	report, err := CheckRuntimeInventory(RuntimeInventoryCheckOptions{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		AllowSkipped:  []string{"legacy:legacy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("check failed: %+v", report.Findings)
	}
}

func TestCheckRuntimeInventoryUsesPolicyAllowedSkipped(t *testing.T) {
	root := setupRuntimeInventoryWorkspace(t)
	writeRuntimeTestFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "defaults": {"include_root": false, "ready_only": false},
  "manifest": {
    "use_workspace_manifest": true,
    "allow_unlisted_repos": false,
    "include_scopes": ["active_first_party"],
    "exclude_scopes": ["compatibility_only"]
  },
  "runtime_inventory": {
    "allowed_skipped": ["legacy:legacy"]
  }
}`)

	report, err := CheckRuntimeInventory(RuntimeInventoryCheckOptions{
		WorkspaceRoot: root,
		SkipArtifacts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("check failed: %+v", report.Findings)
	}
}

func TestCheckRuntimeInventoryAllowsPolicyAndRemoteSkippedByReason(t *testing.T) {
	report := unexpectedRuntimeSkipped([]RuntimeSkippedServer{
		{
			SourceRepo:   "legacy",
			SourceServer: "legacy",
			Reason:       `repo scope "compatibility_only" excluded by policy`,
		},
		{
			SourceRepo:   "app",
			SourceServer: "openai-developer-docs",
			Reason:       "server not ready: remote transport; command not validated",
		},
	}, nil, false)
	if len(report) != 0 {
		t.Fatalf("expected policy/remote skipped entries to be allowed, got %v", report)
	}
}

func TestCheckRuntimeInventoryFailsInvalidLauncher(t *testing.T) {
	root := t.TempDir()
	writeRuntimeTestFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeRuntimeTestFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "defaults": {"include_root": false, "ready_only": false},
  "manifest": {
    "use_workspace_manifest": true,
    "allow_unlisted_repos": false,
    "include_scopes": ["active_first_party"],
    "exclude_scopes": ["compatibility_only"]
  }
}`)
	writeRuntimeTestFile(t, root, "app/.mcp.json", `{"mcpServers":{"app":{"command":"/missing/app-mcp"}}}`)

	report, err := CheckRuntimeInventory(RuntimeInventoryCheckOptions{
		WorkspaceRoot: root,
		SkipArtifacts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("check passed with invalid launcher")
	}
	if !strings.Contains(report.Findings[0].Message, "/missing/app-mcp") {
		t.Fatalf("invalid launcher message = %q", report.Findings[0].Message)
	}
}

func TestCheckRuntimeInventoryFailsArtifactDrift(t *testing.T) {
	root := setupRuntimeInventoryWorkspace(t)
	jsonPath := filepath.Join(root, "docs/inventory/mcp-runtime-inventory-2026-05-08.json")
	markdownPath := filepath.Join(root, "docs/inventory/mcp-runtime-inventory-2026-05-08.md")

	inventory, err := BuildRuntimeInventory(RuntimeInventoryOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-08T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRuntimeInventory(inventory, jsonPath, markdownPath); err != nil {
		t.Fatal(err)
	}
	writeRuntimeTestFile(t, root, "docs/inventory/mcp-runtime-inventory-2026-05-08.md", "stale\n")

	report, err := CheckRuntimeInventory(RuntimeInventoryCheckOptions{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		AllowSkipped:  []string{"legacy:legacy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("check passed with stale markdown artifact")
	}
	if !strings.Contains(runtimeFindingMessage(report, "markdown_artifact"), "does not match") {
		t.Fatalf("markdown artifact finding = %+v", report.Findings)
	}
}

func setupRuntimeInventoryWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeRuntimeTestFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"},
    {"name": "legacy", "category": "emerging", "scope": "compatibility_only", "language": "Go", "baseline_target": false, "go_work_member": false, "lifecycle": "maintenance-only"}
  ]
}`)
	writeRuntimeTestFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "defaults": {"include_root": false, "ready_only": false},
  "manifest": {
    "use_workspace_manifest": true,
    "allow_unlisted_repos": false,
    "include_scopes": ["active_first_party"],
    "exclude_scopes": ["compatibility_only"]
  }
}`)
	writeRuntimeExecutable(t, root, "app/run-mcp.sh")
	writeRuntimeTestFile(t, root, "app/.mcp.json", `{"mcpServers":{"app":{"command":"./run-mcp.sh"}}}`)
	writeRuntimeExecutable(t, root, "legacy/run-mcp.sh")
	writeRuntimeTestFile(t, root, "legacy/.mcp.json", `{"mcpServers":{"legacy":{"command":"./run-mcp.sh"}}}`)
	return root
}

func runtimeFindingMessage(report RuntimeInventoryCheckReport, check string) string {
	for _, finding := range report.Findings {
		if finding.Check == check {
			return finding.Message
		}
	}
	return ""
}

func writeRuntimeExecutable(t *testing.T, root, rel string) {
	t.Helper()
	writeRuntimeTestFile(t, root, rel, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, rel), 0755); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
