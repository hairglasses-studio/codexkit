// Package builtinmodules defines the canonical set of codexkit ToolModules
// shared by the CLI and MCP entrypoints.
package builtinmodules

import (
	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/configindex"
	"github.com/hairglasses-studio/codexkit/fleetaudit"
	"github.com/hairglasses-studio/codexkit/llmreduction"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/perfaudit"
	"github.com/hairglasses-studio/codexkit/primitiveindex"
	"github.com/hairglasses-studio/codexkit/reporeadiness"
	"github.com/hairglasses-studio/codexkit/researchrefresh"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/sourcecontract"
	"github.com/hairglasses-studio/codexkit/surfaceindex"
	"github.com/hairglasses-studio/codexkit/surfaceparity"
	"github.com/hairglasses-studio/codexkit/telemetry"
	"github.com/hairglasses-studio/codexkit/unificationaudit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

// All returns a new slice in deterministic registration order.
func All() []codexkit.ToolModule {
	return []codexkit.ToolModule{
		baselineguard.Module(),
		configindex.Module(),
		skillsync.Module(),
		mcpsync.Module(),
		fleetaudit.Module(),
		llmreduction.Module(),
		perfaudit.Module(),
		primitiveindex.Module(),
		reporeadiness.Module(),
		researchrefresh.Module(),
		sourcecontract.Module(),
		surfaceindex.Module(),
		surfaceparity.Module(),
		telemetry.Module(),
		unificationaudit.Module(),
		workspace.Module(),
	}
}

// ForMCP returns the production MCP module set. Network research refresh is
// intentionally operator-CLI-only; the MCP process mounts the fixed-authority
// offline research gateway separately.
func ForMCP() []codexkit.ToolModule {
	return []codexkit.ToolModule{
		baselineguard.Module(),
		skillsync.Module(),
		mcpsync.Module(),
		fleetaudit.Module(),
		llmreduction.Module(),
		perfaudit.Module(),
		primitiveindex.Module(),
		reporeadiness.Module(),
		sourcecontract.Module(),
		surfaceindex.Module(),
		surfaceparity.Module(),
		telemetry.Module(),
		unificationaudit.Module(),
		workspace.Module(),
	}
}
