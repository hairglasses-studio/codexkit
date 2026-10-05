package launchercontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var evidenceNow = time.Date(2026, 8, 11, 22, 0, 0, 0, time.UTC)

func validEvidenceOptions() EvidenceOptions {
	authority, err := AuthoritySHA256(validTrust())
	if err != nil {
		panic(err)
	}
	return EvidenceOptions{Now: evidenceNow, MaxAge: 2 * time.Hour, ExpectedAuthoritySHA256: authority}
}

func validResolution(t *testing.T) Resolution {
	t.Helper()
	candidate := "/opt/alice/Harness With Spaces"
	resolution, err := Resolve(
		Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}},
		validObservations(candidate),
		validTrust(),
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return resolution
}

func validEvidence(t *testing.T) Evidence {
	t.Helper()
	evidence, err := NewEvidence(validResolution(t), EvidenceRecord{
		ObservedAt:                  evidenceNow.Add(-time.Hour).In(time.FixedZone("PDT", -7*60*60)),
		SideEffectGuardBeforeSHA256: testDigest,
		SideEffectGuardAfterSHA256:  testDigest,
		ProviderStarted:             false,
		NormalLaunchExecuted:        false,
	}, validEvidenceOptions())
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	return evidence
}

func cloneEvidence(evidence Evidence) Evidence {
	evidence.Markers = append([]EvidenceMarker(nil), evidence.Markers...)
	return evidence
}

