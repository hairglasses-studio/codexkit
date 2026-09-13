//go:build linux

// Hermetic lifecycle tests for the journal, receipt, and ownership-ledger
// contracts. Every case drives the real harnessrender.Build output through the
// real openat2/renameat2 substrate against a real temporary target home and a
// real 0700 state store. Nothing here is stubbed.
//
// WHAT THESE FIXTURES CANNOT COVER (fixture-limits rule — say it, do not let a
// green run imply it):
//
//   - Power loss and lying storage. TestInterruptedApply* kill a real process
//     with SIGKILL, which proves the on-disk evidence a dead process leaves
//     behind. It does not prove durability across a power cut: the kernel page
//     cache survives SIGKILL, so an fsync that a device acknowledged without
//     persisting would still read back correctly here. No fixture in this
//     package can distinguish an honest fsync from a lying one.
//   - Data-volume-dependent defects. Payloads are tens of bytes and plans have
//     at most four operations. A defect whose reachability depends on a payload
//     crossing a page, pipe, or buffer boundary, or on operation counts near
//     MaxOperations / MaxLedgerEntries, cannot occur here. The generation
//     probe's exponential/binary search is exercised only over single-digit
//     generation counts.
//   - Timing-dependent races between processes. The interruption points are
//     deterministic hook phases inside one process, not a scheduler race. A
//     TOCTOU window that only opens under concurrent external mutation between
//     an lstat and a rename is not reproduced; internal/harnessfs owns that
//     class and tests it with its own -race -count=10 gate.
//   - Filesystem variation. renameat2 RENAME_NOREPLACE and RENAME_EXCHANGE
//     support differs across filesystems; these run only on whatever backs
//     t.TempDir(). Overlayfs, NFS, and tmpfs-vs-ext4 differences are untested.
//   - Non-Linux behavior. The unsupported build is compile-checked by
//     scripts/check-harness-lifecycle.sh and asserted for fail-closed errors in
//     the portable test file; its runtime behavior on a real macOS or BSD host
//     is not observed anywhere.
package harnesslifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/harnessrender"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

const (
	testHarnessSHA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testMarkerOwner    = "codex-harness-staging"
	testTargetRepo     = "hairglasses-studio/demo"
	testManifestRepo   = "demo"
	testCopyPath       = ".codex/config.toml"
	testLinkPath       = ".codex/link"
	testRenderPath     = ".codex/generated.toml"
	testLegacyPath     = ".codex/old"
	testSymlinkTarget  = "../shared/tool"
	crashPhaseEnv      = "CODEX_HARNESS_LIFECYCLE_CRASH_PHASE"
	crashSequenceEnv   = "CODEX_HARNESS_LIFECYCLE_CRASH_SEQUENCE"
	crashStatePathEnv  = "CODEX_HARNESS_LIFECYCLE_CRASH_STATE"
	crashTargetPathEnv = "CODEX_HARNESS_LIFECYCLE_CRASH_TARGET"
)

var testEvaluationTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func testClock() func() time.Time {
	return func() time.Time { return testEvaluationTime }
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// objectSpec selects one manifest object and the single plan operation that
// must accompany it. Build requires exactly one operation per actionable
// object, so the two are always chosen together.
type objectSpec struct {
	id          string
	destination string
	kind        harnessplan.OperationKind
	// symlink selects a relative-symlink projection instead of a copy.
	symlink bool
	// content is the regular-file payload for create and replace operations.
	content []byte
	// beforeContent is the pre-image a replace or remove operation expects.
	beforeContent []byte
}

func copySpec(kind harnessplan.OperationKind, content, before []byte) objectSpec {
	return objectSpec{id: "copy-config", destination: testCopyPath, kind: kind, content: content, beforeContent: before}
}

func linkSpec() objectSpec {
	return objectSpec{id: "link-tool", destination: testLinkPath, kind: harnessplan.OperationCreate, symlink: true}
}

func renderSpec(kind harnessplan.OperationKind, content, before []byte) objectSpec {
	return objectSpec{id: "render-config", destination: testRenderPath, kind: kind, content: content, beforeContent: before}
}

func legacySpec(kind harnessplan.OperationKind, content, before []byte) objectSpec {
	return objectSpec{id: "legacy-config", destination: testLegacyPath, kind: kind, content: content, beforeContent: before}
}

// buildResult assembles a manifest, an exact-source inventory, and a sealed
// plan for the requested specs, then returns the real renderer's output.
func buildResult(t testing.TB, specs ...objectSpec) harnessrender.Result {
	t.Helper()

	sourceInput := func(path string, content []byte) harnessmanifest.SourceInput {
		return harnessmanifest.SourceInput{
			Repository: "hairglasses-studio/codex-harness-staging",
			Remote:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
			Ref:        "refs/heads/main",
			Commit:     testHarnessSHA,
			Path:       path,
			BlobHash:   digest(content),
		}
	}

	objects := make([]harnessmanifest.Object, 0, len(specs))
	coverage := make([]harnessmanifest.CoverageInput, 0, len(specs))
	materials := make([]harnessrender.Materials, 0, len(specs))
	operations := make([]harnessplan.Operation, 0, len(specs))

	for index, spec := range specs {
		sourcePath := "codex/" + spec.id
		sourceBytes := []byte("source-" + spec.id + "\n")
		input := sourceInput(sourcePath, sourceBytes)

		object := harnessmanifest.Object{
			ID:                spec.id,
			Repo:              testManifestRepo,
			Source:            sourcePath,
			Inputs:            []harnessmanifest.SourceInput{input},
			Destination:       spec.destination,
			OwnershipClass:    harnessmanifest.OwnershipCodexHarnessSpecific,
			SourceDisposition: harnessmanifest.SourceCentralCanonical,
			ProjectionMode:    harnessmanifest.ProjectionCopy,
			Mode:              "0644",
			SourceHash:        input.BlobHash,
			ProjectedHash:     input.BlobHash,
			VerifierIDs:       []string{"projection-check"},
			Lifecycle:         harnessmanifest.LifecycleActive,
			ConflictPolicy:    harnessmanifest.ConflictReplaceOwned,
			RemovalPolicy:     harnessmanifest.RemovalPreserve,
		}
		material := harnessrender.Materials{ObjectID: spec.id, Inputs: [][]byte{sourceBytes}}

		var after harnessplan.FileState
		var payload *harnessplan.PayloadRef

		switch {
		case spec.kind == harnessplan.OperationRemoveTombstone:
			object.ProjectionMode = harnessmanifest.ProjectionNone
			object.Mode = ""
			object.ProjectedHash = ""
			object.Lifecycle = harnessmanifest.LifecycleRetired
			object.RemovalPolicy = harnessmanifest.RemovalTombstoneOnly
			object.ConflictPolicy = harnessmanifest.ConflictFail
			object.Tombstone = true
			after = harnessplan.FileState{Type: harnessplan.FileAbsent}

		case spec.symlink:
			object.ProjectionMode = harnessmanifest.ProjectionRelativeSymlink
			object.Mode = ""
			object.ProjectedHash = digest([]byte(testSymlinkTarget))
			object.SymlinkTarget = testSymlinkTarget
			after = harnessplan.FileState{
				Type:          harnessplan.FileRelativeSymlink,
				Hash:          object.ProjectedHash,
				SymlinkTarget: testSymlinkTarget,
				Marker: &harnessplan.MarkerExpectation{
					Owner: testMarkerOwner, ObjectID: spec.id, StateHash: object.ProjectedHash,
				},
			}

		default:
			object.ProjectionMode = harnessmanifest.ProjectionRender
			object.Generator = "render-object"
			object.GeneratorCommit = testHarnessSHA
			object.Transform = "v1"
			object.ProjectedHash = digest(spec.content)
			material.Rendered = &harnessrender.RenderedPayload{Bytes: spec.content}
			after = harnessplan.FileState{
				Type: harnessplan.FileRegular,
				Mode: "0644",
				Hash: object.ProjectedHash,
				Marker: &harnessplan.MarkerExpectation{
					Owner: testMarkerOwner, ObjectID: spec.id, StateHash: object.ProjectedHash,
				},
			}
			payload = &harnessplan.PayloadRef{Hash: object.ProjectedHash, Size: int64(len(spec.content))}
		}

		before := harnessplan.FileState{Type: harnessplan.FileAbsent}
		if spec.kind != harnessplan.OperationCreate {
			beforeHash := digest(spec.beforeContent)
			before = harnessplan.FileState{
				Type: harnessplan.FileRegular,
				Mode: "0644",
				Hash: beforeHash,
				Marker: &harnessplan.MarkerExpectation{
					Owner: testMarkerOwner, ObjectID: spec.id, StateHash: beforeHash,
				},
			}
		}

		objects = append(objects, object)
		coverage = append(coverage, harnessmanifest.CoverageInput{
			ID: "found-" + strconv.Itoa(index), Path: spec.destination,
		})
		materials = append(materials, material)
		operations = append(operations, harnessplan.Operation{
			ID:               "op-" + spec.id,
			ObjectID:         spec.id,
			Kind:             spec.kind,
			Destination:      spec.destination,
			SourceBindingIDs: []string{"harness-main"},
			Before:           before,
			After:            after,
			Payload:          payload,
		})
	}

	manifest := harnessmanifest.Manifest{
		SchemaVersion:  harnessmanifest.SchemaVersion,
		Repo:           testManifestRepo,
		Objects:        objects,
		CoverageInputs: coverage,
	}
	observed := testEvaluationTime.Add(-time.Hour)
	inventory := sourceref.Inventory{
		SchemaVersion: sourceref.SchemaVersion,
		Repositories: []sourceref.RepositoryEvidence{{
			Repository:         "hairglasses-studio/codex-harness-staging",
			RemoteURL:          "https://github.com/hairglasses-studio/codex-harness-staging.git",
			RequestedRef:       "refs/heads/main",
			RequestedSHA:       testHarnessSHA,
			TestedRef:          "refs/heads/main",
			TestedSHA:          testHarnessSHA,
			Checkout:           &sourceref.Checkout{Path: "worktrees/codex-harness-staging", Dirty: false},
			ObservedAt:         observed,
			Lifecycle:          sourceref.LifecycleActive,
			OwnershipClass:     sourceref.OwnershipCodexHarnessSpecific,
			EvidenceClass:      sourceref.EvidenceFreshWorktree,
			ObservationCommand: "git -C worktrees/codex-harness-staging rev-parse HEAD",
		}},
	}

	options := harnessrender.Options{
		RegisteredVerifierIDs: []string{"projection-check"},
		Trust: harnessrender.Trust{
			TargetRepository:   testTargetRepo,
			ManifestRepository: testManifestRepo,
			MarkerOwner:        testMarkerOwner,
			SourceSHA:          testHarnessSHA,
			EvaluationTime:     testEvaluationTime,
		},
	}

	manifestBytes, err := harnessmanifest.Marshal(manifest, harnessmanifest.ValidationOptions{
		RegisteredVerifierIDs: options.RegisteredVerifierIDs,
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	inventoryBytes, err := sourceref.Marshal(inventory, sourceref.Options{Now: testEvaluationTime})
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}

	plan := harnessplan.Plan{
		ID:                   "install-demo",
		TargetRepository:     testTargetRepo,
		GeneratorSHA:         testHarnessSHA,
		ManifestDigest:       digest(manifestBytes),
		InventoryDigest:      digest(inventoryBytes),
		PrimarySourceBinding: "harness-main",
		ExpectedSourceSHA:    testHarnessSHA,
		SourceBindings: []harnessplan.SourceBinding{{
			ID:            "harness-main",
			Repository:    "hairglasses-studio/codex-harness-staging",
			RemoteURL:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
			RequestedRef:  "refs/heads/main",
			RequestedSHA:  testHarnessSHA,
			TestedRef:     "refs/heads/main",
			TestedSHA:     testHarnessSHA,
			EvidenceClass: harnessplan.EvidenceFreshWorktree,
			ObservedAt:    observed,
			ValidUntil:    observed.Add(sourceref.DefaultMaxAge),
		}},
		Operations: operations,
	}
	sealed, err := harnessplan.Seal(plan, harnessplan.ValidationOptions{
		ExpectedManifestDigest:  plan.ManifestDigest,
		ExpectedInventoryDigest: plan.InventoryDigest,
		ExpectedSourceSHA:       testHarnessSHA,
		EvaluationTime:          testEvaluationTime,
	})
	if err != nil {
		t.Fatalf("seal plan: %v", err)
	}
	options.Trust.ReviewedPlanSHA256 = sealed.PlanSHA256

	result, err := harnessrender.Build(manifest, inventory, sealed, materials, options)
	if err != nil {
		t.Fatalf("harnessrender.Build: %v", err)
	}
	return result
}

type harness struct {
	statePath  string
	targetPath string
}

func newHarness(t testing.TB) harness {
	t.Helper()
	root := t.TempDir()
	statePath := filepath.Join(root, "state")
	targetPath := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(targetPath, ".codex"), 0o755); err != nil {
		t.Fatalf("create target home: %v", err)
	}
	return harness{statePath: statePath, targetPath: targetPath}
}

func (h harness) apply(t testing.TB, result harnessrender.Result) (Receipt, error) {
	t.Helper()
	return Apply(ApplyOptions{
		StatePath:       h.statePath,
		TargetPath:      h.targetPath,
		Result:          result,
		MarkerOwner:     testMarkerOwner,
		TombstoneReason: TombstoneManifestRemoval,
		Now:             testClock(),
	})
}

func (h harness) target(parts ...string) string {
	return filepath.Join(append([]string{h.targetPath}, parts...)...)
}

func (h harness) ledger(t testing.TB) Ledger {
	t.Helper()
	store, err := OpenStore(h.statePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ledger, _, err := store.LoadCurrentLedger(testTargetRepo, testMarkerOwner, testClock())
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	return ledger
}

func (h harness) readStoreDocument(t testing.TB, leaf string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.statePath, leaf))
	if err != nil {
		t.Fatalf("read %s: %v", leaf, err)
	}
	return data
}

