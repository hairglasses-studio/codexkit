//go:build linux || darwin

package harnesslifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/harnessrender"
	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
)

// Recover replays an incomplete apply generation to terminal completion,
// writing missing step records, persisting the updated ledger, and emitting
// the sanitized receipt.
func Recover(options RecoverOptions) (Receipt, error) {
	if err := harnessfs.Capability(); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if options.Now == nil {
		return Receipt{}, fmt.Errorf("%w: a clock is required", ErrInvalidJournal)
	}
	if !filepath.IsAbs(options.TargetPath) || filepath.Clean(options.TargetPath) != options.TargetPath {
		return Receipt{}, fmt.Errorf("%w: target root must be an absolute clean path", ErrInvalidJournal)
	}

	store, err := OpenStore(options.StatePath)
	if err != nil {
		return Receipt{}, err
	}
	defer store.Close()

	target, err := harnessfs.Open(options.TargetPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: target root: %v", ErrInvalidJournal, err)
	}
	defer target.Close()

	run := &recoverRun{
		options:  options,
		store:    store,
		target:   target,
		openedAt: options.Now().UTC().Round(0),
	}
	return run.execute()
}

type recoverRun struct {
	options  RecoverOptions
	store    *Store
	target   *harnessfs.Root
	openedAt time.Time
}

