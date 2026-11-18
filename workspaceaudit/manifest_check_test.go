package workspaceaudit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeAuditFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeGitRepoDir(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeGitWorktreeDir(t *testing.T, root, rel, target string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+target+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasRule(report Report, rule string) bool {
	for _, f := range report.Findings {
		if f.Rule == rule && !f.Passed {
			return true
		}
	}
	return false
}

func setupCleanWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	makeGitRepoDir(t, root, "alpha")
	writeAuditFile(t, root, "alpha/.mcp.json", `{"mcpServers":{"alpha-srv":{"command":"echo","args":["ok"]}}}`)

	makeGitRepoDir(t, root, "beta-src")
	writeAuditFile(t, root, "beta-src/.mcp.json", `{"mcpServers":{"beta-srv":{"command":"echo","args":["ok"]}}}`)

	makeGitRepoDir(t, root, "old-archived")
	writeAuditFile(t, root, "old-archived/.mcp.json", `{"mcpServers":{"old-srv":{"command":"echo","args":["archived"]}}}`)
	writeAuditFile(t, root, "old-archived/mcp.json", `{"mcpServers":{"old-srv-two":{"command":"echo","args":["archived"]}}}`)

	makeGitRepoDir(t, root, "legacy-compat")
	writeAuditFile(t, root, "legacy-compat/.mcp.json", `{"mcpServers":{"compat-srv":{"command":"echo","args":["compat"]}}}`)

	makeGitWorktreeDir(t, root, "alpha-wt-1", "../alpha/.git/worktrees/alpha-wt-1")
	writeAuditFile(t, root, "alpha-wt-1/.mcp.json", `{"mcpServers":{"wt-srv":{"command":"echo","args":["wt"]}}}`)

	makeGitWorktreeDir(t, root, "plain-linked-wt", "../alpha/.git/worktrees/plain-linked-wt")
	writeAuditFile(t, root, "plain-linked-wt/.mcp.json", `{"mcpServers":{"plain-wt-srv":{"command":"echo","args":["wt"]}}}`)

	makeGitWorktreeDir(t, root, "wt-branch", "../alpha/.git/worktrees/wt-branch")
	makeGitWorktreeDir(t, root, ".wt-hidden", "../alpha/.git/worktrees/.wt-hidden")
	makeGitWorktreeDir(t, root, "s2f_lane1", "../alpha/.git/worktrees/s2f_lane1")
	makeGitWorktreeDir(t, root, ".hg-anchor-edit-9", "../alpha/.git/worktrees/anchor9")
	writeAuditFile(t, root, ".worktrees/nested-wt/.mcp.json", `{"mcpServers":{"nested":{"command":"echo"}}}`)
	writeAuditFile(t, root, "worktrees/plain-wt/.mcp.json", `{"mcpServers":{"plain":{"command":"echo"}}}`)
	writeAuditFile(t, root, ".codex-worktrees/cx-1/.mcp.json", `{"mcpServers":{"cx":{"command":"echo"}}}`)
	makeGitWorktreeDir(t, root, ".claude/worktrees/cl-1", "../../alpha/.git/worktrees/cl-1")
	writeAuditFile(t, root, ".claude/worktrees/cl-1/.mcp.json", `{"mcpServers":{"cl":{"command":"echo"}}}`)
	makeGitWorktreeDir(t, root, ".ralph/worktrees/rp-1", "../../alpha/.git/worktrees/rp-1")

	writeAuditFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "alpha", "category": "hub", "scope": "active_operator", "lifecycle": "active"},
    {"name": "beta", "path": "beta-src", "category": "tooling", "scope": "active_first_party", "lifecycle": "active"},
    {"name": "old-archived", "category": "gateway", "scope": "active_first_party", "lifecycle": "archived"},
    {"name": "legacy-compat", "category": "hub", "scope": "compatibility_only", "lifecycle": "compatibility_only"}
  ]
}`)

	writeAuditFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "defaults": {"include_root": true, "ready_only": true},
  "manifest": {
    "use_workspace_manifest": true,
    "allow_unlisted_repos": false,
    "exclude_scopes": ["compatibility_only"]
  },
  "repos": [
    {"name": "alpha", "enabled": true}
  ],
  "servers": [
    {"repo": "beta", "server": "beta-srv", "alias": "beta-main"}
  ]
}`)
	return root
}

