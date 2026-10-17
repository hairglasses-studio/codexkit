package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/builtinmodules"
	"github.com/hairglasses-studio/codexkit/configindex"
	"github.com/hairglasses-studio/codexkit/fleetaudit"
	"github.com/hairglasses-studio/codexkit/llmreduction"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/perfaudit"
	"github.com/hairglasses-studio/codexkit/primitiveindex"
	"github.com/hairglasses-studio/codexkit/reporeadiness"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/sourcecontract"
	"github.com/hairglasses-studio/codexkit/surfaceindex"
	"github.com/hairglasses-studio/codexkit/surfaceparity"
	"github.com/hairglasses-studio/codexkit/telemetry"
	"github.com/hairglasses-studio/codexkit/unificationaudit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

var registry *codexkit.Registry

func init() {
	registry = codexkit.NewRegistry()
	for _, m := range builtinmodules.All() {
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
	case "perf":
		runPerf(os.Args[2:])
	case "repo-readiness":
		runRepoReadiness(os.Args[2:])
	case "unification":
		runUnification(os.Args[2:])
	case "parity":
		runParity(os.Args[2:])
	case "reduction":
		runReduction(os.Args[2:])
	case "telemetry":
		runTelemetry(os.Args[2:])
	case "research":
		runResearch(os.Args[2:])
	case "source-ref":
		runSourceRef(os.Args[2:])
	case "harness":
		runHarness(os.Args[2:])
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
  baseline check <repo|--all> [--overlay <file>]
                                Run baseline-guard validation
  skills sync <repo>            Sync skills to .claude/skills/ and plugins/
  skills diff <repo>            Show what skill sync would change
  skills check <repo>           Fail when managed skill mirrors drift
  skills list <repo>            List skills from surface.yaml
  skills resources <src> <dst> [--apply --expected-plan=SHA256]
                                Plan or add missing support-directory links
  mcp sync [repo|--all]         Sync .mcp.json to .codex/config.toml
  mcp diff [repo|--all]         Show what MCP sync would change
  mcp check [repo|--all]        Fail when the generated MCP block drifts
  mcp list [repo|--all]         List MCP servers from .mcp.json
  provider check <repo>         Verify legacy provider settings during migration
  provider diff <repo>          Show legacy provider settings drift without writing
  provider sync <repo>          Apply legacy provider settings parity during migration
  fleet audit [scan_path]       Run full audit on all repos
  fleet report [scan_path]      Summary report of fleet health
  workspace check [root] [--overlay <file>]
                                Validate workspace/manifest.json and go.work
  workspace generate-manifest [root] [--write|--json]
                                Generate workspace/manifest.json from live repos and docs metadata
  workspace runtime-inventory [root] [--json] [--json-out <path>] [--markdown-out <path>] [--policy <path>]
                                Build manifest-backed MCP runtime projection inventory
  workspace runtime-inventory-check [root] [--json] [--json-path <path>] [--markdown-path <path>] [--policy <path>] [--allow-skipped <repo:server>] [--allow-any-skipped] [--skip-artifacts]
                                Fail when the runtime projection or generated inventory artifacts drift
  workspace global-mcp-projection [root] [--json] [--json-out <path>] [--markdown-out <path>] [--policy <path>]
                                Build the workspace-global MCP provider projection
  workspace global-mcp-projection-check [root] [--json] [--json-path <path>] [--markdown-path <path>] [--policy <path>] [--skip-artifacts]
                                Fail when generated global MCP projection artifacts drift
  workspace global-mcp-sync [root] [--json] [--check|--dry-run] [--claude-json <path>] [--claude-project-key <key>] [--codex-config <path>] [--gemini-settings <path>] [--policy <path>]
                                 Sync workspace-global Claude, Codex, and Gemini MCP overlays
  workspace diff-preview --left <path> --right <path> --rel <path> --kind <kind> [--lines <n>]
                                 Render one JSON diff-preview record matching the dotfiles manual projection contract
  workspace source-contract-check [root] [--json] [--json-out <path>] [--json-path <path>] [--skills-only|--tools-only] [--skip-runtime-inventory] [--skill-validator auto|host|pinned|off] [--overlay <file>]
                                 Fail when repo-controlled workspace, skill, MCP, runtime inventory, or global MCP projection sources drift
  workspace surface-index [root] [--json] [--json-out <path>] [--markdown-out <path>] [--skill-validator auto|host|pinned|off]
                                Build a baseline repo agent surface index
  workspace surface-index-check [root] [--json] [--json-path <path>] [--markdown-path <path>] [--skill-validator auto|host|pinned|off] [--skip-artifacts]
                                Fail when generated repo surface index artifacts drift
  workspace primitive-index [root] [--json] [--json-out <path>] [--markdown-out <path>]
                                Build a workspace index of hooks, provider agents, plugin manifests, nested MCP files, and related agent primitives
  workspace primitive-index-check [root] [--json] [--json-path <path>] [--markdown-path <path>] [--skip-artifacts] [--overlay <file>]
                                Fail when generated workspace agent primitive artifacts drift
  workspace config-index [root] [--json] [--json-out <path>] [--markdown-out <path>] [--user-home <path>] [--root-home <path>] [--runtime-stats]
                                Inventory and classify repo, dotfiles, Claude, Codex, AGY, user-home, and root-home configuration
  workspace config-index-check [root] [--json] [--user-home <path>] [--root-home <path>] [--runtime-stats]
                                Enforce strict Claude/Codex/AGY ownership and least-privilege configuration policy
  workspace refresh-parity      Refresh docs parity outputs through codexkit-owned parity tooling
	  perf audit [root] [--json] [--all-scopes]
	                                Scan the workspace for Codex performance bottlenecks
	  perf report [root] [--all-scopes]
	                                Print the Codex performance audit as Markdown
	  repo-readiness score [root] [--json] [--all-scopes]
	                                Score repos for autonomous mutation readiness
	  unification audit [root] [--json] [--all-scopes]
	                                Scan the workspace for shell, hook, and skill-source unification candidates
  unification report [root] [--all-scopes]
                                 Print the codebase unification audit as Markdown
  unification cycle [root] [--json] [--all-scopes] [--write] [--notes-dir <path>] [--previous-notes <path>] [--cycle-id <id>] [--require-notes-applied] [--ack-carry-forward <reason>]
                                 Generate a repeatable unification cycle note; defaults to the latest prior note for carry-forward checks
  unification shell-file <repo> <path> [--json]
                                 Inventory functions, entrypoints, and obvious callers for one shell file
  parity audit [root] [--json] [--scope <scope>]
                                 Run multi-repo surface parity audit across 74+ workspaces
  parity sync [root] [--json] [--scope <scope>]
                                 Automatically synchronize and repair surface files across workspaces
  parity report [root] [--scope <scope>]
                                 Print the surface parity audit as Markdown
  reduction <audit|dedup|plan|apply|verify> [root] [--json] [--all-scopes] [--max-repos <n>] [--limit <n>] [--execute]
                                 Run LLM-surface reduction ranking and tranche planning helpers
  telemetry record [--event-type <type>] [--repo <repo>] [--provider <p>] [--model <m>] [--status <s>] [--decision <d>]
                                 Record a Codex routing, model selection, or verification event
  telemetry list [--event-type <type>] [--repo <repo>] [--status <s>] [--since <dur|time>] [--limit <n>] [--json]
                                 List and filter recorded telemetry events
  telemetry search <query> [--limit <n>] [--json]
                                 Search recorded telemetry events across all fields
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

// takeFlagValue extracts a "--flag value" pair from args, returning the value,
// the remaining args with that pair removed, and whether the flag was present
// at all. err is set when the flag appears without a following value.
func takeFlagValue(args []string, flag string) (value string, rest []string, present bool, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			present = true
			if i+1 >= len(args) {
				return "", nil, true, fmt.Errorf("%s requires a value", flag)
			}
			value = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return value, rest, present, nil
}

func runBaseline(args []string) {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all>")
		os.Exit(1)
	}

	overlayPath, args, _, err := takeFlagValue(args, "--overlay")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	jsonOut := hasFlag(args, "--json")
	var paths []string

	if len(args) > 1 && args[1] == "--all" {
		home, _ := os.UserHomeDir()
		studioDir := filepath.Join(home, "hairglasses-studio")
		discovered, err := baselineguard.DiscoverWorkspaceTargetsWithOverlay(studioDir, overlayPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error discovering baseline targets in %s: %v\n", studioDir, err)
			os.Exit(1)
		}
		paths = append(paths, discovered...)
	} else if len(args) > 1 && args[1] != "--json" {
		paths = append(paths, args[1])
	} else {
		fmt.Fprintln(os.Stderr, "usage: codexkit baseline check <repo_path|--all> [--overlay <file>]")
		os.Exit(1)
	}

	allPassed := true
	reports := make([]baselineguard.Report, len(paths))
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func(idx int, repoPath string) {
			defer wg.Done()
			reports[idx] = baselineguard.Check(repoPath)
		}(i, p)
	}
	wg.Wait()

	for i, repoPath := range paths {
		report := reports[i]
		repoName := filepath.Base(repoPath)

		status := "pass"
		if !report.Passed {
			status = "fail"
		}
		_, _ = telemetry.Record("", telemetry.Event{
			EventType: telemetry.EventTypeVerification,
			Repo:      repoPath,
			Provider:  "codex",
			CheckType: "baseline",
			Status:    status,
			Findings:  report.Failed,
			Metadata: map[string]any{
				"total_checks":  report.Total,
				"failed_checks": report.Failed,
			},
		})

		if jsonOut {
			if !report.Passed {
				allPassed = false
			}
			continue
		}

		if report.Passed {
			fmt.Printf("  %-20s PASS (%d checks)\n", repoName, report.Total)
		} else {
			allPassed = false
			fmt.Printf("  %-20s FAIL (%d/%d)\n", repoName, report.Failed, report.Total)
			var failedFindings []baselineguard.Finding
			for _, f := range report.Findings {
				if !f.Passed {
					failedFindings = append(failedFindings, f)
					fmt.Printf("    - %s: %s\n", f.Check, f.Message)
				}
			}
			for _, hint := range baselineFailureHints(repoPath, failedFindings) {
				fmt.Printf("    hint: %s\n", hint)
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

func baselineFailureHints(repoPath string, failures []baselineguard.Finding) []string {
	if len(failures) == 0 {
		return nil
	}
	if absRepoPath, err := filepath.Abs(repoPath); err == nil {
		repoPath = absRepoPath
	}

	checks := make(map[string]struct{})
	for _, failure := range failures {
		checks[failure.Check] = struct{}{}
	}

	commandPrefix := fmt.Sprintf("cd %s && GOWORK=off go run ./cmd/codexkit", shellQuote(findCodexkitRoot(repoPath)))
	hints := []string{
		fmt.Sprintf("recheck this repo: %s baseline check %s", commandPrefix, shellQuote(repoPath)),
	}
	if structuredHints := structuredBaselineFailureHints(commandPrefix, failures); len(structuredHints) > 0 {
		return append(hints, structuredHints...)
	}

	add := func(checksToMatch []string, hint string) {
		for _, check := range checksToMatch {
			if _, ok := checks[check]; ok {
				hints = append(hints, hint)
				return
			}
		}
	}

	add([]string{"skill_sync"}, fmt.Sprintf("regenerate skill mirrors: %s skills sync %s --quiet-warnings", commandPrefix, shellQuote(repoPath)))
	add([]string{"mcp_sync"}, fmt.Sprintf("regenerate MCP config: %s mcp sync %s", commandPrefix, shellQuote(repoPath)))
	add([]string{"required_file", "canonical_agents", "canonical_claude"}, "restore canonical instruction files: AGENTS.md and CLAUDE.md")
	add([]string{"claude_settings_json", "agy_hooks_json"}, "repair the provider-native JSON configuration")
	add([]string{"agy_agent_layout"}, "move AGY agents to .agents/agents/<name>/agent.md")
	add([]string{"codex_config_toml", "project_local_profiles"}, "edit .codex/config.toml; keep repo-local configs parseable and remove unsupported [profiles.*] tables")
	add([]string{"agent_naming"}, "rename .codex/agents/*.toml files to underscore_case")
	add([]string{"skill_surface", "skill_file", "skill_portability"}, "fix the canonical .agents/skills surface before regenerating provider mirrors")
	add([]string{"sync_wrapper_portability"}, "make repo-local sync wrappers path-stable and run Go helpers with GOWORK=off")
	add([]string{"mcp_portability"}, "make active .mcp.json and generated Codex MCP launchers use portable absolute commands and cwd values")
	add([]string{"mcp_discovery"}, "publish or update .well-known/mcp.json for active HTTP MCP servers")
	add([]string{"a2a_awareness"}, "fix .well-known/agent.json so the Agent2Agent metadata is valid")

	return hints
}

func structuredBaselineFailureHints(commandPrefix string, failures []baselineguard.Finding) []string {
	var hints []string
	seen := make(map[string]struct{})
	for _, failure := range failures {
		for _, remediation := range failure.Remediation {
			hint := remediation.Message
			if len(remediation.Command) > 0 {
				hint = fmt.Sprintf(
					"%s: %s",
					remediation.Message,
					renderBaselineRemediationCommand(commandPrefix, remediation.Command),
				)
			}
			if _, ok := seen[hint]; ok {
				continue
			}
			seen[hint] = struct{}{}
			hints = append(hints, hint)
		}
	}
	return hints
}

func renderBaselineRemediationCommand(commandPrefix string, command []string) string {
	if len(command) == 0 {
		return ""
	}
	if command[0] == "codexkit" {
		return commandPrefix + " " + shellJoin(command[1:])
	}
	return shellJoin(command)
}

func shellJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, shellArg(arg))
	}
	return strings.Join(parts, " ")
}

func shellArg(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune("_-./:=@+", r) {
			continue
		}
		return shellQuote(value)
	}
	return value
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runSkills(args []string) {
	if len(args) < 1 {
		printSkillsUsage()
		os.Exit(1)
	}

	cmd := args[0]
	if cmd == "resources" {
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: codexkit skills resources ABS_SOURCE_SKILL ABS_TARGET_SKILL [--apply --expected-plan=SHA256]")
			os.Exit(2)
		}
		apply, expected := false, ""
		for _, arg := range args[3:] {
			switch {
			case arg == "--apply":
				apply = true
			case strings.HasPrefix(arg, "--expected-plan="):
				expected = strings.TrimPrefix(arg, "--expected-plan=")
			default:
				fmt.Fprintf(os.Stderr, "unknown resource flag: %s\n", arg)
				os.Exit(2)
			}
		}
		report := skillsync.SyncResources(args[1], args[2], apply, expected)
		printJSON(report)
		if len(report.Errors) != 0 {
			os.Exit(1)
		}
		return
	}
	repoPath := ""
	quietWarnings := os.Getenv("CODEXKIT_QUIET_WARNINGS") == "1"
	validatorMode := skillsync.ValidatorAuto
	for _, arg := range args[1:] {
		switch {
		case arg == "--quiet-warnings":
			quietWarnings = true
		case arg == "--skill-validator":
			fmt.Fprintln(os.Stderr, "--skill-validator requires a value; use --skill-validator=auto|host|pinned|off")
			os.Exit(1)
		case strings.HasPrefix(arg, "--skill-validator="):
			mode, err := skillsync.ParseValidatorMode(strings.TrimPrefix(arg, "--skill-validator="))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			validatorMode = mode
		default:
			if strings.HasPrefix(arg, "-") && arg != "--all" {
				fmt.Fprintf(os.Stderr, "unknown skills flag: %s\n", arg)
				os.Exit(1)
			}
			if repoPath != "" {
				fmt.Fprintf(os.Stderr, "unexpected extra skills argument: %s\n", arg)
				os.Exit(1)
			}
			repoPath = arg
		}
	}

	if repoPath == "" || repoPath == "--all" || repoPath == "all" {
		home, _ := os.UserHomeDir()
		studioDir := filepath.Join(home, "hairglasses-studio")
		if root := os.Getenv("HAIRGLASSES_STUDIO_ROOT"); root != "" {
			studioDir = root
		}
		paths, err := baselineguard.DiscoverWorkspaceTargets(studioDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error discovering baseline targets in %s: %v\n", studioDir, err)
			os.Exit(1)
		}

		switch cmd {
		case "sync":
			var reports []skillsync.SyncReport
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".agents", "skills", "surface.yaml")); err != nil {
					reports = append(reports, skillsync.SyncReport{
						RepoPath: p,
						Actions: []skillsync.SyncAction{
							{Action: "unchanged", Message: "no .agents/skills/surface.yaml defined; nothing to sync"},
						},
					})
					continue
				}
				reports = append(reports, skillsync.Sync(p, false))
			}
			printJSON(reports)
		case "diff":
			var reports []skillsync.SyncReport
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".agents", "skills", "surface.yaml")); err != nil {
					reports = append(reports, skillsync.SyncReport{
						RepoPath: p,
						DryRun:   true,
						Actions: []skillsync.SyncAction{
							{Action: "unchanged", Message: "no .agents/skills/surface.yaml defined; nothing to sync"},
						},
					})
					continue
				}
				reports = append(reports, skillsync.Diff(p))
			}
			printJSON(reports)
		case "check":
			allClean := true
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".agents", "skills", "surface.yaml")); err != nil {
					continue
				}
				report := skillsync.CheckWithOptions(p, skillsync.Options{ValidatorMode: validatorMode})
				if !quietWarnings {
					for _, warning := range report.Warnings {
						fmt.Fprintf(os.Stderr, "[%s] %s\n", filepath.Base(p), warning)
					}
				}
				if len(report.Errors) > 0 {
					allClean = false
					for _, err := range report.Errors {
						fmt.Fprintf(os.Stderr, "[%s] %s\n", filepath.Base(p), err)
					}
				}
				if report.PendingChanges {
					allClean = false
					for _, action := range report.Actions {
						if action.Action == "unchanged" {
							continue
						}
						fmt.Fprintf(os.Stderr, "[%s] %s\n", filepath.Base(p), action.Message)
					}
				}
			}
			if !allClean {
				os.Exit(1)
			}
		case "list":
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".agents", "skills", "surface.yaml")); err != nil {
					continue
				}
				names, err := skillsync.List(p)
				if err != nil {
					continue
				}
				fmt.Printf("[%s]\n", filepath.Base(p))
				for _, name := range names {
					fmt.Printf("  %s\n", name)
				}
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown skills command: %s\n", cmd)
			os.Exit(1)
		}
		return
	}

	switch cmd {
	case "sync":
		report := skillsync.Sync(repoPath, false)
		printJSON(report)
	case "diff":
		report := skillsync.Diff(repoPath)
		printJSON(report)
	case "check":
		report := skillsync.CheckWithOptions(repoPath, skillsync.Options{ValidatorMode: validatorMode})
		if !quietWarnings {
			for _, warning := range report.Warnings {
				fmt.Fprintln(os.Stderr, warning)
			}
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

func printSkillsUsage() {
	fmt.Fprintln(os.Stderr, "usage: codexkit skills <sync|diff|check|list> [repo_path|--all] [--quiet-warnings] [--skill-validator=auto|host|pinned|off]")
}

func runMCP(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: codexkit mcp <sync|diff|check|list> [repo_path|--all]")
		os.Exit(1)
	}

	cmd := args[0]
	repoPath := ""
	if len(args) > 1 {
		repoPath = args[1]
	}

	if repoPath == "" || repoPath == "--all" || repoPath == "all" {
		home, _ := os.UserHomeDir()
		studioDir := filepath.Join(home, "hairglasses-studio")
		if root := os.Getenv("HAIRGLASSES_STUDIO_ROOT"); root != "" {
			studioDir = root
		}
		paths, err := baselineguard.DiscoverWorkspaceTargets(studioDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error discovering baseline targets in %s: %v\n", studioDir, err)
			os.Exit(1)
		}

		switch cmd {
		case "sync":
			var reports []mcpsync.SyncReport
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".mcp.json")); err != nil {
					reports = append(reports, mcpsync.SyncReport{
						RepoPath: p,
						Actions: []mcpsync.SyncAction{
							{Action: "unchanged", Server: "mcp", Message: "no .mcp.json defined; nothing to sync"},
						},
					})
					continue
				}
				reports = append(reports, mcpsync.Sync(p, false))
			}
			printJSON(reports)
		case "diff":
			var reports []mcpsync.SyncReport
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".mcp.json")); err != nil {
					reports = append(reports, mcpsync.SyncReport{
						RepoPath: p,
						DryRun:   true,
						Actions: []mcpsync.SyncAction{
							{Action: "unchanged", Server: "mcp", Message: "no .mcp.json defined; nothing to sync"},
						},
					})
					continue
				}
				reports = append(reports, mcpsync.Diff(p))
			}
			printJSON(reports)
		case "check":
			hasDiff := false
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".mcp.json")); err != nil {
					continue
				}
				diffText, err := mcpsync.DiffText(p)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error in %s: %v\n", p, err)
					os.Exit(1)
				}
				if diffText != "" {
					fmt.Fprintf(os.Stdout, "--- %s ---\n%s", p, diffText)
					hasDiff = true
				}
			}
			if hasDiff {
				os.Exit(1)
			}
		case "list":
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(p, ".mcp.json")); err != nil {
					continue
				}
				names, err := mcpsync.List(p)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error in %s: %v\n", p, err)
					os.Exit(1)
				}
				for _, name := range names {
					fmt.Printf("%s: %s\n", filepath.Base(p), name)
				}
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown mcp command: %s\n", cmd)
			os.Exit(1)
		}
		return
	}

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

