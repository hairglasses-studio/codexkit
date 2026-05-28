// Package surfaceindex builds a workspace-wide index of repo-owned agent
// surfaces and their generated/runtime projections.
package surfaceindex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/sourcecontract"
	"github.com/hairglasses-studio/codexkit/workspace"
)

const IndexKind = "baseline repo agent surface index"

// Options controls surface-index generation.
type Options struct {
	WorkspaceRoot      string
	GeneratedAt        string
	SkillValidatorMode skillsync.ValidatorMode
}

// CheckOptions controls surface-index artifact validation.
type CheckOptions struct {
	WorkspaceRoot      string
	JSONPath           string
	MarkdownPath       string
	SkipArtifacts      bool
	SkillValidatorMode skillsync.ValidatorMode
}

// Index captures the generated repo surface index.
type Index struct {
	GeneratedAt    string          `json:"generated_at"`
	GeneratedBy    string          `json:"generated_by"`
	WorkspaceRoot  string          `json:"workspace_root"`
	IndexKind      string          `json:"index_kind"`
	Summary        Summary         `json:"summary"`
	SourceContract ContractSummary `json:"source_contract"`
	Repos          []RepoEntry     `json:"repos"`
}

// Summary captures index-wide counts.
type Summary struct {
	BaselineRepos          int `json:"baseline_repos"`
	ExistingRepos          int `json:"existing_repos"`
	SkillSurfaceRepos      int `json:"skill_surface_repos"`
	MCPSourceRepos         int `json:"mcp_source_repos"`
	RuntimeProjectedRepos  int `json:"runtime_projected_repos"`
	RuntimeSkippedRepos    int `json:"runtime_skipped_repos"`
	SourceContractFailures int `json:"source_contract_failures"`
}

// ContractSummary captures the aggregate source-contract status used by the index.
type ContractSummary struct {
	Passed             bool   `json:"passed"`
	SkillValidatorMode string `json:"skill_validator_mode"`
	Warnings           int    `json:"warnings"`
	RuntimeIncluded    bool   `json:"runtime_included"`
}

// RepoEntry captures all known agent surfaces for one baseline repo.
type RepoEntry struct {
	Name              string                        `json:"name"`
	Path              string                        `json:"path"`
	Exists            bool                          `json:"exists"`
	Category          string                        `json:"category,omitempty"`
	Scope             string                        `json:"scope,omitempty"`
	Language          string                        `json:"language,omitempty"`
	Lifecycle         string                        `json:"lifecycle,omitempty"`
	BaselineTarget    bool                          `json:"baseline_target"`
	GoWorkMember      bool                          `json:"go_work_member"`
	Consolidation     *ConsolidationDecisionSummary `json:"consolidation,omitempty"`
	SourceContract    RepoContractStatus            `json:"source_contract"`
	Skills            SkillSurface                  `json:"skills"`
	MCP               MCPSurface                    `json:"mcp"`
	Runtime           RuntimeSurface                `json:"runtime"`
	WorkspaceFindings []string                      `json:"workspace_findings,omitempty"`
}

// ConsolidationDecisionSummary captures docs-side consolidation intent.
type ConsolidationDecisionSummary struct {
	State            string   `json:"state,omitempty"`
	WorkspaceScope   string   `json:"workspace_scope,omitempty"`
	GoWorkMember     *bool    `json:"go_work_member,omitempty"`
	BaselineTarget   *bool    `json:"baseline_target,omitempty"`
	MergeTarget      string   `json:"merge_target,omitempty"`
	ArchiveCandidate bool     `json:"archive_candidate,omitempty"`
	Notes            []string `json:"notes,omitempty"`
}

