// Package sourcecontract aggregates workspace source-of-truth checks into one
// report and exit contract.
package sourcecontract

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/workspace"
)

// CheckOptions controls source-contract validation scope.
type CheckOptions struct {
	SkillsOnly           bool
	ToolsOnly            bool
	SkipRuntimeInventory bool
	SkillValidatorMode   skillsync.ValidatorMode
}

// OptionsSummary records the source-contract scope used to build a report.
type OptionsSummary struct {
	SkillsOnly           bool   `json:"skills_only"`
	ToolsOnly            bool   `json:"tools_only"`
	SkipRuntimeInventory bool   `json:"skip_runtime_inventory"`
	SkillValidatorMode   string `json:"skill_validator_mode"`
}

// Summary captures high-level source-contract counts.
type Summary struct {
	ManagedReposChecked      int `json:"managed_repos_checked"`
	SkillSurfaceReposChecked int `json:"skill_surface_repos_checked"`
	MCPReposChecked          int `json:"mcp_repos_checked"`
	Warnings                 int `json:"warnings"`
}

// RepoReport captures checks for one manifest-managed repo.
type RepoReport struct {
	Repo      string                `json:"repo"`
	Path      string                `json:"path"`
	Scope     string                `json:"scope,omitempty"`
	Category  string                `json:"category,omitempty"`
	Passed    bool                  `json:"passed"`
	SkillSync *skillsync.SyncReport `json:"skill_sync,omitempty"`
	MCPSync   *mcpsync.SyncReport   `json:"mcp_sync,omitempty"`
	Warnings  []string              `json:"warnings,omitempty"`
	Errors    []string              `json:"errors,omitempty"`
}

// Report captures a full workspace source-contract check.
type Report struct {
	Root             string                               `json:"root"`
	Passed           bool                                 `json:"passed"`
	Options          OptionsSummary                       `json:"options"`
	Summary          Summary                              `json:"summary"`
	Warnings         []string                             `json:"warnings,omitempty"`
	Artifact         *ArtifactCheck                       `json:"artifact,omitempty"`
	Workspace        workspace.Report                     `json:"workspace"`
	Repos            []RepoReport                         `json:"repos,omitempty"`
	RuntimeInventory *mcpsync.RuntimeInventoryCheckReport `json:"runtime_inventory,omitempty"`
	GlobalProjection *mcpsync.GlobalProjectionCheckReport `json:"global_projection,omitempty"`
}

// Check validates repo-controlled workspace, skill, MCP, and runtime inventory sources.
func Check(root string, opts CheckOptions) (Report, error) {
	if opts.SkillsOnly && opts.ToolsOnly {
		return Report{}, fmt.Errorf("skills-only and tools-only cannot both be true")
	}
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return Report{Root: root}, err
	}

	report := Report{
		Root: root,
		Options: OptionsSummary{
			SkillsOnly:           opts.SkillsOnly,
			ToolsOnly:            opts.ToolsOnly,
			SkipRuntimeInventory: opts.SkipRuntimeInventory,
			SkillValidatorMode:   string(normalizedSkillValidatorMode(opts.SkillValidatorMode)),
		},
		Workspace: workspace.Check(root, manifest),
	}

	for _, repo := range manifest.Filter(workspace.Filter{BaselineOnly: true}) {
		repoPath := filepath.Join(root, repo.Name)
		if _, err := os.Stat(repoPath); err != nil {
			continue
		}

		repoReport := RepoReport{
			Repo:     repo.Name,
			Path:     repoPath,
			Scope:    repo.Scope,
			Category: repo.Category,
			Passed:   true,
		}
		checked := false

		if !opts.ToolsOnly && fileExists(filepath.Join(repoPath, ".agents", "skills", "surface.yaml")) {
			checked = true
			report.Summary.SkillSurfaceReposChecked++
			skillReport := skillsync.CheckWithOptions(repoPath, skillsync.Options{
				ValidatorMode: opts.SkillValidatorMode,
			})
			repoReport.SkillSync = &skillReport
			repoReport.Warnings = append(repoReport.Warnings, skillReport.Warnings...)
			if len(skillReport.Errors) > 0 || skillReport.PendingChanges {
				repoReport.Passed = false
			}
		}

		if !opts.SkillsOnly && fileExists(filepath.Join(repoPath, ".mcp.json")) && fileExists(filepath.Join(repoPath, ".codex", "config.toml")) {
			checked = true
			report.Summary.MCPReposChecked++
			mcpReport := mcpsync.Diff(repoPath)
			repoReport.MCPSync = &mcpReport
			if len(mcpReport.Errors) > 0 || mcpReport.PendingChanges {
				repoReport.Passed = false
			}
		}

		if !checked {
			continue
		}
		report.Summary.ManagedReposChecked++
		report.Repos = append(report.Repos, repoReport)
		report.Warnings = append(report.Warnings, repoReport.Warnings...)
	}

	if !opts.SkillsOnly && !opts.SkipRuntimeInventory {
		runtimeReport, err := mcpsync.CheckRuntimeInventory(mcpsync.RuntimeInventoryCheckOptions{
			WorkspaceRoot: root,
		})
		if err != nil {
			return report, err
		}
		report.RuntimeInventory = &runtimeReport

		globalReport, err := mcpsync.CheckGlobalProjection(mcpsync.GlobalProjectionCheckOptions{
			WorkspaceRoot: root,
		})
		if err != nil {
			return report, err
		}
		report.GlobalProjection = &globalReport
	}

	report.Warnings = uniqueStrings(report.Warnings)
	report.Summary.Warnings = len(report.Warnings)
	report.Passed = report.Workspace.Passed
	for _, repo := range report.Repos {
		if !repo.Passed {
			report.Passed = false
			break
		}
	}
	if report.RuntimeInventory != nil && !report.RuntimeInventory.Passed {
		report.Passed = false
	}
	if report.GlobalProjection != nil && !report.GlobalProjection.Passed {
		report.Passed = false
	}
	return report, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func normalizedSkillValidatorMode(mode skillsync.ValidatorMode) skillsync.ValidatorMode {
	if mode == "" {
		return skillsync.ValidatorAuto
	}
	return mode
}
