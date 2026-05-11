package workspace

import (
	"os"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns workspace-level manifest and validation tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "workspace" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "workspace_manifest_generate",
			Description: "Generate the canonical workspace manifest from live repos and docs metadata. Defaults to dry-run unless write is true.",
			Annotations: codexkit.ToolAnnotations(false, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root":  map[string]any{"type": "string", "description": "Workspace root. Defaults to ~/hairglasses-studio."},
					"write": map[string]any{"type": "boolean", "description": "Write workspace/manifest.json when true."},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				if root == "" {
					root = DefaultRoot()
				}
				report, err := GenerateManifest(root)
				if err != nil {
					return nil, err
				}
				write, _ := params["write"].(bool)
				if write {
					if err := WriteManifest(root, report.Manifest); err != nil {
						return nil, err
					}
				}
				return report, nil
			},
		},
		{
			Name:        "workspace_check",
			Description: "Validate workspace/manifest.json, live repo directories, go.work membership, and consolidation matrix compatibility.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root": map[string]any{"type": "string", "description": "Workspace root. Defaults to ~/hairglasses-studio."},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				root, _ := params["root"].(string)
				if root == "" {
					root = DefaultRoot()
				}
				manifest, err := LoadManifest(root)
				if err != nil {
					if os.IsNotExist(err) {
						report, genErr := GenerateManifest(root)
						if genErr != nil {
							return nil, genErr
						}
						return Check(root, report.Manifest), nil
					}
					return nil, err
				}
				return Check(root, manifest), nil
			},
		},
	}
}
