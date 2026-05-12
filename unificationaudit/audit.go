// Package unificationaudit measures the remaining codebase surfaces that are
// most likely to benefit from consolidation.
package unificationaudit

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/reporeadiness"
	"github.com/hairglasses-studio/codexkit/workspace"
)

const (
	GeneratedBy          = "codexkit unification audit"
	IndexKind            = "workspace codebase unification audit"
	RoadmapFreshnessDays = 90
	smallWrapperMaxLines = 120
)

var ignoredDirNames = map[string]bool{
	".cache":        true,
	".git":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".terraform":    true,
	".tools":        true,
	".venv":         true,
	"__pycache__":   true,
	"bin":           true,
	"build":         true,
	"coverage":      true,
	"dist":          true,
	"htmlcov":       true,
	"node_modules":  true,
	"target":        true,
	"vendor":        true,
	"venv":          true,
}

// Options controls audit scope.
type Options struct {
	WorkspaceRoot string
	AllScopes     bool
	GeneratedAt   string
}

// Report is the full codebase unification audit.
type Report struct {
	GeneratedAt     string                `json:"generated_at"`
	GeneratedBy     string                `json:"generated_by"`
	WorkspaceRoot   string                `json:"workspace_root"`
	IndexKind       string                `json:"index_kind"`
	Summary         Summary               `json:"summary"`
	Repos           []RepoReport          `json:"repos"`
	ExtraRoadmaps   []RoadmapFile         `json:"extra_roadmaps,omitempty"`
	Readiness       *reporeadiness.Report `json:"readiness,omitempty"`
	Recommendations []Recommendation      `json:"recommendations,omitempty"`
	Warnings        []string              `json:"warnings,omitempty"`
}

// Summary aggregates workspace-wide consolidation signals.
type Summary struct {
	Scope                          string         `json:"scope"`
	ReposScanned                   int            `json:"repos_scanned"`
	TotalShellFiles                int            `json:"total_shell_files"`
	TotalShellLines                int            `json:"total_shell_lines"`
	MigratableShellFiles           int            `json:"migratable_shell_files"`
	MigratableShellLines           int            `json:"migratable_shell_lines"`
	StatefulShellFiles             int            `json:"stateful_shell_files"`
	StatefulShellLines             int            `json:"stateful_shell_lines"`
	StructuredParserShellFiles     int            `json:"structured_parser_shell_files"`
	StructuredParserShellLines     int            `json:"structured_parser_shell_lines"`
	OSBootstrapShellFiles          int            `json:"os_bootstrap_shell_files"`
	OSBootstrapShellLines          int            `json:"os_bootstrap_shell_lines"`
	HookFiles                      int            `json:"hook_files"`
	HookLines                      int            `json:"hook_lines"`
	ReposWithHookFiles             int            `json:"repos_with_hook_files"`
	CanonicalSkillFiles            int            `json:"canonical_skill_files"`
	GeneratedSkillFiles            int            `json:"generated_skill_files"`
	PluginSkillFiles               int            `json:"plugin_skill_files"`
	SurfaceFiles                   int            `json:"surface_files"`
	PythonFastPathCommands         int            `json:"python_fast_path_commands"`
	RepoRoadmaps                   int            `json:"repo_roadmaps"`
	ReposMissingRoadmap            int            `json:"repos_missing_roadmap"`
	StaleRoadmaps                  int            `json:"stale_roadmaps"`
	ExtraRoadmaps                  int            `json:"extra_roadmaps"`
	BaselineTargetRepos            int            `json:"baseline_target_repos"`
	BaselineCheckedRepos           int            `json:"baseline_checked_repos"`
	BaselinePassingRepos           int            `json:"baseline_passing_repos"`
	BaselineFailingRepos           int            `json:"baseline_failing_repos"`
	BaselineFailedFindings         int            `json:"baseline_failed_findings"`
	ReposWithSkillSources          int            `json:"repos_with_skill_sources"`
	ReposWithGeneratedSkillMirrors int            `json:"repos_with_generated_skill_mirrors"`
	ReposMissingSkillSurface       int            `json:"repos_missing_skill_surface"`
	SourceSkillDirsMissingEntry    int            `json:"source_skill_dirs_missing_entry"`
	GeneratedMirrorsWithoutSource  int            `json:"generated_mirrors_without_source"`
	MutationReadinessLanes         map[string]int `json:"mutation_readiness_lanes,omitempty"`
	ShellRoles                     map[string]int `json:"shell_roles,omitempty"`
	TopRepos                       []RepoPriority `json:"top_repos,omitempty"`
}

// RepoPriority is a compact ranked repo summary for the report header.
type RepoPriority struct {
	Repo                 string `json:"repo"`
	Score                int    `json:"score"`
	MigratableShellLines int    `json:"migratable_shell_lines"`
	HookFiles            int    `json:"hook_files"`
	SkillSurfaceFinding  string `json:"skill_surface_finding,omitempty"`
	PrimaryFinding       string `json:"primary_finding,omitempty"`
}

// RepoReport captures consolidation signals for one repo.
type RepoReport struct {
	RepoName       string `json:"repo_name"`
	RepoPath       string `json:"repo_path"`
	Scope          string `json:"scope,omitempty"`
	Category       string `json:"category,omitempty"`
	Language       string `json:"language,omitempty"`
	Lifecycle      string `json:"lifecycle,omitempty"`
	BaselineTarget bool   `json:"baseline_target,omitempty"`

	ShellFiles            int                  `json:"shell_files"`
	ShellLines            int                  `json:"shell_lines"`
	MigratableShellFiles  int                  `json:"migratable_shell_files"`
	MigratableShellLines  int                  `json:"migratable_shell_lines"`
	StatefulShellFiles    int                  `json:"stateful_shell_files"`
	StatefulShellLines    int                  `json:"stateful_shell_lines"`
	StructuredParserFiles int                  `json:"structured_parser_files"`
	StructuredParserLines int                  `json:"structured_parser_lines"`
	OSBootstrapFiles      int                  `json:"os_bootstrap_files"`
	OSBootstrapLines      int                  `json:"os_bootstrap_lines"`
	ShellByRole           map[string]ShellRole `json:"shell_by_role,omitempty"`
	LargestShellFiles     []ShellFile          `json:"largest_shell_files,omitempty"`

	HookFiles []FileMetric `json:"hook_files,omitempty"`
	HookLines int          `json:"hook_lines"`

	CanonicalSkillFiles           int               `json:"canonical_skill_files"`
	GeneratedSkillFiles           int               `json:"generated_skill_files"`
	PluginSkillFiles              int               `json:"plugin_skill_files"`
	OtherSkillFiles               int               `json:"other_skill_files"`
	SurfaceFiles                  int               `json:"surface_files"`
	SourceSkillDirsMissingEntry   []string          `json:"source_skill_dirs_missing_entry,omitempty"`
	SkillSurfacePresent           bool              `json:"skill_surface_present"`
	GeneratedMirrorsWithoutSource bool              `json:"generated_mirrors_without_source,omitempty"`
	PythonFastPathCommands        []FastPathCommand `json:"python_fast_path_commands,omitempty"`
	Roadmap                       *RoadmapFile      `json:"roadmap,omitempty"`
	Baseline                      *BaselineStatus   `json:"baseline,omitempty"`

	Findings []Finding `json:"findings,omitempty"`
	Score    int       `json:"score"`
}