func TestAuditCleanPass(t *testing.T) {
	root := setupCleanWorkspace(t)
	report, err := Audit(Options{
		WorkspaceRoot: root,
		CheckOnDisk:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected clean pass, got findings: %+v", report.Findings)
	}
	if report.TotalManifestRepos != 4 {
		t.Fatalf("want 4 manifest repos, got %d", report.TotalManifestRepos)
	}
	if report.ActiveRepos != 2 {
		t.Fatalf("want 2 active repos, got %d", report.ActiveRepos)
	}
	if report.ArchivedRepos != 1 {
		t.Fatalf("want 1 archived repo, got %d", report.ArchivedRepos)
	}
	if len(report.UnlistedDiskRepos) != 0 {
		t.Fatalf("expected no unlisted disk repos, got %v", report.UnlistedDiskRepos)
	}
	if len(report.BleedServers) != 0 {
		t.Fatalf("expected no bleed servers, got %v", report.BleedServers)
	}
	for _, wantSkipped := range []string{
		".claude/worktrees/cl-1",
		".codex-worktrees",
		".hg-anchor-edit-9",
		".ralph/worktrees/rp-1",
		".worktrees",
		".wt-hidden",
		"alpha-wt-1",
		"plain-linked-wt",
		"s2f_lane1",
		"worktrees",
		"wt-branch",
	} {
		if !slices.Contains(report.WorktreesSkipped, wantSkipped) {
			t.Fatalf("expected %q in WorktreesSkipped, got %v", wantSkipped, report.WorktreesSkipped)
		}
	}
}

func TestAuditAllowUnlistedReposFailure(t *testing.T) {
	manifest := []byte(`{"version":1,"repos":[{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"active"}]}`)
	policy := []byte(`{"version":1,"manifest":{"use_workspace_manifest":true,"allow_unlisted_repos":true,"exclude_scopes":["compatibility_only"]}}`)
	report, err := AuditBytes(manifest, policy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected failure when allow_unlisted_repos is true")
	}
	if !hasRule(report, "policy.allow_unlisted_repos_enabled") {
		t.Fatalf("expected policy.allow_unlisted_repos_enabled, got %+v", report.Findings)
	}
}

func TestAuditUnlistedDiskRepoFailure(t *testing.T) {
	root := setupCleanWorkspace(t)
	makeGitRepoDir(t, root, "rogue-repo")

	report, err := Audit(Options{
		WorkspaceRoot: root,
		CheckOnDisk:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected failure for unlisted disk repo")
	}
	if !hasRule(report, "manifest.unlisted_disk_repo") {
		t.Fatalf("expected manifest.unlisted_disk_repo, got %+v", report.Findings)
	}
	if !slices.Equal(report.UnlistedDiskRepos, []string{"rogue-repo"}) {
		t.Fatalf("want [rogue-repo], got %v", report.UnlistedDiskRepos)
	}
}

func TestAuditPolicyUnlistedRepoReference(t *testing.T) {
	manifest := []byte(`{"version":1,"repos":[{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"active"}]}`)
	policy := []byte(`{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]},
  "repos": [{"name": "ghost-repo", "enabled": true}],
  "servers": [{"repo": "phantom-repo", "server": "s1", "enabled": true}]
}`)
	report, err := AuditBytes(manifest, policy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected failure for unlisted policy repo/server references")
	}
	count := 0
	for _, f := range report.Findings {
		if f.Rule == "policy.unlisted_repo_reference" && !f.Passed {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("want 2 policy.unlisted_repo_reference findings, got %d (%+v)", count, report.Findings)
	}
}

func TestAuditArchivedBleed(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		wantRule  string
		wantBleed string
		onDisk    bool
		tomlCfg   string
	}{
		{
			name: "archived_enabled_in_policy_repos",
			policy: `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]},
  "repos": [{"name": "old-archived", "enabled": true}]
}`,
			wantRule:  "mcp.archived_repo_bleed",
			wantBleed: "old-archived",
		},
		{
			name: "archived_enabled_in_policy_servers",
			policy: `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]},
  "servers": [{"repo": "old-archived", "server": "old-srv", "enabled": true}]
}`,
			wantRule:  "mcp.archived_server_bleed",
			wantBleed: "old-archived:old-srv",
		},
		{
			name: "archived_unblocked_on_disk_when_manifest_disabled",
			policy: `{
  "version": 1,
  "manifest": {"use_workspace_manifest": false, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]}
}`,
			wantRule:  "mcp.archived_server_bleed",
			wantBleed: "old-archived:old-srv",
			onDisk:    true,
		},
		{
			name: "archived_in_effective_codex_config",
			policy: `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]}
}`,
			wantRule:  "mcp.archived_server_bleed",
			wantBleed: "leaked-archived",
			onDisk:    true,
			tomlCfg:   "[mcp_servers.leaked-archived]\ncommand = \"echo\"\ncwd = \"old-archived\"\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var report Report
			var err error
			if tc.onDisk {
				root := setupCleanWorkspace(t)
				writeAuditFile(t, root, "workspace/mcp-global-policy.json", tc.policy)
				if tc.tomlCfg != "" {
					writeAuditFile(t, root, ".codex/config.toml", tc.tomlCfg)
				}
				report, err = Audit(Options{WorkspaceRoot: root, CheckOnDisk: true})
			} else {
				manifest := []byte(`{"version":1,"repos":[{"name":"old-archived","category":"gateway","scope":"active_first_party","lifecycle":"archived"}]}`)
				report, err = AuditBytes(manifest, []byte(tc.policy))
			}
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed {
				t.Fatalf("expected failure, got pass")
			}
			if !hasRule(report, tc.wantRule) {
				t.Fatalf("expected rule %q, got %+v", tc.wantRule, report.Findings)
			}
			if !slices.Contains(report.BleedServers, tc.wantBleed) {
				t.Fatalf("expected %q in BleedServers, got %v", tc.wantBleed, report.BleedServers)
			}
		})
	}
}

func TestAuditWorktreeBleed(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, root string)
		wantRule string
	}{
		{
			name: "manifest_path_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [{"name": "alpha", "path": ".worktrees/alpha-wt", "category": "hub", "scope": "active_operator", "lifecycle": "active"}]
}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "manifest_path_git_file_worktree",
			setup: func(t *testing.T, root string) {
				makeGitWorktreeDir(t, root, "custom-checkout", "../alpha/.git/worktrees/custom-checkout")
				writeAuditFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [{"name": "alpha", "path": "custom-checkout", "category": "hub", "scope": "active_operator", "lifecycle": "active"}]
}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "policy_repos_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]},
  "repos": [{"name": "alpha-wt-1", "enabled": true}]
}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "policy_servers_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": ["compatibility_only"]},
  "servers": [{"repo": "alpha", "server": "alpha-srv", "path": ".claude/worktrees/cl-1", "enabled": true}]
}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "candidate_mcp_server_command_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, "alpha/.mcp.json", `{"mcpServers":{"alpha-srv":{"command":"../alpha-wt-1/bin/srv"}}}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "candidate_mcp_server_cwd_git_file_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, "alpha/.mcp.json", `{"mcpServers":{"alpha-srv":{"command":"echo","cwd":"../plain-linked-wt"}}}`)
			},
			wantRule: "mcp.worktree_bleed",
		},
		{
			name: "effective_codex_config_worktree",
			setup: func(t *testing.T, root string) {
				writeAuditFile(t, root, ".codex/config.toml", "[mcp_servers.bad-wt]\ncommand = \"./wt-branch/bin/srv\"\n")
			},
			wantRule: "mcp.worktree_bleed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := setupCleanWorkspace(t)
			tc.setup(t, root)
			report, err := Audit(Options{WorkspaceRoot: root, CheckOnDisk: true})
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed {
				t.Fatal("expected failure on worktree bleed")
			}
			if !hasRule(report, tc.wantRule) {
				t.Fatalf("expected rule %q, got %+v", tc.wantRule, report.Findings)
			}
			if len(report.BleedServers) == 0 {
				t.Fatal("expected non-empty BleedServers")
			}
		})
	}
}

