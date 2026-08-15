package agyloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunFleetAutopilotDryRun(t *testing.T) {
	tempWorkspace, err := os.MkdirTemp("", "agyfleet-test-*")
	if err != nil {
		t.Fatalf("failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	for _, repo := range []string{"repo-alpha", "repo-beta"} {
		repoDir := filepath.Join(tempWorkspace, repo)
		_ = os.MkdirAll(repoDir, 0755)
		_ = os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("# "+repo), 0644)
		_ = os.WriteFile(filepath.Join(repoDir, "CLAUDE.md"), []byte("> AGENTS.md"), 0644)
		_ = os.MkdirAll(filepath.Join(repoDir, ".claude"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, ".claude", "settings.json"), []byte("{}"), 0644)
		_ = os.MkdirAll(filepath.Join(repoDir, ".codex"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, ".codex", "config.toml"), []byte(""), 0644)
		_ = os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)
	}

	opts := FleetAutopilotOptions{
		WorkspaceRoot:   tempWorkspace,
		Concurrency:     2,
		MaxIterations:   2,
		Model:           "gemini-3.7-flash-high",
		ReasoningEffort: "high",
		Prompt:          "fleet dry run test pass",
		DryRun:          true,
	}

	report, err := RunFleetAutopilot(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.TotalReposScanned != 2 {
		t.Errorf("expected 2 repos scanned, got %d", report.TotalReposScanned)
	}
	if report.EligibleRepos != 2 {
		t.Errorf("expected 2 eligible repos, got %d", report.EligibleRepos)
	}
	if report.SuccessfulRepos != 2 {
		t.Errorf("expected 2 successful repos, got %d", report.SuccessfulRepos)
	}
	if report.CircuitBreakerOpen {
		t.Errorf("expected circuit breaker to remain closed")
	}

	statusFile := filepath.Join(tempWorkspace, ".ralph", "fleet_autopilot_status.json")
	if _, err := os.Stat(statusFile); err != nil {
		t.Errorf("expected .ralph/fleet_autopilot_status.json to be created")
	}

	reportMD := filepath.Join(tempWorkspace, ".ralph", "fleet_autopilot_report.md")
	if _, err := os.Stat(reportMD); err != nil {
		t.Errorf("expected .ralph/fleet_autopilot_report.md to be created")
	}
}
