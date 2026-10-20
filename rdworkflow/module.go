// Package rdworkflow adapts the deterministic harness workflow CLI to MCP.
package rdworkflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit"
)

type config struct {
	backend, binding, stateDir, evidenceDir, pool, claimDB string
}

type module struct{ config config }

// Module captures server-owned paths once. MCP callers cannot change authority,
// executable paths, transport, or the native session/account configuration.
func Module() codexkit.ToolModule {
	home, _ := os.UserHomeDir()
	workspace := env("HAIRGLASSES_STUDIO_ROOT", filepath.Join(home, "hairglasses-studio"))
	state := env("CODEXKIT_RD_WORKFLOW_STATE_DIR", filepath.Join(home, ".local/state/hg-codex-rd-workflow"))
	return &module{config: config{
		backend:     env("CODEXKIT_RD_WORKFLOW_BACKEND", filepath.Join(workspace, "claude-harness/bin/hg-codex-rd-workflow")),
		binding:     os.Getenv("CODEXKIT_RD_WORKFLOW_CONFIG"),
		stateDir:    state,
		evidenceDir: env("CODEXKIT_RD_WORKFLOW_EVIDENCE_DIR", filepath.Join(state, "evidence")),
		pool:        env("HG_POOL_DIR", env("POOL_DIR", filepath.Join(workspace, ".agents/pool"))),
		claimDB:     env("HG_CLAIM_DB", filepath.Join(home, ".local/state/claude-harness/claims.sqlite")),
	}}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (m *module) Name() string { return "rdworkflow" }
func (m *module) Init() error  { return nil }

type request struct {
	Apply       bool   `json:"apply"`
	Phase       string `json:"phase"`
	Identity    string `json:"identity"`
	SummaryFile string `json:"summary_file"`
	TaskID      string `json:"task_id"`
	ProgressID  string `json:"progress_id"`
	Result      string `json:"result"`
	PlanID      string `json:"plan_id"`
	Receipt     string `json:"receipt"`
	Inbox       string `json:"inbox"`
}

func (m *module) Tools() []codexkit.ToolDef {
	var tools []codexkit.ToolDef
	for _, verb := range []string{"plan", "intake", "record", "status"} {
		properties := map[string]any{}
		var required []string
		if verb != "status" {
			properties["apply"] = map[string]any{"type": "boolean", "default": false, "description": "Explicitly persist a local reservation or validated receipt; default previews only. Never sends or executes work."}
		}
		artifact := func(name string) {
			properties[name] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]*$", "description": "Existing regular evidence filename under the server-owned evidence directory; no paths or symlinks."}
		}
		identifier := func(name string) {
			properties[name] = map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]*$"}
		}
		switch verb {
		case "plan":
			properties["phase"] = map[string]any{"type": "string", "enum": []string{"offer", "idle", "report"}}
			artifact("identity")
			artifact("summary_file")
			identifier("task_id")
			identifier("progress_id")
			artifact("result")
			required = []string{"phase", "identity", "summary_file"}
		case "intake":
			artifact("identity")
			artifact("inbox")
			required = []string{"identity", "inbox"}
		case "record":
			identifier("plan_id")
			artifact("receipt")
			required = []string{"plan_id", "receipt"}
		}
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		verb, properties := verb, properties
		tools = append(tools, codexkit.ToolDef{
			Name: "rd_workflow_" + verb,
			Description: map[string]string{
				"plan":   "Preview an addressed R&D offer, progress report, or idle request; explicit apply reserves it locally. No message is sent.",
				"intake": "Validate exact-peer handoffs from saved evidence; explicit apply records intake locally. Never acknowledges, claims, or launches work.",
				"record": "Validate a supplied transport receipt against a reserved plan; explicit apply records it locally. Does not perform transport.",
				"status": "Read bounded workflow reservation and evidence status. UNKNOWN remains distinct from success.",
			}[verb],
			Annotations: codexkit.ToolAnnotations(verb == "status", false, verb == "status", false),
			Schema:      schema,
			ContextHandler: func(ctx context.Context, params map[string]any) (any, error) {
				for key := range params {
					if _, ok := properties[key]; !ok {
						return nil, fmt.Errorf("unknown workflow parameter %q", key)
					}
				}
				input, err := codexkit.DecodeParams[request](params)
				if err != nil {
					return nil, err
				}
				return m.invoke(ctx, verb, input), nil
			},
		})
	}
	return tools
}
