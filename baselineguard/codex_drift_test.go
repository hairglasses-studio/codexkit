package baselineguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- codex_version_pin ---

// writeFakeCodexBinary writes an executable shell script that mimics the
// managed launcher: it prints JSON log lines to stdout BEFORE the real
// `codex --version` line, exactly like the live wrapper does, so the parser
// under test is exercised against the real shape rather than a simplified one.
func writeFakeCodexBinary(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex")
	script := "#!/usr/bin/env bash\n" +
		`echo '{"time":"2026-09-30T23:57:54Z","level":"INFO","msg":"agentlaunch.credentials.rotation_disabled","provider":"codex"}'` + "\n" +
		`echo '{"time":"2026-09-30T23:57:54Z","level":"INFO","msg":"agentlaunch.credentials.rotation_disabled","provider":"context7"}'` + "\n" +
		"echo 'codex-cli " + version + "'\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexVersionPin_NoDocumentedPinsSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "# Test\n\n> Canonical instructions: AGENTS.md\n")

	findings := findingsFor(Check(dir), "codex_version_pin")
	if len(findings) != 1 || !findings[0].Passed || !strings.Contains(findings[0].Message, "no documented codex-cli version pins") {
		t.Fatalf("expected a skip finding, got %#v", findings)
	}
}

func TestCodexVersionPin_DisagreeingDocsFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "- **Codex 0.147.0**: stable apps.\n")
	writeFile(t, dir, filepath.Join("docs", "CODEX_COMPATIBILITY.md"), "# Codex 0.154.0 compatibility\n\nThis repository targets Codex CLI `0.154.0`.\n")

	findings := findingsFor(Check(dir), "codex_version_pin")
	if len(findings) != 1 || findings[0].Passed {
		t.Fatalf("expected exactly one failing finding, got %#v", findings)
	}
	msg := findings[0].Message
	if !strings.Contains(msg, "0.147.0") || !strings.Contains(msg, "0.154.0") {
		t.Fatalf("message must name both disagreeing versions, got: %s", msg)
	}
	if !strings.Contains(msg, "AGENTS.md") || !strings.Contains(msg, filepath.Join("docs", "CODEX_COMPATIBILITY.md")) {
		t.Fatalf("message must name both disagreeing files, got: %s", msg)
	}
}

func TestCodexVersionPin_LiveDriftFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "- **Codex 0.154.0**: stable apps.\n")
	t.Setenv("CODEXKIT_CODEX_BIN", writeFakeCodexBinary(t, "0.159.3"))

	findings := findingsFor(Check(dir), "codex_version_pin")
	if len(findings) != 1 || findings[0].Passed {
		t.Fatalf("expected exactly one failing finding, got %#v", findings)
	}
	msg := findings[0].Message
	if !strings.Contains(msg, "0.154.0") || !strings.Contains(msg, "0.159.3") {
		t.Fatalf("message must name both the documented and live versions, got: %s", msg)
	}
}

func TestCodexVersionPin_LiveMatchPasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "- **Codex 0.159.3**: stable apps.\n")
	t.Setenv("CODEXKIT_CODEX_BIN", writeFakeCodexBinary(t, "0.159.3"))

	findings := findingsFor(Check(dir), "codex_version_pin")
	if len(findings) != 1 || !findings[0].Passed {
		t.Fatalf("expected a passing finding, got %#v", findings)
	}
}

func TestCodexVersionPin_BinaryUnavailableFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "- **Codex 0.159.3**: stable apps.\n")
	t.Setenv("CODEXKIT_CODEX_BIN", filepath.Join(t.TempDir(), "does-not-exist"))

	findings := findingsFor(Check(dir), "codex_version_pin")
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %#v", findings)
	}
	if findings[0].Passed {
		t.Fatalf("an unavailable binary must be UNKNOWN (failing closed), never a pass: %#v", findings[0])
	}
	if !strings.Contains(findings[0].Message, "UNKNOWN") {
		t.Fatalf("message must say UNKNOWN, got: %s", findings[0].Message)
	}
}

// --- codex_policy_drift ---

func setupCodexPolicyRepo(t *testing.T, registryJSON string) string {
	t.Helper()
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if registryJSON != "" {
		writeFile(t, root, filepath.Join("workspace", "config-expectations.json"), registryJSON)
	}
	return repoDir
}

const testCodexPolicyRegistry = `{
  "version": 1,
  "expectations": [
    {
      "id": "codex-unattended-active-fleet",
      "provider": "codex",
      "repos": ["repo"],
      "file": ".codex/config.toml",
      "format": "toml",
      "expected": {"approval_policy": "never", "sandbox_mode": "danger-full-access"}
    },
    {
      "id": "codex-unattended-control-plane-safety",
      "provider": "codex",
      "repos": ["other"],
      "file": ".codex/config.toml",
      "format": "toml",
      "expected": {"approval_policy": "never", "sandbox_mode": "danger-full-access"}
    }
  ]
}`

// writeFakeCodexHome writes a fake ~/.codex/config.toml under a fresh temp
// dir and points CODEXKIT_CODEX_HOME at it, returning that directory.
func writeFakeCodexHome(t *testing.T, configBody string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", configBody)
	t.Setenv("CODEXKIT_CODEX_HOME", dir)
	return dir
}

