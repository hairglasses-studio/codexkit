package mcpserver

import "github.com/hairglasses-studio/codexkit"

type metaModule struct {
	registry *codexkit.Registry
	info     ServerInfo
}

func Module(registry *codexkit.Registry, info ServerInfo) codexkit.ToolModule {
	return &metaModule{registry: registry, info: info}
}

func (m *metaModule) Name() string { return "codexkit" }

func (m *metaModule) Init() error { return nil }

func (m *metaModule) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "codexkit_server_health",
			Description: "Report codexkit MCP server health, module coverage, and protocol surface counts.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Handler: func(_ map[string]any) (any, error) {
				return map[string]any{
					"server":         m.info,
					"module_count":   len(m.registry.ListModules()),
					"tool_count":     len(m.registry.ListTools()),
					"resource_count": 2,
					"prompt_count":   1,
				}, nil
			},
		},
	}
}
