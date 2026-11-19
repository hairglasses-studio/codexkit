package mcpsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func buildTestManifest(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	out := t.TempDir()

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"studio_Context7/Docs": map[string]any{
				"url":       "https://mcp.example.com/context7",
				"transport": "streamable-http",
				"headers": map[string]string{
					"Authorization": "${CONTEXT7_AUTH_HEADER}",
				},
			},
			"github_mcp": map[string]any{
				"command": "/usr/local/bin/github-mcp",
				"args":    []string{"--stdio", "--read-only"},
				"cwd":     "/srv/repos/demo",
				"env": map[string]string{
					"APP_ENV":      "production",
					"GITHUB_TOKEN": "${GITHUB_TOKEN}",
					"SOPS_REF":     "sops://fleet/github",
				},
				"autoApprove": []string{"get_issue", "list_prs"},
			},
		},
	}
	mcpBytes, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestArtifact(t, filepath.Join(root, ".mcp.json"), mcpBytes)

	skillContent := `---
name: Code_Review
description: Deterministic code review workflow
allowed-tools:
  - read_file
  - grep_search
globs:
  - "*.go"
  - "mcpsync/*.go"
paths:
  - "mcpsync/**"
alwaysApply: true
user-invocable: true
triggers:
  - review
compatibility: agent-skills-v1
license: MIT
metadata:
  owner: platform
---

Inspect diffs carefully and verify race tests pass.
`
	writeTestArtifact(t, filepath.Join(root, ".agents", "skills", "code_review", "SKILL.md"), []byte(skillContent))
	return root, out
}

