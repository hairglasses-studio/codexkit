//go:build linux || darwin

package workspace

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func ownedAliasManifest(path string) error {
	var info unix.Stat_t
	if err := unix.Lstat(path, &info); err != nil {
		return err
	}
	if info.Uid != uint32(os.Geteuid()) || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		return errors.New("manifest must be an owned singly linked regular file")
	}
	return nil
}

func lockAliasManifest(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	var info unix.Stat_t
	if err = unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 || info.Mode&0o077 != 0 {
		unix.Close(fd)
		return nil, errors.New("unsafe alias reconciliation lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, errors.New("alias reconciliation is already running")
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}
