package harnessrender

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

const (
	testHarnessSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDocsSHA     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testMarkerOwner = "codex-harness-staging"
)

var testEvaluationTime = time.Date(2026, 8, 11, 22, 0, 0, 0, time.UTC)

type buildFixture struct {
	manifest  harnessmanifest.Manifest
	inventory sourceref.Inventory
	envelope  harnessplan.PlanEnvelope
	materials []Materials
	options   Options
}

func newBuildFixture(t testing.TB) buildFixture {
	t.Helper()
	copyBytes := []byte("copy\n")
	templateBytes := []byte("tpl\n")
	fragmentBytes := []byte("frag\n")
	renderedBytes := []byte("rendered\n")
	linkSourceBytes := []byte("link\n")
	tombstoneSourceBytes := []byte("tomb\n")
	referenceBytes := []byte("ref\n")
	noneBytes := []byte("none\n")
	symlinkTarget := "../shared/tool"
	beforeHash := digestBytes([]byte("before\n"))

	harnessInput := func(path string, content []byte) harnessmanifest.SourceInput {
		return harnessmanifest.SourceInput{
			Repository: "hairglasses-studio/codex-harness-staging",
			Remote:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
			Ref:        "refs/heads/main",
			Commit:     testHarnessSHA,
			Path:       path,
			BlobHash:   digestBytes(content),
		}
	}
	docsInput := func(path string, content []byte) harnessmanifest.SourceInput {
		return harnessmanifest.SourceInput{
			Repository: "hairglasses-studio/docs",
			Remote:     "git@github.com:hairglasses-studio/docs.git",
			Ref:        "refs/heads/main",
			Commit:     testDocsSHA,
			Path:       path,
			BlobHash:   digestBytes(content),
		}
	}
	object := func(id, source, destination string, input harnessmanifest.SourceInput) harnessmanifest.Object {
		return harnessmanifest.Object{
			ID:                id,
			Repo:              "demo",
			Source:            source,
			Inputs:            []harnessmanifest.SourceInput{input},
			Destination:       destination,
			OwnershipClass:    harnessmanifest.OwnershipCodexHarnessSpecific,
			SourceDisposition: harnessmanifest.SourceCentralCanonical,
			ProjectionMode:    harnessmanifest.ProjectionCopy,
			Mode:              "0644",
			SourceHash:        input.BlobHash,
			ProjectedHash:     input.BlobHash,
			VerifierIDs:       []string{"projection-check"},
			Lifecycle:         harnessmanifest.LifecycleActive,
			ConflictPolicy:    harnessmanifest.ConflictFail,
			RemovalPolicy:     harnessmanifest.RemovalPreserve,
		}
	}

	copyObject := object("copy-config", "codex/config.toml", ".codex/config.toml", harnessInput("codex/config.toml", copyBytes))
	renderObject := object("render-config", "templates/config.toml", ".codex/generated.toml", harnessInput("templates/config.toml", templateBytes))
	renderObject.Inputs = append(renderObject.Inputs, docsInput("fragments/config.toml", fragmentBytes))
	renderObject.ProjectionMode = harnessmanifest.ProjectionRender
	renderObject.Generator = "render-config"
	renderObject.GeneratorCommit = testHarnessSHA
	renderObject.Transform = "v1"
	renderObject.ProjectedHash = digestBytes(renderedBytes)
	renderObject.ConflictPolicy = harnessmanifest.ConflictReplaceOwned

	symlinkObject := object("link-tool", "codex/tool", ".codex/link", harnessInput("codex/tool", linkSourceBytes))
	symlinkObject.ProjectionMode = harnessmanifest.ProjectionRelativeSymlink
	symlinkObject.Mode = ""
	symlinkObject.ProjectedHash = digestBytes([]byte(symlinkTarget))
	symlinkObject.SymlinkTarget = symlinkTarget

	tombstoneObject := object("remove-old", "tombstones/old", ".codex/old", harnessInput("tombstones/old", tombstoneSourceBytes))
	tombstoneObject.ProjectionMode = harnessmanifest.ProjectionNone
	tombstoneObject.Mode = ""
	tombstoneObject.ProjectedHash = ""
	tombstoneObject.Lifecycle = harnessmanifest.LifecycleRetired
	tombstoneObject.RemovalPolicy = harnessmanifest.RemovalTombstoneOnly
	tombstoneObject.Tombstone = true

	referenceObject := object("external-reference", "references/external.md", "docs/reference", docsInput("references/external.md", referenceBytes))
	referenceObject.OwnershipClass = harnessmanifest.OwnershipExternalOwner
	referenceObject.SourceDisposition = harnessmanifest.SourceExternalOwner
	referenceObject.ProjectionMode = harnessmanifest.ProjectionReference
	referenceObject.Mode = ""
	referenceObject.ProjectedHash = ""

	noneObject := object("external-none", "runtime/state", "runtime/state", docsInput("runtime/state", noneBytes))
	noneObject.OwnershipClass = harnessmanifest.OwnershipExternalOwner
	noneObject.SourceDisposition = harnessmanifest.SourceExternalOwner
	noneObject.ProjectionMode = harnessmanifest.ProjectionNone
	noneObject.Mode = ""
	noneObject.ProjectedHash = ""
	noneObject.Lifecycle = harnessmanifest.LifecyclePlanned

	manifest := harnessmanifest.Manifest{
		SchemaVersion: harnessmanifest.SchemaVersion,
		Repo:          "demo",
		Objects: []harnessmanifest.Object{
			tombstoneObject,
			symlinkObject,
			renderObject,
			copyObject,
			referenceObject,
			noneObject,
		},
		CoverageInputs: []harnessmanifest.CoverageInput{
			{ID: "found-copy", Path: copyObject.Destination},
			{ID: "found-render", Path: renderObject.Destination},
			{ID: "found-link", Path: symlinkObject.Destination},
			{ID: "found-old", Path: tombstoneObject.Destination},
			{ID: "found-reference", Path: referenceObject.Destination},
			{ID: "found-none", Path: noneObject.Destination},
		},
	}

	harnessObserved := testEvaluationTime.Add(-time.Hour)
	docsObserved := testEvaluationTime.Add(-2 * time.Hour)
	inventory := sourceref.Inventory{
		SchemaVersion: sourceref.SchemaVersion,
		Repositories: []sourceref.RepositoryEvidence{
			{
				Repository:         "hairglasses-studio/docs",
				RemoteURL:          "git@github.com:hairglasses-studio/docs.git",
				RequestedRef:       "refs/heads/main",
				RequestedSHA:       testDocsSHA,
				TestedRef:          "refs/heads/main",
				TestedSHA:          testDocsSHA,
				Checkout:           &sourceref.Checkout{Path: "hairglasses-studio/docs", Dirty: true},
				ObservedAt:         docsObserved,
				Lifecycle:          sourceref.LifecycleActive,
				OwnershipClass:     sourceref.OwnershipSharedProviderNeutral,
				EvidenceClass:      sourceref.EvidenceRemoteTree,
				ObservationCommand: "git ls-tree refs/remotes/origin/main",
			},
			{
				Repository:         "hairglasses-studio/codex-harness-staging",
				RemoteURL:          "https://github.com/hairglasses-studio/codex-harness-staging.git",
				RequestedRef:       "refs/heads/main",
				RequestedSHA:       testHarnessSHA,
				TestedRef:          "refs/heads/main",
				TestedSHA:          testHarnessSHA,
				Checkout:           &sourceref.Checkout{Path: "hairglasses-studio/worktrees/codex-harness-staging", Dirty: false},
				ObservedAt:         harnessObserved,
				Lifecycle:          sourceref.LifecycleActive,
				OwnershipClass:     sourceref.OwnershipCodexHarnessSpecific,
				EvidenceClass:      sourceref.EvidenceFreshWorktree,
				ObservationCommand: "git -C hairglasses-studio/worktrees/codex-harness-staging rev-parse HEAD",
			},
		},
	}

	marker := func(objectID, hash string) *harnessplan.MarkerExpectation {
		return &harnessplan.MarkerExpectation{Owner: testMarkerOwner, ObjectID: objectID, StateHash: hash}
	}
	regularAfter := func(object harnessmanifest.Object, size int64) (harnessplan.FileState, *harnessplan.PayloadRef) {
		state := harnessplan.FileState{
			Type:   harnessplan.FileRegular,
			Mode:   object.Mode,
			Hash:   object.ProjectedHash,
			Marker: marker(object.ID, object.ProjectedHash),
		}
		return state, &harnessplan.PayloadRef{Hash: object.ProjectedHash, Size: size}
	}
	copyAfter, copyPayload := regularAfter(copyObject, int64(len(copyBytes)))
	renderAfter, renderPayload := regularAfter(renderObject, int64(len(renderedBytes)))
	plan := harnessplan.Plan{
		ID:                   "install-demo",
		TargetRepository:     "hairglasses-studio/demo",
		GeneratorSHA:         testHarnessSHA,
		PrimarySourceBinding: "harness-main",
		ExpectedSourceSHA:    testHarnessSHA,
		SourceBindings: []harnessplan.SourceBinding{
			{
				ID:            "docs-main",
				Repository:    "hairglasses-studio/docs",
				RemoteURL:     "git@github.com:hairglasses-studio/docs.git",
				RequestedRef:  "refs/heads/main",
				RequestedSHA:  testDocsSHA,
				TestedRef:     "refs/heads/main",
				TestedSHA:     testDocsSHA,
				EvidenceClass: harnessplan.EvidenceRemoteTree,
				CheckoutDirty: true,
				ObservedAt:    docsObserved,
				ValidUntil:    docsObserved.Add(sourceref.DefaultMaxAge),
			},
			{
				ID:            "harness-main",
				Repository:    "hairglasses-studio/codex-harness-staging",
				RemoteURL:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
				RequestedRef:  "refs/heads/main",
				RequestedSHA:  testHarnessSHA,
				TestedRef:     "refs/heads/main",
				TestedSHA:     testHarnessSHA,
				EvidenceClass: harnessplan.EvidenceFreshWorktree,
				CheckoutDirty: false,
				ObservedAt:    harnessObserved,
				ValidUntil:    harnessObserved.Add(sourceref.DefaultMaxAge),
			},
		},
		Operations: []harnessplan.Operation{
			{
				ID:               "remove-old",
				ObjectID:         tombstoneObject.ID,
				Kind:             harnessplan.OperationRemoveTombstone,
				Destination:      tombstoneObject.Destination,
				SourceBindingIDs: []string{"harness-main"},
				Before: harnessplan.FileState{
					Type:   harnessplan.FileRegular,
					Mode:   "0644",
					Hash:   beforeHash,
					Marker: marker(tombstoneObject.ID, beforeHash),
				},
				After: harnessplan.FileState{Type: harnessplan.FileAbsent},
			},
			{
				ID:               "create-link",
				ObjectID:         symlinkObject.ID,
				Kind:             harnessplan.OperationCreate,
				Destination:      symlinkObject.Destination,
				SourceBindingIDs: []string{"harness-main"},
				Before:           harnessplan.FileState{Type: harnessplan.FileAbsent},
				After: harnessplan.FileState{
					Type:          harnessplan.FileRelativeSymlink,
					Hash:          symlinkObject.ProjectedHash,
					SymlinkTarget: symlinkTarget,
					Marker:        marker(symlinkObject.ID, symlinkObject.ProjectedHash),
				},
			},
			{
				ID:               "replace-render",
				ObjectID:         renderObject.ID,
				Kind:             harnessplan.OperationReplaceOwned,
				Destination:      renderObject.Destination,
				SourceBindingIDs: []string{"docs-main", "harness-main"},
				Before: harnessplan.FileState{
					Type:   harnessplan.FileRegular,
					Mode:   "0644",
					Hash:   beforeHash,
					Marker: marker(renderObject.ID, beforeHash),
				},
				After:   renderAfter,
				Payload: renderPayload,
			},
			{
				ID:               "create-copy",
				ObjectID:         copyObject.ID,
				Kind:             harnessplan.OperationCreate,
				Destination:      copyObject.Destination,
				SourceBindingIDs: []string{"harness-main"},
				Before:           harnessplan.FileState{Type: harnessplan.FileAbsent},
				After:            copyAfter,
				Payload:          copyPayload,
			},
		},
	}
	fixture := buildFixture{
		manifest:  manifest,
		inventory: inventory,
		materials: []Materials{
			{ObjectID: renderObject.ID, Inputs: [][]byte{templateBytes, fragmentBytes}, Rendered: &RenderedPayload{Bytes: renderedBytes}},
			{ObjectID: tombstoneObject.ID, Inputs: [][]byte{tombstoneSourceBytes}},
			{ObjectID: copyObject.ID, Inputs: [][]byte{copyBytes}},
			{ObjectID: symlinkObject.ID, Inputs: [][]byte{linkSourceBytes}},
		},
		options: Options{
			RegisteredVerifierIDs: []string{"projection-check"},
			Trust: Trust{
				TargetRepository:   "hairglasses-studio/demo",
				ManifestRepository: "demo",
				MarkerOwner:        testMarkerOwner,
				SourceSHA:          testHarnessSHA,
				EvaluationTime:     testEvaluationTime,
			},
		},
	}
	fixture.envelope.Plan = plan
	fixture.reseal(t)
	return fixture
}

