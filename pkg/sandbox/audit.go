package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	AuditAgentID   = 60
	AuditAgentName = "Agent 60: Sandbox execution security audit logger"

	ViolationSyscallBlocked    = "syscall_blocked"
	ViolationFilesystemWrite   = "filesystem_write_denied"
	ViolationNetworkEgress     = "network_egress_attempted"
	ViolationExecutionTimeout  = "execution_timeout"
	ViolationTeardownSLA       = "teardown_sla_exceeded"
	ViolationSensitiveEnvLeak  = "sensitive_env_leak"
	ViolationUnsafeMount       = "unsafe_mount"
	ViolationPrivilegeEscalate = "privilege_escalation"
	ViolationExecutionError    = "execution_error"

	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

var defaultSensitiveKeyPatterns = []string{
	"API_KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"PRIVATE_KEY",
	"CREDENTIAL",
	"AUTH",
	"ACCESS_KEY",
}

type SecurityAttributes struct {
	InstanceID             string   `json:"instance_id"`
	Hostname               string   `json:"hostname"`
	RootMode               string   `json:"root_mode"`
	ReadOnlyRoot           bool     `json:"read_only_root"`
	NetworkUnshared        bool     `json:"network_unshared"`
	UserUnshared           bool     `json:"user_unshared"`
	PIDUnshared            bool     `json:"pid_unshared"`
	IPCUnshared            bool     `json:"ipc_unshared"`
	UTSUnshared            bool     `json:"uts_unshared"`
	CgroupUnshared         bool     `json:"cgroup_unshared"`
	UID                    uint32   `json:"uid"`
	GID                    uint32   `json:"gid"`
	SeccompEnabled         bool     `json:"seccomp_enabled"`
	DeniedSyscallsCount    int      `json:"denied_syscalls_count"`
	DeniedSyscalls         []uint32 `json:"denied_syscalls,omitempty"`
	ClearEnv               bool     `json:"clear_env"`
	EnvKeyCount            int      `json:"env_key_count"`
	EphemeralWorkDir       bool     `json:"ephemeral_work_dir"`
	HostWorkDir            string   `json:"host_work_dir,omitempty"`
	ContainerWorkDir       string   `json:"container_work_dir,omitempty"`
	DieWithParent          bool     `json:"die_with_parent"`
	NewSession             bool     `json:"new_session"`
	MountCount             int      `json:"mount_count"`
	TmpfsCount             int      `json:"tmpfs_count"`
	ApprovedProxyCount     int      `json:"approved_proxy_count"`
	ApprovedProxyLoopbacks []string `json:"approved_proxy_loopbacks,omitempty"`
	TimeoutSeconds         float64  `json:"timeout_seconds"`
}

func ExtractSecurityAttributes(cfg Config) SecurityAttributes {
	return SecurityAttributes{
		InstanceID:             cfg.InstanceID,
		Hostname:               cfg.Hostname,
		RootMode:               string(cfg.RootMode),
		ReadOnlyRoot:           cfg.RootMode != "" && cfg.RootMode != "writable",
		NetworkUnshared:        cfg.UnshareNet,
		UserUnshared:           cfg.UnshareUser,
		PIDUnshared:            cfg.UnsharePID,
		IPCUnshared:            cfg.UnshareIPC,
		UTSUnshared:            cfg.UnshareUTS,
		CgroupUnshared:         cfg.UnshareCgroup,
		UID:                    cfg.UID,
		GID:                    cfg.GID,
		SeccompEnabled:         cfg.EnableSeccomp,
		DeniedSyscallsCount:    len(cfg.DeniedSyscalls),
		DeniedSyscalls:         cfg.DeniedSyscalls,
		ClearEnv:               cfg.ClearEnv,
		EnvKeyCount:            len(cfg.Env),
		EphemeralWorkDir:       cfg.EphemeralWorkDir,
		HostWorkDir:            cfg.HostWorkDir,
		ContainerWorkDir:       cfg.ContainerWorkDir,
		DieWithParent:          cfg.DieWithParent,
		NewSession:             cfg.NewSession,
		MountCount:             len(cfg.Mounts),
		TmpfsCount:             len(cfg.TmpfsPaths),
		ApprovedProxyCount:     len(cfg.ApprovedProxyLoopbacks),
		ApprovedProxyLoopbacks: cfg.ApprovedProxyLoopbacks,
		TimeoutSeconds:         cfg.Timeout.Seconds(),
	}
}

