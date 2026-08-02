// Package configindex inventories repository, dotfiles, and provider-home
// configuration without reading secret values into reports.
package configindex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hairglasses-studio/codexkit/workspace"
)

const (
	SchemaVersion = 1
	IndexKind     = "workspace provider configuration index"
)

// Profile identifies one provider-home identity. Profiles are deliberately
// independent: the index never assumes that credentials or trust state can be
// copied between them.
type Profile struct {
	Name string `json:"name"`
	Home string `json:"home"`
}

// Options controls configuration-index generation.
type Options struct {
	WorkspaceRoot       string
	Profiles            []Profile
	GeneratedAt         string
	IncludeRuntimeStats bool
}

// Index is the complete, redacted inventory.
type Index struct {
	SchemaVersion int             `json:"schema_version"`
	GeneratedAt   string          `json:"generated_at"`
	GeneratedBy   string          `json:"generated_by"`
	WorkspaceRoot string          `json:"workspace_root"`
	IndexKind     string          `json:"index_kind"`
	Summary       Summary         `json:"summary"`
	Repos         []Repo          `json:"repos"`
	Profiles      []Profile       `json:"profiles"`
	Files         []File          `json:"files"`
	Runtime       []RuntimeBucket `json:"runtime_buckets,omitempty"`
	Duplicates    []Duplicate     `json:"duplicates,omitempty"`
}

// Summary captures physical and logical counts separately.
type Summary struct {
	ManifestRepos     int            `json:"manifest_repos"`
	ExistingRepos     int            `json:"existing_repos"`
	UnmanifestedRepos int            `json:"unmanifested_repos"`
	PhysicalFiles     int            `json:"physical_files"`
	LogicalSources    int            `json:"logical_sources"`
	RuntimeBuckets    int            `json:"runtime_buckets"`
	DuplicateGroups   int            `json:"duplicate_groups"`
	ByProvider        map[string]int `json:"by_provider"`
	ByKind            map[string]int `json:"by_kind"`
	ByClassification  map[string]int `json:"by_classification"`
	ByScope           map[string]int `json:"by_scope"`
}

// Repo records the manifest relationship for a repository root.
type Repo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Lifecycle  string `json:"lifecycle,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Manifested bool   `json:"manifested"`
	Exists     bool   `json:"exists"`
}

// File is one provider-relevant physical file or symlink. Content hashes are
// emitted only for repository-controlled, non-sensitive files.
type File struct {
	ID             string `json:"id"`
	Path           string `json:"path"`
	Scope          string `json:"scope"`
	Profile        string `json:"profile,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Provider       string `json:"provider"`
	Kind           string `json:"kind"`
	Classification string `json:"classification"`
	Sensitivity    string `json:"sensitivity"`
	Format         string `json:"format"`
	Lifecycle      string `json:"lifecycle,omitempty"`
	Tracked        bool   `json:"tracked"`
	Symlink        bool   `json:"symlink"`
	SymlinkTarget  string `json:"symlink_target,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Size           int64  `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

// RuntimeBucket summarizes a mutable directory without exposing its contents.
type RuntimeBucket struct {
	Profile string `json:"profile"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Files   int64  `json:"files,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Counted bool   `json:"counted"`
}

// Duplicate groups repository-controlled identical files.
type Duplicate struct {
	SHA256 string   `json:"sha256"`
	Paths  []string `json:"paths"`
}

// Finding is one policy result.
type Finding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// CheckReport validates the strict Claude/Codex/AGY configuration contract.
type CheckReport struct {
	Passed   bool      `json:"passed"`
	Index    Index     `json:"index"`
	Findings []Finding `json:"findings"`
}

