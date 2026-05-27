package mcpsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGlobalProjectionProviderEntries(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)

	projection, err := BuildGlobalProjection(GlobalProjectionOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-10T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Runtime.ServerCount != 1 {
		t.Fatalf("Runtime.ServerCount = %d, want 1", projection.Runtime.ServerCount)
	}
	if projection.Provider.TotalEntries != 7 {
		t.Fatalf("TotalEntries = %d, want 7", projection.Provider.TotalEntries)
	}
	if projection.Provider.CodexEntries != 1 {
		t.Fatalf("CodexEntries = %d, want 1", projection.Provider.CodexEntries)
	}
	if projection.Provider.ClaudeEntries != 3 {
		t.Fatalf("ClaudeEntries = %d, want 3", projection.Provider.ClaudeEntries)
	}
	if projection.Provider.GeminiEntries != 3 {
		t.Fatalf("GeminiEntries = %d, want 3", projection.Provider.GeminiEntries)
	}

	codexEntry := providerEntryByName(projection, "codex", "studio_desktop")
	if codexEntry == nil {
		t.Fatal("expected codex studio_desktop entry")
	}
	if got := strings.Join(codexEntry.EnabledTools, ","); got != "app_click" {
		t.Fatalf("codex enabled tools = %q, want app_click", got)
	}
	if !strings.Contains(codexEntry.Server.CWD, filepath.Join("studio", "app")) {
		t.Fatalf("codex cwd = %q, want app repo path", codexEntry.Server.CWD)
	}
	if providerEntryByName(projection, "claude", "studio_app_review") == nil {
		t.Fatal("expected default Claude review profile projection")
	}
	if providerEntryByName(projection, "gemini", "studio-app-review") == nil {
		t.Fatal("expected default Gemini review profile projection")
	}
	if providerEntryByName(projection, "claude", "studio_app_app") == nil {
		t.Fatal("expected explicit raw-source Claude projection")
	}
}

