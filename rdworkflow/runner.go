package rdworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	maxEvidenceBytes = 2 * 1024 * 1024
	maxStdoutBytes   = 256 * 1024
	maxStderrBytes   = 16 * 1024
	backendTimeout   = 30 * time.Second
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var artifactName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func unknown(reason string, apply bool) map[string]any {
	return map[string]any{"state": "UNKNOWN", "reason": reason, "dispatch_allowed": false, "apply_requested": apply}
}

// evidenceCopy holds an already-opened inode in a private disposable snapshot,
// so swapping an evidence filename cannot redirect the backend's later open.
func evidenceCopy(root *os.Root, dir, name string) (string, error) {
	if !artifactName.MatchString(name) {
		return "", errors.New("invalid-evidence-name")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxEvidenceBytes {
		return "", errors.New("invalid-evidence-file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxEvidenceBytes+1))
	if err != nil || len(data) > maxEvidenceBytes {
		return "", errors.New("evidence-too-large")
	}
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, data, 0600)
}

func (m *module) invoke(parent context.Context, verb string, input request) map[string]any {
	if err := parent.Err(); err != nil {
		return unknown("request-cancelled", input.Apply)
	}
	cfg := m.config
	for _, path := range []string{cfg.backend, cfg.binding, cfg.stateDir, cfg.evidenceDir, cfg.pool, cfg.claimDB} {
		if path == "" || !filepath.IsAbs(path) {
			return unknown("workflow-not-configured", input.Apply)
		}
	}
	args := []string{"--config", cfg.binding, "--state-dir", cfg.stateDir, verb}
	files := map[string]string{}
	switch verb {
	case "plan":
		if input.Phase != "offer" && input.Phase != "idle" && input.Phase != "report" {
			return unknown("invalid-phase", input.Apply)
		}
		args = append(args, "--phase", input.Phase)
		files["--identity"] = input.Identity
		files["--summary-file"] = input.SummaryFile
		for _, field := range []struct{ flag, value string }{{"--task-id", input.TaskID}, {"--progress-id", input.ProgressID}} {
			if field.value != "" {
				if !identifier.MatchString(field.value) {
					return unknown("invalid-identifier", input.Apply)
				}
				args = append(args, field.flag, field.value)
			}
		}
		if input.Result != "" {
			files["--result"] = input.Result
		}
	case "intake":
		files["--identity"] = input.Identity
		files["--inbox"] = input.Inbox
		args = append(args, "--pool", cfg.pool, "--claim-db", cfg.claimDB)
	case "record":
		if !identifier.MatchString(input.PlanID) {
			return unknown("invalid-plan-id", input.Apply)
		}
		args = append(args, "--plan-id", input.PlanID)
		files["--receipt"] = input.Receipt
	case "status":
		if input.Apply {
			return unknown("status-is-read-only", true)
		}
	default:
		return unknown("invalid-operation", input.Apply)
	}
	if len(files) > 0 {
		resolved, err := filepath.EvalSymlinks(cfg.evidenceDir)
		if err != nil || resolved != filepath.Clean(cfg.evidenceDir) {
			return unknown("evidence-root-unavailable", input.Apply)
		}
		root, err := os.OpenRoot(cfg.evidenceDir)
		if err != nil {
			return unknown("evidence-root-unavailable", input.Apply)
		}
		defer root.Close()
		dir, err := os.MkdirTemp("", "codexkit-rd-evidence-")
		if err != nil {
			return unknown("evidence-snapshot-unavailable", input.Apply)
		}
		defer os.RemoveAll(dir)
		for _, flag := range []string{"--identity", "--summary-file", "--inbox", "--receipt", "--result"} {
			if name, ok := files[flag]; ok {
				path, err := evidenceCopy(root, dir, name)
				if err != nil {
					return unknown("evidence-unavailable-or-invalid", input.Apply)
				}
				args = append(args, flag, path)
			}
		}
		return execute(parent, cfg.backend, appendApply(args, input.Apply), input.Apply)
	}
	return execute(parent, cfg.backend, appendApply(args, input.Apply), input.Apply)
}

func appendApply(args []string, apply bool) []string {
	if apply {
		return append(args, "--apply")
	}
	return args
}

type cappedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errors.New("output-limit")
	}
	return b.Buffer.Write(p)
}

func execute(parent context.Context, backend string, args []string, apply bool) map[string]any {
	return executeBounded(parent, backend, args, apply, backendTimeout)
}

func executeBounded(parent context.Context, backend string, args []string, apply bool, timeout time.Duration) map[string]any {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	stdout := &cappedBuffer{limit: maxStdoutBytes, cancel: cancel}
	stderr := &cappedBuffer{limit: maxStderrBytes, cancel: cancel}
	cmd := exec.CommandContext(ctx, backend, args...)
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return unknown("backend-output-limit", apply)
	}
	if parent.Err() != nil {
		return unknown("request-cancelled", apply)
	}
	if ctx.Err() != nil {
		return unknown("backend-timeout", apply)
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 3) {
		return unknown("backend-unavailable-or-failed", apply)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil || result == nil {
		return unknown("backend-invalid-json", apply)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return unknown("backend-invalid-json", apply)
	}
	state, ok := result["state"].(string)
	if !ok || strings.TrimSpace(state) == "" {
		return unknown("backend-missing-state", apply)
	}
	if err != nil && !strings.EqualFold(state, "unknown") {
		return unknown("backend-exit-state-mismatch", apply)
	}
	return result
}
