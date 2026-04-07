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

	// Should have 2 create actions
	creates := 0
	for _, a := range report.Actions {
		if a.Action == "create" {
			creates++
		}
	}
	if creates != 2 {
		t.Errorf("expected 2 creates, got %d", creates)
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