func (fixture *buildFixture) reseal(t testing.TB) {
	t.Helper()
	manifestBytes, err := harnessmanifest.Marshal(fixture.manifest, harnessmanifest.ValidationOptions{
		RegisteredVerifierIDs: fixture.options.RegisteredVerifierIDs,
	})
	if err != nil {
		t.Fatalf("marshal fixture manifest: %v", err)
	}
	inventoryBytes, err := sourceref.Marshal(fixture.inventory, sourceref.Options{
		Now:    fixture.options.Trust.EvaluationTime,
		MaxAge: fixture.options.InventoryMaxAge,
	})
	if err != nil {
		t.Fatalf("marshal fixture inventory: %v", err)
	}
	fixture.envelope.Plan.ManifestDigest = digestBytes(manifestBytes)
	fixture.envelope.Plan.InventoryDigest = digestBytes(inventoryBytes)
	sealed, err := harnessplan.Seal(fixture.envelope.Plan, harnessplan.ValidationOptions{
		ExpectedManifestDigest:  fixture.envelope.Plan.ManifestDigest,
		ExpectedInventoryDigest: fixture.envelope.Plan.InventoryDigest,
		ExpectedSourceSHA:       fixture.options.Trust.SourceSHA,
		EvaluationTime:          fixture.options.Trust.EvaluationTime,
	})
	if err != nil {
		t.Fatalf("seal fixture plan: %v", err)
	}
	fixture.envelope = sealed
	fixture.options.Trust.ReviewedPlanSHA256 = sealed.PlanSHA256
}

