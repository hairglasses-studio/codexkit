// Package fleetaudit runs aggregate validation across all repos
// in a scan directory (default ~/hairglasses-studio).
//
// It combines baselineguard, skillsync, and mcpsync checks into
// a unified fleet health report.
package fleetaudit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

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

		// Passed if baseline passes and no sync errors
		audit.Passed = audit.Baseline.Passed &&
			len(audit.SkillSync.Errors) == 0 &&
			len(audit.MCPSync.Errors) == 0

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
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default ~/hairglasses-studio)"},
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
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default ~/hairglasses-studio)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					home, _ := os.UserHomeDir()
					scanPath = filepath.Join(home, "hairglasses-studio")
				}
				report := Audit(scanPath)
				return fmt.Sprintf("Fleet: %d repos, %d passed, %d failed",
					report.TotalRepos, report.Passed, report.Failed), nil
			},
		},
	}
}
