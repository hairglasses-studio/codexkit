package harnessmanifest

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const (
	testSourceHash    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testProjectedHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testCommit        = "cccccccccccccccccccccccccccccccccccccccc"
)

func validOptions() ValidationOptions {
	return ValidationOptions{RegisteredVerifierIDs: []string{"projection-check", "runtime-omission-check", "hash-check"}}
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		Repo:          "demo-repo",
		Objects: []Object{{
			ID:          "codex-config",
			Repo:        "demo-repo",
			Source:      "codex/config.toml",
			Destination: ".codex/config.toml",
			Inputs: []SourceInput{{
				Repository: "hairglasses-studio/codex-harness-staging",
				Remote:     "https://github.com/hairglasses-studio/codex-harness-staging.git",
				Ref:        "refs/heads/main",
				Commit:     testCommit,
				Path:       "codex/config.toml",
				BlobHash:   testSourceHash,
			}},
			OwnershipClass:    OwnershipCodexHarnessSpecific,
			SourceDisposition: SourceCentralCanonical,
			ProjectionMode:    ProjectionCopy,
			Mode:              "0644",
			SourceHash:        testSourceHash,
			ProjectedHash:     testProjectedHash,
			VerifierIDs:       []string{"projection-check", "hash-check"},
			Lifecycle:         LifecycleActive,
			ConflictPolicy:    ConflictFail,
			RemovalPolicy:     RemovalPreserve,
		}},
		Omissions: []Omission{{
			ID:             "runtime-state",
			Path:           ".codex/runtime/**",
			Kind:           OmissionGlob,
			OwnershipClass: OwnershipExternalOwner,
			Reason:         "runtime state is host-owned and never projected",
			VerifierIDs:    []string{"runtime-omission-check"},
			Lifecycle:      LifecycleActive,
		}},
		CoverageInputs: []CoverageInput{
			{ID: "found-config", Path: ".codex/config.toml"},
			{ID: "found-runtime", Path: ".codex/runtime/session.json"},
		},
	}
}

func TestValidateAndCoverageReport(t *testing.T) {
	manifest := validManifest()
	if err := Validate(manifest, validOptions()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	report, err := ValidateCoverage(manifest)
	if err != nil {
		t.Fatalf("ValidateCoverage() error = %v", err)
	}
	if report.TotalInputs != 2 || report.Projected != 1 || report.Omitted != 1 || len(report.Assignments) != 2 {
		t.Fatalf("unexpected coverage report: %+v", report)
	}
	if report.Assignments[0].Path != ".codex/config.toml" || report.Assignments[1].Path != ".codex/runtime/session.json" {
		t.Fatalf("assignments are not sorted: %+v", report.Assignments)
	}
}

func TestMarshalIsDeterministicAndDoesNotMutate(t *testing.T) {
	first := validManifest()
	second := validManifest()
	second.Objects[0].VerifierIDs[0], second.Objects[0].VerifierIDs[1] = second.Objects[0].VerifierIDs[1], second.Objects[0].VerifierIDs[0]
	second.CoverageInputs[0], second.CoverageInputs[1] = second.CoverageInputs[1], second.CoverageInputs[0]
	before := append([]string(nil), first.Objects[0].VerifierIDs...)

	firstJSON, err := Marshal(first, validOptions())
	if err != nil {
		t.Fatalf("Marshal(first) error = %v", err)
	}
	secondJSON, err := Marshal(second, validOptions())
	if err != nil {
		t.Fatalf("Marshal(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical JSON differs:\n%s\n%s", firstJSON, secondJSON)
	}
	if !bytes.HasSuffix(firstJSON, []byte("\n")) {
		t.Fatalf("canonical JSON lacks trailing newline: %q", firstJSON)
	}
	if !reflect.DeepEqual(first.Objects[0].VerifierIDs, before) {
		t.Fatalf("Marshal mutated input: got %v want %v", first.Objects[0].VerifierIDs, before)
	}
}

func TestLoadStrictRoundTrip(t *testing.T) {
	data, err := Marshal(validManifest(), validOptions())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(data, validOptions())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Repo != "demo-repo" || len(loaded.Objects) != 1 {
		t.Fatalf("unexpected loaded manifest: %+v", loaded)
	}

	unknown := bytes.Replace(data, []byte(`"repo": "demo-repo"`), []byte(`"repo": "demo-repo", "unexpected": true`), 1)
	if _, err := Load(unknown, validOptions()); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load(unknown field) error = %v", err)
	}
	if _, err := Load(append(data, []byte(`{"extra":true}`)...), validOptions()); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("Load(trailing value) error = %v", err)
	}
	duplicate := bytes.Replace(data, []byte(`"repo": "demo-repo"`), []byte(`"repo": "demo-repo", "repo": "other"`), 1)
	if _, err := Load(duplicate, validOptions()); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("Load(duplicate key) error = %v", err)
	}
	if _, err := Load(make([]byte, MaxDocumentBytes+1), validOptions()); err == nil || !strings.Contains(err.Error(), "document size") {
		t.Fatalf("Load(oversized document) error = %v", err)
	}
}

