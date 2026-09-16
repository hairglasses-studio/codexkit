package sandbox

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunnerAgentConstants(t *testing.T) {
	if Agent57ID != 57 {
		t.Fatalf("expected Agent57ID 57, got %d", Agent57ID)
	}
	if RunnerAgentID != 57 {
		t.Fatalf("expected RunnerAgentID 57, got %d", RunnerAgentID)
	}
	if !strings.Contains(Agent57Name, "Ephemeral test execution harness") {
		t.Fatalf("unexpected Agent57Name: %s", Agent57Name)
	}
	if !strings.Contains(RunnerAgentName, "Agent 57") {
		t.Fatalf("unexpected RunnerAgentName: %s", RunnerAgentName)
	}
}

func TestRunner_StdoutStderrIsolation(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := "echo 'stdout line 1'; echo 'stderr line 1' >&2; echo 'stdout line 2'; echo 'stderr line 2' >&2"
	res, err := r.ExecuteScript(ctx, cmd)
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected success, got exit %d: %s", res.ExitCode, res.Error)
	}
	if res.AgentID != 57 {
		t.Fatalf("expected AgentID 57, got %d", res.AgentID)
	}

	if !strings.Contains(res.Stdout, "stdout line 1") || !strings.Contains(res.Stdout, "stdout line 2") {
		t.Fatalf("stdout missing expected output: %q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "stderr") {
		t.Fatalf("stdout contains stderr leakage: %q", res.Stdout)
	}

	if !strings.Contains(res.Stderr, "stderr line 1") || !strings.Contains(res.Stderr, "stderr line 2") {
		t.Fatalf("stderr missing expected output: %q", res.Stderr)
	}
	if strings.Contains(res.Stderr, "stdout") {
		t.Fatalf("stderr contains stdout leakage: %q", res.Stderr)
	}
}

func TestRunner_StreamingCallbacks(t *testing.T) {
	requireBwrap(t)

	var mu sync.Mutex
	var stdoutChunks []string
	var stderrChunks []string
	var stdoutLines []string
	var stderrLines []string
	var events []StreamEvent

	cfg := DefaultRunnerConfig()
	cfg.OnStdout = func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		stdoutChunks = append(stdoutChunks, string(b))
	}
	cfg.OnStderr = func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		stderrChunks = append(stderrChunks, string(b))
	}
	cfg.OnStdoutLine = func(line string) {
		mu.Lock()
		defer mu.Unlock()
		stdoutLines = append(stdoutLines, line)
	}
	cfg.OnStderrLine = func(line string) {
		mu.Lock()
		defer mu.Unlock()
		stderrLines = append(stderrLines, line)
	}
	cfg.StreamHandler = func(event StreamEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	var stdoutWriterBuf bytes.Buffer
	var stderrWriterBuf bytes.Buffer
	cfg.StdoutWriter = &stdoutWriterBuf
	cfg.StderrWriter = &stderrWriterBuf

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	script := `
echo "alpha"
echo "beta" >&2
echo "gamma"
echo "delta" >&2
`
	res, err := r.ExecuteScript(ctx, script)
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got exit %d: %s", res.ExitCode, res.Error)
	}

	mu.Lock()
	defer mu.Unlock()

	joinedStdoutChunks := strings.Join(stdoutChunks, "")
	if !strings.Contains(joinedStdoutChunks, "alpha") || !strings.Contains(joinedStdoutChunks, "gamma") {
		t.Fatalf("stdout chunks missing data: %q", joinedStdoutChunks)
	}

	joinedStderrChunks := strings.Join(stderrChunks, "")
	if !strings.Contains(joinedStderrChunks, "beta") || !strings.Contains(joinedStderrChunks, "delta") {
		t.Fatalf("stderr chunks missing data: %q", joinedStderrChunks)
	}

	if len(stdoutLines) < 2 {
		t.Fatalf("expected at least 2 stdout lines, got %d: %v", len(stdoutLines), stdoutLines)
	}
	if stdoutLines[0] != "alpha" || stdoutLines[1] != "gamma" {
		t.Fatalf("unexpected stdout lines: %v", stdoutLines)
	}

	if len(stderrLines) < 2 {
		t.Fatalf("expected at least 2 stderr lines, got %d: %v", len(stderrLines), stderrLines)
	}
	if stderrLines[0] != "beta" || stderrLines[1] != "delta" {
		t.Fatalf("unexpected stderr lines: %v", stderrLines)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 stream events, got %d", len(events))
	}
	hasStdout := false
	hasStderr := false
	for _, ev := range events {
		if ev.Stream == StreamStdout {
			hasStdout = true
		}
		if ev.Stream == StreamStderr {
			hasStderr = true
		}
	}
	if !hasStdout || !hasStderr {
		t.Fatalf("missing stdout or stderr event: %+v", events)
	}

	if !strings.Contains(stdoutWriterBuf.String(), "alpha") {
		t.Fatalf("StdoutWriter missing data: %s", stdoutWriterBuf.String())
	}
	if !strings.Contains(stderrWriterBuf.String(), "beta") {
		t.Fatalf("StderrWriter missing data: %s", stderrWriterBuf.String())
	}
}