// BaselineStatus is a compact codexkit baseline check result for manifest
// baseline_target repos.
type BaselineStatus struct {
	Passed   bool              `json:"passed"`
	Total    int               `json:"total"`
	Failed   int               `json:"failed"`
	Findings []BaselineFinding `json:"findings,omitempty"`
}

// BaselineFinding is one failing codexkit baseline check.
type BaselineFinding struct {
	Check       string                      `json:"check"`
	Message     string                      `json:"message,omitempty"`
	Remediation []baselineguard.Remediation `json:"remediation,omitempty"`
}

// RoadmapFile describes one repo-local roadmap discovered during an audit.
type RoadmapFile struct {
	RepoName  string `json:"repo_name"`
	Path      string `json:"path"`
	UpdatedAt string `json:"updated_at,omitempty"`
	AgeDays   int    `json:"age_days"`
	Bytes     int64  `json:"bytes"`
	Stale     bool   `json:"stale,omitempty"`
}

// FastPathCommand describes one legacy command path delegated to a canonical
// language-native command surface before shell libraries are loaded.
type FastPathCommand struct {
	LegacyPath string `json:"legacy_path"`
	TargetPath string `json:"target_path"`
}

// ShellRole aggregates shell files assigned to one migration role.
type ShellRole struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
}

// ShellFile captures one shell file and its migration classification.
type ShellFile struct {
	Path        string   `json:"path"`
	Lines       int      `json:"lines"`
	Bytes       int      `json:"bytes"`
	PrimaryRole string   `json:"primary_role"`
	Reasons     []string `json:"reasons,omitempty"`
}

// FileMetric captures one counted file.
type FileMetric struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	Bytes int    `json:"bytes"`
}

