package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCollectAuditUsesManifestAndPrunesArchiveTrees(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","baseline_target":true}]}`)
	writeFixture(t, root, "AGENTS.md", "# Workspace\n")
	writeFixture(t, root, "scripts/root.sh", "#!/bin/sh\n")
	writeFixture(t, root, "app/.git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, root, "app/AGENTS.md", "# App\n")
	writeFixture(t, root, "app/.claude/skills/demo/SKILL.md", "# Demo\n")
	writeFixture(t, root, "app/scripts/run.sh", "#!/bin/sh\n")
	writeFixture(t, root, "app/imported/legacy/bad.sh", "#!/bin/sh\n")
	writeFixture(t, root, "app/.claude/worktrees/runtime/CLAUDE.md", "# Runtime copy\n")
	writeFixture(t, root, "unmanaged/.git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, root, "unmanaged/scripts/ignored.sh", "#!/bin/sh\n")

	report, err := collectAudit(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Repos, []string{"app"}) {
		t.Fatalf("Repos = %v, want [app]", report.Repos)
	}
	if !slices.Contains(report.Scripts, "scripts/root.sh") || !slices.Contains(report.Scripts, "app/scripts/run.sh") {
		t.Fatalf("expected managed scripts in report: %v", report.Scripts)
	}
	for _, unwanted := range []string{"app/imported/legacy/bad.sh", "unmanaged/scripts/ignored.sh"} {
		if slices.Contains(report.Scripts, unwanted) {
			t.Fatalf("unexpected excluded script %q in report", unwanted)
		}
	}
	if got := report.LLMSurfaces["claude"]; len(got) != 1 || !strings.HasSuffix(got[0], "app/.claude/skills/demo/SKILL.md") {
		t.Fatalf("Claude surfaces = %v", got)
	}
	if got := report.LLMSurfaces["codex"]; len(got) != 2 {
		t.Fatalf("Codex surfaces = %v, want workspace and app AGENTS.md", got)
	}
}

func TestCheckGofmtComplianceProvesItCanFail(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","baseline_target":true}]}`)
	app := filepath.Join(root, "app")
	runGit(t, app, "init", "--quiet")
	writeFixture(t, root, "app/main.go", "package main\nfunc main(){println(\"bad\")}\n")
	runGit(t, app, "add", "main.go")

	var output bytes.Buffer
	if checkGofmtCompliance(root, &output) {
		t.Fatal("expected tracked unformatted Go file to fail the check")
	}
	if !strings.Contains(output.String(), "Unformatted Go file: app/main.go") {
		t.Fatalf("negative-control output missing finding:\n%s", output.String())
	}

	writeFixture(t, root, "app/main.go", "package main\n\nfunc main() { println(\"good\") }\n")
	output.Reset()
	if !checkGofmtCompliance(root, &output) {
		t.Fatalf("expected formatted Go file to pass:\n%s", output.String())
	}
}

func TestRunWritesReportsAndRejectsUnexpectedArguments(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "workspace/manifest.json", `{"version":1,"repos":[]}`)
	jsonPath := filepath.Join(root, "reports", "audit.json")
	mdPath := filepath.Join(root, "reports", "audit.md")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-workspace", root, "-json", jsonPath, "-md", mdPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var report AuditReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	if report.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", report.WorkspaceRoot, root)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unexpected-argument exit = %d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-workspace", root, "-json", root, "-md", mdPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("unwritable-report exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "error writing JSON report") {
		t.Fatalf("missing write error: %s", stderr.String())
	}
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