// Build builds a redacted configuration inventory.
func Build(opts Options) (Index, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return Index{}, err
	}
	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return Index{}, fmt.Errorf("load workspace manifest: %w", err)
	}
	index := Index{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   generatedAt,
		GeneratedBy:   "codexkit workspace config-index",
		WorkspaceRoot: root,
		IndexKind:     IndexKind,
		Profiles:      normalizeProfiles(opts.Profiles),
	}

	manifested := make(map[string]struct{}, len(manifest.Repos))
	seenPaths := make(map[string]struct{})
	for _, repo := range manifest.Repos {
		manifested[repo.Name] = struct{}{}
		repoRoot := filepath.Join(root, repo.Name)
		exists := isGitRepo(repoRoot)
		index.Repos = append(index.Repos, Repo{
			Name: repo.Name, Path: repoRoot, Lifecycle: repo.Lifecycle,
			Scope: repo.Scope, Manifested: true, Exists: exists,
		})
		if !exists {
			continue
		}
		files, scanErr := scanRepo(root, repoRoot, repo.Name, repo.Lifecycle, true)
		if scanErr != nil {
			return Index{}, scanErr
		}
		appendUnique(&index.Files, seenPaths, files)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return Index{}, fmt.Errorf("read workspace root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := manifested[entry.Name()]; ok {
			continue
		}
		repoRoot := filepath.Join(root, entry.Name())
		if !isGitRepo(repoRoot) {
			continue
		}
		index.Repos = append(index.Repos, Repo{
			Name: entry.Name(), Path: repoRoot, Lifecycle: "unmanifested",
			Scope: "inventory_only", Manifested: false, Exists: true,
		})
		files, scanErr := scanRepo(root, repoRoot, entry.Name(), "unmanifested", false)
		if scanErr != nil {
			return Index{}, scanErr
		}
		appendUnique(&index.Files, seenPaths, files)
	}

	workspaceFiles, err := scanWorkspaceRoot(root)
	if err != nil {
		return Index{}, err
	}
	appendUnique(&index.Files, seenPaths, workspaceFiles)

	for _, profile := range index.Profiles {
		files, runtime, scanErr := scanProfile(profile, opts.IncludeRuntimeStats)
		if scanErr != nil {
			return Index{}, scanErr
		}
		appendUnique(&index.Files, seenPaths, files)
		index.Runtime = append(index.Runtime, runtime...)
	}

	sort.Slice(index.Repos, func(i, j int) bool { return index.Repos[i].Name < index.Repos[j].Name })
	sort.Slice(index.Files, func(i, j int) bool { return index.Files[i].ID < index.Files[j].ID })
	sort.Slice(index.Runtime, func(i, j int) bool {
		if index.Runtime[i].Profile != index.Runtime[j].Profile {
			return index.Runtime[i].Profile < index.Runtime[j].Profile
		}
		return index.Runtime[i].Path < index.Runtime[j].Path
	})
	index.Duplicates = findDuplicates(index.Files)
	index.Summary = summarize(index, len(manifest.Repos))
	return index, nil
}

// Check evaluates strict-provider, autonomy-default, and ownership policy.
func Check(opts Options) (CheckReport, error) {
	index, err := Build(opts)
	if err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{Index: index, Passed: true}
	seenObsolete := map[string]struct{}{}
	repoRoots := make(map[string]string, len(index.Repos))
	for _, repo := range index.Repos {
		repoRoots[repo.Name] = repo.Path
	}
	for _, file := range index.Files {
		if file.Classification == "unknown" {
			report.add("owned_configuration", false, file.Path, "provider-relevant file has no management owner")
		}
		if file.Classification == "obsolete" && isActiveObsoleteProjection(file.Path) && (file.Scope == "workspace" || file.Scope == "repo" || file.Scope == "user" || file.Scope == "root") {
			root := obsoleteProjectionRoot(file, repoRoots)
			if _, ok := seenObsolete[root]; !ok {
				seenObsolete[root] = struct{}{}
				report.add("strict_provider_set", false, root, "active Gemini CLI, Copilot, or Cline projection must be retired or migrated to AGY")
			}
		}
		if file.Scope == "user" && file.Provider == "codex" && filepath.Base(file.Path) == "config.toml" && file.Classification != "secret_authentication" {
			if !autonomousCodexConfig(file.Path) {
				report.add("codex_autonomy_default", false, file.Path, "Codex config must default to danger-full-access with approval_policy=never")
			}
		}
		if file.Scope == "user" && file.Provider == "claude" && strings.HasSuffix(filepath.ToSlash(file.Path), "/.claude/settings.json") && !autonomousClaudeConfig(file.Path) {
			report.add("claude_autonomy_default", false, file.Path, "Claude settings must default to bypassPermissions and suppress the dangerous-mode prompt")
		}
		if file.Scope == "user" && file.Provider == "agy" && strings.HasSuffix(filepath.ToSlash(file.Path), "/.gemini/antigravity-cli/settings.json") && !autonomousAGYConfig(file.Path) {
			report.add("agy_autonomy_default", false, file.Path, "AGY settings must default to accept-edits, always-proceed, and non-workspace access")
		}
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/scripts/hg-agent-home-sync.sh") && hasUnscopedDelete(file.Path) {
			report.add("safe_home_sync", false, file.Path, "provider-home sync contains unscoped rsync --delete")
		}
	}
	if len(report.Findings) == 0 {
		report.add("configuration_contract", true, "", "all provider configuration files are owned and satisfy the strict-provider autonomy policy")
	}
	return report, nil
}

