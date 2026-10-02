package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
)

func validHarnessCLITestManifest() harnessmanifest.Manifest {
	sourceHash := "sha256:" + strings.Repeat("a", 64)
	projectedHash := "sha256:" + strings.Repeat("b", 64)
	return harnessmanifest.Manifest{
		SchemaVersion: harnessmanifest.SchemaVersion,
		Repo:          "demo",
		Objects: []harnessmanifest.Object{{
			ID: "codex-config", Repo: "demo", Source: "codex/config.toml", Destination: ".codex/config.toml",
			Inputs: []harnessmanifest.SourceInput{{
				Repository: "hairglasses-studio/codex-harness-staging", Remote: "https://github.com/hairglasses-studio/codex-harness-staging.git",
				Ref: "refs/heads/main", Commit: strings.Repeat("c", 40), Path: "codex/config.toml", BlobHash: sourceHash,
			}},
			OwnershipClass: harnessmanifest.OwnershipCodexHarnessSpecific, SourceDisposition: harnessmanifest.SourceCentralCanonical,
			ProjectionMode: harnessmanifest.ProjectionCopy, Mode: "0644", SourceHash: sourceHash, ProjectedHash: projectedHash,
			VerifierIDs: []string{"projection-check"}, Lifecycle: harnessmanifest.LifecyclePlanned,
			ConflictPolicy: harnessmanifest.ConflictFail, RemovalPolicy: harnessmanifest.RemovalPreserve,
		}},
		CoverageInputs: []harnessmanifest.CoverageInput{{ID: "codex-config", Path: ".codex/config.toml"}},
	}
}

func TestExecuteHarnessPlanRebindsTrustedInputs(t *testing.T) {
	now := time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC)
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	gitSHA := strings.Repeat("c", 40)
	stateHash := "sha256:" + strings.Repeat("d", 64)
	options := harnessplan.ValidationOptions{
		ExpectedManifestDigest: digestA, ExpectedInventoryDigest: digestB,
		ExpectedSourceSHA: gitSHA, EvaluationTime: now,
	}
	marker := &harnessplan.MarkerExpectation{Owner: "codex-harness", ObjectID: "native-hooks", StateHash: stateHash}
	plan := harnessplan.Plan{
		ID: "canary", TargetRepository: "hairglasses-studio/canary", GeneratorSHA: gitSHA,
		ManifestDigest: digestA, InventoryDigest: digestB, PrimarySourceBinding: "harness", ExpectedSourceSHA: gitSHA,
		SourceBindings: []harnessplan.SourceBinding{{
			ID: "harness", Repository: "hairglasses-studio/codex-harness-staging",
			RemoteURL:    "https://github.com/hairglasses-studio/codex-harness-staging.git",
			RequestedRef: "refs/heads/main", RequestedSHA: gitSHA, TestedRef: "HEAD", TestedSHA: gitSHA,
			EvidenceClass: harnessplan.EvidenceFreshWorktree, ObservedAt: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour),
		}},
		Operations: []harnessplan.Operation{{
			ID: "create-hooks", ObjectID: "native-hooks", Kind: harnessplan.OperationCreate,
			Destination: ".codex/hooks.json", SourceBindingIDs: []string{"harness"},
			Before:  harnessplan.FileState{Type: harnessplan.FileAbsent},
			After:   harnessplan.FileState{Type: harnessplan.FileRegular, Mode: "0644", Hash: stateHash, Marker: marker},
			Payload: &harnessplan.PayloadRef{Hash: stateHash, Size: 10},
		}},
	}
	envelope, err := harnessplan.Seal(plan, options)
	if err != nil {
		t.Fatal(err)
	}
	data, err := harnessplan.Marshal(envelope, options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(canonicalTempDir(t), "plan.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summary, jsonOutput, err := executeHarnessPlan([]string{
		"plan-check", path, "--manifest-sha256", digestA, "--inventory-sha256", digestB,
		"--expected-source-sha", gitSHA, "--now", now.Format(time.RFC3339), "--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || summary.Operations != 1 || summary.Creates != 1 || summary.WriteCapable || summary.PlanSHA256 == "" {
		t.Fatalf("unexpected summary: %#v json=%v", summary, jsonOutput)
	}
	if _, _, err := executeHarnessPlan([]string{
		"plan-check", path, "--manifest-sha256", digestA, "--inventory-sha256", digestB,
		"--expected-source-sha", strings.Repeat("e", 40), "--now", now.Format(time.RFC3339),
	}); err == nil || !strings.Contains(err.Error(), "trusted expected source SHA") {
		t.Fatalf("mismatched source SHA error = %v", err)
	}
}

func writeHarnessCLITestFiles(t *testing.T) (string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	registryPath := filepath.Join(root, "verifiers.tsv")
	registry := "id\tcommand\tscope\texecutable_in_manifest_lane\tnotes\nprojection-check\tcheck\ttest\ttrue\ttest verifier\n"
	if err := os.WriteFile(registryPath, []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "projection.json")
	options := harnessmanifest.ValidationOptions{RegisteredVerifierIDs: []string{"projection-check"}}
	data, err := harnessmanifest.Marshal(validHarnessCLITestManifest(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath, registryPath
}

func TestExecuteHarnessManifestValidatesCoverageOffline(t *testing.T) {
	manifestPath, registryPath := writeHarnessCLITestFiles(t)
	summary, jsonOutput, err := executeHarnessManifest([]string{
		"manifest-check", manifestPath, "--verifier-registry", registryPath, "--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || summary.Repo != "demo" || summary.Objects != 1 || summary.CoverageInputs != 1 || summary.Projected != 1 || summary.WriteCapable || len(summary.ManifestSHA256) != 64 {
		t.Fatalf("unexpected summary: %#v, json=%v", summary, jsonOutput)
	}
}

func TestExecuteHarnessManifestRejectsMissingRegistryAndOversizedInput(t *testing.T) {
	manifestPath, registryPath := writeHarnessCLITestFiles(t)
	if _, _, err := executeHarnessManifest([]string{"manifest-check", manifestPath}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing registry error = %v", err)
	}
	large := make([]byte, maxVerifierRegistryBytes+1)
	if err := os.WriteFile(registryPath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeHarnessManifest([]string{"manifest-check", manifestPath, "--verifier-registry", registryPath}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized registry error = %v", err)
	}
}
