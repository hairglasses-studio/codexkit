package codexkit

import (
	"fmt"
	"strings"
)

// Registry holds all registered ToolModules and dispatches tool calls.
type Registry struct {
	modules []ToolModule
	tools   map[string]registeredTool
	ordered []RegisteredTool
}

type registeredTool struct {
	module ToolModule
	def    ToolDef
}

// RegisteredTool pairs a tool definition with its owning module namespace.
type RegisteredTool struct {
	Namespace string
	Tool      ToolDef
}

// NewRegistry creates an empty module registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]registeredTool),
	}
}

// Register adds a module and indexes its tools. Init is called once.
func (r *Registry) Register(m ToolModule) error {
	if m == nil {
		return fmt.Errorf("register module: module is nil")
	}
	moduleName := strings.TrimSpace(m.Name())
	if moduleName == "" {
		return fmt.Errorf("register module: module name is empty")
	}
	for _, existing := range r.modules {
		if existing.Name() == moduleName {
			return fmt.Errorf("register module %s: duplicate module", moduleName)
		}
	}

	if err := m.Init(); err != nil {
		return fmt.Errorf("init %s: %w", moduleName, err)
	}
	tools := m.Tools()
	seen := make(map[string]bool, len(tools))
	for _, td := range tools {
		name := strings.TrimSpace(td.Name)
		switch {
		case name == "":
			return fmt.Errorf("register module %s: tool name is empty", moduleName)
		case strings.TrimSpace(td.Description) == "":
			return fmt.Errorf("register tool %s: description is empty", name)
		case td.Handler == nil:
			return fmt.Errorf("register tool %s: handler is nil", name)
		case td.Schema == nil:
			return fmt.Errorf("register tool %s: input schema is nil", name)
		case td.Schema["type"] != "object":
			return fmt.Errorf("register tool %s: input schema type must be object", name)
		case seen[name]:
			return fmt.Errorf("register tool %s: duplicate tool in module %s", name, moduleName)
		}
		if _, exists := r.tools[name]; exists {
			return fmt.Errorf("register tool %s: duplicate tool", name)
		}
		seen[name] = true
	}
	r.modules = append(r.modules, m)
	for _, td := range tools {
		r.tools[td.Name] = registeredTool{module: m, def: td}
		r.ordered = append(r.ordered, RegisteredTool{Namespace: moduleName, Tool: td})
	}
	return nil
}

// ListModules returns the names of all registered modules.
func (r *Registry) ListModules() []string {
	names := make([]string, len(r.modules))
	for i, m := range r.modules {
		names[i] = m.Name()
	}
	return names
}

// ListTools returns all registered tool definitions.
func (r *Registry) ListTools() []ToolDef {
	registered := r.ListRegisteredTools()
	defs := make([]ToolDef, 0, len(registered))
	for _, entry := range registered {
		defs = append(defs, entry.Tool)
	}
	return defs
}

// ListRegisteredTools returns tool definitions with their owning namespaces.
func (r *Registry) ListRegisteredTools() []RegisteredTool {
	return append([]RegisteredTool(nil), r.ordered...)
}

// GetTool returns a registered tool definition by name.
func (r *Registry) GetTool(name string) (ToolDef, bool) {
	rt, ok := r.tools[name]
	if !ok {
		return ToolDef{}, false
	}
	return rt.def, true
}

// Call dispatches a tool call by name.
func (r *Registry) Call(toolName string, params map[string]any) (any, error) {
	rt, ok := r.tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return rt.def.Handler(params)
}

// HasTool checks if a tool is registered.
func (r *Registry) HasTool(name string) bool {
	_, ok := r.tools[name]
	return ok
}
