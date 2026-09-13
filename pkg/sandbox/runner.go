package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	Agent57ID       = 57
	Agent57Name     = "Agent 57: Ephemeral test execution harness"
	RunnerAgentID   = 57
	RunnerAgentName = "Agent 57: Ephemeral test execution harness"

	DefaultMaxOutputBytes = 10 * 1024 * 1024
	DefaultTestTimeout    = 30 * time.Second
)

type StreamType string

const (
	StreamStdout StreamType = "stdout"
	StreamStderr StreamType = "stderr"
)

type StreamEvent struct {
	Stream    StreamType `json:"stream"`
	Data      []byte     `json:"-"`
	Text      string     `json:"text"`
	Timestamp time.Time  `json:"timestamp"`
}

type StreamHandler func(event StreamEvent)

type TestResult struct {
	AgentID         int           `json:"agent_id"`
	AgentName       string        `json:"agent_name"`
	InstanceID      string        `json:"instance_id"`
	Command         []string      `json:"command"`
	ExitCode        int           `json:"exit_code"`
	Stdout          string        `json:"stdout"`
	Stderr          string        `json:"stderr"`
	StdoutBytes     int64         `json:"stdout_bytes"`
	StderrBytes     int64         `json:"stderr_bytes"`
	StdoutTruncated bool          `json:"stdout_truncated"`
	StderrTruncated bool          `json:"stderr_truncated"`
	Duration        time.Duration `json:"duration"`
	DurationMs      float64       `json:"duration_ms"`
	TeardownMs      float64       `json:"teardown_ms"`
	Success         bool          `json:"success"`
	TimedOut        bool          `json:"timed_out"`
	Error           string        `json:"error,omitempty"`
	WorkDir         string        `json:"work_dir,omitempty"`
}

func (r *TestResult) CombinedOutput() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

type RunnerConfig struct {
	SandboxConfig  Config
	WorkDir        string
	EphemeralDir   bool
	Timeout        time.Duration
	MaxOutputBytes int64
	Files          map[string][]byte
	Env            map[string]string
	StdoutWriter   io.Writer
	StderrWriter   io.Writer
	OnStdout       func([]byte)
	OnStderr       func([]byte)
	OnStdoutLine   func(string)
	OnStderrLine   func(string)
	StreamHandler  StreamHandler
	KeepWorkspace  bool
}

func DefaultRunnerConfig() RunnerConfig {
	cfg := DefaultConfig()
	return RunnerConfig{
		SandboxConfig:  cfg,
		EphemeralDir:   true,
		Timeout:        DefaultTestTimeout,
		MaxOutputBytes: DefaultMaxOutputBytes,
		Files:          make(map[string][]byte),
		Env:            make(map[string]string),
	}
}

type Runner struct {
	cfg             RunnerConfig
	sb              *Sandbox
	workDir         string
	ephemeralDir    string
	mu              sync.Mutex
	teardownDone    bool
	teardownLatency time.Duration
}

type Harness = Runner

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTestTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = DefaultMaxOutputBytes
	}

	var ephemeralDir string
	workDir := cfg.WorkDir
	if workDir == "" {
		dir, err := os.MkdirTemp("", fmt.Sprintf("codexkit-agent-57-%d-", time.Now().UnixNano()))
		if err != nil {
			return nil, fmt.Errorf("create ephemeral dir: %w", err)
		}
		ephemeralDir = dir
		workDir = dir
	} else {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return nil, fmt.Errorf("create work dir: %w", err)
		}
	}

	for relPath, content := range cfg.Files {
		fullPath := filepath.Join(workDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			if ephemeralDir != "" {
				_ = os.RemoveAll(ephemeralDir)
			}
			return nil, fmt.Errorf("create parent dir for file %s: %w", relPath, err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			if ephemeralDir != "" {
				_ = os.RemoveAll(ephemeralDir)
			}
			return nil, fmt.Errorf("write file %s: %w", relPath, err)
		}
	}

	sbCfg := cfg.SandboxConfig
	if sbCfg.BwrapPath == "" {
		sbCfg = DefaultConfig()
	}
	sbCfg.HostWorkDir = workDir
	sbCfg.EphemeralWorkDir = false
	if sbCfg.Env == nil {
		sbCfg.Env = make(map[string]string)
	}
	for k, v := range cfg.Env {
		sbCfg.Env[k] = v
	}

	sb, err := New(sbCfg)
	if err != nil {
		if ephemeralDir != "" {
			_ = os.RemoveAll(ephemeralDir)
		}
		return nil, fmt.Errorf("initialize sandbox: %w", err)
	}

	return &Runner{
		cfg:          cfg,
		sb:           sb,
		workDir:      workDir,
		ephemeralDir: ephemeralDir,
	}, nil
}

