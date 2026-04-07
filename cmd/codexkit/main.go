package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hairglasses-studio/codexkit/baselineguard"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "baseline":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path>")
			os.Exit(1)
		}
		runBaseline(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`codexkit — Codex fleet management toolkit

Commands:
  baseline check <repo>   Run baseline-guard validation on a repo
  baseline check --all    Run on all repos in ~/hairglasses-studio
  help                    Show this help`)
}

func runBaseline(args []string) {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all>")
		os.Exit(1)
	}

	var paths []string
	if len(args) > 1 && args[1] == "--all" {
		home, _ := os.UserHomeDir()
		studioDir := filepath.Join(home, "hairglasses-studio")
		entries, err := os.ReadDir(studioDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", studioDir, err)
			os.Exit(1)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			repoPath := filepath.Join(studioDir, entry.Name())
			if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
				paths = append(paths, repoPath)
			}
		}
	} else if len(args) > 1 {
		paths = append(paths, args[1])
	} else {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all>")
		os.Exit(1)
	}

	allPassed := true
	for _, repoPath := range paths {
		report := baselineguard.Check(repoPath)
		repoName := filepath.Base(repoPath)

		if report.Passed {
			fmt.Printf("  %-16s ✅ PASS (%d checks)\n", repoName, report.Total)
		} else {
			allPassed = false
			fmt.Printf("  %-16s ❌ FAIL (%d/%d)\n", repoName, report.Failed, report.Total)
			for _, f := range report.Findings {
				if !f.Passed {
					fmt.Printf("    - %s: %s\n", f.Check, f.Message)
				}
			}
		}
	}

	// JSON output if --json flag
	for _, arg := range args {
		if arg == "--json" {
			reports := make([]baselineguard.Report, 0, len(paths))
			for _, p := range paths {
				reports = append(reports, baselineguard.Check(p))
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(reports)
			break
		}
	}

	if !allPassed {
		os.Exit(1)
	}
}

func init() {
	// Suppress unused import warning
	_ = strings.TrimSpace
}
