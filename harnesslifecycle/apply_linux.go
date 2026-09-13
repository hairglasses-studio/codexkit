//go:build linux

package harnesslifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/harnessrender"
	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
)

// applyPhase names a deterministic point in an apply. It exists only so
// same-package regression tests can interrupt a run exactly where a crash
// would be most damaging.
type applyPhase uint8

const (
	phaseAfterJournalCommit applyPhase = iota
	phaseBeforeTransition
	phaseAfterTransition
	phaseAfterStepCommit
	phaseBeforeLedgerCommit
	phaseBeforeReceiptCommit
)

// applyTestHook is nil outside same-package regression tests.
var applyTestHook func(phase applyPhase, sequence int)

func hook(phase applyPhase, sequence int) {
	if applyTestHook != nil {
		applyTestHook(phase, sequence)
	}
}

// ApplyOptions binds a verified render result to one privileged target root
// and one operator-private state store. Every field is required.
type ApplyOptions struct {
	// StatePath is the operator-private 0700 lifecycle store.
	StatePath string
	// TargetPath is the absolute root the plan's destinations are relative to.
	TargetPath string
	// Result is the output of harnessrender.Build for a reviewed plan. This
	// package never re-derives authority; it consumes an already-verified one.
	Result harnessrender.Result
	// MarkerOwner must equal the owner named by every artifact's after marker.
	MarkerOwner string
	// TombstoneReason classifies every remove-tombstone intent in this apply.
	TombstoneReason TombstoneReason
	// Now supplies the clock. Tests pass a deterministic one.
	Now func() time.Time
}

// Apply journals, mutates, and records one plan against a target root.
//
// The order is fixed and is the whole point of the contract: observe, decide,
// journal, mutate, step-record, ledger, receipt. Nothing is mutated before the
// journal is durable, and nothing is claimed applied before it is read back.
// Preflight is all-or-nothing: one refused intent refuses the whole apply and
// the target is not touched.
//
// A returned Receipt is meaningful even when the error is non-nil: it is the
// sanitized record of how far the apply got.
func Apply(options ApplyOptions) (Receipt, error) {
	if err := harnessfs.Capability(); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if options.Now == nil {
		return Receipt{}, fmt.Errorf("%w: a clock is required", ErrInvalidJournal)
	}
	if !filepath.IsAbs(options.TargetPath) || filepath.Clean(options.TargetPath) != options.TargetPath {
		return Receipt{}, fmt.Errorf("%w: target root must be an absolute clean path", ErrInvalidJournal)
	}
	switch options.TombstoneReason {
	case TombstoneUninstall, TombstoneManifestRemoval, TombstoneSuperseded:
	default:
		return Receipt{}, fmt.Errorf("%w: a closed tombstone reason is required", ErrInvalidJournal)
	}

	journalID, err := NewJournalID()
	if err != nil {
		return Receipt{}, err
	}

	store, err := CreateStore(options.StatePath)
	if err != nil {
		return Receipt{}, err
	}
	defer store.Close()

	target, err := harnessfs.Open(options.TargetPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: target root: %v", ErrInvalidJournal, err)
	}
	defer target.Close()

	run := &applyRun{
		options:   options,
		store:     store,
		target:    target,
		journalID: journalID,
		openedAt:  options.Now().UTC().Round(0),
	}
	return run.execute()
}

type applyRun struct {
	options    ApplyOptions
	store      *Store
	target     *harnessfs.Root
	journalID  string
	openedAt   time.Time
	generation int
}

type preflightIntent struct {
	intent   Intent
	artifact harnessrender.Artifact
	status   StepStatus
	reason   Reason
}

func (r *applyRun) execute() (Receipt, error) {
	ledger, ledgerGeneration, err := r.store.LoadCurrentLedger(
		r.options.Result.TargetRepository, r.options.MarkerOwner, r.options.Now)
	if err != nil {
		return Receipt{}, err
	}

	generation, err := r.store.nextGeneration(journalLeaf)
	if err != nil {
		return Receipt{}, err
	}
	r.generation = generation

	prepared, refused, err := r.preflight(ledger)
	if err != nil {
		return Receipt{}, err
	}

	if refused {
		return r.finishRefused(prepared)
	}
	return r.mutate(prepared, ledger, ledgerGeneration)
}

