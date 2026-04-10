package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit/workspace"
)

type workspaceRefreshConfig struct {
	Root                string
	CodexkitRoot        string
	WriteWorkspaceCache bool
}

func runWorkspaceRefresh(args []string) error {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			fmt.Fprintln(os.Stdout, "usage: codexkit workspace refresh-parity [root] [--with-workspace-cache]")
			return nil
		}
	}

	cfg, err := parseWorkspaceRefreshArgs(args)
	if err != nil {
		return err
	}

	runScript := filepath.Join(cfg.CodexkitRoot, "scripts", "agent-parity-audit.sh")
	if _, err := os.Stat(runScript); err != nil {
		return fmt.Errorf("codexkit parity audit script not found at %s: %w", runScript, err)
	}

	cmd := exec.Command("bash", append([]string{runScript}, parityAuditArgs(cfg.WriteWorkspaceCache)...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(),
		"HG_STUDIO_ROOT="+cfg.Root,
		"CODEXKIT_ROOT="+cfg.CodexkitRoot,
		"HG_AGENT_PARITY_ROOT="+cfg.CodexkitRoot,
		"HG_AGENT_PARITY_SURFACEKIT_ROOT="+cfg.CodexkitRoot,
	)
	return cmd.Run()
}

func parseWorkspaceRefreshArgs(args []string) (workspaceRefreshConfig, error) {
	cfg := workspaceRefreshConfig{
		Root: workspace.DefaultRoot(),
	}
	rootExplicit := false

	for _, arg := range args {
		switch arg {
		case "--with-workspace-cache":
			cfg.WriteWorkspaceCache = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return workspaceRefreshConfig{}, fmt.Errorf("unknown argument: %s", arg)
			}
			if rootExplicit {
				return workspaceRefreshConfig{}, fmt.Errorf("unexpected extra argument: %s", arg)
			}
			cfg.Root = arg
			rootExplicit = true
		}
	}

	cfg.CodexkitRoot = findCodexkitRoot(cfg.Root)
	return cfg, nil
}

func parityAuditArgs(writeWorkspaceCache bool) []string {
	args := []string{"--write-wiki-docs", "--write-json"}
	if writeWorkspaceCache {
		args = append(args, "--write-workspace-cache")
	}
	return args
}
