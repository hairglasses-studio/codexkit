package rdworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("CODEXKIT_RD_TEST_CHILD"); mode != "" {
		switch mode {
		case "unknown":
			fmt.Print(`{"state":"UNKNOWN","reason":"fixture-evidence-absent"}`)
			os.Exit(3)
		case "mismatch":
			fmt.Print(`{"state":"ok"}`)
			os.Exit(3)
		case "multiple":
			fmt.Print(`{"state":"ok"} {"state":"ok"}`)
		case "array":
			fmt.Print(`[]`)
		case "missing-state":
			fmt.Print(`{"ready":true}`)
		case "stdout-cap":
			fmt.Print(strings.Repeat("x", maxStdoutBytes+1))
		case "stderr-cap":
			fmt.Fprint(os.Stderr, strings.Repeat("x", maxStderrBytes+1))
		case "hang":
			time.Sleep(time.Minute)
		default:
			args := os.Args[1:]
			artifacts := map[string]string{}
			for i, arg := range args {
				if i+1 < len(args) && (arg == "--identity" || arg == "--summary-file" || arg == "--inbox" || arg == "--receipt" || arg == "--result") {
					data, err := os.ReadFile(args[i+1])
					if err != nil {
						os.Exit(9)
					}
					artifacts[arg] = string(data)
				}
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "preview", "args": args, "artifacts": artifacts})
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) *module {
	t.Helper()
	root := t.TempDir()
	evidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"identity.json", "summary.txt", "inbox.json", "receipt.json", "result.json"} {
		if err := os.WriteFile(filepath.Join(evidence, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEXKIT_RD_TEST_CHILD", "echo")
	return &module{config: config{backend: backend, binding: filepath.Join(root, "binding.json"), stateDir: filepath.Join(root, "state"), evidenceDir: evidence, pool: filepath.Join(root, "pool"), claimDB: filepath.Join(root, "claims.sqlite")}}
}

func call(t *testing.T, m *module, name string, params map[string]any) map[string]any {
	t.Helper()
	registry := codexkit.NewRegistry()
	if err := registry.Register(m); err != nil {
		t.Fatal(err)
	}
	result, err := registry.CallContext(context.Background(), name, params)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func argsFrom(t *testing.T, result map[string]any) []string {
	t.Helper()
	raw, ok := result["args"].([]any)
	if !ok {
		t.Fatalf("backend not invoked: %v", result)
	}
	var out []string
	for _, value := range raw {
		out = append(out, value.(string))
	}
	return out
}

func TestToolSchemasAndAuthority(t *testing.T) {
	m := fixture(t)
	tools := m.Tools()
	if len(tools) != 4 {
		t.Fatal(len(tools))
	}
	for _, tool := range tools {
		if tool.ContextHandler == nil || tool.Schema["additionalProperties"] != false {
			t.Fatal(tool.Name)
		}
		if got := tool.Annotations["readOnlyHint"]; got != (tool.Name == "rd_workflow_status") {
			t.Fatalf("wrong annotation %s: %v", tool.Name, got)
		}
		for _, bad := range []string{"backend", "config", "state_dir", "command", "args", "pool", "claim_db"} {
			if _, err := tool.ContextHandler(context.Background(), map[string]any{bad: "override"}); err == nil {
				t.Fatalf("accepted caller authority %s", bad)
			}
		}
	}
}

func TestPlanPreviewAndExplicitApply(t *testing.T) {
	m := fixture(t)
	params := map[string]any{"phase": "report", "identity": "identity.json", "summary_file": "summary.txt", "task_id": "TASK-1", "progress_id": "commit-1", "result": "result.json"}
	result := call(t, m, "rd_workflow_plan", params)
	args := argsFrom(t, result)
	if args[0] != "--config" || args[1] != m.config.binding || args[4] != "plan" || contains(args, "--apply") {
		t.Fatal(args)
	}
	artifacts := result["artifacts"].(map[string]any)
	if artifacts["--summary-file"] != "summary.txt" || artifacts["--result"] != "result.json" {
		t.Fatal(artifacts)
	}
	for i, arg := range args {
		if arg == "--identity" {
			if strings.HasPrefix(args[i+1], m.config.evidenceDir) {
				t.Fatal("backend received mutable original evidence")
			}
			if _, err := os.Stat(args[i+1]); !os.IsNotExist(err) {
				t.Fatal("temporary evidence snapshot retained")
			}
		}
	}
	if _, err := os.Stat(m.config.stateDir); !os.IsNotExist(err) {
		t.Fatal("preview created workflow state")
	}
	params["apply"] = true
	if args := argsFrom(t, call(t, m, "rd_workflow_plan", params)); !contains(args, "--apply") {
		t.Fatal(args)
	}
}

func TestIntakeRecordAndStatusArgv(t *testing.T) {
	m := fixture(t)
	args := argsFrom(t, call(t, m, "rd_workflow_intake", map[string]any{"identity": "identity.json", "inbox": "inbox.json"}))
	if !contains(args, m.config.pool) || !contains(args, m.config.claimDB) || contains(args, "--apply") {
		t.Fatal(args)
	}
	args = argsFrom(t, call(t, m, "rd_workflow_record", map[string]any{"plan_id": "abc123", "receipt": "receipt.json", "apply": true}))
	if !contains(args, "abc123") || !contains(args, "--apply") {
		t.Fatal(args)
	}
	args = argsFrom(t, call(t, m, "rd_workflow_status", nil))
	want := []string{"--config", m.config.binding, "--state-dir", m.config.stateDir, "status"}
	if !reflect.DeepEqual(args, want) {
		t.Fatal(args)
	}
}

func TestServerConfigurationCapturedAtModuleConstruction(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("HAIRGLASSES_STUDIO_ROOT", filepath.Join(root, "workspace"))
	t.Setenv("CODEXKIT_RD_WORKFLOW_CONFIG", filepath.Join(root, "configured.json"))
	t.Setenv("CODEXKIT_RD_WORKFLOW_BACKEND", filepath.Join(root, "backend"))
	t.Setenv("CODEXKIT_RD_WORKFLOW_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("CODEXKIT_RD_WORKFLOW_EVIDENCE_DIR", filepath.Join(root, "evidence"))
	t.Setenv("HG_POOL_DIR", filepath.Join(root, "pool"))
	t.Setenv("HG_CLAIM_DB", filepath.Join(root, "db"))
	m := Module().(*module)
	t.Setenv("CODEXKIT_RD_WORKFLOW_CONFIG", "changed")
	if m.config.binding != filepath.Join(root, "configured.json") || m.config.pool != filepath.Join(root, "pool") {
		t.Fatal(m.config)
	}
}

func contains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
