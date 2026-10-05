// Portable contract tests for the journal, receipt, and ownership-ledger
// documents. These run on every platform because the contract half of this
// package performs no I/O. They cover the fail-closed decode path, the
// tombstone-only ledger semantics, and the receipt sanitization invariants.
//
// They deliberately do NOT cover apply behavior: no filesystem is touched
// here, so nothing in this file says anything about what actually lands on
// disk. See apply_linux_test.go, whose header states what the engine fixtures
// can and cannot prove.
package harnesslifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
)

const (
	contractOwner      = "codex-harness-staging"
	contractRepository = "hairglasses-studio/demo"
	contractJournalID  = "0123456789abcdef0123456789abcdef"
	contractQuarantine = ".codex/.codex-harness-quarantine-00112233445566778899aabbccddeeff"
)

var contractTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func contractDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contractLedger(t testing.TB) Ledger {
	t.Helper()
	ledger, err := NewLedger(contractRepository, contractOwner, contractTime)
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	return ledger
}

func contractInstalled(hash string) InstalledState {
	return InstalledState{
		Type:        harnessplan.FileRegular,
		Mode:        "0644",
		Hash:        hash,
		PlanSHA256:  contractDigest("plan"),
		JournalID:   contractJournalID,
		InstalledAt: contractTime,
	}
}

func TestLedgerRowsSurviveRemovalAndReinstallation(t *testing.T) {
	ledger := contractLedger(t)

	installed, err := ledger.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v1")))
	if err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	if !installed.Owns(".codex/config.toml") {
		t.Fatal("install did not record ownership")
	}

	tombstoned, err := installed.RecordTombstone(".codex/config.toml", "copy-config", Tombstone{
		Reason:         TombstoneUninstall,
		PlanSHA256:     contractDigest("plan"),
		JournalID:      contractJournalID,
		RemovedHash:    contractDigest("v1"),
		QuarantinePath: contractQuarantine,
		RecordedAt:     contractTime,
	})
	if err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}
	if len(tombstoned.Entries) != 1 {
		t.Fatalf("removal dropped the ledger row: %+v", tombstoned.Entries)
	}
	entry := tombstoned.Entries[0]
	if entry.Status != LedgerTombstoned || entry.Installed != nil || len(entry.Tombstones) != 1 {
		t.Fatalf("tombstoned row = %+v", entry)
	}
	if tombstoned.Owns(".codex/config.toml") {
		t.Fatal("a tombstoned destination is still reported as owned")
	}

	revived, err := tombstoned.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v2")))
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if len(revived.Entries) != 1 || len(revived.Entries[0].Tombstones) != 1 {
		t.Fatalf("reinstall discarded removal history: %+v", revived.Entries)
	}
	if !revived.Owns(".codex/config.toml") {
		t.Fatal("reinstall did not revive ownership")
	}
}

func TestLedgerRefusesRemovalOfAnUnownedOrTombstonedDestination(t *testing.T) {
	ledger := contractLedger(t)
	tombstone := Tombstone{
		Reason:     TombstoneUninstall,
		PlanSHA256: contractDigest("plan"),
		JournalID:  contractJournalID,
		RecordedAt: contractTime,
	}

	if _, err := ledger.RecordTombstone(".codex/never-owned", "copy-config", tombstone); !errors.Is(err, ErrUnowned) {
		t.Fatalf("removal of an unrecorded destination error = %v, want ErrUnowned", err)
	}

	installed, err := ledger.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v1")))
	if err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	removed, err := installed.RecordTombstone(".codex/config.toml", "copy-config", tombstone)
	if err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}
	if _, err := removed.RecordTombstone(".codex/config.toml", "copy-config", tombstone); !errors.Is(err, ErrUnowned) {
		t.Fatalf("second removal error = %v, want ErrUnowned", err)
	}
}

func TestLedgerRefusesAForeignObjectClaimingAnOwnedDestination(t *testing.T) {
	ledger := contractLedger(t)
	installed, err := ledger.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v1")))
	if err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	if _, err := installed.RecordInstall(".codex/config.toml", "other-object", contractInstalled(contractDigest("v2"))); !errors.Is(err, ErrUnowned) {
		t.Fatalf("cross-object install error = %v, want ErrUnowned", err)
	}
	decision := installed.Authorize(IntentRemoveTombstone, ".codex/config.toml", "other-object")
	if decision.Authorized || decision.Reason != ReasonUnowned {
		t.Fatalf("cross-object removal decision = %+v", decision)
	}
}

