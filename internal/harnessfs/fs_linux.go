//go:build linux

package harnessfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const maxReadLimit int64 = 256 << 20

// Root is an anchored, no-symlink directory. Its descriptor is never exposed.
type Root struct {
	mu     sync.RWMutex
	fd     int
	closed bool

	// cleanupTestHook is nil outside same-package regression tests. It makes
	// both historical namespace race windows deterministic under test.
	cleanupTestHook func(phase cleanupHookPhase, parentFD int, sourceLeaf, quarantineLeaf string)
	// transitionTestHook is nil outside same-package regression tests. It
	// deterministically opens the transition windows that must be closed by
	// postcondition and referent revalidation.
	transitionTestHook func(phase transitionHookPhase)
}

type cleanupHookPhase uint8

const (
	cleanupBeforeQuarantineRename cleanupHookPhase = iota
	cleanupAfterQuarantineVerify
)

type transitionHookPhase uint8

const (
	transitionBeforeSymlinkCommitRename transitionHookPhase = iota
	transitionAfterRegularCommitRename
	transitionAfterSymlinkCommitRename
	transitionBeforeSymlinkExchange
	transitionAfterExchangeRename
	transitionBeforeExchangeRollback
	transitionAfterExchangeRollback
	transitionBeforeQuarantineOpen
	transitionBeforeQuarantineRestore
	transitionAfterQuarantineRestoreRename
	transitionBeforeQuarantineRestoreRollback
	transitionAfterQuarantineRestoreRollback
)

type stageState uint8

const (
	stagePresent stageState = iota
	stageCommitted
	stageDisplaced
	stageDetached
	stageQuarantined
	// stageRetained means a transition left the pinned inode reachable but a
	// postcondition or rollback failed. The typed stage keeps descriptor
	// authority so the caller can journal the RecoveryError before releasing it.
	stageRetained
)

// Stage is the retention authority for one exclusively-created stage name.
// Its fields are deliberately private so callers cannot forge a namespace
// mutation target.
type Stage struct {
	root       *Root
	parent     string
	name       string
	target     string
	mu         sync.Mutex
	fd         int
	guardFD    int
	fileClosed bool
	state      stageState
	expected   Entry
	linkTarget string
	recovery   []retainedPin
}

// SymlinkStage is a descriptor-pinned, exclusively-created relative symlink.
// It remains distinct from the writable regular-file Stage.
type SymlinkStage struct {
	root       *Root
	parent     string
	name       string
	target     string
	mu         sync.Mutex
	guardFD    int
	closed     bool
	state      stageState
	expected   Entry
	linkTarget string
	recovery   []retainedPin
}

// Hold is the transfer authority for one object detached with NOREPLACE.
type Hold struct {
	root        *Root
	parent      string
	name        string
	mu          sync.Mutex
	transferred bool
	guardFD     int
	expected    Entry
	linkTarget  string
	recovery    []retainedPin
}

// Quarantine retains a descriptor-pinned regular file or contained relative
// symlink under a random, journalable name. Closing never removes the object.
type Quarantine struct {
	root       *Root
	parent     string
	name       string
	mu         sync.Mutex
	guardFD    int
	restored   bool
	expected   Entry
	linkTarget string
	recovery   []retainedPin
}

// retainedPin keeps descriptor authority for an additional artifact observed
// during an ambiguous transition. name is set only after a descriptor-to-name
// identity check; an empty name means the inode remains pinned but neither
// candidate namespace entry could be proven to name it.
type retainedPin struct {
	guardFD    int
	name       string
	expected   Entry
	linkTarget string
}

type exchangeRecoveryError struct {
	recovery   *RecoveryError
	sourceName string
	pins       []retainedPin
}

func (e *exchangeRecoveryError) Error() string { return e.recovery.Error() }
func (e *exchangeRecoveryError) Unwrap() error { return e.recovery }

// Capability reports whether the current build has the Linux containment
// substrate.
func Capability() error { return nil }

// Open anchors an absolute, clean, non-root directory path without following
// symlinks in any component.
func Open(rootPath string) (*Root, error) {
	if rootPath == "" || !filepath.IsAbs(rootPath) || rootPath == "/" || filepath.Clean(rootPath) != rootPath {
		return nil, opError("open-root", rootPath, ErrUnsafeRoot)
	}
	slashFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, opError("open-root-anchor", rootPath, fmt.Errorf("%w: %v", ErrUnsafeRoot, err))
	}
	defer unix.Close(slashFD)

	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS),
	}
	fd, err := unix.Openat2(slashFD, strings.TrimPrefix(rootPath, "/"), how)
	if err != nil {
		return nil, opError("open-root", rootPath, fmt.Errorf("%w: %v", ErrUnsafeRoot, err))
	}
	if err := validateDirectoryFD(fd); err != nil {
		unix.Close(fd)
		return nil, opError("open-root", rootPath, err)
	}
	return &Root{fd: fd}, nil
}

// Close releases the anchor. Already-open leaf descriptors remain valid, but
// no new path operation can begin.
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	err := unix.Close(r.fd)
	r.fd = -1
	if err != nil {
		return opError("close-root", "", err)
	}
	return nil
}

func (r *Root) openParent(parent string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return -1, ErrClosed
	}
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS),
	}
	fd, err := unix.Openat2(r.fd, parent, how)
	if err != nil {
		return -1, err
	}
	if err := validateDirectoryFD(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func validateDirectoryFD(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeRoot, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrUnsafeRoot
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: not owned by the effective user", ErrUnsafeRoot)
	}
	// A group/world-writable parent allows another principal to substitute a
	// leaf between descriptor-relative syscalls. Refuse it rather than claim a
	// stronger adversarial guarantee than pathname mutation can provide.
	if stat.Mode&0o022 != 0 {
		return fmt.Errorf("%w: writable by group or world", ErrUnsafeRoot)
	}
	return nil
}

func entryFromStat(stat *unix.Stat_t) Entry {
	kind := KindUnknown
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		kind = KindRegular
	case unix.S_IFDIR:
		kind = KindDirectory
	case unix.S_IFLNK:
		kind = KindSymlink
	case unix.S_IFIFO:
		kind = KindFIFO
	case unix.S_IFSOCK:
		kind = KindSocket
	case unix.S_IFBLK, unix.S_IFCHR:
		kind = KindDevice
	}
	return Entry{
		Kind:   kind,
		Mode:   stat.Mode & 0o7777,
		Size:   stat.Size,
		Device: uint64(stat.Dev),
		Inode:  stat.Ino,
		Links:  uint64(stat.Nlink),
	}
}

func lstatAt(parentFD int, leaf string) (Entry, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, leaf, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return Entry{}, ErrNotExist
		}
		return Entry{}, err
	}
	return entryFromStat(&stat), nil
}

