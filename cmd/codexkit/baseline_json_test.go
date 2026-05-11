package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineCheckJSONIsMachineReadable(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, ".git"))
	writeText(t, filepath.Join(dir, "AGENTS.md"), "# Demo\n")
	writeText(t, filepath.Join(dir, "CLAUDE.md"), "# Demo\n")
	writeText(t, filepath.Join(dir, "GEMINI.md"), "# Demo\n")

	cmd := exec.Command("go", "run", ".", "baseline", "check", dir, "--json")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err == nil {
		t.Fatal("expected failing baseline command")
	}
	output := strings.TrimSpace(string(out))
	if !strings.HasPrefix(output, "[") {
		t.Fatalf("expected JSON output to start with [, got:\n%s", output)
	}
	if strings.Contains(output, " FAIL ") {
		t.Fatalf("expected JSON output without human status lines, got:\n%s", output)
	}

	var reports []struct {
		Findings []struct {
			Check       string `json:"check"`
			Remediation []struct {
				Message string   `json:"message"`
				Command []string `json:"command"`
			} `json:"remediation"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(output), &reports); err != nil {
		t.Fatalf("baseline JSON did not parse: %v\n%s", err, output)
	}
	foundRemediation := false
	for _, finding := range reports[0].Findings {
		if !strings.HasPrefix(finding.Check, "canonical_") {
			continue
		}
		if len(finding.Remediation) > 0 && finding.Remediation[0].Message != "" {
			foundRemediation = true
			break
		}
	}
	if !foundRemediation {
		t.Fatalf("expected structured remediation in JSON: %#v", reports)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path string, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