type SecurityViolation struct {
	Type      string    `json:"type"`
	Severity  string    `json:"severity"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type ViolationCounters struct {
	SyscallBlocked    int64 `json:"syscall_blocked"`
	FilesystemWrite   int64 `json:"filesystem_write_denied"`
	NetworkEgress     int64 `json:"network_egress_attempted"`
	ExecutionTimeout  int64 `json:"execution_timeout"`
	TeardownSLA       int64 `json:"teardown_sla_exceeded"`
	SensitiveEnvLeak  int64 `json:"sensitive_env_leak"`
	UnsafeMount       int64 `json:"unsafe_mount"`
	PrivilegeEscalate int64 `json:"privilege_escalation"`
	ExecutionError    int64 `json:"execution_error"`
	TotalViolations   int64 `json:"total_violations"`
}

func (vc *ViolationCounters) Increment(vType string) {
	switch vType {
	case ViolationSyscallBlocked:
		vc.SyscallBlocked++
	case ViolationFilesystemWrite:
		vc.FilesystemWrite++
	case ViolationNetworkEgress:
		vc.NetworkEgress++
	case ViolationExecutionTimeout:
		vc.ExecutionTimeout++
	case ViolationTeardownSLA:
		vc.TeardownSLA++
	case ViolationSensitiveEnvLeak:
		vc.SensitiveEnvLeak++
	case ViolationUnsafeMount:
		vc.UnsafeMount++
	case ViolationPrivilegeEscalate:
		vc.PrivilegeEscalate++
	case ViolationExecutionError:
		vc.ExecutionError++
	}
	vc.TotalViolations++
}

func (vc *ViolationCounters) Add(other ViolationCounters) {
	vc.SyscallBlocked += other.SyscallBlocked
	vc.FilesystemWrite += other.FilesystemWrite
	vc.NetworkEgress += other.NetworkEgress
	vc.ExecutionTimeout += other.ExecutionTimeout
	vc.TeardownSLA += other.TeardownSLA
	vc.SensitiveEnvLeak += other.SensitiveEnvLeak
	vc.UnsafeMount += other.UnsafeMount
	vc.PrivilegeEscalate += other.PrivilegeEscalate
	vc.ExecutionError += other.ExecutionError
	vc.TotalViolations += other.TotalViolations
}

type AuditRecord struct {
	AuditID            string              `json:"audit_id"`
	AgentID            int                 `json:"agent_id"`
	AgentName          string              `json:"agent_name"`
	Timestamp          time.Time           `json:"timestamp"`
	InstanceID         string              `json:"instance_id"`
	Command            []string            `json:"command"`
	DurationMs         float64             `json:"duration_ms"`
	TeardownMs         float64             `json:"teardown_ms"`
	ExitCode           int                 `json:"exit_code"`
	Success            bool                `json:"success"`
	SecurityAttributes SecurityAttributes  `json:"security_attributes"`
	Violations         []SecurityViolation `json:"violations,omitempty"`
	ViolationCount     int                 `json:"violation_count"`
	PassedSecurity     bool                `json:"passed_security"`
	Error              string              `json:"error,omitempty"`
}

type AuditSummary struct {
	AgentID         int               `json:"agent_id"`
	AgentName       string            `json:"agent_name"`
	TotalAudits     int               `json:"total_audits"`
	PassedCount     int               `json:"passed_count"`
	FailedCount     int               `json:"failed_count"`
	ViolationRate   float64           `json:"violation_rate"`
	ViolationTotals ViolationCounters `json:"violation_totals"`
	LastAuditTime   time.Time         `json:"last_audit_time,omitempty"`
}

type AuditLogger struct {
	mu            sync.RWMutex
	logWriter     io.Writer
	file          *os.File
	records       []AuditRecord
	counters      ViolationCounters
	sensitiveKeys []string
}

type LoggerOption func(*AuditLogger) error

func WithWriter(w io.Writer) LoggerOption {
	return func(l *AuditLogger) error {
		l.logWriter = w
		return nil
	}
}

func WithFilePath(path string) LoggerOption {
	return func(l *AuditLogger) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		l.file = f
		if l.logWriter == nil {
			l.logWriter = f
		} else {
			l.logWriter = io.MultiWriter(l.logWriter, f)
		}
		return nil
	}
}

func WithSensitiveKeys(keys []string) LoggerOption {
	return func(l *AuditLogger) error {
		l.sensitiveKeys = append(l.sensitiveKeys, keys...)
		return nil
	}
}

func NewAuditLogger(opts ...LoggerOption) (*AuditLogger, error) {
	l := &AuditLogger{
		sensitiveKeys: append([]string(nil), defaultSensitiveKeyPatterns...),
	}
	for _, opt := range opts {
		if err := opt(l); err != nil {
			if l.file != nil {
				_ = l.file.Close()
			}
			return nil, err
		}
	}
	return l, nil
}

func generateAuditID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("audit-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("audit-%s", hex.EncodeToString(b))
}

func (l *AuditLogger) DetectViolations(cfg Config, res *RunResult) []SecurityViolation {
	l.mu.RLock()
	sensitiveKeys := append([]string(nil), l.sensitiveKeys...)
	l.mu.RUnlock()
	return detectViolations(cfg, res, sensitiveKeys)
}

func detectViolations(cfg Config, res *RunResult, sensitiveKeys []string) []SecurityViolation {
	var violations []SecurityViolation
	now := time.Now().UTC()

	for k := range cfg.Env {
		upperK := strings.ToUpper(k)
		for _, sens := range sensitiveKeys {
			if strings.Contains(upperK, strings.ToUpper(sens)) {
				violations = append(violations, SecurityViolation{
					Type:      ViolationSensitiveEnvLeak,
					Severity:  SeverityHigh,
					Message:   fmt.Sprintf("sensitive environment variable detected: %s", k),
					Details:   fmt.Sprintf("key matches sensitive pattern %s", sens),
					Timestamp: now,
				})
				break
			}
		}
	}

	for _, m := range cfg.Mounts {
		if m.Type == MountTypeBind {
			if m.Destination == "/" || m.Destination == "/etc" || m.Destination == "/usr" || m.Destination == "/boot" {
				violations = append(violations, SecurityViolation{
					Type:      ViolationUnsafeMount,
					Severity:  SeverityCritical,
					Message:   fmt.Sprintf("unsafe write bind mount to system directory: %s", m.Destination),
					Details:   fmt.Sprintf("source %s mounted as %s", m.Source, m.Type),
					Timestamp: now,
				})
			}
		}
		if strings.Contains(m.Source, "..") || strings.Contains(m.Destination, "..") {
			violations = append(violations, SecurityViolation{
				Type:      ViolationUnsafeMount,
				Severity:  SeverityHigh,
				Message:   "path traversal detected in mount configuration",
				Details:   fmt.Sprintf("source: %s, dest: %s", m.Source, m.Destination),
				Timestamp: now,
			})
		}
		if strings.Contains(m.Source, ".ssh") || strings.Contains(m.Source, "/etc/shadow") || strings.Contains(m.Source, "/etc/sudoers") {
			if m.Type == MountTypeBind {
				violations = append(violations, SecurityViolation{
					Type:      ViolationUnsafeMount,
					Severity:  SeverityCritical,
					Message:   fmt.Sprintf("sensitive credential path bound writable: %s", m.Source),
					Details:   fmt.Sprintf("mount destination: %s", m.Destination),
					Timestamp: now,
				})
			}
		}
	}

	if !cfg.UnshareUser {
		violations = append(violations, SecurityViolation{
			Type:      ViolationPrivilegeEscalate,
			Severity:  SeverityCritical,
			Message:   "user namespace isolation disabled",
			Details:   fmt.Sprintf("uid=%d, gid=%d without unshare_user", cfg.UID, cfg.GID),
			Timestamp: now,
		})
	}

	if res == nil {
		return violations
	}

	outCombined := res.Stdout + "\n" + res.Stderr

	if res.ExitCode == 159 || strings.Contains(outCombined, "Bad system call") || strings.Contains(outCombined, "bad system call") {
		violations = append(violations, SecurityViolation{
			Type:      ViolationSyscallBlocked,
			Severity:  SeverityCritical,
			Message:   "seccomp blocked restricted system call (SIGSYS)",
			Details:   fmt.Sprintf("exit code %d", res.ExitCode),
			Timestamp: now,
		})
	} else if cfg.EnableSeccomp && (strings.Contains(outCombined, "res=-1 errno=1") || (res.ExitCode != 0 && strings.Contains(outCombined, "Operation not permitted") && strings.Contains(strings.Join(res.Command, " "), "ptrace"))) {
		violations = append(violations, SecurityViolation{
			Type:      ViolationSyscallBlocked,
			Severity:  SeverityHigh,
			Message:   "syscall returned EPERM under seccomp filter",
			Details:   "restricted syscall denied by BPF filter",
			Timestamp: now,
		})
	}

	if strings.Contains(outCombined, "Read-only file system") || strings.Contains(outCombined, "read-only file system") {
		violations = append(violations, SecurityViolation{
			Type:      ViolationFilesystemWrite,
			Severity:  SeverityHigh,
			Message:   "unauthorized write attempt to read-only filesystem",
			Details:   res.Stderr,
			Timestamp: now,
		})
	}

	if cfg.UnshareNet {
		netIndicators := []string{
			"Network is unreachable",
			"network is unreachable",
			"Could not resolve host",
			"could not resolve host",
			"Failed to connect to",
			"failed to connect to",
			"Failed to connect",
			"failed to connect",
			"Couldn't connect",
			"couldn't connect",
			"No route to host",
			"no route to host",
			"Connection refused",
			"connection refused",
		}
		foundNet := false
		for _, ind := range netIndicators {
			if strings.Contains(outCombined, ind) {
				violations = append(violations, SecurityViolation{
					Type:      ViolationNetworkEgress,
					Severity:  SeverityHigh,
					Message:   "network egress attempt failed under unshared network namespace",
					Details:   ind,
					Timestamp: now,
				})
				foundNet = true
				break
			}
		}
		if !foundNet && (res.ExitCode == 7 || res.ExitCode == 6 || res.ExitCode == 28) {
			cmdStr := strings.Join(res.Command, " ")
			if strings.Contains(cmdStr, "curl") || strings.Contains(outCombined, "curl") {
				violations = append(violations, SecurityViolation{
					Type:      ViolationNetworkEgress,
					Severity:  SeverityHigh,
					Message:   "network egress attempt failed under unshared network namespace",
					Details:   fmt.Sprintf("curl network connection failure (exit code %d)", res.ExitCode),
					Timestamp: now,
				})
			}
		}
	}

	if strings.Contains(res.Error, "context deadline exceeded") || (cfg.Timeout > 0 && res.Duration > cfg.Timeout) {
		violations = append(violations, SecurityViolation{
			Type:      ViolationExecutionTimeout,
			Severity:  SeverityMedium,
			Message:   "command execution exceeded timeout limit",
			Details:   fmt.Sprintf("duration %v exceeded %v", res.Duration, cfg.Timeout),
			Timestamp: now,
		})
	}

	if res.TeardownMs > 50.0 {
		violations = append(violations, SecurityViolation{
			Type:      ViolationTeardownSLA,
			Severity:  SeverityMedium,
			Message:   "sandbox teardown latency violated 50ms SLA ceiling",
			Details:   fmt.Sprintf("teardown latency was %.2fms", res.TeardownMs),
			Timestamp: now,
		})
	}

	if !res.Success && len(violations) == 0 {
		violations = append(violations, SecurityViolation{
			Type:      ViolationExecutionError,
			Severity:  SeverityLow,
			Message:   fmt.Sprintf("command exited with non-zero code %d", res.ExitCode),
			Details:   res.Error,
			Timestamp: now,
		})
	}

	return violations
}

func (l *AuditLogger) Record(cfg Config, res *RunResult) (*AuditRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	violations := detectViolations(cfg, res, l.sensitiveKeys)
	for _, v := range violations {
		l.counters.Increment(v.Type)
	}

	var cmd []string
	var exitCode int
	var success bool
	var durationMs, teardownMs float64
	var errStr string
	instanceID := cfg.InstanceID

	if res != nil {
		cmd = res.Command
		exitCode = res.ExitCode
		success = res.Success
		durationMs = res.DurationMs
		teardownMs = res.TeardownMs
		errStr = res.Error
		if res.InstanceID != "" {
			instanceID = res.InstanceID
		}
	}

	rec := AuditRecord{
		AuditID:            generateAuditID(),
		AgentID:            AuditAgentID,
		AgentName:          AuditAgentName,
		Timestamp:          time.Now().UTC(),
		InstanceID:         instanceID,
		Command:            cmd,
		DurationMs:         durationMs,
		TeardownMs:         teardownMs,
		ExitCode:           exitCode,
		Success:            success,
		SecurityAttributes: ExtractSecurityAttributes(cfg),
		Violations:         violations,
		ViolationCount:     len(violations),
		PassedSecurity:     len(violations) == 0,
		Error:              errStr,
	}

	l.records = append(l.records, rec)

	if l.logWriter != nil {
		raw, err := json.Marshal(rec)
		if err != nil {
			return &rec, fmt.Errorf("marshal audit record: %w", err)
		}
		raw = append(raw, '\n')
		if _, err := l.logWriter.Write(raw); err != nil {
			return &rec, fmt.Errorf("write audit record: %w", err)
		}
	}

	return &rec, nil
}

func (l *AuditLogger) Counters() ViolationCounters {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.counters
}

func (l *AuditLogger) Records() []AuditRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	copied := make([]AuditRecord, len(l.records))
	copy(copied, l.records)
	return copied
}

func (l *AuditLogger) Summary() AuditSummary {
	l.mu.RLock()
	defer l.mu.RUnlock()

	total := len(l.records)
	passed := 0
	failed := 0
	var lastTime time.Time

	for _, r := range l.records {
		if r.PassedSecurity {
			passed++
		} else {
			failed++
		}
		if r.Timestamp.After(lastTime) {
			lastTime = r.Timestamp
		}
	}

	rate := 0.0
	if total > 0 {
		rate = float64(failed) / float64(total)
	}

	return AuditSummary{
		AgentID:         AuditAgentID,
		AgentName:       AuditAgentName,
		TotalAudits:     total,
		PassedCount:     passed,
		FailedCount:     failed,
		ViolationRate:   rate,
		ViolationTotals: l.counters,
		LastAuditTime:   lastTime,
	}
}

func (l *AuditLogger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = nil
	l.counters = ViolationCounters{}
}

func (l *AuditLogger) ExportJSON() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return json.MarshalIndent(l.records, "", "  ")
}

func (l *AuditLogger) ExportJSONLines() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var buf bytes.Buffer
	for _, rec := range l.records {
		raw, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func (l *AuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

func (l *AuditLogger) AuditedRun(ctx context.Context, sb *Sandbox, command ...string) (*RunResult, *AuditRecord, error) {
	res, err := sb.Run(ctx, command...)
	rec, recErr := l.Record(sb.cfg, res)
	if recErr != nil && err == nil {
		err = recErr
	}
	return res, rec, err
}

func (l *AuditLogger) AuditedRunScript(ctx context.Context, sb *Sandbox, script string) (*RunResult, *AuditRecord, error) {
	res, err := sb.RunScript(ctx, script)
	rec, recErr := l.Record(sb.cfg, res)
	if recErr != nil && err == nil {
		err = recErr
	}
	return res, rec, err
}

var (
	defaultLoggerLock sync.Mutex
	defaultLogger     *AuditLogger
)

func DefaultLogger() *AuditLogger {
	defaultLoggerLock.Lock()
	defer defaultLoggerLock.Unlock()
	if defaultLogger == nil {
		defaultLogger, _ = NewAuditLogger()
	}
	return defaultLogger
}
