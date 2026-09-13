package mcpserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hairglasses-studio/codexkit"
)

type metaModule struct {
	registry *codexkit.Registry
	info     ServerInfo
	extra    SurfaceCounts
}

// SurfaceCounts reports SDK-native extensions that are mounted outside the
// compatibility ToolModule registry.
type SurfaceCounts struct {
	Tools             int
	Resources         int
	ResourceTemplates int
	Prompts           int
	ToolDefinitions   []codexkit.ToolDef
}

type toolSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type toolSchemaParams struct {
	Name string `json:"name"`
}

func Module(registry *codexkit.Registry, info ServerInfo, extensions ...SurfaceCounts) codexkit.ToolModule {
	extra := mergeSurfaceCounts(extensions...)
	return &metaModule{registry: registry, info: info, extra: extra}
}

func mergeSurfaceCounts(extensions ...SurfaceCounts) SurfaceCounts {
	var extra SurfaceCounts
	for _, extension := range extensions {
		extra.Tools += extension.Tools
		extra.Resources += extension.Resources
		extra.ResourceTemplates += extension.ResourceTemplates
		extra.Prompts += extension.Prompts
		extra.ToolDefinitions = append(extra.ToolDefinitions, extension.ToolDefinitions...)
	}
	return extra
}

func (m *metaModule) Name() string { return "codexkit" }

func (m *metaModule) Init() error { return nil }

func (m *metaModule) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "tool_catalog",
			Description: "List codexkit MCP tools with descriptions, annotations, and deferred-schema status.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Handler: func(_ map[string]any) (any, error) {
				return map[string]any{"tools": m.catalogTools(m.allTools())}, nil
			},
		},
		{
			Name:        "tool_search",
			Description: "Search codexkit MCP tools by name or description and return lightweight matches.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Case-insensitive search text."},
					"limit": map[string]any{"type": "integer", "description": "Maximum matches to return. Defaults to 20."},
				},
			},
			Handler: codexkit.TypedHandler(func(params toolSearchParams) (any, error) {
				matches := m.searchTools(params.Query, params.Limit)
				return map[string]any{"query": params.Query, "tools": matches}, nil
			}),
		},
		{
			Name:        "tool_schema",
			Description: "Return the JSON schema and annotations for one codexkit MCP tool.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Tool name."},
				},
				"required": []string{"name"},
			},
			Handler: codexkit.TypedHandler(func(params toolSchemaParams) (any, error) {
				tool, ok := m.toolDefinition(params.Name)
				if !ok {
					return nil, fmt.Errorf("unknown tool: %s", params.Name)
				}
				return map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"annotations": tool.Annotations,
					"inputSchema": tool.Schema,
				}, nil
			}),
		},
		{
			Name:        "server_health",
			Description: "Report codexkit MCP server health, module coverage, and protocol surface counts.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Handler: func(_ map[string]any) (any, error) {
				return m.healthPayload(), nil
			},
		},
		{
			Name:        "codexkit_server_health",
			Description: "Compatibility alias for server_health.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Handler: func(_ map[string]any) (any, error) {
				return m.healthPayload(), nil
			},
		},
	}
}

func (m *metaModule) healthPayload() map[string]any {
	return map[string]any{
		"server":                  m.info,
		"module_count":            len(m.registry.ListModules()),
		"tool_count":              len(m.registry.ListTools()) + m.extra.Tools,
		"resource_count":          baseResourceCount + m.extra.Resources,
		"resource_template_count": m.extra.ResourceTemplates,
		"prompt_count":            codexkitPromptCount + m.extra.Prompts,
		"compatibility":           compatibilityPayload(),
	}
}

func (m *metaModule) catalogTools(tools []codexkit.ToolDef) []map[string]any {
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		items = append(items, map[string]any{
			"name":            tool.Name,
			"description":     tool.Description,
			"annotations":     tool.Annotations,
			"schema_deferred": true,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"])
	})
	return items
}

func (m *metaModule) allTools() []codexkit.ToolDef {
	tools := m.registry.ListTools()
	tools = append(tools, m.extra.ToolDefinitions...)
	return tools
}

func (m *metaModule) toolDefinition(name string) (codexkit.ToolDef, bool) {
	if tool, ok := m.registry.GetTool(name); ok {
		return tool, true
	}
	for _, tool := range m.extra.ToolDefinitions {
		if tool.Name == name {
			return tool, true
		}
	}
	return codexkit.ToolDef{}, false
}

func (m *metaModule) searchTools(query string, limit int) []map[string]any {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	items := make([]map[string]any, 0, limit)
	for _, tool := range m.catalogTools(m.allTools()) {
		if query != "" {
			haystack := strings.ToLower(fmt.Sprint(tool["name"]) + " " + fmt.Sprint(tool["description"]))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		items = append(items, tool)
		if len(items) >= limit {
			break
		}
	}
	return items
}

func readOnlyAnnotations() map[string]any {
	return codexkit.ToolAnnotations(true, false, true, false)
}
