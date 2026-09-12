package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Sandbox struct {
	cfg             Config
	ephemeralDir    string
	mu              sync.Mutex
	teardownDone    bool
	teardownLatency time.Duration
}

func New(cfg Config) (*Sandbox, error) {
	if cfg.BwrapPath == "" {
		p, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, fmt.Errorf("bwrap binary not found: %w", err)
		}
		cfg.BwrapPath = p
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = generateInstanceID()
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "codexkit-agent-P26"
	}
	if cfg.RootMode == "" {
		cfg.RootMode = RootModeHostRO
	}

	var ephemeralDir string
	if cfg.EphemeralWorkDir && cfg.HostWorkDir == "" {
		dir, err := os.MkdirTemp("", fmt.Sprintf("%s-", cfg.InstanceID))
		if err != nil {
			return nil, fmt.Errorf("create ephemeral work dir: %w", err)
		}
		ephemeralDir = dir
		cfg.HostWorkDir = dir
	} else if cfg.HostWorkDir != "" {
		if _, err := os.Stat(cfg.HostWorkDir); os.IsNotExist(err) {
			if err := os.MkdirAll(cfg.HostWorkDir, 0755); err != nil {
				return nil, fmt.Errorf("create host work dir: %w", err)
			}
		}
	}

	return &Sandbox{
		cfg:          cfg,
		ephemeralDir: ephemeralDir,
	}, nil
}

func (s *Sandbox) InstanceID() string {
	return s.cfg.InstanceID
}

func (s *Sandbox) WorkDir() string {
	return s.cfg.HostWorkDir
}

func (s *Sandbox) BuildBwrapArgs(command []string) ([]string, []*os.File, error) {
	if len(command) == 0 {
		return nil, nil, errors.New("command cannot be empty")
	}

	var args []string
	var extraFiles []*os.File

	if s.cfg.UnshareUser {
		args = append(args, "--unshare-user", "--uid", fmt.Sprint(s.cfg.UID), "--gid", fmt.Sprint(s.cfg.GID))
	}
	if s.cfg.UnsharePID {
		args = append(args, "--unshare-pid")
	}
	if s.cfg.UnshareIPC {
		args = append(args, "--unshare-ipc")
	}
	if s.cfg.UnshareNet {
		args = append(args, "--unshare-net")
	}
	if s.cfg.UnshareUTS {
		args = append(args, "--unshare-uts", "--hostname", s.cfg.Hostname)
	}
	if s.cfg.UnshareCgroup {
		args = append(args, "--unshare-cgroup-try")
	}
	if s.cfg.DieWithParent {
		args = append(args, "--die-with-parent")
	}
	if s.cfg.NewSession {
		args = append(args, "--new-session")
	}

	if s.cfg.RootMode == RootModeMinimal {
		args = append(args,
			"--tmpfs", "/",
			"--ro-bind", "/usr", "/usr",
			"--symlink", "usr/bin", "/bin",
			"--symlink", "usr/bin", "/sbin",
			"--symlink", "usr/lib", "/lib",
			"--ro-bind-try", "/lib64", "/lib64",
			"--ro-bind-try", "/etc", "/etc",
		)
	} else {
		args = append(args, "--ro-bind", "/", "/")
	}

	args = append(args, "--dev", "/dev", "--proc", "/proc")

	for _, tmpPath := range s.cfg.TmpfsPaths {
		args = append(args, "--tmpfs", tmpPath)
	}

	if s.cfg.HostWorkDir != "" {
		targetDir := s.cfg.ContainerWorkDir
		if targetDir == "" {
			targetDir = s.cfg.HostWorkDir
		}

		if s.cfg.RootMode == RootModeHostRO {
			if strings.HasPrefix(targetDir, "/tmp") || strings.HasPrefix(targetDir, "/dev/shm") {
				args = append(args, "--dir", targetDir, "--bind", s.cfg.HostWorkDir, targetDir, "--chdir", targetDir)
			} else if targetDir == s.cfg.HostWorkDir {
				args = append(args, "--bind", s.cfg.HostWorkDir, targetDir, "--chdir", targetDir)
			} else {
				fallbackTarget := "/tmp/agent-work"
				args = append(args, "--dir", fallbackTarget, "--bind", s.cfg.HostWorkDir, fallbackTarget, "--chdir", fallbackTarget)
			}
		} else {
			args = append(args, "--dir", targetDir, "--bind", s.cfg.HostWorkDir, targetDir, "--chdir", targetDir)
		}
	}

	for _, m := range s.cfg.Mounts {
		switch m.Type {
		case MountTypeRoBind:
			args = append(args, "--ro-bind", m.Source, m.Destination)
		case MountTypeBind:
			args = append(args, "--bind", m.Source, m.Destination)
		case MountTypeTmpfs:
			args = append(args, "--tmpfs", m.Destination)
		case MountTypeDev:
			args = append(args, "--dev", m.Destination)
		case MountTypeProc:
			args = append(args, "--proc", m.Destination)
		}
	}

	if s.cfg.ClearEnv {
		args = append(args, "--clearenv")
	}
	for k, v := range s.cfg.Env {
		args = append(args, "--setenv", k, v)
	}

	if len(s.cfg.ApprovedProxyLoopbacks) > 0 {
		proxy := s.cfg.ApprovedProxyLoopbacks[0]
		if strings.HasPrefix(proxy, "/") {
			args = append(args, "--ro-bind-try", proxy, proxy)
		}
		args = append(args,
			"--setenv", "HTTP_PROXY", proxy,
			"--setenv", "HTTPS_PROXY", proxy,
			"--setenv", "http_proxy", proxy,
			"--setenv", "https_proxy", proxy,
			"--setenv", "ALL_PROXY", proxy,
		)
	}

	if s.cfg.EnableSeccomp && runtime.GOARCH == "amd64" {
		filterBytes, err := BuildSeccompFilter(s.cfg.DeniedSyscalls)
		if err != nil {
			return nil, nil, fmt.Errorf("build seccomp filter: %w", err)
		}
		if len(filterBytes) > 0 {
			r, w, err := os.Pipe()
			if err != nil {
				return nil, nil, fmt.Errorf("create seccomp pipe: %w", err)
			}
			if _, err := w.Write(filterBytes); err != nil {
				r.Close()
				w.Close()
				return nil, nil, fmt.Errorf("write seccomp filter: %w", err)
			}
			w.Close()

			extraFiles = append(extraFiles, r)
			fdIndex := 3 + len(extraFiles) - 1
			args = append(args, "--seccomp", fmt.Sprintf("%d", fdIndex))
		}
	}

	args = append(args, "--")
	args = append(args, command...)

	return args, extraFiles, nil
}

