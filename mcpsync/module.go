package mcpsync

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns a ToolModule exposing MCP sync tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "mcpsync" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "mcp_sync",
			Description: "Sync MCP server definitions from .mcp.json to .codex/config.toml",
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
			Name:        "mcp_diff",
			Description: "Show what MCP sync would change (dry-run)",
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
			Name:        "mcp_list",
			Description: "List MCP servers defined in .mcp.json",
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