func (h harness) rollback(t testing.TB, generation int) (Receipt, error) {
	t.Helper()
	return Rollback(RollbackOptions{
		StatePath:        h.statePath,
		TargetPath:       h.targetPath,
		TargetRepository: testTargetRepo,
		MarkerOwner:      testMarkerOwner,
		Generation:       generation,
		Now:              testClock(),
	})
}

func (h harness) recover(t testing.TB, result harnessrender.Result, generation int) (Receipt, error) {
	t.Helper()
	return Recover(RecoverOptions{
		StatePath:       h.statePath,
		TargetPath:      h.targetPath,
		Result:          result,
		MarkerOwner:     testMarkerOwner,
		TombstoneReason: TombstoneManifestRemoval,
		Generation:      generation,
		Now:             testClock(),
	})
}

func (h harness) uninstall(t testing.TB) (Receipt, error) {
	t.Helper()
	return Uninstall(UninstallOptions{
		StatePath:        h.statePath,
		TargetPath:       h.targetPath,
		TargetRepository: testTargetRepo,
		MarkerOwner:      testMarkerOwner,
		Now:              testClock(),
	})
}

func (h harness) incomplete(t testing.TB) []Incomplete {
	t.Helper()
	findings, err := Inspect(h.statePath)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	return findings
}

func TestCleanInstallJournalsBeforeMutatingAndRecordsOwnership(t *testing.T) {
	h := newHarness(t)
	content := []byte("managed = true\n")
	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, content, nil),
		linkSpec(),
	)

	receipt, err := h.apply(t, result)
	if err != nil {
		t.Fatalf("Apply(clean install) error = %v", err)
	}
	if receipt.Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q, want %q", receipt.Outcome, OutcomeApplied)
	}
	if len(receipt.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(receipt.Results))
	}
	for _, result := range receipt.Results {
		if result.Status != StepApplied {
			t.Fatalf("result %q status = %q, want %q", result.Destination, result.Status, StepApplied)
		}
	}

	installed, err := os.ReadFile(h.target(testCopyPath))
	if err != nil {
		t.Fatalf("read installed regular file: %v", err)
	}
	if !bytes.Equal(installed, content) {
		t.Fatalf("installed content = %q, want %q", installed, content)
	}
	info, err := os.Lstat(h.target(testCopyPath))
	if err != nil {
		t.Fatalf("lstat installed file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("installed mode = %04o, want 0644", info.Mode().Perm())
	}
	linkTarget, err := os.Readlink(h.target(testLinkPath))
	if err != nil {
		t.Fatalf("readlink installed symlink: %v", err)
	}
	if linkTarget != testSymlinkTarget {
		t.Fatalf("symlink target = %q, want %q", linkTarget, testSymlinkTarget)
	}

	journal, err := LoadJournal(h.readStoreDocument(t, journalLeaf(0)))
	if err != nil {
		t.Fatalf("load journal: %v", err)
	}
	if !journal.Authorized || len(journal.Intents) != 2 {
		t.Fatalf("journal authorized=%v intents=%d, want true/2", journal.Authorized, len(journal.Intents))
	}
	for _, intent := range journal.Intents {
		if intent.Observed.Type != harnessplan.FileAbsent {
			t.Fatalf("journaled prior state for %q = %q, want absent", intent.Destination, intent.Observed.Type)
		}
	}
	for sequence := range journal.Intents {
		if _, err := LoadStep(h.readStoreDocument(t, stepLeaf(0, sequence))); err != nil {
			t.Fatalf("load step %d: %v", sequence, err)
		}
	}

	ledger := h.ledger(t)
	owned := ledger.OwnedDestinations()
	if len(owned) != 2 || owned[0] != testCopyPath || owned[1] != testLinkPath {
		t.Fatalf("owned destinations = %v", owned)
	}
	if findings := h.incomplete(t); len(findings) != 0 {
		t.Fatalf("incomplete applies after a clean install = %+v", findings)
	}
}

