package mcpsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// End-to-end companion to TestCompactProviderServerEmitsTypeForURLServers:
// proves the "type" key survives the whole Claude overlay writer, not just the
// server-object builder, and that merging it in does not disturb unmanaged
// entries already present in ~/.claude.json.
func TestRenderClaudeOverlayTypesRemoteServers(t *testing.T) {
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudePath, []byte(`{"projects":{"/proj":{"mcpServers":{"manual":{"command":"manual"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	projection := GlobalProjection{
		Providers: []ProviderProjection{
			{
				Provider: "claude",
				Entries: []ProviderProjectionEntry{
					{
						Name:   "studio_context7_docs",
						Server: ProviderServer{URL: "https://example.test/mcp", Transport: "http"},
					},
					{
						Name:   "studio_local_stdio",
						Server: ProviderServer{Command: "./run-mcp.sh", CWD: "/tmp/repo"},
					},
				},
			},
		},
	}

	content, err := renderClaudeOverlay(claudePath, "/proj", projection)
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		Projects map[string]struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("overlay is not valid JSON: %v\n%s", err, content)
	}
	servers := decoded.Projects["/proj"].MCPServers

	remote, ok := servers["studio_context7_docs"]
	if !ok {
		t.Fatalf("managed remote server missing from overlay: %#v", servers)
	}
	if remote["type"] != "http" {
		t.Fatalf(`studio_context7_docs type = %v, want "http"`, remote["type"])
	}
	if remote["url"] != "https://example.test/mcp" {
		t.Fatalf("studio_context7_docs url = %v, want the projected url", remote["url"])
	}

	local, ok := servers["studio_local_stdio"]
	if !ok {
		t.Fatalf("managed stdio server missing from overlay: %#v", servers)
	}
	if _, has := local["type"]; has {
		t.Fatalf("stdio server must not gain a remote type: %#v", local)
	}

	if _, ok := servers["manual"]; !ok {
		t.Fatalf("unmanaged entries must survive the merge: %#v", servers)
	}
}