func (r *CheckReport) add(check string, passed bool, path, message string) {
	r.Findings = append(r.Findings, Finding{Check: check, Passed: passed, Path: path, Message: message})
	if !passed {
		r.Passed = false
	}
}

func normalizeProfiles(profiles []Profile) []Profile {
	seen := map[string]struct{}{}
	var out []Profile
	for _, profile := range profiles {
		if profile.Name == "" || profile.Home == "" {
			continue
		}
		home, err := filepath.Abs(filepath.Clean(profile.Home))
		if err != nil {
			continue
		}
		key := profile.Name + "\x00" + home
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Profile{Name: profile.Name, Home: home})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func scanRepo(workspaceRoot, repoRoot, repoName, lifecycle string, manifested bool) ([]File, error) {
	tracked, err := gitPathList(repoRoot, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("list tracked files in %s: %w", repoName, err)
	}
	var files []File
	for _, rel := range tracked {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if !providerRelevant(rel, path, repoName == "ralphglasses") {
			continue
		}
		files = append(files, buildFile(path, "repo", "", repoName, lifecycle, true))
	}

	status, statusErr := gitPathList(repoRoot, "ls-files", "-z", "--others", "--exclude-standard")
	if statusErr == nil {
		for _, rel := range status {
			path := filepath.Join(repoRoot, filepath.FromSlash(rel))
			if providerRelevant(rel, path, repoName == "ralphglasses") {
				files = append(files, buildFile(path, "repo", "", repoName, lifecycle, false))
			}
		}
	}
	_ = workspaceRoot
	_ = manifested
	return files, nil
}

func scanWorkspaceRoot(root string) ([]File, error) {
	var files []File
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".mcp.json"} {
		path := filepath.Join(root, name)
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			files = append(files, buildFile(path, "workspace", "", "", "active", false))
		}
	}
	for _, name := range []string{".agents", ".claude", ".codex", ".gemini", ".cline"} {
		base := filepath.Join(root, name)
		if _, err := os.Lstat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrPermission) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				if path != base && isRuntimeSegment(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, buildFile(path, "workspace", "", "", "active", false))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func scanProfile(profile Profile, includeStats bool) ([]File, []RuntimeBucket, error) {
	var files []File
	var runtime []RuntimeBucket
	for _, relRoot := range []string{".agents", ".claude", ".codex", ".gemini", ".cline"} {
		base := filepath.Join(profile.Home, relRoot)
		if _, err := os.Lstat(base); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if errors.Is(err, fs.ErrPermission) {
				runtime = append(runtime, RuntimeBucket{Profile: profile.Name, Path: base, Kind: "profile_unreadable", Counted: false})
				continue
			}
			return nil, nil, err
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrPermission) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				if path != base && isProfileRuntimeDir(profile.Home, path) {
					bucket := RuntimeBucket{Profile: profile.Name, Path: path, Kind: "runtime_history_cache"}
					if includeStats {
						bucket.Files, bucket.Bytes = directoryStats(path)
						bucket.Counted = true
					}
					runtime = append(runtime, bucket)
					return filepath.SkipDir
				}
				return nil
			}
			scope := "user"
			if profile.Name == "root" || profile.Home == "/root" {
				scope = "root"
			}
			files = append(files, buildFile(path, scope, profile.Name, "", "active", false))
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	claudeState := filepath.Join(profile.Home, ".claude.json")
	if _, err := os.Lstat(claudeState); err == nil {
		scope := "user"
		if profile.Name == "root" || profile.Home == "/root" {
			scope = "root"
		}
		files = append(files, buildFile(claudeState, scope, profile.Name, "", "active", false))
	}
	return files, runtime, nil
}