func runPerf(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit perf <audit|report> [root] [--json] [--all-scopes]")
		os.Exit(1)
	}

	cmd := args[0]
	root := workspace.DefaultRoot()
	jsonOut := false
	allScopes := false
	rootSet := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOut = true
		case "--all-scopes":
			allScopes = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
				os.Exit(1)
			}
			if rootSet {
				fmt.Fprintf(os.Stderr, "unexpected extra argument: %s\n", arg)
				os.Exit(1)
			}
			root = arg
			rootSet = true
		}
	}

	report := perfaudit.Audit(root, perfaudit.Options{AllScopes: allScopes})
	switch cmd {
	case "audit":
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Print(report.Markdown())
	case "report":
		fmt.Print(report.Markdown())
	default:
		fmt.Fprintf(os.Stderr, "unknown perf command: %s\n", cmd)
		os.Exit(1)
	}
}

func runRepoReadiness(args []string) {
	if len(args) == 0 || args[0] != "score" {
		fmt.Fprintln(os.Stderr, "usage: codexkit repo-readiness score [root] [--json] [--all-scopes]")
		os.Exit(1)
	}
	root := workspace.DefaultRoot()
	jsonOut := false
	allScopes := false
	rootSet := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOut = true
		case "--all-scopes":
			allScopes = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
				os.Exit(1)
			}
			if rootSet {
				fmt.Fprintf(os.Stderr, "unexpected extra argument: %s\n", arg)
				os.Exit(1)
			}
			root = arg
			rootSet = true
		}
	}
	report, err := reporeadiness.Score(root, reporeadiness.Options{AllScopes: allScopes})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Print(report.Markdown())
}

