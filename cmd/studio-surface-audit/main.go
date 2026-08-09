package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flagSet := flag.NewFlagSet("studio-surface-audit", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	var workspaceRoot string
	var outputJSON string
	var outputMD string
	var checkMode bool

	flagSet.StringVar(&workspaceRoot, "workspace", ".", "Workspace root to audit")
	flagSet.StringVar(&outputJSON, "json", "surface-audit.json", "JSON output file")
	flagSet.StringVar(&outputMD, "md", "surface-audit.md", "Markdown output file")
	flagSet.BoolVar(&checkMode, "check", false, "Run continuous unification compliance checks")

	if err := flagSet.Parse(args); err != nil {
		return 2
	}

	if flagSet.NArg() > 0 {
		fmt.Fprintf(stderr, "error: unexpected arguments\n")
		return 2
	}

	if checkMode {
		if !runComplianceChecks(workspaceRoot) {
			return 1
		}
		return 0
	}

	report, err := collectAudit(workspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "error collecting audit: %v\n", err)
		return 1
	}

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "error encoding JSON report: %v\n", err)
		return 1
	}
	if err := writeReportFile(outputJSON, jsonBytes); err != nil {
		fmt.Fprintf(stderr, "error writing JSON report: %v\n", err)
		return 1
	}

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

	if err := writeReportFile(outputMD, []byte(mdBuilder.String())); err != nil {
		fmt.Fprintf(stderr, "error writing Markdown report: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Audit completed successfully. Wrote to %s and %s\n", outputJSON, outputMD)
	return 0
}

func writeReportFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func collectAudit(workspaceRoot string) (AuditReport, error) {
	report := AuditReport{
		Timestamp:     time.Now(),
		WorkspaceRoot: workspaceRoot,
		LLMSurfaces:   make(map[string][]string),
	}

	manifestPath := filepath.Join(workspaceRoot, "workspace", "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m struct {
			Repos []struct {
				Name string `json:"name"`
			} `json:"repos"`
		}
		if err := json.Unmarshal(data, &m); err == nil {
			for _, r := range m.Repos {
				if r.Name != "" {
					report.Repos = append(report.Repos, r.Name)
				}
			}
		}
	}

	if len(report.Repos) == 0 {
		entries, err := os.ReadDir(workspaceRoot)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vault" && e.Name() != "imported" {
					if _, err := os.Stat(filepath.Join(workspaceRoot, e.Name(), ".git")); err == nil {
						report.Repos = append(report.Repos, e.Name())
					}
				}
			}
		}
	}

	skillsDir := filepath.Join(workspaceRoot, ".agents", "skills")
	if skillEntries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range skillEntries {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				report.Skills = append(report.Skills, e.Name())
			}
		}
	}

	skipDirs := map[string]bool{
		"imported":             true,
		"vault":                true,
		".codex-worktrees":     true,
		".graveyard":           true,
		"node_modules":         true,
		"vendor":               true,
		".cache":               true,
		"git-cleanup-archives": true,
		"worktrees":            true,
		".worktrees":           true,
	}

	_ = filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			if len(report.Repos) > 0 && path != workspaceRoot {
				rel, _ := filepath.Rel(workspaceRoot, path)
				parts := strings.Split(filepath.ToSlash(rel), "/")
				if len(parts) == 1 {
					topDir := parts[0]
					if !strings.HasPrefix(topDir, ".") && topDir != "scripts" {
						isRepo := false
						for _, r := range report.Repos {
							if r == topDir {
								isRepo = true
								break
							}
						}
						if !isRepo {
							return filepath.SkipDir
						}
					}
				}
			}
			if strings.HasPrefix(name, ".") && name != ".agents" && name != ".claude" && name != ".codex" && name != ".gemini" && name != ".agy" && path != workspaceRoot {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(workspaceRoot, path)

		if strings.HasSuffix(name, ".sh") || strings.HasSuffix(name, ".py") {
			report.Scripts = append(report.Scripts, rel)
		}

		nameLower := strings.ToLower(name)
		if nameLower == "claude.md" || nameLower == ".claude.json" || strings.Contains(path, "/.claude/") {
			report.LLMSurfaces["claude"] = append(report.LLMSurfaces["claude"], rel)
		} else if nameLower == "codex.md" || nameLower == "agents.md" || nameLower == ".codex.json" || strings.Contains(path, "/.codex/") {
			report.LLMSurfaces["codex"] = append(report.LLMSurfaces["codex"], rel)
		} else if nameLower == "gemini.md" || nameLower == ".gemini.json" || strings.Contains(path, "/.gemini/") {
			report.LLMSurfaces["gemini"] = append(report.LLMSurfaces["gemini"], rel)
		} else if nameLower == "agy.md" || nameLower == ".agy.json" || strings.Contains(path, "/.agy/") {
			report.LLMSurfaces["agy"] = append(report.LLMSurfaces["agy"], rel)
		}

		return nil
	})

	return report, nil
}

func runComplianceChecks(workspaceRoot string) bool {
	fmt.Println("=== Studio Continuous Unification Compliance Checks ===")
	passed := true

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

	fmt.Println("--> Running Skill Symlinks Integrity Check...")
	if !checkSkillSymlinks(workspaceRoot) {
		passed = false
	} else {
		fmt.Println("[PASS] Skill symlinks integrity check passed")
	}

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

	dirsToScan := []string{workspaceRoot}
	entries, err := os.ReadDir(workspaceRoot)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vault" && e.Name() != "imported" {
				dirsToScan = append(dirsToScan, filepath.Join(workspaceRoot, e.Name()))
			}
		}
	}

	skillProviderDirs := []string{".claude/skills", ".codex/skills", ".gemini/skills", ".agents/skills"}
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

func checkGofmtCompliance(workspaceRoot string, out ...io.Writer) bool {
	var w io.Writer = os.Stdout
	if len(out) > 0 && out[0] != nil {
		w = out[0]
	}

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
		".data":                true,
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
				fmt.Fprintf(w, "[FAIL] Could not read Go file %s: %v\n", path, rerr)
				passed = false
				return nil
			}

			formatted, ferr := format.Source(content)
			if ferr != nil {
				fmt.Fprintf(w, "[FAIL] gofmt error in %s: %v\n", path, ferr)
				passed = false
				return nil
			}

			if !bytes.Equal(content, formatted) {
				rel, _ := filepath.Rel(workspaceRoot, path)
				fmt.Fprintf(w, "[FAIL] Unformatted Go file: %s\n", rel)
				passed = false
			}
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(w, "[FAIL] Gofmt walk error: %v\n", err)
		return false
	}

	if filesChecked > 0 {
		fmt.Fprintf(w, "    Checked %d Go source files for formatting\n", filesChecked)
	}
	return passed
}
