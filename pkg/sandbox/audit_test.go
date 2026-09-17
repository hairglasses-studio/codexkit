package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuditAgentConstants(t *testing.T) {
	if AuditAgentID != 60 {
		t.Fatalf("expected AuditAgentID 60, got %d", AuditAgentID)
	}
	if AuditAgentName != "Agent 60: Sandbox execution security audit logger" {
		t.Fatalf("unexpected AuditAgentName: %s", AuditAgentName)
	}
}

func TestExtractSecurityAttributes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InstanceID = "inst-test-123"
	cfg.Hostname = "test-host"
	cfg.HostWorkDir = "/tmp/host-work"
	cfg.ContainerWorkDir = "/workspace"
	cfg.EphemeralWorkDir = false
	cfg.ClearEnv = true
	cfg.Timeout = 15 * time.Second

	attr := ExtractSecurityAttributes(cfg)
	if attr.InstanceID != "inst-test-123" {
		t.Fatalf("expected instance id inst-test-123, got %s", attr.InstanceID)
	}
	if attr.Hostname != "test-host" {
		t.Fatalf("expected hostname test-host, got %s", attr.Hostname)
	}
	if !attr.ReadOnlyRoot {
		t.Fatalf("expected read only root true")
	}
	if !attr.NetworkUnshared {
		t.Fatalf("expected network unshared true")
	}
	if !attr.UserUnshared {
		t.Fatalf("expected user unshared true")
	}
	if !attr.PIDUnshared {
		t.Fatalf("expected pid unshared true")
	}
	if !attr.IPCUnshared {
		t.Fatalf("expected ipc unshared true")
	}
	if !attr.UTSUnshared {
		t.Fatalf("expected uts unshared true")
	}
	if !attr.CgroupUnshared {
		t.Fatalf("expected cgroup unshared true")
	}
	if !attr.SeccompEnabled {
		t.Fatalf("expected seccomp enabled true")
	}
	if attr.DeniedSyscallsCount != len(cfg.DeniedSyscalls) {
		t.Fatalf("expected denied syscalls count %d, got %d", len(cfg.DeniedSyscalls), attr.DeniedSyscallsCount)
	}
	if !attr.ClearEnv {
		t.Fatalf("expected clear env true")
	}
	if attr.TimeoutSeconds != 15.0 {
		t.Fatalf("expected timeout 15.0, got %f", attr.TimeoutSeconds)
	}
}

func TestAuditLogger_RecordSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewAuditLogger(WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	cfg := DefaultConfig()
	res := &RunResult{
		InstanceID: cfg.InstanceID,
		Command:    []string{"echo", "hello"},
		ExitCode:   0,
		Stdout:     "hello\n",
		Stderr:     "",
		DurationMs: 12.5,
		TeardownMs: 1.2,
		Success:    true,
	}

	rec, err := logger.Record(cfg, res)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if rec.AuditID == "" {
		t.Fatalf("expected non-empty audit id")
	}
	if rec.AgentID != 60 {
		t.Fatalf("expected agent id 60, got %d", rec.AgentID)
	}
	if !rec.PassedSecurity {
		t.Fatalf("expected passed security true, got violations: %+v", rec.Violations)
	}
	if rec.ViolationCount != 0 {
		t.Fatalf("expected 0 violations, got %d", rec.ViolationCount)
	}
	if len(rec.Violations) != 0 {
		t.Fatalf("expected empty violations, got %+v", rec.Violations)
	}

	counters := logger.Counters()
	if counters.TotalViolations != 0 {
		t.Fatalf("expected 0 total violations, got %d", counters.TotalViolations)
	}

	var loggedRecord AuditRecord
	if err := json.Unmarshal(buf.Bytes(), &loggedRecord); err != nil {
		t.Fatalf("failed to unmarshal JSONL record: %v", err)
	}
	if loggedRecord.AuditID != rec.AuditID {
		t.Fatalf("expected audit id %s, got %s", rec.AuditID, loggedRecord.AuditID)
	}
}

