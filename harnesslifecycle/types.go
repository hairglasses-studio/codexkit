package harnesslifecycle

import (
	"errors"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/harnessrender"
)

const (
	// SchemaVersion is the only journal, receipt, and ledger schema understood
	// by this package.
	SchemaVersion = 1

	// MaxDocumentBytes bounds an encoded lifecycle document before decoding.
	MaxDocumentBytes = 8 << 20

	// MaxIntents bounds one journal independently of document size. It matches
	// the plan contract so a valid plan can always be journaled.
	MaxIntents = harnessplan.MaxOperations
	// MaxLedgerEntries bounds one ledger document.
	MaxLedgerEntries = 1 << 16
	// MaxTombstonesPerEntry bounds the append-only removal history retained for
	// a single destination.
	MaxTombstonesPerEntry = 1024

	// JournalIDBytes is the exact decoded length of a journal identifier. The
	// identifier is opaque, lowercase hex, and carries no host information.
	JournalIDBytes = 16
)

var (
	// ErrInvalidJournal, ErrInvalidReceipt, and ErrInvalidLedger classify
	// contract violations. Callers match with errors.Is.
	ErrInvalidJournal = errors.New("invalid harness journal")
	ErrInvalidReceipt = errors.New("invalid harness receipt")
	ErrInvalidLedger  = errors.New("invalid harness ownership ledger")

	// ErrUnowned reports an attempt to replace or remove a destination the
	// ledger does not currently own. It is the no-unowned-delete guarantee.
	ErrUnowned = errors.New("destination is not owned by this harness")
	// ErrCollision reports a create whose destination already exists in a state
	// the plan did not authorize.
	ErrCollision = errors.New("destination already exists")
	// ErrPreconditionMismatch reports an observed prior state that does not
	// match the reviewed plan's before state.
	ErrPreconditionMismatch = errors.New("observed destination state does not match plan")
	// ErrIncompleteApply reports a journal whose committed steps and receipt do
	// not agree with its intents. Recovery is a later slice; detection is here.
	ErrIncompleteApply = errors.New("harness apply is incomplete")
	// ErrUnsupported reports a platform without the containment substrate the
	// apply engine requires.
	ErrUnsupported = errors.New("harness lifecycle apply is unsupported on this platform")
)

// IntentKind is the closed set of transitions a journal may record. It mirrors
// harnessplan.OperationKind because a journal may never describe a mutation the
// reviewed plan language cannot express.
type IntentKind string

const (
	IntentCreate          IntentKind = IntentKind(harnessplan.OperationCreate)
	IntentReplaceOwned    IntentKind = IntentKind(harnessplan.OperationReplaceOwned)
	IntentRemoveTombstone IntentKind = IntentKind(harnessplan.OperationRemoveTombstone)
)

// Outcome is the closed set of terminal apply results.
type Outcome string

const (
	// OutcomeApplied means every intent reached its planned after state.
	OutcomeApplied Outcome = "applied"
	// OutcomeRefused means preflight rejected the apply and nothing was
	// mutated. It is the fail-closed result.
	OutcomeRefused Outcome = "refused"
	// OutcomePartial means at least one intent committed and at least one did
	// not. The journal remains the authority for recovery.
	OutcomePartial Outcome = "partial"
	// OutcomeFailed means a transition was attempted and no intent is confirmed
	// applied. It is deliberately distinct from OutcomeRefused, which asserts
	// that the target was never touched.
	OutcomeFailed Outcome = "failed"
)

// StepStatus is the closed set of per-intent results.
type StepStatus string

const (
	// StepApplied means the transition committed and was verified by read-back.
	StepApplied StepStatus = "applied"
	// StepSkippedIdempotent means the destination already held the planned
	// after state under harness ownership, so no mutation was needed.
	StepSkippedIdempotent StepStatus = "skipped-idempotent"
	// StepRefused means preflight rejected this intent. See Reason.
	StepRefused StepStatus = "refused"
	// StepFailed means the transition was attempted and did not complete.
	StepFailed StepStatus = "failed"
	// StepNotAttempted means an earlier intent's outcome stopped the apply
	// before this one began.
	StepNotAttempted StepStatus = "not-attempted"
)

// Reason is a closed enumeration. Receipts carry no free-form text, so a
// receipt cannot leak payload bytes, secret values, or host-absolute paths.
type Reason string

