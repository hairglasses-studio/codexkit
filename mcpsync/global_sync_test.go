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

func setupGlobalWorkspace(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspaceRoot := filepath.Join(root, "studio")
	t.Setenv("HOME", home)

	writeExecutableWorkspaceFile(t, home, "hairglasses-studio/dotfiles/mcp/systemd-mcp/systemd-mcp", "#!/bin/sh\nexit 0\n")
	writeExecutableWorkspaceFile(t, workspaceRoot, "jobb/bin/jobb-mcp", "#!/bin/sh\nexit 0\n")

	writeWorkspaceFile(t, workspaceRoot, ".mcp.json", `{
  "mcpServers": {
    "systemd": {
      "command": "./systemd-mcp",
      "cwd": "${HOME}/hairglasses-studio/dotfiles/mcp/systemd-mcp"
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "systemd-mcp/.well-known/mcp.json", `{
  "tool_count": 10,
  "categories": ["service-management", "devops"],
  "capabilities": {"tools": true, "resources": true, "prompts": true}
}`)
	writeWorkspaceFile(t, workspaceRoot, "jobb/.mcp.json", `{
  "mcpServers": {
    "jobb": {
      "command": "./bin/jobb-mcp"
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "ralphglasses/.mcp.json", `{
  "mcpServers": {
    "ralphglasses": {
      "command": "bash",
      "args": ["./scripts/dev/run-mcp.sh", "--scan-path", "~/hairglasses-studio"],
      "cwd": "."
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "chromecast4k-libre/.mcp.json", `{
  "mcpServers": {
    "kirkwood": {
      "command": "bash",
      "args": ["./scripts/mcp/kirkwood-mcp.sh"]
    }
  }
}`)
	writeWorkspaceFile(t, workspaceRoot, "hg-android/.mcp.json", `{
  "mcpServers": {
    "kirkwood": {
      "command": "bash",
      "args": ["-lc", "echo android"]
    }
  }
}`)

	configPath := filepath.Join(home, ".codex", "config.toml")
	writeWorkspaceFile(t, home, ".codex/config.toml", "model = \"gpt-5.4\"\n")
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
	if validations["jobb"] != "ready" {
		t.Fatalf("expected jobb validation ready, got %q", validations["jobb"])
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
	wantSystemdCWD := filepath.Join(home, "hairglasses-studio", "dotfiles", "mcp", "systemd-mcp")
	if !strings.Contains(content, `cwd = "`+wantSystemdCWD+`"`) {
		t.Fatalf("expected expanded systemd cwd %q", wantSystemdCWD)
	}
	wantJobbCWD := filepath.Join(workspaceRoot, "jobb")
	if !strings.Contains(content, `cwd = "`+wantJobbCWD+`"`) {
		t.Fatalf("expected jobb cwd %q", wantJobbCWD)
	}
	wantRalphPath := filepath.Join(home, "hairglasses-studio")
	if !strings.Contains(content, `--scan-path", "`+wantRalphPath+`"`) {
		t.Fatalf("expected expanded ralphglasses scan path %q", wantRalphPath)
	}
	if !strings.Contains(content, "[mcp_servers.chromecast4k-libre-kirkwood]") {
		t.Fatal("expected prefixed collision alias for chromecast kirkwood")
	}
	if !strings.Contains(content, "[mcp_servers.hg-android-kirkwood]") {
		t.Fatal("expected prefixed collision alias for hg-android kirkwood")
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
    {"name": "chromecast4k-libre", "category": "device", "scope": "active_first_party"},
    {"name": "hg-android", "category": "device", "scope": "active_first_party"},
    {"name": "jobb", "category": "application", "scope": "active_operator"},
    {"name": "ralphglasses", "category": "hub", "scope": "active_operator"},
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
    {"repo": "chromecast4k-libre", "server": "kirkwood", "alias": "cast-kirkwood"},
    {"repo": "hg-android", "server": "kirkwood", "enabled": false}
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
	if names["hg-android-kirkwood"] {
		t.Fatal("expected disabled server to be skipped")
	}
	if names["prompt-improver"] {
		t.Fatal("expected compatibility_only repo to be skipped")
	}

	reasons := map[string]string{}
	for _, skipped := range report.Skipped {
		reasons[skipped.SourceRepo+":"+skipped.SourceServer] = skipped.Reason
	}
	if reasons["hg-android:kirkwood"] != "server disabled by policy" {
		t.Fatalf("unexpected disabled-server reason: %q", reasons["hg-android:kirkwood"])
	}
	if reasons["prompt-improver:prompt-improver"] != `repo scope "compatibility_only" excluded by policy` {
		t.Fatalf("unexpected manifest-skip reason: %q", reasons["prompt-improver:prompt-improver"])
	}
}