func TestJournalIsDurableBeforeTheFirstMutation(t *testing.T) {
	h := newHarness(t)
	result := buildResult(t, copySpec(harnessplan.OperationCreate, []byte("first\n"), nil))

	var journalSeen, destinationSeen bool
	applyTestHook = func(phase applyPhase, _ int) {
		if phase != phaseAfterJournalCommit {
			return
		}
		if _, err := os.Stat(filepath.Join(h.statePath, journalLeaf(0))); err == nil {
			journalSeen = true
		}
		if _, err := os.Lstat(h.target(testCopyPath)); err == nil {
			destinationSeen = true
		}
	}
	t.Cleanup(func() { applyTestHook = nil })

	if _, err := h.apply(t, result); err != nil {
		t.Fatalf("Apply error = %v", err)
	}
	if !journalSeen {
		t.Fatal("journal was not durable when the first mutation was about to begin")
	}
	if destinationSeen {
		t.Fatal("destination already existed before the journal-guarded mutation phase")
	}
}

func TestCollisionWithPreexistingUserFileRefusesAndMutatesNothing(t *testing.T) {
	h := newHarness(t)
	foreign := []byte("# hand written by the operator\n")
	if err := os.WriteFile(h.target(testCopyPath), foreign, 0o600); err != nil {
		t.Fatalf("seed foreign file: %v", err)
	}
	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil),
		linkSpec(),
	)

	receipt, err := h.apply(t, result)
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("Apply(collision) error = %v, want ErrCollision", err)
	}
	if receipt.Outcome != OutcomeRefused {
		t.Fatalf("outcome = %q, want %q", receipt.Outcome, OutcomeRefused)
	}
	if receipt.Results[0].Reason != ReasonCollision {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonCollision)
	}
	if receipt.Results[1].Status != StepNotAttempted {
		t.Fatalf("second result status = %q, want %q", receipt.Results[1].Status, StepNotAttempted)
	}

	after, err := os.ReadFile(h.target(testCopyPath))
	if err != nil || !bytes.Equal(after, foreign) {
		t.Fatalf("foreign file changed: content=%q err=%v", after, err)
	}
	if _, err := os.Lstat(h.target(testLinkPath)); !os.IsNotExist(err) {
		t.Fatalf("unrelated destination was created despite refusal: %v", err)
	}

	journal, err := LoadJournal(h.readStoreDocument(t, journalLeaf(0)))
	if err != nil {
		t.Fatalf("load journal: %v", err)
	}
	if journal.Authorized || len(journal.Intents) != 0 {
		t.Fatalf("refused apply journaled intents: authorized=%v intents=%d", journal.Authorized, len(journal.Intents))
	}
	if ledger := h.ledger(t); len(ledger.Entries) != 0 {
		t.Fatalf("refused apply recorded ownership: %+v", ledger.Entries)
	}
}

func TestReplaceRefusesADestinationTheLedgerDoesNotOwn(t *testing.T) {
	h := newHarness(t)
	before := []byte("pre-existing\n")
	if err := os.WriteFile(h.target(testRenderPath), before, 0o644); err != nil {
		t.Fatalf("seed unowned file: %v", err)
	}
	// The on-disk state matches the plan's before state exactly. Only the
	// missing ledger row stands between the engine and overwriting a file it
	// does not own.
	result := buildResult(t, renderSpec(harnessplan.OperationReplaceOwned, []byte("managed\n"), before))

	receipt, err := h.apply(t, result)
	if !errors.Is(err, ErrUnowned) {
		t.Fatalf("Apply(unowned replace) error = %v, want ErrUnowned", err)
	}
	if receipt.Results[0].Reason != ReasonUnowned {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonUnowned)
	}
	after, err := os.ReadFile(h.target(testRenderPath))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("unowned file was modified: content=%q err=%v", after, err)
	}
}

func TestRemoveRefusesADestinationTheLedgerDoesNotOwn(t *testing.T) {
	h := newHarness(t)
	before := []byte("operator data\n")
	if err := os.WriteFile(h.target(testLegacyPath), before, 0o644); err != nil {
		t.Fatalf("seed unowned file: %v", err)
	}
	result := buildResult(t, legacySpec(harnessplan.OperationRemoveTombstone, nil, before))

	receipt, err := h.apply(t, result)
	if !errors.Is(err, ErrUnowned) {
		t.Fatalf("Apply(unowned remove) error = %v, want ErrUnowned", err)
	}
	if receipt.Results[0].Reason != ReasonUnowned {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonUnowned)
	}
	after, err := os.ReadFile(h.target(testLegacyPath))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("unowned file was removed or modified: content=%q err=%v", after, err)
	}
}

