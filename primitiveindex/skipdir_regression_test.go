package primitiveindex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var skipDirBenchBoolSink bool

func TestShouldSkipDirRegressionMatrix(t *testing.T) {
	skipCases := []string{
		"vendor",
		"vendor/pkg",
		"app/vendor/excluded-vendor-dir",
		"third_party",
		"app/third_party/sdk",
		"node_modules",
		"app/node_modules/dep",
		".venv",
		"app/.venv/lib",
		".tools",
		"app/.tools/bin",
		".ouro",
		".ouro/skills",
		"app/.ouro/cache",
		".claude/worktrees",
		".claude/worktrees/wt-1",
		"app/.claude/worktrees",
		"app/.claude/worktrees/wt-2",
		".wt-agy-lane",
		"sub/.wt-shard-1",
		"repo-wt-01",
		"sub/repo-wt-01/nested",
		".codex-worktrees",
		".codex-worktrees/w1",
		"app/.codex-worktrees/w1",
		".worktrees",
		".worktrees/w1",
		"app/.worktrees/w1",
		".ralph/worktrees",
		".ralph/worktrees/w1",
		"app/.ralph/worktrees/w1",
		".hg-anchor-edit-1",
		"sub/.hg-anchor-edit-1",
		".git",
		"app/.git",
		".graveyard",
		".archive",
		".scratch",
		"s2f_tmp",
		"app/s2f_stage",
		"vault",
		"vault/archive",
		".config",
		"docs/_site",
		"docs/repo-claude",
		"docs/research",
		"docs/agent-parity",
		"agent-parity",
		".ralph/probe",
		"data/appdata",
		"app/data/appdata",
		"vendor/",
		"app/.ouro/cache/",
	}

	for _, rel := range skipCases {
		if !shouldSkipDir(rel) {
			t.Errorf("shouldSkipDir(%q) = false, want true", rel)
		}
		trimmed := filepath.ToSlash(rel)
		for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		fileRel := trimmed + "/.mcp.json"
		if !shouldSkipFile(fileRel) {
			t.Errorf("shouldSkipFile(%q) = false, want true", fileRel)
		}
		nestedSkillRel := trimmed + "/skills/s/SKILL.md"
		if !shouldSkipFile(nestedSkillRel) {
			t.Errorf("shouldSkipFile(%q) = false, want true", nestedSkillRel)
		}
	}

	keepCases := []string{
		".",
		"app",
		"app/",
		"app/.claude",
		"app/.claude/hooks",
		"app/.codex/agents",
		"docs/fleet",
		"codexkit/primitiveindex",
		"app/vendorish",
		"app/worktrees-guide",
	}

	for _, rel := range keepCases {
		if shouldSkipDir(rel) {
			t.Errorf("shouldSkipDir(%q) = true, want false", rel)
		}
		fileRel := ".mcp.json"
		if rel != "." {
			trimmed := filepath.ToSlash(rel)
			for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			fileRel = trimmed + "/active.toml"
		}
		if shouldSkipFile(fileRel) {
			t.Errorf("shouldSkipFile(%q) = true, want false", fileRel)
		}
	}
}