// preflight observes every destination no-follow, decides each intent against
// the ledger and the observation, and never mutates. It returns refused=true
// if any single intent is not authorized.
func (r *applyRun) preflight(ledger Ledger) ([]preflightIntent, bool, error) {
	prepared := make([]preflightIntent, 0, len(r.options.Result.Artifacts))
	refused := false

	for index, artifact := range r.options.Result.Artifacts {
		// The parent must be resolved before the leaf: an absent parent makes
		// the leaf unobservable, and directory creation is deliberately outside
		// the plan language, so this is a refusal rather than an error.
		parentPresent, err := r.parentPresent(artifact.Destination)
		if err != nil {
			return nil, false, err
		}
		observed := ObservedState{Type: harnessplan.FileAbsent}
		supported := true
		if parentPresent {
			if observed, supported, err = r.observe(artifact.Destination); err != nil {
				return nil, false, err
			}
		}

		item := preflightIntent{
			artifact: artifact,
			intent: Intent{
				Sequence:    index,
				OperationID: artifact.OperationID,
				ObjectID:    artifact.ObjectID,
				Kind:        IntentKind(artifact.Kind),
				Destination: artifact.Destination,
				Observed:    observed,
				Expected:    artifact.Before,
				After:       artifact.After,
			},
			status: StepApplied,
		}
		if artifact.After.Type == harnessplan.FileRegular {
			item.intent.PayloadHash = hashBytes(artifact.RegularBytes)
			item.intent.PayloadSize = int64(len(artifact.RegularBytes))
		}

		reason := ReasonUnsupportedFileType
		switch {
		case !parentPresent:
			reason = ReasonParentMissing
		case supported:
			reason = r.decide(ledger, artifact, observed, &item)
		}
		if reason != ReasonNone {
			item.status = StepRefused
			item.reason = reason
			refused = true
		}
		if entry, found := ledger.Lookup(artifact.Destination); found {
			item.intent.LedgerStatus = entry.Status
		}
		prepared = append(prepared, item)
	}

	if refused {
		for index := range prepared {
			if prepared[index].status == StepApplied {
				prepared[index].status = StepNotAttempted
				prepared[index].reason = ReasonEarlierIntentStopped
			}
		}
	}
	return prepared, refused, nil
}

// decide returns ReasonNone when an intent may proceed. It consults the ledger
// first, so a foreign file can never be replaced or removed even when the
// filesystem state happens to match the plan.
func (r *applyRun) decide(
	ledger Ledger, artifact harnessrender.Artifact, observed ObservedState, item *preflightIntent,
) Reason {
	if artifact.After.Marker != nil && artifact.After.Marker.Owner != r.options.MarkerOwner {
		return ReasonUnowned
	}
	decision := ledger.Authorize(IntentKind(artifact.Kind), artifact.Destination, artifact.ObjectID)

	switch artifact.Kind {
	case harnessplan.OperationCreate:
		if observed.Type != harnessplan.FileAbsent {
			// An idempotent re-apply is the one case where an occupied
			// destination is not a collision: the ledger must already own it and
			// the on-disk state must already equal the planned after state.
			if decision.Found && decision.Entry.Status == LedgerOwned &&
				sameState(observed, artifact.After) {
				item.status = StepSkippedIdempotent
				return ReasonNone
			}
			return ReasonCollision
		}
		if !decision.Authorized {
			return decision.Reason
		}
		return ReasonNone

	case harnessplan.OperationReplaceOwned:
		if !decision.Authorized {
			return decision.Reason
		}
		if sameState(observed, artifact.After) {
			item.status = StepSkippedIdempotent
			return ReasonNone
		}
		if !sameState(observed, artifact.Before) {
			return ReasonPreconditionMismatch
		}
		if !sameInstalledState(decision.Entry.Installed, observed) {
			return ReasonPreconditionMismatch
		}
		return ReasonNone

	case harnessplan.OperationRemoveTombstone:
		if !decision.Authorized {
			return decision.Reason
		}
		if observed.Type == harnessplan.FileAbsent {
			item.status = StepSkippedIdempotent
			return ReasonNone
		}
		if !sameState(observed, artifact.Before) {
			return ReasonPreconditionMismatch
		}
		if !sameInstalledState(decision.Entry.Installed, observed) {
			return ReasonPreconditionMismatch
		}
		return ReasonNone

	default:
		return ReasonUnsupportedFileType
	}
}