func openRegularGuard(parentFD int, leaf string) (int, Entry, error) {
	fd, entry, linkTarget, err := openArtifactGuard(parentFD, leaf, leaf)
	if err != nil {
		return -1, entry, err
	}
	if entry.Kind != KindRegular || linkTarget != "" {
		unix.Close(fd)
		return -1, entry, ErrNotRegular
	}
	return fd, entry, nil
}

func openArtifactGuard(parentFD int, leaf, destinationPath string) (int, Entry, string, error) {
	fd, err := unix.Openat(parentFD, leaf, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			err = ErrNotExist
		}
		return -1, Entry{}, "", err
	}
	entry, linkTarget, err := artifactFromGuard(fd, destinationPath)
	if err != nil {
		unix.Close(fd)
		return -1, entry, linkTarget, err
	}
	return fd, entry, linkTarget, nil
}

func artifactFromGuard(fd int, destinationPath string) (Entry, string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return Entry{}, "", err
	}
	entry := entryFromStat(&stat)
	if entry.Kind != KindRegular && entry.Kind != KindSymlink {
		return entry, "", ErrNotArtifact
	}
	if entry.Links != 1 {
		return entry, "", ErrHardlink
	}
	if entry.Kind == KindRegular {
		return entry, "", nil
	}
	buffer := make([]byte, maxSymlinkTargetLength+1)
	n, err := unix.Readlinkat(fd, "", buffer)
	if err != nil {
		return entry, "", err
	}
	if n == len(buffer) {
		return entry, "", ErrInvalidPath
	}
	linkTarget := string(buffer[:n])
	if err := ValidateRelativeSymlinkTarget(destinationPath, linkTarget); err != nil {
		return entry, linkTarget, err
	}
	return entry, linkTarget, nil
}

// Lstat returns leaf metadata without following it.
func (r *Root) Lstat(relativePath string) (Entry, error) {
	parent, leaf, err := splitPortablePath(relativePath)
	if err != nil {
		return Entry{}, err
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return Entry{}, opError("lstat-parent", relativePath, err)
	}
	defer unix.Close(parentFD)
	entry, err := lstatAt(parentFD, leaf)
	if err != nil {
		return Entry{}, opError("lstat", relativePath, err)
	}
	return entry, nil
}

// ReadRegular opens a leaf with O_NOFOLLOW and verifies the opened descriptor
// is regular before reading at most maxBytes.
func (r *Root) ReadRegular(relativePath string, maxBytes int64) ([]byte, Entry, error) {
	if maxBytes <= 0 || maxBytes > maxReadLimit {
		return nil, Entry{}, opError("read-limit", relativePath, ErrInvalidState)
	}
	parent, leaf, err := splitPortablePath(relativePath)
	if err != nil {
		return nil, Entry{}, err
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return nil, Entry{}, opError("read-parent", relativePath, err)
	}
	defer unix.Close(parentFD)
	leafEntry, err := lstatAt(parentFD, leaf)
	if err != nil {
		return nil, Entry{}, opError("lstat-regular", relativePath, err)
	}
	if leafEntry.Kind != KindRegular {
		return nil, leafEntry, opError("read-regular", relativePath, ErrNotRegular)
	}

	fd, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			err = ErrNotExist
		} else if errors.Is(err, unix.ELOOP) {
			err = ErrNotRegular
		}
		return nil, Entry{}, opError("open-regular", relativePath, err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, Entry{}, opError("fstat-regular", relativePath, err)
	}
	entry := entryFromStat(&stat)
	if entry.Kind != KindRegular {
		return nil, entry, opError("read-regular", relativePath, ErrNotRegular)
	}
	if entry.Links != 1 {
		return nil, entry, opError("read-regular", relativePath, ErrHardlink)
	}
	content, err := readBoundedFD(fd, maxBytes)
	if err != nil {
		return nil, entry, opError("read-regular", relativePath, err)
	}
	return content, entry, nil
}

func readBoundedFD(fd int, maxBytes int64) ([]byte, error) {
	buffer := make([]byte, 32*1024)
	content := make([]byte, 0)
	for {
		remaining := maxBytes + 1 - int64(len(content))
		if remaining <= 0 {
			return nil, ErrTooLarge
		}
		chunk := buffer
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		n, err := unix.Read(fd, chunk)
		if n > 0 {
			content = append(content, chunk[:n]...)
			if int64(len(content)) > maxBytes {
				return nil, ErrTooLarge
			}
		}
		if err == nil && n == 0 {
			return content, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

// CreateStage exclusively creates stageLeaf beside targetPath with mode 0600.
func (r *Root) CreateStage(targetPath, stageLeaf string) (*Stage, error) {
	parent, targetLeaf, err := splitPortablePath(targetPath)
	if err != nil {
		return nil, err
	}
	if err := validatePortableLeaf(stageLeaf); err != nil || stageLeaf == targetLeaf {
		return nil, opError("create-stage", stageLeaf, ErrInvalidPath)
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return nil, opError("create-stage-parent", targetPath, err)
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, stageLeaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = ErrCollision
		}
		return nil, opError("create-stage", stageLeaf, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, &RecoveryError{
			Op:       "fstat-stage",
			Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf)},
			Err:      err,
		}
	}
	entry := entryFromStat(&stat)
	if entry.Kind != KindRegular {
		unix.Close(fd)
		return nil, &RecoveryError{
			Op:       "create-stage",
			Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf), Entry: entry, IdentityKnown: true},
			Err:      ErrNotRegular,
		}
	}
	if entry.Links != 1 {
		unix.Close(fd)
		return nil, &RecoveryError{
			Op:       "verify-stage-links",
			Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf), Entry: entry, IdentityKnown: true},
			Err:      ErrHardlink,
		}
	}
	guardFD, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		unix.Close(fd)
		return nil, &RecoveryError{
			Op:       "guard-stage",
			Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf), Entry: entry, IdentityKnown: true},
			Err:      err,
		}
	}
	return &Stage{root: r, parent: parent, name: stageLeaf, target: targetLeaf, fd: fd, guardFD: guardFD, expected: entry}, nil
}

// CreateSymlinkStage exclusively creates a relative symlink beside targetPath.
// The link text is validated against the final destination, not the temporary
// name, and must remain beneath the anchored Root.
func (r *Root) CreateSymlinkStage(targetPath, stageLeaf, linkTarget string) (*SymlinkStage, error) {
	parent, targetLeaf, err := splitPortablePath(targetPath)
	if err != nil {
		return nil, err
	}
	if err := validatePortableLeaf(stageLeaf); err != nil || stageLeaf == targetLeaf {
		return nil, opError("create-symlink-stage", stageLeaf, ErrInvalidPath)
	}
	if err := ValidateRelativeSymlinkTarget(targetPath, linkTarget); err != nil {
		return nil, err
	}
	if err := r.validateSymlinkReferent(targetPath, linkTarget); err != nil {
		return nil, opError("validate-symlink-referent", targetPath, err)
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return nil, opError("create-symlink-stage-parent", targetPath, err)
	}
	defer unix.Close(parentFD)
	if err := unix.Symlinkat(linkTarget, parentFD, stageLeaf); err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = ErrCollision
		}
		return nil, opError("create-symlink-stage", stageLeaf, err)
	}
	guardFD, entry, observedTarget, err := openArtifactGuard(parentFD, stageLeaf, targetPath)
	if err != nil {
		return nil, &RecoveryError{
			Op: "guard-symlink-stage", Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf)}, Err: err,
		}
	}
	if entry.Kind != KindSymlink || observedTarget != linkTarget {
		unix.Close(guardFD)
		return nil, &RecoveryError{
			Op:       "verify-symlink-stage",
			Evidence: RecoveryEvidence{Path: relativeLeafPath(parent, stageLeaf), Entry: entry, IdentityKnown: true},
			Err:      ErrStaleHandle,
		}
	}
	return &SymlinkStage{
		root: r, parent: parent, name: stageLeaf, target: targetLeaf,
		guardFD: guardFD, expected: entry, linkTarget: linkTarget,
	}, nil
}

