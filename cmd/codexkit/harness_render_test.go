package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

type harnessRenderFixture struct {
	args          []string
	sourceRoot    string
	sourcePath    string
	manifestPath  string
	objectID      string
	renderedPath  string
	planSHA256    string
	expectedBytes []byte
	manifest      harnessmanifest.Manifest
	inventory     sourceref.Inventory
	plan          harnessplan.Plan
	now           time.Time
	maxAge        time.Duration
	gitSHA        string
	verifierIDs   []string
}

func newHarnessRenderFixture(t *testing.T, mode harnessmanifest.ProjectionMode) harnessRenderFixture {
	t.Helper()
	work := canonicalTempDir(t)
	sourceRoot := filepath.Join(work, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceRoot, "codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourceBytes := []byte("canonical source\n")
	sourcePath := filepath.Join(sourceRoot, "codex", "input.txt")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC)
	maxAge := 24 * time.Hour
	gitSHA := strings.Repeat("a", 40)
	remote := "https://github.com/hairglasses-studio/codex-harness-staging.git"
	objectID := "native-config"
	projectedBytes := sourceBytes
	object := harnessmanifest.Object{
		ID: objectID, Repo: "canary", Source: "codex/input.txt", Destination: ".codex/config.toml",
		Inputs: []harnessmanifest.SourceInput{{
			Repository: "hairglasses-studio/codex-harness-staging", Remote: remote,
			Ref: "refs/heads/main", Commit: gitSHA, Path: "codex/input.txt", BlobHash: renderDigest(sourceBytes),
		}},
		OwnershipClass: harnessmanifest.OwnershipCodexHarnessSpecific, SourceDisposition: harnessmanifest.SourceCentralCanonical,
		ProjectionMode: mode, Mode: "0600", SourceHash: renderDigest(sourceBytes),
		VerifierIDs: []string{"projection-check"}, Lifecycle: harnessmanifest.LifecyclePlanned,
		ConflictPolicy: harnessmanifest.ConflictFail, RemovalPolicy: harnessmanifest.RemovalPreserve,
	}
	renderedPath := ""
	if mode == harnessmanifest.ProjectionRender {
		projectedBytes = []byte("deterministic rendered output\n")
		object.Generator = "render-canary"
		object.GeneratorCommit = gitSHA
		object.Transform = "v1"
		renderedPath = "rendered/output.txt"
		if err := os.MkdirAll(filepath.Join(sourceRoot, "rendered"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceRoot, filepath.FromSlash(renderedPath)), projectedBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	object.ProjectedHash = renderDigest(projectedBytes)
	manifest := harnessmanifest.Manifest{
		SchemaVersion: harnessmanifest.SchemaVersion,
		Repo:          "canary",
		Objects:       []harnessmanifest.Object{object},
		CoverageInputs: []harnessmanifest.CoverageInput{{
			ID: "native-config", Path: object.Destination,
		}},
	}
	verifierIDs := []string{"projection-check"}
	manifestOptions := harnessmanifest.ValidationOptions{RegisteredVerifierIDs: verifierIDs}
	manifestBytes, err := harnessmanifest.Marshal(manifest, manifestOptions)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(work, "projection.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(work, "verifiers.tsv")
	registry := "id\tcommand\tscope\texecutable_in_manifest_lane\tnotes\nprojection-check\tcheck\ttest\ttrue\ttest verifier\n"
	if err := os.WriteFile(registryPath, []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}

	observedAt := now.Add(-time.Hour)
	evidence := sourceref.RepositoryEvidence{
		Repository: "hairglasses-studio/codex-harness-staging", RemoteURL: remote,
		RequestedRef: "refs/heads/main", RequestedSHA: gitSHA, TestedRef: "HEAD", TestedSHA: gitSHA,
		Checkout: &sourceref.Checkout{Path: "hairglasses-studio/codex-harness-staging"}, ObservedAt: observedAt,
		Lifecycle: sourceref.LifecycleActive, OwnershipClass: sourceref.OwnershipCodexHarnessSpecific,
		EvidenceClass: sourceref.EvidenceFreshWorktree, ObservationCommand: "git rev-parse HEAD",
	}
	inventory := sourceref.Inventory{SchemaVersion: sourceref.SchemaVersion, Repositories: []sourceref.RepositoryEvidence{evidence}}
	inventoryOptions := sourceref.Options{Now: now, MaxAge: maxAge}
	inventoryBytes, err := sourceref.Marshal(inventory, inventoryOptions)
	if err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(work, "inventory.json")
	if err := os.WriteFile(inventoryPath, inventoryBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	stateHash := renderDigest(projectedBytes)
	marker := &harnessplan.MarkerExpectation{Owner: "codex-harness", ObjectID: objectID, StateHash: stateHash}
	planOptions := harnessplan.ValidationOptions{
		ExpectedManifestDigest: renderDigest(manifestBytes), ExpectedInventoryDigest: renderDigest(inventoryBytes),
		ExpectedSourceSHA: gitSHA, EvaluationTime: now,
	}
	plan := harnessplan.Plan{
		ID: "render-canary", TargetRepository: "hairglasses-studio/canary", GeneratorSHA: gitSHA,
		ManifestDigest: planOptions.ExpectedManifestDigest, InventoryDigest: planOptions.ExpectedInventoryDigest,
		PrimarySourceBinding: "harness", ExpectedSourceSHA: gitSHA,
		SourceBindings: []harnessplan.SourceBinding{{
			ID: "harness", Repository: evidence.Repository, RemoteURL: remote,
			RequestedRef: evidence.RequestedRef, RequestedSHA: gitSHA, TestedRef: evidence.TestedRef, TestedSHA: gitSHA,
			EvidenceClass: harnessplan.EvidenceFreshWorktree, ObservedAt: observedAt, ValidUntil: observedAt.Add(maxAge),
		}},
		Operations: []harnessplan.Operation{{
			ID: "create-config", ObjectID: objectID, Kind: harnessplan.OperationCreate,
			Destination: object.Destination, SourceBindingIDs: []string{"harness"},
			Before:  harnessplan.FileState{Type: harnessplan.FileAbsent},
			After:   harnessplan.FileState{Type: harnessplan.FileRegular, Mode: "0600", Hash: stateHash, Marker: marker},
			Payload: &harnessplan.PayloadRef{Hash: stateHash, Size: int64(len(projectedBytes))},
		}},
	}
	envelope, err := harnessplan.Seal(plan, planOptions)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := harnessplan.Marshal(envelope, planOptions)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(work, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"render-check", manifestPath,
		"--inventory", inventoryPath,
		"--plan", planPath,
		"--verifier-registry", registryPath,
		"--source-root", sourceRoot,
		"--reviewed-plan-sha256", envelope.PlanSHA256,
		"--target-repo", "hairglasses-studio/canary",
		"--manifest-repo-id", "canary",
		"--marker-owner", "codex-harness",
		"--expected-source-sha", gitSHA,
		"--now", now.Format(time.RFC3339),
		"--max-age", maxAge.String(),
		"--json",
	}
	if renderedPath != "" {
		args = append(args, "--rendered", objectID+"="+renderedPath)
	}
	return harnessRenderFixture{
		args: args, sourceRoot: sourceRoot, sourcePath: sourcePath, manifestPath: manifestPath,
		objectID: objectID, renderedPath: renderedPath, planSHA256: envelope.PlanSHA256, expectedBytes: projectedBytes,
		manifest: manifest, inventory: inventory, plan: plan, now: now, maxAge: maxAge, gitSHA: gitSHA, verifierIDs: verifierIDs,
	}
}

func (fixture *harnessRenderFixture) rewriteTrustFiles(t *testing.T) {
	t.Helper()
	manifestOptions := harnessmanifest.ValidationOptions{RegisteredVerifierIDs: fixture.verifierIDs}
	manifestBytes, err := harnessmanifest.Marshal(fixture.manifest, manifestOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	inventoryOptions := sourceref.Options{Now: fixture.now, MaxAge: fixture.maxAge}
	inventoryBytes, err := sourceref.Marshal(fixture.inventory, inventoryOptions)
	if err != nil {
		t.Fatal(err)
	}
	inventoryPath := flagValue(fixture.args, "--inventory")
	if err := os.WriteFile(inventoryPath, inventoryBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.plan.ManifestDigest = renderDigest(manifestBytes)
	fixture.plan.InventoryDigest = renderDigest(inventoryBytes)
	planOptions := harnessplan.ValidationOptions{
		ExpectedManifestDigest: fixture.plan.ManifestDigest, ExpectedInventoryDigest: fixture.plan.InventoryDigest,
		ExpectedSourceSHA: fixture.gitSHA, EvaluationTime: fixture.now,
	}
	envelope, err := harnessplan.Seal(fixture.plan, planOptions)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := harnessplan.Marshal(envelope, planOptions)
	if err != nil {
		t.Fatal(err)
	}
	planPath := flagValue(fixture.args, "--plan")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.plan = envelope.Plan
	fixture.planSHA256 = envelope.PlanSHA256
	fixture.args = replaceFlagValue(fixture.args, "--reviewed-plan-sha256", envelope.PlanSHA256)
}

func TestExecuteHarnessRenderCopyCanaryIsReadOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-pinned harness filesystem requires Linux")
	}
	fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
	before := snapshotTree(t, fixture.sourceRoot)
	summary, jsonOutput, err := executeHarnessRender(fixture.args)
	if err != nil {
		t.Fatalf("executeHarnessRender() error = %v", err)
	}
	if !jsonOutput || summary.Artifacts != 1 || summary.RegularFiles != 1 || summary.Symlinks != 0 || summary.Removals != 0 {
		t.Fatalf("unexpected summary: %+v json=%v", summary, jsonOutput)
	}
	if summary.TotalBytes != int64(len(fixture.expectedBytes)) || summary.PlanSHA256 != fixture.planSHA256 || summary.WriteCapable {
		t.Fatalf("unexpected trust summary: %+v", summary)
	}
	if summary.ManifestDigest == "" || summary.InventoryDigest == "" {
		t.Fatalf("summary omitted digests: %+v", summary)
	}
	after := snapshotTree(t, fixture.sourceRoot)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("render-check mutated source root:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestExecuteHarnessRenderRejectsUnknownFlag(t *testing.T) {
	fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
	args := append(append([]string{}, fixture.args...), "--unknown-render-flag")
	if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "unknown harness render flag") {
		t.Fatalf("unknown flag error = %v", err)
	}
}

func TestExecuteHarnessRenderRejectsTraversalAndSymlinkSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-pinned harness filesystem requires Linux")
	}
	t.Run("manifest source traversal", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
		data, err := os.ReadFile(fixture.manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.Replace(data, []byte(`"path": "codex/input.txt"`), []byte(`"path": "../input.txt"`), 1)
		if err := os.WriteFile(fixture.manifestPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := executeHarnessRender(fixture.args); err == nil || !strings.Contains(err.Error(), "parent segments") {
			t.Fatalf("manifest traversal error = %v", err)
		}
	})
	t.Run("rendered traversal", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionRender)
		args := removeFlagValues(fixture.args, "--rendered")
		args = append(args, "--rendered", fixture.objectID+"=../outside")
		if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "portable and relative") {
			t.Fatalf("traversal error = %v", err)
		}
	})
	t.Run("symlink input", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
		outside := filepath.Join(filepath.Dir(fixture.sourceRoot), "outside.txt")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(fixture.sourcePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, fixture.sourcePath); err != nil {
			t.Fatal(err)
		}
		if _, _, err := executeHarnessRender(fixture.args); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("symlink source error = %v", err)
		}
	})
}

