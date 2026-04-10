package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

type bridgeConfig struct {
	RepoPath string
	RepoName string
}

func runBridge(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: codexkit bridge <skill-surface-check|provider-settings-check|codex-mcp-check> ...")
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "skill-surface-check":
		err = runSkillSurfaceCheck(args[1:])
	case "provider-settings-check":
		err = runProviderSettingsCheck(args[1:])
	case "codex-mcp-check":
		err = runCodexMCPCheck(args[1:])
	case "help", "-h", "--help":
		printBridgeUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown bridge command: %s\n", args[0])
		printBridgeUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printBridgeUsage() {
	fmt.Println(`codexkit bridge

Subcommands:
  skill-surface-check <repo>
  provider-settings-check <repo> [--repo-name <name>]
  codex-mcp-check <repo>`)
}

func runSkillSurfaceCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, false)
	if err != nil {
		return err
	}
	report := skillsync.Check(cfg.RepoPath)
	for _, warning := range report.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	if len(report.Errors) > 0 {
		return errors.New(strings.Join(report.Errors, "; "))
	}
	if report.PendingChanges {
		for _, action := range report.Actions {
			if action.Action == "unchanged" {
				continue
			}
			fmt.Fprintln(os.Stderr, action.Message)
		}
		return errors.New("skill surface drift detected")
	}
	return nil
}

func runProviderSettingsCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, true)
	if err != nil {
		return err
	}
	scriptArgs := []string{cfg.RepoPath}
	if cfg.RepoName != "" {
		scriptArgs = append(scriptArgs, "--repo-name", cfg.RepoName)
	}
	scriptArgs = append(scriptArgs, "--check")
	return runCodexkitScript(findCodexkitRoot(cfg.RepoPath), "provider-settings-sync.sh", scriptArgs...)
}

func runCodexMCPCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, false)
	if err != nil {
		return err
	}
	diffText, err := mcpsync.DiffText(cfg.RepoPath)
	if err != nil {
		return err
	}
	if diffText != "" {
		fmt.Fprint(os.Stdout, diffText)
		return errors.New("Codex MCP drift detected")
	}
	return nil
}

func parseBridgeConfig(args []string, allowRepoName bool) (bridgeConfig, error) {
	cfg := bridgeConfig{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			return bridgeConfig{}, fmt.Errorf("usage: see `codexkit bridge --help`")
		case "--repo-name":
			if !allowRepoName {
				return bridgeConfig{}, fmt.Errorf("--repo-name is not supported for this command")
			}
			if i+1 >= len(args) {
				return bridgeConfig{}, fmt.Errorf("--repo-name requires a value")
			}
			cfg.RepoName = args[i+1]
			i++
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return bridgeConfig{}, fmt.Errorf("unknown argument: %s", args[i])
			}
			if cfg.RepoPath != "" {
				return bridgeConfig{}, fmt.Errorf("unexpected extra argument: %s", args[i])
			}
			cfg.RepoPath = args[i]
		}
	}

	if cfg.RepoPath == "" {
		return bridgeConfig{}, fmt.Errorf("repo path is required")
	}
	repoPath, err := filepath.Abs(cfg.RepoPath)
	if err != nil {
		return bridgeConfig{}, err
	}
	cfg.RepoPath = repoPath
	return cfg, nil
}

func findCodexkitRoot(repoPath string) string {
	if root := os.Getenv("CODEXKIT_ROOT"); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			return abs
		}
		return root
	}
	if root, ok := sourceCodexkitRoot(); ok {
		return root
	}
	cwd, err := os.Getwd()
	if err == nil {
		for _, candidate := range walkParents(cwd) {
			if isCodexkitRoot(candidate) {
				return candidate
			}
		}
	}
	for _, candidate := range walkParents(filepath.Dir(repoPath)) {
		if isCodexkitRoot(filepath.Join(candidate, "codexkit")) {
			return filepath.Join(candidate, "codexkit")
		}
	}
	return filepath.Join(filepath.Dir(repoPath), "codexkit")
}

func sourceCodexkitRoot() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if !isCodexkitRoot(root) {
		return "", false
	}
	return root, true
}

func walkParents(start string) []string {
	parents := []string{}
	current := start
	for {
		parents = append(parents, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return parents
}

func isCodexkitRoot(path string) bool {
	required := []string{
		filepath.Join(path, "cmd", "codexkit", "main.go"),
		filepath.Join(path, "scripts", "run-codexkit-mcp.sh"),
	}
	for _, candidate := range required {
		if _, err := os.Stat(candidate); err != nil {
			return false
		}
	}
	return true
}

func runCodexkitScript(root, scriptName string, scriptArgs ...string) error {
	scriptPath := filepath.Join(root, "scripts", scriptName)
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("codexkit bridge script not found at %s: %w", scriptPath, err)
	}
	cmd := exec.Command("bash", append([]string{scriptPath}, scriptArgs...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(
		os.Environ(),
		"CODEXKIT_ROOT="+root,
		"HG_AGENT_PARITY_ROOT="+root,
	)
	return cmd.Run()
}
