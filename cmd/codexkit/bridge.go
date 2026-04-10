package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type bridgeConfig struct {
	RepoPath       string
	RepoName       string
	SurfacekitRoot string
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
  skill-surface-check <repo> [--surfacekit-root <path>]
  provider-settings-check <repo> [--repo-name <name>] [--surfacekit-root <path>]
  codex-mcp-check <repo> [--surfacekit-root <path>]`)
}

func runSkillSurfaceCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, false)
	if err != nil {
		return err
	}
	return runSurfacekitBridge(cfg, "skill-surface-sync.sh", cfg.RepoPath, "--check")
}

func runProviderSettingsCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, true)
	if err != nil {
		return err
	}
	bridgeArgs := []string{cfg.RepoPath}
	if cfg.RepoName != "" {
		bridgeArgs = append(bridgeArgs, "--repo-name", cfg.RepoName)
	}
	bridgeArgs = append(bridgeArgs, "--check")
	return runSurfacekitBridge(cfg, "provider-settings-sync.sh", bridgeArgs...)
}

func runCodexMCPCheck(args []string) error {
	cfg, err := parseBridgeConfig(args, false)
	if err != nil {
		return err
	}
	return runSurfacekitBridge(cfg, "codex-mcp-sync.sh", cfg.RepoPath, "--dry-run")
}

func parseBridgeConfig(args []string, allowRepoName bool) (bridgeConfig, error) {
	cfg := bridgeConfig{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			return bridgeConfig{}, fmt.Errorf("usage: see `codexkit bridge --help`")
		case "--surfacekit-root":
			if i+1 >= len(args) {
				return bridgeConfig{}, fmt.Errorf("--surfacekit-root requires a value")
			}
			cfg.SurfacekitRoot = args[i+1]
			i++
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
	if cfg.SurfacekitRoot == "" {
		root := os.Getenv("SURFACEKIT_ROOT")
		if root != "" {
			cfg.SurfacekitRoot = root
		} else {
			cfg.SurfacekitRoot = filepath.Join(filepath.Dir(repoPath), "surfacekit")
		}
	}
	return cfg, nil
}

func runSurfacekitBridge(cfg bridgeConfig, scriptName string, bridgeArgs ...string) error {
	scriptPath := filepath.Join(cfg.SurfacekitRoot, "scripts", scriptName)
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("surfacekit bridge script not found at %s: %w", scriptPath, err)
	}

	cmd := exec.Command("bash", append([]string{scriptPath}, bridgeArgs...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