func TestCheckGlobalProjectionArtifacts(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)
	jsonPath := filepath.Join(root, "docs/inventory/workspace-global-mcp-projection-2026-05-10.json")
	markdownPath := filepath.Join(root, "docs/inventory/workspace-global-mcp-projection-2026-05-10.md")

	projection, err := BuildGlobalProjection(GlobalProjectionOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-10T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteGlobalProjection(projection, jsonPath, markdownPath); err != nil {
		t.Fatal(err)
	}

	report, err := CheckGlobalProjection(GlobalProjectionCheckOptions{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("global projection check failed: %+v", report.Findings)
	}
}

func TestRenderGlobalProjectionExplainsCodexOptIn(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)
	writeRuntimeTestFile(t, root, "app/.codex/mcp-profile-policy.json", `{
  "version": 1,
  "profiles": [
    {
      "name": "app_review",
      "from": "app",
      "mode": "review",
      "enabled_tools": ["app_search"]
    }
  ]
}`)

	projection, err := BuildGlobalProjection(GlobalProjectionOptions{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-05-10T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Provider.CodexEntries != 0 {
		t.Fatalf("CodexEntries = %d, want 0", projection.Provider.CodexEntries)
	}
	markdown := RenderGlobalProjectionMarkdown(projection)
	for _, want := range []string{
		"Codex workspace-global provider entries are opt-in.",
		"explicitly set `global_codex: true`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestSyncGlobalProviderOverlaysWritesProviderTargets(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude.json")
	codexPath := filepath.Join(home, ".codex", "config.toml")
	geminiPath := filepath.Join(home, ".gemini", "settings.json")
	writeRuntimeTestFile(t, home, ".claude.json", `{"projects":{"keep":{"mcpServers":{"manual":{"command":"manual"}}}}}`)
	writeRuntimeTestFile(t, home, ".codex/config.toml", "model = \"gpt-5.4\"\n")
	writeRuntimeTestFile(t, home, ".gemini/settings.json", `{"mcpServers":{"manual":{"command":"manual"}}}`)

	report, err := SyncGlobalProviderOverlays(GlobalOverlaySyncOptions{
		WorkspaceRoot:    root,
		GeneratedAt:      "2026-05-10T00:00:00Z",
		Mode:             GlobalOverlaySyncWrite,
		ClaudeJSONPath:   claudePath,
		ClaudeProjectKey: root,
		CodexConfigPath:  codexPath,
		GeminiConfigPath: geminiPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Pending {
		t.Fatal("first sync should report pending work that was updated")
	}
	if report.CodexToolCount != 1 || report.ClaudeToolCount != 3 || report.GeminiToolCount != 3 {
		t.Fatalf("provider counts = codex %d claude %d gemini %d, want 1/3/3", report.CodexToolCount, report.ClaudeToolCount, report.GeminiToolCount)
	}

	codexData, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	codexContent := string(codexData)
	if !strings.Contains(codexContent, WorkspaceGlobalStartMarker) || !strings.Contains(codexContent, "[mcp_servers.studio_desktop]") {
		t.Fatalf("codex overlay missing managed block:\n%s", codexContent)
	}
	if !strings.Contains(codexContent, "enabled_tools = [") || !strings.Contains(codexContent, `"app_click"`) {
		t.Fatalf("codex overlay missing enabled tools:\n%s", codexContent)
	}

	claudeData, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeData), `"studio_desktop"`) || !strings.Contains(string(claudeData), `"keep"`) {
		t.Fatalf("claude overlay did not merge managed project with existing JSON:\n%s", string(claudeData))
	}

	geminiData, err := os.ReadFile(geminiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(geminiData), `"studio-desktop"`) || !strings.Contains(string(geminiData), `"manual"`) {
		t.Fatalf("gemini overlay did not merge managed servers with existing JSON:\n%s", string(geminiData))
	}

	check, err := SyncGlobalProviderOverlays(GlobalOverlaySyncOptions{
		WorkspaceRoot:    root,
		GeneratedAt:      "2026-05-10T00:00:00Z",
		Mode:             GlobalOverlaySyncCheck,
		ClaudeJSONPath:   claudePath,
		ClaudeProjectKey: root,
		CodexConfigPath:  codexPath,
		GeminiConfigPath: geminiPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if check.Pending {
		t.Fatalf("check after sync should be clean: %+v", check.Actions)
	}
}

func setupGlobalProjectionWorkspace(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "studio")
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
	writeRuntimeExecutable(t, root, "app/run-mcp.sh")
	writeRuntimeTestFile(t, root, "app/.mcp.json", `{
  "mcpServers": {
    "app": {
      "command": "./run-mcp.sh",
      "env": {"APP_ENV": "test"}
    }
  }
}`)
	writeRuntimeTestFile(t, root, "app/.codex/mcp-profile-policy.json", `{
  "version": 1,
  "profiles": [
    {
      "name": "app_review",
      "from": "app",
      "mode": "review",
      "enabled_tools": ["app_search"],
      "override": {"env": {"PROFILE": "review"}}
    },
    {
      "name": "app_desktop",
      "from": "app",
      "mode": "desktop",
      "global_name": "desktop",
      "global_codex": true,
      "global_claude": true,
      "global_gemini": true,
      "enabled_tools": ["app_click"]
    },
    {
      "name": "app_raw",
      "from": "app",
      "mode": "ops",
      "global_raw_source": true
    }
  ]
}`)
	return root
}

func providerEntryByName(projection GlobalProjection, providerName, entryName string) *ProviderProjectionEntry {
	for _, provider := range projection.Providers {
		if provider.Provider != providerName {
			continue
		}
		for i := range provider.Entries {
			if provider.Entries[i].Name == entryName {
				return &provider.Entries[i]
			}
		}
	}
	return nil
}
