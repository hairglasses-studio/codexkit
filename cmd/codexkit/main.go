package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/fleetaudit"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/workspace"
)

var registry *codexkit.Registry

func init() {
	registry = codexkit.NewRegistry()
	for _, m := range []codexkit.ToolModule{
		baselineguard.Module(),
		skillsync.Module(),
		mcpsync.Module(),
		fleetaudit.Module(),
	} {
		if err := registry.Register(m); err != nil {
			fmt.Fprintf(os.Stderr, "init error: %v\n", err)
			os.Exit(1)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "baseline":
		runBaseline(os.Args[2:])
	case "skills":
		runSkills(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "provider":
		runProvider(os.Args[2:])
	case "fleet":
		runFleet(os.Args[2:])
	case "workspace":
		runWorkspace(os.Args[2:])
	case "bridge":
		runBridge(os.Args[2:])
	case "tools":
		runTools()
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
  baseline check <repo|--all>   Run baseline-guard validation
  skills sync <repo>            Sync skills to .claude/skills/ and plugins/
  skills diff <repo>            Show what skill sync would change
  skills check <repo>           Fail when managed skill mirrors drift
  skills list <repo>            List skills from surface.yaml
  mcp sync <repo>               Sync .mcp.json to .codex/config.toml
  mcp diff <repo>               Show what MCP sync would change
  mcp check <repo>              Fail when the generated MCP block drifts
  mcp list <repo>               List MCP servers from .mcp.json
  provider check <repo>         Verify Claude/Gemini provider settings parity
  provider diff <repo>          Show provider settings drift without writing
  provider sync <repo>          Apply provider settings parity
  fleet audit [scan_path]       Run full audit on all repos
  fleet report [scan_path]      Summary report of fleet health
  workspace check [root]        Validate workspace/manifest.json and go.work
  workspace refresh-parity      Refresh docs parity outputs through codexkit-owned parity tooling
  bridge <subcommand>           Compatibility wrapper for legacy parity entrypoints
  tools                         List all registered tools
  help                          Show this help`)
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func runBaseline(args []string) {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all>")
		os.Exit(1)
	}

	jsonOut := hasFlag(args, "--json")
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
	} else if len(args) > 1 && args[1] != "--json" {
		paths = append(paths, args[1])
	} else {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all>")
		os.Exit(1)
	}

	allPassed := true
	var reports []baselineguard.Report
	for _, repoPath := range paths {
		report := baselineguard.Check(repoPath)
		reports = append(reports, report)
		repoName := filepath.Base(repoPath)

		if report.Passed {
			fmt.Printf("  %-20s PASS (%d checks)\n", repoName, report.Total)
		} else {
			allPassed = false
			fmt.Printf("  %-20s FAIL (%d/%d)\n", repoName, report.Failed, report.Total)
			for _, f := range report.Findings {
				if !f.Passed {
					fmt.Printf("    - %s: %s\n", f.Check, f.Message)
				}
			}
		}
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(reports)
	}

	if !allPassed {
		os.Exit(1)
	}
}

func runSkills(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: codexkit skills <sync|diff|check|list> <repo_path>")
		os.Exit(1)
	}

	cmd, repoPath := args[0], args[1]
	switch cmd {
	case "sync":
		report := skillsync.Sync(repoPath, false)
		printJSON(report)
	case "diff":
		report := skillsync.Diff(repoPath)
		printJSON(report)
	case "check":
		report := skillsync.Check(repoPath)
		for _, warning := range report.Warnings {
			fmt.Fprintln(os.Stderr, warning)
		}
		if len(report.Errors) > 0 {
			for _, err := range report.Errors {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
		if report.PendingChanges {
			for _, action := range report.Actions {
				if action.Action == "unchanged" {
					continue
				}
				fmt.Fprintln(os.Stderr, action.Message)
			}
			os.Exit(1)
		}
	case "list":
		names, err := skillsync.List(repoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, name := range names {
			fmt.Println(name)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown skills command: %s\n", cmd)
		os.Exit(1)
	}
}

func runMCP(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: codexkit mcp <sync|diff|check|list> <repo_path>")
		os.Exit(1)
	}

	cmd, repoPath := args[0], args[1]
	switch cmd {
	case "sync":
		report := mcpsync.Sync(repoPath, false)
		printJSON(report)
	case "diff":
		report := mcpsync.Diff(repoPath)
		printJSON(report)
	case "check":
		diffText, err := mcpsync.DiffText(repoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if diffText != "" {
			fmt.Fprint(os.Stdout, diffText)
			os.Exit(1)
		}
	case "list":
		names, err := mcpsync.List(repoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, name := range names {
			fmt.Println(name)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown mcp command: %s\n", cmd)
		os.Exit(1)
	}
}

func runProvider(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: codexkit provider <check|diff|sync> <repo_path> [--repo-name <name>] [--allow-dirty] [--include-codex-config]")
		os.Exit(1)
	}

	cmd, repoPath := args[0], args[1]
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	scriptArgs := []string{absRepoPath}
	if len(args) > 2 {
		scriptArgs = append(scriptArgs, args[2:]...)
	}
	switch cmd {
	case "check":
		scriptArgs = append(scriptArgs, "--check")
	case "diff":
		scriptArgs = append(scriptArgs, "--dry-run")
	case "sync":
		scriptArgs = append(scriptArgs, "--write")
	default:
		fmt.Fprintf(os.Stderr, "unknown provider command: %s\n", cmd)
		os.Exit(1)
	}
	if err := runCodexkitScript(findCodexkitRoot(absRepoPath), "provider-settings-sync.sh", scriptArgs...); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runFleet(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit fleet <audit|report> [scan_path]")
		os.Exit(1)
	}

	scanPath := ""
	if len(args) > 1 {
		scanPath = args[1]
	}
	if scanPath == "" {
		home, _ := os.UserHomeDir()
		scanPath = filepath.Join(home, "hairglasses-studio")
	}

	switch args[0] {
	case "audit":
		report := fleetaudit.Audit(scanPath)
		printJSON(report)
	case "report":
		report := fleetaudit.Audit(scanPath)
		fmt.Printf("Fleet: %d repos, %d passed, %d failed\n",
			report.TotalRepos, report.Passed, report.Failed)
		for _, repo := range report.Repos {
			status := "PASS"
			if !repo.Passed {
				status = "FAIL"
			}
			fmt.Printf("  %-20s %s\n", repo.RepoName, status)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown fleet command: %s\n", args[0])
		os.Exit(1)
	}
}

func runWorkspace(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit workspace <check|refresh-parity> ...")
		os.Exit(1)
	}

	switch args[0] {
	case "check":
		jsonOut := hasFlag(args, "--json")
		root := workspace.DefaultRoot()
		for _, arg := range args[1:] {
			if arg != "--json" {
				root = arg
				break
			}
		}

		manifest, err := workspace.LoadManifest(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		report := workspace.Check(root, manifest)
		if jsonOut {
			printJSON(report)
		} else {
			if report.Passed {
				fmt.Printf("workspace check: PASS (%d findings)\n", len(report.Findings))
			} else {
				fmt.Printf("workspace check: FAIL (%d findings)\n", len(report.Findings))
			}
			for _, finding := range report.Findings {
				status := "PASS"
				if !finding.Passed {
					status = "FAIL"
				}
				if finding.Repo != "" {
					fmt.Printf("  %-16s %-20s %s\n", status, finding.Check, finding.Repo)
					if finding.Message != "" {
						fmt.Printf("    %s\n", finding.Message)
					}
					continue
				}
				fmt.Printf("  %-16s %-20s %s\n", status, finding.Check, finding.Message)
			}
		}

		if !report.Passed {
			os.Exit(1)
		}
	case "refresh-parity":
		if err := runWorkspaceRefresh(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: codexkit workspace <check|refresh-parity> ...")
		os.Exit(1)
	}
}

func runTools() {
	tools := registry.ListTools()
	for _, t := range tools {
		fmt.Printf("  %-24s %s\n", t.Name, t.Description)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