const (
	ReasonNone                 Reason = ""
	ReasonUnowned              Reason = "unowned-destination"
	ReasonTombstoned           Reason = "destination-tombstoned"
	ReasonCollision            Reason = "destination-exists"
	ReasonPreconditionMismatch Reason = "precondition-mismatch"
	ReasonUnsupportedFileType  Reason = "unsupported-file-type"
	ReasonMissingPayload       Reason = "missing-payload"
	ReasonPayloadMismatch      Reason = "payload-digest-mismatch"
	ReasonReadBackMismatch     Reason = "read-back-mismatch"
	ReasonSubstrateError       Reason = "substrate-error"
	ReasonRetainedForRecovery  Reason = "retained-for-recovery"
	ReasonEarlierIntentStopped Reason = "earlier-intent-stopped-apply"
	ReasonParentMissing        Reason = "destination-parent-missing"
)

// TombstoneReason is the closed set of removal causes.
type TombstoneReason string

const (
	TombstoneUninstall       TombstoneReason = "uninstall"
	TombstoneManifestRemoval TombstoneReason = "manifest-removal"
	TombstoneSuperseded      TombstoneReason = "superseded"
)

// LedgerStatus is the closed lifecycle state of one ledger row. Rows are never
// dropped, so "tombstoned" is the only removal state.
type LedgerStatus string

const (
	LedgerOwned      LedgerStatus = "owned"
	LedgerTombstoned LedgerStatus = "tombstoned"
)

// ObservedState is a no-follow observation of a destination made by the apply
// engine itself. It is deliberately distinct from harnessplan.FileState: a plan
// states what the reviewer believed, this states what the filesystem held. Only
// the identity fields a later recovery needs are retained.
type ObservedState struct {
	Type          harnessplan.FileType `json:"type"`
	Mode          string               `json:"mode,omitempty"`
	Hash          string               `json:"hash,omitempty"`
	SymlinkTarget string               `json:"symlink_target,omitempty"`
	Device        uint64               `json:"device,omitempty"`
	Inode         uint64               `json:"inode,omitempty"`
	Links         uint64               `json:"links,omitempty"`
}

// Journal is written and fsynced before the first mutation of an apply.
type Journal struct {
	SchemaVersion    int       `json:"schema_version"`
	JournalID        string    `json:"journal_id"`
	PlanSHA256       string    `json:"plan_sha256"`
	ManifestDigest   string    `json:"manifest_digest"`
	InventoryDigest  string    `json:"inventory_digest"`
	TargetRepository string    `json:"target_repository"`
	MarkerOwner      string    `json:"marker_owner"`
	OpenedAt         time.Time `json:"opened_at"`
	// Authorized reports that preflight accepted every intent and that mutation
	// was about to begin. An unauthorized journal is the fail-closed record of
	// a refused apply and carries no intents, because nothing was attempted.
	Authorized bool     `json:"authorized"`
	Intents    []Intent `json:"intents"`
}

// Intent is one journaled transition. Sequence is dense, zero-based, and fixes
// the commit order, so recovery can address step documents by position without
// listing a directory.
type Intent struct {
	Sequence     int                   `json:"sequence"`
	OperationID  string                `json:"operation_id"`
	ObjectID     string                `json:"object_id"`
	Kind         IntentKind            `json:"kind"`
	Destination  string                `json:"destination"`
	Observed     ObservedState         `json:"observed"`
	Expected     harnessplan.FileState `json:"expected"`
	After        harnessplan.FileState `json:"after"`
	PayloadHash  string                `json:"payload_hash,omitempty"`
	PayloadSize  int64                 `json:"payload_size,omitempty"`
	LedgerStatus LedgerStatus          `json:"ledger_status,omitempty"`
}

// Step is the atomically created record proving one intent reached a terminal
// state. Its presence is the only evidence a transition committed.
type Step struct {
	SchemaVersion int           `json:"schema_version"`
	JournalID     string        `json:"journal_id"`
	Sequence      int           `json:"sequence"`
	OperationID   string        `json:"operation_id"`
	Destination   string        `json:"destination"`
	Status        StepStatus    `json:"status"`
	Reason        Reason        `json:"reason,omitempty"`
	Observed      ObservedState `json:"observed"`
	Installed     ObservedState `json:"installed"`
	Retained      *Retained     `json:"retained,omitempty"`
	CompletedAt   time.Time     `json:"completed_at"`
}

// Retained names an object the substrate moved out of the public namespace and
// still holds. Nothing is ever unlinked, so a retained object is recoverable
// until an operator disposes of it deliberately.
type Retained struct {
	QuarantinePath string        `json:"quarantine_path"`
	State          ObservedState `json:"state"`
}