func NewHarness(cfg RunnerConfig) (*Runner, error) {
	return NewRunner(cfg)
}

func (r *Runner) WorkDir() string {
	return r.workDir
}

func (r *Runner) Sandbox() *Sandbox {
	return r.sb
}

func (r *Runner) PrepareFile(relPath string, content []byte, mode os.FileMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.teardownDone {
		return errors.New("runner already torn down")
	}
	if mode == 0 {
		mode = 0644
	}
	fullPath := filepath.Join(r.workDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create dir for file %s: %w", relPath, err)
	}
	if err := os.WriteFile(fullPath, content, mode); err != nil {
		return fmt.Errorf("write file %s: %w", relPath, err)
	}
	return nil
}

func (r *Runner) Execute(ctx context.Context, command ...string) (*TestResult, error) {
	r.mu.Lock()
	if r.teardownDone {
		r.mu.Unlock()
		return nil, errors.New("runner already torn down")
	}
	r.mu.Unlock()

	if len(command) == 0 {
		return nil, errors.New("command cannot be empty")
	}

	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	args, extraFiles, err := r.sb.BuildBwrapArgs(command)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, f := range extraFiles {
			_ = f.Close()
		}
	}()

	cmd := exec.CommandContext(ctx, r.sb.cfg.BwrapPath, args...)
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = stdoutPipe.Close()
			_ = stderrPipe.Close()
		case <-done:
		}
	}()

	var stdoutBuf, stderrBuf bytes.Buffer
	var stdoutBytes, stderrBytes int64
	var stdoutTruncated, stderrTruncated bool

	var writerMu sync.Mutex
	var handlerMu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		r.streamCapture(
			stdoutPipe,
			StreamStdout,
			r.cfg.MaxOutputBytes,
			r.cfg.StdoutWriter,
			r.cfg.OnStdout,
			r.cfg.OnStdoutLine,
			r.cfg.StreamHandler,
			&writerMu,
			&handlerMu,
			&stdoutBuf,
			&stdoutBytes,
			&stdoutTruncated,
		)
	}()

	go func() {
		defer wg.Done()
		r.streamCapture(
			stderrPipe,
			StreamStderr,
			r.cfg.MaxOutputBytes,
			r.cfg.StderrWriter,
			r.cfg.OnStderr,
			r.cfg.OnStderrLine,
			r.cfg.StreamHandler,
			&writerMu,
			&handlerMu,
			&stderrBuf,
			&stderrBytes,
			&stderrTruncated,
		)
	}()

	start := time.Now()
	runErr := cmd.Start()
	if runErr != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, fmt.Errorf("start command: %w", runErr)
	}

	wg.Wait()
	waitErr := cmd.Wait()
	duration := time.Since(start)

	timedOut := false
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		timedOut = true
	}

	exitCode := 0
	errStr := ""
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
		errStr = waitErr.Error()
	} else if timedOut {
		exitCode = -1
		errStr = "execution timed out"
	}

	res := &TestResult{
		AgentID:         Agent57ID,
		AgentName:       Agent57Name,
		InstanceID:      r.sb.InstanceID(),
		Command:         command,
		ExitCode:        exitCode,
		Stdout:          stdoutBuf.String(),
		Stderr:          stderrBuf.String(),
		StdoutBytes:     stdoutBytes,
		StderrBytes:     stderrBytes,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
		Duration:        duration,
		DurationMs:      float64(duration.Microseconds()) / 1000.0,
		TeardownMs:      float64(r.teardownLatency.Microseconds()) / 1000.0,
		Success:         exitCode == 0 && waitErr == nil && !timedOut,
		TimedOut:        timedOut,
		Error:           errStr,
		WorkDir:         r.workDir,
	}

	return res, nil
}