func TestAuthorizeClassifiesEveryOwnershipCase(t *testing.T) {
	ledger := contractLedger(t)
	owned, err := ledger.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v1")))
	if err != nil {
		t.Fatalf("RecordInstall: %v", err)
	}
	tombstoned, err := owned.RecordTombstone(".codex/config.toml", "copy-config", Tombstone{
		Reason: TombstoneUninstall, PlanSHA256: contractDigest("plan"),
		JournalID: contractJournalID, RecordedAt: contractTime,
	})
	if err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}

	tests := []struct {
		name       string
		ledger     Ledger
		kind       IntentKind
		authorized bool
		reason     Reason
	}{
		{name: "create on an empty ledger", ledger: ledger, kind: IntentCreate, authorized: true},
		{name: "create over an owned row", ledger: owned, kind: IntentCreate, reason: ReasonCollision},
		{name: "create over a tombstoned row", ledger: tombstoned, kind: IntentCreate, authorized: true},
		{name: "replace an owned row", ledger: owned, kind: IntentReplaceOwned, authorized: true},
		{name: "replace an unrecorded row", ledger: ledger, kind: IntentReplaceOwned, reason: ReasonUnowned},
		{name: "replace a tombstoned row", ledger: tombstoned, kind: IntentReplaceOwned, reason: ReasonTombstoned},
		{name: "remove an owned row", ledger: owned, kind: IntentRemoveTombstone, authorized: true},
		{name: "remove an unrecorded row", ledger: ledger, kind: IntentRemoveTombstone, reason: ReasonUnowned},
		{name: "remove a tombstoned row", ledger: tombstoned, kind: IntentRemoveTombstone, reason: ReasonTombstoned},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := test.ledger.Authorize(test.kind, ".codex/config.toml", "copy-config")
			if decision.Authorized != test.authorized || decision.Reason != test.reason {
				t.Fatalf("Authorize() = %+v, want authorized=%v reason=%q",
					decision, test.authorized, test.reason)
			}
		})
	}
}

func validContractJournal() Journal {
	hash := contractDigest("payload")
	return Journal{
		SchemaVersion:    SchemaVersion,
		JournalID:        contractJournalID,
		PlanSHA256:       contractDigest("plan"),
		ManifestDigest:   contractDigest("manifest"),
		InventoryDigest:  contractDigest("inventory"),
		TargetRepository: contractRepository,
		MarkerOwner:      contractOwner,
		OpenedAt:         contractTime,
		Authorized:       true,
		Intents: []Intent{{
			Sequence:    0,
			OperationID: "op-copy-config",
			ObjectID:    "copy-config",
			Kind:        IntentCreate,
			Destination: ".codex/config.toml",
			Observed:    ObservedState{Type: harnessplan.FileAbsent},
			Expected:    harnessplan.FileState{Type: harnessplan.FileAbsent},
			After:       harnessplan.FileState{Type: harnessplan.FileRegular, Mode: "0644", Hash: hash},
			PayloadHash: hash,
			PayloadSize: 7,
		}},
	}
}