func providerRelevant(rel, path string, dotfilesOwner bool) bool {
	rel = filepath.ToSlash(rel)
	base := strings.ToLower(filepath.Base(rel))
	if base == "agents.md" || base == "claude.md" || base == "gemini.md" || base == ".mcp.json" {
		return true
	}
	for _, segment := range strings.Split(rel, "/") {
		switch segment {
		case ".agents", ".claude", ".codex", ".gemini", ".agy", ".cline":
			return true
		}
	}
	lower := strings.ToLower(rel)
	if strings.HasPrefix(lower, ".github/agents/") || strings.HasPrefix(lower, ".github/skills/") || lower == ".github/copilot-instructions.md" {
		return true
	}
	if !dotfilesOwner || !strings.HasPrefix(rel, "dotfiles/") {
		return false
	}
	for _, token := range []string{"claude", "codex", "agy", "antigravity", "gemini", "agent-home", "workspace-global", "provider-home"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 2<<20 || !looksTextPath(path) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return false
	}
	lowerContent := bytes.ToLower(data)
	for _, token := range [][]byte{[]byte("claude"), []byte("codex"), []byte("antigravity"), []byte("agy")} {
		if bytes.Contains(lowerContent, token) {
			return true
		}
	}
	return false
}

func buildFile(path, scope, profile, repo, lifecycle string, tracked bool) File {
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	file := File{
		ID:             stableID(scope, profile, repo, clean),
		Path:           clean,
		Scope:          scope,
		Profile:        profile,
		Repo:           repo,
		Provider:       providerForPath(clean),
		Kind:           kindForPath(clean),
		Classification: classificationForPath(clean, scope, tracked, lifecycle),
		Sensitivity:    sensitivityForPath(clean, scope),
		Format:         formatForPath(clean),
		Lifecycle:      lifecycle,
		Tracked:        tracked,
	}
	if err != nil {
		return file
	}
	file.Mode = info.Mode().String()
	file.Size = info.Size()
	if info.Mode()&os.ModeSymlink != 0 {
		file.Symlink = true
		file.SymlinkTarget, _ = os.Readlink(clean)
		return file
	}
	if file.Sensitivity == "none" && (scope == "repo" || scope == "workspace") && info.Mode().IsRegular() {
		if data, readErr := os.ReadFile(clean); readErr == nil {
			sum := sha256.Sum256(data)
			file.SHA256 = hex.EncodeToString(sum[:])
		}
	}
	return file
}

func providerForPath(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(lower))
	if strings.Contains(lower, "/.agents/") || base == "agents.md" || base == ".mcp.json" {
		return "shared"
	}
	if strings.Contains(lower, "copilot") {
		return "copilot"
	}
	if strings.Contains(lower, "/.cline/") {
		return "cline"
	}
	if strings.Contains(lower, "/.gemini/") || strings.Contains(lower, "antigravity") || strings.Contains(base, "agy") || base == "gemini.md" {
		if strings.Contains(lower, "/antigravity-cli/") || strings.Contains(lower, "/.gemini/config/") || strings.Contains(lower, "antigravity") || strings.Contains(base, "agy") {
			return "agy"
		}
		return "gemini"
	}
	if strings.Contains(lower, "/.claude") || strings.Contains(base, "claude") {
		return "claude"
	}
	if strings.Contains(lower, "/.codex") || strings.Contains(base, "codex") {
		return "codex"
	}
	return "shared"
}

