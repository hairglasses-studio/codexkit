package mcpsync

import "testing"

// A url-backed MCP server must carry Claude Code's "type" key. Without it the
// entry is silently skipped by `claude doctor` / `claude mcp list` — the
// failure that left studio_context7_docs and studio_openai-developer-docs
// unusable while the projection itself looked correct.
func TestCompactProviderServerEmitsTypeForURLServers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport string
		want      string
	}{
		{"empty transport defaults to http", "", "http"},
		{"http", "http", "http"},
		{"streamable-http", "streamable-http", "http"},
		{"sse", "sse", "sse"},
		{"mixed case sse", "SSE", "sse"},
		{"unknown falls back to http", "grpc", "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := compactProviderServer(ProviderServer{URL: "https://example.test/mcp", Transport: tc.transport})
			got, ok := out["type"]
			if !ok {
				t.Fatalf("url-backed server emitted no %q key: %#v", "type", out)
			}
			if got != tc.want {
				t.Fatalf("type = %v, want %v", got, tc.want)
			}
			if out["url"] != "https://example.test/mcp" {
				t.Fatalf("url dropped: %#v", out)
			}
		})
	}
}

// A stdio server has no URL and must not gain a bogus remote type.
func TestCompactProviderServerOmitsTypeForStdioServers(t *testing.T) {
	out := compactProviderServer(ProviderServer{Command: "/usr/bin/thing", Args: []string{"serve"}})
	if _, ok := out["type"]; ok {
		t.Fatalf("stdio server should not carry a remote type: %#v", out)
	}
}