func objectIndex(fixture *buildFixture, id string) int {
	for index := range fixture.manifest.Objects {
		if fixture.manifest.Objects[index].ID == id {
			return index
		}
	}
	return -1
}

func operationIndex(fixture *buildFixture, id string) int {
	for index := range fixture.envelope.Plan.Operations {
		if fixture.envelope.Plan.Operations[index].ID == id {
			return index
		}
	}
	return -1
}

func materialIndex(fixture *buildFixture, id string) int {
	for index := range fixture.materials {
		if fixture.materials[index].ObjectID == id {
			return index
		}
	}
	return -1
}

func requireBuildError(t testing.TB, fixture buildFixture, want string) {
	t.Helper()
	_, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Build() error = %v, want substring %q", err, want)
	}
	if !errors.Is(err, ErrInvalidRender) {
		t.Fatalf("Build() error does not match ErrInvalidRender: %v", err)
	}
}

func requireAuthorityError(t testing.TB, fixture buildFixture, want string) {
	t.Helper()
	_, err := ValidateAuthority(fixture.manifest, fixture.inventory, fixture.envelope, fixture.options)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateAuthority() error = %v, want substring %q", err, want)
	}
	if !errors.Is(err, ErrInvalidRender) {
		t.Fatalf("ValidateAuthority() error does not match ErrInvalidRender: %v", err)
	}
}