func TestExecuteHarnessRenderRejectsPlanTrustMismatch(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		value string
		want  string
	}{
		{name: "reviewed plan SHA", flag: "--reviewed-plan-sha256", value: "sha256:" + strings.Repeat("f", 64), want: "independently reviewed plan SHA"},
		{name: "target repository", flag: "--target-repo", value: "hairglasses-studio/other", want: "independently reviewed target"},
		{name: "manifest repository", flag: "--manifest-repo-id", value: "other", want: "independently reviewed manifest identity"},
		{name: "marker owner", flag: "--marker-owner", value: "other-owner", want: "reviewed marker owner"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
			// Make material I/O fail if a trust check is accidentally moved after
			// source reads. The trust mismatch must remain the reported error.
			if err := os.Remove(fixture.sourcePath); err != nil {
				t.Fatal(err)
			}
			args := replaceFlagValue(fixture.args, test.flag, test.value)
			if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("trust mismatch error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExecuteHarnessRenderRejectsBindingBeforeMaterialIO(t *testing.T) {
	fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
	if err := os.Remove(fixture.sourcePath); err != nil {
		t.Fatal(err)
	}
	secondarySHA := strings.Repeat("b", 40)
	observedAt := fixture.now.Add(-2 * time.Hour)
	secondaryEvidence := sourceref.RepositoryEvidence{
		Repository: "hairglasses-studio/docs", RemoteURL: "https://github.com/hairglasses-studio/docs.git",
		RequestedRef: "refs/heads/main", RequestedSHA: secondarySHA, TestedRef: "refs/heads/main", TestedSHA: secondarySHA,
		Checkout: &sourceref.Checkout{Path: "hairglasses-studio/docs", Dirty: true}, ObservedAt: observedAt,
		Lifecycle: sourceref.LifecycleActive, OwnershipClass: sourceref.OwnershipSharedProviderNeutral,
		EvidenceClass: sourceref.EvidenceRemoteTree, ObservationCommand: "git ls-tree refs/remotes/origin/main",
	}
	fixture.inventory.Repositories = append(fixture.inventory.Repositories, secondaryEvidence)
	fixture.plan.SourceBindings = append(fixture.plan.SourceBindings, harnessplan.SourceBinding{
		ID: "secondary", Repository: secondaryEvidence.Repository, RemoteURL: secondaryEvidence.RemoteURL,
		RequestedRef: secondaryEvidence.RequestedRef, RequestedSHA: secondarySHA,
		TestedRef: secondaryEvidence.TestedRef, TestedSHA: secondarySHA,
		EvidenceClass: harnessplan.EvidenceRemoteTree, CheckoutDirty: true,
		ObservedAt: observedAt, ValidUntil: observedAt.Add(fixture.maxAge),
	})
	fixture.plan.Operations[0].SourceBindingIDs = append(fixture.plan.Operations[0].SourceBindingIDs, "secondary")
	fixture.rewriteTrustFiles(t)

	if _, _, err := executeHarnessRender(fixture.args); err == nil || !strings.Contains(err.Error(), "source_binding_ids") {
		t.Fatalf("binding preflight error = %v", err)
	}
}

func TestExecuteHarnessRenderSanitizesMissingAbsolutePaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]string, string) []string
		want   string
	}{
		{name: "manifest", mutate: func(args []string, missing string) []string {
			result := append([]string{}, args...)
			result[1] = missing
			return result
		}, want: "projection manifest"},
		{name: "inventory", mutate: func(args []string, missing string) []string {
			return replaceFlagValue(args, "--inventory", missing)
		}, want: "source inventory"},
		{name: "plan", mutate: func(args []string, missing string) []string {
			return replaceFlagValue(args, "--plan", missing)
		}, want: "harness plan"},
		{name: "verifier registry", mutate: func(args []string, missing string) []string {
			return replaceFlagValue(args, "--verifier-registry", missing)
		}, want: "verifier registry"},
		{name: "source root", mutate: func(args []string, missing string) []string {
			return replaceFlagValue(args, "--source-root", missing)
		}, want: "safe contained directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
			missing := filepath.Join(canonicalTempDir(t), "absolute-local-secret", "missing.json")
			args := test.mutate(fixture.args, missing)
			_, _, err := executeHarnessRender(args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("missing path error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), filepath.Dir(missing)) {
				t.Fatalf("error leaked absolute path %q: %v", missing, err)
			}
		})
	}
}

