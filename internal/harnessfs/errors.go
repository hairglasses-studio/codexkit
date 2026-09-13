package harnessfs

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupported  = errors.New("harness filesystem capability is unsupported")
	ErrInvalidPath  = errors.New("invalid portable relative path")
	ErrUnsafeRoot   = errors.New("unsafe harness filesystem root")
	ErrNotExist     = errors.New("filesystem object does not exist")
	ErrCollision    = errors.New("filesystem object already exists")
	ErrNotRegular   = errors.New("filesystem object is not a regular file")
	ErrNotArtifact  = errors.New("filesystem object is not a supported artifact")
	ErrHardlink     = errors.New("filesystem artifact has multiple hard links")
	ErrTooLarge     = errors.New("regular file exceeds read limit")
	ErrClosed       = errors.New("filesystem handle is closed")
	ErrStaleHandle  = errors.New("filesystem object no longer matches its handle")
	ErrInvalidState = errors.New("filesystem handle is in an invalid state")
)

// CapabilityError reports a platform that cannot provide the containment
// guarantees required for write-capable harness installation.
type CapabilityError struct {
	GOOS       string
	Capability string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("harnessfs: %s is unsupported on %s", e.Capability, e.GOOS)
}

func (e *CapabilityError) Is(target error) bool { return target == ErrUnsupported }

// OpError adds the failed primitive and portable path without discarding the
// stable error classification in Err.
type OpError struct {
	Op   string
	Path string
	Err  error
}

func (e *OpError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("harnessfs %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("harnessfs %s %q: %v", e.Op, e.Path, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

func opError(op, path string, err error) error {
	return &OpError{Op: op, Path: path, Err: err}
}

// RecoveryEvidence describes a retained object after an operation that could
// not safely complete. A non-empty Path is portable and relative to the Root.
// DescriptorPinned reports that the handle still retains inode authority even
// when neither candidate name could be rebound to it. IdentityKnown means Path
// was reverified against Entry through that descriptor.
type RecoveryEvidence struct {
	Path             string
	Entry            Entry
	DescriptorPinned bool
	IdentityKnown    bool
}

// RecoveryError is returned instead of deleting an object whose identity or
// lifecycle could not be proven. Evidence is suitable for an installer journal
// and contains no host-absolute path.
type RecoveryError struct {
	Op       string
	Evidence RecoveryEvidence
	// Related records additional retained objects when one atomic transition
	// leaves more than one descriptor-verified artifact reachable.
	Related []RecoveryEvidence
	Err     error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("harnessfs %s retained %q for recovery: %v", e.Op, e.Evidence.Path, e.Err)
}

func (e *RecoveryError) Unwrap() error { return e.Err }
