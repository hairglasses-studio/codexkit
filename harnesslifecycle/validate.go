package harnesslifecycle

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hairglasses-studio/codexkit/harnessplan"
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	journalIDPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	quarantinePattern = regexp.MustCompile(`^(?:[^/]+/)*\.codex-harness-quarantine-[0-9a-f]{32}$`)
)

// Issue is one stable validation finding.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError contains every independently detectable finding for one
// document. Issues are sorted and deduplicated so two runs over equal input
// produce byte-identical diagnostics.
type ValidationError struct {
	Kind   error   `json:"-"`
	Issues []Issue `json:"issues"`
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "harness lifecycle validation failed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s %s: %s", issue.Code, issue.Path, issue.Message))
	}
	return "harness lifecycle validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Is(target error) bool {
	return e != nil && e.Kind != nil && target == e.Kind
}

type issues struct {
	kind  error
	found []Issue
}

func (i *issues) add(code, field, message string) {
	i.found = append(i.found, Issue{Code: code, Path: field, Message: message})
}

func (i *issues) err() error {
	if len(i.found) == 0 {
		return nil
	}
	sort.Slice(i.found, func(a, b int) bool {
		if i.found[a].Path != i.found[b].Path {
			return i.found[a].Path < i.found[b].Path
		}
		if i.found[a].Code != i.found[b].Code {
			return i.found[a].Code < i.found[b].Code
		}
		return i.found[a].Message < i.found[b].Message
	})
	deduplicated := i.found[:1]
	for _, issue := range i.found[1:] {
		last := deduplicated[len(deduplicated)-1]
		if issue == last {
			continue
		}
		deduplicated = append(deduplicated, issue)
	}
	return &ValidationError{Kind: i.kind, Issues: deduplicated}
}

// ValidateJournal reports every contract violation in a journal.
func ValidateJournal(journal Journal) error {
	findings := &issues{kind: ErrInvalidJournal}
	validateJournalInto(findings, journal)
	return findings.err()
}