func TestValidateHarnessRenderSingleSourceRootRejectsSecondaryRepository(t *testing.T) {
	const primarySHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const primaryRemote = "https://github.com/hairglasses-studio/primary.git"
	manifest := harnessmanifest.Manifest{Objects: []harnessmanifest.Object{{
		ID: "cross-repo", Inputs: []harnessmanifest.SourceInput{{
			Repository: "hairglasses-studio/secondary", Remote: primaryRemote,
			Ref: "refs/heads/main", Commit: primarySHA,
		}},
	}}}
	envelope := harnessplan.PlanEnvelope{Plan: harnessplan.Plan{
		PrimarySourceBinding: "primary",
		SourceBindings: []harnessplan.SourceBinding{{
			ID: "primary", Repository: "hairglasses-studio/primary", RemoteURL: primaryRemote,
			RequestedRef: "refs/heads/main", RequestedSHA: primarySHA, TestedSHA: primarySHA,
		}},
		Operations: []harnessplan.Operation{{ObjectID: "cross-repo", SourceBindingIDs: []string{"primary"}}},
	}}
	if err := validateHarnessRenderSingleSourceRoot(manifest, envelope); err == nil || !strings.Contains(err.Error(), "exact primary source identity") {
		t.Fatalf("single-root repository error = %v", err)
	}

	manifest.Objects[0].Inputs[0].Repository = "hairglasses-studio/primary"
	if err := validateHarnessRenderSingleSourceRoot(manifest, envelope); err != nil {
		t.Fatalf("primary source repository rejected: %v", err)
	}

	for _, mutate := range []func(*harnessmanifest.SourceInput){
		func(input *harnessmanifest.SourceInput) {
			input.Remote = "https://mirror.example.com/hairglasses-studio/primary.git"
		},
		func(input *harnessmanifest.SourceInput) { input.Ref = "refs/heads/release" },
		func(input *harnessmanifest.SourceInput) { input.Commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
	} {
		candidate := manifest
		candidate.Objects = append([]harnessmanifest.Object{}, manifest.Objects...)
		candidate.Objects[0].Inputs = append([]harnessmanifest.SourceInput{}, manifest.Objects[0].Inputs...)
		mutate(&candidate.Objects[0].Inputs[0])
		if err := validateHarnessRenderSingleSourceRoot(candidate, envelope); err == nil || !strings.Contains(err.Error(), "exact primary source identity") {
			t.Fatalf("single-root exact identity error = %v", err)
		}
	}

	envelope.Plan.Operations[0].SourceBindingIDs = []string{"primary", "secondary"}
	if err := validateHarnessRenderSingleSourceRoot(manifest, envelope); err == nil || !strings.Contains(err.Error(), "exclusively") {
		t.Fatalf("single-root binding-set error = %v", err)
	}
}

func TestExecuteHarnessRenderRenderedMappingValidation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-pinned harness filesystem requires Linux")
	}
	t.Run("valid", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionRender)
		summary, _, err := executeHarnessRender(fixture.args)
		if err != nil {
			t.Fatalf("valid render mapping error = %v", err)
		}
		if summary.TotalBytes != int64(len(fixture.expectedBytes)) || summary.RegularFiles != 1 {
			t.Fatalf("unexpected render summary: %+v", summary)
		}
	})
	t.Run("missing", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionRender)
		args := removeFlagValues(fixture.args, "--rendered")
		if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "is required") {
			t.Fatalf("missing render mapping error = %v", err)
		}
	})
	t.Run("extra", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
		args := append(append([]string{}, fixture.args...), "--rendered", fixture.objectID+"=codex/input.txt")
		if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "is not required by the plan") {
			t.Fatalf("extra render mapping error = %v", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionRender)
		args := removeFlagValues(fixture.args, "--rendered")
		args = append(args, "--rendered", "missing-separator")
		if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "object-id=relative-path") {
			t.Fatalf("malformed render mapping error = %v", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionRender)
		args := append(append([]string{}, fixture.args...), "--rendered", fixture.objectID+"="+fixture.renderedPath)
		if _, _, err := executeHarnessRender(args); err == nil || !strings.Contains(err.Error(), "duplicates object id") {
			t.Fatalf("duplicate render mapping error = %v", err)
		}
	})
}

