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
			Annotations: codexkit.ToolAnnotations(false, false, true, true),
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
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
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
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
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
		{
			Name:        "mcp_runtime_inventory",
			Description: "Build a manifest-backed runtime MCP projection inventory without starting device-bound services.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_root": map[string]any{"type": "string", "description": "Workspace root. Defaults to ~/hairglasses-studio."},
					"policy_path":    map[string]any{"type": "string", "description": "Optional global MCP policy path."},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				workspaceRoot, _ := params["workspace_root"].(string)
				policyPath, _ := params["policy_path"].(string)
				return BuildRuntimeInventory(RuntimeInventoryOptions{
					WorkspaceRoot: workspaceRoot,
					PolicyPath:    policyPath,
				})
			},
		},
		{
			Name:        "workspace_global_mcp_projection",
			Description: "Build the workspace-global MCP provider projection for Codex, Claude, and Gemini from manifest-backed sources.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_root": map[string]any{"type": "string", "description": "Workspace root. Defaults to ~/hairglasses-studio."},
					"policy_path":    map[string]any{"type": "string", "description": "Optional global MCP policy path."},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				workspaceRoot, _ := params["workspace_root"].(string)
				policyPath, _ := params["policy_path"].(string)
				return BuildGlobalProjection(GlobalProjectionOptions{
					WorkspaceRoot: workspaceRoot,
					PolicyPath:    policyPath,
				})
			},
		},
	}
}