func TestValidateAuthorityChecksAllMaterialIndependentBindings(t *testing.T) {
	fixture := newBuildFixture(t)
	authority, err := ValidateAuthority(fixture.manifest, fixture.inventory, fixture.envelope, fixture.options)
	if err != nil {
		t.Fatalf("ValidateAuthority() error = %v", err)
	}
	if authority.PlanSHA256 != fixture.envelope.PlanSHA256 ||
		authority.ManifestDigest != fixture.envelope.Plan.ManifestDigest ||
		authority.InventoryDigest != fixture.envelope.Plan.InventoryDigest ||
		authority.TargetRepository != fixture.envelope.Plan.TargetRepository {
		t.Fatalf("ValidateAuthority() = %+v", authority)
	}

	tests := []struct {
		name   string
		want   string
		mutate func(*buildFixture)
	}{
		{name: "marker owner", want: "reviewed marker owner", mutate: func(f *buildFixture) {
			f.envelope.Plan.Operations[operationIndex(f, "create-copy")].After.Marker.Owner = "other-owner"
			f.reseal(t)
		}},
		{name: "remote identity", want: "same normalized remote", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0].Remote = "https://mirror.example.com/hairglasses-studio/codex-harness-staging.git"
			f.reseal(t)
		}},
		{name: "requested ref", want: "inventory requested_ref", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0].Ref = "refs/heads/release"
			f.reseal(t)
		}},
		{name: "commit", want: "inventory requested_sha", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0].Commit = testDocsSHA
			f.reseal(t)
		}},
		{name: "operation binding set", want: "source_binding_ids", mutate: func(f *buildFixture) {
			f.envelope.Plan.Operations[operationIndex(f, "replace-render")].SourceBindingIDs = []string{"harness-main"}
			f.reseal(t)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := newBuildFixture(t)
			test.mutate(&candidate)
			requireAuthorityError(t, candidate, test.want)
		})
	}
}

func TestBuildMaterializesAllSupportedModesDeterministically(t *testing.T) {
	fixture := newBuildFixture(t)
	result, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if result.PlanSHA256 != fixture.envelope.PlanSHA256 || result.TargetRepository != fixture.options.Trust.TargetRepository {
		t.Fatalf("unexpected result trust binding: %+v", result)
	}
	if result.ManifestDigest != fixture.envelope.Plan.ManifestDigest || result.InventoryDigest != fixture.envelope.Plan.InventoryDigest {
		t.Fatalf("result digests do not match sealed plan: %+v", result)
	}
	wantDestinations := []string{".codex/config.toml", ".codex/generated.toml", ".codex/link", ".codex/old"}
	if len(result.Artifacts) != len(wantDestinations) {
		t.Fatalf("artifact count = %d, want %d", len(result.Artifacts), len(wantDestinations))
	}
	for index, want := range wantDestinations {
		if result.Artifacts[index].Destination != want {
			t.Fatalf("artifact[%d].destination = %q, want %q", index, result.Artifacts[index].Destination, want)
		}
	}
	if got := string(result.Artifacts[0].RegularBytes); got != "copy\n" {
		t.Fatalf("copy bytes = %q", got)
	}
	if got := string(result.Artifacts[1].RegularBytes); got != "rendered\n" {
		t.Fatalf("render bytes = %q", got)
	}
	if result.Artifacts[2].After.Type != harnessplan.FileRelativeSymlink || result.Artifacts[2].After.SymlinkTarget != "../shared/tool" || result.Artifacts[2].RegularBytes != nil {
		t.Fatalf("unexpected symlink artifact: %+v", result.Artifacts[2])
	}
	if result.Artifacts[3].Kind != harnessplan.OperationRemoveTombstone || result.Artifacts[3].After.Type != harnessplan.FileAbsent || result.Artifacts[3].RegularBytes != nil {
		t.Fatalf("unexpected tombstone artifact: %+v", result.Artifacts[3])
	}

	second := newBuildFixture(t)
	slices.Reverse(second.materials)
	slices.Reverse(second.manifest.Objects)
	second.reseal(t)
	secondResult, err := Build(second.manifest, second.inventory, second.envelope, second.materials, second.options)
	if err != nil {
		t.Fatalf("Build(reordered) error = %v", err)
	}
	if !reflect.DeepEqual(result, secondResult) {
		t.Fatalf("reordered input changed result:\n%+v\n%+v", result, secondResult)
	}
}