func validateJournalInto(findings *issues, journal Journal) {
	if journal.SchemaVersion != SchemaVersion {
		findings.add("schema", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	validateJournalID(findings, "journal_id", journal.JournalID)
	validateDigestField(findings, "plan_sha256", journal.PlanSHA256)
	validateDigestField(findings, "manifest_digest", journal.ManifestDigest)
	validateDigestField(findings, "inventory_digest", journal.InventoryDigest)
	if !validRepository(journal.TargetRepository) {
		findings.add("repository", "target_repository", "must be a stable owner/name repository identifier")
	}
	if !identifierPattern.MatchString(journal.MarkerOwner) {
		findings.add("identifier", "marker_owner", "must be a stable owner identifier")
	}
	validateTimestamp(findings, "opened_at", journal.OpenedAt)

	if len(journal.Intents) > MaxIntents {
		findings.add("bounds", "intents", fmt.Sprintf("must contain at most %d entries", MaxIntents))
		return
	}
	if !journal.Authorized {
		if len(journal.Intents) != 0 {
			findings.add("authorization", "intents", "an unauthorized journal must record no intents")
		}
		return
	}
	seenSequence := make(map[int]struct{}, len(journal.Intents))
	seenOperation := make(map[string]struct{}, len(journal.Intents))
	destinations := make([]string, 0, len(journal.Intents))
	for index, intent := range journal.Intents {
		base := fmt.Sprintf("intents[%d]", index)
		if intent.Sequence != index {
			findings.add("sequence", base+".sequence", "must be dense, zero-based, and equal to its position")
		}
		if _, exists := seenSequence[intent.Sequence]; exists {
			findings.add("duplicate", base+".sequence", "must be unique")
		}
		seenSequence[intent.Sequence] = struct{}{}
		if !identifierPattern.MatchString(intent.OperationID) {
			findings.add("identifier", base+".operation_id", "must be a stable identifier")
		} else if _, exists := seenOperation[intent.OperationID]; exists {
			findings.add("duplicate", base+".operation_id", "must be unique within one journal")
		} else {
			seenOperation[intent.OperationID] = struct{}{}
		}
		if !identifierPattern.MatchString(intent.ObjectID) {
			findings.add("identifier", base+".object_id", "must be a stable identifier")
		}
		if err := validateRelativePath(intent.Destination); err != nil {
			findings.add("path", base+".destination", err.Error())
		} else {
			destinations = append(destinations, intent.Destination)
		}
		validateIntentKind(findings, base, intent)
		validateObservedState(findings, base+".observed", intent.Destination, intent.Observed)
	}
	validateDisjointDestinations(findings, "intents", destinations)
}

func validateIntentKind(findings *issues, base string, intent Intent) {
	switch intent.Kind {
	case IntentCreate:
		if intent.Expected.Type != harnessplan.FileAbsent {
			findings.add("state", base+".expected.type", "create must expect an absent destination")
		}
		requireMaterialized(findings, base+".after", intent.After)
		requirePayloadAgreement(findings, base, intent)
	case IntentReplaceOwned:
		requireMaterialized(findings, base+".expected", intent.Expected)
		requireMaterialized(findings, base+".after", intent.After)
		requirePayloadAgreement(findings, base, intent)
		if intent.LedgerStatus != LedgerOwned {
			findings.add("ownership", base+".ledger_status", "replace requires a currently owned ledger entry")
		}
	case IntentRemoveTombstone:
		requireMaterialized(findings, base+".expected", intent.Expected)
		if intent.After.Type != harnessplan.FileAbsent {
			findings.add("state", base+".after.type", "remove-tombstone must target an absent after state")
		}
		if intent.PayloadHash != "" || intent.PayloadSize != 0 {
			findings.add("payload", base+".payload_hash", "remove-tombstone must not carry a payload")
		}
		if intent.LedgerStatus != LedgerOwned {
			findings.add("ownership", base+".ledger_status", "removal requires a currently owned ledger entry")
		}
	default:
		findings.add("enum", base+".kind", "contains an unknown intent kind")
	}
}

func requireMaterialized(findings *issues, field string, state harnessplan.FileState) {
	if state.Type != harnessplan.FileRegular && state.Type != harnessplan.FileRelativeSymlink {
		findings.add("state", field+".type", "must be a materialized regular file or relative symlink")
		return
	}
	if !digestPattern.MatchString(state.Hash) {
		findings.add("digest", field+".hash", "must be a lowercase sha256 digest")
	}
}

func requirePayloadAgreement(findings *issues, base string, intent Intent) {
	if intent.After.Type == harnessplan.FileRegular {
		if !digestPattern.MatchString(intent.PayloadHash) {
			findings.add("digest", base+".payload_hash", "must be a lowercase sha256 digest")
		} else if intent.PayloadHash != intent.After.Hash {
			findings.add("binding_mismatch", base+".payload_hash", "must equal the after-state hash")
		}
		if intent.PayloadSize < 0 || intent.PayloadSize > harnessplan.MaxPayloadBytes {
			findings.add("bounds", base+".payload_size", fmt.Sprintf("must be between 0 and %d bytes", harnessplan.MaxPayloadBytes))
		}
		return
	}
	if intent.PayloadHash != "" || intent.PayloadSize != 0 {
		findings.add("payload", base+".payload_hash", "is valid only for a regular-file after state")
	}
}

func validateObservedState(findings *issues, field, destination string, state ObservedState) {
	switch state.Type {
	case harnessplan.FileAbsent:
		if state.Mode != "" || state.Hash != "" || state.SymlinkTarget != "" || state.Inode != 0 || state.Device != 0 || state.Links != 0 {
			findings.add("state", field, "absent observation must not carry mode, hash, target, or inode identity")
		}
	case harnessplan.FileRegular:
		validateMode(findings, field+".mode", state.Mode)
		if !digestPattern.MatchString(state.Hash) {
			findings.add("digest", field+".hash", "must be a lowercase sha256 digest")
		}
		if state.SymlinkTarget != "" {
			findings.add("state", field+".symlink_target", "regular-file observation must not carry a symlink target")
		}
	case harnessplan.FileRelativeSymlink:
		if state.Mode != "" {
			findings.add("state", field+".mode", "relative-symlink observation must not carry a mode")
		}
		if !digestPattern.MatchString(state.Hash) {
			findings.add("digest", field+".hash", "must be a lowercase sha256 digest")
		}
		if err := validateSymlinkTarget(destination, state.SymlinkTarget); err != nil {
			findings.add("path", field+".symlink_target", err.Error())
		}
	default:
		findings.add("enum", field+".type", "contains an unknown file type")
	}
}

// ValidateStep reports every contract violation in a step record.
func ValidateStep(step Step) error {
	findings := &issues{kind: ErrInvalidJournal}
	if step.SchemaVersion != SchemaVersion {
		findings.add("schema", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	validateJournalID(findings, "journal_id", step.JournalID)
	if step.Sequence < 0 || step.Sequence >= MaxIntents {
		findings.add("bounds", "sequence", "must address a journaled intent")
	}
	if !identifierPattern.MatchString(step.OperationID) {
		findings.add("identifier", "operation_id", "must be a stable identifier")
	}
	if err := validateRelativePath(step.Destination); err != nil {
		findings.add("path", "destination", err.Error())
	}
	if !validStepStatus(step.Status) {
		findings.add("enum", "status", "contains an unknown step status")
	}
	if !validReason(step.Reason) {
		findings.add("enum", "reason", "contains an unknown reason code")
	}
	if step.Status != StepApplied && step.Status != StepSkippedIdempotent && step.Reason == ReasonNone {
		findings.add("enum", "reason", "a non-successful step must carry a reason code")
	}
	validateObservedState(findings, "observed", step.Destination, step.Observed)
	validateObservedState(findings, "installed", step.Destination, step.Installed)
	if step.Retained != nil {
		if !quarantinePattern.MatchString(step.Retained.QuarantinePath) {
			findings.add("path", "retained.quarantine_path", "must name an object in the substrate quarantine namespace")
		}
		validateObservedState(findings, "retained.state", step.Destination, step.Retained.State)
	}
	validateTimestamp(findings, "completed_at", step.CompletedAt)
	return findings.err()
}

// ValidateReceipt reports every contract violation in a receipt, including the
// sanitization invariants: portable paths, digests, and closed enumerations
// only.
func ValidateReceipt(receipt Receipt) error {
	findings := &issues{kind: ErrInvalidReceipt}
	if receipt.SchemaVersion != SchemaVersion {
		findings.add("schema", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	validateJournalID(findings, "journal_id", receipt.JournalID)
	validateDigestField(findings, "plan_sha256", receipt.PlanSHA256)
	validateDigestField(findings, "manifest_digest", receipt.ManifestDigest)
	validateDigestField(findings, "inventory_digest", receipt.InventoryDigest)
	if !validRepository(receipt.TargetRepository) {
		findings.add("repository", "target_repository", "must be a stable owner/name repository identifier")
	}
	if !identifierPattern.MatchString(receipt.MarkerOwner) {
		findings.add("identifier", "marker_owner", "must be a stable owner identifier")
	}
	switch receipt.Outcome {
	case OutcomeApplied, OutcomeRefused, OutcomePartial, OutcomeFailed:
	default:
		findings.add("enum", "outcome", "contains an unknown outcome")
	}
	validateTimestamp(findings, "opened_at", receipt.OpenedAt)
	validateTimestamp(findings, "closed_at", receipt.ClosedAt)
	if !receipt.OpenedAt.IsZero() && !receipt.ClosedAt.IsZero() && receipt.ClosedAt.Before(receipt.OpenedAt) {
		findings.add("ordering", "closed_at", "must not precede opened_at")
	}
	if len(receipt.Results) > MaxIntents {
		findings.add("bounds", "results", fmt.Sprintf("must contain at most %d entries", MaxIntents))
		return findings.err()
	}

	applied, unfinished := 0, 0
	for index, result := range receipt.Results {
		base := fmt.Sprintf("results[%d]", index)
		if result.Sequence != index {
			findings.add("sequence", base+".sequence", "must be dense, zero-based, and equal to its position")
		}
		if !identifierPattern.MatchString(result.OperationID) {
			findings.add("identifier", base+".operation_id", "must be a stable identifier")
		}
		if !identifierPattern.MatchString(result.ObjectID) {
			findings.add("identifier", base+".object_id", "must be a stable identifier")
		}
		if err := validateRelativePath(result.Destination); err != nil {
			findings.add("path", base+".destination", err.Error())
		}
		switch result.Kind {
		case IntentCreate, IntentReplaceOwned, IntentRemoveTombstone:
		default:
			findings.add("enum", base+".kind", "contains an unknown intent kind")
		}
		if !validStepStatus(result.Status) {
			findings.add("enum", base+".status", "contains an unknown step status")
		}
		if !validReason(result.Reason) {
			findings.add("enum", base+".reason", "contains an unknown reason code")
		}
		if result.BeforeHash != "" && !digestPattern.MatchString(result.BeforeHash) {
			findings.add("digest", base+".before_hash", "must be a lowercase sha256 digest")
		}
		if result.AfterHash != "" && !digestPattern.MatchString(result.AfterHash) {
			findings.add("digest", base+".after_hash", "must be a lowercase sha256 digest")
		}
		if result.Retained != "" && !quarantinePattern.MatchString(result.Retained) {
			findings.add("path", base+".retained", "must name an object in the substrate quarantine namespace")
		}
		switch result.Status {
		case StepApplied, StepSkippedIdempotent:
			applied++
		case StepRefused, StepFailed, StepNotAttempted:
			unfinished++
		}
	}
	switch receipt.Outcome {
	case OutcomeApplied:
		if unfinished != 0 {
			findings.add("outcome", "outcome", "applied requires every result to have succeeded")
		}
	case OutcomeRefused:
		if applied != 0 {
			findings.add("outcome", "outcome", "refused requires that no result mutated the target")
		}
	case OutcomePartial:
		if applied == 0 || unfinished == 0 {
			findings.add("outcome", "outcome", "partial requires both a succeeded and an unfinished result")
		}
	case OutcomeFailed:
		if applied != 0 {
			findings.add("outcome", "outcome", "failed requires that no result succeeded")
		}
	}
	return findings.err()
}

// ValidateLedger reports every contract violation in an ownership ledger.
func ValidateLedger(ledger Ledger) error {
	findings := &issues{kind: ErrInvalidLedger}
	if ledger.SchemaVersion != SchemaVersion {
		findings.add("schema", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	if !validRepository(ledger.TargetRepository) {
		findings.add("repository", "target_repository", "must be a stable owner/name repository identifier")
	}
	if !identifierPattern.MatchString(ledger.MarkerOwner) {
		findings.add("identifier", "marker_owner", "must be a stable owner identifier")
	}
	validateTimestamp(findings, "updated_at", ledger.UpdatedAt)
	if len(ledger.Entries) > MaxLedgerEntries {
		findings.add("bounds", "entries", fmt.Sprintf("must contain at most %d entries", MaxLedgerEntries))
		return findings.err()
	}

	previous := ""
	for index, entry := range ledger.Entries {
		base := fmt.Sprintf("entries[%d]", index)
		if err := validateRelativePath(entry.Destination); err != nil {
			findings.add("path", base+".destination", err.Error())
		} else {
			if index > 0 && entry.Destination <= previous {
				findings.add("ordering", base+".destination", "entries must be sorted by destination and unique")
			}
			previous = entry.Destination
		}
		if !identifierPattern.MatchString(entry.ObjectID) {
			findings.add("identifier", base+".object_id", "must be a stable identifier")
		}
		if entry.Owner != ledger.MarkerOwner {
			findings.add("ownership", base+".owner", "must equal the ledger marker owner")
		}
		switch entry.Status {
		case LedgerOwned:
			if entry.Installed == nil {
				findings.add("ownership", base+".installed", "an owned entry must record its installed state")
			}
		case LedgerTombstoned:
			if entry.Installed != nil {
				findings.add("ownership", base+".installed", "a tombstoned entry must not record an installed state")
			}
			if len(entry.Tombstones) == 0 {
				findings.add("ownership", base+".tombstones", "a tombstoned entry must record at least one tombstone")
			}
		default:
			findings.add("enum", base+".status", "contains an unknown ledger status")
		}
		if entry.Installed != nil {
			validateInstalledState(findings, base+".installed", entry.Destination, *entry.Installed)
		}
		if len(entry.Tombstones) > MaxTombstonesPerEntry {
			findings.add("bounds", base+".tombstones", fmt.Sprintf("must contain at most %d entries", MaxTombstonesPerEntry))
			continue
		}
		for tombstoneIndex, tombstone := range entry.Tombstones {
			tombstoneBase := fmt.Sprintf("%s.tombstones[%d]", base, tombstoneIndex)
			switch tombstone.Reason {
			case TombstoneUninstall, TombstoneManifestRemoval, TombstoneSuperseded:
			default:
				findings.add("enum", tombstoneBase+".reason", "contains an unknown tombstone reason")
			}
			validateDigestField(findings, tombstoneBase+".plan_sha256", tombstone.PlanSHA256)
			validateJournalID(findings, tombstoneBase+".journal_id", tombstone.JournalID)
			if tombstone.RemovedHash != "" && !digestPattern.MatchString(tombstone.RemovedHash) {
				findings.add("digest", tombstoneBase+".removed_hash", "must be a lowercase sha256 digest")
			}
			if tombstone.QuarantinePath != "" && !quarantinePattern.MatchString(tombstone.QuarantinePath) {
				findings.add("path", tombstoneBase+".quarantine_path", "must name an object in the substrate quarantine namespace")
			}
			validateTimestamp(findings, tombstoneBase+".recorded_at", tombstone.RecordedAt)
			if tombstoneIndex > 0 && tombstone.RecordedAt.Before(entry.Tombstones[tombstoneIndex-1].RecordedAt) {
				findings.add("ordering", tombstoneBase+".recorded_at", "tombstone history must be chronological")
			}
		}
	}
	return findings.err()
}

func validateInstalledState(findings *issues, field, destination string, state InstalledState) {
	switch state.Type {
	case harnessplan.FileRegular:
		validateMode(findings, field+".mode", state.Mode)
		if state.SymlinkTarget != "" {
			findings.add("state", field+".symlink_target", "regular-file state must not carry a symlink target")
		}
	case harnessplan.FileRelativeSymlink:
		if state.Mode != "" {
			findings.add("state", field+".mode", "relative-symlink state must not carry a mode")
		}
		if err := validateSymlinkTarget(destination, state.SymlinkTarget); err != nil {
			findings.add("path", field+".symlink_target", err.Error())
		}
	default:
		findings.add("enum", field+".type", "an installed state must be a materialized file type")
	}
	if !digestPattern.MatchString(state.Hash) {
		findings.add("digest", field+".hash", "must be a lowercase sha256 digest")
	}
	validateDigestField(findings, field+".plan_sha256", state.PlanSHA256)
	validateJournalID(findings, field+".journal_id", state.JournalID)
	validateTimestamp(findings, field+".installed_at", state.InstalledAt)
}

func validateJournalID(findings *issues, field, value string) {
	if !journalIDPattern.MatchString(value) {
		findings.add("identifier", field, fmt.Sprintf("must be %d lowercase hex bytes", JournalIDBytes))
	}
}

func validateDigestField(findings *issues, field, value string) {
	if !digestPattern.MatchString(value) {
		findings.add("digest", field, "must be a lowercase sha256 digest")
	}
}

func validateTimestamp(findings *issues, field string, value time.Time) {
	if value.IsZero() {
		findings.add("time", field, "must be set")
		return
	}
	if value.Year() < 2000 || value.Year() > 2200 {
		findings.add("time", field, "must fall within a plausible operating range")
	}
}

func validateDisjointDestinations(findings *issues, field string, destinations []string) {
	sorted := append([]string(nil), destinations...)
	sort.Strings(sorted)
	for index := 1; index < len(sorted); index++ {
		left, right := sorted[index-1], sorted[index]
		if left == right || strings.HasPrefix(right, left+"/") {
			findings.add("overlap", field, fmt.Sprintf("destinations %q and %q overlap", left, right))
		}
	}
}

func validateMode(findings *issues, field, mode string) {
	if len(mode) != 4 || mode[0] != '0' {
		findings.add("mode", field, "must be a four-digit octal permission mode")
		return
	}
	parsed, err := strconv.ParseUint(mode, 8, 12)
	if err != nil || parsed > 0o777 {
		findings.add("mode", field, "must be a four-digit octal permission mode without special bits")
		return
	}
	if parsed&0o400 == 0 {
		findings.add("mode", field, "must be owner-readable")
	}
	if parsed&0o022 != 0 {
		findings.add("mode", field, "must not be group- or world-writable")
	}
}

func validRepository(value string) bool {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, `\`) || hasControl(value) {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 8 {
		return false
	}
	for _, part := range parts {
		if !identifierPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func validateRelativePath(value string) error {
	if value == "" || len(value) > 4096 || hasControl(value) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return fmt.Errorf("must be a portable repository-relative path")
	}
	if path.Clean(value) != value || value == "." {
		return fmt.Errorf("must be normalized without dot or parent segments")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("must not contain empty, dot, or parent segments")
		}
	}
	return nil
}

func validateSymlinkTarget(destination, target string) error {
	if target == "" || len(target) > 4096 || hasControl(target) || strings.Contains(target, `\`) ||
		strings.HasPrefix(target, "/") || path.IsAbs(target) {
		return fmt.Errorf("must be a portable relative symlink target")
	}
	if path.Clean(target) != target || target == "." {
		return fmt.Errorf("must be normalized")
	}
	resolved := path.Clean(path.Join(path.Dir(destination), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") {
		return fmt.Errorf("escapes the target repository")
	}
	return nil
}

func validStepStatus(value StepStatus) bool {
	switch value {
	case StepApplied, StepSkippedIdempotent, StepRefused, StepFailed, StepNotAttempted:
		return true
	}
	return false
}

func validReason(value Reason) bool {
	switch value {
	case ReasonNone, ReasonUnowned, ReasonTombstoned, ReasonCollision, ReasonPreconditionMismatch,
		ReasonUnsupportedFileType, ReasonMissingPayload, ReasonPayloadMismatch, ReasonReadBackMismatch,
		ReasonSubstrateError, ReasonRetainedForRecovery, ReasonEarlierIntentStopped, ReasonParentMissing:
		return true
	}
	return false
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
