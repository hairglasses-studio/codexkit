//go:build darwin

package harnesslifecycle

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// createStateDirectory validates every existing path component before it
// creates anything. It then creates only the first missing suffix, anchored to
// already-open directory descriptors, so an existing symlink can never redirect
// a state-store mkdir.
func createStateDirectory(statePath string, mode os.FileMode) error {
	if !strings.HasPrefix(statePath, "/") || statePath == "/" {
		return fmt.Errorf("state path must be an absolute non-root path")
	}

	rootFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open state path root: %w", err)
	}
	defer unix.Close(rootFD)

	segments := strings.Split(strings.TrimPrefix(statePath, "/"), "/")
	currentFD, err := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("duplicate state path root: %w", err)
	}
	defer func() { _ = unix.Close(currentFD) }()

	missingAt := len(segments)
	for index, segment := range segments {
		if err := validateStateParent(currentFD); err != nil {
			return fmt.Errorf("validate state path parent before %q: %w", segment, err)
		}
		nextFD, openErr := unix.Openat(currentFD, segment,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				missingAt = index
				break
			}
			return fmt.Errorf("validate state path component %q: %w", segment, openErr)
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}

	if missingAt == len(segments) {
		return validateStateDirectory(currentFD)
	}

	for _, segment := range segments[missingAt:] {
		if err := validateStateParent(currentFD); err != nil {
			return fmt.Errorf("refuse state path parent before creating %q: %w", segment, err)
		}
		if err := unix.Mkdirat(currentFD, segment, uint32(mode.Perm())); err != nil {
			return fmt.Errorf("create state path component %q: %w", segment, err)
		}
		nextFD, err := unix.Openat(currentFD, segment,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("open newly created state path component %q: %w", segment, err)
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}
	return validateStateDirectory(currentFD)
}

func validateStateParent(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("parent is not a directory")
	}
	if stat.Mode&0o022 != 0 {
		return fmt.Errorf("parent is writable by group or world")
	}
	return nil
}

func validateStateDirectory(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) ||
		uint32(stat.Mode)&0o7777 != uint32(stateRootMode.Perm()) {
		return fmt.Errorf("state directory must be owned by the effective user with mode %04o",
			stateRootMode.Perm())
	}
	return nil
}