func TestAuditCompatibilityScopeBleed(t *testing.T) {
	root := setupCleanWorkspace(t)
	writeAuditFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": []},
  "repos": [{"name": "legacy-compat", "enabled": true}],
  "servers": [{"repo": "legacy-compat", "server": "compat-srv", "enabled": true}]
}`)
	report, err := Audit(Options{WorkspaceRoot: root, CheckOnDisk: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("expected compatibility_only bleed failure")
	}
	if !hasRule(report, "policy.compatibility_scope_not_excluded") {
		t.Fatalf("expected policy.compatibility_scope_not_excluded, got %+v", report.Findings)
	}
	if !hasRule(report, "mcp.compatibility_scope_bleed") {
		t.Fatalf("expected mcp.compatibility_scope_bleed, got %+v", report.Findings)
	}
}

func TestAuditManifestSchemaValidation(t *testing.T) {
	policy := []byte(`{"version":1,"manifest":{"use_workspace_manifest":true,"allow_unlisted_repos":false,"exclude_scopes":["compatibility_only"]}}`)

	cases := []struct {
		name     string
		manifest string
		wantRule string
	}{
		{
			name:     "invalid_version",
			manifest: `{"version":2,"repos":[{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"active"}]}`,
			wantRule: "manifest.invalid_version",
		},
		{
			name:     "empty_repos",
			manifest: `{"version":1,"repos":[]}`,
			wantRule: "manifest.empty_repos",
		},
		{
			name:     "duplicate_repo_names",
			manifest: `{"version":1,"repos":[{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"active"},{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"active"}]}`,
			wantRule: "manifest.duplicate_repo_name",
		},
		{
			name:     "empty_repo_name",
			manifest: `{"version":1,"repos":[{"name":"","category":"hub","scope":"active_operator","lifecycle":"active"}]}`,
			wantRule: "manifest.empty_repo_name",
		},
		{
			name:     "invalid_scope",
			manifest: `{"version":1,"repos":[{"name":"alpha","category":"hub","scope":"unknown","lifecycle":"active"}]}`,
			wantRule: "manifest.invalid_scope",
		},
		{
			name:     "invalid_lifecycle",
			manifest: `{"version":1,"repos":[{"name":"alpha","category":"hub","scope":"active_operator","lifecycle":"retired"}]}`,
			wantRule: "manifest.invalid_lifecycle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := AuditBytes([]byte(tc.manifest), policy)
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed {
				t.Fatal("expected failure")
			}
			if !hasRule(report, tc.wantRule) {
				t.Fatalf("expected %q, got %+v", tc.wantRule, report.Findings)
			}
		})
	}
}

func TestAuditJSONDeterminism(t *testing.T) {
	root := setupCleanWorkspace(t)
	makeGitRepoDir(t, root, "z-unlisted")
	makeGitRepoDir(t, root, "a-unlisted")
	writeAuditFile(t, root, "workspace/mcp-global-policy.json", `{
  "version": 1,
  "manifest": {"use_workspace_manifest": true, "allow_unlisted_repos": false, "exclude_scopes": []},
  "repos": [{"name": "old-archived", "enabled": true}, {"name": "z-missing", "enabled": true}],
  "servers": [{"repo": "legacy-compat", "server": "compat-srv", "enabled": true}]
}`)

	var first []byte
	for i := 0; i < 5; i++ {
		report, err := Audit(Options{WorkspaceRoot: root, CheckOnDisk: true})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("non-deterministic JSON on run %d:\nfirst: %s\ngot:   %s", i, first, raw)
		}
	}
}

func TestHelpers(t *testing.T) {
	if !IsActiveScope("active") || !IsActiveScope("active_operator") || !IsActiveScope("active_first_party") {
		t.Fatal("expected active scopes to return true")
	}
	if IsActiveScope("compatibility_only") || IsActiveScope("") {
		t.Fatal("expected non-active scopes to return false")
	}
	if !ShouldSkipDir(".worktrees") || !ShouldSkipDir("wt-foo") || !ShouldSkipDir("repo-wt-1") || !ShouldSkipDir("s2f_abc") || !ShouldSkipDir(".hg-anchor-edit-1") {
		t.Fatal("expected transient/worktree dirs to be skipped")
	}
	if ShouldSkipDir("codexkit") {
		t.Fatal("expected normal repo dir not to be skipped")
	}
	if !IsWorktreePath(".claude/worktrees/w1") || !IsWorktreePath(".ralph/worktrees/w2") || !IsWorktreePath(".codex-worktrees/w3") {
		t.Fatal("expected worktree path patterns to match")
	}
	if IsWorktreePath(".claude/skills/s1") {
		t.Fatal("expected non-worktree .claude path not to match")
	}

	root := t.TempDir()
	wt := makeGitWorktreeDir(t, root, "plain-name", "../repo/.git/worktrees/plain-name")
	if !IsWorktreeDir(wt) {
		t.Fatal("expected .git file worktree to be detected")
	}
	normal := makeGitRepoDir(t, root, "normal-repo")
	if IsWorktreeDir(normal) {
		t.Fatal("expected normal git repo not to be detected as worktree")
	}
}
