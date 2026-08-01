package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AuditReport struct {
	Timestamp     time.Time           `json:"timestamp"`
	WorkspaceRoot string              `json:"workspace_root"`
	Repos         []string            `json:"repos"`
	Skills        []string            `json:"skills"`
	Scripts       []string            `json:"scripts"`
	LLMSurfaces   map[string][]string `json:"llm_surfaces"`
}

func main() {
	var workspaceRoot string
	var outputJSON string
	var outputMD string

	flag.StringVar(&workspaceRoot, "workspace", "/home/hg/hairglasses-studio", "Workspace root to audit")
	flag.StringVar(&outputJSON, "json", "surface-audit.json", "JSON output file")
	flag.StringVar(&outputMD, "md", "surface-audit.md", "Markdown output file")
	flag.Parse()

	report := AuditReport{
		Timestamp:     time.Now(),
		WorkspaceRoot: workspaceRoot,
		LLMSurfaces:   make(map[string][]string),
	}

	// 1. Audit repos (directories in workspace root)
	entries, err := os.ReadDir(workspaceRoot)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				// Check if it's a git repo
				if _, err := os.Stat(filepath.Join(workspaceRoot, e.Name(), ".git")); err == nil {
					report.Repos = append(report.Repos, e.Name())
				}
			}
		}
	} else {
		log.Printf("Warning: failed to read workspace root: %v", err)
	}

	// 2. Audit skills
	skillsDir := filepath.Join(workspaceRoot, ".agents", "skills")
	if skillEntries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range skillEntries {
			if e.IsDir() {
				report.Skills = append(report.Skills, e.Name())
			}
		}
	}

	// 3. Audit scripts
	_ = filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != ".agents" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".sh") || strings.HasSuffix(d.Name(), ".py") {
			rel, _ := filepath.Rel(workspaceRoot, path)
			report.Scripts = append(report.Scripts, rel)
		}
		// 4. Audit LLM surfaces (claude, codex, agy, gemini)
		nameLower := strings.ToLower(d.Name())
		if nameLower == "claude.md" || nameLower == ".claude.json" || strings.Contains(path, "/.claude/") {
			report.LLMSurfaces["claude"] = append(report.LLMSurfaces["claude"], path)
		} else if nameLower == "codex.md" || nameLower == "agents.md" || nameLower == ".codex.json" || strings.Contains(path, "/.codex/") {
			report.LLMSurfaces["codex"] = append(report.LLMSurfaces["codex"], path)
		} else if nameLower == "gemini.md" || nameLower == ".gemini.json" || strings.Contains(path, "/.gemini/") {
			report.LLMSurfaces["gemini"] = append(report.LLMSurfaces["gemini"], path)
		} else if nameLower == "agy.md" || nameLower == ".agy.json" || strings.Contains(path, "/.agy/") {
			report.LLMSurfaces["agy"] = append(report.LLMSurfaces["agy"], path)
		}
		return nil
	})

	// Output JSON
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		_ = os.WriteFile(outputJSON, jsonBytes, 0644)
	}

	// Output Markdown
	mdBuilder := strings.Builder{}
	mdBuilder.WriteString(fmt.Sprintf("# Surface Audit Report\n\nGenerated: %s\nWorkspace: %s\n\n", report.Timestamp.Format(time.RFC3339), report.WorkspaceRoot))

	mdBuilder.WriteString(fmt.Sprintf("## Repositories (%d)\n", len(report.Repos)))
	for _, repo := range report.Repos {
		mdBuilder.WriteString(fmt.Sprintf("- %s\n", repo))
	}

	mdBuilder.WriteString(fmt.Sprintf("\n## Skills (%d)\n", len(report.Skills)))
	for _, skill := range report.Skills {
		mdBuilder.WriteString(fmt.Sprintf("- %s\n", skill))
	}

	mdBuilder.WriteString(fmt.Sprintf("\n## Scripts (%d)\n", len(report.Scripts)))
	for _, script := range report.Scripts {
		mdBuilder.WriteString(fmt.Sprintf("- %s\n", script))
	}

	mdBuilder.WriteString("\n## LLM Surfaces\n")
	for provider, surfaces := range report.LLMSurfaces {
		mdBuilder.WriteString(fmt.Sprintf("### %s (%d)\n", provider, len(surfaces)))
		for _, surface := range surfaces {
			mdBuilder.WriteString(fmt.Sprintf("- %s\n", surface))
		}
	}

	_ = os.WriteFile(outputMD, []byte(mdBuilder.String()), 0644)

	fmt.Printf("Audit completed successfully. Wrote to %s and %s\n", outputJSON, outputMD)
}