func TestRemoveTombstonesOwnedObjectAndRetainsIt(t *testing.T) {
	h := newHarness(t)
	content := []byte("retired payload\n")
	if _, err := h.apply(t, buildResult(t, legacySpec(harnessplan.OperationCreate, content, nil))); err != nil {
		t.Fatalf("install: %v", err)
	}

	receipt, err := h.apply(t, buildResult(t, legacySpec(harnessplan.OperationRemoveTombstone, nil, content)))
	if err != nil {
		t.Fatalf("Apply(remove) error = %v", err)
	}
	if receipt.Outcome != OutcomeApplied || receipt.Results[0].Status != StepApplied {
		t.Fatalf("remove receipt = %+v", receipt)
	}
	if _, err := os.Lstat(h.target(testLegacyPath)); !os.IsNotExist(err) {
		t.Fatalf("destination still present after removal: %v", err)
	}

	ledger := h.ledger(t)
	if len(ledger.Entries) != 1 {
		t.Fatalf("ledger rows = %d, want 1 retained row", len(ledger.Entries))
	}
	entry := ledger.Entries[0]
	if entry.Status != LedgerTombstoned || entry.Installed != nil || len(entry.Tombstones) != 1 {
		t.Fatalf("tombstoned row = %+v", entry)
	}
	quarantine := entry.Tombstones[0].QuarantinePath
	if quarantine == "" {
		t.Fatal("tombstone did not name the retained object")
	}
	retained, err := os.ReadFile(h.target(quarantine))
	if err != nil {
		t.Fatalf("retained object is not readable — the substrate unlinked it: %v", err)
	}
	if !bytes.Equal(retained, content) {
		t.Fatalf("retained content = %q, want %q", retained, content)
	}
	if got := ledger.RetainedObjects(); len(got) != 1 || got[0] != quarantine {
		t.Fatalf("RetainedObjects() = %v, want [%q]", got, quarantine)
	}
}

func TestTombstonedDestinationCannotBeRemovedTwice(t *testing.T) {
	h := newHarness(t)
	content := []byte("retired payload\n")
	if _, err := h.apply(t, buildResult(t, legacySpec(harnessplan.OperationCreate, content, nil))); err != nil {
		t.Fatalf("install: %v", err)
	}
	removal := buildResult(t, legacySpec(harnessplan.OperationRemoveTombstone, nil, content))
	if _, err := h.apply(t, removal); err != nil {
		t.Fatalf("first remove: %v", err)
	}

	receipt, err := h.apply(t, removal)
	if !errors.Is(err, ErrUnowned) {
		t.Fatalf("second remove error = %v, want ErrUnowned", err)
	}
	if receipt.Results[0].Reason != ReasonTombstoned {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonTombstoned)
	}
	ledger := h.ledger(t)
	if len(ledger.Entries) != 1 || len(ledger.Entries[0].Tombstones) != 1 {
		t.Fatalf("second removal mutated the ledger: %+v", ledger.Entries)
	}
}

func TestIdempotentReapplySkipsWithoutTouchingTheInode(t *testing.T) {
	h := newHarness(t)
	result := buildResult(t, copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil), linkSpec())
	if _, err := h.apply(t, result); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.Lstat(h.target(testCopyPath))
	if err != nil {
		t.Fatalf("lstat after first apply: %v", err)
	}

	receipt, err := h.apply(t, result)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if receipt.Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q, want %q", receipt.Outcome, OutcomeApplied)
	}
	for _, item := range receipt.Results {
		if item.Status != StepSkippedIdempotent {
			t.Fatalf("result %q status = %q, want %q", item.Destination, item.Status, StepSkippedIdempotent)
		}
	}
	second, err := os.Lstat(h.target(testCopyPath))
	if err != nil {
		t.Fatalf("lstat after second apply: %v", err)
	}
	if first.Sys().(*syscall.Stat_t).Ino != second.Sys().(*syscall.Stat_t).Ino {
		t.Fatal("idempotent re-apply replaced the inode")
	}
}

func TestReplaceOfAnOwnedObjectRetainsThePreImage(t *testing.T) {
	h := newHarness(t)
	original := []byte("v1\n")
	upgraded := []byte("v2\n")
	if _, err := h.apply(t, buildResult(t, renderSpec(harnessplan.OperationCreate, original, nil))); err != nil {
		t.Fatalf("install: %v", err)
	}

	receipt, err := h.apply(t, buildResult(t, renderSpec(harnessplan.OperationReplaceOwned, upgraded, original)))
	if err != nil {
		t.Fatalf("Apply(replace) error = %v", err)
	}
	if receipt.Results[0].Status != StepApplied {
		t.Fatalf("replace status = %q", receipt.Results[0].Status)
	}
	installed, err := os.ReadFile(h.target(testRenderPath))
	if err != nil || !bytes.Equal(installed, upgraded) {
		t.Fatalf("installed content = %q err=%v, want %q", installed, err, upgraded)
	}
	retained := receipt.Results[0].Retained
	if retained == "" {
		t.Fatal("replace did not record a retained pre-image")
	}
	preImage, err := os.ReadFile(h.target(retained))
	if err != nil || !bytes.Equal(preImage, original) {
		t.Fatalf("retained pre-image = %q err=%v, want %q", preImage, err, original)
	}
	ledger := h.ledger(t)
	entry, found := ledger.Lookup(testRenderPath)
	if !found || entry.Installed == nil || entry.Installed.Hash != digest(upgraded) {
		t.Fatalf("ledger did not record the upgraded state: %+v", entry)
	}
}

