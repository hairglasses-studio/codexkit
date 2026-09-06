package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeMetadataProjection(t *testing.T) {
	dir := setupSyncRepo(t)
	writeFile(t, dir, ".agents/skills/myskill/SKILL.md", `---
name: myskill
description: Native restrictions survive portable metadata.
# A comment: is not a provider key.
metadata:
  hg.claude.disallowed-tools: '["Bash(rm *)", "AskUserQuestion"]'
  hg.claude.user-invocable: 'false'
---
Use the bounded MCP tool.
`)
	report := SyncWithOptions(dir, false, Options{ValidatorMode: ValidatorOff})
	if len(report.Errors) != 0 {
		t.Fatal(report.Errors)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude/skills/myskill/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"disallowed-tools:", "Bash(rm *)", "AskUserQuestion", "user-invocable: false"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q in %s", want, data)
		}
	}
	report = SyncWithOptions(dir, true, Options{ValidatorMode: ValidatorOff})
	if report.PendingChanges || len(report.Errors) != 0 {
		t.Fatalf("non-idempotent projection: %+v", report)
	}
}

func TestNativeMetadataRejectsInvalidRestrictions(t *testing.T) {
	for _, entry := range []string{
		"hg.claude.disallowed-tools: 'not-json'",
		"hg.claude.disallowed-tools: '[1]'",
		"hg.claude.disallowed-tools: '[\"\"]'",
		"hg.claude.user-invocable: false",
		"hg.claude.user-invocable: 'maybe'",
		"hg.claude.unknown: 'true'",
	} {
		t.Run(entry, func(t *testing.T) {
			data := []byte("---\nname: test\ndescription: test\nmetadata:\n  " + entry + "\n---\nBody\n")
			if err := ValidateSourceFrontmatter("SKILL.md", data); err == nil {
				t.Fatal("invalid restriction accepted")
			}
		})
	}
}

func TestNativeMetadataRejectsMalformedContainersWithoutLosingRestrictions(t *testing.T) {
	for _, metadata := range []string{
		"metadata: null",
		"metadata: text",
		"metadata: [text]",
		"metadata:\n  1: x\n  hg.claude.disallowed-tools: '[\"Bash\"]'",
		"metadata:\n  true: x\n  hg.claude.disallowed-tools: '[\"Bash\"]'",
	} {
		t.Run(metadata, func(t *testing.T) {
			dir := setupSyncRepo(t)
			source := "---\nname: myskill\ndescription: Restrictions must never be silently dropped.\n" + metadata + "\n---\nBody\n"
			writeFile(t, dir, ".agents/skills/myskill/SKILL.md", source)
			if err := ValidateSourceFrontmatter("SKILL.md", []byte(source)); err == nil {
				t.Fatal("malformed metadata accepted")
			}
			target := filepath.Join(dir, ".claude/skills/myskill/SKILL.md")
			writeFile(t, dir, ".claude/skills/myskill/SKILL.md", "existing restriction-bearing projection")
			report := SyncWithOptions(dir, false, Options{ValidatorMode: ValidatorOff})
			if len(report.Errors) == 0 {
				t.Fatal("malformed metadata projected without error")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "existing restriction-bearing projection" {
				t.Fatalf("invalid source replaced existing projection: %q %v", data, err)
			}
		})
	}
}
