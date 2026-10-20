package rdworkflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEvidenceNamesFilesAndSymlinks(t *testing.T) {
	m := fixture(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, filepath.Join(m.config.evidenceDir, "link.json")); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(m.config.evidenceDir, "directory"), 0700)
	if err := syscall.Mkfifo(filepath.Join(m.config.evidenceDir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.config.evidenceDir, "large"), []byte(strings.Repeat("x", maxEvidenceBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../outside.json", outside, "sub/file.json", "link.json", "directory", "fifo", "large", "missing.json"} {
		result := m.invoke(context.Background(), "plan", request{Phase: "offer", Identity: name, SummaryFile: "summary.txt"})
		if result["state"] != "UNKNOWN" || result["reason"] != "evidence-unavailable-or-invalid" {
			t.Fatalf("%q: %v", name, result)
		}
	}
	link := filepath.Join(t.TempDir(), "evidence-link")
	os.Symlink(m.config.evidenceDir, link)
	m.config.evidenceDir = link
	result := m.invoke(context.Background(), "intake", request{Identity: "identity.json", Inbox: "inbox.json"})
	if result["reason"] != "evidence-root-unavailable" {
		t.Fatal(result)
	}
}

func TestBackendFailuresNeverBecomeSuccess(t *testing.T) {
	m := fixture(t)
	for mode, reason := range map[string]string{"unknown": "fixture-evidence-absent", "mismatch": "backend-exit-state-mismatch", "multiple": "backend-invalid-json", "array": "backend-invalid-json", "missing-state": "backend-missing-state", "stdout-cap": "backend-output-limit", "stderr-cap": "backend-output-limit"} {
		t.Setenv("CODEXKIT_RD_TEST_CHILD", mode)
		result := execute(context.Background(), m.config.backend, nil, false)
		if result["state"] != "UNKNOWN" || result["reason"] != reason {
			t.Fatalf("%s: %v", mode, result)
		}
	}
	result := execute(context.Background(), filepath.Join(t.TempDir(), "absent"), nil, true)
	if result["state"] != "UNKNOWN" || result["dispatch_allowed"] != false || result["apply_requested"] != true {
		t.Fatal(result)
	}
}

func TestCancellationAndBackendDeadline(t *testing.T) {
	m := fixture(t)
	t.Setenv("CODEXKIT_RD_TEST_CHILD", "hang")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := m.invoke(ctx, "status", request{}); result["reason"] != "request-cancelled" {
		t.Fatal(result)
	}
	start := time.Now()
	result := executeBounded(context.Background(), m.config.backend, nil, true, 50*time.Millisecond)
	if result["reason"] != "backend-timeout" || time.Since(start) > 3*time.Second {
		t.Fatal(result, time.Since(start))
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result = execute(ctx, m.config.backend, nil, false)
	if result["reason"] != "request-cancelled" {
		t.Fatal(result)
	}
}

func TestMissingConfigAndInvalidInputsStayUnknown(t *testing.T) {
	m := fixture(t)
	original := m.config.binding
	m.config.binding = ""
	if result := m.invoke(context.Background(), "status", request{}); result["reason"] != "workflow-not-configured" {
		t.Fatal(result)
	}
	m.config.binding = original
	for _, input := range []request{{Phase: "bad"}, {Phase: "offer", TaskID: "--execute"}, {Phase: "offer", ProgressID: "bad\n"}} {
		if result := m.invoke(context.Background(), "plan", input); result["state"] != "UNKNOWN" {
			t.Fatal(result)
		}
	}
	if result := m.invoke(context.Background(), "record", request{PlanID: "../bad"}); result["reason"] != "invalid-plan-id" {
		t.Fatal(result)
	}
	if result := m.invoke(context.Background(), "status", request{Apply: true}); result["reason"] != "status-is-read-only" {
		t.Fatal(result)
	}
}