// finishRefused writes the fail-closed evidence for an apply that mutated
// nothing: an unauthorized journal and a refused receipt.
func (r *applyRun) finishRefused(prepared []preflightIntent) (Receipt, error) {
	journal := Journal{
		SchemaVersion:    SchemaVersion,
		JournalID:        r.journalID,
		PlanSHA256:       r.options.Result.PlanSHA256,
		ManifestDigest:   r.options.Result.ManifestDigest,
		InventoryDigest:  r.options.Result.InventoryDigest,
		TargetRepository: r.options.Result.TargetRepository,
		MarkerOwner:      r.options.MarkerOwner,
		OpenedAt:         r.openedAt,
		Authorized:       false,
		Intents:          nil,
	}
	if err := r.writeJournal(journal); err != nil {
		return Receipt{}, err
	}
	receipt := r.buildReceipt(prepared, OutcomeRefused, nil)
	if err := r.writeReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, refusalError(prepared)
}

func (r *applyRun) mutate(prepared []preflightIntent, ledger Ledger, ledgerGeneration int) (Receipt, error) {
	intents := make([]Intent, 0, len(prepared))
	for _, item := range prepared {
		intents = append(intents, item.intent)
	}
	journal := Journal{
		SchemaVersion:    SchemaVersion,
		JournalID:        r.journalID,
		PlanSHA256:       r.options.Result.PlanSHA256,
		ManifestDigest:   r.options.Result.ManifestDigest,
		InventoryDigest:  r.options.Result.InventoryDigest,
		TargetRepository: r.options.Result.TargetRepository,
		MarkerOwner:      r.options.MarkerOwner,
		OpenedAt:         r.openedAt,
		Authorized:       true,
		Intents:          intents,
	}
	if err := r.writeJournal(journal); err != nil {
		return Receipt{}, err
	}
	hook(phaseAfterJournalCommit, -1)

	retained := make(map[int]string, len(prepared))
	nextLedger := ledger
	var firstFailure error

	for index := range prepared {
		item := &prepared[index]
		if item.status == StepSkippedIdempotent {
			step := r.buildStep(*item, item.intent.Observed, nil)
			if err := r.writeStep(step); err != nil {
				return Receipt{}, err
			}
			hook(phaseAfterStepCommit, index)
			continue
		}

		hook(phaseBeforeTransition, index)
		installed, quarantine, err := r.transition(*item)
		hook(phaseAfterTransition, index)
		if err != nil {
			item.status = StepFailed
			item.reason = classifyTransitionError(err)
			firstFailure = err
			step := r.buildStep(*item, ObservedState{}, quarantine)
			if writeErr := r.writeStep(step); writeErr != nil {
				return Receipt{}, writeErr
			}
			for later := index + 1; later < len(prepared); later++ {
				prepared[later].status = StepNotAttempted
				prepared[later].reason = ReasonEarlierIntentStopped
			}
			break
		}

		if quarantine != nil {
			retained[index] = quarantine.QuarantinePath
		}
		step := r.buildStep(*item, installed, quarantine)
		if err := r.writeStep(step); err != nil {
			return Receipt{}, err
		}
		hook(phaseAfterStepCommit, index)

		nextLedger, err = r.recordOwnership(nextLedger, *item, quarantine)
		if err != nil {
			return Receipt{}, err
		}
	}

	hook(phaseBeforeLedgerCommit, -1)
	if err := r.store.AppendLedger(nextLedger, ledgerGeneration); err != nil {
		return Receipt{}, err
	}

	outcome := OutcomeApplied
	if firstFailure != nil {
		outcome = OutcomePartial
		if !anyProgress(prepared) {
			// A transition was attempted, so this is not a refusal: refusal
			// asserts the target was never touched and that claim would be
			// unprovable here.
			outcome = OutcomeFailed
		}
	}
	receipt := r.buildReceipt(prepared, outcome, retained)
	hook(phaseBeforeReceiptCommit, -1)
	if err := r.writeReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, firstFailure
}