func TestBuildAcceptsEquivalentRemoteSpellings(t *testing.T) {
	fixture := newBuildFixture(t)
	fixture.inventory.Repositories[1].RemoteURL = "git@github.com:hairglasses-studio/codex-harness-staging.git"
	fixture.envelope.Plan.SourceBindings[1].RemoteURL = "ssh://git@github.com/hairglasses-studio/codex-harness-staging.git"
	fixture.reseal(t)

	if _, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options); err != nil {
		t.Fatalf("Build(equivalent remote forms) error = %v", err)
	}
}

func TestBuildDeepCopiesArtifacts(t *testing.T) {
	fixture := newBuildFixture(t)
	result, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	copyMaterial := &fixture.materials[materialIndex(&fixture, "copy-config")]
	copyMaterial.Inputs[0][0] = 'X'
	copyOperation := &fixture.envelope.Plan.Operations[operationIndex(&fixture, "create-copy")]
	copyOperation.After.Marker.Owner = "mutated"
	if got := string(result.Artifacts[0].RegularBytes); got != "copy\n" {
		t.Fatalf("result aliases caller bytes: %q", got)
	}
	if result.Artifacts[0].After.Marker == nil || result.Artifacts[0].After.Marker.Owner != testMarkerOwner {
		t.Fatalf("result aliases caller marker: %+v", result.Artifacts[0].After.Marker)
	}
	result.Artifacts[0].RegularBytes[0] = 'Y'
	if got := string(copyMaterial.Inputs[0]); got != "Xopy\n" {
		t.Fatalf("caller bytes alias result: %q", got)
	}
}

func TestBuildAcceptsZeroOperationPlan(t *testing.T) {
	fixture := newBuildFixture(t)
	fixture.manifest.Objects = []harnessmanifest.Object{
		fixture.manifest.Objects[objectIndex(&fixture, "external-reference")],
		fixture.manifest.Objects[objectIndex(&fixture, "external-none")],
	}
	fixture.manifest.CoverageInputs = []harnessmanifest.CoverageInput{
		{ID: "found-reference", Path: "docs/reference"},
		{ID: "found-none", Path: "runtime/state"},
	}
	fixture.envelope.Plan.Operations = nil
	fixture.materials = nil
	fixture.reseal(t)
	result, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
	if err != nil {
		t.Fatalf("Build(zero operations) error = %v", err)
	}
	if result.Artifacts == nil || len(result.Artifacts) != 0 {
		t.Fatalf("zero-operation artifacts = %#v, want non-nil empty slice", result.Artifacts)
	}
}

func TestBuildRejectsMissingActionableOperations(t *testing.T) {
	tests := []struct {
		name       string
		removeOnly string
	}{
		{name: "partial plan omits copy", removeOnly: "create-copy"},
		{name: "partial plan omits tombstone", removeOnly: "remove-old"},
		{name: "zero operations omit all actionable objects"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBuildFixture(t)
			if test.removeOnly == "" {
				fixture.envelope.Plan.Operations = nil
				fixture.materials = nil
			} else {
				operation := operationIndex(&fixture, test.removeOnly)
				objectID := fixture.envelope.Plan.Operations[operation].ObjectID
				fixture.envelope.Plan.Operations = append(fixture.envelope.Plan.Operations[:operation], fixture.envelope.Plan.Operations[operation+1:]...)
				material := materialIndex(&fixture, objectID)
				fixture.materials = append(fixture.materials[:material], fixture.materials[material+1:]...)
			}
			fixture.reseal(t)
			requireBuildError(t, fixture, "actionable object requires exactly one plan operation")
		})
	}
}

func TestManagedBlockCannotBeOmittedFromPlan(t *testing.T) {
	fixture := newBuildFixture(t)
	object := &fixture.manifest.Objects[objectIndex(&fixture, "copy-config")]
	object.ProjectionMode = harnessmanifest.ProjectionManagedBlock
	object.Generator = "render-block"
	object.GeneratorCommit = testHarnessSHA
	object.Transform = "v1"
	operation := operationIndex(&fixture, "create-copy")
	fixture.envelope.Plan.Operations = append(fixture.envelope.Plan.Operations[:operation], fixture.envelope.Plan.Operations[operation+1:]...)
	material := materialIndex(&fixture, "copy-config")
	fixture.materials = append(fixture.materials[:material], fixture.materials[material+1:]...)
	fixture.reseal(t)

	requireAuthorityError(t, fixture, "managed-block objects are unsupported")
	requireBuildError(t, fixture, "managed-block objects are unsupported")
}

func TestBuildPreservesExplicitEmptyRegularPayload(t *testing.T) {
	fixture := newBuildFixture(t)
	object := &fixture.manifest.Objects[objectIndex(&fixture, "copy-config")]
	emptyHash := digestBytes(nil)
	object.SourceHash = emptyHash
	object.ProjectedHash = emptyHash
	object.Inputs[0].BlobHash = emptyHash
	material := &fixture.materials[materialIndex(&fixture, "copy-config")]
	material.Inputs = [][]byte{{}}
	operation := &fixture.envelope.Plan.Operations[operationIndex(&fixture, "create-copy")]
	operation.After.Hash = emptyHash
	operation.After.Marker.StateHash = emptyHash
	operation.Payload.Hash = emptyHash
	operation.Payload.Size = 0
	fixture.reseal(t)

	result, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
	if err != nil {
		t.Fatalf("Build(empty copy) error = %v", err)
	}
	if result.Artifacts[0].After.Type != harnessplan.FileRegular || result.Artifacts[0].RegularBytes == nil || len(result.Artifacts[0].RegularBytes) != 0 {
		t.Fatalf("empty regular payload not preserved: %#v", result.Artifacts[0].RegularBytes)
	}
}

