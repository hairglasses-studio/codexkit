package sourcecontract

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

type module struct{}

// Module returns a ToolModule exposing workspace source-contract checks.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "sourcecontract" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "workspace_source_contract_check",
			Description: "Validate workspace source contracts for manifest, managed skills, MCP config drift, runtime inventory, and global MCP projection artifacts.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root": map[string]any{
						"type":        "string",
						"description": "Workspace root. Defaults to ~/hairglasses-studio.",
					},
					"skills_only": map[string]any{
						"type":        "boolean",
						"description": "Check managed skill surfaces and workspace manifest only.",
					},
					"tools_only": map[string]any{
						"type":        "boolean",
						"description": "Check MCP sources, runtime inventory, and workspace manifest only.",
					},
					"skip_runtime_inventory": map[string]any{
						"type":        "boolean",
						"description": "Skip runtime inventory artifact validation.",
					},
					"json_path": map[string]any{
						"type":        "string",
						"description": "Optional existing source-contract JSON artifact to compare with the live report.",
					},
					"skill_validator": map[string]any{
						"type":        "string",
						"description": "External skill validator mode: auto, host, pinned, or off.",
						"enum":        []string{"auto", "host", "pinned", "off"},
					},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				validatorRaw, _ := params["skill_validator"].(string)
				validatorMode, err := skillsync.ParseValidatorMode(validatorRaw)
				if err != nil {
					return nil, err
				}
				skillsOnly, _ := params["skills_only"].(bool)
				toolsOnly, _ := params["tools_only"].(bool)
				if skillsOnly && toolsOnly {
					return nil, fmt.Errorf("skills_only and tools_only cannot both be true")
				}
				skipRuntimeInventory, _ := params["skip_runtime_inventory"].(bool)
				report, err := Check(root, CheckOptions{
					SkillsOnly:           skillsOnly,
					ToolsOnly:            toolsOnly,
					SkipRuntimeInventory: skipRuntimeInventory,
					SkillValidatorMode:   validatorMode,
				})
				if err != nil {
					return nil, err
				}
				jsonPath, _ := params["json_path"].(string)
				if jsonPath != "" {
					artifact := CheckArtifact(report, jsonPath)
					report.Artifact = &artifact
					if !artifact.Passed {
						report.Passed = false
					}
				}
				return report, nil
			},
		},
	}
}
