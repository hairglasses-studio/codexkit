package primitiveindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIndexesHooksOverlaysPluginsAndNestedMCP(t *testing.T) {
	root := t.TempDir()
	writePrimitiveTestManifest(t, root)
	writePrimitiveTestFile(t, root, ".claude/hooks/good.sh", "#!/usr/bin/env bash\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, ".claude/hooks/good.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/hooks/good.sh", "#!/usr/bin/env bash\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "docs/fleet/.claude/hooks/good.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/settings.json", `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/good.sh", "timeout": 5}
        ]
      }
    ]
  }
}`)
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/settings.local.json", `{
  "enableAllProjectMcpServers": true,
  "permissions": {
    "allow": ["Bash(sudo pacman -Syu)", "Bash(kill 123)"]
  }
}`)
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/agents/reviewer.md", "---\nname: reviewer\nmodel: sonnet\n---\n# Reviewer\n")
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/output-styles/fleet-terse.md", "# terse\n")
	writePrimitiveTestFile(t, root, "app/.codex/agents/reviewer.toml", "name = \"reviewer\"\n")
	writePrimitiveTestFile(t, root, "app/plugins/app/.codex-plugin/plugin.json", `{"name":"app"}`)
	writePrimitiveTestFile(t, root, "app/plugins/app/.mcp.json", `{"mcpServers":{"app":{"command":"echo"}}}`)

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if index.Summary.Total == 0 {
		t.Fatal("expected primitives")
	}
	if index.Summary.BlockingPrimitiveFailures != 0 {
		t.Fatalf("unexpected blocking failures: %+v", attentionPrimitives(index.Primitives))
	}
	if index.Summary.Warnings == 0 {
		t.Fatal("expected local overlay warnings")
	}
	if !hasPrimitive(index, "claude_hook", "pass", "docs/fleet/.claude/settings.json") {
		t.Fatalf("expected passing Claude hook primitive: %+v", index.Primitives)
	}
	if !hasPrimitive(index, "plugin_manifest", "pass", "app/plugins/app/.codex-plugin/plugin.json") {
		t.Fatalf("expected plugin manifest primitive: %+v", index.Primitives)
	}
	if !hasPrimitive(index, "mcp_source", "pass", "app/plugins/app/.mcp.json") {
		t.Fatalf("expected nested MCP primitive: %+v", index.Primitives)
	}
	if !strings.Contains(RenderMarkdown(index), "Workspace Agent Primitive Index") {
		t.Fatal("expected markdown title")
	}
}

func TestBuildFailsOnMissingCanonicalHookTarget(t *testing.T) {
	root := t.TempDir()
	writePrimitiveTestManifest(t, root)
	writePrimitiveTestFile(t, root, "docs/fleet/.claude/settings.json", `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/missing.sh"}
        ]
      }
    ]
  }
}`)

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if index.Summary.BlockingPrimitiveFailures != 1 {
		t.Fatalf("BlockingPrimitiveFailures = %d, want 1: %+v", index.Summary.BlockingPrimitiveFailures, attentionPrimitives(index.Primitives))
	}
	if !hasPrimitive(index, "claude_hook", "fail", "docs/fleet/.claude/settings.json") {
		t.Fatalf("expected failed hook primitive: %+v", index.Primitives)
	}
}

func TestBuildResolvesClaudeProjectDirPerRepo(t *testing.T) {
	root := t.TempDir()
	writePrimitiveTestManifest(t, root)
	writePrimitiveTestFile(t, root, "app/.claude/hooks/good.sh", "#!/usr/bin/env bash\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "app/.claude/hooks/good.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePrimitiveTestFile(t, root, "app/.claude/settings.json", `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/good.sh"}
        ]
      }
    ]
  }
}`)

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if index.Summary.BlockingPrimitiveFailures != 0 {
		t.Fatalf("unexpected blocking failures: %+v", attentionPrimitives(index.Primitives))
	}
}

func TestBuildExcludesArchiveAndVendorPaths(t *testing.T) {
	root := t.TempDir()
	writePrimitiveTestManifest(t, root)
	writePrimitiveTestFile(t, root, "vault/old/.codex/agents/reviewer.toml", "name = \"reviewer\"\n")
	writePrimitiveTestFile(t, root, "app/node_modules/pkg/.claude/settings.json", "{}")
	writePrimitiveTestFile(t, root, "app/third_party/import/.claude/settings.json", "{}")
	writePrimitiveTestFile(t, root, "docs/research/example/.mcp.json", `{"mcpServers":{}}`)
	writePrimitiveTestFile(t, root, "app/.codex/agents/reviewer.toml", "name = \"reviewer\"\n")

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	for _, primitive := range index.Primitives {
		if strings.HasPrefix(primitive.Path, "vault/") ||
			strings.Contains(primitive.Path, "/node_modules/") ||
			strings.Contains(primitive.Path, "/third_party/") ||
			strings.HasPrefix(primitive.Path, "docs/research/") {
			t.Fatalf("excluded path appeared in index: %+v", primitive)
		}
	}
	if !hasPrimitive(index, "codex_agent", "pass", "app/.codex/agents/reviewer.toml") {
		t.Fatalf("expected active codex agent primitive: %+v", index.Primitives)
	}
}

func TestCheckPassesWithWrittenArtifacts(t *testing.T) {
	root := t.TempDir()
	writePrimitiveTestManifest(t, root)
	writePrimitiveTestFile(t, root, "app/.codex/agents/reviewer.toml", "name = \"reviewer\"\n")

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(root, "docs", "inventory", "workspace-agent-primitives-2026-05-09.json")
	markdownPath := filepath.Join(root, "docs", "inventory", "workspace-agent-primitives-2026-05-09.md")
	if err := Write(index, jsonPath, markdownPath); err != nil {
		t.Fatal(err)
	}

	report, err := Check(CheckOptions{WorkspaceRoot: root, JSONPath: jsonPath, MarkdownPath: markdownPath})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected check to pass: %+v", report.Findings)
	}
}

func hasPrimitive(index Index, kind, status, path string) bool {
	for _, primitive := range index.Primitives {
		if primitive.Kind == kind && primitive.Status == status && primitive.Path == path {
			return true
		}
	}
	return false
}

func writePrimitiveTestManifest(t *testing.T, root string) {
	t.Helper()
	writePrimitiveTestFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"},
    {"name": "docs", "category": "hub", "scope": "active_operator", "language": "Markdown", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
}

func writePrimitiveTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
