package baselineguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupCompliantRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Required files
	writeFile(t, dir, "AGENTS.md", "# Test\n\n> Canonical instructions: AGENTS.md\n")
	writeFile(t, dir, "CLAUDE.md", "# Test\n\nThis repo uses [AGENTS.md](AGENTS.md) as the canonical instruction file.\n")
	writeFile(t, dir, "GEMINI.md", "# Test\n\nThis repo uses [AGENTS.md](AGENTS.md) as the canonical instruction file.\n")
	writeFile(t, dir, ".github/copilot-instructions.md", "See AGENTS.md in the repository root.\n")
	writeFile(t, dir, ".codex/config.toml", `
approval_policy = "on-request"

[profiles.readonly_quiet]
approval_policy = "never"

[profiles.review]
approval_policy = "on-request"

[profiles.workspace_auto]
approval_policy = "on-failure"

[profiles.ci_json]
approval_policy = "never"
`)
	writeFile(t, dir, ".codex/agents/reviewer.toml", `name = "reviewer"`)
	writeFile(t, dir, ".codex/agents/repo_explorer.toml", `name = "repo_explorer"`)

	surface, _ := json.Marshal(map[string]any{
		"version": 1,
		"skills":  []map[string]any{{"name": "test_skill"}},
	})
	writeFile(t, dir, ".agents/skills/surface.yaml", string(surface))
	writeFile(t, dir, ".agents/skills/test_skill/SKILL.md", "---\nname: test_skill\n---\n# Test\n")

	return dir
}

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

func TestCheck_CompliantRepo(t *testing.T) {
	dir := setupCompliantRepo(t)
	report := Check(dir)

	if !report.Passed {
		for _, f := range report.Findings {
			if !f.Passed {
				t.Errorf("unexpected failure: %s — %s", f.Check, f.Message)
			}
		}
		t.Fatalf("expected PASS, got %d failures", report.Failed)
	}
}

func TestCheck_MissingAgentsMd(t *testing.T) {
	dir := setupCompliantRepo(t)
	os.Remove(filepath.Join(dir, "AGENTS.md"))

	report := Check(dir)
	if report.Passed {
		t.Fatal("expected FAIL for missing AGENTS.md")
	}
	found := false
	for _, f := range report.Findings {
		if f.Check == "required_file" && !f.Passed && f.Message == "missing: AGENTS.md" {
			found = true
		}
	}
	if !found {
		t.Error("expected finding for missing AGENTS.md")
	}
}

func TestCheck_MissingCanonicalPattern(t *testing.T) {
	dir := setupCompliantRepo(t)
	writeFile(t, dir, "AGENTS.md", "# Test\n\nNo canonical line here.\n")

	report := Check(dir)
	if report.Passed {
		t.Fatal("expected FAIL for missing canonical pattern")
	}
}

func TestCheck_MissingProfile(t *testing.T) {
	dir := setupCompliantRepo(t)
	writeFile(t, dir, ".codex/config.toml", `
[profiles.readonly_quiet]
[profiles.review]
`)

	report := Check(dir)
	if report.Passed {
		t.Fatal("expected FAIL for missing profiles")
	}
	failCount := 0
	for _, f := range report.Findings {
		if f.Check == "profile" && !f.Passed {
			failCount++
		}
	}
	if failCount != 2 {
		t.Fatalf("expected 2 missing profiles, got %d", failCount)
	}
}

func TestCheck_DashCaseAgent(t *testing.T) {
	dir := setupCompliantRepo(t)
	writeFile(t, dir, ".codex/agents/my-agent.toml", `name = "my-agent"`)

	report := Check(dir)
	found := false
	for _, f := range report.Findings {
		if f.Check == "agent_naming" && !f.Passed {
			found = true
		}
	}
	if !found {
		t.Error("expected agent_naming failure for dash-case filename")
	}
}

func TestCheck_InvalidSurfaceJSON(t *testing.T) {
	dir := setupCompliantRepo(t)
	writeFile(t, dir, ".agents/skills/surface.yaml", "not json at all")

	report := Check(dir)
	found := false
	for _, f := range report.Findings {
		if f.Check == "skill_surface" && !f.Passed {
			found = true
		}
	}
	if !found {
		t.Error("expected skill_surface failure for invalid JSON")
	}
}

func TestCheck_MissingSkillFile(t *testing.T) {
	dir := setupCompliantRepo(t)
	os.RemoveAll(filepath.Join(dir, ".agents/skills/test_skill"))

	report := Check(dir)
	found := false
	for _, f := range report.Findings {
		if f.Check == "skill_file" && !f.Passed {
			found = true
		}
	}
	if !found {
		t.Error("expected skill_file failure for missing SKILL.md")
	}
}

func TestCheck_NoSurface(t *testing.T) {
	dir := setupCompliantRepo(t)
	os.Remove(filepath.Join(dir, ".agents/skills/surface.yaml"))

	report := Check(dir)
	// Should still pass — surface is optional
	for _, f := range report.Findings {
		if f.Check == "skill_surface" && !f.Passed {
			t.Error("missing surface.yaml should not cause failure")
		}
	}
}
