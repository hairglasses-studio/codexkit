package skillsync

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

func setupSyncRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	surface, _ := json.Marshal(map[string]any{
		"version": 1,
		"skills":  []map[string]any{{"name": "myskill"}},
	})
	writeFile(t, dir, ".agents/skills/surface.yaml", string(surface))
	writeFile(t, dir, ".agents/skills/myskill/SKILL.md", "---\nname: myskill\ndescription: test skill\n---\n# My Skill\n")

	return dir
}

func TestSync_CreatesClaudeMirror(t *testing.T) {
	dir := setupSyncRepo(t)

	report := Sync(dir, false)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	// Check .claude/skills mirror was created
	path := filepath.Join(dir, ".claude/skills/myskill/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("mirror not created: %v", err)
	}

	expected, _ := os.ReadFile(filepath.Join(dir, ".agents/skills/myskill/SKILL.md"))
	if string(data) != string(expected) {
		t.Error("mirror content doesn't match source")
	}

	// Check plugins mirror was created
	pluginPath := filepath.Join(dir, "plugins/myskill/SKILL.md")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("plugins mirror not created: %v", err)
	}
}

func TestSync_DryRunNoWrite(t *testing.T) {
	dir := setupSyncRepo(t)

	report := Sync(dir, true)
	if len(report.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}

	path := filepath.Join(dir, ".claude/skills/myskill/SKILL.md")
	if _, err := os.Stat(path); err == nil {
		t.Error("dry-run should not create files")
	}

	// Should have create actions
	hasCreate := false
	for _, a := range report.Actions {
		if a.Action == "create" {
			hasCreate = true
		}
	}
	if !hasCreate {
		t.Error("expected create actions in dry-run report")
	}
}

func TestSync_DetectsUnchanged(t *testing.T) {
	dir := setupSyncRepo(t)

	// First sync
	Sync(dir, false)

	// Second sync — should detect unchanged
	report := Sync(dir, false)
	for _, a := range report.Actions {
		if a.Action != "unchanged" {
			t.Errorf("expected unchanged, got %s for %s", a.Action, a.DstPath)
		}
	}
}

func TestDiff_IsDryRun(t *testing.T) {
	dir := setupSyncRepo(t)
	report := Diff(dir)
	if !report.DryRun {
		t.Error("Diff should set DryRun=true")
	}
}

func TestList_ReturnsSkillNames(t *testing.T) {
	dir := setupSyncRepo(t)
	names, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "myskill" {
		t.Errorf("expected [myskill], got %v", names)
	}
}

func TestFilterPortableFrontmatter(t *testing.T) {
	input := "---\nname: test\ncustom_key: bad\ndescription: good\n---\n# Content\n"
	result := FilterPortableFrontmatter(input)
	if strings.Contains(result, "custom_key") {
		t.Error("non-portable key should be filtered")
	}
	if !strings.Contains(result, "name: test") {
		t.Error("portable key 'name' should be preserved")
	}
	if !strings.Contains(result, "description: good") {
		t.Error("portable key 'description' should be preserved")
	}
}

func TestFilterPortableFrontmatter_PreservesReload(t *testing.T) {
	input := "---\nname: test\nreload: true\n---\n# Content\n"
	result := FilterPortableFrontmatter(input)
	if !strings.Contains(result, "reload: true") {
		t.Error("reload key should be preserved (hot-reloading support)")
	}
}

func TestParseSurface_YAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".agents/skills/surface.yaml", "version: 1\nskills:\n  - name: alpha\n  - name: beta\n")

	surface, err := ParseSurface(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(surface.Skills))
	}
}