func runUnification(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit unification <audit|report|cycle|shell-file> [root] [--json] [--all-scopes]")
		os.Exit(1)
	}

	cmd := args[0]
	if cmd == "shell-file" {
		runUnificationShellFile(args[1:])
		return
	}
	root := workspace.DefaultRoot()
	jsonOut := false
	allScopes := false
	writeNote := false
	requireNotesApplied := false
	notesDir := ""
	previousNotes := ""
	cycleID := ""
	ackCarryForward := ""
	rootSet := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--all-scopes":
			allScopes = true
		case "--write":
			writeNote = true
		case "--require-notes-applied":
			requireNotesApplied = true
		case "--notes-dir":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--notes-dir requires a value")
				os.Exit(1)
			}
			notesDir = args[i]
		case "--previous-notes":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--previous-notes requires a value")
				os.Exit(1)
			}
			previousNotes = args[i]
		case "--cycle-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--cycle-id requires a value")
				os.Exit(1)
			}
			cycleID = args[i]
		case "--ack-carry-forward":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--ack-carry-forward requires a value")
				os.Exit(1)
			}
			ackCarryForward = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", arg)
				os.Exit(1)
			}
			if rootSet {
				fmt.Fprintf(os.Stderr, "unexpected extra argument: %s\n", arg)
				os.Exit(1)
			}
			root = arg
			rootSet = true
		}
	}

	if cmd == "cycle" {
		cycle, err := unificationaudit.BuildCycle(root, unificationaudit.CycleOptions{
			AllScopes:                   allScopes,
			NotesDir:                    notesDir,
			PreviousNotes:               previousNotes,
			CycleID:                     cycleID,
			RequireNotesApplied:         requireNotesApplied,
			CarryForwardAcknowledgement: ackCarryForward,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if writeNote {
			path, err := cycle.WriteMarkdown()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error writing cycle note: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", path)
		}
		if jsonOut {
			printJSON(cycle)
			return
		}
		fmt.Print(cycle.Markdown())
		return
	}

	report, err := unificationaudit.Audit(root, unificationaudit.Options{AllScopes: allScopes})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	switch cmd {
	case "audit":
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Print(report.Markdown())
	case "report":
		fmt.Print(report.Markdown())
	default:
		fmt.Fprintf(os.Stderr, "unknown unification command: %s\n", cmd)
		os.Exit(1)
	}
}

func runUnificationShellFile(args []string) {
	jsonOut := false
	positionals := []string{}
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown shell-file flag: %s\n", arg)
				os.Exit(1)
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 2 {
		fmt.Fprintln(os.Stderr, "usage: codexkit unification shell-file <repo_path> <shell_file> [--json]")
		os.Exit(1)
	}
	inventory, err := unificationaudit.InventoryShellFile(positionals[0], positionals[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(inventory)
		return
	}
	fmt.Print(inventory.Markdown())
}

func runParity(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit parity <audit|sync|report> [root] [--json] [--scope <scope>]")
		os.Exit(1)
	}
	cmd := args[0]
	root := workspace.DefaultRoot()
	jsonOut := false
	scope := ""
	rootSet := false

	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--scope":
			i++
			if i < len(args) {
				scope = args[i]
			}
		default:
			if !strings.HasPrefix(arg, "-") && !rootSet {
				root = arg
				rootSet = true
			}
		}
	}

	syncMode := (cmd == "sync")
	report, err := surfaceparity.RunSweep(surfaceparity.SweepOptions{
		WorkspaceRoot: root,
		Sync:          syncMode,
		ScopeFilter:   scope,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running surface parity sweep: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		printJSON(report)
		return
	}

	fmt.Print(report.Markdown())
}

