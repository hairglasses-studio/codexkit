package harnessplan

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testSHA       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOtherSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testManifest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testInventory = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testBefore    = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	testAfter     = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
)

func validOptions() ValidationOptions {
	return ValidationOptions{
		ExpectedManifestDigest:  testManifest,
		ExpectedInventoryDigest: testInventory,
		ExpectedSourceSHA:       testSHA,
		EvaluationTime:          time.Date(2026, 8, 11, 21, 0, 0, 0, time.UTC),
	}
}

func marker(objectID, hash string) *MarkerExpectation {
	return &MarkerExpectation{Owner: "codex-harness-staging", ObjectID: objectID, StateHash: hash}
}

func validPlan() Plan {
	return Plan{
		ID:                   "install-demo",
		TargetRepository:     "hairglasses-studio/demo",
		GeneratorSHA:         testSHA,
		ManifestDigest:       testManifest,
		InventoryDigest:      testInventory,
		PrimarySourceBinding: "harness-main",
		ExpectedSourceSHA:    testSHA,
		SourceBindings: []SourceBinding{
			{
				ID:            "harness-main",
				Repository:    "hairglasses-studio/codex-harness-staging",
				RemoteURL:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
				RequestedRef:  "refs/heads/main",
				RequestedSHA:  testSHA,
				TestedRef:     "HEAD",
				TestedSHA:     testSHA,
				EvidenceClass: EvidenceFreshWorktree,
				ObservedAt:    time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC),
				ValidUntil:    time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC),
			},
			{
				ID:            "shared-docs",
				Repository:    "hairglasses-studio/docs",
				RemoteURL:     "git@github.com:hairglasses-studio/docs.git",
				RequestedRef:  "refs/heads/main",
				RequestedSHA:  testOtherSHA,
				TestedRef:     "refs/heads/main",
				TestedSHA:     testOtherSHA,
				EvidenceClass: EvidenceRemoteTree,
				CheckoutDirty: true,
				ObservedAt:    time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("offset", -7*60*60)),
				ValidUntil:    time.Date(2026, 8, 12, 12, 0, 0, 0, time.FixedZone("offset", -7*60*60)),
			},
		},
		Operations: []Operation{
			{
				ID:               "create-config",
				ObjectID:         "codex-config",
				Kind:             OperationCreate,
				Destination:      ".codex/config.toml",
				SourceBindingIDs: []string{"harness-main", "shared-docs"},
				Before:           FileState{Type: FileAbsent},
				After: FileState{
					Type:   FileRegular,
					Mode:   "0644",
					Hash:   testAfter,
					Marker: marker("codex-config", testAfter),
				},
				Payload: &PayloadRef{Hash: testAfter, Size: 42},
			},
			{
				ID:               "replace-agent",
				ObjectID:         "planner-agent",
				Kind:             OperationReplaceOwned,
				Destination:      ".codex/agents/planner.toml",
				SourceBindingIDs: []string{"harness-main"},
				Before: FileState{
					Type:   FileRegular,
					Mode:   "0644",
					Hash:   testBefore,
					Marker: marker("planner-agent", testBefore),
				},
				After: FileState{
					Type:   FileRegular,
					Mode:   "0644",
					Hash:   testAfter,
					Marker: marker("planner-agent", testAfter),
				},
				Payload: &PayloadRef{Hash: testAfter, Size: 84},
			},
		},
	}
}

func validEnvelope(t *testing.T) PlanEnvelope {
	t.Helper()
	envelope, err := Seal(validPlan(), validOptions())
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	return envelope
}

func TestSealLoadRoundTrip(t *testing.T) {
	envelope := validEnvelope(t)
	data, err := Marshal(envelope, validOptions())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("Marshal() lacks trailing newline: %q", data)
	}
	if bytes.Contains(data, []byte("-07:00")) {
		t.Fatalf("Marshal() did not canonicalize timestamps to UTC: %s", data)
	}
	loaded, err := Load(data, validOptions())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.PlanSHA256 != envelope.PlanSHA256 || loaded.Plan.ID != "install-demo" {
		t.Fatalf("unexpected loaded envelope: %+v", loaded)
	}
	if !strings.HasPrefix(envelope.PlanSHA256, "sha256:") {
		t.Fatalf("unexpected digest %q", envelope.PlanSHA256)
	}
}