func (r *Root) validateSymlinkReferent(destinationPath, linkTarget string) error {
	return r.validateArtifactPath(resolvedSymlinkTarget(destinationPath, linkTarget), make(map[string]struct{}), 0)
}

func (r *Root) validateArtifactPath(relativePath string, seen map[string]struct{}, depth int) error {
	if depth >= 40 {
		return ErrInvalidPath
	}
	if _, exists := seen[relativePath]; exists {
		return ErrInvalidPath
	}
	seen[relativePath] = struct{}{}
	parent, leaf, err := splitPortablePath(relativePath)
	if err != nil {
		return err
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	defer unix.Close(parentFD)
	entry, err := lstatAt(parentFD, leaf)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if entry.Kind == KindRegular {
		if entry.Links != 1 {
			return ErrHardlink
		}
		return nil
	}
	if entry.Kind != KindSymlink {
		return ErrNotArtifact
	}
	guardFD, _, nestedTarget, err := openArtifactGuard(parentFD, leaf, relativePath)
	if err != nil {
		return err
	}
	unix.Close(guardFD)
	return r.validateArtifactPath(resolvedSymlinkTarget(relativePath, nestedTarget), seen, depth+1)
}

func (s *SymlinkStage) verifyAt(parentFD int) error {
	entry, linkTarget, err := verifyArtifactGuardAt(parentFD, s.name, relativeLeafPath(s.parent, s.name), s.guardFD)
	if err != nil {
		return err
	}
	if !sameObject(entry, s.expected) || linkTarget != s.linkTarget {
		return ErrStaleHandle
	}
	return nil
}

// Close releases the symlink descriptor pin without deleting or renaming it.
func (s *SymlinkStage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var closeErrors []error
	if s.guardFD >= 0 {
		if err := unix.Close(s.guardFD); err != nil {
			closeErrors = append(closeErrors, err)
		}
		s.guardFD = -1
	}
	if err := closeRetainedPins(s.recovery); err != nil {
		closeErrors = append(closeErrors, err)
	}
	s.recovery = nil
	if len(closeErrors) != 0 {
		return opError("close-symlink-stage", s.name, errors.Join(closeErrors...))
	}
	return nil
}

// CommitCreate atomically installs the symlink at an absent target. If an
// identity, link-count, link-text, or final referent postcondition fails, the
// symlink remains descriptor-pinned by this stage and the returned
// RecoveryError identifies its reachable target path.
func (s *SymlinkStage) CommitCreate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state != stagePresent {
		return opError("commit-symlink-create", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return opError("commit-symlink-create-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	if err := s.verifyAt(parentFD); err != nil {
		return opError("commit-symlink-create", s.name, err)
	}
	if err := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.target), s.linkTarget); err != nil {
		return opError("commit-symlink-create-referent", s.target, err)
	}
	if hook := s.root.transitionTestHook; hook != nil {
		hook(transitionBeforeSymlinkCommitRename)
	}
	if err := unix.Renameat2(parentFD, s.name, parentFD, s.target, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return opError("commit-symlink-create", s.target, err)
	}
	if hook := s.root.transitionTestHook; hook != nil {
		hook(transitionAfterSymlinkCommitRename)
	}
	installed, linkTarget, verifyErr := verifyArtifactGuardAt(parentFD, s.target, relativeLeafPath(s.parent, s.target), s.guardFD)
	if verifyErr != nil || installed.Kind != KindSymlink || linkTarget != s.linkTarget {
		s.state = stageRetained
		evidence := pinnedRecoveryEvidence(parentFD, s.target, relativeLeafPath(s.parent, s.target), s.guardFD)
		if evidence.IdentityKnown {
			s.name = s.target
		} else {
			s.name = ""
		}
		return postconditionError("commit-symlink-create-postcondition", evidence, verifyErr)
	}
	if err := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.target), s.linkTarget); err != nil {
		s.state = stageRetained
		evidence := pinnedRecoveryEvidence(parentFD, s.target, relativeLeafPath(s.parent, s.target), s.guardFD)
		if evidence.IdentityKnown {
			s.name = s.target
		} else {
			s.name = ""
		}
		return postconditionError("commit-symlink-create-referent-postcondition", evidence, err)
	}
	s.state = stageCommitted
	unix.Close(s.guardFD)
	s.guardFD = -1
	return nil
}

// Exchange atomically swaps the symlink stage with a regular file or another
// contained relative symlink and retains the displaced artifact in the stage.
func (s *SymlinkStage) Exchange() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state != stagePresent {
		return opError("exchange-symlink", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return opError("exchange-symlink-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	if err := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.target), s.linkTarget); err != nil {
		return opError("exchange-symlink-referent", s.target, err)
	}
	if hook := s.root.transitionTestHook; hook != nil {
		hook(transitionBeforeSymlinkExchange)
	}
	sourceLink := s.linkTarget
	targetGuard, targetEntry, targetLink, err := exchangePinned(s.root, parentFD, s.parent, s.name, s.target, s.guardFD)
	if err != nil {
		s.adoptExchangeRecovery(err)
		return opError("exchange-symlink", s.target, err)
	}
	if referentErr := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.target), sourceLink); referentErr != nil {
		if hook := s.root.transitionTestHook; hook != nil {
			hook(transitionBeforeExchangeRollback)
		}
		rollbackErr := unix.Renameat2(parentFD, s.name, parentFD, s.target, unix.RENAME_EXCHANGE)
		if hook := s.root.transitionTestHook; hook != nil {
			hook(transitionAfterExchangeRollback)
		}
		recoveryErr := reverifyExchangeRollback(
			parentFD, s.parent, s.name, s.target, s.guardFD, targetGuard, sourceLink, targetLink,
			referentErr, rollbackErr,
		)
		s.adoptExchangeRecovery(recoveryErr)
		return opError("exchange-symlink-referent-postcondition", s.target, recoveryErr)
	}
	unix.Close(s.guardFD)
	s.guardFD = targetGuard
	s.expected = targetEntry
	s.linkTarget = targetLink
	s.state = stageDisplaced
	return nil
}

