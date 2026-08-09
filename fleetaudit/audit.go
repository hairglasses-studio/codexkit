// Package fleetaudit runs aggregate validation across all repos
// in a scan directory (default workspace root).
//
// It combines baselineguard, skillsync, and mcpsync checks into
// a unified fleet health report.
package fleetaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

// maxFleetReportFailingRepos bounds how many failing repos fleet_report
// lists by name before collapsing the remainder into a count, keeping the
// report readable for a large fleet.
const maxFleetReportFailingRepos = 30

// RepoAudit is the combined audit result for a single repo.
type RepoAudit struct {
	RepoPath  string               `json:"repo_path"`
	RepoName  string               `json:"repo_name"`
	Baseline  baselineguard.Report `json:"baseline"`
	SkillSync skillsync.SyncReport `json:"skill_sync"`
	MCPSync   mcpsync.SyncReport   `json:"mcp_sync"`
	Passed    bool                 `json:"passed"`
}

// FleetReport is the aggregate audit across all repos.
type FleetReport struct {
	ScanPath   string      `json:"scan_path"`
	TotalRepos int         `json:"total_repos"`
	Passed     int         `json:"passed"`
	Failed     int         `json:"failed"`
	Repos      []RepoAudit `json:"repos"`
}

// Audit runs a full fleet audit on all git repos in scanPath.
func Audit(scanPath string) FleetReport {
	report := FleetReport{ScanPath: scanPath}

	entries, err := os.ReadDir(scanPath)
	if err != nil {
		return report
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(scanPath, entry.Name())
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
			continue
		}

		audit := RepoAudit{
			RepoPath:  repoPath,
			RepoName:  entry.Name(),
			Baseline:  baselineguard.Check(repoPath),
			SkillSync: skillsync.Diff(repoPath),
			MCPSync:   mcpsync.Diff(repoPath),
		}

		// mcpsync.Diff reports a "reading .mcp.json" error when the file is
		// simply absent — a legitimate state, not a repo that has drifted.
		// baselineguard.addMCPSyncCheck already skips its own mcp_sync
		// finding in that case; mirror that here so an absent .mcp.json
		// doesn't fold into a false failure.
		_, mcpConfigErr := os.Stat(filepath.Join(repoPath, ".mcp.json"))
		mcpConfigPresent := mcpConfigErr == nil

		// Passed if baseline passes and no sync errors
		audit.Passed = audit.Baseline.Passed &&
			len(audit.SkillSync.Errors) == 0 &&
			(!mcpConfigPresent || len(audit.MCPSync.Errors) == 0)

		report.Repos = append(report.Repos, audit)
		report.TotalRepos++
		if audit.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
	}

	return report
}

// --- ToolModule implementation ---

type module struct{}

// Module returns a ToolModule exposing fleet audit tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "fleetaudit" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "fleet_audit",
			Description: "Run full audit on all repos in a scan directory",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default workspace root)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					home, _ := os.UserHomeDir()
					scanPath = filepath.Join(home, "hairglasses-studio")
				}
				return Audit(scanPath), nil
			},
		},
		{
			Name:        "fleet_report",
			Description: "Generate a summary report of fleet health",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default workspace root)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					home, _ := os.UserHomeDir()
					scanPath = filepath.Join(home, "hairglasses-studio")
				}
				report := Audit(scanPath)
				return fleetReportSummary(report), nil
			},
		},
	}
}

// fleetReportSummary renders the fleet_report text: an overall count line
// plus, when any repos fail, their names and failing-check counts so the
// caller doesn't have to re-run fleet_audit to see who's broken.
func fleetReportSummary(report FleetReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fleet: %d repos, %d passed, %d failed",
		report.TotalRepos, report.Passed, report.Failed)
	if report.Failed == 0 {
		return b.String()
	}
	b.WriteString("\n\nFailing repos:\n")
	shown := 0
	for _, repo := range report.Repos {
		if repo.Passed {
			continue
		}
		if shown >= maxFleetReportFailingRepos {
			break
		}
		fmt.Fprintf(&b, "- %s (%d failing check%s)\n",
			repo.RepoName, failingCheckCount(repo), pluralSuffix(failingCheckCount(repo)))
		shown++
	}
	if remaining := report.Failed - shown; remaining > 0 {
		fmt.Fprintf(&b, "- ... and %d more\n", remaining)
	}
	return strings.TrimRight(b.String(), "\n")
}

// failingCheckCount counts the failing baseline checks plus skill-sync and
// mcp-sync errors for one repo audit.
func failingCheckCount(repo RepoAudit) int {
	return repo.Baseline.Failed + len(repo.SkillSync.Errors) + len(repo.MCPSync.Errors)
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
