//go:build linux

package harnesslifecycle

import (
	"fmt"
)

// Inspect reports every apply generation whose on-disk evidence does not prove
// a terminal outcome. It is detection only and never repairs, replays, or
// rolls anything back: recovery is the next lifecycle slice.
//
// A generation is incomplete when its journal exists and its receipt does not,
// or when an authorized journal's step set is shorter than its intent set.
// Both conditions are exactly what a process killed mid-apply leaves behind,
// because the journal is committed before the first mutation and the receipt
// is committed after the last one.
func Inspect(statePath string) ([]Incomplete, error) {
	store, err := OpenStore(statePath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.Incomplete()
}

// Incomplete returns the incomplete apply generations in this store.
func (s *Store) Incomplete() ([]Incomplete, error) {
	generations, err := s.nextGeneration(journalLeaf)
	if err != nil {
		return nil, err
	}

	findings := make([]Incomplete, 0)
	for generation := 0; generation < generations; generation++ {
		data, err := s.readDocument(journalLeaf(generation))
		if err != nil {
			return nil, err
		}
		journal, err := LoadJournal(data)
		if err != nil {
			return nil, fmt.Errorf("apply generation %d: %w", generation, err)
		}

		receiptPresent, err := s.exists(receiptLeaf(generation))
		if err != nil {
			return nil, err
		}

		missing := make([]int, 0)
		destinations := make([]string, 0, len(journal.Intents))
		for _, intent := range journal.Intents {
			destinations = append(destinations, intent.Destination)
			present, err := s.exists(stepLeaf(generation, intent.Sequence))
			if err != nil {
				return nil, err
			}
			if !present {
				missing = append(missing, intent.Sequence)
			}
		}

		if receiptPresent && len(missing) == 0 {
			continue
		}
		findings = append(findings, Incomplete{
			JournalID:      journal.JournalID,
			PlanSHA256:     journal.PlanSHA256,
			IntentCount:    len(journal.Intents),
			StepCount:      len(journal.Intents) - len(missing),
			ReceiptPresent: receiptPresent,
			MissingSteps:   missing,
			Destinations:   destinations,
		})
	}
	return findings, nil
}