func TestEvidenceRoundTripIsCanonicalAndPathSanitized(t *testing.T) {
	t.Parallel()
	evidence := validEvidence(t)
	original := cloneEvidence(evidence)
	// Reverse marker order to prove canonicalization without caller mutation.
	evidence.Markers[0], evidence.Markers[1] = evidence.Markers[1], evidence.Markers[0]

	first, err := MarshalEvidence(evidence, validEvidenceOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalEvidence(evidence, validEvidenceOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatalf("MarshalEvidence() is not deterministic with trailing newline:\n%s", first)
	}
	for _, private := range []string{"/opt/alice", "Harness With Spaces", "/srv/git"} {
		if bytes.Contains(first, []byte(private)) {
			t.Fatalf("canonical evidence leaked private path %q:\n%s", private, first)
		}
	}
	if !bytes.Contains(first, []byte(`"observed_at": "2026-08-11T21:00:00Z"`)) {
		t.Fatalf("canonical evidence did not normalize time:\n%s", first)
	}
	if evidence.Markers[0].ID == original.Markers[0].ID {
		t.Fatal("test setup did not reverse markers")
	}
	if evidence.ObservedAt.Location() == time.UTC {
		t.Fatal("test setup lost non-UTC caller time")
	}

	loaded, err := LoadEvidence(first, validEvidenceOptions())
	if err != nil {
		t.Fatalf("LoadEvidence(MarshalEvidence()) error = %v", err)
	}
	if loaded.CandidatePathID != loaded.GitTopLevelPathID || loaded.Status != StatusActivationEligible || !loaded.ActivationEligible {
		t.Fatalf("loaded evidence = %#v", loaded)
	}
	if loaded.Markers[0].ID != "codexkit-module" {
		t.Fatalf("loaded markers are not canonical: %#v", loaded.Markers)
	}
}

func TestNewEvidenceRejectsSideEffectsAndInvalidResolution(t *testing.T) {
	t.Parallel()
	validRecord := EvidenceRecord{
		ObservedAt:                  evidenceNow,
		SideEffectGuardBeforeSHA256: testDigest,
		SideEffectGuardAfterSHA256:  testDigest,
	}
	tests := []struct {
		name       string
		mutateRes  func(*Resolution)
		mutateMeta func(*EvidenceRecord)
		want       string
	}{
		{name: "provider started", mutateMeta: func(record *EvidenceRecord) { record.ProviderStarted = true }, want: "provider_started"},
		{name: "normal launch", mutateMeta: func(record *EvidenceRecord) { record.NormalLaunchExecuted = true }, want: "normal_launch_executed"},
		{name: "guard changed", mutateMeta: func(record *EvidenceRecord) { record.SideEffectGuardAfterSHA256 = testDigest2 }, want: "guard hashes differ"},
		{name: "guard malformed", mutateMeta: func(record *EvidenceRecord) { record.SideEffectGuardBeforeSHA256 = "bad" }, want: "guard hashes are invalid"},
		{name: "ineligible resolution", mutateRes: func(resolution *Resolution) { resolution.ActivationEligible = false }, want: "not activation eligible"},
		{name: "dirty resolution", mutateRes: func(resolution *Resolution) { resolution.CheckoutClean = false }, want: "must be clean"},
		{name: "wrong resolution mode", mutateRes: func(resolution *Resolution) { resolution.Mode = ModePrimaryCheckout }, want: "mode"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolution := validResolution(t)
			record := validRecord
			if test.mutateRes != nil {
				test.mutateRes(&resolution)
			}
			if test.mutateMeta != nil {
				test.mutateMeta(&record)
			}
			_, err := NewEvidence(resolution, record, validEvidenceOptions())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNewEvidenceRebindsTrustedAuthority(t *testing.T) {
	t.Parallel()
	resolution := validResolution(t)
	record := EvidenceRecord{
		ObservedAt:                  evidenceNow,
		SideEffectGuardBeforeSHA256: testDigest,
		SideEffectGuardAfterSHA256:  testDigest,
	}
	options := validEvidenceOptions()
	options.ExpectedAuthoritySHA256 = testDigest2
	if _, err := NewEvidence(resolution, record, options); !errors.Is(err, ErrInvalidEvidence) || !strings.Contains(err.Error(), "trusted authority") {
		t.Fatalf("NewEvidence(wrong authority) error = %v", err)
	}
}

func TestValidateEvidenceRejectsNegativeMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Evidence, *EvidenceOptions)
		want   string
	}{
		{name: "schema", mutate: func(e *Evidence, _ *EvidenceOptions) { e.SchemaVersion = 2 }, want: "schema_version"},
		{name: "unknown source", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Source = "ambient" }, want: "unknown source"},
		{name: "unknown mode", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Mode = "detached" }, want: "unknown mode"},
		{name: "unknown status", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Status = "maybe" }, want: "unknown status"},
		{name: "zero observed", mutate: func(e *Evidence, _ *EvidenceOptions) { e.ObservedAt = time.Time{} }, want: "observed_at is required"},
		{name: "future", mutate: func(e *Evidence, o *EvidenceOptions) { e.ObservedAt = o.Now.Add(time.Nanosecond) }, want: "future"},
		{name: "stale", mutate: func(e *Evidence, o *EvidenceOptions) { e.ObservedAt = o.Now.Add(-o.MaxAge - time.Nanosecond) }, want: "stale"},
		{name: "missing trusted now", mutate: func(_ *Evidence, o *EvidenceOptions) { o.Now = time.Time{} }, want: "validation time"},
		{name: "negative max age", mutate: func(_ *Evidence, o *EvidenceOptions) { o.MaxAge = -time.Second }, want: "max age"},
		{name: "missing trusted authority", mutate: func(_ *Evidence, o *EvidenceOptions) { o.ExpectedAuthoritySHA256 = "" }, want: "expected authority"},
		{name: "wrong trusted authority", mutate: func(_ *Evidence, o *EvidenceOptions) { o.ExpectedAuthoritySHA256 = testDigest2 }, want: "trusted authority"},
		{name: "raw candidate path", mutate: func(e *Evidence, _ *EvidenceOptions) { e.CandidatePathID = "/opt/alice/harness" }, want: "sanitized path ID"},
		{name: "zero path ID", mutate: func(e *Evidence, _ *EvidenceOptions) { e.GitCommonDirPathID = "path-sha256:" + strings.Repeat("0", 64) }, want: "sanitized path ID"},
		{name: "top mismatch", mutate: func(e *Evidence, _ *EvidenceOptions) { e.GitTopLevelPathID = e.GitCommonDirPathID }, want: "top-level path ID"},
		{name: "common equals candidate", mutate: func(e *Evidence, _ *EvidenceOptions) { e.GitCommonDirPathID = e.CandidatePathID }, want: "must differ"},
		{name: "codexkit equals candidate", mutate: func(e *Evidence, _ *EvidenceOptions) { e.CodexkitRootPathID = e.CandidatePathID }, want: "must differ"},
		{name: "wrong repository", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Repository = "hairglasses-studio/codexkit" }, want: "central harness"},
		{name: "wrong normalized remote", mutate: func(e *Evidence, _ *EvidenceOptions) { e.NormalizedRemote = "github.com/hairglasses-studio/codexkit" }, want: "central harness"},
		{name: "bad commit", mutate: func(e *Evidence, _ *EvidenceOptions) { e.TestedSHA = "short" }, want: "40-hex"},
		{name: "different valid commit", mutate: func(e *Evidence, _ *EvidenceOptions) { e.TestedSHA = "abcdef1234567890abcdef1234567890abcdef12" }, want: "does not bind"},
		{name: "forged authority", mutate: func(e *Evidence, _ *EvidenceOptions) { e.AuthoritySHA256 = testDigest2 }, want: "trusted authority"},
		{name: "dirty", mutate: func(e *Evidence, _ *EvidenceOptions) { e.CheckoutClean = false }, want: "must be clean"},
		{name: "no markers", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers = nil }, want: "markers must contain"},
		{name: "bad marker ID", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[0].ID = "Bad ID" }, want: ".id is invalid"},
		{name: "raw marker path", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[0].PathID = "/opt/alice/AGENTS.md" }, want: ".path_id is invalid"},
		{name: "bad marker authority path", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[0].AuthorityMarkerPathID = "marker-path-sha256:bad" }, want: ".authority_marker_path_id is invalid"},
		{name: "fabricated marker authority path", mutate: func(e *Evidence, _ *EvidenceOptions) {
			e.Markers[0].AuthorityMarkerPathID = "marker-path-sha256:" + strings.Repeat("1", 64)
		}, want: "does not bind"},
		{name: "bad marker digest", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[0].SHA256 = "bad" }, want: ".sha256 is invalid"},
		{name: "fabricated marker digest", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[0].SHA256 = testDigest }, want: "does not bind"},
		{name: "duplicate marker ID", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[1].ID = e.Markers[0].ID }, want: "duplicate marker ID"},
		{name: "duplicate marker path", mutate: func(e *Evidence, _ *EvidenceOptions) { e.Markers[1].PathID = e.Markers[0].PathID }, want: "duplicate marker path ID"},
		{name: "bad before guard", mutate: func(e *Evidence, _ *EvidenceOptions) { e.SideEffectGuardBeforeSHA256 = "bad" }, want: "guard hashes are invalid"},
		{name: "changed guard", mutate: func(e *Evidence, _ *EvidenceOptions) { e.SideEffectGuardAfterSHA256 = testDigest2 }, want: "guard hashes differ"},
		{name: "provider started", mutate: func(e *Evidence, _ *EvidenceOptions) { e.ProviderStarted = true }, want: "provider_started"},
		{name: "normal launch executed", mutate: func(e *Evidence, _ *EvidenceOptions) { e.NormalLaunchExecuted = true }, want: "normal_launch_executed"},
		{name: "not eligible", mutate: func(e *Evidence, _ *EvidenceOptions) { e.ActivationEligible = false }, want: "activation_eligible"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evidence := cloneEvidence(validEvidence(t))
			options := validEvidenceOptions()
			test.mutate(&evidence, &options)
			err := ValidateEvidence(evidence, options)
			if err == nil || !errors.Is(err, ErrInvalidEvidence) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateEvidence() error = %v, want ErrInvalidEvidence containing %q", err, test.want)
			}
		})
	}
}

