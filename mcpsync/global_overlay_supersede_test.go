package mcpsync

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

const supersedeNote = "# superseded by generated block (hg-workspace-global-sync) "

func syncCodexOnly(t *testing.T, root, home string, mode GlobalOverlaySyncMode) (GlobalOverlaySyncReport, error) {
	t.Helper()
	return SyncGlobalProviderOverlays(GlobalOverlaySyncOptions{
		WorkspaceRoot:    root,
		GeneratedAt:      "2026-05-10T00:00:00Z",
		Mode:             mode,
		ClaudeJSONPath:   filepath.Join(home, ".claude.json"),
		ClaudeProjectKey: root,
		CodexConfigPath:  filepath.Join(home, ".codex", "config.toml"),
		GeminiConfigPath: filepath.Join(home, ".gemini", "settings.json"),
	})
}

func liveTableCount(content, header string) int {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(header) + `[ \t]*$`)
	return len(re.FindAllString(content, -1))
}

func TestSyncSupersedesUnmarkedServerTablesWithoutMarkers(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)
	home := t.TempDir()
	codexPath := filepath.Join(home, ".codex", "config.toml")
	writeRuntimeTestFile(t, home, ".codex/config.toml", `model = "gpt-5.6-sol"

[mcp_servers.studio_desktop]
command = "old-desktop"
args = [
  "[mcp_servers.studio_desktop]",
  ["nested"],
]

[mcp_servers.studio_desktop.env]
OLD = "1"

[mcp_servers.manual]
command = "manual"

[mcp_servers.studio_desktop_other]
command = "keep"
`)

	report, err := syncCodexOnly(t, root, home, GlobalOverlaySyncWrite)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("synced config is not valid TOML: %v\n%s", err, content)
	}
	if n := liveTableCount(content, "[mcp_servers.studio_desktop]"); n != 1 {
		t.Fatalf("live [mcp_servers.studio_desktop] tables = %d, want 1:\n%s", n, content)
	}
	if n := liveTableCount(content, "[mcp_servers.studio_desktop.env]"); n != 1 {
		t.Fatalf("live [mcp_servers.studio_desktop.env] tables = %d, want 1:\n%s", n, content)
	}
	for _, want := range []string{
		"# [mcp_servers.studio_desktop]\n",
		"# command = \"old-desktop\"\n",
		"#   [\"nested\"],\n",
		"# [mcp_servers.studio_desktop.env]\n",
		"# OLD = \"1\"\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("superseded content missing %q:\n%s", want, content)
		}
	}
	if n := strings.Count(content, supersedeNote); n != 1 {
		t.Fatalf("dated supersede lines = %d, want 1:\n%s", n, content)
	}
	if !regexp.MustCompile(regexp.QuoteMeta(supersedeNote) + `\d{4}-\d{2}-\d{2}\n`).MatchString(content) {
		t.Fatalf("supersede line has no date:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.manual]\ncommand = \"manual\"\n") ||
		!strings.Contains(content, "[mcp_servers.studio_desktop_other]\ncommand = \"keep\"\n") {
		t.Fatalf("unrelated tables must stay live and untouched:\n%s", content)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	desktop, _ := servers["studio_desktop"].(map[string]any)
	if desktop == nil || desktop["command"] == "old-desktop" {
		t.Fatalf("live studio_desktop is not the generated one: %#v", desktop)
	}

	var codexAction *GlobalOverlaySyncAction
	for i := range report.Actions {
		if report.Actions[i].Provider == "codex" {
			codexAction = &report.Actions[i]
		}
	}
	if codexAction == nil || codexAction.Status != "updated" || !strings.Contains(codexAction.Message, "studio_desktop") {
		t.Fatalf("codex action does not report superseded names: %+v", codexAction)
	}
	if md := report.Markdown(); !strings.Contains(md, "superseded unmarked mcp_servers tables") || !strings.Contains(md, "studio_desktop") {
		t.Fatalf("markdown output does not report superseded names:\n%s", md)
	}

	check, err := syncCodexOnly(t, root, home, GlobalOverlaySyncCheck)
	if err != nil {
		t.Fatal(err)
	}
	if check.Pending {
		t.Fatalf("second sync must be a no-op: %+v", check.Actions)
	}
}

func TestSyncWithMarkersReplacesBlockAndCommentsNothing(t *testing.T) {
	root := setupGlobalProjectionWorkspace(t)
	home := t.TempDir()
	codexPath := filepath.Join(home, ".codex", "config.toml")
	writeRuntimeTestFile(t, home, ".codex/config.toml", "model = \"gpt-5.6-sol\"\n\n"+
		WorkspaceGlobalStartMarker+"\n[mcp_servers.studio_desktop]\ncommand = \"stale-inside-markers\"\n"+WorkspaceGlobalEndMarker+"\n\n"+
		"[mcp_servers.manual]\ncommand = \"manual\"\n")

	report, err := syncCodexOnly(t, root, home, GlobalOverlaySyncWrite)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(after)
	if strings.Contains(content, "stale-inside-markers") {
		t.Fatalf("marked block was not replaced:\n%s", content)
	}
	if strings.Contains(content, supersedeNote) || strings.Contains(content, "# [mcp_servers.") {
		t.Fatalf("nothing may be commented when markers are present:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.manual]\ncommand = \"manual\"\n") {
		t.Fatalf("user table outside markers must survive:\n%s", content)
	}
	var doc map[string]any
	if err := toml.Unmarshal(after, &doc); err != nil {
		t.Fatalf("synced config is not valid TOML: %v\n%s", err, content)
	}
	for _, action := range report.Actions {
		if action.Provider == "codex" && action.Message != "" {
			t.Fatalf("codex action must not report superseded names: %+v", action)
		}
	}
}

func TestUpsertWorkspaceGlobalBlockDatesSupersedeLine(t *testing.T) {
	block := WorkspaceGlobalStartMarker + "\n\n[mcp_servers.studio_workspace_gateway]\ncommand = \"gw\"\n\n[mcp_servers.studio_workspace_gateway.env]\nA = \"b\"\n\n" + WorkspaceGlobalEndMarker + "\n"
	existing := "model = \"m\"\n\n[mcp_servers.studio_workspace_gateway]\ncommand = \"old\"\n\n[mcp_servers.studio_workspace_gateway.env]\nOLD = \"1\"\n"
	got, names := upsertWorkspaceGlobalBlock(existing, block, "2026-10-24")
	if len(names) != 1 || names[0] != "studio_workspace_gateway" {
		t.Fatalf("superseded names = %v", names)
	}
	want := supersedeNote + "2026-10-24\n# [mcp_servers.studio_workspace_gateway]\n# command = \"old\"\n\n# [mcp_servers.studio_workspace_gateway.env]\n# OLD = \"1\"\n"
	if !strings.Contains(got, want) {
		t.Fatalf("commented tables not found in place:\n%s", got)
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("result is not valid TOML: %v\n%s", err, got)
	}
}

func TestApplyRenderedFileRefusesInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := "model = \"m\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := []byte("[mcp_servers.a]\ncommand = \"x\"\n\n[mcp_servers.a]\ncommand = \"y\"\n")

	for _, mode := range []GlobalOverlaySyncMode{GlobalOverlaySyncWrite, GlobalOverlaySyncDryRun, GlobalOverlaySyncCheck} {
		action := applyRenderedFile(path, invalid, mode)
		if action.Status != "error" {
			t.Fatalf("mode %s: status = %q, want error", mode, action.Status)
		}
		if !strings.Contains(action.Message, path) || !strings.Contains(action.Message, "invalid TOML") {
			t.Fatalf("mode %s: message must name the file and parser error: %q", mode, action.Message)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("file changed despite refusal:\n%s", got)
	}

	valid := applyRenderedFile(path, []byte("model = \"n\"\n"), GlobalOverlaySyncWrite)
	if valid.Status != "updated" {
		t.Fatalf("valid TOML must still be written: %+v", valid)
	}

	jsonPath := filepath.Join(dir, "settings.json")
	if action := applyRenderedFile(jsonPath, []byte("{not toml}"), GlobalOverlaySyncWrite); action.Status != "updated" {
		t.Fatalf("non-TOML targets must not be TOML-validated: %+v", action)
	}
}