func TestValidateRejectsUnsafeShapes(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		mutate  func(*Manifest)
		options func() ValidationOptions
	}{
		{name: "schema version", want: "schema_version", mutate: func(m *Manifest) { m.SchemaVersion = 99 }},
		{name: "repo identifier", want: "repository identifier", mutate: func(m *Manifest) { m.Repo = "../repo" }},
		{name: "object repo mismatch", want: "must equal", mutate: func(m *Manifest) { m.Objects[0].Repo = "other" }},
		{name: "unknown ownership", want: "unknown ownership", mutate: func(m *Manifest) { m.Objects[0].OwnershipClass = "mystery" }},
		{name: "unknown source disposition", want: "unknown source disposition", mutate: func(m *Manifest) { m.Objects[0].SourceDisposition = "mystery" }},
		{name: "unknown projection", want: "unknown projection", mutate: func(m *Manifest) { m.Objects[0].ProjectionMode = "mystery" }},
		{name: "unknown lifecycle", want: "unknown lifecycle", mutate: func(m *Manifest) { m.Objects[0].Lifecycle = "mystery" }},
		{name: "absolute source", want: "repository-relative", mutate: func(m *Manifest) { m.Objects[0].Source = "/tmp/source" }},
		{name: "escaping source", want: "parent segments", mutate: func(m *Manifest) { m.Objects[0].Source = "../source" }},
		{name: "absolute destination", want: "repository-relative", mutate: func(m *Manifest) { m.Objects[0].Destination = "/tmp/destination" }},
		{name: "unnormalized destination", want: "normalized", mutate: func(m *Manifest) { m.Objects[0].Destination = ".codex/../config.toml" }},
		{name: "bad source hash", want: "lowercase sha256", mutate: func(m *Manifest) { m.Objects[0].SourceHash = "SHA256:nope" }},
		{name: "bad projected hash", want: "materialized content", mutate: func(m *Manifest) { m.Objects[0].ProjectedHash = "sha256:nope" }},
		{name: "world writable mode", want: "world-writable", mutate: func(m *Manifest) { m.Objects[0].Mode = "0666" }},
		{name: "special mode", want: "four-digit octal", mutate: func(m *Manifest) { m.Objects[0].Mode = "4755" }},
		{name: "copy generator", want: "only valid for render", mutate: func(m *Manifest) { m.Objects[0].Generator = "generator" }},
		{name: "render without generator", want: "deterministic generator", mutate: func(m *Manifest) { m.Objects[0].ProjectionMode = ProjectionRender }},
		{name: "active none", want: "active objects", mutate: func(m *Manifest) {
			m.Objects[0].ProjectionMode = ProjectionNone
			m.Objects[0].Mode = ""
			m.Objects[0].ProjectedHash = ""
		}},
		{name: "external owner copy", want: "external-owner sources", mutate: func(m *Manifest) {
			m.Objects[0].OwnershipClass = OwnershipExternalOwner
			m.Objects[0].SourceDisposition = SourceExternalOwner
		}},
		{name: "ownership disposition combination", want: "incompatible", mutate: func(m *Manifest) { m.Objects[0].SourceDisposition = SourceSharedHarness }},
		{name: "unregistered verifier", want: "not present", mutate: func(m *Manifest) { m.Objects[0].VerifierIDs = []string{"missing-check"} }},
		{name: "duplicate verifier", want: "duplicates a verifier", mutate: func(m *Manifest) { m.Objects[0].VerifierIDs = []string{"hash-check", "hash-check"} }},
		{name: "empty verifier registry", want: "not present", mutate: func(*Manifest) {}, options: func() ValidationOptions { return ValidationOptions{} }},
		{name: "duplicate object id", want: "duplicates objects", mutate: func(m *Manifest) {
			clone := m.Objects[0]
			clone.Destination = ".codex/other.toml"
			m.Objects = append(m.Objects, clone)
			m.CoverageInputs = append(m.CoverageInputs, CoverageInput{ID: "found-other", Path: clone.Destination})
		}},
		{name: "destination collision", want: "collides", mutate: func(m *Manifest) {
			clone := m.Objects[0]
			clone.ID = "other"
			m.Objects = append(m.Objects, clone)
		}},
		{name: "destination hierarchy overlap", want: "overlaps", mutate: func(m *Manifest) {
			clone := m.Objects[0]
			clone.ID = "other"
			clone.Destination = ".codex/config.toml/child"
			m.Objects = append(m.Objects, clone)
			m.CoverageInputs = append(m.CoverageInputs, CoverageInput{ID: "found-other", Path: clone.Destination})
		}},
		{name: "object omission id overlap", want: "also declared", mutate: func(m *Manifest) { m.Omissions[0].ID = m.Objects[0].ID }},
		{name: "object omission path overlap", want: "matches objects", mutate: func(m *Manifest) {
			m.Omissions[0].Kind = OmissionExact
			m.Omissions[0].Path = m.Objects[0].Destination
		}},
		{name: "omission overlap", want: "overlaps omissions", mutate: func(m *Manifest) {
			m.Omissions = append(m.Omissions, Omission{ID: "runtime-json", Path: ".codex/runtime/*", Kind: OmissionGlob, OwnershipClass: OwnershipExternalOwner, Reason: "bounded runtime files", VerifierIDs: []string{"runtime-omission-check"}, Lifecycle: LifecycleActive})
		}},
		{name: "invalid recursive glob", want: "terminal segment", mutate: func(m *Manifest) { m.Omissions[0].Path = ".codex/**/runtime" }},
		{name: "uncovered input", want: "not declared", mutate: func(m *Manifest) {
			m.CoverageInputs = append(m.CoverageInputs, CoverageInput{ID: "unknown", Path: ".codex/unknown"})
		}},
		{name: "duplicate coverage path", want: "duplicates coverage_inputs", mutate: func(m *Manifest) {
			m.CoverageInputs = append(m.CoverageInputs, CoverageInput{ID: "duplicate", Path: m.CoverageInputs[0].Path})
		}},
		{name: "stale omission", want: "does not match", mutate: func(m *Manifest) { m.CoverageInputs = m.CoverageInputs[:1] }},
		{name: "missing coverage", want: "must declare discovered", mutate: func(m *Manifest) { m.CoverageInputs = nil }},
		{name: "unowned replace", want: "replace-owned requires", mutate: func(m *Manifest) {
			m.Objects[0].OwnershipClass = OwnershipExternalOwner
			m.Objects[0].SourceDisposition = SourceExternalOwner
			m.Objects[0].ProjectionMode = ProjectionReference
			m.Objects[0].Mode = ""
			m.Objects[0].ProjectedHash = ""
			m.Objects[0].ConflictPolicy = ConflictReplaceOwned
		}},
		{name: "unowned tombstone", want: "deletion requires", mutate: func(m *Manifest) {
			m.Objects[0].OwnershipClass = OwnershipExternalOwner
			m.Objects[0].SourceDisposition = SourceExternalOwner
			m.Objects[0].ProjectionMode = ProjectionNone
			m.Objects[0].Mode = ""
			m.Objects[0].ProjectedHash = ""
			m.Objects[0].Lifecycle = LifecycleRetired
			m.Objects[0].RemovalPolicy = RemovalTombstoneOnly
			m.Objects[0].Tombstone = true
		}},
		{name: "implicit tombstone removal", want: "explicit tombstone", mutate: func(m *Manifest) { m.Objects[0].RemovalPolicy = RemovalTombstoneOnly }},
		{name: "bad tombstone shape", want: "retired and non-materialized", mutate: func(m *Manifest) {
			m.Objects[0].RemovalPolicy = RemovalTombstoneOnly
			m.Objects[0].Tombstone = true
		}},
		{name: "empty inputs", want: "ordered exact-ref", mutate: func(m *Manifest) { m.Objects[0].Inputs = nil }},
		{name: "bad source repository", want: "namespace/repository", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Repository = "repo" }},
		{name: "bad remote", want: "repository remote", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Remote = "file:///tmp/repo" }},
		{name: "remote repository mismatch", want: "must match", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Remote = "https://github.com/hairglasses-studio/other.git" }},
		{name: "https userinfo token", want: "must not embed user information", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "https://token@" + "github.com/hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "https userinfo password", want: "must not embed user information", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "https://token:password@" + "github.com/hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "ssh password", want: "must not embed a password", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "ssh://git:password@" + "github.com/hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "ssh non-git user", want: "user must be git", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "ssh://ubuntu@" + "github.com/hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "ssh missing user", want: "user must be git", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "ssh://github.com/hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "scp non-git user", want: "user must be git", mutate: func(m *Manifest) {
			m.Objects[0].Inputs[0].Remote = "ubuntu@" + "github.com:hairglasses-studio/codex-harness-staging.git"
		}},
		{name: "bad ref", want: "normalized explicit", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Ref = "refs/../main" }},
		{name: "bad commit", want: "exact commit", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Commit = "main" }},
		{name: "escaping input path", want: "parent segments", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Path = "../secret" }},
		{name: "input source mismatch", want: "inputs[0].path", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].Path = "codex/other.toml" }},
		{name: "input hash mismatch", want: "inputs[0].blob_hash", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].BlobHash = testProjectedHash }},
		{name: "non-hermetic without reason", want: "must explain", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].NonHermetic = true }},
		{name: "reason without non-hermetic", want: "requires non_hermetic", mutate: func(m *Manifest) { m.Objects[0].Inputs[0].NonHermeticReason = "ambient tool version" }},
		{name: "duplicate ordered input", want: "duplicates objects[0].inputs[0]", mutate: func(m *Manifest) { m.Objects[0].Inputs = append(m.Objects[0].Inputs, m.Objects[0].Inputs[0]) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			options := validOptions()
			if test.options != nil {
				options = test.options()
			}
			err := Validate(manifest, options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("Validate() error type = %T, want *ValidationError", err)
			}
		})
	}
}