func TestShouldSkipDirBuildWalkRegression(t *testing.T) {
	root := t.TempDir()
	writeSkipDirManifest(t, root)

	excludedFiles := map[string]string{
		"app/vendor/excluded-vendor-dir/.mcp.json":         `{"mcpServers":{"bad":{"command":"echo"}}}`,
		"vendor/top/.mcp.json":                             `{"mcpServers":{}}`,
		"third_party/pkg/.mcp.json":                        `{"mcpServers":{}}`,
		"node_modules/pkg/.mcp.json":                       `{"mcpServers":{}}`,
		".venv/pkg/.claude/settings.json":                  `{}`,
		".tools/cli/.mcp.json":                             `{"mcpServers":{}}`,
		".ouro/skills/s/SKILL.md":                          "---\nname: excluded-ouro-skill\n---\n",
		"app/.ouro/cache/.mcp.json":                        `{"mcpServers":{}}`,
		".claude/worktrees/wt-1/.mcp.json":                 `{"mcpServers":{}}`,
		"app/.claude/worktrees/wt1/.mcp.json":              `{"mcpServers":{}}`,
		"app/.claude/worktrees/wt-2/.codex/agents/a.toml":  "name = \"excluded\"\n",
		".wt-agy-1/.codex/agents/a.toml":                   "name = \"excluded\"\n",
		"sub/.wt-shard-1/.mcp.json":                        `{"mcpServers":{}}`,
		"repo-wt-01/.codex/agents/a.toml":                  "name = \"excluded\"\n",
		".codex-worktrees/w1/.mcp.json":                    `{"mcpServers":{}}`,
		".worktrees/w1/.mcp.json":                          `{"mcpServers":{}}`,
		".ralph/worktrees/w1/.mcp.json":                    `{"mcpServers":{}}`,
		".hg-anchor-edit-1/.mcp.json":                      `{"mcpServers":{}}`,
		"relocated-lane/.git":                              "gitdir: /var/tmp/main/.git/worktrees/relocated-lane\n",
		"relocated-lane/.mcp.json":                         `{"mcpServers":{}}`,
		"nested/linked-checkout/.git":                      "gitdir: /var/tmp/bare/worktrees/linked-checkout\n",
		"nested/linked-checkout/.codex/agents/linked.toml": "name = \"linked\"\n",
		"app/.git/HEAD":                                    "ref: refs/heads/main\n",
		"app/.git/.mcp.json":                               `{"mcpServers":{}}`,
	}
	for rel, content := range excludedFiles {
		writeSkipDirFile(t, root, rel, content)
	}

	writeSkipDirFile(t, root, "app/.codex/agents/active.toml", "name = \"active\"\n")
	writeSkipDirFile(t, root, "app/.claude/hooks/check.sh", "#!/usr/bin/env bash\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "app/.claude/hooks/check.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkipDirFile(t, root, "docs/fleet/AGENTS.md", "# Fleet\n")

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	expectedActive := map[string]string{
		"app/.codex/agents/active.toml": "codex_agent",
		"app/.claude/hooks/check.sh":    "claude_hook_script",
		"docs/fleet/AGENTS.md":          "instruction_file",
	}

	if len(index.Primitives) != len(expectedActive) {
		t.Fatalf("indexed %d primitives, want %d: %+v", len(index.Primitives), len(expectedActive), index.Primitives)
	}

	for _, primitive := range index.Primitives {
		wantKind, ok := expectedActive[primitive.Path]
		if !ok {
			t.Fatalf("excluded path was indexed: %+v", primitive)
		}
		if primitive.Kind != wantKind || primitive.Status != "pass" {
			t.Fatalf("unexpected primitive state for %s: %+v", primitive.Path, primitive)
		}
	}

	for path, kind := range expectedActive {
		if !hasPrimitive(index, kind, "pass", path) {
			t.Fatalf("missing active primitive %s (%s): %+v", path, kind, index.Primitives)
		}
	}
}

func BenchmarkShouldSkipDir(b *testing.B) {
	paths := []string{
		"vendor",
		"app/vendor/excluded-vendor-dir",
		"third_party",
		"node_modules",
		".venv",
		".tools",
		".ouro",
		".ouro/skills",
		"app/.ouro/cache",
		".claude/worktrees",
		".claude/worktrees/wt-1",
		"app/.claude/worktrees/wt-2",
		".wt-agy-lane",
		"sub/.wt-shard-1",
		"repo-wt-01",
		".codex-worktrees/w1",
		".worktrees/w1",
		".ralph/worktrees/w1",
		".hg-anchor-edit-1",
		".",
		"app",
		"app/.claude",
		"app/.claude/hooks",
		"app/.codex/agents",
		"docs/fleet",
		"codexkit/primitiveindex",
	}

	b.ReportAllocs()
	b.ResetTimer()
	var local bool
	for i := 0; i < b.N; i++ {
		local = shouldSkipDir(paths[i%len(paths)])
	}
	skipDirBenchBoolSink = local
}

func BenchmarkBuildSkipsExcludedTrees(b *testing.B) {
	root, expectedTotal := setupBenchmarkWorkspace(b, 2, 2)
	opts := Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index, err := Build(opts)
		if err != nil {
			b.Fatal(err)
		}
		if index.Summary.Total != expectedTotal {
			b.Fatalf("Total = %d, want %d", index.Summary.Total, expectedTotal)
		}
	}
}

