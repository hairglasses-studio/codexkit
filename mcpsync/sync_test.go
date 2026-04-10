package mcpsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, base, name, content string) {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setupMCPRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
			},
			"filesystem": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-filesystem"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", `approval_policy = "on-request"
`)
	return dir
}

func TestSync_CreatesServerBlocks(t *testing.T) {
	dir := setupMCPRepo(t)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	// Check config.toml was updated
	data, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "[mcp_servers.github]") {
		t.Error("expected [mcp_servers.github] in config.toml")
	}
	if !strings.Contains(content, "[mcp_servers.filesystem]") {
		t.Error("expected [mcp_servers.filesystem] in config.toml")
	}

	// Should record both generated profiles.
	updated := 0
	for _, a := range report.Actions {
		if a.Action == "update" || a.Action == "create" {
			updated++
		}
	}
	if updated != 2 {
		t.Errorf("expected 2 generated profile updates, got %d", updated)
	}
}

func TestSync_DoesNotInventRepoRelativeCWD(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"demo": map[string]any{
				"command": "bash",
				"args":    []string{"-lc", "exec ./scripts/run-demo.sh"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", "")

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, `cwd = "."`) {
		t.Fatalf("expected sync to avoid inventing repo-relative cwd, got:\n%s", content)
	}
}

func TestSync_DetectsExisting(t *testing.T) {
	dir := setupMCPRepo(t)

	// First sync
	Sync(dir, false)

	// Second sync — should detect existing
	report := Sync(dir, false)
	for _, a := range report.Actions {
		if a.Action != "unchanged" {
			t.Errorf("expected unchanged, got %s for %s", a.Action, a.Server)
		}
	}
}

func TestSync_DryRunNoWrite(t *testing.T) {
	dir := setupMCPRepo(t)

	report := Sync(dir, true)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if strings.Contains(string(data), "[mcp_servers.") {
		t.Error("dry-run should not write to config.toml")
	}
	if report.Diff == "" {
		t.Error("dry-run report should include a unified diff")
	}
}

func TestList_ReturnsServerNames(t *testing.T) {
	dir := setupMCPRepo(t)
	names, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 servers, got %d", len(names))
	}
}

func TestSync_EmptyMCPServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".mcp.json", `{"mcpServers": {}}`)
	writeFile(t, dir, ".codex/config.toml", "")

	report := Sync(dir, false)
	if len(report.Actions) != 0 {
		t.Error("expected no actions for empty mcpServers")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), startMarker) {
		t.Fatal("did not expect an empty managed MCP block for empty mcpServers")
	}
}

func TestSync_IgnoresExampleOnlyMCPServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `.mcp.json`, `{
  "mcpServers": {
    "_example_stdio_server": {
      "command": "go",
      "args": ["run", "./cmd/example"]
    }
  }
}`)
	writeFile(t, dir, ".codex/config.toml", "")

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	if len(report.Actions) != 0 {
		t.Fatalf("expected no generated actions for example-only MCP servers, got %+v", report.Actions)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), startMarker) {
		t.Fatal("did not expect a managed MCP block for example-only MCP servers")
	}

	names, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("expected no generated profile names for example-only MCP servers, got %v", names)
	}
}

func TestSync_RemovesManagedBlockWhenNoRealServersRemain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `.mcp.json`, `{"mcpServers": {}}`)
	writeFile(t, dir, ".codex/config.toml", `approval_policy = "never"

# BEGIN GENERATED MCP SERVERS: codex-mcp-sync
[mcp_servers.demo]
command = "stale"
# END GENERATED MCP SERVERS: codex-mcp-sync

model = "gpt-5"
`)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, startMarker) {
		t.Fatal("expected managed MCP block to be removed when no servers remain")
	}
	if !strings.Contains(content, `model = "gpt-5"`) {
		t.Fatal("expected trailing config to be preserved")
	}
}

func TestSync_HTTPTransport(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"remote": map[string]any{
				"transport": "http",
				"url":       "https://example.com/mcp",
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", "")

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	content := string(data)
	if !strings.Contains(content, `transport = "http"`) {
		t.Error("expected transport = http in config.toml")
	}
	if !strings.Contains(content, `url = "https://example.com/mcp"`) {
		t.Error("expected url in config.toml")
	}
}

func TestSync_ReplacesManagedBlockPreservingOtherConfig(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", `approval_policy = "never"

# BEGIN GENERATED MCP SERVERS: codex-mcp-sync
[mcp_servers.stale]
command = "stale"
# END GENERATED MCP SERVERS: codex-mcp-sync

model = "gpt-5"
`)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	content := string(data)
	if !strings.Contains(content, `approval_policy = "never"`) {
		t.Fatal("expected prefix config to be preserved")
	}
	if !strings.Contains(content, `# Generated by codexkit/scripts/codex-mcp-sync.sh from .mcp.json`) {
		t.Fatal("expected codexkit generator comment in managed block")
	}
	if !strings.Contains(content, `model = "gpt-5"`) {
		t.Fatal("expected suffix config to be preserved")
	}
	if strings.Contains(content, "[mcp_servers.stale]") {
		t.Fatal("expected stale managed block to be replaced")
	}
	if !strings.Contains(content, "[mcp_servers.github]") {
		t.Fatal("expected github profile in managed block")
	}
}

