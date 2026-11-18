package workspaceaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hairglasses-studio/codexkit/workspace"
	"github.com/pelletier/go-toml/v2"
)

type Options struct {
	WorkspaceRoot string `json:"workspace_root"`
	ManifestPath  string `json:"manifest_path,omitempty"`
	PolicyPath    string `json:"policy_path,omitempty"`
	CheckOnDisk   bool   `json:"check_on_disk"`
}

type Finding struct {
	Rule     string `json:"rule"`
	Repo     string `json:"repo,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Passed   bool   `json:"passed"`
}

type Report struct {
	WorkspaceRoot      string    `json:"workspace_root"`
	Passed             bool      `json:"passed"`
	TotalManifestRepos int       `json:"total_manifest_repos"`
	ActiveRepos        int       `json:"active_repos"`
	ArchivedRepos      int       `json:"archived_repos"`
	UnlistedDiskRepos  []string  `json:"unlisted_disk_repos,omitempty"`
	WorktreesSkipped   []string  `json:"worktrees_skipped,omitempty"`
	BleedServers       []string  `json:"bleed_servers,omitempty"`
	Findings           []Finding `json:"findings"`
}

type manifestDoc struct {
	Version int            `json:"version"`
	Repos   []manifestRepo `json:"repos"`
}

type manifestRepo struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	Scope     string `json:"scope"`
	Lifecycle string `json:"lifecycle"`
	Path      string `json:"path,omitempty"`
}

type policyDoc struct {
	Version  int                  `json:"version"`
	Defaults policyDefaults       `json:"defaults"`
	Manifest policyManifestConfig `json:"manifest"`
	Repos    []policyRepoRule     `json:"repos,omitempty"`
	Servers  []policyServerRule   `json:"servers,omitempty"`
}

type policyDefaults struct {
	IncludeRoot bool `json:"include_root"`
	ReadyOnly   bool `json:"ready_only"`
}

type policyManifestConfig struct {
	UseWorkspaceManifest bool     `json:"use_workspace_manifest"`
	AllowUnlistedRepos   bool     `json:"allow_unlisted_repos"`
	IncludeScopes        []string `json:"include_scopes,omitempty"`
	ExcludeScopes        []string `json:"exclude_scopes,omitempty"`
	IncludeCategories    []string `json:"include_categories,omitempty"`
	ExcludeCategories    []string `json:"exclude_categories,omitempty"`
}

type policyRepoRule struct {
	Name           string   `json:"name"`
	Enabled        *bool    `json:"enabled,omitempty"`
	AliasPrefix    string   `json:"alias_prefix,omitempty"`
	IncludeServers []string `json:"include_servers,omitempty"`
	ExcludeServers []string `json:"exclude_servers,omitempty"`
	Path           string   `json:"path,omitempty"`
}

type policyServerRule struct {
	Repo    string `json:"repo"`
	Server  string `json:"server"`
	Enabled *bool  `json:"enabled,omitempty"`
	Alias   string `json:"alias,omitempty"`
	Path    string `json:"path,omitempty"`
}

type mcpFileDoc struct {
	MCPServers map[string]mcpServerDef `json:"mcpServers"`
}

type mcpServerDef struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
}

type codexConfigDoc struct {
	MCPServers map[string]codexServerDef `toml:"mcp_servers"`
}

type codexServerDef struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	CWD     string            `toml:"cwd"`
	URL     string            `toml:"url"`
}

var IgnoredDirNames = map[string]bool{
	".archive":             true,
	".cache":               true,
	".codex-worktrees":     true,
	".data":                true,
	".git":                 true,
	".graveyard":           true,
	".mypy_cache":          true,
	".ouro":                true,
	".pytest_cache":        true,
	".ruff_cache":          true,
	".scratch":             true,
	".terraform":           true,
	".tools":               true,
	".venv":                true,
	".worktrees":           true,
	"__pycache__":          true,
	"_salvage":             true,
	"bin":                  true,
	"build":                true,
	"data":                 true,
	"dist":                 true,
	"git-cleanup-archives": true,
	"htmlcov":              true,
	"imported":             true,
	"node_modules":         true,
	"vault":                true,
	"vendor":               true,
	"venv":                 true,
	"venv_test":            true,
	"worktrees":            true,
}

var validScopes = map[string]bool{
	"active":             true,
	"active_operator":    true,
	"active_first_party": true,
	"compatibility_only": true,
}

var validLifecycles = map[string]bool{
	"active":             true,
	"maintenance-only":   true,
	"archived":           true,
	"compatibility_only": true,
	"never-publish":      true,
	"publish-mirror":     true,
}

func Audit(opts Options) (Report, error) {
	root := opts.WorkspaceRoot
	if root != "" {
		root = filepath.Clean(root)
	}
	manifestPath := opts.ManifestPath
	policyPath := opts.PolicyPath

	if manifestPath == "" {
		if root == "" {
			root = workspace.DefaultRoot()
		}
		manifestPath = workspace.ManifestPath(root)
	}
	if policyPath == "" {
		if root == "" {
			root = workspace.DefaultRoot()
		}
		policyPath = filepath.Join(root, "workspace", "mcp-global-policy.json")
	}
	if opts.CheckOnDisk && root == "" {
		root = workspace.DefaultRoot()
	}
	if opts.CheckOnDisk {
		info, err := os.Stat(root)
		if err != nil {
			return Report{}, fmt.Errorf("stat workspace root %s: %w", root, err)
		}
		if !info.IsDir() {
			return Report{}, fmt.Errorf("workspace root is not a directory: %s", root)
		}
	}

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return Report{}, fmt.Errorf("reading manifest %s: %w", manifestPath, err)
	}
	policyData, err := os.ReadFile(policyPath)
	if err != nil {
		return Report{}, fmt.Errorf("reading policy %s: %w", policyPath, err)
	}
	return audit(root, manifestData, policyData, opts.CheckOnDisk)
}

func AuditBytes(manifestData, policyData []byte) (Report, error) {
	return audit("", manifestData, policyData, false)
}

func audit(root string, manifestData, policyData []byte, checkOnDisk bool) (Report, error) {
	var manifest manifestDoc
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return Report{}, fmt.Errorf("parsing manifest JSON: %w", err)
	}
	var policy policyDoc
	if err := json.Unmarshal(policyData, &policy); err != nil {
		return Report{}, fmt.Errorf("parsing policy JSON: %w", err)
	}

	report := Report{
		WorkspaceRoot:      root,
		Passed:             true,
		TotalManifestRepos: len(manifest.Repos),
		Findings:           []Finding{},
	}

	if manifest.Version != 1 {
		report.add("manifest.invalid_version", "", "error", fmt.Sprintf("manifest version is %d, want 1", manifest.Version), false)
	}
	if len(manifest.Repos) == 0 {
		report.add("manifest.empty_repos", "", "error", "manifest repos list is empty", false)
	}

	manifestRepos := make(map[string]manifestRepo, len(manifest.Repos))
	pathAliases := make(map[string]string)
	for i, repo := range manifest.Repos {
		name := strings.TrimSpace(repo.Name)
		if name == "" {
			report.add("manifest.empty_repo_name", "", "error", fmt.Sprintf("manifest repos[%d] has empty name", i), false)
			continue
		}
		if _, dup := manifestRepos[name]; dup {
			report.add("manifest.duplicate_repo_name", name, "error", "duplicate repo name in manifest", false)
			continue
		}
		manifestRepos[name] = repo

		if repo.Lifecycle == "archived" {
			report.ArchivedRepos++
		} else if IsActiveScope(repo.Scope) {
			report.ActiveRepos++
		}

		if !validScopes[repo.Scope] {
			report.add("manifest.invalid_scope", name, "error", fmt.Sprintf("invalid manifest scope %q", repo.Scope), false)
		}
		if !validLifecycles[repo.Lifecycle] {
			report.add("manifest.invalid_lifecycle", name, "error", fmt.Sprintf("invalid manifest lifecycle %q", repo.Lifecycle), false)
		}

		if repo.Path != "" {
			if IsWorktreePath(repo.Path) {
				report.add("mcp.worktree_bleed", name, "error", fmt.Sprintf("manifest path %q points into a worktree directory", repo.Path), false)
				report.BleedServers = append(report.BleedServers, name)
			} else if checkOnDisk && root != "" {
				resolved := resolveRepoPath(root, name, repo.Path)
				if IsWorktreeDir(resolved) {
					report.add("mcp.worktree_bleed", name, "error", fmt.Sprintf("manifest path %q resolves to a git worktree", repo.Path), false)
					report.BleedServers = append(report.BleedServers, name)
				}
			}
			cleanPath := filepath.ToSlash(filepath.Clean(repo.Path))
			if cleanPath != "." && !strings.Contains(cleanPath, "/") && cleanPath != name {
				pathAliases[cleanPath] = name
			}
		}
	}

	if policy.Version != 1 {
		report.add("policy.invalid_version", "", "error", fmt.Sprintf("policy version is %d, want 1", policy.Version), false)
	}
	if !policy.Manifest.UseWorkspaceManifest {
		report.add("policy.manifest_disabled", "", "error", "policy.manifest.use_workspace_manifest must be true", false)
	}
	if policy.Manifest.AllowUnlistedRepos {
		report.add("policy.allow_unlisted_repos_enabled", "", "error", "policy.manifest.allow_unlisted_repos must be false", false)
	}
	if !slices.Contains(policy.Manifest.ExcludeScopes, "compatibility_only") {
		report.add("policy.compatibility_scope_not_excluded", "", "error", "policy.manifest.exclude_scopes must include compatibility_only", false)
	}

	repoRules := make(map[string]policyRepoRule, len(policy.Repos))
	for _, rule := range policy.Repos {
		repoName := strings.TrimSpace(rule.Name)
		if IsWorktreePath(rule.Name) || (rule.Path != "" && IsWorktreePath(rule.Path)) {
			report.add("mcp.worktree_bleed", repoName, "error", "policy.repos entry references a worktree path", false)
			if repoName != "" {
				report.BleedServers = append(report.BleedServers, repoName)
			} else if rule.Path != "" {
				report.BleedServers = append(report.BleedServers, rule.Path)
			}
		} else if checkOnDisk && root != "" {
			candidatePath := rule.Path
			if candidatePath == "" && repoName != "" {
				candidatePath = repoName
			}
			if candidatePath != "" && IsWorktreeDir(resolveRepoPath(root, repoName, candidatePath)) {
				report.add("mcp.worktree_bleed", repoName, "error", "policy.repos entry resolves to a git worktree on disk", false)
				report.BleedServers = append(report.BleedServers, repoName)
			}
		}

		repo, listed := manifestRepos[repoName]
		if repoName == "" || !listed {
			report.add("policy.unlisted_repo_reference", repoName, "error", fmt.Sprintf("policy.repos references unlisted repo %q", rule.Name), false)
			continue
		}
		repoRules[repoName] = rule
		if rule.Enabled != nil && *rule.Enabled {
			if repo.Lifecycle == "archived" {
				report.add("mcp.archived_repo_bleed", repoName, "error", "archived repo is explicitly enabled in policy.repos", false)
				report.BleedServers = append(report.BleedServers, repoName)
			}
			if isCompatibilityOnly(repo) {
				report.add("mcp.compatibility_scope_bleed", repoName, "error", "compatibility_only repo is explicitly enabled in policy.repos", false)
				report.BleedServers = append(report.BleedServers, repoName)
			}
		}
	}

	serverRules := make(map[string]policyServerRule, len(policy.Servers))
	for _, rule := range policy.Servers {
		repoName := strings.TrimSpace(rule.Repo)
		srvName := strings.TrimSpace(rule.Server)
		serverID := repoName + ":" + srvName

		if IsWorktreePath(rule.Repo) || (rule.Path != "" && IsWorktreePath(rule.Path)) {
			report.add("mcp.worktree_bleed", repoName, "error", fmt.Sprintf("policy.servers entry %q references a worktree path", serverID), false)
			report.BleedServers = append(report.BleedServers, serverID)
		} else if checkOnDisk && root != "" {
			candidatePath := rule.Path
			if candidatePath == "" && repoName != "" {
				candidatePath = repoName
			}
			if candidatePath != "" && IsWorktreeDir(resolveRepoPath(root, repoName, candidatePath)) {
				report.add("mcp.worktree_bleed", repoName, "error", fmt.Sprintf("policy.servers entry %q resolves to a git worktree on disk", serverID), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
		}

		repo, listed := manifestRepos[repoName]
		if repoName == "" || !listed {
			report.add("policy.unlisted_repo_reference", repoName, "error", fmt.Sprintf("policy.servers references unlisted repo %q", rule.Repo), false)
			continue
		}
		serverRules[serverRuleKey(repoName, srvName)] = rule
		if rule.Enabled != nil && *rule.Enabled {
			if repo.Lifecycle == "archived" {
				report.add("mcp.archived_server_bleed", repoName, "error", fmt.Sprintf("archived repo server %q is explicitly enabled in policy.servers", serverID), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
			if isCompatibilityOnly(repo) {
				report.add("mcp.compatibility_scope_bleed", repoName, "error", fmt.Sprintf("compatibility_only repo server %q is explicitly enabled in policy.servers", serverID), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
		}
	}

	if checkOnDisk {
		if err := auditOnDisk(root, manifestRepos, pathAliases, policy, repoRules, serverRules, &report); err != nil {
			return Report{}, err
		}
	}

	finalizeReport(&report)
	return report, nil
}

func auditOnDisk(
	root string,
	manifestRepos map[string]manifestRepo,
	pathAliases map[string]string,
	policy policyDoc,
	repoRules map[string]policyRepoRule,
	serverRules map[string]policyServerRule,
	report *Report,
) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("reading workspace root %s: %w", root, err)
	}

	for _, relParent := range []string{".claude/worktrees", ".ralph/worktrees"} {
		parentDir := filepath.Join(root, filepath.FromSlash(relParent))
		children, readErr := os.ReadDir(parentDir)
		if readErr != nil {
			continue
		}
		for _, child := range children {
			if child.IsDir() {
				report.WorktreesSkipped = append(report.WorktreesSkipped, relParent+"/"+child.Name())
			}
		}
	}

	checkedRepoMCP := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dirPath := filepath.Join(root, name)

		if IsWorktreePath(name) || isGitWorktreeMarker(dirPath) {
			report.WorktreesSkipped = append(report.WorktreesSkipped, name)
			continue
		}
		if ShouldSkipDir(name) {
			continue
		}

		canonicalName := name
		if mapped, ok := pathAliases[name]; ok {
			canonicalName = mapped
		}
		repo, listed := manifestRepos[canonicalName]

		if _, gitErr := os.Stat(filepath.Join(dirPath, ".git")); gitErr == nil {
			if !listed {
				report.UnlistedDiskRepos = append(report.UnlistedDiskRepos, name)
				if !policy.Manifest.AllowUnlistedRepos {
					report.add("manifest.unlisted_disk_repo", name, "error", "on-disk git repository is not listed in workspace manifest", false)
				}
			}
		}

		if listed {
			checkedRepoMCP[canonicalName] = true
			inspectRepoMCPFiles(root, canonicalName, dirPath, repo, policy, repoRules, serverRules, report)
		} else {
			inspectUnlistedRepoMCPFiles(root, name, dirPath, report)
		}
	}

	for name, repo := range manifestRepos {
		if checkedRepoMCP[name] {
			continue
		}
		repoPath := resolveRepoPath(root, name, repo.Path)
		info, statErr := os.Stat(repoPath)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if IsWorktreeDir(repoPath) {
			continue
		}
		inspectRepoMCPFiles(root, name, repoPath, repo, policy, repoRules, serverRules, report)
	}

	if policy.Defaults.IncludeRoot {
		inspectRootMCPFiles(root, report)
	}
	inspectEffectiveCodexConfig(root, manifestRepos, policy, repoRules, serverRules, report)
	return nil
}

func inspectRootMCPFiles(root string, report *Report) {
	rootName := filepath.Base(root)
	for _, filename := range []string{".mcp.json", "mcp.json"} {
		mcpPath := filepath.Join(root, filename)
		servers, ok := readMCPFile(mcpPath)
		if !ok {
			continue
		}
		for srvName, srv := range servers {
			if serverReferencesWorktree(root, root, srv) {
				serverID := rootName + ":" + srvName
				report.add("mcp.worktree_bleed", rootName, "error", fmt.Sprintf("root %s server %q references a worktree path", filename, srvName), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
		}
	}
}

func inspectUnlistedRepoMCPFiles(root, repoName, repoPath string, report *Report) {
	for _, filename := range []string{".mcp.json", "mcp.json"} {
		mcpPath := filepath.Join(repoPath, filename)
		servers, ok := readMCPFile(mcpPath)
		if !ok {
			continue
		}
		for srvName, srv := range servers {
			if serverReferencesWorktree(root, repoPath, srv) {
				serverID := repoName + ":" + srvName
				report.add("mcp.worktree_bleed", repoName, "error", fmt.Sprintf("server %q in %s references a worktree path", srvName, filename), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
		}
	}
}

func inspectRepoMCPFiles(
	root string,
	repoName string,
	repoPath string,
	repo manifestRepo,
	policy policyDoc,
	repoRules map[string]policyRepoRule,
	serverRules map[string]policyServerRule,
	report *Report,
) {
	repoRule, hasRepoRule := repoRules[repoName]
	for _, filename := range []string{".mcp.json", "mcp.json"} {
		mcpPath := filepath.Join(repoPath, filename)
		servers, ok := readMCPFile(mcpPath)
		if !ok {
			continue
		}
		for srvName, srv := range servers {
			serverID := repoName + ":" + srvName
			if serverReferencesWorktree(root, repoPath, srv) {
				report.add("mcp.worktree_bleed", repoName, "error", fmt.Sprintf("server %q in %s references a worktree path", srvName, filename), false)
				report.BleedServers = append(report.BleedServers, serverID)
			}
			serverRule, hasServerRule := serverRules[serverRuleKey(repoName, srvName)]
			if !isServerBlockedByPolicy(repo, policy, repoRule, hasRepoRule, serverRule, hasServerRule, srvName) {
				if repo.Lifecycle == "archived" {
					report.add("mcp.archived_server_bleed", repoName, "error", fmt.Sprintf("archived repo server %q in %s is not blocked by global policy", srvName, filename), false)
					report.BleedServers = append(report.BleedServers, serverID)
				}
				if isCompatibilityOnly(repo) {
					report.add("mcp.compatibility_scope_bleed", repoName, "error", fmt.Sprintf("compatibility_only repo server %q in %s is not blocked by global policy", srvName, filename), false)
					report.BleedServers = append(report.BleedServers, serverID)
				}
			}
		}
	}
}

func inspectEffectiveCodexConfig(
	root string,
	manifestRepos map[string]manifestRepo,
	policy policyDoc,
	repoRules map[string]policyRepoRule,
	serverRules map[string]policyServerRule,
	report *Report,
) {
	cfgPath := filepath.Join(root, ".codex", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var cfg codexConfigDoc
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return
	}

	repoByPath := make(map[string]manifestRepo, len(manifestRepos))
	for name, repo := range manifestRepos {
		resolved := filepath.Clean(resolveRepoPath(root, name, repo.Path))
		repoByPath[resolved] = repo
	}

	for alias, srv := range cfg.MCPServers {
		def := mcpServerDef{
			Command: srv.Command,
			Args:    srv.Args,
			Env:     srv.Env,
			CWD:     srv.CWD,
			URL:     srv.URL,
		}
		if serverReferencesWorktree(root, root, def) {
			report.add("mcp.worktree_bleed", "", "error", fmt.Sprintf("effective codex server %q references a worktree path", alias), false)
			report.BleedServers = append(report.BleedServers, alias)
		}
		if srv.CWD == "" {
			continue
		}
		cwd := filepath.Clean(srv.CWD)
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Clean(filepath.Join(root, cwd))
		}
		for repoPath, repo := range repoByPath {
			if cwd == repoPath || strings.HasPrefix(cwd, repoPath+string(filepath.Separator)) {
				if repo.Lifecycle == "archived" {
					report.add("mcp.archived_server_bleed", repo.Name, "error", fmt.Sprintf("effective codex server %q targets archived repo %q", alias, repo.Name), false)
					report.BleedServers = append(report.BleedServers, alias)
				}
				if isCompatibilityOnly(repo) && !repoAllowedByExplicitRule(repo, policy, repoRules, serverRules) {
					report.add("mcp.compatibility_scope_bleed", repo.Name, "error", fmt.Sprintf("effective codex server %q targets excluded compatibility_only repo %q", alias, repo.Name), false)
					report.BleedServers = append(report.BleedServers, alias)
				}
			}
		}
	}
}

func repoAllowedByExplicitRule(repo manifestRepo, policy policyDoc, repoRules map[string]policyRepoRule, serverRules map[string]policyServerRule) bool {
	if slices.Contains(policy.Manifest.ExcludeScopes, "compatibility_only") {
		return false
	}
	if rule, ok := repoRules[repo.Name]; ok && rule.Enabled != nil && !*rule.Enabled {
		return false
	}
	return true
}

func isServerBlockedByPolicy(
	repo manifestRepo,
	policy policyDoc,
	repoRule policyRepoRule,
	hasRepoRule bool,
	serverRule policyServerRule,
	hasServerRule bool,
	serverName string,
) bool {
	if hasServerRule && serverRule.Enabled != nil && !*serverRule.Enabled {
		return true
	}
	if hasRepoRule && len(repoRule.IncludeServers) > 0 && !slices.Contains(repoRule.IncludeServers, serverName) {
		if !(hasServerRule && serverRule.Enabled != nil && *serverRule.Enabled) {
			return true
		}
	}
	if hasRepoRule && slices.Contains(repoRule.ExcludeServers, serverName) {
		if !(hasServerRule && serverRule.Enabled != nil && *serverRule.Enabled) {
			return true
		}
	}
	if hasRepoRule && repoRule.Enabled != nil {
		return !*repoRule.Enabled
	}
	if !policy.Manifest.UseWorkspaceManifest {
		return false
	}
	if repo.Lifecycle == "archived" {
		return true
	}
	if len(policy.Manifest.IncludeScopes) > 0 && !slices.Contains(policy.Manifest.IncludeScopes, repo.Scope) {
		return true
	}
	if slices.Contains(policy.Manifest.ExcludeScopes, repo.Scope) {
		return true
	}
	if len(policy.Manifest.IncludeCategories) > 0 && !slices.Contains(policy.Manifest.IncludeCategories, repo.Category) {
		return true
	}
	if slices.Contains(policy.Manifest.ExcludeCategories, repo.Category) {
		return true
	}
	return false
}

func readMCPFile(path string) (map[string]mcpServerDef, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc mcpFileDoc
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.MCPServers) == 0 {
		return nil, false
	}
	filtered := make(map[string]mcpServerDef, len(doc.MCPServers))
	for name, srv := range doc.MCPServers {
		if strings.HasPrefix(name, "_") {
			continue
		}
		filtered[name] = srv
	}
	return filtered, len(filtered) > 0
}

func serverReferencesWorktree(root, repoPath string, srv mcpServerDef) bool {
	candidates := make([]string, 0, 2+len(srv.Args)+len(srv.Env))
	if srv.Command != "" {
		candidates = append(candidates, srv.Command)
	}
	if srv.CWD != "" {
		candidates = append(candidates, srv.CWD)
	}
	candidates = append(candidates, srv.Args...)
	for _, val := range srv.Env {
		candidates = append(candidates, val)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if IsWorktreePath(c) {
			return true
		}
		if root != "" && !strings.HasPrefix(c, "-") && !strings.Contains(c, "://") {
			base := repoPath
			if base == "" {
				base = root
			}
			resolved := c
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(base, resolved)
			}
			resolved = filepath.Clean(resolved)
			if pathInsideGitWorktree(root, resolved) {
				return true
			}
		}
	}
	return false
}

func pathInsideGitWorktree(root, target string) bool {
	cur := filepath.Clean(target)
	rootClean := filepath.Clean(root)
	for {
		if isGitWorktreeMarker(cur) {
			return true
		}
		if cur == rootClean || cur == "/" || cur == "." {
			return false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

func resolveRepoPath(root, name, overridePath string) string {
	if overridePath == "" {
		return filepath.Join(root, name)
	}
	if filepath.IsAbs(overridePath) {
		return filepath.Clean(overridePath)
	}
	return filepath.Join(root, overridePath)
}

func isCompatibilityOnly(repo manifestRepo) bool {
	return repo.Scope == "compatibility_only" || repo.Lifecycle == "compatibility_only"
}

func serverRuleKey(repo, server string) string {
	return repo + "\x00" + server
}

func IsWorktreeDir(dir string) bool {
	if IsWorktreePath(dir) {
		return true
	}
	return isGitWorktreeMarker(dir)
}

func isGitWorktreeMarker(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return false
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "gitdir:") {
			return false
		}
		target := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(line, "gitdir:")))
		return strings.Contains(target, "/.git/worktrees/") || strings.Contains(target, "/worktrees/")
	}
	return false
}

func IsWorktreePath(p string) bool {
	if p == "" {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	segs := strings.Split(clean, "/")
	for i, seg := range segs {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".worktrees" || seg == "worktrees" || seg == ".codex-worktrees" {
			return true
		}
		if strings.HasPrefix(seg, ".wt-") ||
			strings.HasPrefix(seg, "wt-") ||
			strings.HasPrefix(seg, "s2f_") ||
			strings.HasPrefix(seg, ".hg-anchor-edit-") ||
			strings.Contains(seg, "-wt-") {
			return true
		}
		if i+1 < len(segs) && (seg == ".claude" || seg == ".ralph" || seg == ".git") && segs[i+1] == "worktrees" {
			return true
		}
	}
	return false
}

func ShouldSkipDir(name string) bool {
	if IgnoredDirNames[name] {
		return true
	}
	return strings.HasPrefix(name, "s2f_") ||
		strings.HasPrefix(name, ".wt-") ||
		strings.HasPrefix(name, "wt-") ||
		strings.HasPrefix(name, ".hg-anchor-edit-") ||
		strings.Contains(name, "-wt-")
}

func IsActiveScope(scope string) bool {
	return scope == "active" || scope == "active_operator" || scope == "active_first_party"
}

func (r *Report) add(rule, repo, severity, message string, passed bool) {
	r.Findings = append(r.Findings, Finding{
		Rule:     rule,
		Repo:     repo,
		Severity: severity,
		Message:  message,
		Passed:   passed,
	})
}

func finalizeReport(report *Report) {
	slices.Sort(report.UnlistedDiskRepos)
	report.UnlistedDiskRepos = slices.Compact(report.UnlistedDiskRepos)

	slices.Sort(report.WorktreesSkipped)
	report.WorktreesSkipped = slices.Compact(report.WorktreesSkipped)

	slices.Sort(report.BleedServers)
	report.BleedServers = slices.Compact(report.BleedServers)

	slices.SortFunc(report.Findings, func(a, b Finding) int {
		if a.Rule != b.Rule {
			return strings.Compare(a.Rule, b.Rule)
		}
		if a.Repo != b.Repo {
			return strings.Compare(a.Repo, b.Repo)
		}
		if a.Severity != b.Severity {
			return strings.Compare(a.Severity, b.Severity)
		}
		if a.Message != b.Message {
			return strings.Compare(a.Message, b.Message)
		}
		if a.Passed != b.Passed {
			if !a.Passed {
				return -1
			}
			return 1
		}
		return 0
	})
	report.Findings = slices.Compact(report.Findings)

	report.Passed = true
	for _, f := range report.Findings {
		if !f.Passed {
			report.Passed = false
			break
		}
	}
}