func TestCodexPolicyDrift_AbsentRegistrySkips(t *testing.T) {
	dir := setupCodexPolicyRepo(t, "")

	findings := findingsFor(Check(dir), "codex_policy_drift")
	if len(findings) != 1 || !findings[0].Passed || findings[0].Message != "config expectations registry absent — skipping" {
		t.Fatalf("expected absent registry to skip, got %#v", findings)
	}
}

func TestCodexPolicyDrift_MatchPasses(t *testing.T) {
	dir := setupCodexPolicyRepo(t, testCodexPolicyRegistry)
	writeFakeCodexHome(t, "approval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")

	findings := findingsFor(Check(dir), "codex_policy_drift")
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (one per key), got %d: %#v", len(findings), findings)
	}
	for _, f := range findings {
		if !f.Passed {
			t.Fatalf("expected matching live config to pass, got: %s", f.Message)
		}
	}
}

func TestCodexPolicyDrift_DriftFailsWithoutOverride(t *testing.T) {
	dir := setupCodexPolicyRepo(t, testCodexPolicyRegistry)
	writeFakeCodexHome(t, "approval_policy = \"on-request\"\nsandbox_mode = \"danger-full-access\"\n")

	findings := findingsFor(Check(dir), "codex_policy_drift")
	failed := 0
	for _, f := range findings {
		if !f.Passed {
			failed++
			if !strings.Contains(f.Message, "approval_policy") {
				t.Fatalf("drift failure for the wrong key: %s", f.Message)
			}
			if !strings.Contains(f.Message, "no operator-override") {
				t.Fatalf("expected the no-override message, got: %s", f.Message)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("expected exactly one drift failure, got %d: %#v", failed, findings)
	}
}

func TestCodexPolicyDrift_DriftPassesWithOperatorOverride(t *testing.T) {
	dir := setupCodexPolicyRepo(t, testCodexPolicyRegistry)
	writeFakeCodexHome(t, "# operator-override: approval_policy\napproval_policy = \"on-request\"\nsandbox_mode = \"danger-full-access\"\n")

	findings := findingsFor(Check(dir), "codex_policy_drift")
	for _, f := range findings {
		if !f.Passed {
			t.Fatalf("expected the operator-override comment to clear the drift, got: %s", f.Message)
		}
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Message, "operator-override") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected one finding to mention the operator-override, got %#v", findings)
	}
}

func TestCodexPolicyDrift_LiveConfigMissingFailsClosed(t *testing.T) {
	dir := setupCodexPolicyRepo(t, testCodexPolicyRegistry)
	t.Setenv("CODEXKIT_CODEX_HOME", t.TempDir()) // directory exists, config.toml does not

	findings := findingsFor(Check(dir), "codex_policy_drift")
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %#v", len(findings), findings)
	}
	for _, f := range findings {
		if f.Passed {
			t.Fatalf("a missing live config must be UNKNOWN (failing closed), never a pass: %#v", f)
		}
		if !strings.Contains(f.Message, "UNKNOWN") {
			t.Fatalf("message must say UNKNOWN, got: %s", f.Message)
		}
	}
}

func TestCodexPolicyDrift_RegistryDisagreesWithItselfFails(t *testing.T) {
	registry := `{
  "version": 1,
  "expectations": [
    {"id": "a", "provider": "codex", "repos": ["repo"], "file": ".codex/config.toml", "format": "toml",
     "expected": {"approval_policy": "never", "sandbox_mode": "danger-full-access"}},
    {"id": "b", "provider": "codex", "repos": ["other"], "file": ".codex/config.toml", "format": "toml",
     "expected": {"approval_policy": "on-request", "sandbox_mode": "danger-full-access"}}
  ]
}`
	dir := setupCodexPolicyRepo(t, registry)
	writeFakeCodexHome(t, "approval_policy = \"never\"\nsandbox_mode = \"danger-full-access\"\n")

	findings := findingsFor(Check(dir), "codex_policy_drift")
	var sawDisagreement bool
	for _, f := range findings {
		if strings.Contains(f.Message, "disagrees with itself") {
			sawDisagreement = true
			if f.Passed {
				t.Fatalf("a self-disagreeing registry must fail, got %#v", f)
			}
		}
	}
	if !sawDisagreement {
		t.Fatalf("expected a self-disagreement finding for approval_policy, got %#v", findings)
	}
}

// TestCodexPolicyDrift_NeverLeaksUnrelatedConfigValues is the "read keys
// only, never values of secrets" safety requirement: a live config carrying
// an unrelated secret-shaped value (e.g. in a generated MCP server block)
// must never have that value surface in any finding message, even while the
// two policy keys are being read out of the very same document.
func TestCodexPolicyDrift_NeverLeaksUnrelatedConfigValues(t *testing.T) {
	dir := setupCodexPolicyRepo(t, testCodexPolicyRegistry)
	const sentinel = "SUPER_SECRET_TOKEN_DO_NOT_LEAK_9f1c"
	writeFakeCodexHome(t, "approval_policy = \"on-request\"\nsandbox_mode = \"danger-full-access\"\n\n"+
		"[mcp_servers.example]\ncommand = \"/bin/example\"\n[mcp_servers.example.env]\nAPI_TOKEN = \""+sentinel+"\"\n")

	report := Check(dir)
	for _, f := range report.Findings {
		if strings.Contains(f.Message, sentinel) {
			t.Fatalf("finding leaked an unrelated secret-shaped config value: %s", f.Message)
		}
	}
}