func kindForPath(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(lower))
	switch {
	case base == "agents.md" || base == "claude.md" || base == "gemini.md":
		return "instructions"
	case strings.Contains(lower, "/skills/") || base == "skill.md" || base == "surface.yaml":
		return "skill"
	case strings.Contains(lower, "/agents/"):
		return "agent"
	case strings.Contains(lower, "/rules/") || strings.HasSuffix(base, ".rules"):
		return "rule"
	case strings.Contains(lower, "/hooks/") || strings.Contains(base, "hook"):
		return "hook"
	case strings.Contains(lower, "/plugins/") || strings.Contains(lower, "plugin.json"):
		return "plugin"
	case base == ".mcp.json" || base == "mcp_config.json" || strings.Contains(base, "mcp"):
		return "mcp"
	case strings.Contains(lower, "/commands/"):
		return "command"
	case strings.Contains(base, "launch") || strings.Contains(lower, "/.local/bin/"):
		return "launcher"
	case strings.Contains(lower, "/systemd/") || strings.HasSuffix(base, ".service") || strings.HasSuffix(base, ".timer"):
		return "service"
	default:
		return "config"
	}
}

func classificationForPath(path, scope string, tracked bool, lifecycle string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(lower))
	if strings.Contains(lower, "/imported/") || strings.Contains(lower, "/archives/") || strings.Contains(lower, "/vault/") || strings.Contains(lower, "/third_party/") || strings.Contains(lower, "/vendor/") || lifecycle == "archived" {
		return "archived_provenance"
	}
	if isSecretPath(lower) {
		return "secret_authentication"
	}
	if (scope == "user" || scope == "root") && isAGYHomePath(lower) {
		if strings.HasSuffix(lower, "/.gemini/antigravity-cli/settings.json") || strings.Contains(lower, "/.gemini/config/") {
			return "managed_overlay"
		}
		return "user_owned_mutable"
	}
	if strings.Contains(lower, "/examples/") || strings.Contains(lower, "/testdata/") || strings.Contains(lower, "/fixtures/") {
		return "test_fixture"
	}
	if strings.Contains(lower, "copilot") || strings.Contains(lower, "/.cline/") || strings.Contains(lower, "/.github/agents/") || strings.Contains(lower, "/.github/skills/") || base == "gemini.md" || isLegacyGeminiPath(lower) {
		return "obsolete"
	}
	if scope == "workspace" && (base == "agents.md" || base == "claude.md" || base == ".mcp.json") {
		return "canonical_declarative"
	}
	if scope == "repo" || scope == "workspace" {
		if strings.Contains(lower, "/.claude/skills/") || strings.Contains(lower, "/.claude/agents/") || strings.Contains(lower, "/.codex/agents/") {
			return "generated_managed"
		}
		if strings.HasSuffix(lower, "/.claude/settings.json") || strings.HasSuffix(lower, "/.codex/config.toml") || strings.HasSuffix(lower, "/.agents/hooks.json") {
			return "managed_overlay"
		}
		if scope == "workspace" && (strings.Contains(lower, "/.agents/") || strings.Contains(lower, "/.claude/") || strings.Contains(lower, "/.codex/")) {
			return "canonical_declarative"
		}
		if tracked {
			return "canonical_declarative"
		}
		return "unknown"
	}
	if isProfileRuntimePath(lower) {
		return "runtime_history_cache"
	}
	if base == ".claude.json" {
		return "user_owned_mutable"
	}
	if strings.Contains(lower, "/.claude/") || strings.Contains(lower, "/.codex/") || strings.Contains(lower, "/.agents/") || strings.Contains(lower, "/.gemini/antigravity-cli/settings.json") || strings.Contains(lower, "/.gemini/config/") {
		return "managed_overlay"
	}
	return "unknown"
}

func sensitivityForPath(path, scope string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	if isSecretPath(lower) {
		return "secret"
	}
	if scope == "user" || scope == "root" {
		return "private"
	}
	return "none"
}

