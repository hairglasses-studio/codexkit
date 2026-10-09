package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/hairglasses-studio/codexkit"
)

type dummyBudgetModule struct {
	tools []codexkit.ToolDef
}

func (m dummyBudgetModule) Name() string              { return "dummy" }
func (m dummyBudgetModule) Init() error               { return nil }
func (m dummyBudgetModule) Tools() []codexkit.ToolDef { return m.tools }
func (m dummyBudgetModule) Execute(ctx context.Context, name string, args map[string]any) (any, error) {
	return nil, nil
}

func TestAuditToolDescriptionBudget(t *testing.T) {
	reg := codexkit.NewRegistry()
	dummyHandler := func(params map[string]any) (any, error) { return nil, nil }
	dummySchema := map[string]any{"type": "object"}
	mod := dummyBudgetModule{
		tools: []codexkit.ToolDef{
			{Name: "valid_tool", Description: "Short concise description.", Handler: dummyHandler, Schema: dummySchema},
			{Name: "long_tool", Description: strings.Repeat("A", 350), Handler: dummyHandler, Schema: dummySchema},
		},
	}
	if err := reg.Register(mod); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	rep := AuditToolDescriptionBudget(reg, 300)
	if rep.TotalTools != 2 {
		t.Fatalf("expected 2 tools, got %d", rep.TotalTools)
	}
	if rep.SatisfiesLimit {
		t.Fatalf("expected violations, got satisfiesLimit=true")
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(rep.Violations))
	}
	if rep.Violations[0].ToolName != "long_tool" {
		t.Fatalf("expected long_tool violation, got %s", rep.Violations[0].ToolName)
	}
}
