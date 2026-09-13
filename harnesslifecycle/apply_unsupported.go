//go:build !linux

package harnesslifecycle

import (
	"fmt"
	"runtime"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessrender"
)

// ApplyOptions is declared on every platform so callers compile everywhere,
// but only Linux provides the descriptor-relative containment the apply engine
// requires. Elsewhere every entry point fails closed.
type ApplyOptions struct {
	StatePath       string
	TargetPath      string
	Result          harnessrender.Result
	MarkerOwner     string
	TombstoneReason TombstoneReason
	Now             func() time.Time
}

// Store is unavailable without the containment substrate.
type Store struct{}

func unsupported(capability string) error {
	return fmt.Errorf("%w: %s requires openat2/renameat2 containment (GOOS=%s)",
		ErrUnsupported, capability, runtime.GOOS)
}

// Apply fails closed on platforms without the containment substrate.
func Apply(ApplyOptions) (Receipt, error) { return Receipt{}, unsupported("apply") }

// Inspect fails closed on platforms without the containment substrate.
func Inspect(string) ([]Incomplete, error) { return nil, unsupported("inspect") }

// Recover fails closed on platforms without the containment substrate.
func Recover(RecoverOptions) (Receipt, error) { return Receipt{}, unsupported("recover") }

// Rollback fails closed on platforms without the containment substrate.
func Rollback(RollbackOptions) (Receipt, error) { return Receipt{}, unsupported("rollback") }

// Uninstall fails closed on platforms without the containment substrate.
func Uninstall(UninstallOptions) (Receipt, error) { return Receipt{}, unsupported("uninstall") }

// OpenStore fails closed on platforms without the containment substrate.
func OpenStore(string) (*Store, error) { return nil, unsupported("state store") }

// CreateStore fails closed on platforms without the containment substrate.
func CreateStore(string) (*Store, error) { return nil, unsupported("state store") }

// NewJournalID fails closed on platforms without the containment substrate so
// no caller can mint an identifier for an apply that can never run.
func NewJournalID() (string, error) { return "", unsupported("journal identity") }