func isSecretPath(lower string) bool {
	base := strings.ToLower(filepath.Base(lower))
	for _, token := range []string{"credential", "auth.json", "api-key", "api_key", "anthropic-api-key", "control.key", "oauth", "token", "cookie", "secret"} {
		if strings.Contains(base, token) {
			return true
		}
	}
	return false
}

func formatForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonl"):
		return "json"
	case strings.HasSuffix(base, ".toml"):
		return "toml"
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		return "yaml"
	case strings.HasSuffix(base, ".md"):
		return "markdown"
	case strings.HasSuffix(base, ".sh"):
		return "shell"
	case strings.HasSuffix(base, ".service") || strings.HasSuffix(base, ".timer"):
		return "systemd"
	default:
		return "other"
	}
}

func isProfileRuntimeDir(home, path string) bool {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, prefix := range []string{
		".agents/memory", ".agents/sessions", ".agents/worktrees", ".agents/logs", ".agents/cache", ".agents/tmp",
		".claude/file-history", ".claude/projects", ".claude/sessions", ".claude/shell-snapshots", ".claude/session-env", ".claude/history", ".claude/cache", ".claude/paste-cache", ".claude/plans", ".claude/jobs", ".claude/backups", ".claude/agent-memory", ".claude/debug", ".claude/telemetry", ".claude/todos",
		".claude/tasks", ".claude/teams", ".claude/plugins/cache", ".claude/plugins/marketplaces", ".claude/plugins/repos",
		".codex/sessions", ".codex/archived_sessions", ".codex/logs", ".codex/log", ".codex/attachments", ".codex/shell_snapshots", ".codex/memories", ".codex/worktrees", ".codex/cache", ".codex/tmp", ".codex/.tmp", ".codex/plugins/cache",
		".gemini/antigravity-cli/bin", ".gemini/antigravity-cli/brain", ".gemini/antigravity-cli/builtin", ".gemini/antigravity-cli/cache", ".gemini/antigravity-cli/conversations", ".gemini/antigravity-cli/crashes", ".gemini/antigravity-cli/implicit", ".gemini/antigravity-cli/log", ".gemini/antigravity-cli/logs", ".gemini/antigravity-cli/sidecar_data", ".gemini/antigravity-cli/updater", ".gemini/antigravity/bin", ".gemini/antigravity/brain", ".gemini/antigravity/cache", ".gemini/antigravity/daemon", ".gemini/antigravity/implicit", ".gemini/antigravity/knowledge", ".gemini/antigravity/sidecar_data", ".gemini/antigravity/skills_library", ".gemini/antigravity/tool_cache", ".gemini/antigravity/workflow_library", ".gemini/worktrees", ".gemini/history", ".gemini/tmp",
		".cline/data", ".cline/cache", ".cline/logs", ".cline/tasks", ".cline/sessions",
	} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

func isProfileRuntimePath(lower string) bool {
	for _, segment := range []string{"/history.jsonl", "/sessions/", "/conversations/", "/logs/", "/cache/", "/file-history/", "/attachments/", "/shell_snapshots/", "/sidecar_data/", "/backups/"} {
		if strings.Contains(lower, segment) {
			return true
		}
	}
	return false
}

func isRuntimeSegment(name string) bool {
	for _, prefix := range []string{"explorer_", "worker_", "reviewer_", "teamwork_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "sessions", "history", "logs", "cache", "tmp", "worktrees", "conversations", "sidecar_data", "file-history", "attachments", "backups", "memory", "brain", "implicit", "tasks", "teams":
		return true
	default:
		return false
	}
}

func directoryStats(root string) (files, bytesCount int64) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		files++
		if info, infoErr := entry.Info(); infoErr == nil {
			bytesCount += info.Size()
		}
		return nil
	})
	return files, bytesCount
}

func gitPathList(repoRoot string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	cmd.Stdin = nil
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	var paths []string
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		paths = append(paths, filepath.ToSlash(string(raw)))
	}
	return paths, nil
}

func isGitRepo(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".git"))
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return false
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	cmd.Stdin = nil
	output, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

