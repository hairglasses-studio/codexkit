package harnesslifecycle

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
)

// NewLedger returns a valid empty ownership ledger.
func NewLedger(targetRepository, markerOwner string, updatedAt time.Time) (Ledger, error) {
	ledger := Ledger{
		SchemaVersion:    SchemaVersion,
		TargetRepository: targetRepository,
		MarkerOwner:      markerOwner,
		UpdatedAt:        updatedAt.UTC().Round(0),
		Entries:          []LedgerEntry{},
	}
	if err := ValidateLedger(ledger); err != nil {
		return Ledger{}, err
	}
	return ledger, nil
}

// Lookup returns the entry for a destination. Tombstoned entries are returned
// too: callers must consult Status, because a tombstoned row is proof of past
// ownership and explicitly not authority to mutate.
func (l Ledger) Lookup(destination string) (LedgerEntry, bool) {
	index, found := l.indexOf(destination)
	if !found {
		return LedgerEntry{}, false
	}
	return l.Entries[index], true
}

// Owns reports whether the ledger currently owns a destination.
func (l Ledger) Owns(destination string) bool {
	entry, found := l.Lookup(destination)
	return found && entry.Status == LedgerOwned
}

// Ownership is the closed classification of a destination for one intent.
type Ownership struct {
	Authorized bool
	Reason     Reason
	Entry      LedgerEntry
	Found      bool
}

// Authorize decides whether one intent kind may proceed against a destination
// given only ledger state. It is the no-unowned-delete gate: replacement and
// removal require a currently owned entry, and a tombstoned row never revives
// implicitly.
func (l Ledger) Authorize(kind IntentKind, destination, objectID string) Ownership {
	entry, found := l.Lookup(destination)
	decision := Ownership{Entry: entry, Found: found}

	switch kind {
	case IntentCreate:
		if found && entry.Status == LedgerOwned {
			// A create over an owned destination is a plan error, not a
			// collision with a foreign file. Report it as a collision so the
			// receipt says what the filesystem will also say.
			decision.Reason = ReasonCollision
			return decision
		}
		if found && entry.ObjectID != objectID {
			decision.Reason = ReasonUnowned
			return decision
		}
		decision.Authorized = true
		return decision
	case IntentReplaceOwned, IntentRemoveTombstone:
		if !found {
			decision.Reason = ReasonUnowned
			return decision
		}
		if entry.Status == LedgerTombstoned {
			decision.Reason = ReasonTombstoned
			return decision
		}
		if entry.ObjectID != objectID {
			decision.Reason = ReasonUnowned
			return decision
		}
		decision.Authorized = true
		return decision
	default:
		decision.Reason = ReasonUnowned
		return decision
	}
}

// RecordInstall returns a ledger in which destination is owned with the given
// installed state. Reinstalling a tombstoned destination revives the row and
// retains its tombstone history; rows are never dropped.
func (l Ledger) RecordInstall(destination, objectID string, state InstalledState) (Ledger, error) {
	if err := ValidateLedger(l); err != nil {
		return Ledger{}, err
	}
	next := l.clone()
	index, found := next.indexOf(destination)
	if found {
		if next.Entries[index].ObjectID != objectID {
			return Ledger{}, fmt.Errorf("%w: destination %q is recorded for object %q",
				ErrUnowned, destination, next.Entries[index].ObjectID)
		}
		next.Entries[index].Status = LedgerOwned
		installed := state
		next.Entries[index].Installed = &installed
	} else {
		installed := state
		next.Entries = append(next.Entries, LedgerEntry{
			Destination: destination,
			ObjectID:    objectID,
			Owner:       l.MarkerOwner,
			Status:      LedgerOwned,
			Installed:   &installed,
		})
		sort.Slice(next.Entries, func(a, b int) bool {
			return next.Entries[a].Destination < next.Entries[b].Destination
		})
	}
	next.UpdatedAt = state.InstalledAt.UTC().Round(0)
	if err := ValidateLedger(next); err != nil {
		return Ledger{}, err
	}
	return next, nil
}

// RecordTombstone is the only removal operation. It refuses a destination the
// ledger does not currently own, appends to the destination's removal history,
// and keeps the row forever.
func (l Ledger) RecordTombstone(destination, objectID string, tombstone Tombstone) (Ledger, error) {
	if err := ValidateLedger(l); err != nil {
		return Ledger{}, err
	}
	decision := l.Authorize(IntentRemoveTombstone, destination, objectID)
	if !decision.Authorized {
		return Ledger{}, fmt.Errorf("%w: destination %q (%s)", ErrUnowned, destination, decision.Reason)
	}

	next := l.clone()
	index, _ := next.indexOf(destination)
	entry := &next.Entries[index]
	if len(entry.Tombstones) >= MaxTombstonesPerEntry {
		return Ledger{}, fmt.Errorf("%w: destination %q exceeded its retained removal history",
			ErrInvalidLedger, destination)
	}
	normalized := tombstone
	normalized.RecordedAt = normalized.RecordedAt.UTC().Round(0)
	entry.Status = LedgerTombstoned
	entry.Installed = nil
	entry.Tombstones = append(entry.Tombstones, normalized)
	next.UpdatedAt = normalized.RecordedAt

	if err := ValidateLedger(next); err != nil {
		return Ledger{}, err
	}
	return next, nil
}

// OwnedDestinations returns the currently owned destinations in canonical
// order. It is the input an uninstall may consider — never a directory walk.
func (l Ledger) OwnedDestinations() []string {
	owned := make([]string, 0, len(l.Entries))
	for _, entry := range l.Entries {
		if entry.Status == LedgerOwned {
			owned = append(owned, entry.Destination)
		}
	}
	return owned
}

// RetainedObjects returns every quarantine path the ledger's tombstone history
// still references, in canonical order. Nothing here has been unlinked.
func (l Ledger) RetainedObjects() []string {
	retained := make([]string, 0)
	for _, entry := range l.Entries {
		for _, tombstone := range entry.Tombstones {
			if tombstone.QuarantinePath != "" {
				retained = append(retained, tombstone.QuarantinePath)
			}
		}
	}
	sort.Strings(retained)
	return slices.Compact(retained)
}

func (l Ledger) indexOf(destination string) (int, bool) {
	for index, entry := range l.Entries {
		if entry.Destination == destination {
			return index, true
		}
	}
	return 0, false
}

func (l Ledger) clone() Ledger {
	next := l
	next.Entries = make([]LedgerEntry, len(l.Entries))
	for index, entry := range l.Entries {
		next.Entries[index] = entry
		if entry.Installed != nil {
			installed := *entry.Installed
			next.Entries[index].Installed = &installed
		}
		next.Entries[index].Tombstones = slices.Clone(entry.Tombstones)
	}
	return next
}

// InstalledFrom projects a planned after state into the ledger's installed
// shape. It exists so the engine cannot record an installed state the plan
// language could not express.
func InstalledFrom(state harnessplan.FileState, planSHA256, journalID string, installedAt time.Time) (InstalledState, error) {
	switch state.Type {
	case harnessplan.FileRegular, harnessplan.FileRelativeSymlink:
	default:
		return InstalledState{}, fmt.Errorf("%w: only materialized states are installable", ErrInvalidLedger)
	}
	return InstalledState{
		Type:          state.Type,
		Mode:          state.Mode,
		Hash:          state.Hash,
		SymlinkTarget: state.SymlinkTarget,
		PlanSHA256:    planSHA256,
		JournalID:     journalID,
		InstalledAt:   installedAt.UTC().Round(0),
	}, nil
}