// transition performs exactly one planned mutation and reads the result back
// before returning. A returned quarantine names an object the substrate
// retained; nothing is ever unlinked.
func (r *applyRun) transition(item preflightIntent) (ObservedState, *Retained, error) {
	artifact := item.artifact
	stageLeaf, err := newStageLeaf()
	if err != nil {
		return ObservedState{}, nil, err
	}

	switch artifact.Kind {
	case harnessplan.OperationRemoveTombstone:
		hold, err := r.target.Detach(artifact.Destination, stageLeaf)
		if err != nil {
			return ObservedState{}, nil, err
		}
		defer hold.Close()
		quarantine, err := hold.Remove()
		if err != nil {
			return ObservedState{}, nil, err
		}
		record := quarantine.Record()
		quarantine.Close()
		if err := r.target.SyncParent(artifact.Destination); err != nil {
			return ObservedState{}, nil, err
		}
		observed, supported, err := r.observe(artifact.Destination)
		if err != nil {
			return ObservedState{}, nil, err
		}
		if !supported || observed.Type != harnessplan.FileAbsent {
			return ObservedState{}, nil, fmt.Errorf("%w: destination still present after removal", ErrIncompleteApply)
		}
		return observed, retainedFrom(record, item.intent.Observed), nil

	case harnessplan.OperationCreate, harnessplan.OperationReplaceOwned:
		replace := artifact.Kind == harnessplan.OperationReplaceOwned
		var record harnessfs.QuarantineRecord
		var haveRecord bool

		switch artifact.After.Type {
		case harnessplan.FileRegular:
			stage, err := r.target.CreateStage(artifact.Destination, stageLeaf)
			if err != nil {
				return ObservedState{}, nil, err
			}
			defer stage.Close()
			if _, err := stage.Write(artifact.RegularBytes); err != nil {
				return ObservedState{}, nil, err
			}
			mode, err := parseMode(artifact.After.Mode)
			if err != nil {
				return ObservedState{}, nil, err
			}
			if err := stage.Chmod(mode); err != nil {
				return ObservedState{}, nil, err
			}
			if err := stage.Sync(); err != nil {
				return ObservedState{}, nil, err
			}
			if replace {
				if err := stage.Exchange(); err != nil {
					return ObservedState{}, nil, err
				}
				quarantine, err := stage.Remove()
				if err != nil {
					return ObservedState{}, nil, err
				}
				record = quarantine.Record()
				haveRecord = true
				quarantine.Close()
			} else if err := stage.CommitCreate(); err != nil {
				return ObservedState{}, nil, err
			}

		case harnessplan.FileRelativeSymlink:
			stage, err := r.target.CreateSymlinkStage(artifact.Destination, stageLeaf, artifact.After.SymlinkTarget)
			if err != nil {
				return ObservedState{}, nil, err
			}
			defer stage.Close()
			if replace {
				if err := stage.Exchange(); err != nil {
					return ObservedState{}, nil, err
				}
				quarantine, err := stage.Remove()
				if err != nil {
					return ObservedState{}, nil, err
				}
				record = quarantine.Record()
				haveRecord = true
				quarantine.Close()
			} else if err := stage.CommitCreate(); err != nil {
				return ObservedState{}, nil, err
			}

		default:
			return ObservedState{}, nil, fmt.Errorf("%w: unsupported after state", ErrInvalidJournal)
		}

		if err := r.target.SyncParent(artifact.Destination); err != nil {
			return ObservedState{}, nil, err
		}
		observed, supported, err := r.observe(artifact.Destination)
		if err != nil {
			return ObservedState{}, nil, err
		}
		if !supported || !sameState(observed, artifact.After) {
			return ObservedState{}, nil, fmt.Errorf("%w: read-back does not match the planned after state", ErrIncompleteApply)
		}
		if haveRecord {
			return observed, retainedFrom(record, item.intent.Observed), nil
		}
		return observed, nil, nil

	default:
		return ObservedState{}, nil, fmt.Errorf("%w: unsupported operation kind", ErrInvalidJournal)
	}
}

func (r *applyRun) recordOwnership(ledger Ledger, item preflightIntent, quarantine *Retained) (Ledger, error) {
	now := r.options.Now().UTC().Round(0)
	switch item.artifact.Kind {
	case harnessplan.OperationCreate, harnessplan.OperationReplaceOwned:
		installed, err := InstalledFrom(item.artifact.After, r.options.Result.PlanSHA256, r.journalID, now)
		if err != nil {
			return Ledger{}, err
		}
		return ledger.RecordInstall(item.artifact.Destination, item.artifact.ObjectID, installed)
	case harnessplan.OperationRemoveTombstone:
		tombstone := Tombstone{
			Reason:      r.options.TombstoneReason,
			PlanSHA256:  r.options.Result.PlanSHA256,
			JournalID:   r.journalID,
			RemovedHash: item.intent.Observed.Hash,
			RecordedAt:  now,
		}
		if quarantine != nil {
			tombstone.QuarantinePath = quarantine.QuarantinePath
		}
		return ledger.RecordTombstone(item.artifact.Destination, item.artifact.ObjectID, tombstone)
	default:
		return Ledger{}, fmt.Errorf("%w: unsupported operation kind", ErrInvalidJournal)
	}
}