// Detach moves the displaced artifact to an absent hold name.
func (s *SymlinkStage) Detach(holdLeaf string) (*Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state != stageDisplaced {
		return nil, opError("detach-symlink-stage", s.name, ErrInvalidState)
	}
	if err := validatePortableLeaf(holdLeaf); err != nil || holdLeaf == s.name || holdLeaf == s.target {
		return nil, opError("detach-symlink-stage", holdLeaf, ErrInvalidPath)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return nil, opError("detach-symlink-stage-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	if err := s.verifyAt(parentFD); err != nil {
		return nil, opError("detach-symlink-stage", s.name, err)
	}
	if s.expected.Kind == KindSymlink {
		if err := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.name), s.linkTarget); err != nil {
			return nil, opError("detach-symlink-stage-referent", s.name, err)
		}
	}
	if err := unix.Renameat2(parentFD, s.name, parentFD, holdLeaf, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return nil, opError("detach-symlink-stage", s.name, err)
	}
	heldEntry, heldLink, verifyErr := verifyArtifactGuardAt(parentFD, holdLeaf, relativeLeafPath(s.parent, holdLeaf), s.guardFD)
	if verifyErr != nil || !sameObject(heldEntry, s.expected) || heldLink != s.linkTarget {
		rollbackErr := unix.Renameat2(parentFD, holdLeaf, parentFD, s.name, unix.RENAME_NOREPLACE)
		return nil, opError("detach-symlink-stage-postcondition", s.name, fmt.Errorf("%w: verification=%v rollback=%v", ErrStaleHandle, verifyErr, rollbackErr))
	}
	hold := &Hold{root: s.root, parent: s.parent, name: holdLeaf, guardFD: s.guardFD, expected: s.expected, linkTarget: s.linkTarget}
	s.guardFD = -1
	s.state = stageDetached
	return hold, nil
}

// Remove transfers the staged/displaced artifact into retained quarantine.
func (s *SymlinkStage) Remove() (*Quarantine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (s.state != stagePresent && s.state != stageDisplaced && s.state != stageRetained) {
		return nil, opError("remove-symlink-stage", s.name, ErrInvalidState)
	}
	if s.name == "" || len(s.recovery) != 0 {
		return nil, opError("remove-symlink-stage", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return nil, opError("remove-symlink-stage-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	quarantine, err := s.root.quarantinePinned(parentFD, s.parent, s.name, s.guardFD)
	if err != nil {
		return nil, opError("remove-symlink-stage", s.name, err)
	}
	s.state = stageQuarantined
	s.guardFD = -1
	return quarantine, nil
}

// Write writes to the exclusively-created stage descriptor.
func (s *Stage) Write(content []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileClosed {
		return 0, opError("write-stage", s.name, ErrClosed)
	}
	if s.state != stagePresent {
		return 0, opError("write-stage", s.name, ErrInvalidState)
	}
	written := 0
	for written < len(content) {
		n, err := unix.Write(s.fd, content[written:])
		written += n
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return written, opError("write-stage", s.name, err)
		}
		if n == 0 {
			return written, opError("write-stage", s.name, io.ErrShortWrite)
		}
	}
	return written, nil
}

// Chmod changes only permission bits on the stage descriptor.
func (s *Stage) Chmod(mode uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode&^0o777 != 0 || mode&0o022 != 0 || mode&0o400 == 0 {
		return opError("chmod-stage", s.name, ErrInvalidState)
	}
	if s.fileClosed {
		return opError("chmod-stage", s.name, ErrClosed)
	}
	if s.state != stagePresent {
		return opError("chmod-stage", s.name, ErrInvalidState)
	}
	if err := unix.Fchmod(s.fd, mode); err != nil {
		return opError("chmod-stage", s.name, err)
	}
	return nil
}

// Sync flushes stage contents and metadata through its descriptor.
func (s *Stage) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileClosed {
		return opError("sync-stage", s.name, ErrClosed)
	}
	if s.state != stagePresent {
		return opError("sync-stage", s.name, ErrInvalidState)
	}
	if err := unix.Fsync(s.fd); err != nil {
		return opError("sync-stage", s.name, err)
	}
	return nil
}

// Close releases the writable stage descriptor and, after a failed commit
// postcondition, its retained recovery pin. It never removes a name.
func (s *Stage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileClosed && (s.state != stageRetained || s.guardFD < 0) && len(s.recovery) == 0 {
		return nil
	}
	var closeErrors []error
	if !s.fileClosed {
		s.fileClosed = true
		if err := unix.Close(s.fd); err != nil {
			closeErrors = append(closeErrors, err)
		}
		s.fd = -1
	}
	if s.state == stageRetained && s.guardFD >= 0 {
		if err := unix.Close(s.guardFD); err != nil {
			closeErrors = append(closeErrors, err)
		}
		s.guardFD = -1
	}
	if err := closeRetainedPins(s.recovery); err != nil {
		closeErrors = append(closeErrors, err)
	}
	s.recovery = nil
	if len(closeErrors) != 0 {
		return opError("close-stage", s.name, errors.Join(closeErrors...))
	}
	return nil
}

func (s *Stage) verifyAt(parentFD int) error {
	entry, linkTarget, err := verifyArtifactGuardAt(parentFD, s.name, relativeLeafPath(s.parent, s.name), s.guardFD)
	if err != nil {
		return err
	}
	if !sameObject(entry, s.expected) || linkTarget != s.linkTarget {
		return ErrStaleHandle
	}
	return nil
}

func verifyArtifactGuardAt(parentFD int, leaf, destinationPath string, guardFD int) (Entry, string, error) {
	if guardFD < 0 {
		return Entry{}, "", ErrClosed
	}
	var stat unix.Stat_t
	if err := unix.Fstat(guardFD, &stat); err != nil {
		return Entry{}, "", err
	}
	guardIdentity := entryFromStat(&stat)
	entry, err := lstatAt(parentFD, leaf)
	if err != nil {
		return Entry{}, "", err
	}
	if !sameObject(entry, guardIdentity) {
		return entry, "", ErrStaleHandle
	}
	guardEntry, linkTarget, err := artifactFromGuard(guardFD, destinationPath)
	if err != nil {
		return guardEntry, linkTarget, err
	}
	return guardEntry, linkTarget, nil
}

func pinnedRecoveryEvidence(parentFD int, leaf, relativePath string, guardFD int) RecoveryEvidence {
	_, evidence := locateRetainedPin(parentFD, ".", []string{leaf}, guardFD, "")
	if evidence.IdentityKnown {
		evidence.Path = relativePath
	}
	return evidence
}

func locateRetainedPin(parentFD int, parent string, leaves []string, guardFD int, knownLinkTarget string) (retainedPin, RecoveryEvidence) {
	pin := retainedPin{guardFD: guardFD, linkTarget: knownLinkTarget}
	evidence := RecoveryEvidence{DescriptorPinned: guardFD >= 0}
	if guardFD < 0 {
		return pin, evidence
	}
	var stat unix.Stat_t
	if err := unix.Fstat(guardFD, &stat); err != nil {
		return pin, evidence
	}
	pin.expected = entryFromStat(&stat)
	evidence.Entry = pin.expected
	for _, leaf := range leaves {
		namedEntry, err := lstatAt(parentFD, leaf)
		if err != nil || !sameObject(namedEntry, pin.expected) {
			continue
		}
		pin.name = leaf
		evidence.Path = relativeLeafPath(parent, leaf)
		evidence.IdentityKnown = true
		return pin, evidence
	}
	return pin, evidence
}

func pinCandidate(parentFD int, parent, leaf string) (retainedPin, RecoveryEvidence, error) {
	fd, err := unix.Openat(parentFD, leaf, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return retainedPin{}, RecoveryEvidence{}, ErrNotExist
		}
		return retainedPin{}, RecoveryEvidence{}, err
	}
	pin, evidence := locateRetainedPin(parentFD, parent, []string{leaf}, fd, "")
	if !evidence.IdentityKnown {
		unix.Close(fd)
		return retainedPin{}, RecoveryEvidence{}, ErrStaleHandle
	}
	if pin.expected.Kind == KindSymlink {
		buffer := make([]byte, maxSymlinkTargetLength+1)
		n, readErr := unix.Readlinkat(fd, "", buffer)
		if readErr == nil && n < len(buffer) {
			pin.linkTarget = string(buffer[:n])
		}
	}
	return pin, evidence, nil
}