func TestReceiptCarriesNoAbsolutePathsOrPayloadBytes(t *testing.T) {
	h := newHarness(t)
	secretish := []byte("TOKEN=not-a-real-secret-value\n")
	if _, err := h.apply(t, buildResult(t, copySpec(harnessplan.OperationCreate, secretish, nil))); err != nil {
		t.Fatalf("install: %v", err)
	}
	encoded := h.readStoreDocument(t, receiptLeaf(0))
	if bytes.Contains(encoded, secretish) || bytes.Contains(encoded, []byte("TOKEN=")) {
		t.Fatalf("receipt contains payload bytes:\n%s", encoded)
	}
	if bytes.Contains(encoded, []byte(h.targetPath)) || bytes.Contains(encoded, []byte(h.statePath)) {
		t.Fatalf("receipt contains a host-absolute path:\n%s", encoded)
	}
	if _, err := LoadReceipt(encoded); err != nil {
		t.Fatalf("stored receipt does not satisfy its own contract: %v", err)
	}
}

func TestStoreRefusesAStateRootThatIsNotOperatorPrivate(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("create state root: %v", err)
	}
	if _, err := OpenStore(statePath); !errors.Is(err, ErrInvalidLedger) {
		t.Fatalf("OpenStore(0755) error = %v, want ErrInvalidLedger", err)
	}
}

func TestApplyRefusesADestinationWhoseParentIsMissing(t *testing.T) {
	root := t.TempDir()
	h := harness{statePath: filepath.Join(root, "state"), targetPath: filepath.Join(root, "home")}
	if err := os.MkdirAll(h.targetPath, 0o755); err != nil {
		t.Fatalf("create target home: %v", err)
	}
	// .codex/ deliberately absent: directory creation is not in the plan
	// language, so the engine refuses instead of inventing a directory whose
	// ownership nothing records.
	result := buildResult(t, copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil))
	receipt, err := h.apply(t, result)
	if err == nil {
		t.Fatal("Apply(missing parent) succeeded")
	}
	if receipt.Results[0].Reason != ReasonParentMissing {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonParentMissing)
	}
}

func TestApplyRefusesADestinationOccupiedByADirectory(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.target(testCopyPath), 0o755); err != nil {
		t.Fatalf("seed directory at destination: %v", err)
	}
	receipt, _ := h.apply(t, buildResult(t, copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil)))
	if receipt.Results[0].Reason != ReasonUnsupportedFileType {
		t.Fatalf("reason = %q, want %q", receipt.Results[0].Reason, ReasonUnsupportedFileType)
	}
	if info, err := os.Lstat(h.target(testCopyPath)); err != nil || !info.IsDir() {
		t.Fatalf("directory at destination was disturbed: err=%v", err)
	}
}

// TestInterruptedApplyBeforeAnyRenameIsDetectable kills a real process with
// SIGKILL after the journal is durable and before the first rename. Nothing
// runs after SIGKILL — no defers, no flushes — so the store state observed
// afterwards is exactly what a crashed apply leaves behind.
func TestInterruptedApplyBeforeAnyRenameIsDetectable(t *testing.T) {
	h := newHarness(t)
	runCrashHelper(t, h, "after-journal", 0)

	if _, err := os.Lstat(h.target(testCopyPath)); !os.IsNotExist(err) {
		t.Fatalf("destination exists after a pre-rename crash: %v", err)
	}
	findings := h.incomplete(t)
	if len(findings) != 1 {
		t.Fatalf("incomplete applies = %d, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.ReceiptPresent {
		t.Fatal("a killed apply left a receipt")
	}
	if finding.IntentCount != 2 || finding.StepCount != 0 {
		t.Fatalf("finding = %+v, want 2 intents and 0 steps", finding)
	}
	if len(finding.MissingSteps) != 2 || finding.MissingSteps[0] != 0 || finding.MissingSteps[1] != 1 {
		t.Fatalf("missing steps = %v, want [0 1]", finding.MissingSteps)
	}
}

// TestInterruptedApplyMidSequenceIsDetectable kills the process after the
// first transition is recorded and before the second one begins.
func TestInterruptedApplyMidSequenceIsDetectable(t *testing.T) {
	h := newHarness(t)
	runCrashHelper(t, h, "before-transition", 1)

	if _, err := os.Lstat(h.target(testCopyPath)); err != nil {
		t.Fatalf("first destination missing after a mid-sequence crash: %v", err)
	}
	if _, err := os.Lstat(h.target(testLinkPath)); !os.IsNotExist(err) {
		t.Fatalf("second destination exists after a mid-sequence crash: %v", err)
	}
	findings := h.incomplete(t)
	if len(findings) != 1 {
		t.Fatalf("incomplete applies = %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].StepCount != 1 || len(findings[0].MissingSteps) != 1 || findings[0].MissingSteps[0] != 1 {
		t.Fatalf("finding = %+v, want 1 step and missing step 1", findings[0])
	}
	if ledger := h.ledger(t); len(ledger.Entries) != 0 {
		t.Fatalf("a killed apply committed ownership: %+v", ledger.Entries)
	}
}

// TestInspectDetectsAMissingReceiptOnAnOtherwiseCompleteApply is the negative
// control for the detector: without it, an Inspect that always returned an
// empty slice would pass both interruption tests' sibling assertions and the
// clean-install assertion. Removing one receipt from a genuinely complete
// apply must flip the verdict.
func TestInspectDetectsAMissingReceiptOnAnOtherwiseCompleteApply(t *testing.T) {
	h := newHarness(t)
	if _, err := h.apply(t, buildResult(t, copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil))); err != nil {
		t.Fatalf("install: %v", err)
	}
	if findings := h.incomplete(t); len(findings) != 0 {
		t.Fatalf("complete apply reported incomplete: %+v", findings)
	}
	if err := os.Remove(filepath.Join(h.statePath, receiptLeaf(0))); err != nil {
		t.Fatalf("remove receipt: %v", err)
	}
	findings := h.incomplete(t)
	if len(findings) != 1 || findings[0].ReceiptPresent {
		t.Fatalf("detector did not notice the missing receipt: %+v", findings)
	}
}

// TestInspectDetectsAMissingStepOnAnOtherwiseCompleteApply is the second
// negative control: step accounting must be real, not inferred from the
// receipt's presence.
func TestInspectDetectsAMissingStepOnAnOtherwiseCompleteApply(t *testing.T) {
	h := newHarness(t)
	result := buildResult(t, copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil), linkSpec())
	if _, err := h.apply(t, result); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.Remove(filepath.Join(h.statePath, stepLeaf(0, 1))); err != nil {
		t.Fatalf("remove step: %v", err)
	}
	findings := h.incomplete(t)
	if len(findings) != 1 || !findings[0].ReceiptPresent || findings[0].StepCount != 1 {
		t.Fatalf("detector did not notice the missing step: %+v", findings)
	}
}