func runReduction(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit reduction <audit|dedup|plan|apply|verify> [root] [--json] [--all-scopes] [--max-repos <n>] [--limit <n>] [--execute]")
		os.Exit(1)
	}

	cmd := args[0]
	root := workspace.DefaultRoot()
	jsonOut := false
	allScopes := false
	execute := false
	maxRepos := 8
	limit := 25
	rootSet := false

	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--all-scopes":
			allScopes = true
		case "--execute":
			execute = true
		case "--max-repos":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--max-repos requires a value")
				os.Exit(1)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "--max-repos must be a positive integer")
				os.Exit(1)
			}
			maxRepos = n
		case "--limit":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "--limit requires a value")
				os.Exit(1)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "--limit must be a positive integer")
				os.Exit(1)
			}
			limit = n
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "unknown reduction flag: %s\n", arg)
				os.Exit(1)
			}
			if rootSet {
				fmt.Fprintf(os.Stderr, "unexpected extra argument: %s\n", arg)
				os.Exit(1)
			}
			root = arg
			rootSet = true
		}
	}

	switch cmd {
	case "audit":
		report, err := llmreduction.BuildDebtAudit(root, allScopes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("scanned=%d top_repo=%s p0=%d p1=%d p2=%d\n", report.Summary.ReposScanned, report.Summary.TopRepo, report.Summary.P0, report.Summary.P1, report.Summary.P2)
	case "dedup":
		candidates, err := llmreduction.BuildDedupCandidates(root, allScopes, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(candidates)
			return
		}
		for _, candidate := range candidates {
			fmt.Printf("%s\t%s\t%s\n", candidate.Repo, candidate.Kind, candidate.Path)
		}
	case "plan":
		plan, err := llmreduction.BuildReductionPlan(root, allScopes, maxRepos)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(plan)
			return
		}
		for _, item := range plan.Items {
			fmt.Printf("%s\t%s\t%s\n", item.Repo, item.Priority, item.Action)
		}
	case "apply":
		plan, err := llmreduction.BuildReductionPlan(root, allScopes, maxRepos)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		result := map[string]any{
			"execute": execute,
			"applied": false,
			"note":    "safety default: generated plan only; repo-specific mutation executors are required",
			"plan":    plan,
		}
		if jsonOut {
			printJSON(result)
			return
		}
		fmt.Println(result["note"])
	case "verify":
		report, err := llmreduction.BuildDebtAudit(root, allScopes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(map[string]any{
				"current": report,
				"delta":   "baseline omitted",
			})
			return
		}
		fmt.Printf("verify: scanned=%d top_repo=%s\n", report.Summary.ReposScanned, report.Summary.TopRepo)
	default:
		fmt.Fprintf(os.Stderr, "unknown reduction command: %s\n", cmd)
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
		overlayPath, rest, _, err := takeFlagValue(args[1:], "--overlay")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		jsonOut := hasFlag(rest, "--json")
		root := workspace.DefaultRoot()
		for _, arg := range rest {
			if arg != "--json" {
				root = arg
				break
			}
		}

		manifest, err := workspace.LoadManifestWithOverlay(root, overlayPath)
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
	case "generate-manifest":
		write := hasFlag(args, "--write")
		jsonOut := hasFlag(args, "--json")
		root := workspace.DefaultRoot()
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			root = arg
			break
		}

		report, err := workspace.GenerateManifest(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if write {
			if err := workspace.WriteManifest(root, report.Manifest); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("wrote %s (%d repos, %d live repos)\n", report.ManifestPath, report.RepoCount, report.LiveRepoCount)
		}
		if jsonOut || !write {
			printJSON(report)
		}
	case "runtime-inventory":
		if err := runWorkspaceRuntimeInventory(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "runtime-inventory-check":
		passed, err := runWorkspaceRuntimeInventoryCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "global-mcp-projection":
		if err := runWorkspaceGlobalMCPProjection(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "global-mcp-projection-check":
		passed, err := runWorkspaceGlobalMCPProjectionCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "global-mcp-sync":
		passed, err := runWorkspaceGlobalMCPSync(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "diff-preview":
		if err := runWorkspaceDiffPreview(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "source-contract-check":
		passed, err := runWorkspaceSourceContractCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "surface-index":
		if err := runWorkspaceSurfaceIndex(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "surface-index-check":
		passed, err := runWorkspaceSurfaceIndexCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "primitive-index":
		if err := runWorkspacePrimitiveIndex(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "primitive-index-check":
		passed, err := runWorkspacePrimitiveIndexCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
			os.Exit(1)
		}
	case "config-index":
		if err := runWorkspaceConfigIndex(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "config-index-check":
		passed, err := runWorkspaceConfigIndexCheck(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !passed {
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

func runWorkspaceGlobalMCPProjection(args []string) error {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	policyPath := ""
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--json-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--json-out requires a path")
			}
			jsonPath = args[i]
		case "--markdown-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--markdown-out requires a path")
			}
			markdownPath = args[i]
		case "--policy":
			i++
			if i >= len(args) {
				return fmt.Errorf("--policy requires a path")
			}
			policyPath = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	projection, err := mcpsync.BuildGlobalProjection(mcpsync.GlobalProjectionOptions{
		WorkspaceRoot: root,
		PolicyPath:    policyPath,
	})
	if err != nil {
		return err
	}
	if err := mcpsync.WriteGlobalProjection(projection, jsonPath, markdownPath); err != nil {
		return err
	}
	if jsonOut {
		printJSON(projection)
		return nil
	}
	if jsonPath != "" || markdownPath != "" {
		fmt.Printf("global MCP projection: %d runtime servers, %d provider entries (%d warnings)\n",
			projection.Runtime.ServerCount,
			projection.Provider.TotalEntries,
			len(projection.Warnings),
		)
		return nil
	}
	fmt.Print(mcpsync.RenderGlobalProjectionMarkdown(projection))
	return nil
}

func runWorkspaceGlobalMCPProjectionCheck(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	policyPath := ""
	skipArtifacts := false
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--json-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-path requires a path")
			}
			jsonPath = args[i]
		case "--markdown-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--markdown-path requires a path")
			}
			markdownPath = args[i]
		case "--policy":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--policy requires a path")
			}
			policyPath = args[i]
		case "--skip-artifacts":
			skipArtifacts = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	report, err := mcpsync.CheckGlobalProjection(mcpsync.GlobalProjectionCheckOptions{
		WorkspaceRoot: root,
		PolicyPath:    policyPath,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		SkipArtifacts: skipArtifacts,
	})
	if err != nil {
		return false, err
	}
	if jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("global MCP projection check: %s (%d findings)\n", status, len(report.Findings))
	for _, finding := range report.Findings {
		findingStatus := "PASS"
		if !finding.Passed {
			findingStatus = "FAIL"
		}
		fmt.Printf("  %-16s %-20s %s\n", findingStatus, finding.Check, finding.Message)
	}
	return report.Passed, nil
}

func runWorkspaceDiffPreview(args []string) error {
	left := ""
	right := ""
	rel := ""
	kind := ""
	lines := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--left":
			i++
			if i >= len(args) {
				return fmt.Errorf("--left requires a path")
			}
			left = args[i]
		case "--right":
			i++
			if i >= len(args) {
				return fmt.Errorf("--right requires a path")
			}
			right = args[i]
		case "--rel":
			i++
			if i >= len(args) {
				return fmt.Errorf("--rel requires a path")
			}
			rel = args[i]
		case "--kind":
			i++
			if i >= len(args) {
				return fmt.Errorf("--kind requires a value")
			}
			kind = args[i]
		case "--lines":
			i++
			if i >= len(args) {
				return fmt.Errorf("--lines requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("--lines must be a positive integer")
			}
			lines = n
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if left == "" || right == "" || rel == "" || kind == "" {
		return fmt.Errorf("--left, --right, --rel, and --kind are required")
	}
	data, err := mcpsync.RenderDiffPreviewJSON(rel, left, right, kind, lines)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func runWorkspaceRuntimeInventory(args []string) error {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	policyPath := ""
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--json-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--json-out requires a path")
			}
			jsonPath = args[i]
		case "--markdown-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--markdown-out requires a path")
			}
			markdownPath = args[i]
		case "--policy":
			i++
			if i >= len(args) {
				return fmt.Errorf("--policy requires a path")
			}
			policyPath = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	inventory, err := mcpsync.BuildRuntimeInventory(mcpsync.RuntimeInventoryOptions{
		WorkspaceRoot: root,
		PolicyPath:    policyPath,
	})
	if err != nil {
		return err
	}
	if err := mcpsync.WriteRuntimeInventory(inventory, jsonPath, markdownPath); err != nil {
		return err
	}
	if jsonOut {
		printJSON(inventory)
		return nil
	}
	if jsonPath != "" || markdownPath != "" {
		fmt.Printf("runtime inventory: %d servers (%d ready, %d invalid, %d skipped)\n",
			inventory.Projection.ServerCount,
			inventory.Projection.ReadyCount,
			inventory.Projection.InvalidCount,
			inventory.Projection.SkippedCount,
		)
		return nil
	}
	fmt.Print(mcpsync.RenderRuntimeInventoryMarkdown(inventory))
	return nil
}

func runWorkspaceRuntimeInventoryCheck(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	policyPath := ""
	allowSkipped := []string{}
	allowSkippedSet := false
	allowAnySkipped := false
	skipArtifacts := false
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--json-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-path requires a path")
			}
			jsonPath = args[i]
		case "--markdown-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--markdown-path requires a path")
			}
			markdownPath = args[i]
		case "--policy":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--policy requires a path")
			}
			policyPath = args[i]
		case "--allow-skipped":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--allow-skipped requires a repo:server value")
			}
			allowSkippedSet = true
			allowSkipped = append(allowSkipped, args[i])
		case "--allow-any-skipped":
			allowAnySkipped = true
		case "--skip-artifacts":
			skipArtifacts = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	var allowList []string
	if allowSkippedSet {
		allowList = allowSkipped
	}
	report, err := mcpsync.CheckRuntimeInventory(mcpsync.RuntimeInventoryCheckOptions{
		WorkspaceRoot:   root,
		PolicyPath:      policyPath,
		JSONPath:        jsonPath,
		MarkdownPath:    markdownPath,
		AllowSkipped:    allowList,
		AllowAnySkipped: allowAnySkipped,
		SkipArtifacts:   skipArtifacts,
	})
	if err != nil {
		return false, err
	}
	if jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("runtime inventory check: %s (%d findings)\n", status, len(report.Findings))
	for _, finding := range report.Findings {
		findingStatus := "PASS"
		if !finding.Passed {
			findingStatus = "FAIL"
		}
		fmt.Printf("  %-16s %-20s %s\n", findingStatus, finding.Check, finding.Message)
	}
	return report.Passed, nil
}

