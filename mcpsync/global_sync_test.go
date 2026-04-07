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

func setupGlobalWorkspace(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workspaceRoot := filepath.Join(root, "studio")
	t.Setenv("HOME", home)

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

	report := SyncGlobal(workspaceRoot, configPath, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	if len(report.Servers) != 5 {
		t.Fatalf("expected 5 servers, got %d", len(report.Servers))
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

	report := SyncGlobal(workspaceRoot, configPath, true)
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