func TestRelativeSymlinkContainment(t *testing.T) {
	manifest := validManifest()
	object := &manifest.Objects[0]
	object.Source = ".agents/skills/tool/SKILL.md"
	object.Inputs[0].Path = object.Source
	object.Destination = ".codex/skills/tool/SKILL.md"
	object.ProjectionMode = ProjectionRelativeSymlink
	object.Mode = ""
	object.SymlinkTarget = "../../../.agents/skills/tool/SKILL.md"
	manifest.CoverageInputs[0].Path = object.Destination
	if err := Validate(manifest, validOptions()); err != nil {
		t.Fatalf("contained relative symlink rejected: %v", err)
	}

	object.SymlinkTarget = "../../../../../../etc/passwd"
	if err := Validate(manifest, validOptions()); err == nil || !strings.Contains(err.Error(), "escapes the repository") {
		t.Fatalf("escaping relative symlink error = %v", err)
	}

	object.SymlinkTarget = "/etc/passwd"
	if err := Validate(manifest, validOptions()); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("absolute relative symlink error = %v", err)
	}
}

func TestValidCredentialFreeRemoteForms(t *testing.T) {
	remotes := []string{
		"https://github.com/hairglasses-studio/codex-harness-staging.git",
		"ssh://git@" + "github.com/hairglasses-studio/codex-harness-staging.git",
		"git@" + "github.com:hairglasses-studio/codex-harness-staging.git",
	}
	for _, remote := range remotes {
		t.Run(remote, func(t *testing.T) {
			manifest := validManifest()
			manifest.Objects[0].Inputs[0].Remote = remote
			if err := Validate(manifest, validOptions()); err != nil {
				t.Fatalf("credential-free remote %q rejected: %v", remote, err)
			}
		})
	}
}

func TestValidClosedCombinationsAndOwnedLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Object)
	}{
		{name: "shared harness copy", mutate: func(object *Object) {
			object.OwnershipClass = OwnershipSharedProviderNeutral
			object.SourceDisposition = SourceSharedHarness
		}},
		{name: "generated consumer render", mutate: func(object *Object) {
			object.OwnershipClass = OwnershipRepoLocalProjection
			object.SourceDisposition = SourceGeneratedFromConsumer
			object.ProjectionMode = ProjectionRender
			object.Generator = "render-config"
			object.GeneratorCommit = testCommit
			object.Transform = "v1"
		}},
		{name: "claude external reference", mutate: func(object *Object) {
			object.OwnershipClass = OwnershipClaudeHarnessSpecific
			object.SourceDisposition = SourceExternalOwner
			object.ProjectionMode = ProjectionReference
			object.Mode = ""
			object.ProjectedHash = ""
		}},
		{name: "external reference", mutate: func(object *Object) {
			object.OwnershipClass = OwnershipExternalOwner
			object.SourceDisposition = SourceExternalOwner
			object.ProjectionMode = ProjectionReference
			object.Mode = ""
			object.ProjectedHash = ""
		}},
		{name: "managed block", mutate: func(object *Object) {
			object.ProjectionMode = ProjectionManagedBlock
			object.Generator = "render-block"
			object.GeneratorCommit = testCommit
			object.Transform = "v1"
		}},
		{name: "owned replacement", mutate: func(object *Object) {
			object.ConflictPolicy = ConflictReplaceOwned
		}},
		{name: "owned tombstone", mutate: func(object *Object) {
			object.ProjectionMode = ProjectionNone
			object.Mode = ""
			object.ProjectedHash = ""
			object.Lifecycle = LifecycleRetired
			object.RemovalPolicy = RemovalTombstoneOnly
			object.Tombstone = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest.Objects[0])
			if err := Validate(manifest, validOptions()); err != nil {
				t.Fatalf("valid combination rejected: %v", err)
			}
		})
	}
}

