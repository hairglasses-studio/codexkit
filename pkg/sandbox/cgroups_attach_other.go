//go:build !linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// ErrCgroupUnsupported is returned when cgroup confinement is requested on a
// platform that has no cgroups. It is a distinct sentinel so callers can decide
// between "degrade to a weaker sandbox" and "refuse to run", rather than having
// to string-match.
var ErrCgroupUnsupported = errors.New("cgroup attach is unsupported on this platform")

// attachCgroupFD REFUSES on every non-Linux platform, deliberately.
//
// The tempting alternative — return nil and let the command run — would produce
// a sandbox that reports success while confining nothing: resource limits
// silently absent, the caller none the wiser. That is the fail-open shape this
// fleet keeps finding in its own guards, and a sandbox is the worst possible
// place for it. A caller that genuinely wants to proceed unconfined on macOS
// must say so explicitly by handling ErrCgroupUnsupported; it cannot get there
// by accident.
//
// Building the package on darwin is still worth it: everything else here
// (audit, env, fdleak, config, result, runner) is portable, and before this
// split two Linux-only field assignments made the ENTIRE package, and therefore
// every consumer of it, fail to compile on Apple Silicon.
func attachCgroupFD(_ *exec.Cmd, _ *os.File) error {
	return fmt.Errorf("%w (GOOS=%s); cgroups are a Linux kernel primitive with no macOS equivalent — "+
		"Seatbelt/sandbox-exec is a different API and is not a drop-in substitute", ErrCgroupUnsupported, runtime.GOOS)
}