func (r *Runner) ExecuteScript(ctx context.Context, script string) (*TestResult, error) {
	return r.Execute(ctx, "/bin/sh", "-c", script)
}

func (r *Runner) streamCapture(
	reader io.Reader,
	streamType StreamType,
	maxBytes int64,
	writer io.Writer,
	onChunk func([]byte),
	onLine func(string),
	streamHandler StreamHandler,
	writerMu *sync.Mutex,
	handlerMu *sync.Mutex,
	buf *bytes.Buffer,
	totalBytes *int64,
	truncated *bool,
) {
	var pending []byte
	chunk := make([]byte, 4096)

	processLine := func(data []byte) {
		if onLine == nil {
			return
		}
		pending = append(pending, data...)
		for {
			idx := bytes.IndexByte(pending, '\n')
			if idx < 0 {
				break
			}
			line := string(pending[:idx])
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			onLine(line)
			pending = pending[idx+1:]
		}
	}

	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			raw := chunk[:n]
			*totalBytes += int64(n)

			if maxBytes > 0 && int64(buf.Len()) >= maxBytes {
				*truncated = true
			} else if maxBytes > 0 && int64(buf.Len()+n) > maxBytes {
				*truncated = true
				allowed := maxBytes - int64(buf.Len())
				buf.Write(raw[:allowed])
			} else {
				buf.Write(raw)
			}

			if writer != nil {
				writerMu.Lock()
				_, _ = writer.Write(raw)
				writerMu.Unlock()
			}

			if onChunk != nil {
				onChunk(raw)
			}

			if onLine != nil {
				processLine(raw)
			}

			if streamHandler != nil {
				handlerMu.Lock()
				streamHandler(StreamEvent{
					Stream:    streamType,
					Data:      raw,
					Text:      string(raw),
					Timestamp: time.Now(),
				})
				handlerMu.Unlock()
			}
		}

		if err != nil {
			break
		}
	}

	if onLine != nil && len(pending) > 0 {
		line := string(pending)
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		onLine(line)
		pending = nil
	}
}

func (r *Runner) Teardown() (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.teardownDone {
		return r.teardownLatency, nil
	}

	start := time.Now()
	var err error
	if r.sb != nil {
		_, err = r.sb.Teardown()
	}
	if r.ephemeralDir != "" && !r.cfg.KeepWorkspace {
		rmErr := os.RemoveAll(r.ephemeralDir)
		if err == nil {
			err = rmErr
		}
	}

	r.teardownDone = true
	r.teardownLatency = time.Since(start)

	if r.teardownLatency > 50*time.Millisecond {
		return r.teardownLatency, fmt.Errorf("teardown SLA violated: latency %v exceeded 50ms ceiling", r.teardownLatency)
	}

	return r.teardownLatency, err
}

func (r *Runner) Close() error {
	_, err := r.Teardown()
	return err
}

func RunEphemeralTest(ctx context.Context, cfg RunnerConfig, command ...string) (*TestResult, error) {
	r, err := NewRunner(cfg)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	res, err := r.Execute(ctx, command...)
	if res != nil {
		tdDur, tdErr := r.Teardown()
		res.TeardownMs = float64(tdDur.Microseconds()) / 1000.0
		if tdErr != nil && res.Error == "" {
			res.Error = tdErr.Error()
		}
	}
	return res, err
}

func RunEphemeralScript(ctx context.Context, cfg RunnerConfig, script string) (*TestResult, error) {
	return RunEphemeralTest(ctx, cfg, "/bin/sh", "-c", script)
}
