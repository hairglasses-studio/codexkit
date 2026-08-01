package configindex

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildClassifiesRepoDotfilesHomesAndSecrets(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home", "user")
	rootHome := filepath.Join(root, "home", "root")
	write(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","scope":"active_product","lifecycle":"active","baseline_target":true},{"name":"ralphglasses","scope":"active_product","lifecycle":"active","baseline_target":true}]}`)

	for _, repo := range []string{"app", "ralphglasses"} {
		repoRoot := filepath.Join(root, repo)
		runGit(t, repoRoot, "init", "--quiet")
	}
	write(t, root, "app/AGENTS.md", "> Canonical instructions: AGENTS.md\n")
	write(t, root, "app/.agents/skills/demo/SKILL.md", "---\nname: demo\ndescription: demo\n---\n")
	write(t, root, "app/.claude/skills/demo/SKILL.md", "generated\n")
	write(t, root, "app/.codex/config.toml", "approval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")
	write(t, root, "ralphglasses/dotfiles/scripts/hg-agy-launch.sh", "#!/bin/sh\nexec agy-real \"$@\"\n")
	for _, repo := range []string{"app", "ralphglasses"} {
		runGit(t, filepath.Join(root, repo), "add", ".")
	}

	writeAbs(t, filepath.Join(userHome, ".claude", "settings.json"), "{}\n")
	writeAbs(t, filepath.Join(userHome, ".claude", ".credentials.json"), `{"token":"do-not-read"}`)
	writeAbs(t, filepath.Join(userHome, ".codex", "config.toml"), "approval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")
	writeAbs(t, filepath.Join(userHome, ".codex", "sessions", "one.jsonl"), "runtime\n")
	writeAbs(t, filepath.Join(userHome, ".agents", "memory", "projects", "demo", ".git", "objects", "aa", "object"), "runtime\n")

	index, err := Build(Options{
		WorkspaceRoot: root, GeneratedAt: "2026-08-01T00:00:00Z",
		Profiles: []Profile{{Name: "user", Home: userHome}, {Name: "root", Home: rootHome}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if index.Summary.ManifestRepos != 2 || index.Summary.ExistingRepos != 2 {
		t.Fatalf("unexpected repo summary: %+v", index.Summary)
	}
	generated := findFile(index.Files, "app/.claude/skills/demo/SKILL.md")
	if generated == nil || generated.Classification != "generated_managed" {
		t.Fatalf("generated Claude skill classification = %+v", generated)
	}
	secret := findFile(index.Files, ".claude/.credentials.json")
	if secret == nil || secret.Classification != "secret_authentication" || secret.SHA256 != "" {
		t.Fatalf("secret classification/hash = %+v", secret)
	}
	if !slices.ContainsFunc(index.Runtime, func(bucket RuntimeBucket) bool {
		return strings.HasSuffix(filepath.ToSlash(bucket.Path), "/.codex/sessions") && !bucket.Counted
	}) {
		t.Fatalf("missing redacted runtime bucket: %+v", index.Runtime)
	}
	if !slices.ContainsFunc(index.Runtime, func(bucket RuntimeBucket) bool {
		return strings.HasSuffix(filepath.ToSlash(bucket.Path), "/.agents/memory") && !bucket.Counted
	}) {
		t.Fatalf("missing redacted agent-memory bucket: %+v", index.Runtime)
	}
	if findFile(index.Files, ".git/objects/aa/object") != nil {
		t.Fatal("agent memory contents leaked into the file inventory")
	}
	if findFile(index.Files, "hg-agy-launch.sh") == nil {
		t.Fatal("dotfiles AGY launcher was not inventoried")
	}
}

func TestCheckProvesRestrictedDefaultsAndUnscopedDeleteFail(t *testing.T) {
	root := t.TempDir()
	write(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","scope":"active_product","lifecycle":"active","baseline_target":true},{"name":"ralphglasses","scope":"active_product","lifecycle":"active","baseline_target":true}]}`)
	for _, repo := range []string{"app", "ralphglasses"} {
		runGit(t, filepath.Join(root, repo), "init", "--quiet")
	}
	write(t, root, "app/.codex/config.toml", "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n")
	write(t, root, "ralphglasses/dotfiles/scripts/hg-agent-home-sync.sh", "#!/bin/sh\nrsync -a --delete source/ target/\n")
	for _, repo := range []string{"app", "ralphglasses"} {
		runGit(t, filepath.Join(root, repo), "add", ".")
	}
	report, err := Check(Options{WorkspaceRoot: root, GeneratedAt: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("restricted-default negative control unexpectedly passed")
	}
	for _, check := range []string{"codex_autonomy_default", "safe_home_sync"} {
		if !slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Check == check && !f.Passed }) {
			t.Fatalf("missing %s failure: %+v", check, report.Findings)
		}
	}
}

func TestCheckAcceptsAutonomousThreeProviderFixture(t *testing.T) {
	root := t.TempDir()
	write(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","scope":"active_product","lifecycle":"active","baseline_target":true}]}`)
	runGit(t, filepath.Join(root, "app"), "init", "--quiet")
	write(t, root, "app/AGENTS.md", "> Canonical instructions: AGENTS.md\n")
	write(t, root, "app/CLAUDE.md", "See AGENTS.md\n")
	write(t, root, "app/.codex/config.toml", "approval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")
	write(t, root, "app/.agents/rules/workspace.md", "# AGY workspace rule\n")
	runGit(t, filepath.Join(root, "app"), "add", ".")
	report, err := Check(Options{WorkspaceRoot: root, GeneratedAt: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("autonomous fixture failed: %+v", report.Findings)
	}
}

func TestCheckEnforcesAutonomyDefaultsInUserProviderHomes(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home", "user")
	write(t, root, "workspace/manifest.json", `{"version":1,"repos":[]}`)
	writeAbs(t, filepath.Join(userHome, ".claude", "settings.json"), `{"permissions":{"defaultMode":"default"}}`)
	writeAbs(t, filepath.Join(userHome, ".codex", "config.toml"), "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n")
	writeAbs(t, filepath.Join(userHome, ".gemini", "antigravity-cli", "settings.json"), `{"agentMode":"plan","allowNonWorkspaceAccess":false,"artifactReviewPolicy":"asks-for-review","model":"Gemini 3.1 Pro (High)","toolPermission":"request-review"}`)

	report, err := Check(Options{
		WorkspaceRoot: root,
		GeneratedAt:   "2026-08-01T00:00:00Z",
		Profiles:      []Profile{{Name: "user", Home: userHome}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"claude_autonomy_default", "codex_autonomy_default", "agy_autonomy_default"} {
		if !slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Check == check && !f.Passed }) {
			t.Fatalf("missing %s failure: %+v", check, report.Findings)
		}
	}
}

func TestCheckAllowsHistoricalMentionsButRejectsActiveLegacyProjection(t *testing.T) {
	root := t.TempDir()
	write(t, root, "workspace/manifest.json", `{"version":1,"repos":[{"name":"app","scope":"active_product","lifecycle":"active","baseline_target":true}]}`)
	runGit(t, filepath.Join(root, "app"), "init", "--quiet")
	write(t, root, "app/docs/migrations/copilot-retirement.md", "Copilot and Gemini CLI were retired.\n")
	runGit(t, filepath.Join(root, "app"), "add", ".")

	report, err := Check(Options{WorkspaceRoot: root, GeneratedAt: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("historical migration evidence failed strict checks: %+v", report.Findings)
	}

	write(t, root, "app/.github/agents/legacy.md", "generated legacy agent\n")
	runGit(t, filepath.Join(root, "app"), "add", ".")
	report, err = Check(Options{WorkspaceRoot: root, GeneratedAt: "2026-08-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Check == "strict_provider_set" && !f.Passed }) {
		t.Fatalf("active legacy projection unexpectedly passed: %+v", report.Findings)
	}
}

func findFile(files []File, suffix string) *File {
	for i := range files {
		if strings.HasSuffix(filepath.ToSlash(files[i].Path), filepath.ToSlash(suffix)) {
			return &files[i]
		}
	}
	return nil
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	writeAbs(t, filepath.Join(root, filepath.FromSlash(rel)), content)
}

func writeAbs(t *testing.T, path, content string) {
	t.Helper()
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