func runWorkspaceGlobalMCPSync(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	mode := mcpsync.GlobalOverlaySyncWrite
	claudeJSONPath := ""
	claudeProjectKey := ""
	codexConfigPath := ""
	geminiSettingsPath := ""
	policyPath := ""
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			jsonOut = true
		case "--check":
			mode = mcpsync.GlobalOverlaySyncCheck
		case "--dry-run":
			mode = mcpsync.GlobalOverlaySyncDryRun
		case "--claude-json":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--claude-json requires a path")
			}
			claudeJSONPath = args[i]
		case "--claude-project-key":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--claude-project-key requires a value")
			}
			claudeProjectKey = args[i]
		case "--codex-config":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--codex-config requires a path")
			}
			codexConfigPath = args[i]
		case "--gemini-settings":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--gemini-settings requires a path")
			}
			geminiSettingsPath = args[i]
		case "--policy":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--policy requires a path")
			}
			policyPath = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	report, err := mcpsync.SyncGlobalProviderOverlays(mcpsync.GlobalOverlaySyncOptions{
		WorkspaceRoot:    root,
		PolicyPath:       policyPath,
		Mode:             mode,
		ClaudeJSONPath:   claudeJSONPath,
		ClaudeProjectKey: claudeProjectKey,
		CodexConfigPath:  codexConfigPath,
		GeminiConfigPath: geminiSettingsPath,
	})
	if err != nil {
		return false, err
	}
	if jsonOut {
		printJSON(report)
	} else {
		fmt.Print(report.Markdown())
	}
	return !(mode == mcpsync.GlobalOverlaySyncCheck && report.Pending), nil
}

