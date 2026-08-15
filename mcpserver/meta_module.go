package mcpserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hairglasses-studio/codexkit"
)

const (
	defaultDiscoveryLimit = 20
	maxDiscoveryLimit     = 50
	maxAllowedTools       = 64
	recommendedGroupSize  = 10
)

type metaModule struct {
	registry *codexkit.Registry
	info     ServerInfo
}

type discoveryParams struct {
	Query        string   `json:"query"`
	Limit        int      `json:"limit"`
	Namespaces   []string `json:"namespaces"`
	AllowedTools []string `json:"allowed_tools"`
}

type toolSchemaParams struct {
	Name string `json:"name"`
}

func Module(registry *codexkit.Registry, info ServerInfo) codexkit.ToolModule {
	return &metaModule{registry: registry, info: info}
}

func (m *metaModule) Name() string { return "codexkit" }

func (m *metaModule) Init() error { return nil }

func (m *metaModule) Tools() []codexkit.ToolDef {
	discoverySchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Case-insensitive text matched against namespace, name, and description."},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxDiscoveryLimit, "description": "Maximum tools to return; defaults to 20."},
			"namespaces": map[string]any{
				"type": "array", "maxItems": 16, "uniqueItems": true,
				"items":       map[string]any{"type": "string"},
				"description": "Optional module namespaces to search.",
			},
			"allowed_tools": map[string]any{
				"type": "array", "maxItems": maxAllowedTools, "uniqueItems": true,
				"items":       map[string]any{"type": "string"},
				"description": "Optional bounded discovery filter. This does not grant authorization.",
			},
		},
	}
	return []codexkit.ToolDef{
		{
			Name:        "tool_catalog",
			Description: "Browse compact codexkit tool groups before loading individual schemas.",
			Annotations: readOnlyAnnotations(),
			Schema:      discoverySchema,
			Handler: codexkit.TypedHandler(func(params discoveryParams) (any, error) {
				return m.discoveryPayload(params)
			}),
		},
		{
			Name:        "tool_search",
			Description: "Search codexkit tools by namespace, name, or compact description.",
			Annotations: readOnlyAnnotations(),
			Schema:      discoverySchema,
			Handler: codexkit.TypedHandler(func(params discoveryParams) (any, error) {
				return m.discoveryPayload(params)
			}),
		},
		{
			Name:        "tool_schema",
			Description: "Load the complete input schema and annotations for one codexkit tool.",
			Annotations: readOnlyAnnotations(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Exact tool name."},
				},
				"required": []string{"name"},
			},
			Handler: codexkit.TypedHandler(func(params toolSchemaParams) (any, error) {
				for _, entry := range m.registry.ListRegisteredTools() {
					if entry.Tool.Name == params.Name {
						return map[string]any{
							"namespace":   entry.Namespace,
							"name":        entry.Tool.Name,
							"description": entry.Tool.Description,
							"annotations": entry.Tool.Annotations,
							"inputSchema": entry.Tool.Schema,
						}, nil
					}
				}
				return nil, fmt.Errorf("unknown tool: %s", params.Name)
			}),
		},
		{
			Name:        "server_health",
			Description: "Report server counts, namespace sizes, and the Codex compatibility contract.",
			Annotations: readOnlyAnnotations(),
			Schema:      emptyObjectSchema(),
			Handler: func(_ map[string]any) (any, error) {
				return m.healthPayload(), nil
			},
		},
		{
			Name:        "codexkit_server_health",
			Description: "Compatibility alias for server_health.",
			Annotations: readOnlyAnnotations(),
			Schema:      emptyObjectSchema(),
			Handler: func(_ map[string]any) (any, error) {
				return m.healthPayload(), nil
			},
		},
	}
}

func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (m *metaModule) healthPayload() map[string]any {
	sizes := map[string]int{}
	for _, entry := range m.registry.ListRegisteredTools() {
		sizes[entry.Namespace]++
	}
	oversized := []string{}
	for namespace, size := range sizes {
		if size > recommendedGroupSize {
			oversized = append(oversized, namespace)
		}
	}
	sort.Strings(oversized)
	return map[string]any{
		"server":               m.info,
		"module_count":         len(m.registry.ListModules()),
		"tool_count":           len(m.registry.ListTools()),
		"resource_count":       3,
		"prompt_count":         codexkitPromptCount,
		"namespace_sizes":      sizes,
		"oversized_namespaces": oversized,
		"compatibility":        compatibilityPayload(),
	}
}

