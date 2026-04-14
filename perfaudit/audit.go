// Package perfaudit scans the workspace for Codex performance bottlenecks.
//
// The current implementation is intentionally static and read-only. It looks
// for architectural smells that are already known to hurt interactive latency:
// large duplicated repo-local configs, shell-wrapped MCP launchers, `go run`
// cold starts, missing discovery scoping, oversized hot-path skills/prompts,
// and missing benchmark/regression infrastructure.
package perfaudit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/workspace"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	softEntryTokenBudget    = 2500
	hardEntryTokenBudget    = 4000
	configBloatLineBudget   = 120
	configNoMCPLineBudget   = 80
	topRepoCount            = 10
	maxRepoFindingsInReport = 4
)

var ignoredDirNames = map[string]bool{
	".git":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"venv_test":     true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".ruff_cache":   true,
	"htmlcov":       true,
	"_salvage":      true,
	"bin":           true,
	"build":         true,
	"dist":          true,
}

var shellCommands = map[string]bool{
	"bash":      true,
	"sh":        true,
	"zsh":       true,
	"/bin/bash": true,
	"/bin/sh":   true,
	"/bin/zsh":  true,
}

// EntryMetric captures the size of one hot-path skill or prompt entry file.
type EntryMetric struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"estimated_tokens"`
}

// Finding is one prioritized performance observation for a repo or fleet.
type Finding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// RootSummary captures the shared workspace bootstrap surface outside repo-local state.
type RootSummary struct {
	RootPath           string `json:"root_path"`
	RootMCPServerCount int    `json:"root_mcp_server_count"`
}

// RepoReport captures static performance metrics and bottlenecks for one repo.
type RepoReport struct {
	RepoName  string `json:"repo_name"`
	RepoPath  string `json:"repo_path"`
	Scope     string `json:"scope"`
	Category  string `json:"category"`
	Language  string `json:"language"`
	Lifecycle string `json:"lifecycle"`

	CodexConfigBytes         int      `json:"codex_config_bytes"`
	CodexConfigLines         int      `json:"codex_config_lines"`
	CodexProfileCount        int      `json:"codex_profile_count"`
	CodexMCPServerCount      int      `json:"codex_mcp_server_count"`
	RootMCPServerCount       int      `json:"root_mcp_server_count"`
	MissingEnabledTools      []string `json:"missing_enabled_tools,omitempty"`
	ShellWrappedCodexServers []string `json:"shell_wrapped_codex_servers,omitempty"`
	GoRunCodexServers        []string `json:"go_run_codex_servers,omitempty"`
	LauncherScripts          []string `json:"launcher_scripts,omitempty"`
	GoRunLauncherScripts     []string `json:"go_run_launcher_scripts,omitempty"`
	GeneratedMCPBlocks       bool     `json:"generated_mcp_blocks"`

	SkillCount       int           `json:"skill_count"`
	SkillEntryBytes  int           `json:"skill_entry_bytes"`
	SkillEntryTokens int           `json:"skill_entry_estimated_tokens"`
	OversizedSkills  []EntryMetric `json:"oversized_skills,omitempty"`

	RuntimePromptCount      int           `json:"runtime_prompt_count"`
	RuntimePromptBytes      int           `json:"runtime_prompt_bytes"`
	RuntimePromptTokens     int           `json:"runtime_prompt_estimated_tokens"`
	ArchivePromptCount      int           `json:"archive_prompt_count"`
	ArchivePromptBytes      int           `json:"archive_prompt_bytes"`
	CommandPromptCount      int           `json:"command_prompt_count"`
	CommandPromptBytes      int           `json:"command_prompt_bytes"`
	OversizedRuntimePrompts []EntryMetric `json:"oversized_runtime_prompts,omitempty"`

	BenchmarkTestFileCount int  `json:"benchmark_test_file_count"`
	PerfWorkflow           bool `json:"perf_workflow"`
	BaselineArtifactCount  int  `json:"baseline_artifact_count"`

	Score    int       `json:"score"`
	Findings []Finding `json:"findings,omitempty"`
}

// Summary aggregates fleet-wide static bottleneck counts.
type Summary struct {
	Scope                        string       `json:"scope"`
	ReposScanned                 int          `json:"repos_scanned"`
	TotalCodexMCPServers         int          `json:"total_codex_mcp_servers"`
	ReposWithGoRunLaunchers      int          `json:"repos_with_go_run_launchers"`
	ReposWithShellWrappedServers int          `json:"repos_with_shell_wrapped_servers"`
	ReposMissingEnabledTools     int          `json:"repos_missing_enabled_tools"`
	ReposWithConfigBloat         int          `json:"repos_with_config_bloat"`
	ReposWithOversizedSkills     int          `json:"repos_with_oversized_skills"`
	ReposWithOversizedPrompts    int          `json:"repos_with_oversized_prompts"`
	ReposWithoutPerfHarness      int          `json:"repos_without_perf_harness"`
	DocsArchivePromptCount       int          `json:"docs_archive_prompt_count"`
	DocsArchivePromptTokens      int          `json:"docs_archive_prompt_estimated_tokens"`
	TopFindings                  []Finding    `json:"top_findings,omitempty"`
	TopRepos                     []RepoReport `json:"top_repos,omitempty"`
}

// Options controls workspace selection.
type Options struct {
	AllScopes bool
}

// Report is the full Codex performance audit.
type Report struct {
	GeneratedAt string       `json:"generated_at"`
	Root        RootSummary  `json:"root"`
	Summary     Summary      `json:"summary"`
	Repos       []RepoReport `json:"repos"`
}

// Audit scans the workspace for current Codex performance bottlenecks.
func Audit(root string, opts Options) Report {
	report := Report{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Root:        readRootSummary(root),
	}

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		report.Summary.Scope = selectedScopeLabel(opts)
		return report
	}

	repos := selectedRepos(manifest, opts)
	report.Summary.Scope = selectedScopeLabel(opts)
	report.Repos = make([]RepoReport, 0, len(repos))
	for _, repo := range repos {
		repoPath := filepath.Join(root, repo.Name)
		if _, err := os.Stat(repoPath); err != nil {
			continue
		}
		repoReport := auditRepo(repoPath, repo)
		report.Repos = append(report.Repos, repoReport)
	}

	sort.Slice(report.Repos, func(i, j int) bool {
		if report.Repos[i].Score == report.Repos[j].Score {
			return report.Repos[i].RepoName < report.Repos[j].RepoName
		}
		return report.Repos[i].Score > report.Repos[j].Score
	})

	report.Summary = buildSummary(report.Root, report.Repos, selectedScopeLabel(opts))
	return report
}

func selectedRepos(manifest workspace.Manifest, opts Options) []workspace.Repo {
	if opts.AllScopes {
		return manifest.Repos
	}
	repos := make([]workspace.Repo, 0, len(manifest.Repos))
	for _, repo := range manifest.Repos {
		if repo.Scope == "active_operator" || repo.Scope == "active_first_party" {
			repos = append(repos, repo)
		}
	}
	return repos
}

func selectedScopeLabel(opts Options) string {
	if opts.AllScopes {
		return "all"
	}
	return "active"
}

func readRootSummary(root string) RootSummary {
	return RootSummary{
		RootPath:           root,
		RootMCPServerCount: countMCPServers(filepath.Join(root, ".mcp.json")),
	}
}

func auditRepo(repoPath string, repo workspace.Repo) RepoReport {
	report := RepoReport{
		RepoName:  repo.Name,
		RepoPath:  repoPath,
		Scope:     repo.Scope,
		Category:  repo.Category,
		Language:  repo.Language,
		Lifecycle: repo.Lifecycle,
	}

	readCodexConfig(repoPath, &report)
	report.RootMCPServerCount = countMCPServers(filepath.Join(repoPath, ".mcp.json"))
	readLauncherScripts(repoPath, &report)
	readSurfaceFiles(repoPath, repo.Name, &report)
	report.Findings = repoFindings(report)
	report.Score = scoreRepo(report)
	return report
}

func readCodexConfig(repoPath string, report *RepoReport) {
	path := filepath.Join(repoPath, ".codex", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	report.CodexConfigBytes = len(data)
	report.CodexConfigLines = countLines(data)
	report.GeneratedMCPBlocks = bytes.Contains(data, []byte("BEGIN GENERATED MCP SERVERS"))

	var parsed map[string]any
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return
	}

	profiles := tableMap(parsed["profiles"])
	report.CodexProfileCount = len(profiles)

	servers := tableMap(parsed["mcp_servers"])
	report.CodexMCPServerCount = len(servers)
	launcherSet := map[string]bool{}
	for name, raw := range servers {
		server := tableMap(raw)
		command := stringValue(server["command"])
		args := stringSlice(server["args"])
		enabledTools := stringSlice(server["enabled_tools"])
		if len(enabledTools) == 0 {
			report.MissingEnabledTools = append(report.MissingEnabledTools, name)
		}
		if isShellWrapped(command, args) {
			report.ShellWrappedCodexServers = append(report.ShellWrappedCodexServers, name)
		}
		if containsGoRun(command, args) {
			report.GoRunCodexServers = append(report.GoRunCodexServers, name)
		}
		for _, script := range extractScriptRefs(command, args) {
			launcherSet[script] = true
		}
	}

	for script := range launcherSet {
		report.LauncherScripts = append(report.LauncherScripts, script)
	}
	sort.Strings(report.LauncherScripts)
	sort.Strings(report.MissingEnabledTools)
	sort.Strings(report.ShellWrappedCodexServers)
	sort.Strings(report.GoRunCodexServers)
}

func readLauncherScripts(repoPath string, report *RepoReport) {
	for _, relPath := range report.LauncherScripts {
		data, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte("go run")) {
			report.GoRunLauncherScripts = append(report.GoRunLauncherScripts, filepath.Base(relPath))
		}
	}
	sort.Strings(report.GoRunLauncherScripts)
}

func readSurfaceFiles(repoPath, repoName string, report *RepoReport) {
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(repoPath, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		switch {
		case isSkillEntry(rel):
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			report.SkillCount++
			report.SkillEntryBytes += len(data)
			tokens := estimateTokens(len(data))
			report.SkillEntryTokens += tokens
			if tokens > hardEntryTokenBudget {
				report.OversizedSkills = append(report.OversizedSkills, EntryMetric{
					Path:   rel,
					Bytes:  len(data),
					Tokens: tokens,
				})
			}
		case isCommandPrompt(rel):
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			report.CommandPromptCount++
			report.CommandPromptBytes += len(data)
		case isRuntimePrompt(repoName, rel):
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			report.RuntimePromptCount++
			report.RuntimePromptBytes += len(data)
			tokens := estimateTokens(len(data))
			report.RuntimePromptTokens += tokens
			if tokens > hardEntryTokenBudget {
				report.OversizedRuntimePrompts = append(report.OversizedRuntimePrompts, EntryMetric{
					Path:   rel,
					Bytes:  len(data),
					Tokens: tokens,
				})
			}
		case isArchivePrompt(repoName, rel):
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			report.ArchivePromptCount++
			report.ArchivePromptBytes += len(data)
		case isBenchmarkTest(rel):
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if bytes.Contains(data, []byte("Benchmark")) {
				report.BenchmarkTestFileCount++
			}
		case isPerfWorkflow(rel):
			report.PerfWorkflow = true
		case isBaselineArtifact(rel):
			report.BaselineArtifactCount++
		}
		return nil
	})
	if err != nil {
		return
	}

	sort.Slice(report.OversizedSkills, func(i, j int) bool {
		if report.OversizedSkills[i].Tokens == report.OversizedSkills[j].Tokens {
			return report.OversizedSkills[i].Path < report.OversizedSkills[j].Path
		}
		return report.OversizedSkills[i].Tokens > report.OversizedSkills[j].Tokens
	})
	sort.Slice(report.OversizedRuntimePrompts, func(i, j int) bool {
		if report.OversizedRuntimePrompts[i].Tokens == report.OversizedRuntimePrompts[j].Tokens {
			return report.OversizedRuntimePrompts[i].Path < report.OversizedRuntimePrompts[j].Path
		}
		return report.OversizedRuntimePrompts[i].Tokens > report.OversizedRuntimePrompts[j].Tokens
	})
}

func shouldSkipDir(repoPath, path, name string) bool {
	if ignoredDirNames[name] {
		return true
	}
	rel, err := filepath.Rel(repoPath, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return strings.HasPrefix(rel, ".claude/worktrees/") ||
		strings.HasPrefix(rel, ".ralph/worktrees/") ||
		strings.HasPrefix(rel, ".github/docs/")
}

func isSkillEntry(rel string) bool {
	if !strings.HasPrefix(rel, ".agents/skills/") || !strings.HasSuffix(rel, "/SKILL.md") {
		return false
	}
	return strings.Count(rel, "/") >= 3
}

func isCommandPrompt(rel string) bool {
	return (strings.HasPrefix(rel, ".claude/commands/") || strings.HasPrefix(rel, ".gemini/commands/")) &&
		strings.HasSuffix(rel, ".md")
}

func isRuntimePrompt(repoName, rel string) bool {
	if strings.HasPrefix(rel, ".codex/prompts/") && strings.HasSuffix(rel, ".md") {
		return true
	}
	if repoName == "docs" {
		return false
	}
	return strings.HasPrefix(rel, "prompts/") && strings.HasSuffix(rel, ".md")
}

func isArchivePrompt(repoName, rel string) bool {
	return repoName == "docs" && strings.HasPrefix(rel, "prompts/") && strings.HasSuffix(rel, ".md")
}

func isBenchmarkTest(rel string) bool {
	return strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/vendor/")
}

func isPerfWorkflow(rel string) bool {
	if !strings.HasPrefix(rel, ".github/workflows/") {
		return false
	}
	base := strings.ToLower(filepath.Base(rel))
	return strings.Contains(base, "perf") || strings.Contains(base, "benchmark")
}

func isBaselineArtifact(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if strings.Contains(base, "baseline") || strings.Contains(base, "benchstat") {
		return true
	}
	return strings.HasSuffix(rel, "baselines.json")
}

func repoFindings(report RepoReport) []Finding {
	findings := make([]Finding, 0, 8)

	if len(report.GoRunCodexServers) > 0 || len(report.GoRunLauncherScripts) > 0 {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "cold_start",
			Message:  fmt.Sprintf("`go run` remains on the Codex path (%d config servers, %d launcher scripts)", len(report.GoRunCodexServers), len(report.GoRunLauncherScripts)),
		})
	}
	if len(report.MissingEnabledTools) > 0 {
		findings = append(findings, Finding{
			Severity: "high",
			Category: "tool_scope",
			Message:  fmt.Sprintf("%d MCP server profiles expose the full tool surface because `enabled_tools` is missing", len(report.MissingEnabledTools)),
		})
	}
	if len(report.ShellWrappedCodexServers) > 0 {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "launcher_wrappers",
			Message:  fmt.Sprintf("%d Codex MCP profiles still launch through shell wrappers", len(report.ShellWrappedCodexServers)),
		})
	}
	if report.CodexConfigLines > configBloatLineBudget || (report.CodexMCPServerCount == 0 && report.CodexConfigLines > configNoMCPLineBudget) {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "config_bloat",
			Message:  fmt.Sprintf(".codex/config.toml is %d lines with %d profiles", report.CodexConfigLines, report.CodexProfileCount),
		})
	}
	if len(report.OversizedSkills) > 0 {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "skill_budget",
			Message:  fmt.Sprintf("%d hot-path skill entries exceed the %d-token hard cap", len(report.OversizedSkills), hardEntryTokenBudget),
		})
	}
	if len(report.OversizedRuntimePrompts) > 0 {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "prompt_budget",
			Message:  fmt.Sprintf("%d runtime prompt entries exceed the %d-token hard cap", len(report.OversizedRuntimePrompts), hardEntryTokenBudget),
		})
	}
	if report.BenchmarkTestFileCount == 0 && !report.PerfWorkflow {
		findings = append(findings, Finding{
			Severity: "medium",
			Category: "perf_harness",
			Message:  "no benchmark or performance workflow is present",
		})
	}
	if report.RepoName == "docs" && report.ArchivePromptCount > 0 {
		findings = append(findings, Finding{
			Severity: "low",
			Category: "archive_prompts",
			Message:  fmt.Sprintf("archive prompt library is large (%d files, ~%d tokens) and should stay off the runtime path", report.ArchivePromptCount, estimateTokens(report.ArchivePromptBytes)),
		})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		return severityWeight(findings[i].Severity) > severityWeight(findings[j].Severity)
	})
	return findings
}

func scoreRepo(report RepoReport) int {
	score := 0
	score += len(report.GoRunCodexServers) * 20
	score += len(report.GoRunLauncherScripts) * 20
	score += len(report.MissingEnabledTools) * 12
	score += len(report.ShellWrappedCodexServers) * 8
	if report.CodexConfigLines > configBloatLineBudget {
		score += 10
	}
	if report.CodexMCPServerCount == 0 && report.CodexConfigLines > configNoMCPLineBudget {
		score += 6
	}
	if len(report.OversizedSkills) > 0 {
		score += 8 + len(report.OversizedSkills)*2
	}
	if len(report.OversizedRuntimePrompts) > 0 {
		score += 8 + len(report.OversizedRuntimePrompts)*2
	}
	if report.BenchmarkTestFileCount == 0 && !report.PerfWorkflow {
		score += 8
	}
	return score
}

func buildSummary(root RootSummary, repos []RepoReport, scope string) Summary {
	summary := Summary{
		Scope:        scope,
		ReposScanned: len(repos),
		TopRepos:     make([]RepoReport, 0, minInt(len(repos), topRepoCount)),
		TopFindings:  []Finding{},
	}

	aggregate := map[string]int{}
	for _, repo := range repos {
		summary.TotalCodexMCPServers += repo.CodexMCPServerCount
		if len(repo.GoRunCodexServers) > 0 || len(repo.GoRunLauncherScripts) > 0 {
			summary.ReposWithGoRunLaunchers++
		}
		if len(repo.ShellWrappedCodexServers) > 0 {
			summary.ReposWithShellWrappedServers++
		}
		if len(repo.MissingEnabledTools) > 0 {
			summary.ReposMissingEnabledTools++
		}
		if repo.CodexConfigLines > configBloatLineBudget || (repo.CodexMCPServerCount == 0 && repo.CodexConfigLines > configNoMCPLineBudget) {
			summary.ReposWithConfigBloat++
		}
		if len(repo.OversizedSkills) > 0 {
			summary.ReposWithOversizedSkills++
		}
		if len(repo.OversizedRuntimePrompts) > 0 {
			summary.ReposWithOversizedPrompts++
		}
		if repo.BenchmarkTestFileCount == 0 && !repo.PerfWorkflow {
			summary.ReposWithoutPerfHarness++
		}
		if repo.RepoName == "docs" {
			summary.DocsArchivePromptCount = repo.ArchivePromptCount
			summary.DocsArchivePromptTokens = estimateTokens(repo.ArchivePromptBytes)
		}
		for _, finding := range repo.Findings {
			key := finding.Category + "|" + finding.Message
			aggregate[key]++
		}
	}

	for i := 0; i < minInt(len(repos), topRepoCount); i++ {
		summary.TopRepos = append(summary.TopRepos, repos[i])
	}

	type bucket struct {
		finding Finding
		count   int
	}
	buckets := make([]bucket, 0, len(aggregate))
	for _, repo := range repos {
		for _, finding := range repo.Findings {
			key := finding.Category + "|" + finding.Message
			if aggregate[key] == 0 {
				continue
			}
			buckets = append(buckets, bucket{
				finding: Finding{
					Severity: finding.Severity,
					Category: finding.Category,
					Message:  fmt.Sprintf("%s (%d repos)", finding.Message, aggregate[key]),
				},
				count: aggregate[key],
			})
			delete(aggregate, key)
		}
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].count == buckets[j].count {
			return severityWeight(buckets[i].finding.Severity) > severityWeight(buckets[j].finding.Severity)
		}
		return buckets[i].count > buckets[j].count
	})
	for i := 0; i < minInt(len(buckets), 6); i++ {
		summary.TopFindings = append(summary.TopFindings, buckets[i].finding)
	}
	summary.TotalCodexMCPServers += root.RootMCPServerCount
	return summary
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

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return bytes.Count(data, []byte("\n")) + 1
}

func estimateTokens(byteCount int) int {
	if byteCount <= 0 {
		return 0
	}
	return (byteCount + 3) / 4
}

func tableMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return map[string]any{}
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
}

func stringSlice(value any) []string {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func isShellWrapped(command string, args []string) bool {
	if shellCommands[command] {
		return true
	}
	for _, arg := range args {
		if arg == "-lc" || arg == "-c" {
			return true
		}
		if strings.Contains(arg, "exec ./scripts/run-") || strings.Contains(arg, "repo_root=") || strings.Contains(arg, "cd ") {
			return true
		}
	}
	return false
}

func containsGoRun(command string, args []string) bool {
	if strings.Contains(command, "go run") {
		return true
	}
	for _, arg := range args {
		if strings.Contains(arg, "go run") {
			return true
		}
	}
	return false
}

func extractScriptRefs(command string, args []string) []string {
	set := map[string]bool{}
	for _, token := range append([]string{command}, args...) {
		for _, part := range shellLikeTokens(token) {
			if strings.Contains(part, "scripts/") && strings.HasSuffix(part, ".sh") {
				normalized := strings.TrimPrefix(part, "./")
				set[filepath.ToSlash(normalized)] = true
			}
		}
	}
	refs := make([]string, 0, len(set))
	for ref := range set {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func shellLikeTokens(value string) []string {
	splitter := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '"', '\'', '=', ';', '&', '(', ')', '{', '}', '[', ']', ',':
			return true
		default:
			return false
		}
	}
	parts := strings.FieldsFunc(value, splitter)
	filtered := parts[:0]
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func countMCPServers(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var parsed struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0
	}
	count := 0
	for name := range parsed.MCPServers {
		if strings.HasPrefix(name, "_") {
			continue
		}
		count++
	}
	return count
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Markdown renders a human-readable fleet report.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Performance Audit\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Scope: `%s`\n", r.Summary.Scope)
	fmt.Fprintf(&b, "- Repos scanned: `%d`\n", r.Summary.ReposScanned)
	fmt.Fprintf(&b, "- Workspace root MCP servers: `%d`\n", r.Root.RootMCPServerCount)
	fmt.Fprintf(&b, "- Repo-local Codex MCP servers: `%d`\n", r.Summary.TotalCodexMCPServers-r.Root.RootMCPServerCount)
	fmt.Fprintf(&b, "- Repos with `go run` on the Codex path: `%d`\n", r.Summary.ReposWithGoRunLaunchers)
	fmt.Fprintf(&b, "- Repos with shell-wrapped Codex servers: `%d`\n", r.Summary.ReposWithShellWrappedServers)
	fmt.Fprintf(&b, "- Repos missing `enabled_tools`: `%d`\n", r.Summary.ReposMissingEnabledTools)
	fmt.Fprintf(&b, "- Repos with config bloat: `%d`\n", r.Summary.ReposWithConfigBloat)
	fmt.Fprintf(&b, "- Repos without perf harnesses: `%d`\n", r.Summary.ReposWithoutPerfHarness)
	fmt.Fprintf(&b, "- Repos with oversized hot-path skills: `%d`\n", r.Summary.ReposWithOversizedSkills)
	fmt.Fprintf(&b, "- Repos with oversized runtime prompts: `%d`\n", r.Summary.ReposWithOversizedPrompts)
	if r.Summary.DocsArchivePromptCount > 0 {
		fmt.Fprintf(&b, "- Docs archive prompt library: `%d` files / ~`%d` tokens (tracked separately from runtime prompts)\n", r.Summary.DocsArchivePromptCount, r.Summary.DocsArchivePromptTokens)
	}

	fmt.Fprintf(&b, "\n## Top Fleet Bottlenecks\n\n")
	if len(r.Summary.TopFindings) == 0 {
		fmt.Fprintf(&b, "- No bottlenecks detected.\n")
	} else {
		for _, finding := range r.Summary.TopFindings {
			fmt.Fprintf(&b, "- `%s` `%s`: %s\n", finding.Severity, finding.Category, finding.Message)
		}
	}

	fmt.Fprintf(&b, "\n## Highest-Priority Repos\n\n")
	fmt.Fprintf(&b, "| Repo | Score | Key issues |\n")
	fmt.Fprintf(&b, "|------|------:|------------|\n")
	for _, repo := range r.Summary.TopRepos {
		fmt.Fprintf(&b, "| `%s` | `%d` | %s |\n", repo.RepoName, repo.Score, markdownRepoIssues(repo))
	}

	fmt.Fprintf(&b, "\n## Repo Detail\n\n")
	for _, repo := range r.Repos {
		fmt.Fprintf(&b, "### `%s`\n\n", repo.RepoName)
		fmt.Fprintf(&b, "- Scope: `%s` | Category: `%s` | Language: `%s`\n", repo.Scope, repo.Category, repo.Language)
		fmt.Fprintf(&b, "- Config: `%d` lines, `%d` profiles, `%d` Codex MCP servers\n", repo.CodexConfigLines, repo.CodexProfileCount, repo.CodexMCPServerCount)
		fmt.Fprintf(&b, "- Skills: `%d` files / ~`%d` tokens\n", repo.SkillCount, repo.SkillEntryTokens)
		fmt.Fprintf(&b, "- Runtime prompts: `%d` files / ~`%d` tokens\n", repo.RuntimePromptCount+repo.CommandPromptCount, repo.RuntimePromptTokens+estimateTokens(repo.CommandPromptBytes))
		if repo.RepoName == "docs" && repo.ArchivePromptCount > 0 {
			fmt.Fprintf(&b, "- Archive prompts: `%d` files / ~`%d` tokens\n", repo.ArchivePromptCount, estimateTokens(repo.ArchivePromptBytes))
		}
		if len(repo.Findings) == 0 {
			fmt.Fprintf(&b, "- Findings: none\n\n")
			continue
		}
		for i, finding := range repo.Findings {
			if i >= maxRepoFindingsInReport {
				break
			}
			fmt.Fprintf(&b, "- `%s` `%s`: %s\n", finding.Severity, finding.Category, finding.Message)
		}
		fmt.Fprintf(&b, "\n")
	}

	return b.String()
}

func markdownRepoIssues(repo RepoReport) string {
	if len(repo.Findings) == 0 {
		return "none"
	}
	parts := make([]string, 0, minInt(len(repo.Findings), 3))
	for i, finding := range repo.Findings {
		if i >= 3 {
			break
		}
		parts = append(parts, finding.Message)
	}
	return strings.Join(parts, "; ")
}

// --- ToolModule implementation ---

type module struct{}

// Module returns a ToolModule exposing Codex performance audit tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "perfaudit" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "perf_audit",
			Description: "Scan the workspace for current Codex performance bottlenecks",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default ~/hairglasses-studio)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				return Audit(scanPath, Options{AllScopes: allScopes}), nil
			},
		},
		{
			Name:        "perf_report",
			Description: "Return a human-readable summary of the Codex performance audit",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default ~/hairglasses-studio)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				report := Audit(scanPath, Options{AllScopes: allScopes})
				return report.Markdown(), nil
			},
		},
	}
}