func runWorkspaceSourceContractCheck(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonOutPath := ""
	jsonCheckPath := ""
	opts := sourcecontract.CheckOptions{}
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--json-out":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-out requires a path")
			}
			jsonOutPath = args[i]
		case arg == "--json-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-path requires a path")
			}
			jsonCheckPath = args[i]
		case arg == "--skills-only":
			opts.SkillsOnly = true
		case arg == "--tools-only":
			opts.ToolsOnly = true
		case arg == "--skip-runtime-inventory":
			opts.SkipRuntimeInventory = true
		case arg == "--overlay":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--overlay requires a path")
			}
			opts.OverlayPath = args[i]
		case arg == "--skill-validator":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--skill-validator requires auto, host, pinned, or off")
			}
			mode, err := skillsync.ParseValidatorMode(args[i])
			if err != nil {
				return false, err
			}
			opts.SkillValidatorMode = mode
		case strings.HasPrefix(arg, "--skill-validator="):
			mode, err := skillsync.ParseValidatorMode(strings.TrimPrefix(arg, "--skill-validator="))
			if err != nil {
				return false, err
			}
			opts.SkillValidatorMode = mode
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}
	if jsonOutPath != "" && jsonCheckPath != "" {
		return false, fmt.Errorf("--json-out and --json-path cannot be combined")
	}

	report, err := sourcecontract.Check(root, opts)
	if err != nil {
		return false, err
	}
	artifactReport := report
	if jsonCheckPath != "" {
		artifact := sourcecontract.CheckArtifact(artifactReport, jsonCheckPath)
		report.Artifact = &artifact
		if !artifact.Passed {
			report.Passed = false
		}
	}
	if jsonOutPath != "" {
		if err := sourcecontract.WriteArtifact(artifactReport, jsonOutPath); err != nil {
			return false, err
		}
	}
	if jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	printSourceContractReport(report)
	return report.Passed, nil
}

func printSourceContractReport(report sourcecontract.Report) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("source contract check: %s (%d repos, %d skill surfaces, %d MCP repos, %d warnings)\n",
		status,
		report.Summary.ManagedReposChecked,
		report.Summary.SkillSurfaceReposChecked,
		report.Summary.MCPReposChecked,
		report.Summary.Warnings,
	)
	printSourceContractComponent("workspace", report.Workspace.Passed, len(report.Workspace.Findings))
	skillsPassed, mcpPassed := true, true
	for _, repo := range report.Repos {
		if repo.SkillSync != nil && !repo.Passed {
			if len(repo.SkillSync.Errors) > 0 || repo.SkillSync.PendingChanges {
				skillsPassed = false
			}
		}
		if repo.MCPSync != nil && (len(repo.MCPSync.Errors) > 0 || repo.MCPSync.PendingChanges) {
			mcpPassed = false
		}
	}
	printSourceContractComponent("skills", skillsPassed, report.Summary.SkillSurfaceReposChecked)
	printSourceContractComponent("mcp", mcpPassed, report.Summary.MCPReposChecked)
	if report.RuntimeInventory != nil {
		printSourceContractComponent("runtime_inventory", report.RuntimeInventory.Passed, len(report.RuntimeInventory.Findings))
	}
	if report.GlobalProjection != nil {
		printSourceContractComponent("global_projection", report.GlobalProjection.Passed, len(report.GlobalProjection.Findings))
	}
	if report.Artifact != nil {
		printSourceContractComponent("source_artifact", report.Artifact.Passed, 1)
		if !report.Artifact.Passed && report.Artifact.Message != "" {
			fmt.Printf("    %s\n", report.Artifact.Message)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Printf("  WARN             %-20s %s\n", "warning", warning)
	}
	for _, repo := range report.Repos {
		if repo.Passed {
			continue
		}
		fmt.Printf("  FAIL             %-20s %s\n", "repo", repo.Repo)
		for _, err := range repo.Errors {
			fmt.Printf("    %s\n", err)
		}
		if repo.SkillSync != nil {
			for _, err := range repo.SkillSync.Errors {
				fmt.Printf("    skill: %s\n", err)
			}
			for _, action := range repo.SkillSync.Actions {
				if action.Action != "unchanged" {
					fmt.Printf("    skill: %s\n", action.Message)
				}
			}
		}
		if repo.MCPSync != nil {
			for _, err := range repo.MCPSync.Errors {
				fmt.Printf("    mcp: %s\n", err)
			}
			for _, action := range repo.MCPSync.Actions {
				if action.Action != "unchanged" {
					fmt.Printf("    mcp: %s\n", action.Message)
				}
			}
		}
	}
}