// observe reports the no-follow state of a destination. The boolean is false
// when the destination holds a type outside the plan language (a directory or a
// special file); such a destination is refused, never mutated.
// parentPresent reports whether the destination's containing directory exists.
func (r *applyRun) parentPresent(destination string) (bool, error) {
	parent := path.Dir(destination)
	if parent == "." {
		return true, nil
	}
	entry, err := r.target.Lstat(parent)
	if err != nil {
		if errors.Is(err, harnessfs.ErrNotExist) {
			return false, nil
		}
		// openat2 reports an unresolvable intermediate component without a
		// distinct classification; treat any parent that will not resolve as
		// absent rather than failing the whole apply without evidence.
		return false, nil
	}
	return entry.Kind == harnessfs.KindDirectory, nil
}

func (r *applyRun) observe(destination string) (ObservedState, bool, error) {
	entry, err := r.target.Lstat(destination)
	if err != nil {
		if errors.Is(err, harnessfs.ErrNotExist) {
			return ObservedState{Type: harnessplan.FileAbsent}, true, nil
		}
		return ObservedState{}, false, err
	}
	switch entry.Kind {
	case harnessfs.KindRegular:
		content, readEntry, err := r.target.ReadRegular(destination, harnessplan.MaxPayloadBytes)
		if err != nil {
			return ObservedState{}, false, err
		}
		return ObservedState{
			Type:   harnessplan.FileRegular,
			Mode:   formatMode(readEntry.Mode),
			Hash:   hashBytes(content),
			Device: readEntry.Device,
			Inode:  readEntry.Inode,
			Links:  readEntry.Links,
		}, true, nil
	case harnessfs.KindSymlink:
		linkTarget, err := os.Readlink(filepath.Join(r.options.TargetPath, filepath.FromSlash(destination)))
		if err != nil {
			return ObservedState{}, false, err
		}
		return ObservedState{
			Type:          harnessplan.FileRelativeSymlink,
			Hash:          hashBytes([]byte(linkTarget)),
			SymlinkTarget: linkTarget,
			Device:        entry.Device,
			Inode:         entry.Inode,
			Links:         entry.Links,
		}, true, nil
	default:
		return ObservedState{Type: harnessplan.FileAbsent}, false, nil
	}
}

func (r *applyRun) writeJournal(journal Journal) error {
	data, err := MarshalJournal(journal)
	if err != nil {
		return err
	}
	return r.store.writeNew(journalLeaf(r.generation), data)
}

func (r *applyRun) writeStep(step Step) error {
	data, err := MarshalStep(step)
	if err != nil {
		return err
	}
	return r.store.writeNew(stepLeaf(r.generation, step.Sequence), data)
}

func (r *applyRun) writeReceipt(receipt Receipt) error {
	data, err := MarshalReceipt(receipt)
	if err != nil {
		return err
	}
	return r.store.writeNew(receiptLeaf(r.generation), data)
}

func (r *applyRun) buildStep(item preflightIntent, installed ObservedState, retained *Retained) Step {
	if installed.Type == "" {
		installed = ObservedState{Type: harnessplan.FileAbsent}
	}
	return Step{
		SchemaVersion: SchemaVersion,
		JournalID:     r.journalID,
		Sequence:      item.intent.Sequence,
		OperationID:   item.intent.OperationID,
		Destination:   item.intent.Destination,
		Status:        item.status,
		Reason:        item.reason,
		Observed:      item.intent.Observed,
		Installed:     installed,
		Retained:      retained,
		CompletedAt:   r.options.Now().UTC().Round(0),
	}
}