func (m *metaModule) discoveryPayload(params discoveryParams) (any, error) {
	allowed, err := boundedSet("allowed_tools", params.AllowedTools, maxAllowedTools)
	if err != nil {
		return nil, err
	}
	namespaces, err := boundedSet("namespaces", params.Namespaces, 16)
	if err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultDiscoveryLimit
	}
	if limit > maxDiscoveryLimit {
		return nil, fmt.Errorf("limit must be at most %d", maxDiscoveryLimit)
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))

	entries := append([]codexkit.RegisteredTool(nil), m.registry.ListRegisteredTools()...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Namespace == entries[j].Namespace {
			return entries[i].Tool.Name < entries[j].Tool.Name
		}
		return entries[i].Namespace < entries[j].Namespace
	})

	items := make([]map[string]any, 0, limit)
	groups := map[string][]map[string]any{}
	for _, entry := range entries {
		if len(allowed) > 0 && !allowed[entry.Tool.Name] {
			continue
		}
		if len(namespaces) > 0 && !namespaces[entry.Namespace] {
			continue
		}
		description := compactDescription(entry.Tool.Description)
		if query != "" {
			haystack := strings.ToLower(entry.Namespace + " " + entry.Tool.Name + " " + description)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		item := map[string]any{
			"namespace":        entry.Namespace,
			"name":             entry.Tool.Name,
			"description":      description,
			"annotations":      entry.Tool.Annotations,
			"approval_hint":    approvalHint(entry.Tool.Annotations),
			"schema_available": true,
		}
		items = append(items, item)
		groups[entry.Namespace] = append(groups[entry.Namespace], item)
		if len(items) >= limit {
			break
		}
	}

	groupItems := make([]map[string]any, 0, len(groups))
	for namespace, tools := range groups {
		groupItems = append(groupItems, map[string]any{
			"namespace":   namespace,
			"description": namespaceDescription(namespace),
			"tool_count":  len(tools),
			"tools":       tools,
		})
	}
	sort.Slice(groupItems, func(i, j int) bool {
		return groupItems[i]["namespace"].(string) < groupItems[j]["namespace"].(string)
	})

	return map[string]any{
		"query":              params.Query,
		"matched_tool_count": len(items),
		"namespaces":         groupItems,
		"tools":              items,
		"filter_scope":       "discovery_only_not_authorization",
		"approval_policy":    "Callers enforce approvals; approval_hint is advisory MCP metadata.",
	}, nil
}

func boundedSet(field string, values []string, max int) (map[string]bool, error) {
	if len(values) > max {
		return nil, fmt.Errorf("%s must contain at most %d entries", field, max)
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s must not contain empty entries", field)
		}
		if set[value] {
			return nil, fmt.Errorf("%s must not contain duplicate %q", field, value)
		}
		set[value] = true
	}
	return set, nil
}

func compactDescription(description string) string {
	description = strings.Join(strings.Fields(description), " ")
	const maxRunes = 180
	runes := []rune(description)
	if len(runes) <= maxRunes {
		return description
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func approvalHint(annotations map[string]any) string {
	if destructive, _ := annotations["destructiveHint"].(bool); destructive {
		return "always"
	}
	if readOnly, _ := annotations["readOnlyHint"].(bool); readOnly {
		return "never"
	}
	return "on-request"
}

func namespaceDescription(namespace string) string {
	descriptions := map[string]string{
		"baselineguard":    "Repository baseline and compatibility validation.",
		"codexkit":         "Discovery, schema loading, health, and compatibility metadata.",
		"fleetaudit":       "Fleet-wide baseline, skill, and MCP audit summaries.",
		"llmreduction":     "LLM-facing surface reduction analysis.",
		"mcpsync":          "MCP source, policy, and Codex configuration synchronization.",
		"perfaudit":        "Static Codex performance and regression-budget checks.",
		"primitiveindex":   "Workspace agent primitive inventory generation.",
		"reporeadiness":    "Autonomous repository mutation readiness scoring.",
		"skillsync":        "Canonical skill validation and provider mirror synchronization.",
		"sourcecontract":   "Workspace source-contract verification.",
		"surfaceindex":     "Repository agent-surface and runtime-projection indexes.",
		"unificationaudit": "Cross-repository implementation unification analysis.",
		"workspace":        "Workspace manifest, provider projection, and configuration checks.",
	}
	if description := descriptions[namespace]; description != "" {
		return description
	}
	return "Tools provided by the " + namespace + " module."
}

func readOnlyAnnotations() map[string]any {
	return codexkit.ToolAnnotations(true, false, true, false)
}
