package codexkit

import (
	"strings"
	"testing"
)

func TestTypedHandlerDecodesRequestStruct(t *testing.T) {
	type request struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}

	handler := TypedHandler(func(req request) (any, error) {
		return req, nil
	})

	result, err := handler(map[string]any{"name": "tool_search", "limit": float64(7)})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	got, ok := result.(request)
	if !ok {
		t.Fatalf("result = %T, want request", result)
	}
	if got.Name != "tool_search" || got.Limit != 7 {
		t.Fatalf("decoded request = %+v", got)
	}
}

type registryTestModule struct {
	name  string
	tools []ToolDef
}

func (m *registryTestModule) Name() string     { return m.name }
func (m *registryTestModule) Init() error      { return nil }
func (m *registryTestModule) Tools() []ToolDef { return m.tools }

func validRegistryTool(name string) ToolDef {
	return ToolDef{
		Name:        name,
		Description: "A compact test tool.",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:     func(map[string]any) (any, error) { return nil, nil },
	}
}

func TestRegistryRejectsInvalidAndDuplicateTools(t *testing.T) {
	tests := []struct {
		name  string
		tools []ToolDef
		want  string
	}{
		{name: "nil schema", tools: []ToolDef{{Name: "broken", Description: "Broken.", Handler: func(map[string]any) (any, error) { return nil, nil }}}, want: "input schema is nil"},
		{name: "non-object schema", tools: []ToolDef{{Name: "broken", Description: "Broken.", Schema: map[string]any{"type": "string"}, Handler: func(map[string]any) (any, error) { return nil, nil }}}, want: "must be object"},
		{name: "duplicate within module", tools: []ToolDef{validRegistryTool("same"), validRegistryTool("same")}, want: "duplicate tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.Register(&registryTestModule{name: "test", tools: tt.tools})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Register() error = %v, want substring %q", err, tt.want)
			}
			if len(reg.ListModules()) != 0 || len(reg.ListTools()) != 0 {
				t.Fatal("invalid module must not be partially registered")
			}
		})
	}
}

func TestRegistryExposesToolNamespaceAndRejectsCrossModuleDuplicate(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&registryTestModule{name: "alpha", tools: []ToolDef{validRegistryTool("inspect")}}); err != nil {
		t.Fatal(err)
	}
	entries := reg.ListRegisteredTools()
	if len(entries) != 1 || entries[0].Namespace != "alpha" || entries[0].Tool.Name != "inspect" {
		t.Fatalf("registered tools = %+v", entries)
	}
	if err := reg.Register(&registryTestModule{name: "beta", tools: []ToolDef{validRegistryTool("inspect")}}); err == nil || !strings.Contains(err.Error(), "duplicate tool") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}