func appendUnique(dst *[]File, seen map[string]struct{}, files []File) {
	for _, file := range files {
		key := filepath.Clean(file.Path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		*dst = append(*dst, file)
	}
}

func stableID(scope, profile, repo, path string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{scope, profile, repo, filepath.Clean(path)}, "\x00")))
	return hex.EncodeToString(sum[:12])
}

func findDuplicates(files []File) []Duplicate {
	byHash := map[string][]string{}
	for _, file := range files {
		if file.SHA256 == "" || file.Classification == "archived_provenance" {
			continue
		}
		byHash[file.SHA256] = append(byHash[file.SHA256], file.Path)
	}
	var out []Duplicate
	for hash, paths := range byHash {
		if len(paths) < 2 {
			continue
		}
		sort.Strings(paths)
		out = append(out, Duplicate{SHA256: hash, Paths: paths})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SHA256 < out[j].SHA256 })
	return out
}

func summarize(index Index, manifestRepos int) Summary {
	summary := Summary{
		ManifestRepos:   manifestRepos,
		PhysicalFiles:   len(index.Files),
		RuntimeBuckets:  len(index.Runtime),
		DuplicateGroups: len(index.Duplicates),
		ByProvider:      map[string]int{}, ByKind: map[string]int{},
		ByClassification: map[string]int{}, ByScope: map[string]int{},
	}
	for _, repo := range index.Repos {
		if repo.Exists {
			summary.ExistingRepos++
		}
		if !repo.Manifested {
			summary.UnmanifestedRepos++
		}
	}
	for _, file := range index.Files {
		summary.ByProvider[file.Provider]++
		summary.ByKind[file.Kind]++
		summary.ByClassification[file.Classification]++
		summary.ByScope[file.Scope]++
		if file.Classification == "canonical_declarative" {
			summary.LogicalSources++
		}
	}
	return summary
}

func autonomousCodexConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, `sandbox_mode = "danger-full-access"`) && strings.Contains(lower, `approval_policy = "never"`)
}

func autonomousClaudeConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		Permissions struct {
			DefaultMode                       string `json:"defaultMode"`
			SkipDangerousModePermissionPrompt bool   `json:"skipDangerousModePermissionPrompt"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	return settings.Permissions.DefaultMode == "bypassPermissions" && settings.Permissions.SkipDangerousModePermissionPrompt
}

func autonomousAGYConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		AgentMode               string `json:"agentMode"`
		AllowNonWorkspaceAccess bool   `json:"allowNonWorkspaceAccess"`
		ArtifactReviewPolicy    string `json:"artifactReviewPolicy"`
		ToolPermission          string `json:"toolPermission"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	return settings.AgentMode == "accept-edits" && settings.AllowNonWorkspaceAccess &&
		settings.ArtifactReviewPolicy == "always-proceed" && settings.ToolPermission == "always-proceed"
}

func hasUnscopedDelete(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(data)
	if !strings.Contains(text, "rsync") || (!strings.Contains(text, "--delete") && !strings.Contains(text, "-rlptD --delete")) {
		return false
	}
	guardedDirectorySync := strings.Contains(text, `local source_dir="$1"`) &&
		strings.Contains(text, `local target_dir="$2"`) &&
		strings.Contains(text, `"$source_dir/" "$target_dir/"`)
	return !guardedDirectorySync
}

// isActiveObsoleteProjection distinguishes retired provider surfaces from
// historical source, tests, and migration ledgers that intentionally mention
// them. Strict checks fail only when an executable or discoverable alias still
// exists at a provider-native path.
func isActiveObsoleteProjection(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	base := strings.ToLower(filepath.Base(lower))
	if base == "gemini.md" || base == "copilot-instructions.md" {
		return true
	}
	for _, segment := range []string{
		"/.cline/",
		"/.github/agents/",
		"/.github/skills/",
		"/.gemini/agents/",
		"/.gemini/commands/",
		"/.gemini/extensions/",
	} {
		if strings.Contains(lower, segment) {
			return true
		}
	}
	for _, name := range []string{
		"hg-gemini-launch.sh",
		"hg-copilot-launch.sh",
		"symlink_gemini.tmpl",
		"symlink_hg-gemini-launch.sh.tmpl",
		"symlink_copilot.tmpl",
		"symlink_hg-copilot-launch.sh.tmpl",
	} {
		if base == name {
			return true
		}
	}
	return false
}

