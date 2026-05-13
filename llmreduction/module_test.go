package llmreduction

import (
	"testing"
)

func TestModule_NameAndInit(t *testing.T) {
	m := Module()
	if m.Name() != "llmreduction" {
		t.Errorf("Name() = %q, want %q", m.Name(), "llmreduction")
	}
	if err := m.Init(); err != nil {
		t.Errorf("Init() = %v, want nil", err)
	}
}

func TestModule_ToolCount(t *testing.T) {
	m := Module()
	tools := m.Tools()
	if len(tools) != 5 {
		t.Errorf("len(Tools()) = %d, want 5", len(tools))
	}

	wantNames := []string{
		"llm_surface_debt_audit",
		"llm_surface_dedup_candidates",
		"llm_surface_reduction_plan",
		"llm_surface_reduction_apply",
		"llm_surface_reduction_verify",
	}
	nameSet := map[string]bool{}
	for _, tool := range tools {
		nameSet[tool.Name] = true
	}
	for _, want := range wantNames {
		if !nameSet[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestModule_ToolsHaveDescriptions(t *testing.T) {
	m := Module()
	for _, tool := range m.Tools() {
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
}

func TestModule_ToolsHaveSchemas(t *testing.T) {
	m := Module()
	for _, tool := range m.Tools() {
		if tool.Schema == nil {
			t.Errorf("tool %q has nil schema", tool.Name)
		}
		schemaType, ok := tool.Schema["type"].(string)
		if !ok || schemaType != "object" {
			t.Errorf("tool %q schema type = %v, want 'object'", tool.Name, tool.Schema["type"])
		}
	}
}

func TestModule_ToolsHaveHandlers(t *testing.T) {
	m := Module()
	for _, tool := range m.Tools() {
		if tool.Handler == nil {
			t.Errorf("tool %q has nil handler", tool.Name)
		}
	}
}
