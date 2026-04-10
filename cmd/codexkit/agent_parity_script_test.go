package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataPathLabelPrefersRepoRelativePathForCodexkitSelfAudit(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join(t.TempDir(), "codexkit")
	toolRoot := filepath.Join(t.TempDir(), "codexkit-tool")

	for _, path := range []string{
		filepath.Join(repoRoot, "templates"),
		filepath.Join(toolRoot, "scripts", "lib"),
		filepath.Join(toolRoot, "templates"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repoTemplate := filepath.Join(repoRoot, "templates", "gemini-settings.standard.json")
	toolTemplate := filepath.Join(toolRoot, "templates", "gemini-settings.standard.json")
	if err := os.WriteFile(repoTemplate, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolTemplate, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(toolRoot, "scripts", "lib", "hg-agent-parity.sh")
	source := strings.ReplaceAll(`#!/usr/bin/env bash
set -euo pipefail
hg_require() { return 0; }
source "__REAL_SCRIPT__"
hg_parity_metadata_path_label "__REPO_ROOT__" "__TOOL_TEMPLATE__"
`, "__REAL_SCRIPT__", filepath.Join("/tmp/codexkit-refresh-yJiz8x", "scripts", "lib", "hg-agent-parity.sh"))
	source = strings.ReplaceAll(source, "__REPO_ROOT__", repoRoot)
	source = strings.ReplaceAll(source, "__TOOL_TEMPLATE__", toolTemplate)
	if err := os.WriteFile(scriptPath, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(), "HG_AGENT_PARITY_SURFACEKIT_ROOT="+toolRoot)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("metadata label command failed: %v", err)
	}

	if got := strings.TrimSpace(string(out)); got != "templates/gemini-settings.standard.json" {
		t.Fatalf("metadata path label = %q, want %q", got, "templates/gemini-settings.standard.json")
	}
}

func TestMetadataPathLabelKeepsCodexkitPrefixForExternalRepos(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join(t.TempDir(), "external-repo")
	toolRoot := filepath.Join(t.TempDir(), "codexkit-tool")

	for _, path := range []string{
		filepath.Join(repoRoot, "templates"),
		filepath.Join(toolRoot, "scripts", "lib"),
		filepath.Join(toolRoot, "templates"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	toolTemplate := filepath.Join(toolRoot, "templates", "gemini-settings.standard.json")
	if err := os.WriteFile(toolTemplate, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "templates", "gemini-settings.standard.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(toolRoot, "scripts", "lib", "hg-agent-parity.sh")
	source := strings.ReplaceAll(`#!/usr/bin/env bash
set -euo pipefail
hg_require() { return 0; }
source "__REAL_SCRIPT__"
hg_parity_metadata_path_label "__REPO_ROOT__" "__TOOL_TEMPLATE__"
`, "__REAL_SCRIPT__", filepath.Join("/tmp/codexkit-refresh-yJiz8x", "scripts", "lib", "hg-agent-parity.sh"))
	source = strings.ReplaceAll(source, "__REPO_ROOT__", repoRoot)
	source = strings.ReplaceAll(source, "__TOOL_TEMPLATE__", toolTemplate)
	if err := os.WriteFile(scriptPath, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(), "HG_AGENT_PARITY_SURFACEKIT_ROOT="+toolRoot)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("metadata label command failed: %v", err)
	}

	if got := strings.TrimSpace(string(out)); got != "codexkit/templates/gemini-settings.standard.json" {
		t.Fatalf("metadata path label = %q, want %q", got, "codexkit/templates/gemini-settings.standard.json")
	}
}
