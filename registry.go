package codexkit

import "fmt"

// Registry holds all registered ToolModules and dispatches tool calls.
type Registry struct {
	modules []ToolModule
	tools   map[string]registeredTool
}

type registeredTool struct {
	module ToolModule
	def    ToolDef
}

// NewRegistry creates an empty module registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]registeredTool),
	}
}

// Register adds a module and indexes its tools. Init is called once.
func (r *Registry) Register(m ToolModule) error {
	if err := m.Init(); err != nil {
		return fmt.Errorf("init %s: %w", m.Name(), err)
	}
	r.modules = append(r.modules, m)
	for _, td := range m.Tools() {
		r.tools[td.Name] = registeredTool{module: m, def: td}
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
	var defs []ToolDef
	for _, m := range r.modules {
		defs = append(defs, m.Tools()...)
	}
	return defs
}

// Call dispatches a tool call by name.
func (r *Registry) Call(toolName string, params map[string]any) (any, error) {
	rt, ok := r.tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return rt.def.Handler(params)
}

// ToolCategory returns module name for a tool, or "unknown" when missing.
func (r *Registry) ToolCategory(toolName string) string {
	rt, ok := r.tools[toolName]
	if !ok || rt.module == nil {
		return "unknown"
	}
	return rt.module.Name()
}

// HasTool checks if a tool is registered.
func (r *Registry) HasTool(name string) bool {
	_, ok := r.tools[name]
	return ok
}
