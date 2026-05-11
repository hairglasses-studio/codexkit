package surfaceindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

func TestBuildIndexesBaselineRepoSurfaces(t *testing.T) {
	root := t.TempDir()
	writeSurfaceIndexManifest(t, root)
	writeSurfaceIndexConsolidation(t, root)
	writeSurfaceIndexSkill(t, root)
	writeSurfaceIndexMCP(t, root)
	if report := skillsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("skill sync errors: %v", report.Errors)
	}
	if report := mcpsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("mcp sync errors: %v", report.Errors)
	}

	index, err := Build(Options{
		WorkspaceRoot:      root,
		GeneratedAt:        "2026-05-09T00:00:00Z",
		SkillValidatorMode: skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if index.Summary.BaselineRepos != 1 {
		t.Fatalf("BaselineRepos = %d, want 1", index.Summary.BaselineRepos)
	}
	if index.Summary.SkillSurfaceRepos != 1 {
		t.Fatalf("SkillSurfaceRepos = %d, want 1", index.Summary.SkillSurfaceRepos)
	}
	if index.Summary.MCPSourceRepos != 1 {
		t.Fatalf("MCPSourceRepos = %d, want 1", index.Summary.MCPSourceRepos)
	}
	repo := index.Repos[0]
	if repo.Consolidation == nil || repo.Consolidation.State != "merge_target" {
		t.Fatalf("unexpected consolidation summary: %+v", repo.Consolidation)
	}
	if len(repo.Skills.CanonicalSkills) != 1 || repo.Skills.CanonicalSkills[0].Name != "demo_skill" {
		t.Fatalf("unexpected skills: %+v", repo.Skills.CanonicalSkills)
	}
	if len(repo.Skills.GeneratedMirrors) == 0 {
		t.Fatal("expected generated skill mirror paths")
	}
	if len(repo.MCP.SourceServers) != 1 || repo.MCP.SourceServers[0] != "demo" {
		t.Fatalf("unexpected MCP servers: %+v", repo.MCP.SourceServers)
	}
	if len(repo.Runtime.ProjectedAliases) != 1 {
		t.Fatalf("expected one runtime projection: %+v", repo.Runtime)
	}
}

func TestCheckPassesWithWrittenArtifacts(t *testing.T) {
	root := t.TempDir()
	writeSurfaceIndexManifest(t, root)
	writeSurfaceIndexSkill(t, root)
	if report := skillsync.Sync(filepath.Join(root, "app"), false); len(report.Errors) > 0 {
		t.Fatalf("skill sync errors: %v", report.Errors)
	}

	index, err := Build(Options{
		WorkspaceRoot:      root,
		GeneratedAt:        "2026-05-09T00:00:00Z",
		SkillValidatorMode: skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(root, "docs", "inventory", "repo-surface-index-2026-05-09.json")
	markdownPath := filepath.Join(root, "docs", "inventory", "repo-surface-index-2026-05-09.md")
	if err := Write(index, jsonPath, markdownPath); err != nil {
		t.Fatal(err)
	}
	report, err := Check(CheckOptions{
		WorkspaceRoot:      root,
		JSONPath:           jsonPath,
		MarkdownPath:       markdownPath,
		SkillValidatorMode: skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected artifact check to pass: %+v", report.Findings)
	}
}

func TestBuildClassifiesRuntimeProjectionOnlyRepo(t *testing.T) {
	root := t.TempDir()
	writeSurfaceIndexFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "stack", "category": "application", "scope": "active_first_party", "language": "Shell", "baseline_target": true, "go_work_member": false, "lifecycle": "active"},
    {"name": "notes", "category": "local", "scope": "active_first_party", "language": "Config", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeSurfaceIndexFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {"repo": "stack", "state": "runtime_projection_only_alias", "merge_target": "runner"}
  ]
}`)
	writeSurfaceIndexFile(t, root, "stack/.mcp.json", `{"mcpServers":{"runner":{"command":"bash","args":["-lc","echo stack"]}}}`)
	writeSurfaceIndexFile(t, root, "notes/.codex/config.toml", "approval_policy = \"on-request\"\n")

	index, err := Build(Options{
		WorkspaceRoot:      root,
		GeneratedAt:        "2026-05-09T00:00:00Z",
		SkillValidatorMode: skillsync.ValidatorOff,
	})
	if err != nil {
		t.Fatal(err)
	}

	byRepo := map[string]RepoEntry{}
	for _, repo := range index.Repos {
		byRepo[repo.Name] = repo
	}
	stack := byRepo["stack"]
	if stack.SourceContract.Status != "runtime_projection_only" {
		t.Fatalf("stack source contract status = %q", stack.SourceContract.Status)
	}
	if stack.Consolidation == nil || stack.Consolidation.MergeTarget != "runner" {
		t.Fatalf("unexpected stack consolidation: %+v", stack.Consolidation)
	}
	if len(stack.Runtime.ProjectedAliases) != 1 {
		t.Fatalf("expected runtime projection for stack: %+v", stack.Runtime)
	}

	notes := byRepo["notes"]
	if len(notes.MCP.Errors) > 0 {
		t.Fatalf("did not expect MCP parse errors for config-only repo: %+v", notes.MCP.Errors)
	}
	markdown := RenderMarkdown(index)
	if !strings.Contains(markdown, "runtime_projection_only") {
		t.Fatalf("expected markdown to include runtime projection only status:\n%s", markdown)
	}
}

func writeSurfaceIndexManifest(t *testing.T, root string) {
	t.Helper()
	manifest := map[string]any{
		"version": 1,
		"repos": []map[string]any{{
			"name":            "app",
			"category":        "application",
			"scope":           "active_first_party",
			"language":        "Go",
			"baseline_target": true,
			"go_work_member":  false,
			"lifecycle":       "active",
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeSurfaceIndexFile(t, root, "workspace/manifest.json", string(data))
}

func writeSurfaceIndexConsolidation(t *testing.T, root string) {
	t.Helper()
	writeSurfaceIndexFile(t, root, "docs/inventory/repo-consolidation-matrix.json", `{
  "decisions": [
    {"repo": "app", "state": "merge_target", "notes": ["canonical app"]}
  ]
}`)
}

func writeSurfaceIndexSkill(t *testing.T, root string) {
	t.Helper()
	writeSurfaceIndexFile(t, root, "app/.agents/skills/surface.yaml", `{"version":1,"skills":[{"name":"demo_skill"}]}`)
	writeSurfaceIndexFile(t, root, "app/.agents/skills/demo_skill/SKILL.md", "---\nname: demo_skill\ndescription: demo skill\n---\n# Demo\n")
}

func writeSurfaceIndexMCP(t *testing.T, root string) {
	t.Helper()
	writeSurfaceIndexFile(t, root, "app/.mcp.json", `{"mcpServers":{"demo":{"command":"bash","args":["-lc","echo demo"]}}}`)
	writeSurfaceIndexFile(t, root, "app/.codex/config.toml", "")
}

func writeSurfaceIndexFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
