package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/sourcecontract"
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
	var checkMode bool

	flag.StringVar(&workspaceRoot, "workspace", "/home/hg/hairglasses-studio", "Workspace root to audit")
	flag.StringVar(&outputJSON, "json", "surface-audit.json", "JSON output file")
	flag.StringVar(&outputMD, "md", "surface-audit.md", "Markdown output file")
	flag.BoolVar(&checkMode, "check", false, "Run continuous unification compliance checks")
	flag.Parse()

	if checkMode {
		if !runComplianceChecks(workspaceRoot) {
			os.Exit(1)
		}
		os.Exit(0)
	}

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

func runComplianceChecks(workspaceRoot string) bool {
	fmt.Println("=== Studio Continuous Unification Compliance Checks ===")
	passed := true

	// 1. Schema drift via codexkit source-contract-check
	fmt.Println("--> Running Source Contract Check...")
	report, err := sourcecontract.Check(workspaceRoot, sourcecontract.CheckOptions{})
	if err != nil {
		fmt.Printf("[FAIL] Source contract check error: %v\n", err)
		passed = false
	} else if !report.Passed {
		fmt.Println("[FAIL] Source contract check failed:")
		if !report.Workspace.Passed {
			fmt.Printf("  Workspace check failed (%d findings):\n", len(report.Workspace.Findings))
			for _, f := range report.Workspace.Findings {
				if !f.Passed {
					fmt.Printf("    - [%s] %s: %s\n", f.Check, f.Repo, f.Message)
				}
			}
		}
		if report.RuntimeInventory != nil && !report.RuntimeInventory.Passed {
			fmt.Println("  Runtime inventory check failed:")
			for _, f := range report.RuntimeInventory.Findings {
				if !f.Passed {
					fmt.Printf("    - [%s] %s\n", f.Check, f.Message)
				}
			}
		}
		if report.GlobalProjection != nil && !report.GlobalProjection.Passed {
			fmt.Println("  Global projection check failed:")
			for _, f := range report.GlobalProjection.Findings {
				if !f.Passed {
					fmt.Printf("    - [%s] %s\n", f.Check, f.Message)
				}
			}
		}
		for _, repo := range report.Repos {
			if !repo.Passed {
				fmt.Printf("  Repo check failed for %s:\n", repo.Repo)
				for _, e := range repo.Errors {
					fmt.Printf("    - %s\n", e)
				}
				if repo.SkillSync != nil {
					for _, e := range repo.SkillSync.Errors {
						fmt.Printf("    - skill: %s\n", e)
					}
					for _, a := range repo.SkillSync.Actions {
						if a.Action == "update" || a.Action == "create" {
							fmt.Printf("    - skill action: %s %s\n", a.Action, a.DstPath)
						}
					}
				}
				if repo.MCPSync != nil {
					for _, e := range repo.MCPSync.Errors {
						fmt.Printf("    - mcp: %s\n", e)
					}
				}
			}
		}
		passed = false
	} else {
		fmt.Println("[PASS] Source contract check passed")
	}

	// 2. Skill symlinks integrity check
	fmt.Println("--> Running Skill Symlinks Integrity Check...")
	if !checkSkillSymlinks(workspaceRoot) {
		passed = false
	} else {
		fmt.Println("[PASS] Skill symlinks integrity check passed")
	}

	// 3. Gofmt compliance check
	fmt.Println("--> Running Gofmt Compliance Check...")
	if !checkGofmtCompliance(workspaceRoot) {
		passed = false
	} else {
		fmt.Println("[PASS] Gofmt compliance check passed")
	}

	if passed {
		fmt.Println("=== ALL COMPLIANCE CHECKS PASSED ===")
	} else {
		fmt.Println("=== COMPLIANCE CHECKS FAILED ===")
	}

	return passed
}

func checkSkillSymlinks(workspaceRoot string) bool {
	passed := true
	symlinksChecked := 0

	// Find all repo directories + workspaceRoot
	dirsToScan := []string{workspaceRoot}
	entries, err := os.ReadDir(workspaceRoot)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vault" && e.Name() != "imported" {
				dirsToScan = append(dirsToScan, filepath.Join(workspaceRoot, e.Name()))
			}
		}
	}

	skillProviderDirs := []string{".claude/skills", ".codex/skills", ".gemini/skills"}
	for _, baseDir := range dirsToScan {
		for _, providerDir := range skillProviderDirs {
			targetDir := filepath.Join(baseDir, providerDir)
			if _, serr := os.Stat(targetDir); serr != nil {
				continue
			}

			_ = filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, ferr error) error {
				if ferr != nil {
					return nil
				}

				info, lerr := os.Lstat(path)
				if lerr != nil {
					return nil
				}

				if info.Mode()&os.ModeSymlink != 0 {
					symlinksChecked++
					linkTarget, rerr := os.Readlink(path)
					if rerr != nil {
						fmt.Printf("[FAIL] Broken symlink read error: %s: %v\n", path, rerr)
						passed = false
						return nil
					}

					resolvedTarget := linkTarget
					if !filepath.IsAbs(linkTarget) {
						resolvedTarget = filepath.Join(filepath.Dir(path), linkTarget)
					}

					if _, serr := os.Stat(resolvedTarget); serr != nil {
						fmt.Printf("[FAIL] Broken skill symlink: %s -> %s (target does not exist)\n", path, linkTarget)
						passed = false
					}
				}
				return nil
			})
		}
	}

	if symlinksChecked > 0 {
		fmt.Printf("    Verified %d skill symlinks\n", symlinksChecked)
	}
	return passed
}

func checkGofmtCompliance(workspaceRoot string) bool {
	excludedDirs := map[string]bool{
		".git":                 true,
		"vault":                true,
		"imported":             true,
		".codex-worktrees":     true,
		".graveyard":           true,
		"vendor":               true,
		".tools":               true,
		"node_modules":         true,
		"git-cleanup-archives": true,
		"config":               true,
		"data":                 true,
		"docs":                 true,
		"reports":              true,
		"deploy":               true,
		".cache":               true,
		".npm":                 true,
		".venv":                true,
		"__pycache__":          true,
		"dist":                 true,
		"build":                true,
	}

	passed := true
	filesChecked := 0

	err := filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if excludedDirs[name] {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") && name != ".agents" && path != workspaceRoot {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasSuffix(d.Name(), ".go") {
			filesChecked++
			content, rerr := os.ReadFile(path)
			if rerr != nil {
				fmt.Printf("[FAIL] Could not read Go file %s: %v\n", path, rerr)
				passed = false
				return nil
			}

			formatted, ferr := format.Source(content)
			if ferr != nil {
				fmt.Printf("[FAIL] gofmt error in %s: %v\n", path, ferr)
				passed = false
				return nil
			}

			if !bytes.Equal(content, formatted) {
				rel, _ := filepath.Rel(workspaceRoot, path)
				fmt.Printf("[FAIL] Unformatted Go file: %s\n", rel)
				passed = false
			}
		}

		return nil
	})

	if err != nil {
		fmt.Printf("[FAIL] Gofmt walk error: %v\n", err)
		return false
	}

	if filesChecked > 0 {
		fmt.Printf("    Checked %d Go source files for formatting\n", filesChecked)
	}
	return passed
}