// RepoContractStatus captures per-repo source-contract pass/fail state.
type RepoContractStatus struct {
	Checked        bool     `json:"checked"`
	Status         string   `json:"status"`
	Reason         string   `json:"reason,omitempty"`
	Passed         bool     `json:"passed"`
	SkillChecked   bool     `json:"skill_checked"`
	SkillPassed    bool     `json:"skill_passed"`
	MCPChecked     bool     `json:"mcp_checked"`
	MCPPassed      bool     `json:"mcp_passed"`
	PendingChanges bool     `json:"pending_changes"`
	Warnings       []string `json:"warnings,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// SkillSurface captures canonical skill sources and generated mirrors.
type SkillSurface struct {
	HasSurface       bool              `json:"has_surface"`
	SurfacePath      string            `json:"surface_path,omitempty"`
	CanonicalSkills  []SkillSource     `json:"canonical_skills,omitempty"`
	GeneratedMirrors []GeneratedMirror `json:"generated_mirrors,omitempty"`
	Errors           []string          `json:"errors,omitempty"`
}

// SkillSource captures one canonical skill entry.
type SkillSource struct {
	Name                   string   `json:"name"`
	Path                   string   `json:"path"`
	ClaudeIncludeCanonical bool     `json:"claude_include_canonical"`
	ExportPlugin           bool     `json:"export_plugin"`
	ClaudeAliases          []string `json:"claude_aliases,omitempty"`
}

// GeneratedMirror captures a managed generated skill mirror.
type GeneratedMirror struct {
	Path    string `json:"path"`
	Action  string `json:"action,omitempty"`
	Message string `json:"message,omitempty"`
}

// MCPSurface captures source and generated MCP profile state.
type MCPSurface struct {
	HasSource         bool     `json:"has_source"`
	SourcePath        string   `json:"source_path,omitempty"`
	ConfigPath        string   `json:"config_path,omitempty"`
	PolicyPath        string   `json:"policy_path,omitempty"`
	SourceServers     []string `json:"source_servers,omitempty"`
	GeneratedProfiles []string `json:"generated_profiles,omitempty"`
	PendingChanges    bool     `json:"pending_changes"`
	Errors            []string `json:"errors,omitempty"`
}

// RuntimeSurface captures global runtime projection status by source repo.
type RuntimeSurface struct {
	Status           string                  `json:"status"`
	ProjectedAliases []RuntimeAlias          `json:"projected_aliases,omitempty"`
	SkippedServers   []RuntimeSkippedSurface `json:"skipped_servers,omitempty"`
	ReadyCount       int                     `json:"ready_count,omitempty"`
	InvalidCount     int                     `json:"invalid_count,omitempty"`
	RemoteCount      int                     `json:"remote_count,omitempty"`
}

// RuntimeAlias captures one projected global MCP alias.
type RuntimeAlias struct {
	Name         string   `json:"name"`
	SourceServer string   `json:"source_server"`
	Validation   string   `json:"validation,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

// RuntimeSkippedSurface captures one policy-skipped source server.
type RuntimeSkippedSurface struct {
	SourceServer string `json:"source_server"`
	Reason       string `json:"reason"`
}

// CheckReport captures surface-index artifact validation.
type CheckReport struct {
	Passed        bool           `json:"passed"`
	WorkspaceRoot string         `json:"workspace_root"`
	JSONPath      string         `json:"json_path,omitempty"`
	MarkdownPath  string         `json:"markdown_path,omitempty"`
	Index         Index          `json:"index"`
	Findings      []CheckFinding `json:"findings"`
}

// CheckFinding captures one surface-index validation result.
type CheckFinding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

func (r *CheckReport) add(check string, passed bool, message string) {
	r.Findings = append(r.Findings, CheckFinding{Check: check, Passed: passed, Message: message})
}

// Build builds a live surface index for baseline manifest repos.
func Build(opts Options) (Index, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	validatorMode := normalizedValidatorMode(opts.SkillValidatorMode)

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return Index{}, err
	}
	decisions, err := loadDecisionMap(root)
	if err != nil {
		return Index{}, err
	}
	sourceReport, err := sourcecontract.Check(root, sourcecontract.CheckOptions{
		SkillValidatorMode: validatorMode,
	})
	if err != nil {
		return Index{}, err
	}
	repoReports := mapSourceContractRepos(sourceReport)
	workspaceFindings := mapWorkspaceFindings(sourceReport.Workspace.Findings)
	runtimeByRepo, skippedByRepo := mapRuntimeSurfaces(sourceReport)

	index := Index{
		GeneratedAt:   generatedAt,
		GeneratedBy:   "codexkit workspace surface-index",
		WorkspaceRoot: root,
		IndexKind:     IndexKind,
		SourceContract: ContractSummary{
			Passed:             sourceReport.Passed,
			SkillValidatorMode: string(validatorMode),
			Warnings:           sourceReport.Summary.Warnings,
			RuntimeIncluded:    sourceReport.RuntimeInventory != nil,
		},
	}

	for _, repo := range manifest.Filter(workspace.Filter{BaselineOnly: true}) {
		repoPath := filepath.Join(root, repo.Name)
		entry := RepoEntry{
			Name:           repo.Name,
			Path:           relPath(root, repoPath),
			Exists:         dirExists(repoPath),
			Category:       repo.Category,
			Scope:          repo.Scope,
			Language:       repo.Language,
			Lifecycle:      repo.Lifecycle,
			BaselineTarget: repo.BaselineTarget,
			GoWorkMember:   repo.GoWorkMember,
			Skills:         buildSkillSurface(root, repoPath, repoReports[repo.Name]),
			MCP:            buildMCPSurface(root, repoPath, repoReports[repo.Name]),
			Runtime:        buildRuntimeSurface(runtimeByRepo[repo.Name], skippedByRepo[repo.Name]),
		}
		if decision, ok := decisions[repo.Name]; ok {
			entry.Consolidation = summarizeDecision(decision)
		}
		entry.SourceContract = buildRepoContractStatus(entry, repoReports[repo.Name])
		entry.WorkspaceFindings = workspaceFindings[repo.Name]
		index.Repos = append(index.Repos, entry)
	}

	index.Summary = summarizeIndex(index.Repos)
	return index, nil
}