func closeRetainedPins(pins []retainedPin) error {
	var closeErrors []error
	for index := range pins {
		if pins[index].guardFD < 0 {
			continue
		}
		if err := unix.Close(pins[index].guardFD); err != nil {
			closeErrors = append(closeErrors, err)
		}
		pins[index].guardFD = -1
	}
	return errors.Join(closeErrors...)
}

func collectCandidatePins(parentFD int, parent string, leaves []string, claimed map[string]struct{}) ([]retainedPin, []RecoveryEvidence, error) {
	var pins []retainedPin
	var evidence []RecoveryEvidence
	var pinErrors []error
	for _, leaf := range leaves {
		if _, exists := claimed[leaf]; exists {
			continue
		}
		pin, item, err := pinCandidate(parentFD, parent, leaf)
		if errors.Is(err, ErrNotExist) {
			continue
		}
		if err != nil {
			pinErrors = append(pinErrors, err)
			continue
		}
		claimed[leaf] = struct{}{}
		pins = append(pins, pin)
		evidence = append(evidence, item)
	}
	return pins, evidence, errors.Join(pinErrors...)
}

func postconditionError(op string, evidence RecoveryEvidence, causes ...error) error {
	joined := []error{ErrStaleHandle}
	for _, cause := range causes {
		if cause != nil {
			joined = append(joined, cause)
		}
	}
	return &RecoveryError{Op: op, Evidence: evidence, Err: errors.Join(joined...)}
}

func (s *Stage) adoptExchangeRecovery(err error) {
	var recovery *exchangeRecoveryError
	if !errors.As(err, &recovery) {
		return
	}
	s.name = recovery.sourceName
	s.recovery = append(s.recovery, recovery.pins...)
	recovery.pins = nil
	s.state = stageRetained
}

func (s *SymlinkStage) adoptExchangeRecovery(err error) {
	var recovery *exchangeRecoveryError
	if !errors.As(err, &recovery) {
		return
	}
	s.name = recovery.sourceName
	s.recovery = append(s.recovery, recovery.pins...)
	recovery.pins = nil
	s.state = stageRetained
}