func TestRunner_OutputTruncationSafety(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	cfg.MaxOutputBytes = 128

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	script := `
python3 -c "import sys; sys.stdout.write('X' * 50000); sys.stderr.write('Y' * 50000)"
`
	res, err := r.ExecuteScript(ctx, script)
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got exit %d: %s", res.ExitCode, res.Error)
	}

	if !res.StdoutTruncated {
		t.Fatalf("expected StdoutTruncated true, got false")
	}
	if !res.StderrTruncated {
		t.Fatalf("expected StderrTruncated true, got false")
	}

	if int64(len(res.Stdout)) != 128 {
		t.Fatalf("expected stdout length 128, got %d", len(res.Stdout))
	}
	if int64(len(res.Stderr)) != 128 {
		t.Fatalf("expected stderr length 128, got %d", len(res.Stderr))
	}

	if res.StdoutBytes < 50000 {
		t.Fatalf("expected StdoutBytes >= 50000, got %d", res.StdoutBytes)
	}
	if res.StderrBytes < 50000 {
		t.Fatalf("expected StderrBytes >= 50000, got %d", res.StderrBytes)
	}
}

func TestRunner_EphemeralWorkspaceLifecycle(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	cfg.EphemeralDir = true

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	workDir := r.WorkDir()
	if workDir == "" {
		t.Fatalf("expected non-empty workDir")
	}
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Fatalf("workDir does not exist: %s", workDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := r.ExecuteScript(ctx, "echo 'ephemeral run' > test.log && cat test.log")
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}
	if !res.Success || strings.TrimSpace(res.Stdout) != "ephemeral run" {
		t.Fatalf("unexpected result: %+v", res)
	}

	tdDur, err := r.Teardown()
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if tdDur > 50*time.Millisecond {
		t.Fatalf("teardown SLA violated: %v > 50ms", tdDur)
	}

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("ephemeral workDir still exists after teardown: %s", workDir)
	}

	tdDur2, err := r.Teardown()
	if err != nil || tdDur2 != tdDur {
		t.Fatalf("expected idempotent teardown, got %v, err %v", tdDur2, err)
	}

	_, err = r.Execute(ctx, "/bin/true")
	if err == nil {
		t.Fatalf("expected Execute after Teardown to fail")
	}
}

func TestRunner_PrepareFileAndFilesConfig(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	cfg.Files = map[string][]byte{
		"config/params.json": []byte(`{"env":"test"}`),
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	testScript := []byte("#!/bin/sh\ncat config/params.json\n")
	if err := r.PrepareFile("run_check.sh", testScript, 0755); err != nil {
		t.Fatalf("PrepareFile failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := r.Execute(ctx, "/bin/sh", "run_check.sh")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute failed with exit %d: %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != `{"env":"test"}` {
		t.Fatalf("unexpected stdout: %s", res.Stdout)
	}
}

func TestRunner_ExitCodeAndFailure(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := r.ExecuteScript(ctx, "echo 'error message' >&2; exit 42")
	if err != nil {
		t.Fatalf("ExecuteScript returned error: %v", err)
	}
	if res.Success {
		t.Fatalf("expected Success false, got true")
	}
	if res.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "error message") {
		t.Fatalf("stderr missing expected error: %s", res.Stderr)
	}
}

func TestRunner_Timeout(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	cfg.Timeout = 500 * time.Millisecond

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	defer r.Close()

	ctx := context.Background()
	res, err := r.Execute(ctx, "sleep", "5")
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut true, got false")
	}
	if res.Success {
		t.Fatalf("expected Success false on timeout")
	}
}

func TestRunner_CombinedOutput(t *testing.T) {
	r1 := &TestResult{Stdout: "out", Stderr: ""}
	if r1.CombinedOutput() != "out" {
		t.Fatalf("expected 'out', got %q", r1.CombinedOutput())
	}

	r2 := &TestResult{Stdout: "", Stderr: "err"}
	if r2.CombinedOutput() != "err" {
		t.Fatalf("expected 'err', got %q", r2.CombinedOutput())
	}

	r3 := &TestResult{Stdout: "out", Stderr: "err"}
	if r3.CombinedOutput() != "out\nerr" {
		t.Fatalf("expected 'out\\nerr', got %q", r3.CombinedOutput())
	}
}

func TestRunEphemeralHelper(t *testing.T) {
	requireBwrap(t)

	cfg := DefaultRunnerConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := RunEphemeralScript(ctx, cfg, "echo -n 'one-shot test'")
	if err != nil {
		t.Fatalf("RunEphemeralScript failed: %v", err)
	}
	if !res.Success || res.Stdout != "one-shot test" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.TeardownMs <= 0 && res.TeardownMs > 50.0 {
		t.Fatalf("unexpected teardown duration: %v", res.TeardownMs)
	}
}