func TestAuditLogger_DetectViolations(t *testing.T) {
	logger, err := NewAuditLogger()
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	t.Run("SensitiveEnvLeak", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Env["GITHUB_TOKEN"] = "ghp_123456"
		cfg.Env["OPENAI_API_KEY"] = "sk-abcdef"
		violations := logger.DetectViolations(cfg, nil)
		if len(violations) < 2 {
			t.Fatalf("expected at least 2 env violations, got %d", len(violations))
		}
		for _, v := range violations {
			if v.Type != ViolationSensitiveEnvLeak {
				t.Fatalf("expected violation type %s, got %s", ViolationSensitiveEnvLeak, v.Type)
			}
		}
	})

	t.Run("UnsafeMounts", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Mounts = []Mount{
			{Type: MountTypeBind, Source: "/var/app", Destination: "/"},
			{Type: MountTypeRoBind, Source: "/var/log/../../etc/shadow", Destination: "/tmp/shadow"},
			{Type: MountTypeBind, Source: "/root/.ssh", Destination: "/root/.ssh"},
		}
		violations := logger.DetectViolations(cfg, nil)
		if len(violations) < 3 {
			t.Fatalf("expected at least 3 mount violations, got %d", len(violations))
		}
		for _, v := range violations {
			if v.Type != ViolationUnsafeMount {
				t.Fatalf("expected violation type %s, got %s", ViolationUnsafeMount, v.Type)
			}
		}
	})

	t.Run("PrivilegeEscalation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UnshareUser = false
		violations := logger.DetectViolations(cfg, nil)
		if len(violations) == 0 {
			t.Fatalf("expected privilege escalation violation")
		}
		found := false
		for _, v := range violations {
			if v.Type == ViolationPrivilegeEscalate {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("did not find %s violation", ViolationPrivilegeEscalate)
		}
	})

	t.Run("SyscallSIGSYS", func(t *testing.T) {
		cfg := DefaultConfig()
		res := &RunResult{
			Command:    []string{"evil_cmd"},
			ExitCode:   159,
			Stderr:     "Bad system call\n",
			Success:    false,
			DurationMs: 5.0,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationSyscallBlocked {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation for SIGSYS exit", ViolationSyscallBlocked)
		}
	})

	t.Run("SyscallEPERM", func(t *testing.T) {
		cfg := DefaultConfig()
		res := &RunResult{
			Command:    []string{"python3", "-c", "import ctypes; ..."},
			ExitCode:   0,
			Stdout:     "res=-1 errno=1\n",
			Success:    true,
			DurationMs: 10.0,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationSyscallBlocked {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation for errno=1", ViolationSyscallBlocked)
		}
	})

	t.Run("FilesystemWriteDenied", func(t *testing.T) {
		cfg := DefaultConfig()
		res := &RunResult{
			Command:    []string{"touch", "/test_root_file"},
			ExitCode:   1,
			Stderr:     "touch: cannot touch '/test_root_file': Read-only file system\n",
			Success:    false,
			DurationMs: 3.0,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationFilesystemWrite {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation", ViolationFilesystemWrite)
		}
	})

	t.Run("NetworkEgressAttempted", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UnshareNet = true
		res := &RunResult{
			Command:    []string{"curl", "http://1.1.1.1"},
			ExitCode:   7,
			Stderr:     "curl: (7) Couldn't connect to server: Network is unreachable\n",
			Success:    false,
			DurationMs: 15.0,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationNetworkEgress {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation", ViolationNetworkEgress)
		}
	})

	t.Run("ExecutionTimeout", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Timeout = 2 * time.Second
		res := &RunResult{
			Command:    []string{"sleep", "10"},
			ExitCode:   -1,
			Error:      "context deadline exceeded",
			Duration:   3 * time.Second,
			DurationMs: 3000.0,
			Success:    false,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationExecutionTimeout {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation", ViolationExecutionTimeout)
		}
	})

	t.Run("TeardownSLAViolation", func(t *testing.T) {
		cfg := DefaultConfig()
		res := &RunResult{
			Command:    []string{"/bin/true"},
			ExitCode:   0,
			DurationMs: 5.0,
			TeardownMs: 65.4,
			Success:    true,
		}
		violations := logger.DetectViolations(cfg, res)
		found := false
		for _, v := range violations {
			if v.Type == ViolationTeardownSLA {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s violation", ViolationTeardownSLA)
		}
	})
}

func TestAuditLogger_ViolationCounters(t *testing.T) {
	logger, err := NewAuditLogger()
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	cfg1 := DefaultConfig()
	res1 := &RunResult{
		Command:  []string{"touch", "/etc/foo"},
		ExitCode: 1,
		Stderr:   "Read-only file system",
		Success:  false,
	}
	if _, err := logger.Record(cfg1, res1); err != nil {
		t.Fatalf("Record 1 failed: %v", err)
	}

	cfg2 := DefaultConfig()
	cfg2.UnshareNet = true
	res2 := &RunResult{
		Command:  []string{"curl", "http://google.com"},
		ExitCode: 1,
		Stderr:   "Network is unreachable",
		Success:  false,
	}
	if _, err := logger.Record(cfg2, res2); err != nil {
		t.Fatalf("Record 2 failed: %v", err)
	}

	cfg3 := DefaultConfig()
	cfg3.Env["AWS_SECRET_ACCESS_KEY"] = "supersecret"
	res3 := &RunResult{
		Command:  []string{"echo", "ok"},
		ExitCode: 0,
		Success:  true,
	}
	if _, err := logger.Record(cfg3, res3); err != nil {
		t.Fatalf("Record 3 failed: %v", err)
	}

	cfg4 := DefaultConfig()
	res4 := &RunResult{
		Command:    []string{"/bin/true"},
		ExitCode:   0,
		Success:    true,
		TeardownMs: 70.0,
	}
	if _, err := logger.Record(cfg4, res4); err != nil {
		t.Fatalf("Record 4 failed: %v", err)
	}

	counters := logger.Counters()
	if counters.FilesystemWrite != 1 {
		t.Fatalf("expected 1 FilesystemWrite, got %d", counters.FilesystemWrite)
	}
	if counters.NetworkEgress != 1 {
		t.Fatalf("expected 1 NetworkEgress, got %d", counters.NetworkEgress)
	}
	if counters.SensitiveEnvLeak != 1 {
		t.Fatalf("expected 1 SensitiveEnvLeak, got %d", counters.SensitiveEnvLeak)
	}
	if counters.TeardownSLA != 1 {
		t.Fatalf("expected 1 TeardownSLA, got %d", counters.TeardownSLA)
	}
	if counters.TotalViolations != 4 {
		t.Fatalf("expected 4 TotalViolations, got %d", counters.TotalViolations)
	}

	summary := logger.Summary()
	if summary.TotalAudits != 4 {
		t.Fatalf("expected 4 total audits, got %d", summary.TotalAudits)
	}
	if summary.PassedCount != 0 {
		t.Fatalf("expected 0 passed, got %d", summary.PassedCount)
	}
	if summary.FailedCount != 4 {
		t.Fatalf("expected 4 failed, got %d", summary.FailedCount)
	}
	if summary.ViolationRate != 1.0 {
		t.Fatalf("expected violation rate 1.0, got %f", summary.ViolationRate)
	}
}

func TestAuditLogger_JSONLOutput(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewAuditLogger(WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	cfg := DefaultConfig()
	for i := 0; i < 3; i++ {
		res := &RunResult{
			Command:    []string{"echo", "test"},
			ExitCode:   0,
			Stdout:     "test\n",
			Success:    true,
			DurationMs: 1.0,
			TeardownMs: 0.5,
		}
		if _, err := logger.Record(cfg, res); err != nil {
			t.Fatalf("iteration %d: record failed: %v", i, err)
		}
	}

	scanner := bufio.NewScanner(&buf)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lineCount++
		var rec AuditRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("line %d invalid JSON: %v", lineCount, err)
		}
		if rec.AgentID != 60 {
			t.Fatalf("line %d agent id expected 60, got %d", lineCount, rec.AgentID)
		}
		if !rec.PassedSecurity {
			t.Fatalf("line %d expected passed security", lineCount)
		}
	}
	if lineCount != 3 {
		t.Fatalf("expected 3 JSON lines, got %d", lineCount)
	}
}

func TestAuditLogger_ExportJSONAndSummary(t *testing.T) {
	logger, err := NewAuditLogger()
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	cfg := DefaultConfig()
	resSuccess := &RunResult{
		Command:  []string{"echo", "ok"},
		ExitCode: 0,
		Success:  true,
	}
	if _, err := logger.Record(cfg, resSuccess); err != nil {
		t.Fatalf("record success failed: %v", err)
	}

	cfgBad := DefaultConfig()
	cfgBad.UnshareUser = false
	resBad := &RunResult{
		Command:  []string{"whoami"},
		ExitCode: 0,
		Success:  true,
	}
	if _, err := logger.Record(cfgBad, resBad); err != nil {
		t.Fatalf("record bad failed: %v", err)
	}

	jsonBytes, err := logger.ExportJSON()
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}
	var records []AuditRecord
	if err := json.Unmarshal(jsonBytes, &records); err != nil {
		t.Fatalf("failed to unmarshal exported JSON: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 exported records, got %d", len(records))
	}

	jsonlBytes, err := logger.ExportJSONLines()
	if err != nil {
		t.Fatalf("ExportJSONLines failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(jsonlBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d", len(lines))
	}

	summary := logger.Summary()
	if summary.TotalAudits != 2 {
		t.Fatalf("expected 2 total audits, got %d", summary.TotalAudits)
	}
	if summary.PassedCount != 1 || summary.FailedCount != 1 {
		t.Fatalf("expected 1 passed and 1 failed, got %d passed, %d failed", summary.PassedCount, summary.FailedCount)
	}
	if summary.ViolationRate != 0.5 {
		t.Fatalf("expected violation rate 0.5, got %f", summary.ViolationRate)
	}

	logger.Reset()
	if len(logger.Records()) != 0 {
		t.Fatalf("expected 0 records after Reset")
	}
	if logger.Counters().TotalViolations != 0 {
		t.Fatalf("expected 0 total violations after Reset")
	}
}

func TestAuditLogger_FilePersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-file-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "sandbox-audit.jsonl")
	logger, err := NewAuditLogger(WithFilePath(logPath))
	if err != nil {
		t.Fatalf("NewAuditLogger with file path failed: %v", err)
	}

	cfg := DefaultConfig()
	res := &RunResult{
		Command:  []string{"date"},
		ExitCode: 0,
		Success:  true,
	}
	if _, err := logger.Record(cfg, res); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var rec AuditRecord
	if err := json.Unmarshal(content, &rec); err != nil {
		t.Fatalf("unmarshal from file failed: %v", err)
	}
	if rec.AgentID != 60 {
		t.Fatalf("expected agent id 60, got %d", rec.AgentID)
	}
}