// CommitCreate atomically moves a stage to an absent target with
// RENAME_NOREPLACE. A collision leaves both objects untouched. If an identity,
// type, or link-count postcondition fails, the regular file remains
// descriptor-pinned by this stage and the returned RecoveryError identifies
// its reachable target path.
func (s *Stage) CommitCreate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stagePresent {
		return opError("commit-create", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return opError("commit-create-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	if err := s.verifyAt(parentFD); err != nil {
		return opError("commit-create", s.name, err)
	}
	if err := unix.Renameat2(parentFD, s.name, parentFD, s.target, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return opError("commit-create", s.target, err)
	}
	if hook := s.root.transitionTestHook; hook != nil {
		hook(transitionAfterRegularCommitRename)
	}
	installed, linkTarget, verifyErr := verifyArtifactGuardAt(parentFD, s.target, relativeLeafPath(s.parent, s.target), s.guardFD)
	if verifyErr != nil || installed.Kind != KindRegular || linkTarget != "" {
		s.state = stageRetained
		evidence := pinnedRecoveryEvidence(parentFD, s.target, relativeLeafPath(s.parent, s.target), s.guardFD)
		if evidence.IdentityKnown {
			s.name = s.target
		} else {
			s.name = ""
		}
		return postconditionError("commit-create-postcondition", evidence, verifyErr)
	}
	s.state = stageCommitted
	unix.Close(s.guardFD)
	s.guardFD = -1
	return nil
}

// Exchange atomically swaps a regular stage with an existing regular file or
// contained relative symlink. Directories, special nodes, and hardlinks are
// rejected before the exchange.
func (s *Stage) Exchange() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stagePresent {
		return opError("exchange", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return opError("exchange-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	targetGuard, targetEntry, targetLink, err := exchangePinned(
		s.root, parentFD, s.parent, s.name, s.target, s.guardFD,
	)
	if err != nil {
		s.adoptExchangeRecovery(err)
		return opError("exchange", s.target, err)
	}
	unix.Close(s.guardFD)
	s.guardFD = targetGuard
	s.expected = targetEntry
	s.linkTarget = targetLink
	s.state = stageDisplaced
	return nil
}

func reverifyExchangeRollback(parentFD int, parent, sourceLeaf, targetLeaf string, sourceGuard, targetGuard int, sourceLink, targetLink string, causes ...error) error {
	leaves := []string{sourceLeaf, targetLeaf}
	sourcePin, sourceEvidence := locateRetainedPin(parentFD, parent, leaves, sourceGuard, sourceLink)
	targetPin, targetEvidence := locateRetainedPin(parentFD, parent, leaves, targetGuard, targetLink)
	claimed := make(map[string]struct{}, 2)
	if sourcePin.name != "" {
		claimed[sourcePin.name] = struct{}{}
	}
	if targetPin.name != "" {
		claimed[targetPin.name] = struct{}{}
	}
	replacementPins, replacementEvidence, replacementErr := collectCandidatePins(parentFD, parent, leaves, claimed)
	causes = append(causes, replacementErr)
	if sourcePin.name == sourceLeaf && targetPin.name == targetLeaf && len(replacementPins) == 0 {
		unix.Close(targetGuard)
		return errors.Join(append([]error{ErrStaleHandle}, causes...)...)
	}

	related := make([]RecoveryEvidence, 0, 1+len(replacementEvidence))
	related = append(related, targetEvidence)
	related = append(related, replacementEvidence...)
	pins := make([]retainedPin, 0, 1+len(replacementPins))
	pins = append(pins, targetPin)
	pins = append(pins, replacementPins...)
	return &exchangeRecoveryError{
		recovery: &RecoveryError{
			Op:       "exchange-rollback-postcondition",
			Evidence: sourceEvidence,
			Related:  related,
			Err:      errors.Join(append([]error{ErrStaleHandle}, causes...)...),
		},
		sourceName: sourcePin.name,
		pins:       pins,
	}
}

func exchangePinned(root *Root, parentFD int, parent, sourceLeaf, targetLeaf string, sourceGuard int) (int, Entry, string, error) {
	sourcePath := relativeLeafPath(parent, sourceLeaf)
	targetPath := relativeLeafPath(parent, targetLeaf)
	sourceEntry, sourceLink, err := verifyArtifactGuardAt(parentFD, sourceLeaf, sourcePath, sourceGuard)
	if err != nil {
		return -1, Entry{}, "", err
	}
	targetGuard, targetEntry, targetLink, err := openArtifactGuard(parentFD, targetLeaf, targetPath)
	if err != nil {
		return -1, Entry{}, "", err
	}
	if targetEntry.Kind == KindSymlink {
		if err := root.validateSymlinkReferent(targetPath, targetLink); err != nil {
			unix.Close(targetGuard)
			return -1, Entry{}, "", err
		}
	}
	if err := unix.Renameat2(parentFD, sourceLeaf, parentFD, targetLeaf, unix.RENAME_EXCHANGE); err != nil {
		unix.Close(targetGuard)
		return -1, Entry{}, "", err
	}
	if hook := root.transitionTestHook; hook != nil {
		hook(transitionAfterExchangeRename)
	}
	displaced, displacedLink, displacedErr := verifyArtifactGuardAt(parentFD, sourceLeaf, sourcePath, targetGuard)
	installed, installedLink, installedErr := verifyArtifactGuardAt(parentFD, targetLeaf, targetPath, sourceGuard)
	if displacedErr == nil && installedErr == nil && sameObject(displaced, targetEntry) && sameObject(installed, sourceEntry) &&
		displacedLink == targetLink && installedLink == sourceLink {
		return targetGuard, displaced, displacedLink, nil
	}
	if hook := root.transitionTestHook; hook != nil {
		hook(transitionBeforeExchangeRollback)
	}
	rollbackErr := unix.Renameat2(parentFD, sourceLeaf, parentFD, targetLeaf, unix.RENAME_EXCHANGE)
	if hook := root.transitionTestHook; hook != nil {
		hook(transitionAfterExchangeRollback)
	}
	recoveryErr := reverifyExchangeRollback(
		parentFD, parent, sourceLeaf, targetLeaf, sourceGuard, targetGuard, sourceLink, targetLink,
		displacedErr, installedErr, rollbackErr,
	)
	return -1, Entry{}, "", recoveryErr
}

// Detach renames an existing regular file or contained relative symlink to an
// absent hold name with RENAME_NOREPLACE.
func (r *Root) Detach(targetPath, holdLeaf string) (*Hold, error) {
	parent, targetLeaf, err := splitPortablePath(targetPath)
	if err != nil {
		return nil, err
	}
	if err := validatePortableLeaf(holdLeaf); err != nil || holdLeaf == targetLeaf {
		return nil, opError("detach", holdLeaf, ErrInvalidPath)
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return nil, opError("detach-parent", targetPath, err)
	}
	defer unix.Close(parentFD)
	targetGuard, targetEntry, targetLink, err := openArtifactGuard(parentFD, targetLeaf, targetPath)
	if err != nil {
		return nil, opError("detach", targetPath, err)
	}
	if targetEntry.Kind == KindSymlink {
		if err := r.validateSymlinkReferent(targetPath, targetLink); err != nil {
			unix.Close(targetGuard)
			return nil, opError("detach", targetPath, err)
		}
	}
	if err := unix.Renameat2(parentFD, targetLeaf, parentFD, holdLeaf, unix.RENAME_NOREPLACE); err != nil {
		unix.Close(targetGuard)
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return nil, opError("detach", targetPath, err)
	}
	heldEntry, heldLink, verifyErr := verifyArtifactGuardAt(parentFD, holdLeaf, relativeLeafPath(parent, holdLeaf), targetGuard)
	if verifyErr == nil && sameObject(heldEntry, targetEntry) && heldLink == targetLink {
		return &Hold{root: r, parent: parent, name: holdLeaf, guardFD: targetGuard, expected: heldEntry, linkTarget: targetLink}, nil
	}
	rollbackErr := unix.Renameat2(parentFD, holdLeaf, parentFD, targetLeaf, unix.RENAME_NOREPLACE)
	unix.Close(targetGuard)
	if rollbackErr != nil {
		return nil, opError("detach-postcondition", targetPath, fmt.Errorf("%w: verification=%v rollback=%v", ErrStaleHandle, verifyErr, rollbackErr))
	}
	return nil, opError("detach-postcondition", targetPath, ErrStaleHandle)
}

// Detach renames the displaced object at a stage name to an absent hold name.
func (s *Stage) Detach(holdLeaf string) (*Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stageDisplaced {
		return nil, opError("detach-stage", s.name, ErrInvalidState)
	}
	if err := validatePortableLeaf(holdLeaf); err != nil || holdLeaf == s.name || holdLeaf == s.target {
		return nil, opError("detach-stage", holdLeaf, ErrInvalidPath)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return nil, opError("detach-stage-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	if err := s.verifyAt(parentFD); err != nil {
		return nil, opError("detach-stage", s.name, err)
	}
	if s.expected.Kind == KindSymlink {
		if err := s.root.validateSymlinkReferent(relativeLeafPath(s.parent, s.name), s.linkTarget); err != nil {
			return nil, opError("detach-stage-referent", s.name, err)
		}
	}
	if err := unix.Renameat2(parentFD, s.name, parentFD, holdLeaf, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return nil, opError("detach-stage", s.name, err)
	}
	heldEntry, heldLink, verifyErr := verifyArtifactGuardAt(parentFD, holdLeaf, relativeLeafPath(s.parent, holdLeaf), s.guardFD)
	if verifyErr != nil || !sameObject(heldEntry, s.expected) || heldLink != s.linkTarget {
		rollbackErr := unix.Renameat2(parentFD, holdLeaf, parentFD, s.name, unix.RENAME_NOREPLACE)
		return nil, opError("detach-stage-postcondition", s.name, fmt.Errorf("%w: verification=%v rollback=%v", ErrStaleHandle, verifyErr, rollbackErr))
	}
	hold := &Hold{root: s.root, parent: s.parent, name: holdLeaf, guardFD: s.guardFD, expected: s.expected, linkTarget: s.linkTarget}
	s.guardFD = -1
	s.state = stageDetached
	return hold, nil
}

// Remove atomically removes a stage/displaced object from its public name by
// transferring it to a retained, descriptor-pinned Quarantine. It never
// unlinks the object; the returned record must be journaled or restored.
func (s *Stage) Remove() (*Quarantine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stagePresent && s.state != stageDisplaced && s.state != stageRetained {
		return nil, opError("remove-stage", s.name, ErrInvalidState)
	}
	if s.name == "" || len(s.recovery) != 0 {
		return nil, opError("remove-stage", s.name, ErrInvalidState)
	}
	parentFD, err := s.root.openParent(s.parent)
	if err != nil {
		return nil, opError("remove-stage-parent", s.name, err)
	}
	defer unix.Close(parentFD)
	quarantine, err := s.root.quarantinePinned(parentFD, s.parent, s.name, s.guardFD)
	if err != nil {
		return nil, opError("remove-stage", s.name, err)
	}
	s.state = stageQuarantined
	s.guardFD = -1
	return quarantine, nil
}

// Remove transfers a held object into a retained, descriptor-pinned
// Quarantine. It never unlinks the object.
func (h *Hold) Remove() (*Quarantine, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.transferred || h.name == "" || len(h.recovery) != 0 {
		return nil, opError("remove-hold", h.name, ErrInvalidState)
	}
	parentFD, err := h.root.openParent(h.parent)
	if err != nil {
		return nil, opError("remove-hold-parent", h.name, err)
	}
	defer unix.Close(parentFD)
	if h.guardFD < 0 {
		return nil, opError("remove-hold", h.name, ErrClosed)
	}
	quarantine, err := h.root.quarantinePinned(parentFD, h.parent, h.name, h.guardFD)
	if err != nil {
		return nil, opError("remove-hold", h.name, err)
	}
	h.transferred = true
	h.guardFD = -1
	return quarantine, nil
}

const quarantinePrefix = ".codex-harness-quarantine-"

func newQuarantineLeaf() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return quarantinePrefix + hex.EncodeToString(entropy[:]), nil
}

func relativeLeafPath(parent, leaf string) string {
	if parent == "." {
		return leaf
	}
	return parent + "/" + leaf
}

// quarantinePinned moves the current source object to an unguessable, absent
// name with RENAME_NOREPLACE and transfers the already-pinned descriptor into
// a typed Quarantine. It performs no unlink. A substitution in either the
// source or quarantine race window is restored when possible and always
// returned with path-safe recovery evidence.
func (r *Root) quarantinePinned(parentFD int, parent, sourceLeaf string, guardFD int) (*Quarantine, error) {
	if guardFD < 0 {
		return nil, ErrClosed
	}
	guardEntry, linkTarget, err := verifyArtifactGuardAt(parentFD, sourceLeaf, relativeLeafPath(parent, sourceLeaf), guardFD)
	if err != nil {
		return nil, err
	}
	if guardEntry.Kind == KindSymlink {
		if err := r.validateSymlinkReferent(relativeLeafPath(parent, sourceLeaf), linkTarget); err != nil {
			return nil, err
		}
	}

	for attempt := 0; attempt < 8; attempt++ {
		quarantineLeaf, err := newQuarantineLeaf()
		if err != nil {
			return nil, err
		}
		if hook := r.cleanupTestHook; hook != nil {
			hook(cleanupBeforeQuarantineRename, parentFD, sourceLeaf, quarantineLeaf)
		}
		err = unix.Renameat2(parentFD, sourceLeaf, parentFD, quarantineLeaf, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			continue
		}
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil, ErrStaleHandle
			}
			return nil, err
		}

		quarantined, _, verifyErr := verifyArtifactGuardAt(parentFD, quarantineLeaf, relativeLeafPath(parent, quarantineLeaf), guardFD)
		if verifyErr != nil || !sameObject(quarantined, guardEntry) {
			return nil, restoreRacedObject(parentFD, parent, sourceLeaf, quarantineLeaf, quarantined, verifyErr)
		}
		if hook := r.cleanupTestHook; hook != nil {
			hook(cleanupAfterQuarantineVerify, parentFD, sourceLeaf, quarantineLeaf)
		}
		quarantined, _, verifyErr = verifyArtifactGuardAt(parentFD, quarantineLeaf, relativeLeafPath(parent, quarantineLeaf), guardFD)
		if verifyErr != nil || !sameObject(quarantined, guardEntry) {
			return nil, restoreRacedObject(parentFD, parent, sourceLeaf, quarantineLeaf, quarantined, verifyErr)
		}
		return &Quarantine{
			root: r, parent: parent, name: quarantineLeaf,
			guardFD: guardFD, expected: guardEntry, linkTarget: linkTarget,
		}, nil
	}
	return nil, fmt.Errorf("%w: could not reserve quarantine name", ErrCollision)
}

