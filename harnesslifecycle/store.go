//go:build linux || darwin

package harnesslifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
)

const (
	// stateRootMode is the operator-private mode required of the lifecycle
	// store. Journals, receipts, and ledgers are never world- or group-readable.
	stateRootMode os.FileMode = 0o700
	stateFileMode uint32      = 0o600

	// maxGenerations bounds the append-only apply and ledger series so a probe
	// over a corrupted store terminates.
	maxGenerations = 1 << 20
)

// Store is the append-only, operator-private lifecycle state directory.
//
// Every document is created with RENAME_NOREPLACE and never replaced, so the
// store needs no delete primitive and two concurrent applies cannot claim the
// same generation. The layout is deliberately flat: the substrate cannot create
// directories, so every write is a leaf operation beneath one anchored root.
//
//	ledger-<generation>.json          ownership ledger, newest generation wins
//	apply-<generation>.journal.json   pre-mutation journal, also the apply claim
//	apply-<generation>.step-<n>.json  one terminal record per committed intent
//	apply-<generation>.receipt.json   sanitized terminal record
type Store struct {
	root *harnessfs.Root
	path string
}

// OpenStore anchors an existing operator-private state directory. The
// directory must already exist with mode 0700; this package never widens
// permissions it did not create.
func OpenStore(statePath string) (*Store, error) {
	if !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath {
		return nil, fmt.Errorf("%w: state root must be an absolute clean path", ErrInvalidLedger)
	}
	info, err := os.Lstat(statePath)
	if err != nil {
		return nil, fmt.Errorf("%w: state root: %v", ErrInvalidLedger, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: state root is not a directory", ErrInvalidLedger)
	}
	if info.Mode().Perm() != stateRootMode.Perm() {
		return nil, fmt.Errorf("%w: state root must be mode %04o, found %04o",
			ErrInvalidLedger, stateRootMode.Perm(), info.Mode().Perm())
	}
	root, err := harnessfs.Open(statePath)
	if err != nil {
		return nil, fmt.Errorf("%w: state root: %v", ErrInvalidLedger, err)
	}
	return &Store{root: root, path: statePath}, nil
}

// CreateStore creates the operator-private state directory when absent and
// anchors it.
func CreateStore(statePath string) (*Store, error) {
	if !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath {
		return nil, fmt.Errorf("%w: state root must be an absolute clean path", ErrInvalidLedger)
	}
	if err := createStateDirectory(statePath, stateRootMode); err != nil {
		return nil, fmt.Errorf("%w: state root: %v", ErrInvalidLedger, err)
	}
	return OpenStore(statePath)
}

// Close releases the anchor.
func (s *Store) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

// Path returns the anchored state directory. It is a host-absolute path and is
// therefore never written into a receipt.
func (s *Store) Path() string { return s.path }

func ledgerLeaf(generation int) string  { return fmt.Sprintf("ledger-%012d.json", generation) }
func journalLeaf(generation int) string { return fmt.Sprintf("apply-%012d.journal.json", generation) }
func receiptLeaf(generation int) string { return fmt.Sprintf("apply-%012d.receipt.json", generation) }
func stepLeaf(generation, sequence int) string {
	return fmt.Sprintf("apply-%012d.step-%04d.json", generation, sequence)
}

// nextGeneration finds the first absent generation in an append-only series
// without listing the directory: exponential probe to bracket the end, then
// binary search inside the bracket.
func (s *Store) nextGeneration(leaf func(int) string) (int, error) {
	if _, err := s.stat(leaf(0)); err != nil {
		if errors.Is(err, harnessfs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	low, high := 0, 1
	for {
		if high >= maxGenerations {
			return 0, fmt.Errorf("%w: state store exceeded %d generations", ErrInvalidLedger, maxGenerations)
		}
		if _, err := s.stat(leaf(high)); err != nil {
			if errors.Is(err, harnessfs.ErrNotExist) {
				break
			}
			return 0, err
		}
		low = high
		high *= 2
	}
	// low exists, high does not.
	for high-low > 1 {
		middle := low + (high-low)/2
		if _, err := s.stat(leaf(middle)); err != nil {
			if errors.Is(err, harnessfs.ErrNotExist) {
				high = middle
				continue
			}
			return 0, err
		}
		low = middle
	}
	return high, nil
}

func (s *Store) stat(leaf string) (harnessfs.Entry, error) {
	entry, err := s.root.Lstat(leaf)
	if err != nil {
		return harnessfs.Entry{}, err
	}
	if entry.Kind != harnessfs.KindRegular {
		return harnessfs.Entry{}, fmt.Errorf("%w: %q is not a regular file", ErrInvalidLedger, leaf)
	}
	return entry, nil
}

func (s *Store) exists(leaf string) (bool, error) {
	if _, err := s.stat(leaf); err != nil {
		if errors.Is(err, harnessfs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// writeNew atomically creates a document that must not already exist. A
// collision is returned verbatim so a caller racing for a generation fails
// closed instead of overwriting another apply's evidence.
func (s *Store) writeNew(leaf string, data []byte) error {
	stageLeaf, err := newStageLeaf()
	if err != nil {
		return err
	}
	stage, err := s.root.CreateStage(leaf, stageLeaf)
	if err != nil {
		return err
	}
	defer stage.Close()

	if _, err := stage.Write(data); err != nil {
		return err
	}
	if err := stage.Chmod(stateFileMode); err != nil {
		return err
	}
	if err := stage.Sync(); err != nil {
		return err
	}
	if err := stage.CommitCreate(); err != nil {
		return err
	}
	return s.root.SyncParent(leaf)
}

func (s *Store) readDocument(leaf string) ([]byte, error) {
	data, _, err := s.root.ReadRegular(leaf, MaxDocumentBytes)
	return data, err
}

// LoadCurrentLedger returns the newest ledger generation, or a fresh empty
// ledger when the store has none.
func (s *Store) LoadCurrentLedger(targetRepository, markerOwner string, now func() time.Time) (Ledger, int, error) {
	next, err := s.nextGeneration(ledgerLeaf)
	if err != nil {
		return Ledger{}, 0, err
	}
	if next == 0 {
		ledger, err := NewLedger(targetRepository, markerOwner, now())
		if err != nil {
			return Ledger{}, 0, err
		}
		return ledger, 0, nil
	}
	data, err := s.readDocument(ledgerLeaf(next - 1))
	if err != nil {
		return Ledger{}, 0, err
	}
	ledger, err := LoadLedger(data)
	if err != nil {
		return Ledger{}, 0, err
	}
	if ledger.TargetRepository != targetRepository || ledger.MarkerOwner != markerOwner {
		return Ledger{}, 0, fmt.Errorf("%w: stored ledger belongs to %q/%q",
			ErrInvalidLedger, ledger.TargetRepository, ledger.MarkerOwner)
	}
	return ledger, next, nil
}

// AppendLedger writes the next ledger generation.
func (s *Store) AppendLedger(ledger Ledger, generation int) error {
	data, err := MarshalLedger(ledger)
	if err != nil {
		return err
	}
	return s.writeNew(ledgerLeaf(generation), data)
}

func newStageLeaf() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("%w: stage entropy: %v", ErrInvalidJournal, err)
	}
	return ".codex-harness-stage-" + hex.EncodeToString(entropy[:]), nil
}

// NewJournalID returns an opaque identifier carrying no host information.
func NewJournalID() (string, error) {
	var entropy [JournalIDBytes]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("%w: journal entropy: %v", ErrInvalidJournal, err)
	}
	return hex.EncodeToString(entropy[:]), nil
}