func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s|%s", filepath.ToSlash(relative), info.Mode())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line += "|" + fmt.Sprintf("%x", content)
		}
		snapshot = append(snapshot, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(snapshot)
	return snapshot
}

func replaceFlagValue(args []string, flag, value string) []string {
	result := append([]string{}, args...)
	for index := range result {
		if result[index] == flag && index+1 < len(result) {
			result[index+1] = value
			return result
		}
	}
	return result
}

func flagValue(args []string, flag string) string {
	for index := range args {
		if args[index] == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func removeFlagValues(args []string, flag string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == flag && index+1 < len(args) {
			index++
			continue
		}
		result = append(result, args[index])
	}
	return result
}

func TestHarnessRenderSummaryContainsNoArtifactBytesOrPaths(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-pinned harness filesystem requires Linux")
	}
	fixture := newHarnessRenderFixture(t, harnessmanifest.ProjectionCopy)
	summary, _, err := executeHarnessRender(fixture.args)
	if err != nil {
		t.Fatal(err)
	}
	encoded := fmt.Sprintf("%+v", summary)
	if bytes.Contains([]byte(encoded), fixture.expectedBytes) || strings.Contains(encoded, fixture.sourceRoot) || strings.Contains(encoded, ".codex/config.toml") {
		t.Fatalf("summary leaked artifact content or paths: %s", encoded)
	}
}
