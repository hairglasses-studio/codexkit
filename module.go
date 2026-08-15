// Package codexkit provides the ToolModule interface and shared types
// used by all codexkit packages.
package codexkit

import (
	"encoding/json"
	"fmt"
)

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

	// Annotations exposes MCP tool behavior hints such as readOnlyHint,
	// destructiveHint, idempotentHint, and openWorldHint.
	Annotations map[string]any `json:"annotations,omitempty"`

	// Schema is the JSON Schema object for the tool's input parameters.
	// Tools without parameters still use an explicit empty object schema.
	Schema map[string]any `json:"inputSchema,omitempty"`

	// Handler executes the tool with the given parameters and returns
	// a result or error.
	Handler func(params map[string]any) (any, error) `json:"-"`
}

// DecodeParams decodes a loose MCP argument map into a typed request struct.
func DecodeParams[T any](params map[string]any) (T, error) {
	var out T
	if params == nil {
		params = map[string]any{}
	}
	data, err := json.Marshal(params)
	if err != nil {
		return out, fmt.Errorf("encode tool params: %w", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode tool params: %w", err)
	}
	return out, nil
}

// TypedHandler adapts a typed request handler to the ToolDef map boundary.
func TypedHandler[T any](fn func(T) (any, error)) func(map[string]any) (any, error) {
	return func(params map[string]any) (any, error) {
		typed, err := DecodeParams[T](params)
		if err != nil {
			return nil, err
		}
		return fn(typed)
	}
}

// ToolAnnotations builds MCP behavior hints for tool definitions.
func ToolAnnotations(readOnly, destructive, idempotent, openWorld bool) map[string]any {
	return map[string]any{
		"readOnlyHint":    readOnly,
		"destructiveHint": destructive,
		"idempotentHint":  idempotent,
		"openWorldHint":   openWorld,
	}
}

// PortableFrontmatterKeys are the only keys allowed in portable
// skill frontmatter per the Agent Skills open standard (Dec 2025).
var PortableFrontmatterKeys = map[string]bool{
	"name":                     true,
	"description":              true,
	"allowed-tools":            true,
	"compatibility":            true,
	"license":                  true,
	"user-invocable":           true,
	"argument-hint":            true,
	"disable-model-invocation": true,
	"reload":                   true, // hot-reloading support (Jan 2026)
	"triggers":                 true,
	"capabilities":             true,
	"see_also":                 true,
	"paths":                    true,
	"context":                  true,
	"metadata":                 true,
	"agent":                    true,
	"effort":                   true,
	"model":                    true,
}

// SkillSourceFrontmatterKeys are allowed in canonical repo-owned skill sources.
// Provider-specific export paths still filter down to PortableFrontmatterKeys.
var SkillSourceFrontmatterKeys = map[string]bool{
	"name":                     true,
	"description":              true,
	"allowed-tools":            true,
	"compatibility":            true,
	"license":                  true,
	"user-invocable":           true,
	"argument-hint":            true,
	"metadata":                 true,
	"reload":                   true,
	"disable-model-invocation": true,
	"triggers":                 true,
	"capabilities":             true,
	"see_also":                 true,
	"paths":                    true,
	"context":                  true,
	"agent":                    true,
	"effort":                   true,
	"model":                    true,
}