func writeTestArtifact(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompileUniversalSurface_FiveProvidersAndDeterminism(t *testing.T) {
	root, _ := buildTestManifest(t)
	manifest, err := LoadUniversalManifest(root)
	if err != nil {
		t.Fatalf("LoadUniversalManifest: %v", err)
	}

	opts := UniversalCompileOptions{
		CodexSnakeCase: true,
	}
	res1, err := CompileUniversalSurface(*manifest, opts)
	if err != nil {
		t.Fatalf("CompileUniversalSurface: %v", err)
	}
	if len(res1.Providers) != 5 {
		t.Fatalf("got %d providers, want 5", len(res1.Providers))
	}
	if len(res1.Artifacts) != 10 {
		t.Fatalf("got %d artifacts, want 10 (5 mcp + 5 skill)", len(res1.Artifacts))
	}

	reversed := UniversalManifest{
		RepoRoot: manifest.RepoRoot,
		Servers:  []UniversalServerSpec{manifest.Servers[1], manifest.Servers[0]},
		Skills:   append([]UniversalSkillAST(nil), manifest.Skills...),
	}
	res2, err := CompileUniversalSurface(reversed, opts)
	if err != nil {
		t.Fatalf("CompileUniversalSurface reversed: %v", err)
	}
	if res1.Digest != res2.Digest {
		t.Fatalf("non-deterministic digest: %s != %s", res1.Digest, res2.Digest)
	}
	for i := range res1.Artifacts {
		if !bytes.Equal(res1.Artifacts[i].Content, res2.Artifacts[i].Content) {
			t.Fatalf("artifact %s differs across runs", res1.Artifacts[i].RelPath)
		}
	}

	byRel := make(map[string]CompiledArtifact, len(res1.Artifacts))
	for _, art := range res1.Artifacts {
		byRel[art.RelPath] = art
	}

	claudeArt, ok := byRel[".mcp.json"]
	if !ok {
		t.Fatal("missing claude .mcp.json")
	}
	var claudeDoc map[string]map[string]map[string]any
	if err := json.Unmarshal(claudeArt.Content, &claudeDoc); err != nil {
		t.Fatalf("invalid claude json: %v", err)
	}
	if claudeDoc["mcpServers"]["context7-docs"]["type"] != "http" {
		t.Fatalf("claude remote server missing type=http: %+v", claudeDoc)
	}

	codexArt, ok := byRel[".codex/config.toml"]
	if !ok {
		t.Fatal("missing codex .codex/config.toml")
	}
	var codexDoc struct {
		MCPServers map[string]map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(codexArt.Content, &codexDoc); err != nil {
		t.Fatalf("invalid codex toml: %v", err)
	}
	if _, exists := codexDoc.MCPServers["context7_docs"]; !exists {
		t.Fatalf("expected snake_case codex alias context7_docs, got %+v", codexDoc.MCPServers)
	}
	if _, exists := codexDoc.MCPServers["github_mcp"]; !exists {
		t.Fatalf("expected codex alias github_mcp, got %+v", codexDoc.MCPServers)
	}

	agyArt, ok := byRel[".gemini/antigravity/mcp_config.json"]
	if !ok {
		t.Fatal("missing agy mcp_config.json")
	}
	var agyDoc map[string]map[string]map[string]any
	if err := json.Unmarshal(agyArt.Content, &agyDoc); err != nil {
		t.Fatalf("invalid agy json: %v", err)
	}
	if agyDoc["mcpServers"]["context7-docs"]["serverUrl"] != "https://mcp.example.com/context7" {
		t.Fatalf("agy remote server missing serverUrl: %+v", agyDoc)
	}

	clineArt, ok := byRel[".cline/cline_mcp_settings.json"]
	if !ok {
		t.Fatal("missing cline_mcp_settings.json")
	}
	var clineDoc map[string]map[string]map[string]any
	if err := json.Unmarshal(clineArt.Content, &clineDoc); err != nil {
		t.Fatalf("invalid cline json: %v", err)
	}
	if _, ok := clineDoc["mcpServers"]["github-mcp"]["autoApprove"]; !ok {
		t.Fatalf("cline entry missing autoApprove: %+v", clineDoc)
	}

	cursorMCP, ok := byRel[".cursor/mcp.json"]
	if !ok {
		t.Fatal("missing .cursor/mcp.json")
	}
	var cursorDoc map[string]map[string]map[string]any
	if err := json.Unmarshal(cursorMCP.Content, &cursorDoc); err != nil {
		t.Fatalf("invalid cursor json: %v", err)
	}
	if cursorDoc["mcpServers"]["github-mcp"]["command"] != "/usr/local/bin/github-mcp" {
		t.Fatalf("cursor server command mismatch: %+v", cursorDoc)
	}

	mdcArt, ok := byRel[".cursor/rules/code-review.mdc"]
	if !ok {
		t.Fatal("missing cursor .mdc skill artifact")
	}
	mdcText := string(mdcArt.Content)
	if !strings.HasPrefix(mdcText, "---\n") || !strings.Contains(mdcText, "alwaysApply: true") {
		t.Fatalf("invalid cursor mdc frontmatter:\n%s", mdcText)
	}
	rest := strings.TrimPrefix(mdcText, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatalf("missing closing mdc frontmatter:\n%s", mdcText)
	}
	var mdcFM map[string]any
	if err := yaml.Unmarshal([]byte(rest[:end]), &mdcFM); err != nil {
		t.Fatalf("invalid mdc yaml: %v", err)
	}
	if mdcFM["description"] != "Deterministic code review workflow" {
		t.Fatalf("unexpected mdc description: %+v", mdcFM)
	}
}

func TestNormalizeServerAlias_AndCollisionHandling(t *testing.T) {
	cases := []struct {
		raw      string
		prefixes []string
		want     string
	}{
		{"studio_Context7/Docs", nil, "context7-docs"},
		{"studio-github_mcp", nil, "github-mcp"},
		{"acme_search_tool", []string{"acme_"}, "search-tool"},
		{"   ///  ", nil, "mcp"},
	}
	for _, tc := range cases {
		if got := NormalizeServerAlias(tc.raw, tc.prefixes); got != tc.want {
			t.Errorf("NormalizeServerAlias(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	implicitCollision := UniversalManifest{
		Servers: []UniversalServerSpec{
			{Name: "my_server", Command: "srv-a"},
			{Name: "my-server", Command: "srv-b"},
		},
	}
	res, err := CompileUniversalSurface(implicitCollision, UniversalCompileOptions{})
	if err != nil {
		t.Fatalf("implicit collision should disambiguate cleanly: %v", err)
	}
	if res.NormalizedAliases["my-server"] == res.NormalizedAliases["my_server"] {
		t.Fatalf("expected distinct disambiguated aliases, got %+v", res.NormalizedAliases)
	}

	explicitCollision := UniversalManifest{
		Servers: []UniversalServerSpec{
			{Name: "srv_a", CanonicalAlias: "shared", Command: "a"},
			{Name: "srv_b", CanonicalAlias: "shared", Command: "b"},
		},
	}
	if _, err := CompileUniversalSurface(explicitCollision, UniversalCompileOptions{}); err == nil {
		t.Fatal("expected explicit canonical alias collision error")
	}
}

func TestSecretNegativeControls_RejectFailClosedAndScrub(t *testing.T) {
	syntheticGH := "ghp_" + strings.Repeat("a", 24)
	syntheticSK := "sk-" + strings.Repeat("b", 24)
	syntheticJWT := "eyJ" + strings.Repeat("a", 12) + ".eyJ" + strings.Repeat("b", 12) + "." + strings.Repeat("c", 12)
	syntheticPEM := "-----BEGIN " + "PRIVATE KEY-----"
	rawSecretLiteral := "super-raw-credential-value-do-not-leak"

	badManifests := []struct {
		name     string
		manifest UniversalManifest
		leakText string
	}{
		{
			name: "raw_secret_env_key",
			manifest: UniversalManifest{
				Servers: []UniversalServerSpec{{
					Name:    "svc",
					Command: "svc-bin",
					Env:     map[string]string{"GEMINI_API_KEY": rawSecretLiteral},
				}},
			},
			leakText: rawSecretLiteral,
		},
		{
			name: "benign_env_key_with_gh_token",
			manifest: UniversalManifest{
				Servers: []UniversalServerSpec{{
					Name:    "svc",
					Command: "svc-bin",
					Env:     map[string]string{"CONFIG_VALUE": syntheticGH},
				}},
			},
			leakText: syntheticGH,
		},
		{
			name: "header_raw_bearer",
			manifest: UniversalManifest{
				Servers: []UniversalServerSpec{{
					Name:    "svc",
					URL:     "https://mcp.example.com/api",
					Headers: map[string]string{"Authorization": "Bearer " + rawSecretLiteral},
				}},
			},
			leakText: rawSecretLiteral,
		},
		{
			name: "url_embedded_credentials",
			manifest: UniversalManifest{
				Servers: []UniversalServerSpec{{
					Name: "svc",
					URL:  "https://alice:" + rawSecretLiteral + "@mcp.example.com/api",
				}},
			},
			leakText: rawSecretLiteral,
		},
		{
			name: "arg_sk_token",
			manifest: UniversalManifest{
				Servers: []UniversalServerSpec{{
					Name:    "svc",
					Command: "svc-bin",
					Args:    []string{"--key", syntheticSK},
				}},
			},
			leakText: syntheticSK,
		},
		{
			name: "skill_body_jwt_and_pem",
			manifest: UniversalManifest{
				Skills: []UniversalSkillAST{{
					Name:        "bad-skill",
					Description: "Bad skill",
					Body:        "Do not embed " + syntheticJWT + " or " + syntheticPEM,
				}},
			},
			leakText: syntheticJWT,
		},
	}

	for _, tc := range badManifests {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			_, err := CompileUniversalSurface(tc.manifest, UniversalCompileOptions{})
			if !errors.Is(err, ErrSecretViolation) {
				t.Fatalf("expected ErrSecretViolation, got %v", err)
			}
			if strings.Contains(err.Error(), tc.leakText) {
				t.Fatalf("error message leaked raw secret value: %v", err)
			}
		})

		t.Run("scrub_"+tc.name, func(t *testing.T) {
			res, err := CompileUniversalSurface(tc.manifest, UniversalCompileOptions{
				SecretMode: SecretModeScrub,
			})
			if err != nil {
				t.Fatalf("scrub mode failed: %v", err)
			}
			if len(res.SecretFindings) == 0 {
				t.Fatal("expected recorded SecretFindings in scrub mode")
			}
			for _, art := range res.Artifacts {
				if strings.Contains(string(art.Content), tc.leakText) {
					t.Fatalf("scrubbed artifact %s leaked secret text", art.RelPath)
				}
			}
		})
	}
}

func TestOptoinvestAndTraversalRefusal_FailClosed(t *testing.T) {
	optoDir := filepath.Join(t.TempDir(), "optoinvest", "repo")
	if err := os.MkdirAll(optoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUniversalManifest(optoDir); !errors.Is(err, ErrOptoinvestRefused) {
		t.Fatalf("expected ErrOptoinvestRefused on LoadUniversalManifest, got %v", err)
	}

	baseDir := t.TempDir()
	symlinkOpto := filepath.Join(baseDir, "harmless-link")
	if err := os.Symlink(optoDir, symlinkOpto); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSafePath(symlinkOpto); !errors.Is(err, ErrOptoinvestRefused) {
		t.Fatalf("expected ErrOptoinvestRefused via symlink resolution, got %v", err)
	}

	_, err := CompileUniversalSurface(UniversalManifest{
		Servers: []UniversalServerSpec{{
			Name:    "leaker",
			Command: "node",
			CWD:     "/srv/opto-invest/service",
		}},
	}, UniversalCompileOptions{})
	if !errors.Is(err, ErrOptoinvestRefused) {
		t.Fatalf("expected ErrOptoinvestRefused for server CWD, got %v", err)
	}

	outDir := t.TempDir()
	if err := validateTargetOutputPath(outDir, filepath.Join(outDir, "..", "escape.json")); err == nil {
		t.Fatal("expected path traversal refusal")
	}
}

func TestSyncUniversalSurface_IdempotentCheckAndDriftRejection(t *testing.T) {
	root, out := buildTestManifest(t)

	_, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   root,
		OutputRoot: out,
		Mode:       UniversalModeCheck,
	})
	if !errors.Is(err, ErrUniversalSurfaceDrift) {
		t.Fatalf("expected ErrUniversalSurfaceDrift before initial write, got %v", err)
	}

	writeReport, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   root,
		OutputRoot: out,
		Mode:       UniversalModeWrite,
	})
	if err != nil {
		t.Fatalf("write mode failed: %v", err)
	}
	if !writeReport.Passed || writeReport.Drifted {
		t.Fatalf("unexpected write report state: %+v", writeReport)
	}

	checkReport, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   root,
		OutputRoot: out,
		Mode:       UniversalModeCheck,
	})
	if err != nil {
		t.Fatalf("idempotent check after write failed: %v", err)
	}
	if !checkReport.Passed || checkReport.Drifted {
		t.Fatalf("expected clean check report, got %+v", checkReport)
	}
	for _, st := range checkReport.Artifacts {
		if st.Status != "in_sync" {
			t.Fatalf("artifact %s status = %s, want in_sync", st.RelPath, st.Status)
		}
	}

	var stdout, stderr bytes.Buffer
	rc := RunUniversalCompilerCLI([]string{"--repo", root, "--out", out, "--check"}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("CLI --check expected rc=0, got %d stderr=%s", rc, stderr.String())
	}

	driftedTarget := filepath.Join(out, ".cursor", "rules", "code-review.mdc")
	if err := os.WriteFile(driftedTarget, []byte("drifted content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	driftReport, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   root,
		OutputRoot: out,
		Mode:       UniversalModeCheck,
	})
	if !errors.Is(err, ErrUniversalSurfaceDrift) {
		t.Fatalf("expected ErrUniversalSurfaceDrift on modified target, got %v", err)
	}
	if driftReport.Passed || !driftReport.Drifted {
		t.Fatalf("expected Drifted=true Passed=false, got %+v", driftReport)
	}

	stdout.Reset()
	stderr.Reset()
	rc = RunUniversalCompilerCLI([]string{"--repo", root, "--out", out, "--check"}, &stdout, &stderr)
	if rc == 0 {
		t.Fatal("CLI --check expected non-zero exit code on drift")
	}

	if err := os.Remove(filepath.Join(out, ".gemini", "antigravity", "mcp_config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   root,
		OutputRoot: out,
		Mode:       UniversalModeCheck,
	}); !errors.Is(err, ErrUniversalSurfaceDrift) {
		t.Fatalf("expected ErrUniversalSurfaceDrift on missing target, got %v", err)
	}

	rc = RunUniversalCompilerCLI([]string{"--repo", root, "--out", out}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("CLI repair write expected rc=0, got %d stderr=%s", rc, stderr.String())
	}
	rc = RunUniversalCompilerCLI([]string{"--repo", root, "--out", out, "--check"}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("CLI post-repair --check expected rc=0, got %d stderr=%s", rc, stderr.String())
	}
}

func TestCompileUniversalSurface_RaceConcurrent(t *testing.T) {
	root, _ := buildTestManifest(t)
	manifest, err := LoadUniversalManifest(root)
	if err != nil {
		t.Fatal(err)
	}

	base, err := CompileUniversalSurface(*manifest, UniversalCompileOptions{CodexSnakeCase: true})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	digests := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, cerr := CompileUniversalSurface(*manifest, UniversalCompileOptions{CodexSnakeCase: true})
			if cerr != nil {
				errs <- cerr
				return
			}
			digests <- res.Digest
		}()
	}
	wg.Wait()
	close(errs)
	close(digests)

	for cerr := range errs {
		t.Fatalf("concurrent compile error: %v", cerr)
	}
	for d := range digests {
		if d != base.Digest {
			t.Fatalf("concurrent digest mismatch: %s != %s", d, base.Digest)
		}
	}
}
