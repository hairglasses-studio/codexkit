package builtinmodules

import (
	"testing"

	"github.com/hairglasses-studio/codexkit"
)

func TestAllRegistersOneCanonicalModuleSet(t *testing.T) {
	registry := codexkit.NewRegistry()
	moduleNames := map[string]bool{}
	for _, module := range All() {
		if moduleNames[module.Name()] {
			t.Fatalf("duplicate built-in module %q", module.Name())
		}
		moduleNames[module.Name()] = true
		if err := registry.Register(module); err != nil {
			t.Fatalf("register %s: %v", module.Name(), err)
		}
	}
	for _, name := range []string{"configindex", "researchrefresh", "workspace"} {
		if !moduleNames[name] {
			t.Fatalf("canonical module set is missing %q", name)
		}
	}
	for _, tool := range []string{"workspace_config_index", "research_refresh", "research_freshness"} {
		if !registry.HasTool(tool) {
			t.Fatalf("canonical registry is missing %q", tool)
		}
	}
}

func TestForMCPExcludesCallerControlledResearchNetworking(t *testing.T) {
	registry := codexkit.NewRegistry()
	for _, module := range ForMCP() {
		if module.Name() == "researchrefresh" {
			t.Fatal("production MCP module set includes researchrefresh")
		}
		if err := registry.Register(module); err != nil {
			t.Fatalf("register %s: %v", module.Name(), err)
		}
	}
	for _, name := range []string{"research_refresh", "research_freshness", "workspace_config_index", "workspace_config_check"} {
		if registry.HasTool(name) {
			t.Fatalf("production MCP registry exposes %q", name)
		}
	}
	if !registry.HasTool("baseline_check") {
		t.Fatal("production MCP registry lost ordinary built-in tools")
	}
}
