package agyloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hairglasses-studio/codexkit"
)

type ModuleImpl struct{}

func Module() codexkit.ToolModule {
	return &ModuleImpl{}
}

func (m *ModuleImpl) Name() string {
	return "agyloop"
}

func (m *ModuleImpl) Init() error {
	return nil
}

func (m *ModuleImpl) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "agy_loop",
			Description: "Run autonomous Ralph Wiggum iteration loop using AGY 2.0 and Gemini 3.7 Flash",
			Annotations: codexkit.ToolAnnotations(false, false, false, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{
						"type":        "string",
						"description": "Path to target repository",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Model identifier (default: gemini-3.7-flash-high)",
					},
					"reasoning_effort": map[string]any{
						"type":        "string",
						"description": "Reasoning effort (high, medium, low)",
					},
					"max_iterations": map[string]any{
						"type":        "integer",
						"description": "Maximum loop iterations (default: 5)",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Iteration prompt / task goal",
					},
					"verification_cmd": map[string]any{
						"type":        "string",
						"description": "Verification command (e.g. make check-all, go test ./...)",
					},
					"auto_commit": map[string]any{
						"type":        "boolean",
						"description": "Automatically stage and commit verified rounds",
					},
					"auto_push": map[string]any{
						"type":        "boolean",
						"description": "Automatically push green branch to remote origin",
					},
					"dry_run": map[string]any{
						"type":        "boolean",
						"description": "Preview loop without executing subprocesses",
					},
				},
			},
			Handler: codexkit.TypedHandler(func(req LoopOptions) (any, error) {
				return RunLoop(context.Background(), req)
			}),
		},
		{
			Name:        "agy_teamwork",
			Description: "Coordinate multi-agent teamwork sessions with AGY 2.0 and Gemini 3.7 Flash across disjoint worker scopes",
			Annotations: codexkit.ToolAnnotations(false, false, false, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{
						"type":        "string",
						"description": "Path to target repository",
					},
					"topology": map[string]any{
						"type":        "string",
						"description": "Team topology (star, chain, blackboard)",
					},
					"workers": map[string]any{
						"type":        "integer",
						"description": "Number of concurrent workers (default: 2, max: 4)",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Master task description",
					},
					"scopes": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "List of disjoint scope strings or target paths",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Model identifier (default: gemini-3.7-flash-high)",
					},
					"auto_merge": map[string]any{
						"type":        "boolean",
						"description": "Automatically merge green worker outputs",
					},
					"auto_push": map[string]any{
						"type":        "boolean",
						"description": "Automatically push master result to remote origin",
					},
					"dry_run": map[string]any{
						"type":        "boolean",
						"description": "Preview teamwork plan without executing subprocesses",
					},
				},
			},
			Handler: codexkit.TypedHandler(func(req TeamworkOptions) (any, error) {
				return RunTeamwork(context.Background(), req)
			}),
		},
		{
			Name:        "agy_status",
			Description: "Inspect loop status, progress, and HMAC attestation receipts for a repository",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{
						"type":        "string",
						"description": "Path to target repository",
					},
				},
			},
			Handler: codexkit.TypedHandler(func(req struct {
				RepoPath string `json:"repo_path"`
			}) (any, error) {
				repo := req.RepoPath
				if repo == "" {
					repo = "."
				}
				progressPath := filepath.Join(repo, ".ralph", "progress.json")
				data, err := os.ReadFile(progressPath)
				if err != nil {
					return map[string]any{
						"status":  "no_active_loop",
						"message": fmt.Sprintf("no .ralph/progress.json found in %s", repo),
					}, nil
				}
				var report LoopReport
				if err := json.Unmarshal(data, &report); err != nil {
					return nil, err
				}
				return report, nil
			}),
		},
		{
			Name:        "agy_autopilot",
			Description: "Run autonomous fleet autopilot across all ready repositories using AGY 2.0 and Gemini 3.7 Flash",
			Annotations: codexkit.ToolAnnotations(false, false, false, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_root": map[string]any{
						"type":        "string",
						"description": "Path to workspace root (defaults to current directory)",
					},
					"concurrency": map[string]any{
						"type":        "integer",
						"description": "Concurrent worker sessions (default: 4, max: 8)",
					},
					"max_iterations": map[string]any{
						"type":        "integer",
						"description": "Max iterations per repo (default: 3)",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Model identifier (default: gemini-3.7-flash-high)",
					},
					"reasoning_effort": map[string]any{
						"type":        "string",
						"description": "Reasoning effort (high, medium, low)",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Master unification/modernization task prompt",
					},
					"auto_commit": map[string]any{
						"type":        "boolean",
						"description": "Auto commit verified rounds",
					},
					"auto_push": map[string]any{
						"type":        "boolean",
						"description": "Auto push verified commits to origin",
					},
					"dry_run": map[string]any{
						"type":        "boolean",
						"description": "Dry run preview mode",
					},
				},
			},
			Handler: codexkit.TypedHandler(func(req FleetAutopilotOptions) (any, error) {
				return RunFleetAutopilot(context.Background(), req)
			}),
		},
	}
}