func printSourceContractComponent(name string, passed bool, count int) {
	status := "PASS"
	if !passed {
		status = "FAIL"
	}
	fmt.Printf("  %-16s %-20s %d\n", status, name, count)
}

func runWorkspaceSurfaceIndex(args []string) error {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	validatorMode := skillsync.ValidatorOff
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--json-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--json-out requires a path")
			}
			jsonPath = args[i]
		case arg == "--markdown-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--markdown-out requires a path")
			}
			markdownPath = args[i]
		case arg == "--skill-validator":
			i++
			if i >= len(args) {
				return fmt.Errorf("--skill-validator requires auto, host, pinned, or off")
			}
			mode, err := skillsync.ParseValidatorMode(args[i])
			if err != nil {
				return err
			}
			validatorMode = mode
		case strings.HasPrefix(arg, "--skill-validator="):
			mode, err := skillsync.ParseValidatorMode(strings.TrimPrefix(arg, "--skill-validator="))
			if err != nil {
				return err
			}
			validatorMode = mode
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	index, err := surfaceindex.Build(surfaceindex.Options{
		WorkspaceRoot:      root,
		SkillValidatorMode: validatorMode,
	})
	if err != nil {
		return err
	}
	if err := surfaceindex.Write(index, jsonPath, markdownPath); err != nil {
		return err
	}
	if jsonOut {
		printJSON(index)
		return nil
	}
	if jsonPath != "" || markdownPath != "" {
		fmt.Printf("surface index: %d baseline repos (%d skill surfaces, %d MCP repos, %d runtime projected repos)\n",
			index.Summary.BaselineRepos,
			index.Summary.SkillSurfaceRepos,
			index.Summary.MCPSourceRepos,
			index.Summary.RuntimeProjectedRepos,
		)
		return nil
	}
	fmt.Print(surfaceindex.RenderMarkdown(index))
	return nil
}

func runWorkspaceSurfaceIndexCheck(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	skipArtifacts := false
	validatorMode := skillsync.ValidatorOff
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--json-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-path requires a path")
			}
			jsonPath = args[i]
		case arg == "--markdown-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--markdown-path requires a path")
			}
			markdownPath = args[i]
		case arg == "--skip-artifacts":
			skipArtifacts = true
		case arg == "--skill-validator":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--skill-validator requires auto, host, pinned, or off")
			}
			mode, err := skillsync.ParseValidatorMode(args[i])
			if err != nil {
				return false, err
			}
			validatorMode = mode
		case strings.HasPrefix(arg, "--skill-validator="):
			mode, err := skillsync.ParseValidatorMode(strings.TrimPrefix(arg, "--skill-validator="))
			if err != nil {
				return false, err
			}
			validatorMode = mode
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	report, err := surfaceindex.Check(surfaceindex.CheckOptions{
		WorkspaceRoot:      root,
		JSONPath:           jsonPath,
		MarkdownPath:       markdownPath,
		SkipArtifacts:      skipArtifacts,
		SkillValidatorMode: validatorMode,
	})
	if err != nil {
		return false, err
	}
	if jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("surface index check: %s (%d findings)\n", status, len(report.Findings))
	for _, finding := range report.Findings {
		findingStatus := "PASS"
		if !finding.Passed {
			findingStatus = "FAIL"
		}
		fmt.Printf("  %-16s %-20s %s\n", findingStatus, finding.Check, finding.Message)
	}
	return report.Passed, nil
}

type configIndexCLIOptions struct {
	root         string
	jsonOut      bool
	jsonPath     string
	markdownPath string
	profiles     []configindex.Profile
	runtimeStats bool
}

func parseConfigIndexCLIOptions(args []string, allowArtifacts bool) (configIndexCLIOptions, error) {
	opts := configIndexCLIOptions{root: workspace.DefaultRoot()}
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			opts.jsonOut = true
		case "--json-out":
			if !allowArtifacts {
				return opts, fmt.Errorf("--json-out is not valid for config-index-check")
			}
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--json-out requires a path")
			}
			opts.jsonPath = args[i]
		case "--markdown-out":
			if !allowArtifacts {
				return opts, fmt.Errorf("--markdown-out is not valid for config-index-check")
			}
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--markdown-out requires a path")
			}
			opts.markdownPath = args[i]
		case "--user-home":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--user-home requires a path")
			}
			opts.profiles = append(opts.profiles, configindex.Profile{Name: "user", Home: args[i]})
		case "--root-home":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--root-home requires a path")
			}
			opts.profiles = append(opts.profiles, configindex.Profile{Name: "root", Home: args[i]})
		case "--runtime-stats":
			opts.runtimeStats = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return opts, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			opts.root = arg
			rootSet = true
		}
	}
	return opts, nil
}

func runWorkspaceConfigIndex(args []string) error {
	opts, err := parseConfigIndexCLIOptions(args, true)
	if err != nil {
		return err
	}
	index, err := configindex.Build(configindex.Options{
		WorkspaceRoot: opts.root, Profiles: opts.profiles, IncludeRuntimeStats: opts.runtimeStats,
	})
	if err != nil {
		return err
	}
	if err := configindex.Write(index, opts.jsonPath, opts.markdownPath); err != nil {
		return err
	}
	if opts.jsonOut {
		printJSON(index)
		return nil
	}
	if opts.jsonPath != "" || opts.markdownPath != "" {
		fmt.Printf("configuration index: %d physical files, %d canonical logical sources, %d runtime buckets, %d duplicate groups\n",
			index.Summary.PhysicalFiles, index.Summary.LogicalSources, index.Summary.RuntimeBuckets, index.Summary.DuplicateGroups)
		return nil
	}
	fmt.Print(configindex.RenderMarkdown(index))
	return nil
}

func runWorkspaceConfigIndexCheck(args []string) (bool, error) {
	opts, err := parseConfigIndexCLIOptions(args, false)
	if err != nil {
		return false, err
	}
	report, err := configindex.Check(configindex.Options{
		WorkspaceRoot: opts.root, Profiles: opts.profiles, IncludeRuntimeStats: opts.runtimeStats,
	})
	if err != nil {
		return false, err
	}
	if opts.jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("configuration index check: %s (%d findings)\n", status, len(report.Findings))
	for _, finding := range report.Findings {
		findingStatus := "PASS"
		if !finding.Passed {
			findingStatus = "FAIL"
		}
		fmt.Printf("  %-5s %-24s %s\n", findingStatus, finding.Check, finding.Message)
		if finding.Path != "" {
			fmt.Printf("        %s\n", finding.Path)
		}
	}
	return report.Passed, nil
}