func TestCanonicalMarshalDoesNotMutate(t *testing.T) {
	first := validEnvelope(t)
	secondPlan := validPlan()
	secondPlan.SourceBindings[0], secondPlan.SourceBindings[1] = secondPlan.SourceBindings[1], secondPlan.SourceBindings[0]
	secondPlan.Operations[0], secondPlan.Operations[1] = secondPlan.Operations[1], secondPlan.Operations[0]
	secondPlan.Operations[1].SourceBindingIDs[0], secondPlan.Operations[1].SourceBindingIDs[1] = secondPlan.Operations[1].SourceBindingIDs[1], secondPlan.Operations[1].SourceBindingIDs[0]
	second, err := Seal(secondPlan, validOptions())
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanSHA256 != second.PlanSHA256 {
		t.Fatalf("digest differs by set ordering: %q != %q", first.PlanSHA256, second.PlanSHA256)
	}
	beforeBindings := append([]string(nil), second.Plan.Operations[1].SourceBindingIDs...)
	firstJSON, err := Marshal(first, validOptions())
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := Marshal(second, validOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical output differs:\n%s\n%s", firstJSON, secondJSON)
	}
	if !reflect.DeepEqual(second.Plan.Operations[1].SourceBindingIDs, beforeBindings) {
		t.Fatal("Marshal mutated source binding order")
	}
}

