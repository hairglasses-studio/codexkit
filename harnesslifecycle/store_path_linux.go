//go:build linux

package harnesslifecycle

import (
	"os"
)

func createStateDirectory(statePath string, mode os.FileMode) error {
	return os.MkdirAll(statePath, mode)
}