// runCrashHelper re-executes this test binary so a real process can be killed
// mid-apply, then asserts the child actually died from SIGKILL rather than
// returning normally.
func runCrashHelper(t *testing.T, h harness, phase string, sequence int) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCrashHelperProcess$", "-test.count=1")
	command.Env = append(os.Environ(),
		crashPhaseEnv+"="+phase,
		crashSequenceEnv+"="+strconv.Itoa(sequence),
		crashStatePathEnv+"="+h.statePath,
		crashTargetPathEnv+"="+h.targetPath,
	)
	output, err := command.CombinedOutput()
	exitError := &exec.ExitError{}
	if !errors.As(err, &exitError) {
		t.Fatalf("crash helper did not fail: err=%v output=%s", err, output)
	}
	status, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("crash helper exited without SIGKILL: %v output=%s", exitError, output)
	}
}

// TestCrashHelperProcess is not a test. It is the body of the child process
// launched by runCrashHelper, and it kills itself at a named apply phase.
func TestCrashHelperProcess(t *testing.T) {
	phase := os.Getenv(crashPhaseEnv)
	if phase == "" {
		t.Skip("helper process entry point; driven by runCrashHelper")
	}
	wantSequence, err := strconv.Atoi(os.Getenv(crashSequenceEnv))
	if err != nil {
		t.Fatalf("crash sequence: %v", err)
	}
	h := harness{
		statePath:  os.Getenv(crashStatePathEnv),
		targetPath: os.Getenv(crashTargetPathEnv),
	}

	applyTestHook = func(observed applyPhase, sequence int) {
		switch {
		case phase == "after-journal" && observed == phaseAfterJournalCommit:
		case phase == "before-transition" && observed == phaseBeforeTransition && sequence == wantSequence:
		default:
			return
		}
		// SIGKILL cannot be caught, deferred, or buffered around. Whatever is
		// on disk at this instant is what a crashed apply leaves behind.
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}

	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, []byte("managed\n"), nil),
		linkSpec(),
	)
	receipt, err := h.apply(t, result)
	t.Fatalf("apply returned instead of being killed at %q: receipt=%+v err=%v", phase, receipt, err)
}