func (s *Sandbox) Run(ctx context.Context, command ...string) (*RunResult, error) {
	s.mu.Lock()
	if s.teardownDone {
		s.mu.Unlock()
		return nil, errors.New("sandbox already torn down")
	}
	s.mu.Unlock()

	if s.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
	}

	args, extraFiles, err := s.BuildBwrapArgs(command)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, f := range extraFiles {
			_ = f.Close()
		}
	}()

	cmd := exec.CommandContext(ctx, s.cfg.BwrapPath, args...)
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	errStr := ""
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
		errStr = runErr.Error()
	}

	res := &RunResult{
		InstanceID: s.cfg.InstanceID,
		Command:    command,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		Duration:   duration,
		DurationMs: float64(duration.Microseconds()) / 1000.0,
		TeardownMs: float64(s.teardownLatency.Microseconds()) / 1000.0,
		Success:    exitCode == 0 && runErr == nil,
		Error:      errStr,
	}

	return res, nil
}

func (s *Sandbox) RunScript(ctx context.Context, script string) (*RunResult, error) {
	return s.Run(ctx, "/bin/sh", "-c", script)
}

func (s *Sandbox) Teardown() (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.teardownDone {
		return s.teardownLatency, nil
	}

	start := time.Now()
	var err error
	if s.ephemeralDir != "" {
		err = os.RemoveAll(s.ephemeralDir)
	}
	s.teardownDone = true
	s.teardownLatency = time.Since(start)

	if s.teardownLatency > 50*time.Millisecond {
		return s.teardownLatency, fmt.Errorf("teardown SLA violated: latency %v exceeded 50ms ceiling", s.teardownLatency)
	}

	return s.teardownLatency, err
}

func (s *Sandbox) Close() error {
	_, err := s.Teardown()
	return err
}