func TestBuildRejectsInvalidTrustAndUpstreamValues(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*buildFixture)
	}{
		{name: "missing reviewed plan", want: "reviewed_plan_sha256", mutate: func(f *buildFixture) { f.options.Trust.ReviewedPlanSHA256 = "" }},
		{name: "reviewed plan mismatch", want: "independently reviewed", mutate: func(f *buildFixture) { f.options.Trust.ReviewedPlanSHA256 = "sha256:" + strings.Repeat("1", 64) }},
		{name: "target mismatch", want: "reviewed target", mutate: func(f *buildFixture) { f.options.Trust.TargetRepository = "hairglasses-studio/other" }},
		{name: "manifest repo mismatch", want: "reviewed manifest", mutate: func(f *buildFixture) { f.options.Trust.ManifestRepository = "other" }},
		{name: "bad marker owner", want: "marker_owner", mutate: func(f *buildFixture) { f.options.Trust.MarkerOwner = "bad owner" }},
		{name: "source mismatch", want: "trusted expected source", mutate: func(f *buildFixture) { f.options.Trust.SourceSHA = testDocsSHA }},
		{name: "missing time", want: "evaluation_time", mutate: func(f *buildFixture) { f.options.Trust.EvaluationTime = time.Time{} }},
		{name: "negative inventory age", want: "inventory_max_age", mutate: func(f *buildFixture) { f.options.InventoryMaxAge = -time.Second }},
		{name: "raised blob bound", want: "max_blob_bytes", mutate: func(f *buildFixture) { f.options.MaxBlobBytes = MaxMaterialBlobBytes + 1 }},
		{name: "blob above total", want: "must not exceed max_total_bytes", mutate: func(f *buildFixture) { f.options.MaxBlobBytes = 20; f.options.MaxTotalBytes = 10 }},
		{name: "invalid manifest", want: "canonical manifest", mutate: func(f *buildFixture) { f.manifest.SchemaVersion = 99 }},
		{name: "invalid inventory", want: "canonical source inventory", mutate: func(f *buildFixture) {
			f.inventory.Repositories[0].ObservedAt = testEvaluationTime.Add(-48 * time.Hour)
		}},
		{name: "tampered sealed plan", want: "canonical plan digest", mutate: func(f *buildFixture) { f.envelope.Plan.ID = "tampered" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBuildFixture(t)
			test.mutate(&fixture)
			requireBuildError(t, fixture, test.want)
		})
	}
}

func TestBuildRejectsSourceContractDrift(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*buildFixture)
	}{
		{name: "64 hex input commit", want: "64-hex commits", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "copy-config")]
			object.Inputs[0].Commit = strings.Repeat("c", 64)
			f.reseal(t)
		}},
		{name: "64 hex generator commit", want: "64-hex commits", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "render-config")]
			object.GeneratorCommit = strings.Repeat("c", 64)
			f.reseal(t)
		}},
		{name: "non hermetic input", want: "non-hermetic inputs", mutate: func(f *buildFixture) {
			input := &f.manifest.Objects[objectIndex(f, "render-config")].Inputs[1]
			input.NonHermetic = true
			input.NonHermeticReason = "ambient consumer state"
			f.reseal(t)
		}},
		{name: "manifest remote host drift", want: "same normalized remote", mutate: func(f *buildFixture) {
			input := &f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0]
			input.Remote = "https://mirror.example.com/hairglasses-studio/codex-harness-staging.git"
			f.reseal(t)
		}},
		{name: "manifest ref drift", want: "inventory requested_ref", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0].Ref = "main"
			f.reseal(t)
		}},
		{name: "manifest commit drift", want: "inventory requested_sha", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Inputs[0].Commit = testDocsSHA
			f.reseal(t)
		}},
		{name: "plan remote host drift", want: "same normalized remote", mutate: func(f *buildFixture) {
			f.envelope.Plan.SourceBindings[1].RemoteURL = "git@mirror.example.com:hairglasses-studio/codex-harness-staging.git"
			f.reseal(t)
		}},
		{name: "plan observed time drift", want: "inventory observed_at", mutate: func(f *buildFixture) {
			f.envelope.Plan.SourceBindings[1].ObservedAt = f.envelope.Plan.SourceBindings[1].ObservedAt.Add(time.Minute)
			f.reseal(t)
		}},
		{name: "plan expiry exceeds evidence", want: "inventory freshness window", mutate: func(f *buildFixture) {
			f.envelope.Plan.SourceBindings[1].ValidUntil = f.envelope.Plan.SourceBindings[1].ObservedAt.Add(25 * time.Hour)
			f.reseal(t)
		}},
		{name: "ineligible primary evidence", want: "remote-tree or fresh-worktree", mutate: func(f *buildFixture) {
			f.inventory.Repositories[1].EvidenceClass = sourceref.EvidencePrimaryCheckout
			f.envelope.Plan.SourceBindings[1].EvidenceClass = "primary-checkout"
			// Keep the old seal: Build must reject the ineligible current values.
		}},
		{name: "dirty fresh worktree", want: "fresh-worktree evidence cannot be dirty", mutate: func(f *buildFixture) {
			f.inventory.Repositories[1].Checkout.Dirty = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBuildFixture(t)
			test.mutate(&fixture)
			requireBuildError(t, fixture, test.want)
		})
	}
}