func BenchmarkBuildSkipsExcludedWorktreeAndVendorTrees(b *testing.B) {
	root, expectedTotal := setupBenchmarkWorkspace(b, 12, 12)
	opts := Options{WorkspaceRoot: root, GeneratedAt: "2026-05-09T00:00:00Z"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index, err := Build(opts)
		if err != nil {
			b.Fatal(err)
		}
		if index.Summary.Total != expectedTotal {
			b.Fatalf("Total = %d, want %d", index.Summary.Total, expectedTotal)
		}
	}
}

func setupBenchmarkWorkspace(b *testing.B, vendorDepth, worktreeCount int) (string, int) {
	b.Helper()
	root := b.TempDir()
	writeSkipDirManifest(b, root)

	writeSkipDirFile(b, root, "app/.codex/agents/active.toml", "name = \"active\"\n")
	writeSkipDirFile(b, root, "app/.claude/hooks/check.sh", "#!/usr/bin/env bash\nexit 0\n")
	writeSkipDirFile(b, root, "docs/fleet/AGENTS.md", "# Fleet\n")

	writeSkipDirFile(b, root, "app/vendor/excluded-vendor-dir/.mcp.json", `{"mcpServers":{}}`)
	writeSkipDirFile(b, root, ".ouro/skills/s/SKILL.md", "---\nname: s\n---\n")
	writeSkipDirFile(b, root, "app/.ouro/cache/.mcp.json", `{"mcpServers":{}}`)
	writeSkipDirFile(b, root, "app/.claude/worktrees/wt1/.mcp.json", `{"mcpServers":{}}`)
	writeSkipDirFile(b, root, ".wt-agy-1/.codex/agents/a.toml", "name = \"a\"\n")

	for i := 0; i < vendorDepth; i++ {
		writeSkipDirFile(b, root, fmt.Sprintf("vendor/pkg-%d/nested/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf("app/node_modules/mod-%d/sub/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf("third_party/lib-%d/sub/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".venv/env-%d/site/.claude/settings.json", i), `{}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".tools/tool-%d/.mcp.json", i), `{"mcpServers":{}}`)
	}

	for i := 0; i < worktreeCount; i++ {
		writeSkipDirFile(b, root, fmt.Sprintf(".wt-lane-%d/.codex/agents/a.toml", i), "name = \"a\"\n")
		writeSkipDirFile(b, root, fmt.Sprintf("repo-wt-%02d/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".codex-worktrees/w%d/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".worktrees/w%d/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".ralph/worktrees/w%d/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf(".hg-anchor-edit-%d/.mcp.json", i), `{"mcpServers":{}}`)
		writeSkipDirFile(b, root, fmt.Sprintf("relocated-%d/.git", i), fmt.Sprintf("gitdir: /var/tmp/main/.git/worktrees/relocated-%d\n", i))
		writeSkipDirFile(b, root, fmt.Sprintf("relocated-%d/.mcp.json", i), `{"mcpServers":{}}`)
	}

	return root, 3
}

func writeSkipDirManifest(tb testing.TB, root string) {
	tb.Helper()
	writeSkipDirFile(tb, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"},
    {"name": "docs", "category": "hub", "scope": "active_operator", "language": "Markdown", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
}

func writeSkipDirFile(tb testing.TB, root, rel, content string) {
	tb.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatal(err)
	}
}