func TestZeroOperationPlanDeterministicRoundTrip(t *testing.T) {
	plan := validPlan()
	plan.Operations = nil

	first, err := Seal(plan, validOptions())
	if err != nil {
		t.Fatalf("Seal(zero operations) error = %v", err)
	}
	firstJSON, err := Marshal(first, validOptions())
	if err != nil {
		t.Fatalf("Marshal(zero operations) error = %v", err)
	}
	loaded, err := Load(firstJSON, validOptions())
	if err != nil {
		t.Fatalf("Load(zero operations) error = %v", err)
	}
	if len(loaded.Plan.Operations) != 0 {
		t.Fatalf("loaded operation count = %d, want 0", len(loaded.Plan.Operations))
	}
	secondJSON, err := Marshal(loaded, validOptions())
	if err != nil {
		t.Fatalf("Marshal(loaded zero operations) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) || first.PlanSHA256 != loaded.PlanSHA256 {
		t.Fatalf("zero-operation round trip is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}
	if !bytes.Contains(firstJSON, []byte(`"operations": []`)) {
		t.Fatalf("zero-operation plan did not canonicalize to an empty array: %s", firstJSON)
	}
	plan.Operations = []Operation{}
	explicitEmpty, err := Seal(plan, validOptions())
	if err != nil {
		t.Fatalf("Seal(explicit empty operations) error = %v", err)
	}
	if explicitEmpty.PlanSHA256 != first.PlanSHA256 {
		t.Fatalf("nil and empty operations produced different digests: %q != %q", first.PlanSHA256, explicitEmpty.PlanSHA256)
	}
}

func TestOperationUpperBound(t *testing.T) {
	plan := validPlan()
	plan.Operations = make([]Operation, MaxOperations+1)
	_, err := Seal(plan, validOptions())
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("Seal(over-limit operations) error = %v", err)
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Seal(over-limit operations) error type = %T", err)
	}
	if len(validationError.Issues) != 1 || validationError.Issues[0].Path != "plan.operations" || validationError.Issues[0].Code != "bounds" {
		t.Fatalf("over-limit validation amplified findings: %+v", validationError.Issues)
	}
}

func TestLoadRejectsNonCanonicalJSONShapes(t *testing.T) {
	data, err := Marshal(validEnvelope(t), validOptions())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "unknown field", data: bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "unknown": true`), 1), want: "unknown field"},
		{name: "duplicate key", data: bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1), want: "duplicate object key"},
		{name: "trailing value", data: append(append([]byte(nil), data...), []byte(`{"extra":true}`)...), want: "trailing JSON"},
		{name: "empty", data: nil, want: "document size"},
		{name: "oversize", data: bytes.Repeat([]byte(" "), MaxDocumentBytes+1), want: "document size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(test.data, validOptions()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsTamperAndTrustedInputDrift(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*PlanEnvelope, *ValidationOptions)
	}{
		{name: "envelope schema", want: "schema_version", mutate: func(e *PlanEnvelope, _ *ValidationOptions) { e.SchemaVersion = 99 }},
		{name: "plan digest tamper", want: "canonical plan digest", mutate: func(e *PlanEnvelope, _ *ValidationOptions) { e.Plan.ID = "tampered" }},
		{name: "malformed plan digest", want: "lowercase sha256", mutate: func(e *PlanEnvelope, _ *ValidationOptions) { e.PlanSHA256 = "sha256:nope" }},
		{name: "manifest input drift", want: "trusted digest", mutate: func(_ *PlanEnvelope, o *ValidationOptions) { o.ExpectedManifestDigest = testBefore }},
		{name: "inventory input drift", want: "trusted digest", mutate: func(_ *PlanEnvelope, o *ValidationOptions) { o.ExpectedInventoryDigest = testBefore }},
		{name: "source input drift", want: "trusted expected source SHA", mutate: func(_ *PlanEnvelope, o *ValidationOptions) { o.ExpectedSourceSHA = testOtherSHA }},
		{name: "missing trusted manifest", want: "expected_manifest_digest", mutate: func(_ *PlanEnvelope, o *ValidationOptions) { o.ExpectedManifestDigest = "" }},
		{name: "missing evaluation time", want: "evaluation_time", mutate: func(_ *PlanEnvelope, o *ValidationOptions) { o.EvaluationTime = time.Time{} }},
		{name: "expired evidence", want: "source evidence has expired", mutate: func(_ *PlanEnvelope, o *ValidationOptions) {
			o.EvaluationTime = time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
		}},
		{name: "future evidence", want: "trusted evaluation time", mutate: func(_ *PlanEnvelope, o *ValidationOptions) {
			o.EvaluationTime = time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := validEnvelope(t)
			options := validOptions()
			test.mutate(&envelope, &options)
			err := Validate(envelope, options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("Validate() error type = %T", err)
			}
		})
	}
}

func TestSealRejectsUnsafeAndIneligiblePlanShapes(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*Plan)
	}{
		{name: "unsafe destination", want: "parent segments", mutate: func(p *Plan) { p.Operations[0].Destination = "../escape" }},
		{name: "absolute destination", want: "repository-relative", mutate: func(p *Plan) { p.Operations[0].Destination = "/tmp/escape" }},
		{name: "destination collision", want: "collides", mutate: func(p *Plan) { p.Operations[1].Destination = p.Operations[0].Destination }},
		{name: "duplicate object", want: "duplicates plan.operations", mutate: func(p *Plan) { p.Operations[1].ObjectID = p.Operations[0].ObjectID }},
		{name: "destination hierarchy overlap", want: "overlaps", mutate: func(p *Plan) { p.Operations[1].Destination = p.Operations[0].Destination + "/child" }},
		{name: "primary checkout", want: "remote-tree or fresh-worktree", mutate: func(p *Plan) { p.SourceBindings[0].EvidenceClass = "primary-checkout" }},
		{name: "dirty fresh worktree", want: "must be clean", mutate: func(p *Plan) { p.SourceBindings[0].CheckoutDirty = true }},
		{name: "requested tested mismatch", want: "must equal requested_sha", mutate: func(p *Plan) { p.SourceBindings[0].TestedSHA = testOtherSHA }},
		{name: "duplicate source repository", want: "duplicates plan.source_bindings", mutate: func(p *Plan) { p.SourceBindings[1].Repository = p.SourceBindings[0].Repository }},
		{name: "remote repository mismatch", want: "does not match", mutate: func(p *Plan) { p.SourceBindings[0].RemoteURL = "https://github.com/hairglasses-studio/other.git" }},
		{name: "unsafe remote scheme", want: "HTTPS or SSH", mutate: func(p *Plan) { p.SourceBindings[0].RemoteURL = "file:///tmp/repo" }},
		{name: "invalid evidence window", want: "must be after observed_at", mutate: func(p *Plan) { p.SourceBindings[0].ValidUntil = p.SourceBindings[0].ObservedAt }},
		{name: "missing observed time", want: "observed_at", mutate: func(p *Plan) { p.SourceBindings[0].ObservedAt = time.Time{} }},
		{name: "expected primary mismatch", want: "primary source binding", mutate: func(p *Plan) { p.PrimarySourceBinding = "shared-docs" }},
		{name: "missing source binding", want: "declared source binding", mutate: func(p *Plan) { p.Operations[0].SourceBindingIDs[0] = "missing" }},
		{name: "duplicate source binding", want: "duplicates a source binding", mutate: func(p *Plan) { p.Operations[0].SourceBindingIDs[1] = p.Operations[0].SourceBindingIDs[0] }},
		{name: "unknown operation", want: "unknown operation", mutate: func(p *Plan) { p.Operations[0].Kind = "chmod" }},
		{name: "create transition", want: "create requires", mutate: func(p *Plan) { p.Operations[0].Before = p.Operations[0].After }},
		{name: "replace transition", want: "replace-owned requires", mutate: func(p *Plan) { p.Operations[1].After = FileState{Type: FileAbsent}; p.Operations[1].Payload = nil }},
		{name: "remove transition", want: "remove-tombstone requires", mutate: func(p *Plan) { p.Operations[0].Kind = OperationRemoveTombstone }},
		{name: "regular missing payload", want: "staged payload", mutate: func(p *Plan) { p.Operations[0].Payload = nil }},
		{name: "payload hash mismatch", want: "after-state hash", mutate: func(p *Plan) { p.Operations[0].Payload.Hash = testBefore }},
		{name: "payload oversize", want: "must be between", mutate: func(p *Plan) { p.Operations[0].Payload.Size = MaxPayloadBytes + 1 }},
		{name: "world writable", want: "group- or world-writable", mutate: func(p *Plan) { p.Operations[0].After.Mode = "0666" }},
		{name: "group writable", want: "group- or world-writable", mutate: func(p *Plan) { p.Operations[0].After.Mode = "0660" }},
		{name: "not owner readable", want: "owner-readable", mutate: func(p *Plan) { p.Operations[0].After.Mode = "0200" }},
		{name: "special mode", want: "four-digit octal", mutate: func(p *Plan) { p.Operations[0].After.Mode = "4755" }},
		{name: "missing marker", want: "ownership marker", mutate: func(p *Plan) { p.Operations[0].After.Marker = nil }},
		{name: "marker object mismatch", want: "operation object_id", mutate: func(p *Plan) { p.Operations[0].After.Marker.ObjectID = "other" }},
		{name: "marker hash mismatch", want: "file-state hash", mutate: func(p *Plan) { p.Operations[0].After.Marker.StateHash = testBefore }},
		{name: "regular symlink target", want: "must not contain", mutate: func(p *Plan) { p.Operations[0].After.SymlinkTarget = "target" }},
		{name: "unknown file type", want: "unknown file type", mutate: func(p *Plan) { p.Operations[0].After.Type = "device"; p.Operations[0].Payload = nil }},
		{name: "generator source mismatch", want: "must equal expected_source_sha", mutate: func(p *Plan) { p.GeneratorSHA = testOtherSHA }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPlan()
			test.mutate(&plan)
			_, err := Seal(plan, validOptions())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Seal() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSymlinkAndTombstoneTransitions(t *testing.T) {
	plan := validPlan()
	op := &plan.Operations[0]
	op.Kind = OperationRemoveTombstone
	op.Before = FileState{
		Type:          FileRelativeSymlink,
		Hash:          testBefore,
		SymlinkTarget: "../agents/planner.toml",
		Marker:        marker(op.ObjectID, testBefore),
	}
	op.After = FileState{Type: FileAbsent}
	op.Payload = nil
	if _, err := Seal(plan, validOptions()); err != nil {
		t.Fatalf("Seal(valid symlink tombstone) error = %v", err)
	}

	op.Before.SymlinkTarget = "../../../etc/passwd"
	if _, err := Seal(plan, validOptions()); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("Seal(escaping symlink) error = %v", err)
	}
}

func TestAggregatePayloadBound(t *testing.T) {
	plan := validPlan()
	template := plan.Operations[0]
	plan.Operations = nil
	for index := range 9 {
		operation := template
		operation.ID = "create-" + string(rune('a'+index))
		operation.ObjectID = "object-" + string(rune('a'+index))
		operation.Destination = ".codex/generated/file-" + string(rune('a'+index))
		operation.SourceBindingIDs = []string{"harness-main"}
		operation.After.Marker = marker(operation.ObjectID, operation.After.Hash)
		operation.Payload = &PayloadRef{Hash: operation.After.Hash, Size: MaxPayloadBytes}
		plan.Operations = append(plan.Operations, operation)
	}
	if _, err := Seal(plan, validOptions()); err == nil || !strings.Contains(err.Error(), "aggregate payload size") {
		t.Fatalf("Seal(oversize aggregate) error = %v", err)
	}
}

func FuzzLoad(f *testing.F) {
	envelope, err := Seal(validPlan(), validOptions())
	if err != nil {
		f.Fatal(err)
	}
	valid, err := Marshal(envelope, validOptions())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"schema_version":1,"schema_version":1}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		loaded, err := Load(data, validOptions())
		if err != nil {
			return
		}
		rendered, err := Marshal(loaded, validOptions())
		if err != nil {
			t.Fatalf("Marshal(Load(data)) error = %v", err)
		}
		if _, err := Load(rendered, validOptions()); err != nil {
			t.Fatalf("Load(Marshal(loaded)) error = %v", err)
		}
	})
}