// Finding captures one repo-level observation.
type Finding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// Recommendation is one prioritized next step.
type Recommendation struct {
	Rank     int      `json:"rank"`
	Severity string   `json:"severity"`
	Area     string   `json:"area"`
	Repo     string   `json:"repo,omitempty"`
	Message  string   `json:"message"`
	Command  string   `json:"command,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type repoInput struct {
	Name           string
	Path           string
	Scope          string
	Category       string
	Language       string
	Lifecycle      string
	BaselineTarget bool
}

// Audit scans the workspace for remaining unification candidates.
func Audit(root string, opts Options) (Report, error) {
	if root == "" {
		root = opts.WorkspaceRoot
	}
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return Report{}, err
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("workspace root is not a directory: %s", root)
	}

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	auditTime := generatedTime(generatedAt)
	report := Report{
		GeneratedAt:   generatedAt,
		GeneratedBy:   GeneratedBy,
		WorkspaceRoot: root,
		IndexKind:     IndexKind,
	}

	repos, warnings, err := selectRepos(root, opts)
	if err != nil {
		return Report{}, err
	}
	report.Warnings = append(report.Warnings, warnings...)
	report.Repos = make([]RepoReport, 0, len(repos))
	for _, repo := range repos {
		repoReport, repoWarnings := auditRepo(repo, auditTime)
		report.Warnings = append(report.Warnings, repoWarnings...)
		report.Repos = append(report.Repos, repoReport)
	}
	report.ExtraRoadmaps = discoverExtraRoadmaps(root, repos, auditTime)

	sort.Slice(report.Repos, func(i, j int) bool {
		if report.Repos[i].Score == report.Repos[j].Score {
			return report.Repos[i].RepoName < report.Repos[j].RepoName
		}
		return report.Repos[i].Score > report.Repos[j].Score
	})
	report.Summary = summarize(report.Repos, scopeLabel(opts))
	report.Summary.ExtraRoadmaps = len(report.ExtraRoadmaps)
	if readiness, readinessErr := reporeadiness.Score(root, reporeadiness.Options{
		AllScopes:   opts.AllScopes,
		GeneratedAt: generatedAt,
	}); readinessErr != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("repo readiness scoring failed: %v", readinessErr))
	} else {
		report.Readiness = &readiness
		report.Summary.MutationReadinessLanes = readiness.Summary.Lanes
	}
	report.Recommendations = buildRecommendations(report)
	sort.Strings(report.Warnings)
	return report, nil
}

func selectRepos(root string, opts Options) ([]repoInput, []string, error) {
	manifest, err := workspace.LoadManifest(root)
	if err == nil {
		repos := make([]repoInput, 0, len(manifest.Repos))
		for _, repo := range manifest.Repos {
			if !opts.AllScopes && repo.Scope != "active_operator" && repo.Scope != "active_first_party" {
				continue
			}
			repoPath := filepath.Join(root, repo.Name)
			if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
				continue
			}
			repos = append(repos, repoInput{
				Name:           repo.Name,
				Path:           repoPath,
				Scope:          repo.Scope,
				Category:       repo.Category,
				Language:       repo.Language,
				Lifecycle:      repo.Lifecycle,
				BaselineTarget: repo.BaselineTarget,
			})
		}
		return repos, nil, nil
	}

	discovered, discoverErr := discoverGitRepos(root)
	if discoverErr != nil {
		return nil, nil, discoverErr
	}
	warnings := []string{fmt.Sprintf("workspace manifest not loaded (%v); using top-level git repo discovery", err)}
	return discovered, warnings, nil
}

func discoverGitRepos(root string) ([]repoInput, error) {
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		return []repoInput{{
			Name:      filepath.Base(root),
			Path:      root,
			Scope:     "discovered",
			Lifecycle: "unknown",
		}}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	repos := make([]repoInput, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(root, entry.Name())
		if info, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil && info.IsDir() {
			repos = append(repos, repoInput{
				Name:      entry.Name(),
				Path:      repoPath,
				Scope:     "discovered",
				Lifecycle: "unknown",
			})
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

func auditRepo(repo repoInput, now time.Time) (RepoReport, []string) {
	report := RepoReport{
		RepoName:       repo.Name,
		RepoPath:       repo.Path,
		Scope:          repo.Scope,
		Category:       repo.Category,
		Language:       repo.Language,
		Lifecycle:      repo.Lifecycle,
		BaselineTarget: repo.BaselineTarget,
		ShellByRole:    map[string]ShellRole{},
	}
	warnings := []string{}
	sourceSkillDirs := map[string]bool{}
	sourceSkillEntries := map[string]bool{}

	err := filepath.WalkDir(repo.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", repo.Name, err))
			return nil
		}
		rel, relErr := filepath.Rel(repo.Path, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if entry.IsDir() {
			if shouldSkipDir(rel, entry.Name()) {
				return filepath.SkipDir
			}
			if skillName, ok := rootSourceSkillDir(rel); ok {
				sourceSkillDirs[skillName] = true
			}
			return nil
		}

		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}

		var data []byte
		loaded := false
		readData := func() []byte {
			if loaded {
				return data
			}
			loaded = true
			data, _ = os.ReadFile(path)
			return data
		}

		if isSkillEntry(rel) {
			switch skillEntryKind(rel) {
			case "canonical":
				report.CanonicalSkillFiles++
				if skillName, ok := rootSourceSkillEntry(rel); ok {
					sourceSkillEntries[skillName] = true
				}
			case "generated":
				report.GeneratedSkillFiles++
			case "plugin":
				report.PluginSkillFiles++
			default:
				report.OtherSkillFiles++
			}
		}
		if isSurfaceFile(rel) {
			report.SurfaceFiles++
			report.SkillSurfacePresent = true
		}
		if isHookFile(rel) {
			hookData := readData()
			metric := FileMetric{Path: rel, Lines: countLines(hookData), Bytes: len(hookData)}
			report.HookFiles = append(report.HookFiles, metric)
			report.HookLines += metric.Lines
		}
		if likelyShellFile(rel, info.Mode()) {
			shellData := readData()
			if isShellFile(shellData, rel) {
				addShellFile(&report, rel, shellData)
			}
		}
		return nil
	})
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s: %v", repo.Name, err))
	}

	for name := range sourceSkillDirs {
		if !sourceSkillEntries[name] {
			report.SourceSkillDirsMissingEntry = append(report.SourceSkillDirsMissingEntry, ".agents/skills/"+name)
		}
	}
	sort.Strings(report.SourceSkillDirsMissingEntry)
	sort.Slice(report.HookFiles, func(i, j int) bool {
		if report.HookFiles[i].Lines == report.HookFiles[j].Lines {
			return report.HookFiles[i].Path < report.HookFiles[j].Path
		}
		return report.HookFiles[i].Lines > report.HookFiles[j].Lines
	})
	sort.Slice(report.LargestShellFiles, func(i, j int) bool {
		if report.LargestShellFiles[i].Lines == report.LargestShellFiles[j].Lines {
			return report.LargestShellFiles[i].Path < report.LargestShellFiles[j].Path
		}
		return report.LargestShellFiles[i].Lines > report.LargestShellFiles[j].Lines
	})
	if len(report.LargestShellFiles) > 12 {
		report.LargestShellFiles = report.LargestShellFiles[:12]
	}
	report.GeneratedMirrorsWithoutSource = report.GeneratedSkillFiles > 0 && report.CanonicalSkillFiles == 0
	report.PythonFastPathCommands = readPythonFastPaths(repo.Path)
	report.Roadmap = readRoadmap(repo.Name, repo.Path, now)
	if repo.BaselineTarget {
		report.Baseline = runBaselineStatus(repo.Path)
	}
	report.Findings = repoFindings(report)
	report.Score = scoreRepo(report)
	return report, warnings
}

func runBaselineStatus(repoPath string) *BaselineStatus {
	baseline := baselineguard.Check(repoPath)
	status := &BaselineStatus{
		Passed: baseline.Passed,
		Total:  baseline.Total,
		Failed: baseline.Failed,
	}
	for _, finding := range baseline.Findings {
		if finding.Passed {
			continue
		}
		status.Findings = append(status.Findings, BaselineFinding{
			Check:       finding.Check,
			Message:     finding.Message,
			Remediation: finding.Remediation,
		})
	}
	return status
}

func readRoadmap(repoName, repoPath string, now time.Time) *RoadmapFile {
	for _, rel := range []string{"ROADMAP.md", "docs/ROADMAP.md"} {
		path := filepath.Join(repoPath, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		updated := info.ModTime().UTC()
		ageDays := int(now.Sub(updated).Hours() / 24)
		if ageDays < 0 {
			ageDays = 0
		}
		return &RoadmapFile{
			RepoName:  repoName,
			Path:      rel,
			UpdatedAt: updated.Format("2006-01-02"),
			AgeDays:   ageDays,
			Bytes:     info.Size(),
			Stale:     ageDays > RoadmapFreshnessDays,
		}
	}
	return nil
}

func discoverExtraRoadmaps(root string, repos []repoInput, now time.Time) []RoadmapFile {
	selected := make(map[string]bool, len(repos))
	for _, repo := range repos {
		selected[repo.Name] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var roadmaps []RoadmapFile
	for _, entry := range entries {
		if !entry.IsDir() || selected[entry.Name()] || ignoredDirNames[entry.Name()] {
			continue
		}
		repoPath := filepath.Join(root, entry.Name())
		roadmap := readRoadmap(entry.Name(), repoPath, now)
		if roadmap != nil {
			roadmaps = append(roadmaps, *roadmap)
		}
	}
	sort.Slice(roadmaps, func(i, j int) bool { return roadmaps[i].RepoName < roadmaps[j].RepoName })
	return roadmaps
}

func generatedTime(value string) time.Time {
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts.UTC()
	}
	return time.Now().UTC()
}

func addShellFile(report *RepoReport, rel string, data []byte) {
	role, reasons := classifyShell(rel, data)
	lines := countLines(data)
	shellFile := ShellFile{
		Path:        rel,
		Lines:       lines,
		Bytes:       len(data),
		PrimaryRole: role,
		Reasons:     reasons,
	}
	report.LargestShellFiles = append(report.LargestShellFiles, shellFile)
	report.ShellFiles++
	report.ShellLines += lines
	roleSummary := report.ShellByRole[role]
	roleSummary.Files++
	roleSummary.Lines += lines
	report.ShellByRole[role] = roleSummary

	switch role {
	case "stateful_workflow":
		report.StatefulShellFiles++
		report.StatefulShellLines += lines
		report.MigratableShellFiles++
		report.MigratableShellLines += lines
	case "structured_parser":
		report.StructuredParserFiles++
		report.StructuredParserLines += lines
		report.MigratableShellFiles++
		report.MigratableShellLines += lines
	case "duplicate_implementation":
		report.MigratableShellFiles++
		report.MigratableShellLines += lines
	case "os_bootstrap":
		report.OSBootstrapFiles++
		report.OSBootstrapLines += lines
	}
}

func shouldSkipDir(rel, name string) bool {
	if ignoredDirNames[name] {
		return true
	}
	if strings.HasPrefix(name, "s2f_") {
		return true
	}
	return strings.HasPrefix(rel, ".claude/worktrees/") ||
		strings.HasPrefix(rel, ".ralph/worktrees/") ||
		strings.HasPrefix(rel, ".ralph/probe/") ||
		strings.HasPrefix(rel, "agent-parity/") ||
		strings.HasPrefix(rel, "docs/agent-parity/") ||
		strings.Contains(rel, "/.claude/worktrees/") ||
		strings.Contains(rel, "/.ralph/worktrees/") ||
		strings.Contains(rel, "/.ralph/probe/")
}

func rootSourceSkillDir(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == ".agents" && parts[1] == "skills" && parts[2] != "" {
		return parts[2], true
	}
	return "", false
}

func rootSourceSkillEntry(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) == 4 && parts[0] == ".agents" && parts[1] == "skills" && parts[3] == "SKILL.md" {
		return parts[2], true
	}
	return "", false
}

func isSkillEntry(rel string) bool {
	return strings.HasSuffix(rel, "/SKILL.md")
}

func skillEntryKind(rel string) string {
	switch {
	case strings.HasPrefix(rel, ".agents/skills/"):
		return "canonical"
	case strings.HasPrefix(rel, ".claude/skills/") || strings.Contains(rel, "/.claude/skills/"):
		return "generated"
	case strings.Contains(rel, "/plugins/") && strings.Contains(rel, "/skills/"):
		return "plugin"
	default:
		return "other"
	}
}

func isSurfaceFile(rel string) bool {
	return rel == ".agents/skills/surface.yaml" || rel == ".agents/skills/surface.yml"
}

func isHookFile(rel string) bool {
	return strings.HasPrefix(rel, ".claude/hooks/") ||
		strings.HasPrefix(rel, ".clinerules/hooks/") ||
		strings.Contains(rel, "/.claude/hooks/") ||
		strings.Contains(rel, "/.clinerules/hooks/")
}

func likelyShellFile(rel string, mode fs.FileMode) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".sh", ".bash", ".zsh":
		return true
	}
	return mode&0o111 != 0
}

func isShellFile(data []byte, rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".sh", ".bash", ".zsh":
		return true
	}
	firstLine := firstLine(data)
	return strings.HasPrefix(firstLine, "#!") &&
		(strings.Contains(firstLine, " sh") ||
			strings.Contains(firstLine, "/sh") ||
			strings.Contains(firstLine, "bash") ||
			strings.Contains(firstLine, "zsh"))
}

func classifyShell(rel string, data []byte) (string, []string) {
	lowerRel := strings.ToLower(rel)
	lowerBase := strings.ToLower(filepath.Base(rel))
	lowerContent := strings.ToLower(string(data))
	lines := countLines(data)
	reasons := []string{}

	if containsAny(lowerRel, "deprecated", "legacy", "archive/", "old/", "superseded") ||
		containsAny(lowerBase, "deprecated", "legacy", "old-") ||
		containsAny(lowerContent, "deprecated", "legacy implementation", "superseded by") {
		reasons = append(reasons, "legacy or superseded naming/content")
		return "duplicate_implementation", reasons
	}
	if containsAny(lowerRel, "install", "setup", "bootstrap", "provision", "systemd", "launchagent", "distro", "unraid", "opnsense") ||
		containsAny(lowerContent, "apt-get ", "apt ", "yum ", "dnf ", "pacman ", "brew install", "systemctl ", "launchctl ", "useradd ", "groupadd ") {
		reasons = append(reasons, "OS, package manager, or service bootstrap")
		return "os_bootstrap", reasons
	}
	if containsAny(lowerRel, "testdata/", "fixtures/", "/test/", "/tests/") || strings.HasPrefix(lowerBase, "test-") {
		reasons = append(reasons, "test or fixture support")
		return "test_or_fixture", reasons
	}
	if strings.HasPrefix(lowerRel, "scripts/hg-") || strings.HasPrefix(lowerRel, "scripts/hg-modules/") {
		reasons = append(reasons, "workspace operator command surface")
		return "os_bootstrap", reasons
	}
	if (strings.HasPrefix(lowerRel, "lib/") || strings.Contains(lowerRel, "/lib/")) &&
		countShellFunctionDefs(data) >= 8 &&
		!containsAny(lowerContent, "while true", "nohup ", "systemd-run", "\nwait\n", " &\n") {
		reasons = append(reasons, "function-dense shell library module")
		return "structured_parser", reasons
	}
	if lines <= smallWrapperMaxLines && containsAny(lowerContent,
		"exec ", "go run", "go test", "uv run", "python -m", "npm ", "npx ", "pnpm ", "yarn ",
		"cargo ", "docker compose", "./cmd/", "cmd/",
	) {
		reasons = append(reasons, "small command wrapper")
		return "wrapper", reasons
	}
	if containsAny(lowerContent,
		"sqlite3", "psql ", "mysql ", "redis-cli", "flock ", "trap ", "mktemp", "jsonl",
		"queue", " state ", "state_dir", ".state", "state/", "systemd-run", "docker ", "docker-compose", "rclone ", "rsync ",
		"aws ", "gcloud ", "kubectl ", "terraform ", "git push", "git commit", "while true",
		"retry", "nohup ", ">>", " tee -a", " mv ", " cp ", " rm ", " mkdir ", " touch ",
	) || strings.Contains(lowerContent, "\nwait\n") || strings.Contains(lowerContent, " &\n") {
		reasons = append(reasons, "stateful workflow, side effects, retries, or external orchestration")
		return "stateful_workflow", reasons
	}
	if containsAny(lowerContent,
		" jq ", "\njq ", "|jq ", " yq ", "\nyq ", "|yq ", " awk ", "\nawk ", " sed ", "\nsed ",
		"python -", "python3 -", "node -e", "ruby -e", "perl -", "go run", ".json", ".yaml", ".yml", ".toml",
	) {
		reasons = append(reasons, "structured parsing or embedded interpreter logic")
		return "structured_parser", reasons
	}
	return "unknown", reasons
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func firstLine(data []byte) string {
	idx := bytes.IndexByte(data, '\n')
	if idx == -1 {
		return string(data)
	}
	return string(data[:idx])
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func countShellFunctionDefs(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte("() {"))
	count += bytes.Count(data, []byte("\nfunction "))
	return count
}

func repoFindings(report RepoReport) []Finding {
	findings := make([]Finding, 0, 8)
	if report.MigratableShellLines >= 300 {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "shell_migration",
			Message:  fmt.Sprintf("%d migratable shell LOC across %d files", report.MigratableShellLines, report.MigratableShellFiles),
		})
	} else if report.MigratableShellLines > 0 {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "shell_migration",
			Message:  fmt.Sprintf("%d migratable shell LOC across %d files", report.MigratableShellLines, report.MigratableShellFiles),
		})
	}
	if len(report.HookFiles) > 0 {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "hook_surface",
			Message:  fmt.Sprintf("%d hook files should share one runner/validation contract", len(report.HookFiles)),
		})
	}
	if report.CanonicalSkillFiles > 0 && !report.SkillSurfacePresent {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "skill_surface",
			Message:  "canonical skills exist without .agents/skills/surface.yaml",
		})
	}
	if len(report.SourceSkillDirsMissingEntry) > 0 {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "skill_source",
			Message:  fmt.Sprintf("%d source skill directories are missing SKILL.md", len(report.SourceSkillDirsMissingEntry)),
		})
	}
	if report.GeneratedMirrorsWithoutSource {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "generated_surface",
			Message:  "generated Claude skill mirrors exist without canonical repo skill sources",
		})
	}
	if len(report.PythonFastPathCommands) > 0 {
		findings = append(findings, Finding{
			Severity: "low",
			Category: "python_fast_path",
			Message:  fmt.Sprintf("%d legacy command paths delegate to a canonical Python CLI before shell libraries load", len(report.PythonFastPathCommands)),
		})
	}
	if report.Roadmap != nil && report.Roadmap.Stale {
		findings = append(findings, Finding{
			Severity: "low",
			Category: "roadmap_freshness",
			Message:  fmt.Sprintf("repo roadmap is %d days old", report.Roadmap.AgeDays),
		})
	}
	if report.Baseline != nil && !report.Baseline.Passed {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "baseline_contract",
			Message:  fmt.Sprintf("codexkit baseline check fails %d of %d checks", report.Baseline.Failed, report.Baseline.Total),
		})
	}
	if report.OSBootstrapLines > 250 {
		findings = append(findings, Finding{
			Severity: "low",
			Category: "bootstrap_boundary",
			Message:  fmt.Sprintf("%d OS bootstrap shell LOC should remain shell unless cross-platform logic grows", report.OSBootstrapLines),
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if severityWeight(findings[i].Severity) == severityWeight(findings[j].Severity) {
			return findings[i].Category < findings[j].Category
		}
		return severityWeight(findings[i].Severity) > severityWeight(findings[j].Severity)
	})
	return findings
}

func scoreRepo(report RepoReport) int {
	score := 0
	score += report.StatefulShellFiles * 18
	score += report.StructuredParserFiles * 12
	score += report.MigratableShellLines / 20
	score += len(report.HookFiles) * 8
	if report.CanonicalSkillFiles > 0 && !report.SkillSurfacePresent {
		score += 24
	}
	score += len(report.SourceSkillDirsMissingEntry) * 20
	if report.GeneratedMirrorsWithoutSource {
		score += 10
	}
	if report.Baseline != nil && !report.Baseline.Passed {
		score += 30 + report.Baseline.Failed
	}
	return score
}

func summarize(repos []RepoReport, scope string) Summary {
	summary := Summary{
		Scope:      scope,
		ShellRoles: map[string]int{},
	}
	for _, repo := range repos {
		summary.ReposScanned++
		summary.TotalShellFiles += repo.ShellFiles
		summary.TotalShellLines += repo.ShellLines
		summary.MigratableShellFiles += repo.MigratableShellFiles
		summary.MigratableShellLines += repo.MigratableShellLines
		summary.StatefulShellFiles += repo.StatefulShellFiles
		summary.StatefulShellLines += repo.StatefulShellLines
		summary.StructuredParserShellFiles += repo.StructuredParserFiles
		summary.StructuredParserShellLines += repo.StructuredParserLines
		summary.OSBootstrapShellFiles += repo.OSBootstrapFiles
		summary.OSBootstrapShellLines += repo.OSBootstrapLines
		summary.HookFiles += len(repo.HookFiles)
		summary.HookLines += repo.HookLines
		if len(repo.HookFiles) > 0 {
			summary.ReposWithHookFiles++
		}
		summary.CanonicalSkillFiles += repo.CanonicalSkillFiles
		summary.GeneratedSkillFiles += repo.GeneratedSkillFiles
		summary.PluginSkillFiles += repo.PluginSkillFiles
		summary.SurfaceFiles += repo.SurfaceFiles
		summary.PythonFastPathCommands += len(repo.PythonFastPathCommands)
		if repo.Roadmap != nil {
			summary.RepoRoadmaps++
			if repo.Roadmap.Stale {
				summary.StaleRoadmaps++
			}
		} else {
			summary.ReposMissingRoadmap++
		}
		if repo.BaselineTarget {
			summary.BaselineTargetRepos++
		}
		if repo.Baseline != nil {
			summary.BaselineCheckedRepos++
			summary.BaselineFailedFindings += repo.Baseline.Failed
			if repo.Baseline.Passed {
				summary.BaselinePassingRepos++
			} else {
				summary.BaselineFailingRepos++
			}
		}
		if repo.CanonicalSkillFiles > 0 {
			summary.ReposWithSkillSources++
		}
		if repo.GeneratedSkillFiles > 0 {
			summary.ReposWithGeneratedSkillMirrors++
		}
		if repo.CanonicalSkillFiles > 0 && !repo.SkillSurfacePresent {
			summary.ReposMissingSkillSurface++
		}
		summary.SourceSkillDirsMissingEntry += len(repo.SourceSkillDirsMissingEntry)
		if repo.GeneratedMirrorsWithoutSource {
			summary.GeneratedMirrorsWithoutSource++
		}
		for role, roleSummary := range repo.ShellByRole {
			summary.ShellRoles[role] += roleSummary.Files
		}
		if repo.Score > 0 {
			priority := RepoPriority{
				Repo:                 repo.RepoName,
				Score:                repo.Score,
				MigratableShellLines: repo.MigratableShellLines,
				HookFiles:            len(repo.HookFiles),
			}
			if repo.CanonicalSkillFiles > 0 && !repo.SkillSurfacePresent {
				priority.SkillSurfaceFinding = "missing surface.yaml"
			}
			if len(repo.Findings) > 0 {
				priority.PrimaryFinding = repo.Findings[0].Message
			}
			summary.TopRepos = append(summary.TopRepos, priority)
		}
	}
	sort.Slice(summary.TopRepos, func(i, j int) bool {
		if summary.TopRepos[i].Score == summary.TopRepos[j].Score {
			return summary.TopRepos[i].Repo < summary.TopRepos[j].Repo
		}
		return summary.TopRepos[i].Score > summary.TopRepos[j].Score
	})
	if len(summary.TopRepos) > 10 {
		summary.TopRepos = summary.TopRepos[:10]
	}
	if len(summary.ShellRoles) == 0 {
		summary.ShellRoles = nil
	}
	return summary
}

func buildRecommendations(report Report) []Recommendation {
	recommendations := []Recommendation{}
	add := func(severity, area, repo, message, command string, evidence []string) {
		recommendations = append(recommendations, Recommendation{
			Rank:     len(recommendations) + 1,
			Severity: severity,
			Area:     area,
			Repo:     repo,
			Message:  message,
			Command:  command,
			Evidence: evidence,
		})
	}

	if len(report.Summary.TopRepos) > 0 && report.Summary.TopRepos[0].MigratableShellLines > 0 {
		top := report.Summary.TopRepos[0]
		add(
			"high",
			"shell_migration",
			top.Repo,
			"Move the highest-scoring stateful or structured shell workflow behind the repo's canonical command surface before migrating small wrappers.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit unification report ~/hairglasses-studio --all-scopes",
			[]string{fmt.Sprintf("%s has %d migratable shell LOC", top.Repo, top.MigratableShellLines)},
		)
	}
	if report.Summary.HookFiles > 0 {
		add(
			"medium",
			"hook_surface",
			"",
			"Consolidate hook scripts under one validated runner contract and keep repo hooks as thin declarations.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit workspace primitive-index-check ~/hairglasses-studio --skip-artifacts",
			[]string{fmt.Sprintf("%d hook files across %d repos", report.Summary.HookFiles, report.Summary.ReposWithHookFiles)},
		)
	}
	if report.Summary.ReposMissingSkillSurface > 0 || report.Summary.SourceSkillDirsMissingEntry > 0 {
		add(
			"high",
			"skill_contract",
			"",
			"Normalize canonical skill sources so each source skill has SKILL.md and each repo skill source has surface.yaml.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit workspace source-contract-check ~/hairglasses-studio --skills-only",
			[]string{
				fmt.Sprintf("%d repos missing skill surfaces", report.Summary.ReposMissingSkillSurface),
				fmt.Sprintf("%d source skill dirs missing SKILL.md", report.Summary.SourceSkillDirsMissingEntry),
			},
		)
	}
	if report.Summary.GeneratedMirrorsWithoutSource > 0 {
		add(
			"medium",
			"generated_surface",
			"",
			"Replace generated-only Claude skill mirrors with canonical repo-owned skill sources or remove the stale mirrors.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit workspace surface-index ~/hairglasses-studio",
			[]string{fmt.Sprintf("%d repos have generated mirrors without canonical sources", report.Summary.GeneratedMirrorsWithoutSource)},
		)
	}
	if report.Summary.BaselineFailingRepos > 0 {
		add(
			"high",
			"baseline_contract",
			"",
			"Fix failing codexkit baseline_target repos before adding more repos to the baseline fleet.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit unification report ~/hairglasses-studio",
			[]string{
				fmt.Sprintf("%d baseline_target repos failing", report.Summary.BaselineFailingRepos),
				fmt.Sprintf("%d failed baseline findings", report.Summary.BaselineFailedFindings),
			},
		)
	}
	if report.Readiness != nil && report.Readiness.Summary.CleanupFirst > 0 {
		add(
			"high",
			"mutation_readiness",
			"",
			"Run the cleanup-first queue before autonomous roadmap work: clean worktrees, sync branches, and repair failing baselines.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit repo-readiness score ~/hairglasses-studio --all-scopes",
			[]string{fmt.Sprintf("%d repos are cleanup-first", report.Readiness.Summary.CleanupFirst)},
		)
	}
	if report.Summary.ReposMissingRoadmap > 0 || report.Summary.ExtraRoadmaps > 0 || report.Summary.StaleRoadmaps > 0 {
		add(
			"low",
			"roadmap_coverage",
			"",
			"Keep repo-local ROADMAP.md files present, fresh, and included in the workspace inventory when they become active work surfaces.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit unification report ~/hairglasses-studio",
			[]string{
				fmt.Sprintf("%d scanned repos missing roadmaps", report.Summary.ReposMissingRoadmap),
				fmt.Sprintf("%d stale scanned roadmaps", report.Summary.StaleRoadmaps),
				fmt.Sprintf("%d roadmap files outside the current scan", report.Summary.ExtraRoadmaps),
			},
		)
	}
	if report.Summary.MigratableShellLines == 0 && report.Summary.HookFiles == 0 && report.Summary.ReposMissingSkillSurface == 0 {
		add(
			"low",
			"maintenance",
			"",
			"Keep running the unification audit with source-contract checks after generated-surface or shell workflow changes.",
			"cd ~/hairglasses-studio/codexkit && GOWORK=off go run ./cmd/codexkit unification audit ~/hairglasses-studio --json",
			nil,
		)
	}
	return recommendations
}

func scopeLabel(opts Options) string {
	if opts.AllScopes {
		return "all"
	}
	return "active"
}

func severityWeight(severity string) int {
	switch severity {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

// Markdown renders a human-readable audit report.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codebase Unification Audit\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`\n\n", r.GeneratedBy)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Scope: `%s`\n", r.Summary.Scope)
	fmt.Fprintf(&b, "- Repos scanned: `%d`\n", r.Summary.ReposScanned)
	fmt.Fprintf(&b, "- Shell surface: `%d` files / `%d` LOC\n", r.Summary.TotalShellFiles, r.Summary.TotalShellLines)
	fmt.Fprintf(&b, "- Migratable shell surface: `%d` files / `%d` LOC\n", r.Summary.MigratableShellFiles, r.Summary.MigratableShellLines)
	fmt.Fprintf(&b, "- Stateful shell workflows: `%d` files / `%d` LOC\n", r.Summary.StatefulShellFiles, r.Summary.StatefulShellLines)
	fmt.Fprintf(&b, "- Structured shell parsers: `%d` files / `%d` LOC\n", r.Summary.StructuredParserShellFiles, r.Summary.StructuredParserShellLines)
	fmt.Fprintf(&b, "- OS bootstrap shell retained boundary: `%d` files / `%d` LOC\n", r.Summary.OSBootstrapShellFiles, r.Summary.OSBootstrapShellLines)
	fmt.Fprintf(&b, "- Hook files: `%d` files / `%d` LOC across `%d` repos\n", r.Summary.HookFiles, r.Summary.HookLines, r.Summary.ReposWithHookFiles)
	fmt.Fprintf(&b, "- Skills: `%d` canonical, `%d` generated, `%d` plugin, `%d` surface files\n", r.Summary.CanonicalSkillFiles, r.Summary.GeneratedSkillFiles, r.Summary.PluginSkillFiles, r.Summary.SurfaceFiles)
	fmt.Fprintf(&b, "- Python fast-path delegates: `%d` command paths\n", r.Summary.PythonFastPathCommands)
	fmt.Fprintf(&b, "- Roadmaps: `%d` present, `%d` missing, `%d` stale, `%d` outside current scan\n", r.Summary.RepoRoadmaps, r.Summary.ReposMissingRoadmap, r.Summary.StaleRoadmaps, r.Summary.ExtraRoadmaps)
	fmt.Fprintf(&b, "- Baseline targets: `%d` target repos, `%d` checked, `%d` passing, `%d` failing / `%d` failed findings\n", r.Summary.BaselineTargetRepos, r.Summary.BaselineCheckedRepos, r.Summary.BaselinePassingRepos, r.Summary.BaselineFailingRepos, r.Summary.BaselineFailedFindings)
	fmt.Fprintf(&b, "- Skill contract gaps: `%d` repos missing surface.yaml, `%d` source dirs missing SKILL.md\n", r.Summary.ReposMissingSkillSurface, r.Summary.SourceSkillDirsMissingEntry)
	if len(r.Summary.MutationReadinessLanes) > 0 {
		fmt.Fprintf(&b, "- Mutation readiness: full-auto `%d`, cleanup-first `%d`, human-approved `%d`, review-only `%d`\n",
			r.Summary.MutationReadinessLanes[reporeadiness.LaneFullAutoCandidate],
			r.Summary.MutationReadinessLanes[reporeadiness.LaneCleanupFirst],
			r.Summary.MutationReadinessLanes[reporeadiness.LaneHumanApprovedOnly],
			r.Summary.MutationReadinessLanes[reporeadiness.LaneReviewOnly],
		)
	}

	if r.Summary.RepoRoadmaps > 0 || r.Summary.ReposMissingRoadmap > 0 || len(r.ExtraRoadmaps) > 0 {
		fmt.Fprintf(&b, "\n## Roadmap Coverage\n\n")
		fmt.Fprintf(&b, "- Scanned repos: `%d` with roadmap, `%d` missing, `%d` stale.\n", r.Summary.RepoRoadmaps, r.Summary.ReposMissingRoadmap, r.Summary.StaleRoadmaps)
		if present := roadmapsForRepos(r.Repos, true, 12); len(present) > 0 {
			fmt.Fprintf(&b, "- Present: %s\n", strings.Join(present, ", "))
		}
		if missing := missingRoadmapRepos(r.Repos, 12); len(missing) > 0 {
			fmt.Fprintf(&b, "- Missing: %s\n", strings.Join(missing, ", "))
		}
		if len(r.ExtraRoadmaps) > 0 {
			fmt.Fprintf(&b, "- Outside current scan: %s\n", roadmapFileList(r.ExtraRoadmaps, 12))
		}
	}

	if r.Summary.BaselineFailingRepos > 0 {
		fmt.Fprintf(&b, "\n## Baseline Remediation Queue\n\n")
		fmt.Fprintf(&b, "| Repo | Failed Checks | Command | First Failing Checks | Remediation |\n")
		fmt.Fprintf(&b, "|---|---:|---|---|---|\n")
		for _, repo := range baselineQueue(r.Repos, 12) {
			fmt.Fprintf(&b, "| `%s` | `%d` | `%s` | %s | %s |\n",
				mdEscape(repo.RepoName),
				repo.Baseline.Failed,
				mdEscape(baselineCheckCommand(r.WorkspaceRoot, repo.RepoPath)),
				baselineFindingList(repo.Baseline.Findings, 4),
				baselineRemediationList(r.WorkspaceRoot, repo.RepoPath, repo.Baseline.Findings, 3),
			)
		}
	}

	if r.Readiness != nil && len(r.Readiness.Repos) > 0 {
		fmt.Fprintf(&b, "\n## Mutation Readiness\n\n")
		fmt.Fprintf(&b, "| Repo | Lane | Score | Git | Baseline | Action |\n")
		fmt.Fprintf(&b, "|---|---|---:|---|---|---|\n")
		for _, repo := range limitedReadinessRepos(r.Readiness.Repos, 16) {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%d` | %s | %s | %s |\n",
				mdEscape(repo.RepoName),
				mdEscape(repo.Lane),
				repo.Score,
				mdEscape(readinessGitLabel(repo.Git)),
				mdEscape(readinessBaselineLabel(repo.Baseline, repo.BaselineTarget)),
				mdEscape(repo.RecommendedAction),
			)
		}
	}

	if len(r.Summary.TopRepos) > 0 {
		fmt.Fprintf(&b, "\n## Highest-Priority Repos\n\n")
		fmt.Fprintf(&b, "| Repo | Score | Migratable shell LOC | Hooks | Primary finding |\n")
		fmt.Fprintf(&b, "|---|---:|---:|---:|---|\n")
		for _, repo := range r.Summary.TopRepos {
			fmt.Fprintf(&b, "| `%s` | `%d` | `%d` | `%d` | %s |\n",
				mdEscape(repo.Repo),
				repo.Score,
				repo.MigratableShellLines,
				repo.HookFiles,
				mdEscape(emptyLabel(repo.PrimaryFinding)),
			)
		}
	}

	if len(r.Recommendations) > 0 {
		fmt.Fprintf(&b, "\n## Recommended Steps\n\n")
		for _, rec := range r.Recommendations {
			target := rec.Area
			if rec.Repo != "" {
				target = rec.Area + " / " + rec.Repo
			}
			fmt.Fprintf(&b, "%d. `%s` `%s`: %s\n", rec.Rank, rec.Severity, mdEscape(target), rec.Message)
			if rec.Command != "" {
				fmt.Fprintf(&b, "   Command: `%s`\n", mdEscape(rec.Command))
			}
		}
	}

	fmt.Fprintf(&b, "\n## Repo Detail\n\n")
	for _, repo := range r.Repos {
		if repo.Score == 0 {
			continue
		}
		fmt.Fprintf(&b, "### `%s`\n\n", mdEscape(repo.RepoName))
		fmt.Fprintf(&b, "- Score: `%d`; scope: `%s`; language: `%s`\n", repo.Score, mdEscape(emptyLabel(repo.Scope)), mdEscape(emptyLabel(repo.Language)))
		fmt.Fprintf(&b, "- Shell: `%d` files / `%d` LOC; migratable: `%d` files / `%d` LOC\n", repo.ShellFiles, repo.ShellLines, repo.MigratableShellFiles, repo.MigratableShellLines)
		fmt.Fprintf(&b, "- Hooks: `%d` files / `%d` LOC\n", len(repo.HookFiles), repo.HookLines)
		fmt.Fprintf(&b, "- Skills: `%d` canonical, `%d` generated, `%d` plugin, `%d` surfaces\n", repo.CanonicalSkillFiles, repo.GeneratedSkillFiles, repo.PluginSkillFiles, repo.SurfaceFiles)
		if repo.Roadmap != nil {
			fmt.Fprintf(&b, "- Roadmap: `%s` updated `%s` (%d days old)\n", mdEscape(repo.Roadmap.Path), mdEscape(repo.Roadmap.UpdatedAt), repo.Roadmap.AgeDays)
		} else {
			fmt.Fprintf(&b, "- Roadmap: missing\n")
		}
		if repo.Baseline != nil {
			fmt.Fprintf(&b, "- Baseline: %s\n", baselineLabel(repo.Baseline))
		}
		if len(repo.PythonFastPathCommands) > 0 {
			fmt.Fprintf(&b, "- Python fast paths: %s\n", fastPathList(repo.PythonFastPathCommands, 8))
		}
		for _, finding := range repo.Findings {
			fmt.Fprintf(&b, "- `%s` `%s`: %s\n", finding.Severity, finding.Category, finding.Message)
		}
		if len(repo.LargestShellFiles) > 0 {
			fmt.Fprintf(&b, "- Largest shell files: %s\n", shellFileList(repo.LargestShellFiles, 4))
		}
		fmt.Fprintf(&b, "\n")
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(&b, "## Warnings\n\n")
		for _, warning := range r.Warnings {
			fmt.Fprintf(&b, "- %s\n", mdEscape(warning))
		}
	}
	return b.String()
}

func roadmapsForRepos(repos []RepoReport, presentOnly bool, limit int) []string {
	parts := make([]string, 0, limit)
	for _, repo := range repos {
		if len(parts) >= limit {
			break
		}
		if presentOnly && repo.Roadmap == nil {
			continue
		}
		if repo.Roadmap == nil {
			parts = append(parts, fmt.Sprintf("`%s`", mdEscape(repo.RepoName)))
			continue
		}
		stale := ""
		if repo.Roadmap.Stale {
			stale = ", stale"
		}
		parts = append(parts, fmt.Sprintf("`%s/%s` (%d days%s)", mdEscape(repo.RepoName), mdEscape(repo.Roadmap.Path), repo.Roadmap.AgeDays, stale))
	}
	if presentOnly && countPresentRoadmaps(repos) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", countPresentRoadmaps(repos)-limit))
	}
	return parts
}

func missingRoadmapRepos(repos []RepoReport, limit int) []string {
	parts := make([]string, 0, limit)
	total := 0
	for _, repo := range repos {
		if repo.Roadmap != nil {
			continue
		}
		total++
		if len(parts) < limit {
			parts = append(parts, fmt.Sprintf("`%s`", mdEscape(repo.RepoName)))
		}
	}
	if total > limit {
		parts = append(parts, fmt.Sprintf("and %d more", total-limit))
	}
	return parts
}

func countPresentRoadmaps(repos []RepoReport) int {
	total := 0
	for _, repo := range repos {
		if repo.Roadmap != nil {
			total++
		}
	}
	return total
}

func roadmapFileList(files []RoadmapFile, limit int) string {
	parts := make([]string, 0, limit)
	for i, file := range files {
		if i >= limit {
			break
		}
		stale := ""
		if file.Stale {
			stale = ", stale"
		}
		parts = append(parts, fmt.Sprintf("`%s/%s` (%d days%s)", mdEscape(file.RepoName), mdEscape(file.Path), file.AgeDays, stale))
	}
	if len(files) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(files)-limit))
	}
	return strings.Join(parts, ", ")
}

func shellFileList(files []ShellFile, limit int) string {
	parts := make([]string, 0, limit)
	for i, file := range files {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("`%s` (%d LOC, %s)", mdEscape(file.Path), file.Lines, mdEscape(file.PrimaryRole)))
	}
	return strings.Join(parts, ", ")
}

func baselineQueue(repos []RepoReport, limit int) []RepoReport {
	queue := make([]RepoReport, 0, len(repos))
	for _, repo := range repos {
		if repo.Baseline == nil || repo.Baseline.Passed {
			continue
		}
		queue = append(queue, repo)
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].Baseline.Failed == queue[j].Baseline.Failed {
			if queue[i].Score == queue[j].Score {
				return queue[i].RepoName < queue[j].RepoName
			}
			return queue[i].Score > queue[j].Score
		}
		return queue[i].Baseline.Failed > queue[j].Baseline.Failed
	})
	if len(queue) > limit {
		return queue[:limit]
	}
	return queue
}

func baselineFindingList(findings []BaselineFinding, limit int) string {
	if len(findings) == 0 {
		return "none"
	}
	parts := make([]string, 0, limit)
	for i, finding := range findings {
		if i >= limit {
			break
		}
		label := finding.Check
		if finding.Message != "" {
			label += ": " + finding.Message
		}
		parts = append(parts, fmt.Sprintf("`%s`", mdEscape(label)))
	}
	if len(findings) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(findings)-limit))
	}
	return strings.Join(parts, ", ")
}

func baselineRemediationList(
	workspaceRoot string,
	repoPath string,
	findings []BaselineFinding,
	limit int,
) string {
	if len(findings) == 0 {
		return "none"
	}
	parts := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, finding := range findings {
		for _, remediation := range finding.Remediation {
			label := remediation.Message
			if len(remediation.Command) > 0 {
				label = fmt.Sprintf(
					"%s: `%s`",
					remediation.Message,
					mdEscape(baselineRemediationCommand(workspaceRoot, repoPath, remediation.Command)),
				)
			}
			if label == "" {
				continue
			}
			if _, ok := seen[label]; ok {
				continue
			}
			seen[label] = struct{}{}
			parts = append(parts, label)
			if len(parts) >= limit {
				break
			}
		}
		if len(parts) >= limit {
			break
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "<br>")
}

func baselineRemediationCommand(workspaceRoot string, repoPath string, command []string) string {
	if len(command) == 0 {
		return ""
	}
	if command[0] == "codexkit" {
		prefix := baselineCheckCommand(workspaceRoot, repoPath)
		prefix = strings.TrimSuffix(prefix, " baseline check "+repoPath)
		return strings.TrimSpace(prefix + " " + strings.Join(command[1:], " "))
	}
	return strings.Join(command, " ")
}

func baselineCheckCommand(workspaceRoot, repoPath string) string {
	if workspaceRoot == "" {
		return fmt.Sprintf("GOWORK=off go run ./cmd/codexkit baseline check %s", repoPath)
	}
	return fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit baseline check %s", workspaceRoot, repoPath)
}

func fastPathList(commands []FastPathCommand, limit int) string {
	parts := make([]string, 0, limit)
	for i, command := range commands {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("`%s` -> `%s`", mdEscape(command.LegacyPath), mdEscape(command.TargetPath)))
	}
	if len(commands) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(commands)-limit))
	}
	return strings.Join(parts, ", ")
}

func baselineLabel(status *BaselineStatus) string {
	if status.Passed {
		return fmt.Sprintf("pass (%d checks)", status.Total)
	}
	parts := make([]string, 0, len(status.Findings))
	for i, finding := range status.Findings {
		if i >= 4 {
			break
		}
		label := finding.Check
		if finding.Message != "" {
			label += ": " + finding.Message
		}
		parts = append(parts, fmt.Sprintf("`%s`", mdEscape(label)))
	}
	if len(status.Findings) > 4 {
		parts = append(parts, fmt.Sprintf("and %d more", len(status.Findings)-4))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("fail (%d/%d checks)", status.Failed, status.Total)
	}
	return fmt.Sprintf("fail (%d/%d checks): %s", status.Failed, status.Total, strings.Join(parts, ", "))
}

func limitedReadinessRepos(repos []reporeadiness.RepoScore, limit int) []reporeadiness.RepoScore {
	if len(repos) <= limit {
		return repos
	}
	return repos[:limit]
}

func readinessGitLabel(status reporeadiness.GitStatus) string {
	if status.Error != "" {
		return status.Error
	}
	branch := status.Branch
	if branch == "" {
		branch = "detached"
	}
	parts := []string{branch}
	if status.DirtyFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", status.DirtyFiles))
	} else {
		parts = append(parts, "clean")
	}
	if status.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d ahead", status.Ahead))
	}
	if status.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", status.Behind))
	}
	return strings.Join(parts, ", ")
}

func readinessBaselineLabel(status *reporeadiness.BaselineStatus, target bool) string {
	if !target {
		return "not target"
	}
	if status == nil {
		return "not checked"
	}
	if status.Passed {
		return fmt.Sprintf("pass (%d)", status.Total)
	}
	return fmt.Sprintf("fail (%d/%d)", status.Failed, status.Total)
}

func readPythonFastPaths(repoPath string) []FastPathCommand {
	path := filepath.Join(repoPath, "config", "python_fast_paths.tsv")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var commands []FastPathCommand
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		legacyPath, targetPath, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		legacyPath = strings.TrimSpace(legacyPath)
		targetPath = strings.TrimSpace(targetPath)
		if legacyPath == "" || targetPath == "" {
			continue
		}
		commands = append(commands, FastPathCommand{
			LegacyPath: legacyPath,
			TargetPath: targetPath,
		})
	}
	return commands
}

func emptyLabel(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
