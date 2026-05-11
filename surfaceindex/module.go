package surfaceindex

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

type module struct{}

// Module returns a ToolModule exposing the workspace surface index.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "surfaceindex" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "workspace_surface_index",
			Description: "Build a baseline repo index of canonical skill sources, MCP sources, generated mirrors, runtime projection status, and consolidation decisions.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root": map[string]any{
						"type":        "string",
						"description": "Workspace root. Defaults to ~/hairglasses-studio.",
					},
					"skill_validator": map[string]any{
						"type":        "string",
						"description": "External skill validator mode: auto, host, pinned, or off. Defaults to off for stable index output.",
						"enum":        []string{"auto", "host", "pinned", "off"},
					},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				validatorRaw, _ := params["skill_validator"].(string)
				validatorMode := skillsync.ValidatorOff
				if validatorRaw != "" {
					parsed, err := skillsync.ParseValidatorMode(validatorRaw)
					if err != nil {
						return nil, err
					}
					validatorMode = parsed
				}
				index, err := Build(Options{
					WorkspaceRoot:      root,
					SkillValidatorMode: validatorMode,
				})
				if err != nil {
					return nil, fmt.Errorf("build surface index: %w", err)
				}
				return index, nil
			},
		},
	}
}