func TestBuildRejectsOperationAndObjectDrift(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*buildFixture)
	}{
		{name: "destination mismatch", want: "manifest object destination", mutate: func(f *buildFixture) {
			index := objectIndex(f, "copy-config")
			f.manifest.Objects[index].Destination = ".codex/renamed.toml"
			for coverageIndex := range f.manifest.CoverageInputs {
				if f.manifest.CoverageInputs[coverageIndex].ID == "found-copy" {
					f.manifest.CoverageInputs[coverageIndex].Path = ".codex/renamed.toml"
				}
			}
			f.reseal(t)
		}},
		{name: "mode mismatch", want: "manifest object mode", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].Mode = "0600"
			f.reseal(t)
		}},
		{name: "projected hash mismatch", want: "manifest projected_hash", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "copy-config")].ProjectedHash = digestBytes([]byte("different"))
			f.reseal(t)
		}},
		{name: "payload size mismatch", want: "supplied payload size", mutate: func(f *buildFixture) {
			f.envelope.Plan.Operations[operationIndex(f, "create-copy")].Payload.Size++
			f.reseal(t)
		}},
		{name: "marker owner mismatch", want: "reviewed marker owner", mutate: func(f *buildFixture) {
			f.envelope.Plan.Operations[operationIndex(f, "create-copy")].After.Marker.Owner = "other-owner"
			f.reseal(t)
		}},
		{name: "replace without policy", want: "replace-owned requires", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "render-config")].ConflictPolicy = harnessmanifest.ConflictFail
			f.reseal(t)
		}},
		{name: "generator source mismatch", want: "generator_commit", mutate: func(f *buildFixture) {
			f.manifest.Objects[objectIndex(f, "render-config")].GeneratorCommit = testDocsSHA
			f.reseal(t)
		}},
		{name: "source binding set mismatch", want: "source_binding_ids", mutate: func(f *buildFixture) {
			f.envelope.Plan.Operations[operationIndex(f, "replace-render")].SourceBindingIDs = []string{"harness-main"}
			f.reseal(t)
		}},
		{name: "managed block unsupported", want: "managed-block operations are unsupported", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "copy-config")]
			object.ProjectionMode = harnessmanifest.ProjectionManagedBlock
			object.Generator = "render-block"
			object.GeneratorCommit = testHarnessSHA
			object.Transform = "v1"
			f.reseal(t)
		}},
		{name: "reference has operation", want: "must not have plan operations", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "copy-config")]
			object.OwnershipClass = harnessmanifest.OwnershipExternalOwner
			object.SourceDisposition = harnessmanifest.SourceExternalOwner
			object.ProjectionMode = harnessmanifest.ProjectionReference
			object.Mode = ""
			object.ProjectedHash = ""
			f.reseal(t)
		}},
		{name: "none has operation", want: "must not have plan operations", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "copy-config")]
			object.OwnershipClass = harnessmanifest.OwnershipExternalOwner
			object.SourceDisposition = harnessmanifest.SourceExternalOwner
			object.ProjectionMode = harnessmanifest.ProjectionNone
			object.Mode = ""
			object.ProjectedHash = ""
			object.Lifecycle = harnessmanifest.LifecyclePlanned
			f.reseal(t)
		}},
		{name: "remove without tombstone", want: "exact manifest tombstone", mutate: func(f *buildFixture) {
			object := &f.manifest.Objects[objectIndex(f, "remove-old")]
			object.Tombstone = false
			object.RemovalPolicy = harnessmanifest.RemovalPreserve
			object.Lifecycle = harnessmanifest.LifecyclePlanned
			f.reseal(t)
		}},
		{name: "copy operation is symlink", want: "regular-file for copy", mutate: func(f *buildFixture) {
			operation := &f.envelope.Plan.Operations[operationIndex(f, "create-copy")]
			target := "../shared/copy"
			hash := digestBytes([]byte(target))
			operation.After = harnessplan.FileState{
				Type:          harnessplan.FileRelativeSymlink,
				Hash:          hash,
				SymlinkTarget: target,
				Marker:        &harnessplan.MarkerExpectation{Owner: testMarkerOwner, ObjectID: operation.ObjectID, StateHash: hash},
			}
			operation.Payload = nil
			f.reseal(t)
		}},
		{name: "symlink target mismatch", want: "manifest symlink_target", mutate: func(f *buildFixture) {
			operation := &f.envelope.Plan.Operations[operationIndex(f, "create-link")]
			operation.After.SymlinkTarget = "../shared/other"
			f.reseal(t)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBuildFixture(t)
			test.mutate(&fixture)
			requireBuildError(t, fixture, test.want)
		})
	}
}