func TestLoadEvidenceRejectsJSONMatrix(t *testing.T) {
	t.Parallel()
	valid, err := MarshalEvidence(validEvidence(t), validEvidenceOptions())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(valid, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "provider_started")
	missingFalseField, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "empty", data: nil, want: "document size"},
		{name: "unknown field", data: bytes.Replace(valid, []byte("\n}"), []byte(",\n  \"unknown\": true\n}"), 1), want: "unknown field"},
		{name: "duplicate top-level key", data: bytes.Replace(valid, []byte("{\n"), []byte("{\n  \"schema_version\": 1,\n"), 1), want: "duplicate object key"},
		{name: "duplicate nested key", data: bytes.Replace(valid, []byte(`"id": "codexkit-module",`), []byte(`"id": "codexkit-module", "id": "again",`), 1), want: "duplicate object key"},
		{name: "trailing value", data: append(append([]byte(nil), valid...), []byte(` {}`)...), want: "trailing JSON value"},
		{name: "missing false field", data: missingFalseField, want: "provider_started"},
		{name: "null", data: []byte("null"), want: "missing required field"},
		{name: "oversize", data: bytes.Repeat([]byte("x"), MaxEvidenceDocumentBytes+1), want: "document size"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadEvidence(test.data, validEvidenceOptions())
			if err == nil || !errors.Is(err, ErrInvalidEvidence) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadEvidence() error = %v, want ErrInvalidEvidence containing %q", err, test.want)
			}
		})
	}
}

func TestEvidenceDefaultMaxAgeBoundary(t *testing.T) {
	t.Parallel()
	evidence := validEvidence(t)
	evidence.ObservedAt = evidenceNow.Add(-DefaultEvidenceMaxAge)
	authority, err := AuthoritySHA256(validTrust())
	if err != nil {
		t.Fatal(err)
	}
	options := EvidenceOptions{Now: evidenceNow, ExpectedAuthoritySHA256: authority}
	if err := ValidateEvidence(evidence, options); err != nil {
		t.Fatalf("ValidateEvidence(boundary) error = %v", err)
	}
	evidence.ObservedAt = evidence.ObservedAt.Add(-time.Nanosecond)
	if err := ValidateEvidence(evidence, options); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("ValidateEvidence(stale) error = %v", err)
	}
}

func FuzzLoadEvidenceNeverPanics(f *testing.F) {
	candidate := "/opt/alice/Harness With Spaces"
	resolution, resolveErr := Resolve(
		Inputs{CodexHarnessDir: InputValue{Present: true, Value: candidate}},
		validObservations(candidate),
		validTrust(),
	)
	evidence, evidenceErr := NewEvidence(resolution, EvidenceRecord{
		ObservedAt:                  evidenceNow.Add(-time.Hour),
		SideEffectGuardBeforeSHA256: testDigest,
		SideEffectGuardAfterSHA256:  testDigest,
	}, validEvidenceOptions())
	if resolveErr == nil && evidenceErr == nil {
		if seed, err := MarshalEvidence(evidence, validEvidenceOptions()); err == nil {
			f.Add(seed)
		}
	}
	f.Add([]byte(`{"schema_version":1}`))
	f.Add([]byte("null"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = LoadEvidence(data, validEvidenceOptions())
	})
}