func (r *applyRun) buildReceipt(prepared []preflightIntent, outcome Outcome, retained map[int]string) Receipt {
	results := make([]ReceiptResult, 0, len(prepared))
	for index, item := range prepared {
		result := ReceiptResult{
			Sequence:    index,
			OperationID: item.intent.OperationID,
			ObjectID:    item.intent.ObjectID,
			Kind:        item.intent.Kind,
			Destination: item.intent.Destination,
			Status:      item.status,
			Reason:      item.reason,
			BeforeHash:  item.intent.Observed.Hash,
		}
		if item.status == StepApplied || item.status == StepSkippedIdempotent {
			result.AfterHash = item.artifact.After.Hash
		}
		if retained != nil {
			result.Retained = retained[index]
		}
		results = append(results, result)
	}
	return Receipt{
		SchemaVersion:    SchemaVersion,
		JournalID:        r.journalID,
		PlanSHA256:       r.options.Result.PlanSHA256,
		ManifestDigest:   r.options.Result.ManifestDigest,
		InventoryDigest:  r.options.Result.InventoryDigest,
		TargetRepository: r.options.Result.TargetRepository,
		MarkerOwner:      r.options.MarkerOwner,
		Outcome:          outcome,
		OpenedAt:         r.openedAt,
		ClosedAt:         r.options.Now().UTC().Round(0),
		Results:          results,
	}
}

func anyProgress(prepared []preflightIntent) bool {
	for _, item := range prepared {
		if item.status == StepApplied || item.status == StepSkippedIdempotent {
			return true
		}
	}
	return false
}

func refusalError(prepared []preflightIntent) error {
	for _, item := range prepared {
		switch item.reason {
		case ReasonUnowned, ReasonTombstoned:
			return fmt.Errorf("%w: %q", ErrUnowned, item.intent.Destination)
		case ReasonCollision:
			return fmt.Errorf("%w: %q", ErrCollision, item.intent.Destination)
		case ReasonPreconditionMismatch:
			return fmt.Errorf("%w: %q", ErrPreconditionMismatch, item.intent.Destination)
		}
	}
	for _, item := range prepared {
		if item.status == StepRefused {
			return fmt.Errorf("%w: %q (%s)", ErrPreconditionMismatch, item.intent.Destination, item.reason)
		}
	}
	return nil
}

func classifyTransitionError(err error) Reason {
	var recovery *harnessfs.RecoveryError
	switch {
	case errors.As(err, &recovery):
		return ReasonRetainedForRecovery
	case errors.Is(err, harnessfs.ErrCollision):
		return ReasonCollision
	case errors.Is(err, harnessfs.ErrStaleHandle):
		return ReasonPreconditionMismatch
	case errors.Is(err, ErrIncompleteApply):
		return ReasonReadBackMismatch
	default:
		return ReasonSubstrateError
	}
}

// retainedFrom binds the substrate's descriptor-verified quarantine identity to
// the pre-image this engine observed before the transition. The observation
// supplies the content digest; the record supplies the inode identity a later
// recovery must re-verify.
func retainedFrom(record harnessfs.QuarantineRecord, preImage ObservedState) *Retained {
	state := preImage
	state.Device = record.Entry.Device
	state.Inode = record.Entry.Inode
	state.Links = record.Entry.Links
	return &Retained{QuarantinePath: record.Path, State: state}
}

func sameState(observed ObservedState, planned harnessplan.FileState) bool {
	if observed.Type != planned.Type {
		return false
	}
	switch planned.Type {
	case harnessplan.FileAbsent:
		return true
	case harnessplan.FileRegular:
		return observed.Hash == planned.Hash && observed.Mode == planned.Mode
	case harnessplan.FileRelativeSymlink:
		return observed.SymlinkTarget == planned.SymlinkTarget && observed.Hash == planned.Hash
	default:
		return false
	}
}

func sameInstalledState(installed *InstalledState, observed ObservedState) bool {
	if installed == nil {
		return false
	}
	if installed.Type != observed.Type || installed.Hash != observed.Hash {
		return false
	}
	switch installed.Type {
	case harnessplan.FileRegular:
		return installed.Mode == observed.Mode
	case harnessplan.FileRelativeSymlink:
		return installed.SymlinkTarget == observed.SymlinkTarget
	default:
		return false
	}
}

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func formatMode(mode uint32) string {
	return fmt.Sprintf("0%03o", mode&0o777)
}

func parseMode(mode string) (uint32, error) {
	parsed, err := strconv.ParseUint(mode, 8, 12)
	if err != nil || parsed > 0o777 {
		return 0, fmt.Errorf("%w: mode %q is not a plain permission mode", ErrInvalidJournal, mode)
	}
	return uint32(parsed), nil
}
