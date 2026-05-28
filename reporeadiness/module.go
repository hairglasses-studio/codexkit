package reporeadiness

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

type module struct{}

// Module returns a ToolModule exposing repo mutation-readiness scoring.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "reporeadiness" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"root": map[string]any{
				"type":        "string",
				"description": "Workspace root. Defaults to the default workspace root.",
			},
			"all_scopes": map[string]any{
				"type":        "boolean",
				"description": "Include inactive and compatibility-only repos.",
			},
			"markdown": map[string]any{
				"type":        "boolean",
				"description": "Return Markdown instead of structured JSON.",
			},
		},
	}
	return []codexkit.ToolDef{
		{
			Name:        "repo_readiness_score",
			Description: "Score workspace repos for autonomous mutation readiness using manifest, fleet, git, and baseline signals.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema:      schema,
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				if root == "" {
					root = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				report, err := Score(root, Options{AllScopes: allScopes})
				if err != nil {
					return nil, fmt.Errorf("score repo readiness: %w", err)
				}
				markdown, _ := params["markdown"].(bool)
				if markdown {
					return report.Markdown(), nil
				}
				return report, nil
			},
		},
	}
}
