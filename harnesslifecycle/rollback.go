//go:build linux || darwin

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

// Rollback reverses the committed transitions of an apply generation using
// retained quarantine objects and records tombstones for created files.
func Rollback(options RollbackOptions) (Receipt, error) {
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

	run := &rollbackRun{
		options:  options,
		store:    store,
		target:   target,
		openedAt: options.Now().UTC().Round(0),
	}
	return run.execute()
}

type rollbackRun struct {
	options  RollbackOptions
	store    *Store
	target   *harnessfs.Root
	openedAt time.Time
}

func (r *rollbackRun) execute() (Receipt, error) {
	targetGen := r.options.Generation
	if targetGen < 0 {
		nextGen, err := r.store.nextGeneration(journalLeaf)
		if err != nil {
			return Receipt{}, err
		}
		if nextGen == 0 {
			return Receipt{
				SchemaVersion:    SchemaVersion,
				Outcome:          OutcomeApplied,
				TargetRepository: r.options.TargetRepository,
				MarkerOwner:      r.options.MarkerOwner,
				OpenedAt:         r.openedAt,
				ClosedAt:         r.options.Now().UTC().Round(0),
				Results:          []ReceiptResult{},
			}, nil
		}
		targetGen = nextGen - 1
	}

	data, err := r.store.readDocument(journalLeaf(targetGen))
	if err != nil {
		return Receipt{}, fmt.Errorf("read journal for generation %d: %w", targetGen, err)
	}
	journal, err := LoadJournal(data)
	if err != nil {
		return Receipt{}, fmt.Errorf("load journal for generation %d: %w", targetGen, err)
	}
	if !journal.Authorized {
		return Receipt{}, fmt.Errorf("%w: generation %d was not authorized", ErrInvalidJournal, targetGen)
	}

	targetRepo := journal.TargetRepository
	if r.options.TargetRepository != "" {
		targetRepo = r.options.TargetRepository
	}
	markerOwner := journal.MarkerOwner
	if r.options.MarkerOwner != "" {
		markerOwner = r.options.MarkerOwner
	}

	ledger, ledgerGeneration, err := r.store.LoadCurrentLedger(targetRepo, markerOwner, r.options.Now)
	if err != nil {
		return Receipt{}, err
	}

	type stepRecord struct {
		intent Intent
		step   Step
	}

	var committed []stepRecord
	for _, intent := range journal.Intents {
		stepPath := stepLeaf(targetGen, intent.Sequence)
		present, err := r.store.exists(stepPath)
		if err != nil {
			return Receipt{}, err
		}
		if !present {
			continue
		}
		stepData, err := r.store.readDocument(stepPath)
		if err != nil {
			return Receipt{}, err
		}
		step, err := LoadStep(stepData)
		if err != nil {
			return Receipt{}, err
		}
		if step.Status == StepApplied || step.Status == StepSkippedIdempotent {
			committed = append(committed, stepRecord{intent: intent, step: step})
		}
	}

	results := make([]ReceiptResult, 0, len(committed))
	nextLedger := ledger
	now := r.options.Now().UTC().Round(0)

	// Rollback in reverse order
	for i := len(committed) - 1; i >= 0; i-- {
		item := committed[i]
		res := ReceiptResult{
			Sequence:    item.step.Sequence,
			OperationID: item.step.OperationID,
			ObjectID:    item.intent.ObjectID,
			Kind:        item.intent.Kind,
			Destination: item.step.Destination,
			Status:      StepApplied,
		}

		switch item.intent.Kind {
		case IntentCreate:
			observed, supported, err := r.observe(item.step.Destination)
			if err != nil {
				return Receipt{}, err
			}
			if supported && observed.Type != harnessplan.FileAbsent {
				stageLeaf, err := newStageLeaf()
				if err != nil {
					return Receipt{}, err
				}
				hold, err := r.target.Detach(item.step.Destination, stageLeaf)
				if err != nil {
					return Receipt{}, err
				}
				quarantine, err := hold.Remove()
				if err != nil {
					hold.Close()
					return Receipt{}, err
				}
				rec := quarantine.Record()
				quarantine.Close()
				if err := r.target.SyncParent(item.step.Destination); err != nil {
					return Receipt{}, err
				}
				tombstone := Tombstone{
					Reason:         TombstoneSuperseded,
					PlanSHA256:     journal.PlanSHA256,
					JournalID:      journal.JournalID,
					RemovedHash:    observed.Hash,
					QuarantinePath: rec.Path,
					RecordedAt:     now,
				}
				nextLedger, err = nextLedger.RecordTombstone(item.step.Destination, item.intent.ObjectID, tombstone)
				if err != nil {
					return Receipt{}, err
				}
				res.Retained = rec.Path
				res.BeforeHash = observed.Hash
			}

		case IntentReplaceOwned:
			if item.step.Retained != nil && item.step.Retained.QuarantinePath != "" {
				observed, supported, err := r.observe(item.step.Destination)
				if err != nil {
					return Receipt{}, err
				}
				if supported && observed.Type != harnessplan.FileAbsent {
					stageLeaf, err := newStageLeaf()
					if err != nil {
						return Receipt{}, err
					}
					hold, err := r.target.Detach(item.step.Destination, stageLeaf)
					if err != nil {
						return Receipt{}, err
					}
					currQuarantine, err := hold.Remove()
					if err != nil {
						hold.Close()
						return Receipt{}, err
					}
					currQuarantine.Close()
				}

				mode, err := parseMode(item.step.Retained.State.Mode)
				if err != nil && item.step.Retained.State.Type == harnessplan.FileRegular {
					return Receipt{}, err
				}
				qRec := harnessfs.QuarantineRecord{
					Path: item.step.Retained.QuarantinePath,
					Entry: harnessfs.Entry{
						Kind:   kindFromType(item.step.Retained.State.Type),
						Mode:   mode,
						Device: item.step.Retained.State.Device,
						Inode:  item.step.Retained.State.Inode,
						Links:  item.step.Retained.State.Links,
					},
					LinkTarget: item.step.Retained.State.SymlinkTarget,
				}
				quarantine, err := r.target.OpenQuarantine(qRec)
				if err != nil {
					return Receipt{}, fmt.Errorf("open quarantine for rollback of %s: %w", item.step.Destination, err)
				}
				if err := quarantine.Restore(item.step.Destination); err != nil {
					quarantine.Close()
					return Receipt{}, fmt.Errorf("restore quarantine for rollback of %s: %w", item.step.Destination, err)
				}
				quarantine.Close()
				if err := r.target.SyncParent(item.step.Destination); err != nil {
					return Receipt{}, err
				}

				installed := InstalledState{
					Type:          item.step.Retained.State.Type,
					Mode:          item.step.Retained.State.Mode,
					Hash:          item.step.Retained.State.Hash,
					SymlinkTarget: item.step.Retained.State.SymlinkTarget,
					PlanSHA256:    journal.PlanSHA256,
					JournalID:     journal.JournalID,
					InstalledAt:   now,
				}
				nextLedger, err = nextLedger.RecordInstall(item.step.Destination, item.intent.ObjectID, installed)
				if err != nil {
					return Receipt{}, err
				}
				res.AfterHash = item.step.Retained.State.Hash
			}

		case IntentRemoveTombstone:
			if item.step.Retained != nil && item.step.Retained.QuarantinePath != "" {
				mode, err := parseMode(item.step.Retained.State.Mode)
				if err != nil && item.step.Retained.State.Type == harnessplan.FileRegular {
					return Receipt{}, err
				}
				qRec := harnessfs.QuarantineRecord{
					Path: item.step.Retained.QuarantinePath,
					Entry: harnessfs.Entry{
						Kind:   kindFromType(item.step.Retained.State.Type),
						Mode:   mode,
						Device: item.step.Retained.State.Device,
						Inode:  item.step.Retained.State.Inode,
						Links:  item.step.Retained.State.Links,
					},
					LinkTarget: item.step.Retained.State.SymlinkTarget,
				}
				quarantine, err := r.target.OpenQuarantine(qRec)
				if err != nil {
					return Receipt{}, fmt.Errorf("open quarantine for rollback of %s: %w", item.step.Destination, err)
				}
				if err := quarantine.Restore(item.step.Destination); err != nil {
					quarantine.Close()
					return Receipt{}, fmt.Errorf("restore quarantine for rollback of %s: %w", item.step.Destination, err)
				}
				quarantine.Close()
				if err := r.target.SyncParent(item.step.Destination); err != nil {
					return Receipt{}, err
				}

				installed := InstalledState{
					Type:          item.step.Retained.State.Type,
					Mode:          item.step.Retained.State.Mode,
					Hash:          item.step.Retained.State.Hash,
					SymlinkTarget: item.step.Retained.State.SymlinkTarget,
					PlanSHA256:    journal.PlanSHA256,
					JournalID:     journal.JournalID,
					InstalledAt:   now,
				}
				nextLedger, err = nextLedger.RecordInstall(item.step.Destination, item.intent.ObjectID, installed)
				if err != nil {
					return Receipt{}, err
				}
				res.AfterHash = item.step.Retained.State.Hash
			}
		}
		results = append(results, res)
	}

	if err := r.store.AppendLedger(nextLedger, ledgerGeneration); err != nil {
		return Receipt{}, err
	}

	receipt := Receipt{
		SchemaVersion:    SchemaVersion,
		JournalID:        journal.JournalID,
		PlanSHA256:       journal.PlanSHA256,
		ManifestDigest:   journal.ManifestDigest,
		InventoryDigest:  journal.InventoryDigest,
		TargetRepository: targetRepo,
		MarkerOwner:      markerOwner,
		Outcome:          OutcomeApplied,
		OpenedAt:         r.openedAt,
		ClosedAt:         now,
		Results:          results,
	}
	return receipt, nil
}

func (r *rollbackRun) observe(destination string) (ObservedState, bool, error) {
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

func kindFromType(t harnessplan.FileType) harnessfs.EntryKind {
	switch t {
	case harnessplan.FileRegular:
		return harnessfs.KindRegular
	case harnessplan.FileRelativeSymlink:
		return harnessfs.KindSymlink
	default:
		return 0
	}
}
