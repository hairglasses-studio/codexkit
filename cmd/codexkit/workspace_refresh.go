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
	SurfacekitRoot      string
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

	runScript := filepath.Join(cfg.SurfacekitRoot, "scripts", "run-surfacekit.sh")
	if _, err := os.Stat(runScript); err != nil {
		return fmt.Errorf("surfacekit launcher not found at %s: %w", runScript, err)
	}

	cmd := exec.Command(runScript, surfacekitParityArgs(cfg.Root, cfg.WriteWorkspaceCache)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
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

	cfg.SurfacekitRoot = os.Getenv("SURFACEKIT_ROOT")
	if cfg.SurfacekitRoot == "" {
		cfg.SurfacekitRoot = filepath.Join(cfg.Root, "surfacekit")
	}

	return cfg, nil
}

func surfacekitParityArgs(root string, writeWorkspaceCache bool) []string {
	args := []string{
		"coverage",
		"workspace",
		"--root",
		root,
		"--write-wiki-docs",
		"--write-json",
	}
	if writeWorkspaceCache {
		args = append(args, "--write-workspace-cache")
	}
	return args
}