func runWorkspacePrimitiveIndex(args []string) error {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--json-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--json-out requires a path")
			}
			jsonPath = args[i]
		case arg == "--markdown-out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--markdown-out requires a path")
			}
			markdownPath = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	index, err := primitiveindex.Build(primitiveindex.Options{WorkspaceRoot: root})
	if err != nil {
		return err
	}
	if err := primitiveindex.Write(index, jsonPath, markdownPath); err != nil {
		return err
	}
	if jsonOut {
		printJSON(index)
		return nil
	}
	if jsonPath != "" || markdownPath != "" {
		fmt.Printf("primitive index: %d primitives (%d warnings, %d blocking failures)\n",
			index.Summary.Total,
			index.Summary.Warnings,
			index.Summary.BlockingPrimitiveFailures,
		)
		return nil
	}
	fmt.Print(primitiveindex.RenderMarkdown(index))
	return nil
}

func runWorkspacePrimitiveIndexCheck(args []string) (bool, error) {
	root := workspace.DefaultRoot()
	jsonOut := false
	jsonPath := ""
	markdownPath := ""
	skipArtifacts := false
	overlayPath := ""
	rootSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--json-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--json-path requires a path")
			}
			jsonPath = args[i]
		case arg == "--markdown-path":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--markdown-path requires a path")
			}
			markdownPath = args[i]
		case arg == "--skip-artifacts":
			skipArtifacts = true
		case arg == "--overlay":
			i++
			if i >= len(args) {
				return false, fmt.Errorf("--overlay requires a path")
			}
			overlayPath = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return false, fmt.Errorf("unknown flag: %s", arg)
			}
			if rootSet {
				return false, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			root = arg
			rootSet = true
		}
	}

	report, err := primitiveindex.Check(primitiveindex.CheckOptions{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		SkipArtifacts: skipArtifacts,
		OverlayPath:   overlayPath,
	})
	if err != nil {
		return false, err
	}
	if jsonOut {
		printJSON(report)
		return report.Passed, nil
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("primitive index check: %s (%d findings)\n", status, len(report.Findings))
	for _, finding := range report.Findings {
		findingStatus := "PASS"
		if !finding.Passed {
			findingStatus = "FAIL"
		}
		fmt.Printf("  %-16s %-20s %s\n", findingStatus, finding.Check, finding.Message)
	}
	return report.Passed, nil
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

func runTelemetry(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit telemetry <record|list|search> [flags]")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "record":
		runTelemetryRecord(subArgs)
	case "list":
		runTelemetryList(subArgs)
	case "search":
		runTelemetrySearch(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown telemetry subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func runTelemetryRecord(args []string) {
	storePath, args, _, _ := takeFlagValue(args, "--store-path")
	eventType, args, _, _ := takeFlagValue(args, "--event-type")
	repo, args, _, _ := takeFlagValue(args, "--repo")
	provider, args, _, _ := takeFlagValue(args, "--provider")
	model, args, _, _ := takeFlagValue(args, "--model")
	profile, args, _, _ := takeFlagValue(args, "--profile")
	role, args, _, _ := takeFlagValue(args, "--role")
	decision, args, _, _ := takeFlagValue(args, "--decision")
	checkType, args, _, _ := takeFlagValue(args, "--check-type")
	status, args, _, _ := takeFlagValue(args, "--status")
	errStr, args, _, _ := takeFlagValue(args, "--error")
	durStr, args, _, _ := takeFlagValue(args, "--duration-ms")
	findingsStr, _, _, _ := takeFlagValue(args, "--findings")

	if eventType == "" {
		eventType = telemetry.EventTypeRouting
	}

	var dur float64
	if durStr != "" {
		dur, _ = strconv.ParseFloat(durStr, 64)
	}
	var findings int
	if findingsStr != "" {
		findings, _ = strconv.Atoi(findingsStr)
	}

	ev := telemetry.Event{
		EventType:  eventType,
		Repo:       repo,
		Provider:   provider,
		Model:      model,
		Profile:    profile,
		Role:       role,
		Decision:   decision,
		CheckType:  checkType,
		Status:     status,
		DurationMS: dur,
		Findings:   findings,
		Error:      errStr,
	}

	rec, err := telemetry.Record(storePath, ev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error recording telemetry: %v\n", err)
		os.Exit(1)
	}
	printJSON(rec)
}

func runTelemetryList(args []string) {
	storePath, args, _, _ := takeFlagValue(args, "--store-path")
	eventType, args, _, _ := takeFlagValue(args, "--event-type")
	repo, args, _, _ := takeFlagValue(args, "--repo")
	provider, args, _, _ := takeFlagValue(args, "--provider")
	status, args, _, _ := takeFlagValue(args, "--status")
	sinceStr, args, _, _ := takeFlagValue(args, "--since")
	limitStr, args, _, _ := takeFlagValue(args, "--limit")
	jsonOut := hasFlag(args, "--json")

	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	var sinceTime time.Time
	if sinceStr != "" {
		if dur, err := time.ParseDuration(sinceStr); err == nil {
			sinceTime = time.Now().Add(-dur)
		} else if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			sinceTime = t
		} else if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			sinceTime = t
		}
	}

	filter := telemetry.Filter{
		EventType: eventType,
		Repo:      repo,
		Provider:  provider,
		Status:    status,
		Since:     sinceTime,
		Limit:     limit,
	}

	events, err := telemetry.Query(storePath, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying telemetry: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		printJSON(events)
		return
	}

	if len(events) == 0 {
		fmt.Println("No matching telemetry events found.")
		return
	}

	fmt.Printf("Telemetry Events (%d):\n", len(events))
	for _, ev := range events {
		fmt.Printf("  [%s] %s | type: %-14s | repo: %-20s | status: %-5s | model: %s\n",
			ev.Timestamp[:19], ev.ID, ev.EventType, ev.Repo, ev.Status, ev.Model)
		if ev.Decision != "" {
			fmt.Printf("    decision: %s\n", ev.Decision)
		}
		if ev.Error != "" {
			fmt.Printf("    error: %s\n", ev.Error)
		}
	}
}

func runTelemetrySearch(args []string) {
	storePath, args, _, _ := takeFlagValue(args, "--store-path")
	limitStr, args, _, _ := takeFlagValue(args, "--limit")
	jsonOut := hasFlag(args, "--json")

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit telemetry search <query> [--limit <n>] [--json]")
		os.Exit(1)
	}
	query := args[0]

	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := telemetry.Search(storePath, query, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error searching telemetry: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		printJSON(events)
		return
	}

	if len(events) == 0 {
		fmt.Printf("No telemetry events matching %q found.\n", query)
		return
	}

	fmt.Printf("Telemetry Search Results for %q (%d):\n", query, len(events))
	for _, ev := range events {
		fmt.Printf("  [%s] %s | type: %-14s | repo: %-20s | status: %-5s | model: %s\n",
			ev.Timestamp[:19], ev.ID, ev.EventType, ev.Repo, ev.Status, ev.Model)
		if ev.Decision != "" {
			fmt.Printf("    decision: %s\n", ev.Decision)
		}
		if ev.Error != "" {
			fmt.Printf("    error: %s\n", ev.Error)
		}
	}
}
