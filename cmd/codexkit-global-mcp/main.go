package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit/mcpsync"
)

func main() {
	home, _ := os.UserHomeDir()
	defaultConfig := filepath.Join(home, ".codex", "config.toml")

	workspaceRoot := flag.String("workspace-root", defaultWorkspaceRoot(home), "Path to the workspace root")
	configPath := flag.String("config", defaultConfig, "Target Codex config.toml path")
	dryRun := flag.Bool("dry-run", false, "Preview changes without writing")
	flag.Parse()

	report := mcpsync.SyncGlobal(*workspaceRoot, *configPath, *dryRun)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if len(report.Errors) > 0 {
		os.Exit(1)
	}
}

func defaultWorkspaceRoot(home string) string {
	if home == "" {
		return "hairglasses-studio"
	}
	return filepath.Join(home, "hairglasses-studio")
}
