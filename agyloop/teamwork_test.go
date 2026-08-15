package agyloop

import (
	"context"
	"os"
	"testing"
)

func TestRunTeamworkDryRun(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agyteamwork-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	opts := TeamworkOptions{
		RepoPath:        tempDir,
		Topology:        TopologyStar,
		Workers:         3,
		Prompt:          "parallel fleet audit and modernization",
		Scopes:          []string{"mcp-dual-sdk", "schema-v2-roles", "benchmarks"},
		Model:           "gemini-3.7-flash-high",
		ReasoningEffort: "high",
		DryRun:          true,
	}

	report, err := RunTeamwork(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.WorkersSpawned != 3 {
		t.Errorf("expected 3 workers spawned, got %d", report.WorkersSpawned)
	}
	if len(report.WorkerResults) != 3 {
		t.Errorf("expected 3 worker results, got %d", len(report.WorkerResults))
	}
	if !report.ConvergencePassed {
		t.Errorf("expected convergence to pass in dry-run mode")
	}
	if len(report.MasterReceipt) != 64 {
		t.Errorf("expected 64-char HMAC master receipt, got %s", report.MasterReceipt)
	}
}
