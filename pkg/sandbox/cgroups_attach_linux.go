//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// attachCgroupFD wires the child process into the cgroup subtree via clone3's
// CLONE_INTO_CGROUP, so the process is created ALREADY inside the cgroup rather
// than being moved after the fact — there is no window in which it runs
// unconfined. UseCgroupFD/CgroupFD exist only in the Linux build of
// syscall.SysProcAttr, which is why this lives behind a build tag; see
// cgroups_attach_other.go for the refusal on every other platform.
func attachCgroupFD(cmd *exec.Cmd, dir *os.File) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(dir.Fd())
	return nil
}