func TestGenerationProbeFindsTheEndOfAnAppendOnlySeries(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state")
	store, err := CreateStore(statePath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	for expected := 0; expected < 9; expected++ {
		got, err := store.nextGeneration(journalLeaf)
		if err != nil {
			t.Fatalf("nextGeneration: %v", err)
		}
		if got != expected {
			t.Fatalf("nextGeneration() = %d, want %d", got, expected)
		}
		if err := store.writeNew(journalLeaf(expected), []byte("{}\n")); err != nil {
			t.Fatalf("writeNew: %v", err)
		}
	}
}

func TestStoreRefusesToOverwriteAnExistingGeneration(t *testing.T) {
	root := t.TempDir()
	store, err := CreateStore(filepath.Join(root, "state"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	if err := store.writeNew(journalLeaf(0), []byte("first\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	err = store.writeNew(journalLeaf(0), []byte("second\n"))
	if err == nil {
		t.Fatal("second write to the same generation succeeded")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second write error = %v, want a collision", err)
	}
	data, readErr := store.readDocument(journalLeaf(0))
	if readErr != nil || string(data) != "first\n" {
		t.Fatalf("existing generation was disturbed: %q err=%v", data, readErr)
	}
}

func TestRollbackCleanInstall(t *testing.T) {
	h := newHarness(t)
	content := []byte("managed config\n")
	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, content, nil),
		linkSpec(),
	)

	receipt, err := h.apply(t, result)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if receipt.Outcome != OutcomeApplied {
		t.Fatalf("outcome = %q, want %q", receipt.Outcome, OutcomeApplied)
	}

	if _, err := os.Stat(h.target(testCopyPath)); err != nil {
		t.Fatalf("applied file missing: %v", err)
	}

	rbReceipt, err := h.rollback(t, 0)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rbReceipt.Outcome != OutcomeApplied {
		t.Fatalf("rollback outcome = %q, want %q", rbReceipt.Outcome, OutcomeApplied)
	}

	if _, err := os.Lstat(h.target(testCopyPath)); !os.IsNotExist(err) {
		t.Fatalf("file still exists after rollback: %v", err)
	}
	if _, err := os.Lstat(h.target(testLinkPath)); !os.IsNotExist(err) {
		t.Fatalf("symlink still exists after rollback: %v", err)
	}

	ledger := h.ledger(t)
	if len(ledger.Entries) != 2 {
		t.Fatalf("ledger entry count = %d, want 2", len(ledger.Entries))
	}
	for _, entry := range ledger.Entries {
		if entry.Status != LedgerTombstoned {
			t.Fatalf("entry %s status = %q, want %q", entry.Destination, entry.Status, LedgerTombstoned)
		}
	}
}

func TestRollbackReplaceOwnedRestoresQuarantine(t *testing.T) {
	h := newHarness(t)
	v1 := []byte("version-1 content\n")
	v2 := []byte("version-2 content\n")

	r1 := buildResult(t, copySpec(harnessplan.OperationCreate, v1, nil))
	if _, err := h.apply(t, r1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}

	r2 := buildResult(t, copySpec(harnessplan.OperationReplaceOwned, v2, v1))
	if _, err := h.apply(t, r2); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	gotV2, err := os.ReadFile(h.target(testCopyPath))
	if err != nil || !bytes.Equal(gotV2, v2) {
		t.Fatalf("target file = %q, want %q", gotV2, v2)
	}

	rbReceipt, err := h.rollback(t, 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rbReceipt.Outcome != OutcomeApplied {
		t.Fatalf("rollback outcome = %q, want %q", rbReceipt.Outcome, OutcomeApplied)
	}

	gotV1, err := os.ReadFile(h.target(testCopyPath))
	if err != nil || !bytes.Equal(gotV1, v1) {
		t.Fatalf("target file after rollback = %q, want %q (v1 restored)", gotV1, v1)
	}

	ledger := h.ledger(t)
	entry, found := ledger.Lookup(testCopyPath)
	if !found || entry.Status != LedgerOwned || entry.Installed == nil || entry.Installed.Hash != digest(v1) {
		t.Fatalf("ledger after rollback = %+v", entry)
	}
}

func TestRollbackRemoveTombstoneRestoresQuarantine(t *testing.T) {
	h := newHarness(t)
	v1 := []byte("removable content\n")

	r1 := buildResult(t, copySpec(harnessplan.OperationCreate, v1, nil))
	if _, err := h.apply(t, r1); err != nil {
		t.Fatalf("apply create: %v", err)
	}

	r2 := buildResult(t, copySpec(harnessplan.OperationRemoveTombstone, nil, v1))
	if _, err := h.apply(t, r2); err != nil {
		t.Fatalf("apply remove: %v", err)
	}

	if _, err := os.Lstat(h.target(testCopyPath)); !os.IsNotExist(err) {
		t.Fatalf("file still present after remove: %v", err)
	}

	rbReceipt, err := h.rollback(t, 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rbReceipt.Outcome != OutcomeApplied {
		t.Fatalf("rollback outcome = %q, want %q", rbReceipt.Outcome, OutcomeApplied)
	}

	restored, err := os.ReadFile(h.target(testCopyPath))
	if err != nil || !bytes.Equal(restored, v1) {
		t.Fatalf("target file after remove rollback = %q, want %q", restored, v1)
	}

	ledger := h.ledger(t)
	entry, found := ledger.Lookup(testCopyPath)
	if !found || entry.Status != LedgerOwned {
		t.Fatalf("ledger after remove rollback = %+v", entry)
	}
}

func TestRecoverReplaysInterruptedApply(t *testing.T) {
	h := newHarness(t)
	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, []byte("recovered-copy\n"), nil),
		linkSpec(),
	)

	// Simulate an interrupted apply by applying with a test hook that stops after transition 0
	interrupted := false
	applyTestHook = func(phase applyPhase, sequence int) {
		if phase == phaseAfterStepCommit && sequence == 0 {
			interrupted = true
			panic("test interruption")
		}
	}
	defer func() { applyTestHook = nil }()

	func() {
		defer func() {
			_ = recover()
		}()
		_, _ = h.apply(t, result)
	}()

	if !interrupted {
		t.Fatal("hook did not run to simulate interruption")
	}

	incompletes := h.incomplete(t)
	if len(incompletes) == 0 {
		t.Fatal("incomplete apply was not detected after interruption")
	}

	applyTestHook = nil

	receipt, err := h.recover(t, result, 0)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if receipt.Outcome != OutcomeApplied {
		t.Fatalf("recovered outcome = %q, want %q", receipt.Outcome, OutcomeApplied)
	}

	incompletesAfter := h.incomplete(t)
	if len(incompletesAfter) != 0 {
		t.Fatalf("incomplete applies remain after recovery: %+v", incompletesAfter)
	}

	if _, err := os.Stat(h.target(testCopyPath)); err != nil {
		t.Fatalf("recovered file missing: %v", err)
	}
	if _, err := os.Lstat(h.target(testLinkPath)); err != nil {
		t.Fatalf("recovered link missing: %v", err)
	}
}

func TestUninstallQuarantinesOwnedObjectsAndPreservesUnmanaged(t *testing.T) {
	h := newHarness(t)
	userFile := h.target(".codex", "user_custom.toml")
	userContent := []byte("# User config to preserve\n")
	if err := os.WriteFile(userFile, userContent, 0o600); err != nil {
		t.Fatalf("seed user file: %v", err)
	}

	result := buildResult(t,
		copySpec(harnessplan.OperationCreate, []byte("harness file\n"), nil),
		linkSpec(),
	)
	if _, err := h.apply(t, result); err != nil {
		t.Fatalf("apply: %v", err)
	}

	receipt, err := h.uninstall(t)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if receipt.Outcome != OutcomeApplied {
		t.Fatalf("uninstall outcome = %q, want %q", receipt.Outcome, OutcomeApplied)
	}

	if _, err := os.Lstat(h.target(testCopyPath)); !os.IsNotExist(err) {
		t.Fatalf("harness file not removed: %v", err)
	}
	if _, err := os.Lstat(h.target(testLinkPath)); !os.IsNotExist(err) {
		t.Fatalf("harness symlink not removed: %v", err)
	}

	gotUser, err := os.ReadFile(userFile)
	if err != nil || !bytes.Equal(gotUser, userContent) {
		t.Fatalf("unmanaged user file was disturbed: %q err=%v", gotUser, err)
	}

	// Second uninstall is a clean no-op
	receipt2, err := h.uninstall(t)
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if receipt2.Outcome != OutcomeApplied {
		t.Fatalf("second uninstall outcome = %q", receipt2.Outcome)
	}
}