// Check validates saved surface-index artifacts against a live index.
func Check(opts CheckOptions) (CheckReport, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	jsonPath := opts.JSONPath
	markdownPath := opts.MarkdownPath
	if !opts.SkipArtifacts {
		if jsonPath == "" {
			jsonPath = latestArtifact(root, ".json")
		}
		if markdownPath == "" {
			markdownPath = markdownPathForJSON(jsonPath)
			if markdownPath == "" {
				markdownPath = latestArtifact(root, ".md")
			}
		}
	}

	generatedAt := ""
	if jsonPath != "" && !opts.SkipArtifacts {
		existing, err := readJSONArtifact(jsonPath)
		if err == nil {
			generatedAt = existing.GeneratedAt
		}
	}

	index, err := Build(Options{
		WorkspaceRoot:      root,
		GeneratedAt:        generatedAt,
		SkillValidatorMode: opts.SkillValidatorMode,
	})
	if err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		Index:         index,
	}
	if !opts.SkipArtifacts {
		checkJSONArtifact(&report, index, jsonPath)
		checkMarkdownArtifact(&report, index, markdownPath)
	}
	report.Passed = true
	for _, finding := range report.Findings {
		if !finding.Passed {
			report.Passed = false
			break
		}
	}
	return report, nil
}

// Write writes JSON and/or Markdown surface-index artifacts.
func Write(index Index, jsonPath, markdownPath string) error {
	if jsonPath != "" {
		data, err := MarshalJSON(index)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
			return err
		}
	}
	if markdownPath != "" {
		if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(markdownPath, []byte(RenderMarkdown(index)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSON returns stable JSON for the index artifact.
func MarshalJSON(index Index) ([]byte, error) {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// RenderMarkdown returns a compact human-readable surface index.
func RenderMarkdown(index Index) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Repo Agent Surface Index\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", index.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`, `workspace/manifest.json`, `docs/inventory/repo-consolidation-matrix.json`, repo-local skill/MCP files, and runtime projection inventory.\n\n", index.GeneratedBy)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Count |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Baseline repos | %d |\n", index.Summary.BaselineRepos)
	fmt.Fprintf(&b, "| Existing repos | %d |\n", index.Summary.ExistingRepos)
	fmt.Fprintf(&b, "| Skill surface repos | %d |\n", index.Summary.SkillSurfaceRepos)
	fmt.Fprintf(&b, "| MCP source repos | %d |\n", index.Summary.MCPSourceRepos)
	fmt.Fprintf(&b, "| Runtime projected repos | %d |\n", index.Summary.RuntimeProjectedRepos)
	fmt.Fprintf(&b, "| Runtime skipped repos | %d |\n", index.Summary.RuntimeSkippedRepos)
	fmt.Fprintf(&b, "| Source-contract failures | %d |\n\n", index.Summary.SourceContractFailures)
	fmt.Fprintf(&b, "Source-contract status: `%s` with `%s` skill validator mode and %d warnings.\n\n", passLabel(index.SourceContract.Passed), index.SourceContract.SkillValidatorMode, index.SourceContract.Warnings)

	fmt.Fprintf(&b, "## Repos\n\n")
	fmt.Fprintf(&b, "| Repo | Scope | Decision | Source contract | Skills | MCP | Runtime |\n|---|---|---|---|---:|---:|---|\n")
	for _, repo := range index.Repos {
		decision := ""
		if repo.Consolidation != nil {
			decision = repo.Consolidation.State
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | %d | %d | %s |\n",
			mdEscape(repo.Name),
			mdEscape(repo.Scope),
			mdEscape(emptyLabel(decision)),
			repo.SourceContract.Status,
			len(repo.Skills.CanonicalSkills),
			len(repo.MCP.SourceServers),
			runtimeLabel(repo.Runtime),
		)
	}
	fmt.Fprintf(&b, "\n")

	nonContractRows := nonContractRepos(index.Repos)
	if len(nonContractRows) > 0 {
		fmt.Fprintf(&b, "## Non-Contract Rows\n\n")
		fmt.Fprintf(&b, "| Repo | Status | Reason | Runtime | Decision |\n|---|---|---|---|---|\n")
		for _, repo := range nonContractRows {
			decision := ""
			if repo.Consolidation != nil {
				decision = repo.Consolidation.State
				if repo.Consolidation.MergeTarget != "" {
					decision += " -> " + repo.Consolidation.MergeTarget
				}
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | `%s` |\n",
				mdEscape(repo.Name),
				mdEscape(repo.SourceContract.Status),
				mdEscape(emptyLabel(repo.SourceContract.Reason)),
				runtimeLabel(repo.Runtime),
				mdEscape(emptyLabel(decision)),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Regenerate\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "cd /path/to/workspace/codexkit\n")
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace surface-index /path/to/workspace --skill-validator=off --json-out ../docs/inventory/repo-surface-index-%s.json --markdown-out ../docs/inventory/repo-surface-index-%s.md\n", artifactDate(index.GeneratedAt), artifactDate(index.GeneratedAt))
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace surface-index-check /path/to/workspace --skill-validator=off\n")
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

func loadDecisionMap(root string) (map[string]workspace.ConsolidationDecision, error) {
	matrix, err := workspace.LoadConsolidationMatrix(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]workspace.ConsolidationDecision{}, nil
		}
		return nil, err
	}
	decisions := make(map[string]workspace.ConsolidationDecision, len(matrix.Decisions))
	for _, decision := range matrix.Decisions {
		decisions[decision.Repo] = decision
	}
	return decisions, nil
}

func mapSourceContractRepos(report sourcecontract.Report) map[string]sourcecontract.RepoReport {
	repos := make(map[string]sourcecontract.RepoReport, len(report.Repos))
	for _, repo := range report.Repos {
		repos[repo.Repo] = repo
	}
	return repos
}

func mapWorkspaceFindings(findings []workspace.Finding) map[string][]string {
	byRepo := make(map[string][]string)
	for _, finding := range findings {
		if finding.Repo == "" || finding.Passed {
			continue
		}
		message := finding.Check
		if finding.Message != "" {
			message += ": " + finding.Message
		}
		byRepo[finding.Repo] = append(byRepo[finding.Repo], message)
	}
	return byRepo
}

func mapRuntimeSurfaces(report sourcecontract.Report) (map[string][]mcpsync.RuntimeServer, map[string][]mcpsync.RuntimeSkippedServer) {
	projected := map[string][]mcpsync.RuntimeServer{}
	skipped := map[string][]mcpsync.RuntimeSkippedServer{}
	if report.RuntimeInventory == nil {
		return projected, skipped
	}
	for _, server := range report.RuntimeInventory.Inventory.Servers {
		projected[server.SourceRepo] = append(projected[server.SourceRepo], server)
	}
	for _, server := range report.RuntimeInventory.Inventory.Skipped {
		skipped[server.SourceRepo] = append(skipped[server.SourceRepo], server)
	}
	return projected, skipped
}

func buildRepoContractStatus(entry RepoEntry, report sourcecontract.RepoReport) RepoContractStatus {
	status := RepoContractStatus{Checked: report.Repo != "", Status: "not_checked", Passed: report.Passed}
	if !status.Checked {
		switch {
		case entry.MCP.HasSource && len(entry.Runtime.ProjectedAliases) > 0:
			status.Status = "runtime_projection_only"
			status.Reason = "repo has raw MCP source projected through workspace runtime inventory, but no repo-local Codex generated profile contract"
		case entry.MCP.HasSource && entry.MCP.ConfigPath == "":
			status.Reason = "repo has raw MCP source but no repo-local Codex config contract"
		case !entry.Skills.HasSurface && !entry.MCP.HasSource:
			status.Reason = "repo has no managed skill surface or repo-local MCP source"
		default:
			status.Reason = "repo has no source-contract-managed surface"
		}
		return status
	}
	status.Status = passLabel(report.Passed)
	status.Warnings = append(status.Warnings, report.Warnings...)
	status.Errors = append(status.Errors, report.Errors...)
	if report.SkillSync != nil {
		status.SkillChecked = true
		status.SkillPassed = len(report.SkillSync.Errors) == 0 && !report.SkillSync.PendingChanges
		status.PendingChanges = status.PendingChanges || report.SkillSync.PendingChanges
		status.Errors = append(status.Errors, report.SkillSync.Errors...)
	}
	if report.MCPSync != nil {
		status.MCPChecked = true
		status.MCPPassed = len(report.MCPSync.Errors) == 0 && !report.MCPSync.PendingChanges
		status.PendingChanges = status.PendingChanges || report.MCPSync.PendingChanges
		status.Errors = append(status.Errors, report.MCPSync.Errors...)
	}
	return status
}

func buildSkillSurface(root, repoPath string, report sourcecontract.RepoReport) SkillSurface {
	surfacePath := filepath.Join(repoPath, ".agents", "skills", "surface.yaml")
	surface := SkillSurface{HasSurface: fileExists(surfacePath)}
	if !surface.HasSurface {
		return surface
	}
	surface.SurfacePath = relPath(root, surfacePath)
	parsed, err := skillsync.ParseSurface(repoPath)
	if err != nil {
		surface.Errors = append(surface.Errors, err.Error())
		return surface
	}
	for _, skill := range parsed.Skills {
		aliases := make([]string, 0, len(skill.ClaudeAliases)+1)
		seen := map[string]struct{}{}
		for _, alias := range skill.ClaudeAliases {
			aliases = append(aliases, alias.Name)
			seen[alias.Name] = struct{}{}
		}
		if normalized := strings.ReplaceAll(skill.Name, "_", "-"); normalized != skill.Name {
			if _, ok := seen[normalized]; !ok {
				aliases = append(aliases, normalized)
			}
		}
		sort.Strings(aliases)
		surface.CanonicalSkills = append(surface.CanonicalSkills, SkillSource{
			Name:                   skill.Name,
			Path:                   relPath(root, filepath.Join(repoPath, ".agents", "skills", skill.Name, "SKILL.md")),
			ClaudeIncludeCanonical: skill.ClaudeIncludeCanonical,
			ExportPlugin:           skill.ExportPlugin,
			ClaudeAliases:          aliases,
		})
	}
	if report.SkillSync != nil {
		for _, action := range report.SkillSync.Actions {
			if action.DstPath == "" {
				continue
			}
			surface.GeneratedMirrors = append(surface.GeneratedMirrors, GeneratedMirror{
				Path:    relPath(root, action.DstPath),
				Action:  action.Action,
				Message: action.Message,
			})
		}
	}
	sort.Slice(surface.CanonicalSkills, func(i, j int) bool {
		return surface.CanonicalSkills[i].Name < surface.CanonicalSkills[j].Name
	})
	sort.Slice(surface.GeneratedMirrors, func(i, j int) bool {
		return surface.GeneratedMirrors[i].Path < surface.GeneratedMirrors[j].Path
	})
	return surface
}

func buildMCPSurface(root, repoPath string, report sourcecontract.RepoReport) MCPSurface {
	sourcePath := filepath.Join(repoPath, ".mcp.json")
	configPath := filepath.Join(repoPath, ".codex", "config.toml")
	policyPath := filepath.Join(repoPath, ".codex", "mcp-profile-policy.json")
	surface := MCPSurface{
		HasSource:      fileExists(sourcePath),
		PendingChanges: report.MCPSync != nil && report.MCPSync.PendingChanges,
	}
	if surface.HasSource {
		surface.SourcePath = relPath(root, sourcePath)
		parsed, err := mcpsync.Parse(repoPath)
		if err != nil {
			surface.Errors = append(surface.Errors, err.Error())
		} else {
			for name := range parsed.MCPServers {
				surface.SourceServers = append(surface.SourceServers, name)
			}
			sort.Strings(surface.SourceServers)
		}
	}
	if fileExists(configPath) {
		surface.ConfigPath = relPath(root, configPath)
		if surface.HasSource {
			profiles, err := mcpsync.List(repoPath)
			if err != nil {
				surface.Errors = append(surface.Errors, err.Error())
			} else {
				surface.GeneratedProfiles = profiles
			}
		}
	}
	if fileExists(policyPath) {
		surface.PolicyPath = relPath(root, policyPath)
	}
	if report.MCPSync != nil {
		surface.Errors = append(surface.Errors, report.MCPSync.Errors...)
	}
	return surface
}

func buildRuntimeSurface(projected []mcpsync.RuntimeServer, skipped []mcpsync.RuntimeSkippedServer) RuntimeSurface {
	surface := RuntimeSurface{Status: "none"}
	for _, server := range projected {
		surface.ProjectedAliases = append(surface.ProjectedAliases, RuntimeAlias{
			Name:         server.Name,
			SourceServer: server.SourceServer,
			Validation:   server.Validation,
			Notes:        append([]string{}, server.ValidationNotes...),
		})
		switch server.Validation {
		case "ready":
			surface.ReadyCount++
		case "invalid":
			surface.InvalidCount++
		case "remote":
			surface.RemoteCount++
		}
	}
	for _, server := range skipped {
		surface.SkippedServers = append(surface.SkippedServers, RuntimeSkippedSurface{
			SourceServer: server.SourceServer,
			Reason:       server.Reason,
		})
	}
	sort.Slice(surface.ProjectedAliases, func(i, j int) bool {
		return surface.ProjectedAliases[i].Name < surface.ProjectedAliases[j].Name
	})
	sort.Slice(surface.SkippedServers, func(i, j int) bool {
		return surface.SkippedServers[i].SourceServer < surface.SkippedServers[j].SourceServer
	})
	switch {
	case surface.InvalidCount > 0:
		surface.Status = "invalid"
	case len(surface.ProjectedAliases) > 0:
		surface.Status = "projected"
	case len(surface.SkippedServers) > 0:
		surface.Status = "skipped"
	}
	return surface
}

func summarizeDecision(decision workspace.ConsolidationDecision) *ConsolidationDecisionSummary {
	return &ConsolidationDecisionSummary{
		State:            decision.State,
		WorkspaceScope:   decision.WorkspaceScope,
		GoWorkMember:     decision.GoWorkMember,
		BaselineTarget:   decision.BaselineTarget,
		MergeTarget:      decision.MergeTarget,
		ArchiveCandidate: decision.ArchiveCandidate,
		Notes:            append([]string{}, decision.Notes...),
	}
}

func summarizeIndex(repos []RepoEntry) Summary {
	summary := Summary{BaselineRepos: len(repos)}
	for _, repo := range repos {
		if repo.Exists {
			summary.ExistingRepos++
		}
		if repo.Skills.HasSurface {
			summary.SkillSurfaceRepos++
		}
		if repo.MCP.HasSource {
			summary.MCPSourceRepos++
		}
		if len(repo.Runtime.ProjectedAliases) > 0 {
			summary.RuntimeProjectedRepos++
		}
		if len(repo.Runtime.SkippedServers) > 0 {
			summary.RuntimeSkippedRepos++
		}
		if repo.SourceContract.Checked && !repo.SourceContract.Passed {
			summary.SourceContractFailures++
		}
	}
	return summary
}

func nonContractRepos(repos []RepoEntry) []RepoEntry {
	out := make([]RepoEntry, 0)
	for _, repo := range repos {
		if repo.SourceContract.Status == "pass" {
			continue
		}
		out = append(out, repo)
	}
	return out
}

func readJSONArtifact(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return index, nil
}

func checkJSONArtifact(report *CheckReport, index Index, path string) {
	if path == "" {
		report.add("json_artifact", false, "no repo-surface-index-*.json artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("json_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected, err := MarshalJSON(index)
	if err != nil {
		report.add("json_artifact", false, err.Error())
		return
	}
	if !bytes.Equal(actual, expected) {
		report.add("json_artifact", false, fmt.Sprintf("%s does not match live repo surface index", path))
		return
	}
	report.add("json_artifact", true, path)
}

func checkMarkdownArtifact(report *CheckReport, index Index, path string) {
	if path == "" {
		report.add("markdown_artifact", false, "no repo-surface-index-*.md artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("markdown_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected := []byte(RenderMarkdown(index))
	if !bytes.Equal(actual, expected) {
		report.add("markdown_artifact", false, fmt.Sprintf("%s does not match live repo surface index", path))
		return
	}
	report.add("markdown_artifact", true, path)
}

func normalizedValidatorMode(mode skillsync.ValidatorMode) skillsync.ValidatorMode {
	if mode == "" {
		return skillsync.ValidatorOff
	}
	return mode
}

func latestArtifact(root, ext string) string {
	matches, err := filepath.Glob(filepath.Join(root, "docs", "inventory", "repo-surface-index-*"+ext))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func markdownPathForJSON(jsonPath string) string {
	if jsonPath == "" {
		return ""
	}
	if strings.HasSuffix(jsonPath, ".json") {
		return strings.TrimSuffix(jsonPath, ".json") + ".md"
	}
	return jsonPath + ".md"
}

func relPath(root, path string) string {
	if root == "" || path == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func passLabel(passed bool) string {
	if passed {
		return "pass"
	}
	return "fail"
}

func runtimeLabel(surface RuntimeSurface) string {
	parts := []string{surface.Status}
	if len(surface.ProjectedAliases) > 0 {
		parts = append(parts, fmt.Sprintf("%d projected", len(surface.ProjectedAliases)))
	}
	if len(surface.SkippedServers) > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", len(surface.SkippedServers)))
	}
	return "`" + mdEscape(strings.Join(parts, ", ")) + "`"
}

func emptyLabel(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func artifactDate(generatedAt string) string {
	if len(generatedAt) >= len("2006-01-02") {
		return generatedAt[:len("2006-01-02")]
	}
	return "YYYY-MM-DD"
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}