// Receipt is the sanitized terminal record of one apply.
type Receipt struct {
	SchemaVersion    int             `json:"schema_version"`
	JournalID        string          `json:"journal_id"`
	PlanSHA256       string          `json:"plan_sha256"`
	ManifestDigest   string          `json:"manifest_digest"`
	InventoryDigest  string          `json:"inventory_digest"`
	TargetRepository string          `json:"target_repository"`
	MarkerOwner      string          `json:"marker_owner"`
	Outcome          Outcome         `json:"outcome"`
	OpenedAt         time.Time       `json:"opened_at"`
	ClosedAt         time.Time       `json:"closed_at"`
	Results          []ReceiptResult `json:"results"`
}

// ReceiptResult is one intent's sanitized outcome. Content appears only as a
// digest and paths only as portable relative paths.
type ReceiptResult struct {
	Sequence    int        `json:"sequence"`
	OperationID string     `json:"operation_id"`
	ObjectID    string     `json:"object_id"`
	Kind        IntentKind `json:"kind"`
	Destination string     `json:"destination"`
	Status      StepStatus `json:"status"`
	Reason      Reason     `json:"reason,omitempty"`
	BeforeHash  string     `json:"before_hash,omitempty"`
	AfterHash   string     `json:"after_hash,omitempty"`
	Retained    string     `json:"retained,omitempty"`
}

// Ledger records every destination this harness has ever owned in the target
// repository. Entries are keyed by destination and are never removed.
type Ledger struct {
	SchemaVersion    int           `json:"schema_version"`
	TargetRepository string        `json:"target_repository"`
	MarkerOwner      string        `json:"marker_owner"`
	UpdatedAt        time.Time     `json:"updated_at"`
	Entries          []LedgerEntry `json:"entries"`
}

// LedgerEntry is one owned or tombstoned destination. Installed is present
// exactly when Status is LedgerOwned. Tombstones is append-only history and is
// retained across reinstallation.
type LedgerEntry struct {
	Destination string          `json:"destination"`
	ObjectID    string          `json:"object_id"`
	Owner       string          `json:"owner"`
	Status      LedgerStatus    `json:"status"`
	Installed   *InstalledState `json:"installed,omitempty"`
	Tombstones  []Tombstone     `json:"tombstones,omitempty"`
}

// InstalledState is the post-image the harness believes it owns.
type InstalledState struct {
	Type          harnessplan.FileType `json:"type"`
	Mode          string               `json:"mode,omitempty"`
	Hash          string               `json:"hash"`
	SymlinkTarget string               `json:"symlink_target,omitempty"`
	PlanSHA256    string               `json:"plan_sha256"`
	JournalID     string               `json:"journal_id"`
	InstalledAt   time.Time            `json:"installed_at"`
}

// Tombstone records one removal. QuarantinePath names the object the substrate
// still retains; it is never an unlink receipt.
type Tombstone struct {
	Reason         TombstoneReason `json:"reason"`
	PlanSHA256     string          `json:"plan_sha256"`
	JournalID      string          `json:"journal_id"`
	RemovedHash    string          `json:"removed_hash,omitempty"`
	QuarantinePath string          `json:"quarantine_path,omitempty"`
	RecordedAt     time.Time       `json:"recorded_at"`
}

// Incomplete describes one journal whose on-disk evidence does not prove a
// terminal apply.
type Incomplete struct {
	JournalID      string   `json:"journal_id"`
	PlanSHA256     string   `json:"plan_sha256"`
	IntentCount    int      `json:"intent_count"`
	StepCount      int      `json:"step_count"`
	ReceiptPresent bool     `json:"receipt_present"`
	MissingSteps   []int    `json:"missing_steps"`
	Destinations   []string `json:"destinations"`
}

// RecoverOptions binds an incomplete apply generation to its original inputs
// so missing transitions can be completed and recorded.
type RecoverOptions struct {
	StatePath       string
	TargetPath      string
	Result          harnessrender.Result
	MarkerOwner     string
	TombstoneReason TombstoneReason
	Generation      int
	Now             func() time.Time
}

// RollbackOptions binds an applied or partial apply generation so its committed
// transitions can be reversed using retained quarantine objects.
type RollbackOptions struct {
	StatePath        string
	TargetPath       string
	TargetRepository string
	MarkerOwner      string
	Generation       int
	Now              func() time.Time
}

// UninstallOptions binds a target repository to remove all currently owned
// destinations to quarantine and record tombstones in the ledger.
type UninstallOptions struct {
	StatePath        string
	TargetPath       string
	TargetRepository string
	MarkerOwner      string
	Now              func() time.Time
}
