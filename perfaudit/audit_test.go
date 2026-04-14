package perfaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudit_DefaultsToActiveScopeAndDetectsBottlenecks(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceManifest(t, root, map[string]any{
		"version": 1,
		"repos": []map[string]any{
			{
				"name":            "active-repo",
				"category":        "app",
				"scope":           "active_first_party",
				"language":        "go",
				"baseline_target": true,
				"go_work_member":  true,
				"lifecycle":       "active",
			},
			{
				"name":            "archived-repo",
				"category":        "archive",
				"scope":           "compatibility_only",
				"language":        "go",
				"baseline_target": false,
				"go_work_member":  false,
				"lifecycle":       "archived",
			},
		},
	})
	writeFile(t, root, ".mcp.json", `{"mcpServers":{"root":{"command":"bash"}}}`)

	repoPath := filepath.Join(root, "active-repo")
	writeFile(t, repoPath, ".codex/config.toml", strings.Join([]string{
		`approval_policy = "on-request"`,
		`sandbox_mode = "workspace-write"`,
		``,
		`[mcp_servers.demo]`,
		`command = "bash"`,
		`args = ["-lc", "exec ./scripts/run-demo.sh"]`,
		``,
		`[profiles.readonly_quiet]`,
		`approval_policy = "never"`,
		`[profiles.review]`,
		`approval_policy = "on-request"`,
		`[profiles.workspace_auto]`,
		`approval_policy = "on-failure"`,
		`[profiles.ci_json]`,
		`approval_policy = "never"`,
		`# filler to push the config above the bloat threshold`,
		strings.Repeat("# filler line\n", 130),
	}, "\n"))
	writeFile(t, repoPath, ".mcp.json", `{"mcpServers":{"demo":{"command":"bash"}}}`)
	writeFile(t, repoPath, "scripts/run-demo.sh", "#!/usr/bin/env bash\nset -euo pipefail\nexec go run ./cmd/demo-mcp\n")
	writeFile(t, repoPath, ".agents/skills/huge_skill/SKILL.md", strings.Repeat("A", 17000))
	writeFile(t, repoPath, "prompts/huge-prompt.md", strings.Repeat("B", 17000))

	archivedPath := filepath.Join(root, "archived-repo")
	writeFile(t, archivedPath, ".codex/config.toml", `[profiles.readonly_quiet]`+"\n")

	report := Audit(root, Options{})
	if report.Summary.Scope != "active" {
		t.Fatalf("expected active scope summary, got %q", report.Summary.Scope)
	}
	if got := len(report.Repos); got != 1 {
		t.Fatalf("expected 1 active repo in report, got %d", got)
	}

	repo := report.Repos[0]
	if repo.RepoName != "active-repo" {
		t.Fatalf("expected active-repo, got %q", repo.RepoName)
	}
	if len(repo.GoRunCodexServers) != 0 {
		t.Fatalf("expected no inline go-run codex server after script extraction, got %#v", repo.GoRunCodexServers)
	}
	if len(repo.GoRunLauncherScripts) != 1 {
		t.Fatalf("expected go-run launcher detection, got %#v", repo.GoRunLauncherScripts)
	}
	if len(repo.MissingEnabledTools) != 1 {
		t.Fatalf("expected missing enabled_tools detection, got %#v", repo.MissingEnabledTools)
	}
	if len(repo.OversizedSkills) != 1 {
		t.Fatalf("expected oversized skill detection, got %#v", repo.OversizedSkills)
	}
	if len(repo.OversizedRuntimePrompts) != 1 {
		t.Fatalf("expected oversized prompt detection, got %#v", repo.OversizedRuntimePrompts)
	}
	if report.Root.RootMCPServerCount != 1 {
		t.Fatalf("expected root MCP count 1, got %d", report.Root.RootMCPServerCount)
	}
	if report.Summary.ReposWithGoRunLaunchers != 1 {
		t.Fatalf("expected summary to count go-run repos, got %d", report.Summary.ReposWithGoRunLaunchers)
	}
	if report.Summary.ReposMissingEnabledTools != 1 {
		t.Fatalf("expected summary to count enabled-tool gaps, got %d", report.Summary.ReposMissingEnabledTools)
	}
	if report.Summary.ReposWithoutPerfHarness != 1 {
		t.Fatalf("expected summary to count missing perf harness, got %d", report.Summary.ReposWithoutPerfHarness)
	}
}

func TestAudit_AllScopesIncludesNonActiveReposAndDocsArchivePrompts(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceManifest(t, root, map[string]any{
		"version": 1,
		"repos": []map[string]any{
			{
				"name":            "docs",
				"category":        "hub",
				"scope":           "active_first_party",
				"language":        "go",
				"baseline_target": true,
				"go_work_member":  true,
				"lifecycle":       "active",
			},
			{
				"name":            "compat-repo",
				"category":        "archive",
				"scope":           "compatibility_only",
				"language":        "shell",
				"baseline_target": false,
				"go_work_member":  false,
				"lifecycle":       "archived",
			},
		},
	})
	writeFile(t, filepath.Join(root, "docs"), ".codex/config.toml", `[profiles.readonly_quiet]`+"\n")
	writeFile(t, filepath.Join(root, "docs"), "prompts/library.md", strings.Repeat("P", 4000))
	writeFile(t, filepath.Join(root, "compat-repo"), ".codex/config.toml", `[profiles.readonly_quiet]`+"\n")

	report := Audit(root, Options{AllScopes: true})
	if len(report.Repos) != 2 {
		t.Fatalf("expected all scopes to include both repos, got %d", len(report.Repos))
	}
	if report.Summary.Scope != "all" {
		t.Fatalf("expected all scope label, got %q", report.Summary.Scope)
	}
	if report.Summary.DocsArchivePromptCount != 1 {
		t.Fatalf("expected docs archive prompt count 1, got %d", report.Summary.DocsArchivePromptCount)
	}
}

func TestMarkdownIncludesPriorityTable(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceManifest(t, root, map[string]any{
		"version": 1,
		"repos": []map[string]any{
			{
				"name":            "active-repo",
				"category":        "app",
				"scope":           "active_first_party",
				"language":        "go",
				"baseline_target": true,
				"go_work_member":  true,
				"lifecycle":       "active",
			},
		},
	})
	writeFile(t, filepath.Join(root, "active-repo"), ".codex/config.toml", `[profiles.readonly_quiet]`+"\n")

	markdown := Audit(root, Options{}).Markdown()
	if !strings.Contains(markdown, "Codex Performance Audit") {
		t.Fatal("expected markdown title")
	}
	if !strings.Contains(markdown, "`active-repo`") {
		t.Fatal("expected repo name in markdown output")
	}
}

func writeWorkspaceManifest(t *testing.T, root string, manifest map[string]any) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "workspace/manifest.json", string(data))
}

func writeFile(t *testing.T, base, name, content string) {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