func TestRenderPreservesOrderedInputs(t *testing.T) {
	manifest := validManifest()
	object := &manifest.Objects[0]
	object.ProjectionMode = ProjectionRender
	object.Generator = "render-config"
	object.GeneratorCommit = testCommit
	object.Transform = "v1"
	object.Inputs = append(object.Inputs, SourceInput{
		Repository:        "hairglasses-studio/shared-inputs",
		Remote:            "git@" + "github.com:hairglasses-studio/shared-inputs.git",
		Ref:               "refs/tags/v1.0.0",
		Commit:            strings.Repeat("d", 40),
		Path:              "templates/fragment.toml",
		BlobHash:          testProjectedHash,
		NonHermetic:       true,
		NonHermeticReason: "consumer-local capability selection",
	})
	data, err := Marshal(manifest, validOptions())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	first := bytes.Index(data, []byte(`"path": "codex/config.toml"`))
	second := bytes.Index(data, []byte(`"path": "templates/fragment.toml"`))
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("ordered inputs were reordered:\n%s", data)
	}
}

func TestValidateCoverageInputsUsesCallerInventory(t *testing.T) {
	manifest := validManifest()
	inputs := []CoverageInput{{ID: "fresh-config", Path: ".codex/config.toml"}}
	report, err := ValidateCoverageInputs(manifest, inputs)
	if err == nil || !strings.Contains(err.Error(), "stale_omission") && !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ValidateCoverageInputs() error = %v", err)
	}
	if report.Projected != 1 || report.TotalInputs != 1 {
		t.Fatalf("unexpected partial report: %+v", report)
	}
	if len(manifest.CoverageInputs) != 2 {
		t.Fatal("ValidateCoverageInputs mutated manifest")
	}
}

func TestValidateCoverageRejectsDuplicateEmbeddedInputs(t *testing.T) {
	manifest := validManifest()
	manifest.CoverageInputs = append(manifest.CoverageInputs, CoverageInput{ID: "another", Path: manifest.CoverageInputs[0].Path})
	if _, err := ValidateCoverage(manifest); err == nil || !strings.Contains(err.Error(), "duplicates coverage_inputs") {
		t.Fatalf("ValidateCoverage() error = %v", err)
	}
}
