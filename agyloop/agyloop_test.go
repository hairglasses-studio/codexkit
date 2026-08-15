package agyloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSignAndVerifyReceipt(t *testing.T) {
	repo := "codexkit"
	branch := "main"
	gitSHA := "abcdef1234567890abcdef1234567890abcdef12"
	prompt := "modernize loop tooling"
	iteration := 1

	sig := SignReceipt(repo, branch, gitSHA, prompt, iteration)
	if len(sig) == 0 {
		t.Fatalf("expected non-empty HMAC signature")
	}

	if len(sig) != 64 {
		t.Fatalf("expected 64-character SHA256 hex string, got %d", len(sig))
	}
}

func TestRunLoopDryRun(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agyloop-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	opts := LoopOptions{
		RepoPath:        tempDir,
		Model:           "gemini-3.7-flash-high",
		ReasoningEffort: "high",
		MaxIterations:   3,
		Prompt:          "test dry run loop pass",
		DryRun:          true,
	}

	report, err := RunLoop(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.TotalIterations != 3 {
		t.Errorf("expected 3 iterations, got %d", report.TotalIterations)
	}
	if report.SuccessfulRounds != 3 {
		t.Errorf("expected 3 successful rounds, got %d", report.SuccessfulRounds)
	}
	if report.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", report.Status)
	}

	statusFile := filepath.Join(tempDir, ".ralph", "status.json")
	if _, err := os.Stat(statusFile); err != nil {
		t.Errorf("expected .ralph/status.json to be created")
	}
}

func TestModuleDeclaration(t *testing.T) {
	mod := Module()
	if mod.Name() != "agyloop" {
		t.Errorf("expected module name 'agyloop', got '%s'", mod.Name())
	}
	if len(mod.Tools()) != 4 {
		t.Errorf("expected 4 tools, got %d", len(mod.Tools()))
	}
}