func TestSync_InsertsAfterManagedOllamaBlock(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"demo": map[string]any{
				"command": "bash",
				"args":    []string{"-lc", "exec ./scripts/run-demo.sh"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", `approval_policy = "never"

# BEGIN GENERATED OLLAMA PROFILES: provider-settings-sync
# Generated by codexkit/scripts/provider-settings-sync.sh from dotfiles shared local-model defaults.
[model_providers.ollama_local]
name = "Local Ollama"

# END GENERATED OLLAMA PROFILES: provider-settings-sync
`)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	content := string(data)
	ollamaEnd := strings.Index(content, ollamaEndMarker)
	mcpStart := strings.Index(content, startMarker)
	if ollamaEnd < 0 || mcpStart < 0 {
		t.Fatalf("expected both managed blocks, got:\n%s", content)
	}
	if mcpStart <= ollamaEnd {
		t.Fatalf("expected MCP block after Ollama block, got:\n%s", content)
	}
}

func TestSync_RelocatesMCPBlockOutOfManagedOllamaBlock(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"demo": map[string]any{
				"command": "bash",
				"args":    []string{"-lc", "exec ./scripts/run-demo.sh"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", `approval_policy = "never"

# BEGIN GENERATED OLLAMA PROFILES: provider-settings-sync
# Generated by codexkit/scripts/provider-settings-sync.sh from dotfiles shared local-model defaults.

# BEGIN GENERATED MCP SERVERS: codex-mcp-sync
# Generated by codexkit/scripts/codex-mcp-sync.sh from .mcp.json
[mcp_servers.demo]
command = "stale"
# END GENERATED MCP SERVERS: codex-mcp-sync
[model_providers.ollama_local]
name = "Local Ollama"

# END GENERATED OLLAMA PROFILES: provider-settings-sync
`)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	content := string(data)
	ollamaEnd := strings.Index(content, ollamaEndMarker)
	mcpStart := strings.Index(content, startMarker)
	if ollamaEnd < 0 || mcpStart < 0 {
		t.Fatalf("expected both managed blocks, got:\n%s", content)
	}
	if mcpStart <= ollamaEnd {
		t.Fatalf("expected relocated MCP block after Ollama block, got:\n%s", content)
	}
}

func TestSync_RejectsUnmanagedBlocks(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/config.toml", `[mcp_servers.rogue]
command = "rogue"
`)

	report := Sync(dir, true)
	if len(report.Errors) == 0 {
		t.Fatal("expected unmanaged block error")
	}
	if !strings.Contains(report.Errors[0], "unmanaged [mcp_servers.*] blocks") {
		t.Fatalf("unexpected error: %v", report.Errors)
	}
}

func TestSync_PolicyFileRendersOverrides(t *testing.T) {
	dir := t.TempDir()
	mcpJSON, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"runner": map[string]any{
				"command": "bin/runner-mcp",
				"args":    []string{"serve"},
				"env": map[string]string{
					"RUNNER_ENV": "source",
				},
			},
		},
	})
	policyJSON, _ := json.Marshal(map[string]any{
		"version": 1,
		"profiles": []map[string]any{{
			"name":                "runner-curated",
			"from":                "runner",
			"comment":             "Curated runner profile",
			"enabled":             true,
			"required":            true,
			"startup_timeout_sec": 15,
			"tool_timeout_sec":    45,
			"enabled_tools":       []string{"launch", "status"},
			"disabled_tools":      []string{"destroy"},
			"tool_overrides": map[string]any{
				"launch": map[string]any{
					"approval_mode": "manual",
				},
			},
			"override": map[string]any{
				"cwd": ".",
				"env": map[string]string{
					"RUNNER_ENV": "curated",
				},
			},
		}},
	})
	writeFile(t, dir, ".mcp.json", string(mcpJSON))
	writeFile(t, dir, ".codex/mcp-profile-policy.json", string(policyJSON))
	writeFile(t, dir, ".codex/config.toml", "")

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	content := string(data)
	for _, want := range []string{
		"[mcp_servers.runner-curated]",
		`enabled = true`,
		`required = true`,
		`startup_timeout_sec = 15`,
		`tool_timeout_sec = 45`,
		`enabled_tools = [`,
		`disabled_tools = [`,
		`RUNNER_ENV = "curated"`,
		`[mcp_servers.runner-curated.tools.launch]`,
		`approval_mode = "manual"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected config to contain %q\n%s", want, content)
		}
	}
}
