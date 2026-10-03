package surfaceparity

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

type module struct{}

func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "surfaceparity" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"root": map[string]any{
				"type":        "string",
				"description": "Workspace root directory. Defaults to the default workspace root.",
			},
			"sync": map[string]any{
				"type":        "boolean",
				"description": "Automatically repair missing AGENTS.md, CLAUDE.md, and MCP drift.",
			},
			"scope_filter": map[string]any{
				"type":        "string",
				"description": "Filter by repo scope (e.g., active_first_party, active_operator).",
			},
			"markdown": map[string]any{
				"type":        "boolean",
				"description": "Return human-readable Markdown report instead of JSON.",
			},
		},
	}

	return []codexkit.ToolDef{
		{
			Name:        "surface_parity_audit",
			Description: "Run multi-repo surface parity sweep verifying AGENTS.md, CLAUDE.md, and MCP schemas across all 74+ workspace repos.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      schema,
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				if root == "" {
					root = workspace.DefaultRoot()
				}
				scopeFilter, _ := params["scope_filter"].(string)
				markdown, _ := params["markdown"].(bool)

				report, err := RunSweep(SweepOptions{
					WorkspaceRoot: root,
					Sync:          false,
					ScopeFilter:   scopeFilter,
				})
				if err != nil {
					return nil, fmt.Errorf("surface parity sweep: %w", err)
				}

				if markdown {
					return report.Markdown(), nil
				}
				return report, nil
			},
		},
		{
			Name:        "surface_parity_sync",
			Description: "Automatically synchronize and repair AGENTS.md, CLAUDE.md, and MCP schemas across all 74+ workspace repos.",
			Annotations: codexkit.ToolAnnotations(false, false, true, true),
			Schema:      schema,
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				if root == "" {
					root = workspace.DefaultRoot()
				}
				scopeFilter, _ := params["scope_filter"].(string)
				markdown, _ := params["markdown"].(bool)

				report, err := RunSweep(SweepOptions{
					WorkspaceRoot: root,
					Sync:          true,
					ScopeFilter:   scopeFilter,
				})
				if err != nil {
					return nil, fmt.Errorf("surface parity sync: %w", err)
				}

				if markdown {
					return report.Markdown(), nil
				}
				return report, nil
			},
		},
	}
}