func restoreRacedObject(parentFD int, parent, sourceLeaf, quarantineLeaf string, observed Entry, verifyErr error) error {
	evidence := RecoveryEvidence{Path: relativeLeafPath(parent, quarantineLeaf)}
	if verifyErr == nil {
		evidence.Entry = observed
		evidence.IdentityKnown = true
	}
	restoreErr := unix.Renameat2(parentFD, quarantineLeaf, parentFD, sourceLeaf, unix.RENAME_NOREPLACE)
	if restoreErr == nil {
		evidence.Path = relativeLeafPath(parent, sourceLeaf)
		return &RecoveryError{Op: "restore-raced-quarantine", Evidence: evidence, Err: ErrStaleHandle}
	}
	return &RecoveryError{
		Op:       "retain-raced-quarantine",
		Evidence: evidence,
		Err:      fmt.Errorf("%w: restore: %v", ErrStaleHandle, restoreErr),
	}
}

// Record returns the portable identity that must be written to an installer
// journal before the descriptor pin is released.
func (q *Quarantine) Record() QuarantineRecord {
	q.mu.Lock()
	defer q.mu.Unlock()
	path := ""
	if q.name != "" {
		path = relativeLeafPath(q.parent, q.name)
	}
	return QuarantineRecord{Path: path, Entry: q.expected, LinkTarget: q.linkTarget}
}

