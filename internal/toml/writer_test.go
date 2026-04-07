package toml

import (
	"strings"
	"testing"
)

func TestWriteSections(t *testing.T) {
	sections := []Section{
		{
			Name: "server.github",
			Values: map[string]any{
				"command":   "npx",
				"transport": "stdio",
			},
		},
	}

	result := WriteSections(sections)
	if !strings.Contains(result, "[server.github]") {
		t.Error("expected section header")
	}
	if !strings.Contains(result, `command = "npx"`) {
		t.Error("expected command value")
	}
	if !strings.Contains(result, `transport = "stdio"`) {
		t.Error("expected transport value")
	}
}

func TestAppendSection(t *testing.T) {
	result := AppendSection("mcp_servers.test", map[string]any{
		"command": "node",
		"args":    []string{"server.js", "--port=3000"},
	})

	if !strings.HasPrefix(result, "\n[mcp_servers.test]\n") {
		t.Error("expected section with leading newline")
	}
	if !strings.Contains(result, `args = ["server.js", "--port=3000"]`) {
		t.Error("expected args array")
	}
}

func TestWriteSections_BoolAndInt(t *testing.T) {
	sections := []Section{
		{
			Name: "config",
			Values: map[string]any{
				"enabled": true,
				"port":    8080,
			},
		},
	}

	result := WriteSections(sections)
	if !strings.Contains(result, "enabled = true") {
		t.Error("expected bool value")
	}
	if !strings.Contains(result, "port = 8080") {
		t.Error("expected int value")
	}
}

func TestWriteSections_MultipleSections(t *testing.T) {
	sections := []Section{
		{Name: "a", Values: map[string]any{"x": "1"}},
		{Name: "b", Values: map[string]any{"y": "2"}},
	}

	result := WriteSections(sections)
	if !strings.Contains(result, "[a]") || !strings.Contains(result, "[b]") {
		t.Error("expected both sections")
	}
}