func TestAuditLogger_Concurrency(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewAuditLogger(WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	var wg sync.WaitGroup
	workers := 10
	recordsPerWorker := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			cfg := DefaultConfig()
			for j := 0; j < recordsPerWorker; j++ {
				res := &RunResult{
					Command:  []string{"echo", "concurrent"},
					ExitCode: 0,
					Success:  true,
				}
				if _, err := logger.Record(cfg, res); err != nil {
					t.Errorf("worker %d record %d failed: %v", workerID, j, err)
				}
			}
		}(i)
	}

	wg.Wait()

	records := logger.Records()
	expectedTotal := workers * recordsPerWorker
	if len(records) != expectedTotal {
		t.Fatalf("expected %d records, got %d", expectedTotal, len(records))
	}
}

func TestAuditLogger_LiveSandboxExecution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sandbox requires linux")
	}
	requireBwrap(t)

	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	var buf bytes.Buffer
	logger, err := NewAuditLogger(WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewAuditLogger failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("AuditedCleanRun", func(t *testing.T) {
		res, rec, err := sb.RunWithAudit(ctx, logger, "/bin/echo", "clean execution")
		if err != nil {
			t.Fatalf("RunWithAudit failed: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected success, got exit %d", res.ExitCode)
		}
		if !rec.PassedSecurity {
			t.Fatalf("expected passed security, got violations: %+v", rec.Violations)
		}
		if rec.ViolationCount != 0 {
			t.Fatalf("expected 0 violations, got %d", rec.ViolationCount)
		}
	})

	t.Run("AuditedReadOnlyRootViolation", func(t *testing.T) {
		res, rec, err := sb.RunScriptWithAudit(ctx, logger, "touch /live_root_test 2>&1")
		if err != nil {
			t.Fatalf("RunScriptWithAudit failed: %v", err)
		}
		if res.Success {
			t.Fatalf("expected touch /live_root_test to fail")
		}
		if rec.PassedSecurity {
			t.Fatalf("expected security violation for write to read-only root")
		}
		found := false
		for _, v := range rec.Violations {
			if v.Type == ViolationFilesystemWrite {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected ViolationFilesystemWrite, got violations: %+v", rec.Violations)
		}
	})

	t.Run("AuditedNetworkIsolationViolation", func(t *testing.T) {
		res, rec, err := sb.RunScriptWithAudit(ctx, logger, "curl -s --connect-timeout 1 http://1.1.1.1 2>&1")
		if err != nil {
			t.Fatalf("RunScriptWithAudit failed: %v", err)
		}
		if res.Success {
			t.Fatalf("expected outbound network curl to fail")
		}
		if rec.PassedSecurity {
			t.Fatalf("expected network violation")
		}
		found := false
		for _, v := range rec.Violations {
			if v.Type == ViolationNetworkEgress {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected ViolationNetworkEgress, got violations: %+v", rec.Violations)
		}
	})

	t.Run("AuditedSeccompViolation", func(t *testing.T) {
		pyCode := `
import ctypes
libc = ctypes.CDLL(None, use_errno=True)
res = libc.ptrace(0, 0, 0, 0)
err = ctypes.get_errno()
print(f"res={res} errno={err}")
`
		res, rec, err := sb.RunWithAudit(ctx, logger, "python3", "-c", pyCode)
		if err != nil {
			t.Fatalf("RunWithAudit failed: %v", err)
		}
		if !res.Success {
			t.Fatalf("expected python execution to complete, got exit %d", res.ExitCode)
		}
		if rec.PassedSecurity {
			t.Fatalf("expected security violation for blocked ptrace")
		}
		found := false
		for _, v := range rec.Violations {
			if v.Type == ViolationSyscallBlocked {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected ViolationSyscallBlocked, got violations: %+v", rec.Violations)
		}
	})

	counters := logger.Counters()
	if counters.FilesystemWrite < 1 {
		t.Fatalf("expected at least 1 filesystem write violation, got %d", counters.FilesystemWrite)
	}
	if counters.NetworkEgress < 1 {
		t.Fatalf("expected at least 1 network egress violation, got %d", counters.NetworkEgress)
	}
	if counters.SyscallBlocked < 1 {
		t.Fatalf("expected at least 1 syscall blocked violation, got %d", counters.SyscallBlocked)
	}
	if counters.TotalViolations < 3 {
		t.Fatalf("expected at least 3 total violations, got %d", counters.TotalViolations)
	}
}
