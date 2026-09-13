//go:build linux

package harnesslifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
)

// Uninstall removes all currently owned destinations to quarantine, records
// tombstones in the ownership ledger, and emits an applied receipt.
func Uninstall(options UninstallOptions) (Receipt, error) {
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

	run := &uninstallRun{
		options:  options,
		store:    store,
		target:   target,
		openedAt: options.Now().UTC().Round(0),
	}
	return run.execute()
}

type uninstallRun struct {
	options  UninstallOptions
	store    *Store
	target   *harnessfs.Root
	openedAt time.Time
}

func (r *uninstallRun) execute() (Receipt, error) {
	ledger, ledgerGeneration, err := r.store.LoadCurrentLedger(
		r.options.TargetRepository, r.options.MarkerOwner, r.options.Now)
	if err != nil {
		return Receipt{}, err
	}

	var ownedEntries []LedgerEntry
	for _, entry := range ledger.Entries {
		if entry.Status == LedgerOwned {
			ownedEntries = append(ownedEntries, entry)
		}
	}

	now := r.options.Now().UTC().Round(0)
	if len(ownedEntries) == 0 {
		return Receipt{
			SchemaVersion:    SchemaVersion,
			TargetRepository: r.options.TargetRepository,
			MarkerOwner:      r.options.MarkerOwner,
			Outcome:          OutcomeApplied,
			OpenedAt:         r.openedAt,
			ClosedAt:         now,
			Results:          []ReceiptResult{},
		}, nil
	}

	generation, err := r.store.nextGeneration(journalLeaf)
	if err != nil {
		return Receipt{}, err
	}
	journalID, err := NewJournalID()
	if err != nil {
		return Receipt{}, err
	}

	intents := make([]Intent, 0, len(ownedEntries))
	for index, entry := range ownedEntries {
		observed, _, err := r.observe(entry.Destination)
		if err != nil {
			return Receipt{}, err
		}
		expected := harnessplan.FileState{Type: harnessplan.FileAbsent}
		if entry.Installed != nil {
			expected = harnessplan.FileState{
				Type:          entry.Installed.Type,
				Mode:          entry.Installed.Mode,
				Hash:          entry.Installed.Hash,
				SymlinkTarget: entry.Installed.SymlinkTarget,
			}
		}
		intents = append(intents, Intent{
			Sequence:     index,
			OperationID:  entry.ObjectID,
			ObjectID:     entry.ObjectID,
			Kind:         IntentRemoveTombstone,
			Destination:  entry.Destination,
			Observed:     observed,
			Expected:     expected,
			After:        harnessplan.FileState{Type: harnessplan.FileAbsent},
			LedgerStatus: LedgerOwned,
		})
	}

	uninstallDigest := hashBytes([]byte("harness-uninstall:" + journalID))
	journal := Journal{
		SchemaVersion:    SchemaVersion,
		JournalID:        journalID,
		PlanSHA256:       uninstallDigest,
		ManifestDigest:   uninstallDigest,
		InventoryDigest:  uninstallDigest,
		TargetRepository: r.options.TargetRepository,
		MarkerOwner:      r.options.MarkerOwner,
		OpenedAt:         r.openedAt,
		Authorized:       true,
		Intents:          intents,
	}

	journalData, err := MarshalJournal(journal)
	if err != nil {
		return Receipt{}, err
	}
	if err := r.store.writeNew(journalLeaf(generation), journalData); err != nil {
		return Receipt{}, err
	}

	nextLedger := ledger
	results := make([]ReceiptResult, 0, len(intents))

	for _, intent := range intents {
		res := ReceiptResult{
			Sequence:    intent.Sequence,
			OperationID: intent.OperationID,
			ObjectID:    intent.ObjectID,
			Kind:        intent.Kind,
			Destination: intent.Destination,
			BeforeHash:  intent.Observed.Hash,
		}

		observed, supported, err := r.observe(intent.Destination)
		if err != nil {
			return Receipt{}, err
		}

		if !supported || observed.Type == harnessplan.FileAbsent {
			step := Step{
				SchemaVersion: SchemaVersion,
				JournalID:     journalID,
				Sequence:      intent.Sequence,
				OperationID:   intent.OperationID,
				Destination:   intent.Destination,
				Status:        StepSkippedIdempotent,
				Observed:      observed,
				Installed:     ObservedState{Type: harnessplan.FileAbsent},
				CompletedAt:   now,
			}
			stepData, err := MarshalStep(step)
			if err != nil {
				return Receipt{}, err
			}
			if err := r.store.writeNew(stepLeaf(generation, intent.Sequence), stepData); err != nil {
				return Receipt{}, err
			}

			tombstone := Tombstone{
				Reason:     TombstoneUninstall,
				PlanSHA256: uninstallDigest,
				JournalID:  journalID,
				RecordedAt: now,
			}
			nextLedger, err = nextLedger.RecordTombstone(intent.Destination, intent.ObjectID, tombstone)
			if err != nil {
				return Receipt{}, err
			}
			res.Status = StepSkippedIdempotent
			results = append(results, res)
			continue
		}

		stageLeaf, err := newStageLeaf()
		if err != nil {
			return Receipt{}, err
		}
		hold, err := r.target.Detach(intent.Destination, stageLeaf)
		if err != nil {
			return Receipt{}, err
		}
		quarantine, err := hold.Remove()
		if err != nil {
			hold.Close()
			return Receipt{}, err
		}
		record := quarantine.Record()
		quarantine.Close()
		if err := r.target.SyncParent(intent.Destination); err != nil {
			return Receipt{}, err
		}

		retained := retainedFrom(record, observed)
		step := Step{
			SchemaVersion: SchemaVersion,
			JournalID:     journalID,
			Sequence:      intent.Sequence,
			OperationID:   intent.OperationID,
			Destination:   intent.Destination,
			Status:        StepApplied,
			Observed:      observed,
			Installed:     ObservedState{Type: harnessplan.FileAbsent},
			Retained:      retained,
			CompletedAt:   now,
		}
		stepData, err := MarshalStep(step)
		if err != nil {
			return Receipt{}, err
		}
		if err := r.store.writeNew(stepLeaf(generation, intent.Sequence), stepData); err != nil {
			return Receipt{}, err
		}

		tombstone := Tombstone{
			Reason:         TombstoneUninstall,
			PlanSHA256:     uninstallDigest,
			JournalID:      journalID,
			RemovedHash:    observed.Hash,
			QuarantinePath: record.Path,
			RecordedAt:     now,
		}
		nextLedger, err = nextLedger.RecordTombstone(intent.Destination, intent.ObjectID, tombstone)
		if err != nil {
			return Receipt{}, err
		}

		res.Status = StepApplied
		res.Retained = record.Path
		results = append(results, res)
	}

	if err := r.store.AppendLedger(nextLedger, ledgerGeneration); err != nil {
		return Receipt{}, err
	}

	receipt := Receipt{
		SchemaVersion:    SchemaVersion,
		JournalID:        journalID,
		PlanSHA256:       uninstallDigest,
		ManifestDigest:   uninstallDigest,
		InventoryDigest:  uninstallDigest,
		TargetRepository: r.options.TargetRepository,
		MarkerOwner:      r.options.MarkerOwner,
		Outcome:          OutcomeApplied,
		OpenedAt:         r.openedAt,
		ClosedAt:         now,
		Results:          results,
	}

	receiptData, err := MarshalReceipt(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if err := r.store.writeNew(receiptLeaf(generation), receiptData); err != nil {
		return Receipt{}, err
	}

	return receipt, nil
}

func (r *uninstallRun) observe(destination string) (ObservedState, bool, error) {
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