func TestJournalRoundTripsAndRejectsContractViolations(t *testing.T) {
	encoded, err := MarshalJournal(validContractJournal())
	if err != nil {
		t.Fatalf("MarshalJournal: %v", err)
	}
	if _, err := LoadJournal(encoded); err != nil {
		t.Fatalf("LoadJournal(valid): %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Journal)
		message string
	}{
		{
			name:    "unauthorized journal recording intents",
			mutate:  func(j *Journal) { j.Authorized = false },
			message: "an unauthorized journal must record no intents",
		},
		{
			name:    "sparse sequence",
			mutate:  func(j *Journal) { j.Intents[0].Sequence = 3 },
			message: "must be dense, zero-based",
		},
		{
			name:    "payload digest disagreeing with the after state",
			mutate:  func(j *Journal) { j.Intents[0].PayloadHash = contractDigest("other") },
			message: "must equal the after-state hash",
		},
		{
			name:    "opaque identifier replaced by a host path",
			mutate:  func(j *Journal) { j.JournalID = "/opt/operator/journal" },
			message: "must be 16 lowercase hex bytes",
		},
		{
			name:    "absolute destination",
			mutate:  func(j *Journal) { j.Intents[0].Destination = "/etc/passwd" },
			message: "must be a portable repository-relative path",
		},
		{
			name:    "escaping destination",
			mutate:  func(j *Journal) { j.Intents[0].Destination = "../outside" },
			message: "must not contain empty, dot, or parent segments",
		},
		{
			name: "create expecting a materialized before state",
			mutate: func(j *Journal) {
				j.Intents[0].Expected = harnessplan.FileState{
					Type: harnessplan.FileRegular, Mode: "0644", Hash: contractDigest("x"),
				}
			},
			message: "create must expect an absent destination",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal := validContractJournal()
			test.mutate(&journal)
			err := ValidateJournal(journal)
			if err == nil {
				t.Fatal("ValidateJournal accepted an invalid journal")
			}
			if !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("error kind = %v, want ErrInvalidJournal", err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestLoadRejectsMalformedDocuments(t *testing.T) {
	valid, err := MarshalJournal(validContractJournal())
	if err != nil {
		t.Fatalf("MarshalJournal: %v", err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "unknown field", data: []byte(`{"schema_version":1,"unexpected":true}`)},
		{name: "duplicate key", data: []byte(`{"schema_version":1,"schema_version":1}`)},
		{name: "trailing value", data: append(append([]byte{}, valid...), []byte("{}")...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadJournal(test.data); !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("LoadJournal error = %v, want ErrInvalidJournal", err)
			}
		})
	}
}

func validContractReceipt() Receipt {
	return Receipt{
		SchemaVersion:    SchemaVersion,
		JournalID:        contractJournalID,
		PlanSHA256:       contractDigest("plan"),
		ManifestDigest:   contractDigest("manifest"),
		InventoryDigest:  contractDigest("inventory"),
		TargetRepository: contractRepository,
		MarkerOwner:      contractOwner,
		Outcome:          OutcomeApplied,
		OpenedAt:         contractTime,
		ClosedAt:         contractTime.Add(time.Second),
		Results: []ReceiptResult{{
			Sequence:    0,
			OperationID: "op-copy-config",
			ObjectID:    "copy-config",
			Kind:        IntentCreate,
			Destination: ".codex/config.toml",
			Status:      StepApplied,
			AfterHash:   contractDigest("payload"),
		}},
	}
}

func TestReceiptRejectsUnsanitizedAndInconsistentValues(t *testing.T) {
	if _, err := MarshalReceipt(validContractReceipt()); err != nil {
		t.Fatalf("MarshalReceipt(valid): %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Receipt)
		message string
	}{
		{
			name:    "host-absolute destination",
			mutate:  func(r *Receipt) { r.Results[0].Destination = "/opt/operator/.codex/config.toml" },
			message: "must be a portable repository-relative path",
		},
		{
			name:    "retained path outside the quarantine namespace",
			mutate:  func(r *Receipt) { r.Results[0].Retained = ".codex/backup.bak" },
			message: "must name an object in the substrate quarantine namespace",
		},
		{
			name:    "content where a digest belongs",
			mutate:  func(r *Receipt) { r.Results[0].AfterHash = "managed = true" },
			message: "must be a lowercase sha256 digest",
		},
		{
			name:    "unknown reason code",
			mutate:  func(r *Receipt) { r.Results[0].Reason = Reason("permission denied opening /etc/shadow") },
			message: "contains an unknown reason code",
		},
		{
			name: "applied outcome contradicted by its results",
			mutate: func(r *Receipt) {
				r.Results[0].Status = StepFailed
				r.Results[0].Reason = ReasonSubstrateError
			},
			message: "applied requires every result to have succeeded",
		},
		{
			name: "refused outcome contradicted by a successful result",
			mutate: func(r *Receipt) {
				r.Outcome = OutcomeRefused
			},
			message: "refused requires that no result mutated the target",
		},
		{
			name:    "closed before opened",
			mutate:  func(r *Receipt) { r.ClosedAt = r.OpenedAt.Add(-time.Hour) },
			message: "must not precede opened_at",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := validContractReceipt()
			test.mutate(&receipt)
			err := ValidateReceipt(receipt)
			if err == nil {
				t.Fatal("ValidateReceipt accepted an invalid receipt")
			}
			if !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("error kind = %v, want ErrInvalidReceipt", err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestLedgerValidationRejectsInconsistentRows(t *testing.T) {
	base := func(t testing.TB) Ledger {
		t.Helper()
		ledger := contractLedger(t)
		installed, err := ledger.RecordInstall(".codex/config.toml", "copy-config", contractInstalled(contractDigest("v1")))
		if err != nil {
			t.Fatalf("RecordInstall: %v", err)
		}
		return installed
	}

	tests := []struct {
		name    string
		mutate  func(*Ledger)
		message string
	}{
		{
			name:    "owned row without an installed state",
			mutate:  func(l *Ledger) { l.Entries[0].Installed = nil },
			message: "an owned entry must record its installed state",
		},
		{
			name: "tombstoned row that still claims an installed state",
			mutate: func(l *Ledger) {
				l.Entries[0].Status = LedgerTombstoned
				l.Entries[0].Tombstones = []Tombstone{{
					Reason: TombstoneUninstall, PlanSHA256: contractDigest("plan"),
					JournalID: contractJournalID, RecordedAt: contractTime,
				}}
			},
			message: "a tombstoned entry must not record an installed state",
		},
		{
			name:    "row owned by a different marker owner",
			mutate:  func(l *Ledger) { l.Entries[0].Owner = "someone-else" },
			message: "must equal the ledger marker owner",
		},
		{
			name: "unsorted destinations",
			mutate: func(l *Ledger) {
				l.Entries = append(l.Entries, l.Entries[0])
			},
			message: "entries must be sorted by destination and unique",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ledger := base(t)
			test.mutate(&ledger)
			err := ValidateLedger(ledger)
			if err == nil {
				t.Fatal("ValidateLedger accepted an invalid ledger")
			}
			if !errors.Is(err, ErrInvalidLedger) {
				t.Fatalf("error kind = %v, want ErrInvalidLedger", err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want it to mention %q", err, test.message)
			}
		})
	}
}

func TestValidationFindingsAreDeterministic(t *testing.T) {
	journal := validContractJournal()
	journal.JournalID = ""
	journal.MarkerOwner = ""
	journal.Intents[0].Destination = "/absolute"

	first := ValidateJournal(journal)
	second := ValidateJournal(journal)
	if first == nil || second == nil {
		t.Fatal("expected findings")
	}
	if first.Error() != second.Error() {
		t.Fatalf("findings are not deterministic:\n%v\n%v", first, second)
	}
	var validation *ValidationError
	if !errors.As(first, &validation) || len(validation.Issues) < 3 {
		t.Fatalf("expected at least three independent findings, got %v", first)
	}
}

func TestMarshalRejectsInvalidDocumentsBeforeEncoding(t *testing.T) {
	if _, err := MarshalLedger(Ledger{}); !errors.Is(err, ErrInvalidLedger) {
		t.Fatalf("MarshalLedger(zero) error = %v, want ErrInvalidLedger", err)
	}
	if _, err := MarshalReceipt(Receipt{}); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("MarshalReceipt(zero) error = %v, want ErrInvalidReceipt", err)
	}
	if _, err := MarshalStep(Step{}); !errors.Is(err, ErrInvalidJournal) {
		t.Fatalf("MarshalStep(zero) error = %v, want ErrInvalidJournal", err)
	}
}

func TestEmptyLedgerEncodesAsAnExplicitEmptyArray(t *testing.T) {
	encoded, err := MarshalLedger(contractLedger(t))
	if err != nil {
		t.Fatalf("MarshalLedger: %v", err)
	}
	if !strings.Contains(string(encoded), `"entries": []`) {
		t.Fatalf("empty ledger did not encode an explicit empty array:\n%s", encoded)
	}
	reloaded, err := LoadLedger(encoded)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(reloaded.Entries) != 0 {
		t.Fatalf("round trip changed the entry count: %+v", reloaded.Entries)
	}
}

func TestFormatExtractSpliceRemoveManagedBlock(t *testing.T) {
	owner := "CODEX-HARNESS-STAGING"
	rendered := "model = \"gpt-5.6-sol\"\nreasoning = \"high\""
	formatted := FormatManagedBlock(rendered, owner)

	if !strings.Contains(formatted, "# BEGIN CODEX-HARNESS-STAGING MANAGED CONFIG") {
		t.Fatalf("formatted missing begin marker:\n%s", formatted)
	}
	if !strings.Contains(formatted, "# END CODEX-HARNESS-STAGING MANAGED CONFIG") {
		t.Fatalf("formatted missing end marker:\n%s", formatted)
	}

	block, prefix, suffix, found := ExtractManagedBlock(formatted, owner)
	if !found {
		t.Fatal("ExtractManagedBlock did not find formatted block")
	}
	if strings.TrimSpace(block) != strings.TrimSpace(rendered) {
		t.Fatalf("extracted block = %q, want %q", block, rendered)
	}
	if prefix != "" || suffix != "" {
		t.Fatalf("unexpected prefix=%q suffix=%q", prefix, suffix)
	}

	existingUserConfig := "[profiles.user]\nname = \"custom\"\n"
	spliced, err := SpliceManagedBlock(existingUserConfig, rendered, owner)
	if err != nil {
		t.Fatalf("SpliceManagedBlock: %v", err)
	}
	if !strings.HasPrefix(spliced, "[profiles.user]") {
		t.Fatalf("spliced did not preserve unmanaged user config:\n%s", spliced)
	}
	if !strings.Contains(spliced, "model = \"gpt-5.6-sol\"") {
		t.Fatalf("spliced missing rendered content:\n%s", spliced)
	}

	updatedRendered := "model = \"gpt-5.6-luna\"\nreasoning = \"xhigh\""
	respliced, err := SpliceManagedBlock(spliced, updatedRendered, owner)
	if err != nil {
		t.Fatalf("SpliceManagedBlock (update): %v", err)
	}
	if !strings.HasPrefix(respliced, "[profiles.user]") {
		t.Fatalf("respliced did not preserve user prefix:\n%s", respliced)
	}
	if !strings.Contains(respliced, "model = \"gpt-5.6-luna\"") {
		t.Fatalf("respliced missing updated model:\n%s", respliced)
	}
	if strings.Contains(respliced, "gpt-5.6-sol") {
		t.Fatalf("respliced retained old model:\n%s", respliced)
	}

	removed, ok := RemoveManagedBlock(respliced, owner)
	if !ok {
		t.Fatal("RemoveManagedBlock failed to remove block")
	}
	if strings.TrimSpace(removed) != strings.TrimSpace(existingUserConfig) {
		t.Fatalf("after removal content = %q, want %q", removed, existingUserConfig)
	}
}

func TestManagedBlockPreservesUnmanagedContent(t *testing.T) {
	owner := "CODEX-HARNESS-STAGING"
	unmanagedPrefix := "# User custom config\n[custom]\nenabled = true\n\n"
	unmanagedSuffix := "\n[footer]\nversion = 1\n"
	managedContent := "setting = \"managed\""

	initial := unmanagedPrefix + FormatManagedBlock(managedContent, owner) + unmanagedSuffix
	block, prefix, suffix, found := ExtractManagedBlock(initial, owner)
	if !found {
		t.Fatal("ExtractManagedBlock failed")
	}
	if strings.TrimSpace(block) != managedContent {
		t.Fatalf("block = %q, want %q", block, managedContent)
	}
	if strings.TrimSpace(prefix) != strings.TrimSpace(unmanagedPrefix) {
		t.Fatalf("prefix = %q, want %q", prefix, unmanagedPrefix)
	}
	if strings.TrimSpace(suffix) != strings.TrimSpace(unmanagedSuffix) {
		t.Fatalf("suffix = %q, want %q", suffix, unmanagedSuffix)
	}

	newContent := "setting = \"updated-managed\""
	spliced, err := SpliceManagedBlock(initial, newContent, owner)
	if err != nil {
		t.Fatalf("SpliceManagedBlock: %v", err)
	}
	if !strings.Contains(spliced, "[custom]") || !strings.Contains(spliced, "[footer]") {
		t.Fatalf("unmanaged sections lost in splice:\n%s", spliced)
	}

	removed, ok := RemoveManagedBlock(spliced, owner)
	if !ok {
		t.Fatal("RemoveManagedBlock failed")
	}
	if !strings.Contains(removed, "[custom]") || !strings.Contains(removed, "[footer]") {
		t.Fatalf("unmanaged sections lost in removal:\n%s", removed)
	}
	if strings.Contains(removed, "BEGIN") || strings.Contains(removed, "END") {
		t.Fatalf("fence markers remained after removal:\n%s", removed)
	}
}