func (r *recoverRun) execute() (Receipt, error) {
	targetGen := r.options.Generation
	if targetGen < 0 {
		generations, err := r.store.nextGeneration(journalLeaf)
		if err != nil {
			return Receipt{}, err
		}
		found := -1
		for gen := generations - 1; gen >= 0; gen-- {
			receiptPresent, err := r.store.exists(receiptLeaf(gen))
			if err != nil {
				return Receipt{}, err
			}
			if !receiptPresent {
				found = gen
				break
			}
		}
		if found < 0 {
			return Receipt{
				SchemaVersion:    SchemaVersion,
				Outcome:          OutcomeApplied,
				TargetRepository: r.options.Result.TargetRepository,
				MarkerOwner:      r.options.MarkerOwner,
				OpenedAt:         r.openedAt,
				ClosedAt:         r.options.Now().UTC().Round(0),
				Results:          []ReceiptResult{},
			}, nil
		}
		targetGen = found
	}

	journalData, err := r.store.readDocument(journalLeaf(targetGen))
	if err != nil {
		return Receipt{}, fmt.Errorf("read journal for generation %d: %w", targetGen, err)
	}
	journal, err := LoadJournal(journalData)
	if err != nil {
		return Receipt{}, fmt.Errorf("load journal for generation %d: %w", targetGen, err)
	}
	if !journal.Authorized {
		return Receipt{}, fmt.Errorf("%w: generation %d was not authorized", ErrInvalidJournal, targetGen)
	}

	targetRepo := journal.TargetRepository
	if r.options.Result.TargetRepository != "" {
		targetRepo = r.options.Result.TargetRepository
	}
	markerOwner := journal.MarkerOwner
	if r.options.MarkerOwner != "" {
		markerOwner = r.options.MarkerOwner
	}

	ledger, ledgerGeneration, err := r.store.LoadCurrentLedger(targetRepo, markerOwner, r.options.Now)
	if err != nil {
		return Receipt{}, err
	}

	artifactsByDest := make(map[string]harnessrender.Artifact, len(r.options.Result.Artifacts))
	for _, a := range r.options.Result.Artifacts {
		artifactsByDest[a.Destination] = a
	}

	retained := make(map[int]string)
	prepared := make([]preflightIntent, 0, len(journal.Intents))
	nextLedger := ledger
	now := r.options.Now().UTC().Round(0)
	var firstFailure error

	for index, intent := range journal.Intents {
		stepPath := stepLeaf(targetGen, intent.Sequence)
		present, err := r.store.exists(stepPath)
		if err != nil {
			return Receipt{}, err
		}

		artifact, hasArtifact := artifactsByDest[intent.Destination]
		if !hasArtifact {
			artifact = harnessrender.Artifact{
				OperationID: intent.OperationID,
				ObjectID:    intent.ObjectID,
				Kind:        harnessplan.OperationKind(intent.Kind),
				Destination: intent.Destination,
				Before:      intent.Expected,
				After:       intent.After,
			}
		}

		item := preflightIntent{
			intent:   intent,
			artifact: artifact,
			status:   StepApplied,
		}

		if present {
			stepData, err := r.store.readDocument(stepPath)
			if err != nil {
				return Receipt{}, err
			}
			step, err := LoadStep(stepData)
			if err != nil {
				return Receipt{}, err
			}
			item.status = step.Status
			item.reason = step.Reason
			if step.Retained != nil {
				retained[index] = step.Retained.QuarantinePath
			}
			prepared = append(prepared, item)
			continue
		}

		// Step is missing. Check if destination already has planned after state.
		observed, supported, err := r.observe(intent.Destination)
		if err != nil {
			return Receipt{}, err
		}

		if supported && sameState(observed, intent.After) {
			item.status = StepApplied
			step := Step{
				SchemaVersion: SchemaVersion,
				JournalID:     journal.JournalID,
				Sequence:      intent.Sequence,
				OperationID:   intent.OperationID,
				Destination:   intent.Destination,
				Status:        StepApplied,
				Observed:      intent.Observed,
				Installed:     observed,
				CompletedAt:   now,
			}
			if err := r.writeStep(targetGen, step); err != nil {
				return Receipt{}, err
			}
			nextLedger, err = r.recordOwnership(nextLedger, item, nil, journal.PlanSHA256, journal.JournalID, now)
			if err != nil {
				return Receipt{}, err
			}
			prepared = append(prepared, item)
			continue
		}

		// Perform the transition now
		installed, quarantine, err := r.transition(item)
		if err != nil {
			item.status = StepFailed
			item.reason = classifyTransitionError(err)
			firstFailure = err
			step := Step{
				SchemaVersion: SchemaVersion,
				JournalID:     journal.JournalID,
				Sequence:      intent.Sequence,
				OperationID:   intent.OperationID,
				Destination:   intent.Destination,
				Status:        StepFailed,
				Reason:        item.reason,
				Observed:      intent.Observed,
				Installed:     ObservedState{Type: harnessplan.FileAbsent},
				Retained:      quarantine,
				CompletedAt:   now,
			}
			if writeErr := r.writeStep(targetGen, step); writeErr != nil {
				return Receipt{}, writeErr
			}
			prepared = append(prepared, item)
			for later := index + 1; later < len(journal.Intents); later++ {
				laterIntent := journal.Intents[later]
				notAttempted := preflightIntent{
					intent:   laterIntent,
					artifact: artifactsByDest[laterIntent.Destination],
					status:   StepNotAttempted,
					reason:   ReasonEarlierIntentStopped,
				}
				stepNotAttempted := Step{
					SchemaVersion: SchemaVersion,
					JournalID:     journal.JournalID,
					Sequence:      laterIntent.Sequence,
					OperationID:   laterIntent.OperationID,
					Destination:   laterIntent.Destination,
					Status:        StepNotAttempted,
					Reason:        ReasonEarlierIntentStopped,
					Observed:      laterIntent.Observed,
					Installed:     ObservedState{Type: harnessplan.FileAbsent},
					CompletedAt:   now,
				}
				_ = r.writeStep(targetGen, stepNotAttempted)
				prepared = append(prepared, notAttempted)
			}
			break
		}

		if quarantine != nil {
			retained[index] = quarantine.QuarantinePath
		}
		step := Step{
			SchemaVersion: SchemaVersion,
			JournalID:     journal.JournalID,
			Sequence:      intent.Sequence,
			OperationID:   intent.OperationID,
			Destination:   intent.Destination,
			Status:        StepApplied,
			Observed:      intent.Observed,
			Installed:     installed,
			Retained:      quarantine,
			CompletedAt:   now,
		}
		if err := r.writeStep(targetGen, step); err != nil {
			return Receipt{}, err
		}
		nextLedger, err = r.recordOwnership(nextLedger, item, quarantine, journal.PlanSHA256, journal.JournalID, now)
		if err != nil {
			return Receipt{}, err
		}
		prepared = append(prepared, item)
	}

	if err := r.store.AppendLedger(nextLedger, ledgerGeneration); err != nil {
		return Receipt{}, err
	}

	outcome := OutcomeApplied
	if firstFailure != nil {
		outcome = OutcomePartial
		if !anyProgress(prepared) {
			outcome = OutcomeFailed
		}
	}

	results := make([]ReceiptResult, 0, len(prepared))
	for index, item := range prepared {
		res := ReceiptResult{
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
			res.AfterHash = item.artifact.After.Hash
		}
		if p, ok := retained[index]; ok {
			res.Retained = p
		}
		results = append(results, res)
	}

	receipt := Receipt{
		SchemaVersion:    SchemaVersion,
		JournalID:        journal.JournalID,
		PlanSHA256:       journal.PlanSHA256,
		ManifestDigest:   journal.ManifestDigest,
		InventoryDigest:  journal.InventoryDigest,
		TargetRepository: targetRepo,
		MarkerOwner:      markerOwner,
		Outcome:          outcome,
		OpenedAt:         journal.OpenedAt,
		ClosedAt:         now,
		Results:          results,
	}

	receiptData, err := MarshalReceipt(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if err := r.store.writeNew(receiptLeaf(targetGen), receiptData); err != nil {
		return Receipt{}, err
	}

	return receipt, firstFailure
}

func (r *recoverRun) writeStep(generation int, step Step) error {
	data, err := MarshalStep(step)
	if err != nil {
		return err
	}
	return r.store.writeNew(stepLeaf(generation, step.Sequence), data)
}

func (r *recoverRun) transition(item preflightIntent) (ObservedState, *Retained, error) {
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
			return ObservedState{}, nil, fmt.Errorf("%w: read-back does not match planned after state", ErrIncompleteApply)
		}
		if haveRecord {
			return observed, retainedFrom(record, item.intent.Observed), nil
		}
		return observed, nil, nil

	default:
		return ObservedState{}, nil, fmt.Errorf("%w: unsupported operation kind", ErrInvalidJournal)
	}
}

func (r *recoverRun) recordOwnership(ledger Ledger, item preflightIntent, quarantine *Retained, planSHA256, journalID string, now time.Time) (Ledger, error) {
	switch item.artifact.Kind {
	case harnessplan.OperationCreate, harnessplan.OperationReplaceOwned:
		installed, err := InstalledFrom(item.artifact.After, planSHA256, journalID, now)
		if err != nil {
			return Ledger{}, err
		}
		return ledger.RecordInstall(item.artifact.Destination, item.artifact.ObjectID, installed)
	case harnessplan.OperationRemoveTombstone:
		tombstone := Tombstone{
			Reason:      r.options.TombstoneReason,
			PlanSHA256:  planSHA256,
			JournalID:   journalID,
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

func (r *recoverRun) observe(destination string) (ObservedState, bool, error) {
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