// OpenQuarantine reacquires and verifies a descriptor pin from journaled
// evidence. Only names issued by this package's quarantine namespace are
// accepted.
func (r *Root) OpenQuarantine(record QuarantineRecord) (*Quarantine, error) {
	parent, leaf, err := splitPortablePath(record.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(leaf, quarantinePrefix) || (record.Entry.Kind != KindRegular && record.Entry.Kind != KindSymlink) {
		return nil, opError("open-quarantine", record.Path, ErrInvalidPath)
	}
	if record.Entry.Kind == KindSymlink {
		if err := ValidateRelativeSymlinkTarget(record.Path, record.LinkTarget); err != nil {
			return nil, err
		}
		if err := r.validateSymlinkReferent(record.Path, record.LinkTarget); err != nil {
			return nil, opError("open-quarantine-referent", record.Path, err)
		}
	}
	if hook := r.transitionTestHook; hook != nil {
		hook(transitionBeforeQuarantineOpen)
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return nil, opError("open-quarantine-parent", record.Path, err)
	}
	defer unix.Close(parentFD)
	guardFD, entry, linkTarget, err := openArtifactGuard(parentFD, leaf, record.Path)
	if err != nil {
		return nil, opError("open-quarantine", record.Path, err)
	}
	if !sameObject(entry, record.Entry) || linkTarget != record.LinkTarget {
		unix.Close(guardFD)
		return nil, opError("open-quarantine", record.Path, ErrStaleHandle)
	}
	if entry.Kind == KindSymlink {
		if err := r.validateSymlinkReferent(record.Path, linkTarget); err != nil {
			evidence := pinnedRecoveryEvidence(parentFD, leaf, record.Path, guardFD)
			unix.Close(guardFD)
			return nil, postconditionError("open-quarantine-referent-postcondition", evidence, err)
		}
	}
	return &Quarantine{root: r, parent: parent, name: leaf, guardFD: guardFD, expected: entry, linkTarget: linkTarget}, nil
}

// Restore moves the pinned object from quarantine to an absent destination in
// the same directory. It never replaces or deletes another object.
func (q *Quarantine) Restore(destinationPath string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.restored || q.guardFD < 0 || q.name == "" || len(q.recovery) != 0 {
		return opError("restore-quarantine", destinationPath, ErrInvalidState)
	}
	parent, leaf, err := splitPortablePath(destinationPath)
	if err != nil {
		return err
	}
	if parent != q.parent || leaf == q.name {
		return opError("restore-quarantine", destinationPath, ErrInvalidPath)
	}
	if q.expected.Kind == KindSymlink {
		if err := ValidateRelativeSymlinkTarget(destinationPath, q.linkTarget); err != nil {
			return err
		}
		if err := q.root.validateSymlinkReferent(destinationPath, q.linkTarget); err != nil {
			return opError("restore-quarantine-referent", destinationPath, err)
		}
	}
	parentFD, err := q.root.openParent(q.parent)
	if err != nil {
		return opError("restore-quarantine-parent", destinationPath, err)
	}
	defer unix.Close(parentFD)
	entry, linkTarget, err := verifyArtifactGuardAt(parentFD, q.name, relativeLeafPath(q.parent, q.name), q.guardFD)
	if err != nil {
		return opError("restore-quarantine", q.name, err)
	}
	if !sameObject(entry, q.expected) || linkTarget != q.linkTarget {
		return opError("restore-quarantine", q.name, ErrStaleHandle)
	}
	if hook := q.root.transitionTestHook; hook != nil {
		hook(transitionBeforeQuarantineRestore)
	}
	quarantineLeaf := q.name
	if err := unix.Renameat2(parentFD, quarantineLeaf, parentFD, leaf, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
			err = ErrCollision
		}
		return opError("restore-quarantine", destinationPath, err)
	}
	if hook := q.root.transitionTestHook; hook != nil {
		hook(transitionAfterQuarantineRestoreRename)
	}
	restored, restoredLink, verifyErr := verifyArtifactGuardAt(parentFD, leaf, destinationPath, q.guardFD)
	var referentErr error
	if verifyErr == nil && sameObject(restored, q.expected) && restoredLink == q.linkTarget && restored.Kind == KindSymlink {
		referentErr = q.root.validateSymlinkReferent(destinationPath, restoredLink)
	}
	if verifyErr != nil || !sameObject(restored, q.expected) || restoredLink != q.linkTarget || referentErr != nil {
		if hook := q.root.transitionTestHook; hook != nil {
			hook(transitionBeforeQuarantineRestoreRollback)
		}
		rollbackErr := unix.Renameat2(parentFD, leaf, parentFD, quarantineLeaf, unix.RENAME_NOREPLACE)
		if hook := q.root.transitionTestHook; hook != nil {
			hook(transitionAfterQuarantineRestoreRollback)
		}
		leaves := []string{quarantineLeaf, leaf}
		quarantinePin, evidence := locateRetainedPin(parentFD, q.parent, leaves, q.guardFD, q.linkTarget)
		claimed := make(map[string]struct{}, 1)
		if quarantinePin.name != "" {
			claimed[quarantinePin.name] = struct{}{}
		}
		replacementPins, related, replacementErr := collectCandidatePins(parentFD, q.parent, leaves, claimed)
		q.name = quarantinePin.name
		q.recovery = append(q.recovery, replacementPins...)
		return &RecoveryError{
			Op:       "restore-quarantine-postcondition",
			Evidence: evidence,
			Related:  related,
			Err:      errors.Join(ErrStaleHandle, verifyErr, referentErr, rollbackErr, replacementErr),
		}
	}
	q.restored = true
	unix.Close(q.guardFD)
	q.guardFD = -1
	return nil
}

// Close releases the descriptor pin without deleting the retained object.
// Callers must persist Record before calling Close if recovery spans a process.
func (q *Quarantine) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.guardFD < 0 && len(q.recovery) == 0 {
		return nil
	}
	var closeErrors []error
	if q.guardFD >= 0 {
		if err := unix.Close(q.guardFD); err != nil {
			closeErrors = append(closeErrors, err)
		}
		q.guardFD = -1
	}
	if err := closeRetainedPins(q.recovery); err != nil {
		closeErrors = append(closeErrors, err)
	}
	q.recovery = nil
	if len(closeErrors) != 0 {
		return opError("close-quarantine", relativeLeafPath(q.parent, q.name), errors.Join(closeErrors...))
	}
	return nil
}

// Close releases a hold without removing its object. The hold name remains in
// place and can no longer be removed through this handle.
func (h *Hold) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.guardFD < 0 && len(h.recovery) == 0 {
		return nil
	}
	var closeErrors []error
	if h.guardFD >= 0 {
		if err := unix.Close(h.guardFD); err != nil {
			closeErrors = append(closeErrors, err)
		}
		h.guardFD = -1
	}
	if err := closeRetainedPins(h.recovery); err != nil {
		closeErrors = append(closeErrors, err)
	}
	h.recovery = nil
	if len(closeErrors) != 0 {
		return opError("close-hold", h.name, errors.Join(closeErrors...))
	}
	return nil
}

// SyncParent fsyncs the containing directory of relativePath. Callers use it
// after a successful namespace rename to establish the desired durability
// point.
func (r *Root) SyncParent(relativePath string) error {
	parent, _, err := splitPortablePath(relativePath)
	if err != nil {
		return err
	}
	parentFD, err := r.openParent(parent)
	if err != nil {
		return opError("sync-parent", relativePath, err)
	}
	defer unix.Close(parentFD)
	if err := unix.Fsync(parentFD); err != nil {
		return opError("sync-parent", relativePath, err)
	}
	return nil
}
