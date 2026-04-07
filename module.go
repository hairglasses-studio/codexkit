// Package codexkit provides the ToolModule interface and shared types
// used by all codexkit packages.
package codexkit

// ToolModule is the interface that all codexkit packages implement.
// It is modeled after claudekit's ToolModule pattern: each package
// registers itself as a named module exposing a set of typed tool
// definitions that can be aggregated by the MCP server or CLI.
type ToolModule interface {
	// Name returns the module identifier (e.g. "baselineguard").
	Name() string

	// Tools returns the tool definitions provided by this module.
	Tools() []ToolDef

	// Init performs any one-time setup. Called before first tool use.
	Init() error
}

// ToolDef describes a single tool exposed by a module.
type ToolDef struct {
	// Name is the tool identifier (e.g. "baseline_check").
	Name string `json:"name"`

	// Description is a short human-readable summary.
	Description string `json:"description"`

	// Schema is the JSON Schema for the tool's input parameters.
	// nil means the tool takes no parameters.
	Schema map[string]any `json:"inputSchema,omitempty"`

	// Handler executes the tool with the given parameters and returns
	// a result or error.
	Handler func(params map[string]any) (any, error) `json:"-"`
}

// PortableFrontmatterKeys are the only keys allowed in portable
// skill frontmatter per the Agent Skills open standard (Dec 2025).
var PortableFrontmatterKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"allowed-tools": true,
	"reload":        true, // hot-reloading support (Jan 2026)
}
