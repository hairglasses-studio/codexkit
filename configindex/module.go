package configindex

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns the provider configuration inventory module.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "configindex" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	properties := map[string]any{
		"root":          map[string]any{"type": "string", "description": "Workspace root."},
		"user_home":     map[string]any{"type": "string", "description": "Primary user home to inventory."},
		"root_home":     map[string]any{"type": "string", "description": "Root home to inventory."},
		"runtime_stats": map[string]any{"type": "boolean", "description": "Count files and bytes in runtime buckets."},
	}
	buildOptions := func(params map[string]any) Options {
		root, _ := params["root"].(string)
		userHome, _ := params["user_home"].(string)
		rootHome, _ := params["root_home"].(string)
		runtimeStats, _ := params["runtime_stats"].(bool)
		var profiles []Profile
		if userHome != "" {
			profiles = append(profiles, Profile{Name: "user", Home: userHome})
		}
		if rootHome != "" {
			profiles = append(profiles, Profile{Name: "root", Home: rootHome})
		}
		return Options{WorkspaceRoot: root, Profiles: profiles, IncludeRuntimeStats: runtimeStats}
	}
	return []codexkit.ToolDef{
		{
			Name: "workspace_config_index", Description: "Inventory and classify Claude, Codex, AGY, dotfiles, and provider-home configuration without exposing secret values.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      map[string]any{"type": "object", "properties": properties},
			Handler: func(params map[string]any) (any, error) {
				index, err := Build(buildOptions(params))
				if err != nil {
					return nil, fmt.Errorf("build config index: %w", err)
				}
				return index, nil
			},
		},
		{
			Name: "workspace_config_check", Description: "Check the strict Claude/Codex/AGY configuration ownership and autonomy-default contract.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      map[string]any{"type": "object", "properties": properties},
			Handler: func(params map[string]any) (any, error) {
				report, err := Check(buildOptions(params))
				if err != nil {
					return nil, fmt.Errorf("check config index: %w", err)
				}
				return report, nil
			},
		},
	}
}
