package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/sourceref"
)

func TestExecuteSourceRefValidatesAndSummarizesOfflineInventory(t *testing.T) {
	now := time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC)
	inventory := sourceref.Inventory{SchemaVersion: sourceref.SchemaVersion, Repositories: []sourceref.RepositoryEvidence{{
		Repository: "hairglasses-studio/example", RemoteURL: "git@" + "github.com:hairglasses-studio/example.git",
		RequestedRef: "refs/heads/main", RequestedSHA: strings.Repeat("1", 40),
		TestedRef: "HEAD", TestedSHA: strings.Repeat("1", 40),
		Checkout: &sourceref.Checkout{Path: "hairglasses-studio/example"}, ObservedAt: now.Add(-time.Hour),
		Lifecycle: sourceref.LifecycleActive, OwnershipClass: sourceref.OwnershipRepoLocalProjection,
		EvidenceClass: sourceref.EvidenceFreshWorktree, ObservationCommand: "git ls-remote origin refs/heads/main; git rev-parse HEAD",
	}}}
	data, err := sourceref.Marshal(inventory, sourceref.Options{Now: now, MaxAge: 2 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source-refs.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summary, jsonOutput, err := executeSourceRef([]string{"check", path, "--json", "--max-age", "2h"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || summary.Repositories != 1 || summary.Authoritative != 1 || summary.Dirty != 0 || len(summary.InventorySHA256) != 64 {
		t.Fatalf("unexpected summary: %#v, json=%v", summary, jsonOutput)
	}
}

func TestExecuteSourceRefRejectsStaleAndOversizedInputs(t *testing.T) {
	now := time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "source-refs.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"repositories":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeSourceRef([]string{"check", path, "--max-age", "0s"}, now); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("zero max age error = %v", err)
	}
	large := make([]byte, sourceref.MaxDocumentBytes+1)
	if err := os.WriteFile(path, large, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeSourceRef([]string{"check", path}, now); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized inventory error = %v", err)
	}
}
