package mcpsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkspaceFile(t *testing.T, base, name, content string) {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeExecutableWorkspaceFile(t *testing.T, base, name, content string) {
	t.Helper()
	writeWorkspaceFile(t, base, name, content)
	path := filepath.Join(base, name)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityCardCandidatesIncludesGenericToolsMCPPath(t *testing.T) {
	workspaceRoot := filepath.Join("workspace-root")
	repoPath := filepath.Join(workspaceRoot, "service-app")
	candidates := capabilityCardCandidates(workspaceRoot, repoPath, "systemd")
	want := filepath.Join(workspaceRoot, "tools", "mcp", "systemd-mcp", ".well-known", "mcp.json")
	for _, candidate := range candidates {
		if candidate == want {
			return
		}
	}
	t.Fatalf("expected capability card candidates to include %q, got %#v", want, candidates)
}

func setupGlobalWorkspace(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspaceRoot := filepath.Join(root, "studio")
	t.Setenv("HOME", home)

	writeExecutableWorkspaceFile(t, home, "public-workspace/system-tools/mcp/systemd-mcp/systemd-mcp", "#!/bin/sh\nexit 0\n")
	writeExecutableWorkspaceFile(t, workspaceRoot, "service-app/bin/service-app-mcp", "#!/bin/sh\nexit 0\n")

	writeWorkspaceFile(t, workspaceRoot, ".mcp.json", `{
  "mcpServers": {
    "systemd": {
      "command": "./systemd-mcp",
      "cwd": "${HOME}/public-workspace/system-tools/mcp/systemd-mcp"
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "systemd-mcp/.well-known/mcp.json", `{
  "tool_count": 10,
  "categories": ["service-management", "devops"],
  "capabilities": {"tools": true, "resources": true, "prompts": true}
}`)
	writeWorkspaceFile(t, workspaceRoot, "service-app/.mcp.json", `{
  "mcpServers": {
    "service-app": {
      "command": "./bin/service-app-mcp"
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "agent-hub/.mcp.json", `{
  "mcpServers": {
    "agent-hub": {
      "command": "bash",
      "args": ["./scripts/dev/run-mcp.sh", "--scan-path", "~/public-workspace"],
      "cwd": "."
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "media-device/.mcp.json", `{
  "mcpServers": {
    "kirkwood": {
      "command": "bash",
      "args": ["./scripts/mcp/kirkwood-mcp.sh"]
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "mobile-app/.mcp.json", `{
  "mcpServers": {
    "kirkwood": {
      "command": "bash",
      "args": ["-lc", "echo android"]
    }
  }
}`)

	configPath := filepath.Join(home, ".codex", "config.toml")
	writeWorkspaceFile(t, home, ".codex/config.toml", "model = \"gpt-5.5\"\n")
	return workspaceRoot, configPath, home
}

func TestSyncGlobal_WritesNormalizedWorkspaceServers(t *testing.T) {
	workspaceRoot, configPath, home := setupGlobalWorkspace(t)

	report := SyncGlobal(workspaceRoot, configPath, "", false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	if len(report.Servers) != 5 {
		t.Fatalf("expected 5 servers, got %d", len(report.Servers))
	}
	validations := map[string]string{}
	for _, server := range report.Servers {
		validations[server.Name] = server.Validation
	}
	if validations["systemd"] != "ready" {
		t.Fatalf("expected systemd validation ready, got %q", validations["systemd"])
	}
	if validations["service-app"] != "ready" {
		t.Fatalf("expected service-app validation ready, got %q", validations["service-app"])
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, GlobalStartMarker) {
		t.Fatal("expected generated start marker")
	}
	if !strings.Contains(content, "[mcp_servers.systemd]") {
		t.Fatal("expected systemd block")
	}
	if got := strings.Count(content, `default_tools_approval_mode = "approve"`); got != len(report.Servers) {
		t.Fatalf("expected approve-by-default for all %d generated servers, got %d:\n%s", len(report.Servers), got, content)
	}
	wantSystemdCWD := filepath.Join(home, "public-workspace", "system-tools", "mcp", "systemd-mcp")
	if !strings.Contains(content, `cwd = "`+wantSystemdCWD+`"`) {
		t.Fatalf("expected expanded systemd cwd %q", wantSystemdCWD)
	}
	wantServiceAppCWD := filepath.Join(workspaceRoot, "service-app")
	if !strings.Contains(content, `cwd = "`+wantServiceAppCWD+`"`) {
		t.Fatalf("expected service-app cwd %q", wantServiceAppCWD)
	}
	wantAgentHubPath := filepath.Join(home, "public-workspace")
	if !strings.Contains(content, `--scan-path", "`+wantAgentHubPath+`"`) {
		t.Fatalf("expected expanded agent-hub scan path %q", wantAgentHubPath)
	}
	if !strings.Contains(content, "[mcp_servers.media-device-kirkwood]") {
		t.Fatal("expected prefixed collision alias for media-device kirkwood")
	}
	if !strings.Contains(content, "[mcp_servers.mobile-app-kirkwood]") {
		t.Fatal("expected prefixed collision alias for mobile-app kirkwood")
	}
	if !strings.Contains(content, "10 tools; tools/resources/prompts; service-management, devops") {
		t.Fatal("expected capability summary from server card")
	}
}

func TestSyncGlobal_DryRunDoesNotWrite(t *testing.T) {
	workspaceRoot, configPath, _ := setupGlobalWorkspace(t)

	report := SyncGlobal(workspaceRoot, configPath, "", true)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), GlobalStartMarker) {
		t.Fatal("dry-run should not write generated block")
	}
}

func TestSyncGlobal_RespectsPolicyAndManifest(t *testing.T) {
	workspaceRoot, configPath, _ := setupGlobalWorkspace(t)
	writeWorkspaceFile(t, workspaceRoot, "prompt-improver/.mcp.json", `{
  "mcpServers": {
    "prompt-improver": {
      "command": "bash",
      "args": ["./scripts/mcp/prompt-improver-mcp.sh"]
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "media-device", "category": "device", "scope": "active_first_party"},
    {"name": "mobile-app", "category": "device", "scope": "active_first_party"},
    {"name": "service-app", "category": "application", "scope": "active_operator"},
    {"name": "agent-hub", "category": "hub", "scope": "active_operator"},
    {"name": "prompt-improver", "category": "tooling", "scope": "compatibility_only"}
  ]
}`)
	writeWorkspaceFile(t, workspaceRoot, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "defaults": {
    "include_root": true,
    "ready_only": true
  },
  "manifest": {
    "use_workspace_manifest": true,
    "allow_unlisted_repos": false,
    "exclude_scopes": ["compatibility_only"]
  },
  "servers": [
    {"repo": "media-device", "server": "kirkwood", "alias": "cast-kirkwood"},
    {"repo": "mobile-app", "server": "kirkwood", "enabled": false}
  ]
}`)

	report := SyncGlobal(workspaceRoot, configPath, "", true)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	if !report.PolicyLoaded {
		t.Fatal("expected policy to load")
	}
	if !report.ManifestLoaded {
		t.Fatal("expected manifest to load")
	}

	names := make(map[string]bool)
	for _, server := range report.Servers {
		names[server.Name] = true
	}
	if !names["cast-kirkwood"] {
		t.Fatal("expected explicit alias from policy")
	}
	if names["mobile-app-kirkwood"] {
		t.Fatal("expected disabled server to be skipped")
	}
	if names["prompt-improver"] {
		t.Fatal("expected compatibility_only repo to be skipped")
	}

	reasons := map[string]string{}
	for _, skipped := range report.Skipped {
		reasons[skipped.SourceRepo+":"+skipped.SourceServer] = skipped.Reason
	}
	if reasons["mobile-app:kirkwood"] != "server disabled by policy" {
		t.Fatalf("unexpected disabled-server reason: %q", reasons["mobile-app:kirkwood"])
	}
	if reasons["prompt-improver:prompt-improver"] != `repo scope "compatibility_only" excluded by policy` {
		t.Fatalf("unexpected manifest-skip reason: %q", reasons["prompt-improver:prompt-improver"])
	}
}