// obsoleteProjectionRoot returns the smallest actionable provider-native root
// so a generated tree with thousands of files produces one policy finding.
func obsoleteProjectionRoot(file File, repoRoots map[string]string) string {
	if file.Scope == "repo" && file.Repo != "" {
		if root := repoRoots[file.Repo]; root != "" {
			return filepath.Clean(root)
		}
	}
	clean := filepath.ToSlash(filepath.Clean(file.Path))
	lower := strings.ToLower(clean)
	for _, marker := range []string{
		"/.cline/",
		"/.github/agents/",
		"/.github/skills/",
		"/.gemini/agents/",
		"/.gemini/commands/",
		"/.gemini/extensions/",
	} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return filepath.Clean(clean[:idx+len(marker)-1])
		}
	}
	return filepath.Clean(file.Path)
}

func isLegacyGeminiPath(lower string) bool {
	marker := "/.gemini/"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return false
	}
	rel := lower[idx+len(marker):]
	if strings.HasPrefix(rel, "antigravity-cli/") || strings.HasPrefix(rel, "antigravity/") || strings.HasPrefix(rel, "config/") {
		return false
	}
	return true
}

func isAGYHomePath(lower string) bool {
	return strings.Contains(lower, "/.gemini/antigravity-cli/") ||
		strings.Contains(lower, "/.gemini/antigravity/") ||
		strings.Contains(lower, "/.gemini/config/")
}

func looksTextPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, suffix := range []string{".sh", ".zsh", ".bash", ".toml", ".json", ".yaml", ".yml", ".md", ".service", ".timer", ".tmpl", ".conf"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return base == "makefile" || base == "install.sh"
}

// Write writes deterministic JSON and Markdown artifacts.
func Write(index Index, jsonPath, markdownPath string) error {
	if jsonPath != "" {
		data, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile(jsonPath, append(data, '\n')); err != nil {
			return err
		}
	}
	if markdownPath != "" {
		if err := writeFile(markdownPath, []byte(RenderMarkdown(index))); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RenderMarkdown renders a human-readable, value-redacted catalog.
func RenderMarkdown(index Index) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Provider Configuration Index\n\nGenerated: %s\nWorkspace: `%s`\n\n", index.GeneratedAt, index.WorkspaceRoot)
	fmt.Fprintf(&out, "## Summary\n\n- Manifest repositories: %d\n- Existing repositories: %d\n- Unmanifested repositories: %d\n- Physical files: %d\n- Canonical logical sources: %d\n- Runtime buckets: %d\n- Duplicate groups: %d\n\n",
		index.Summary.ManifestRepos, index.Summary.ExistingRepos, index.Summary.UnmanifestedRepos,
		index.Summary.PhysicalFiles, index.Summary.LogicalSources, index.Summary.RuntimeBuckets, index.Summary.DuplicateGroups)
	out.WriteString("## Files\n\n| Provider | Scope | Class | Kind | Path |\n|---|---|---|---|---|\n")
	for _, file := range index.Files {
		fmt.Fprintf(&out, "| %s | %s | %s | %s | `%s` |\n", file.Provider, file.Scope, file.Classification, file.Kind, strings.ReplaceAll(file.Path, "|", "\\|"))
	}
	if len(index.Runtime) > 0 {
		out.WriteString("\n## Runtime Buckets\n\n| Profile | Path | Counted | Files | Bytes |\n|---|---|---:|---:|---:|\n")
		for _, bucket := range index.Runtime {
			fmt.Fprintf(&out, "| %s | `%s` | %t | %d | %d |\n", bucket.Profile, bucket.Path, bucket.Counted, bucket.Files, bucket.Bytes)
		}
	}
	return out.String()
}
