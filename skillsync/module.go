package skillsync

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns a ToolModule exposing skill sync tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "skillsync" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "skill_sync",
			Description: "Sync skills from .agents/skills/ to .claude/skills/ and plugins/ mirrors",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repository"},
					"dry_run":   map[string]any{"type": "boolean", "description": "Preview changes without writing"},
				},
				"required": []string{"repo_path"},
			},
			Handler: func(params map[string]any) (any, error) {
				repoPath, _ := params["repo_path"].(string)
				if repoPath == "" {
					return nil, fmt.Errorf("repo_path is required")
				}
				dryRun, _ := params["dry_run"].(bool)
				return Sync(repoPath, dryRun), nil
			},
		},
		{
			Name:        "skill_diff",
			Description: "Show what skill sync would change (dry-run)",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repository"},
				},
				"required": []string{"repo_path"},
			},
			Handler: func(params map[string]any) (any, error) {
				repoPath, _ := params["repo_path"].(string)
				if repoPath == "" {
					return nil, fmt.Errorf("repo_path is required")
				}
				return Diff(repoPath), nil
			},
		},
		{
			Name:        "skill_list",
			Description: "List skills defined in surface.yaml",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repository"},
				},
				"required": []string{"repo_path"},
			},
			Handler: func(params map[string]any) (any, error) {
				repoPath, _ := params["repo_path"].(string)
				if repoPath == "" {
					return nil, fmt.Errorf("repo_path is required")
				}
				return List(repoPath)
			},
		},
	}
}
