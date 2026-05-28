package primitiveindex

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns a ToolModule exposing the workspace primitive index.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "primitiveindex" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "workspace_primitive_index",
			Description: "Build a workspace index of Claude hooks/settings, provider agents, plugin manifests, nested MCP files, and other LLM agent primitives.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root": map[string]any{
						"type":        "string",
						"description": "Workspace root. Defaults to the default workspace root.",
					},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				index, err := Build(Options{WorkspaceRoot: root})
				if err != nil {
					return nil, fmt.Errorf("build primitive index: %w", err)
				}
				return index, nil
			},
		},
	}
}