func TestBuildRejectsMaterialDriftAndBounds(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*buildFixture)
	}{
		{name: "missing material", want: "requires exactly one material", mutate: func(f *buildFixture) {
			index := materialIndex(f, "copy-config")
			f.materials = append(f.materials[:index], f.materials[index+1:]...)
		}},
		{name: "duplicate material", want: "duplicates a material", mutate: func(f *buildFixture) {
			f.materials[1] = f.materials[0]
		}},
		{name: "unexpected material", want: "operated manifest object", mutate: func(f *buildFixture) {
			f.materials[1] = Materials{ObjectID: "external-reference", Inputs: [][]byte{[]byte("ref\n")}}
		}},
		{name: "more materials than operations", want: "more entries than plan operations", mutate: func(f *buildFixture) {
			f.materials = append(f.materials, Materials{ObjectID: "external-reference"})
		}},
		{name: "inputs exceed matching object", want: "matching manifest object input count", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "copy-config")].Inputs = [][]byte{[]byte("copy\n"), nil}
		}},
		{name: "input count", want: "ordered object inputs", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "render-config")].Inputs = f.materials[materialIndex(f, "render-config")].Inputs[:1]
		}},
		{name: "ordered input swap", want: "ordered manifest input", mutate: func(f *buildFixture) {
			inputs := f.materials[materialIndex(f, "render-config")].Inputs
			inputs[0], inputs[1] = inputs[1], inputs[0]
		}},
		{name: "input blob mutation", want: "blob_hash", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "copy-config")].Inputs[0][0] = 'X'
		}},
		{name: "missing rendered", want: "requires caller-supplied rendered", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "render-config")].Rendered = nil
		}},
		{name: "rendered hash mismatch", want: "manifest projected_hash", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "render-config")].Rendered.Bytes = []byte("wrong\n")
		}},
		{name: "copy rendered bytes", want: "copy projection", mutate: func(f *buildFixture) {
			f.materials[materialIndex(f, "copy-config")].Rendered = &RenderedPayload{Bytes: []byte("extra")}
		}},
		{name: "per blob bound", want: "must not exceed 4 bytes", mutate: func(f *buildFixture) {
			f.options.MaxBlobBytes = 4
			f.options.MaxTotalBytes = 64
		}},
		{name: "aggregate bound", want: "aggregate material bytes", mutate: func(f *buildFixture) {
			f.options.MaxBlobBytes = 10
			f.options.MaxTotalBytes = 20
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBuildFixture(t)
			test.mutate(&fixture)
			requireBuildError(t, fixture, test.want)
		})
	}
}

func TestMaterialCardinalityPreflightBoundsZeroByteEntries(t *testing.T) {
	operations := make([]harnessplan.Operation, harnessplan.MaxOperations)
	tooManyMaterials := make([]Materials, harnessplan.MaxOperations+1)
	if err := validateMaterialCardinality(tooManyMaterials, operations, nil); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("validateMaterialCardinality(over material count) error = %v", err)
	}

	perObject := make([][]byte, MaxMaterialInputsPerObject+1)
	objects := map[string]harnessmanifest.Object{
		"object": {ID: "object", Inputs: make([]harnessmanifest.SourceInput, len(perObject))},
	}
	if err := validateMaterialCardinality([]Materials{{ObjectID: "object", Inputs: perObject}}, []harnessplan.Operation{{ObjectID: "object"}}, objects); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("validateMaterialCardinality(per-object entries) error = %v", err)
	}

	const materialCount = 10
	inputsPerMaterial := MaxAggregateMaterialInputs/materialCount + 1
	materials := make([]Materials, materialCount)
	operations = make([]harnessplan.Operation, materialCount)
	objects = make(map[string]harnessmanifest.Object, materialCount)
	for index := range materials {
		id := "object-" + string(rune('a'+index))
		materials[index] = Materials{ObjectID: id, Inputs: make([][]byte, inputsPerMaterial)}
		operations[index] = harnessplan.Operation{ObjectID: id}
		objects[id] = harnessmanifest.Object{ID: id, Inputs: make([]harnessmanifest.SourceInput, inputsPerMaterial)}
	}
	if err := validateMaterialCardinality(materials, operations, objects); err == nil || !strings.Contains(err.Error(), "aggregate input entries") {
		t.Fatalf("validateMaterialCardinality(aggregate entries) error = %v", err)
	}
}

func FuzzBuildMaterialBytes(f *testing.F) {
	f.Add([]byte("copy\n"), []byte("rendered\n"))
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, copyBytes, renderedBytes []byte) {
		if len(copyBytes) > 1024 || len(renderedBytes) > 1024 {
			t.Skip()
		}
		fixture := newBuildFixture(t)
		fixture.materials[materialIndex(&fixture, "copy-config")].Inputs[0] = slices.Clone(copyBytes)
		fixture.materials[materialIndex(&fixture, "render-config")].Rendered.Bytes = slices.Clone(renderedBytes)
		result, err := Build(fixture.manifest, fixture.inventory, fixture.envelope, fixture.materials, fixture.options)
		if string(copyBytes) == "copy\n" && string(renderedBytes) == "rendered\n" {
			if err != nil || len(result.Artifacts) != 4 {
				t.Fatalf("valid seed: result=%+v error=%v", result, err)
			}
		} else if err == nil {
			t.Fatalf("unreviewed material unexpectedly accepted: copy=%q render=%q", copyBytes, renderedBytes)
		}
	})
}
