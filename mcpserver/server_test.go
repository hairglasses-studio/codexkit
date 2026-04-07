package mcpserver

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hairglasses-studio/codexkit"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	reg := codexkit.NewRegistry()

	// Register a simple test module
	m := &testModule{}
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}

	return New(reg, ServerInfo{Name: "test", Version: "0.1.0"})
}

type testModule struct{}

func (m *testModule) Name() string { return "test" }
func (m *testModule) Init() error  { return nil }
func (m *testModule) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "test_echo",
			Description: "Echo back the input",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				return map[string]any{"echo": params["message"]}, nil
			},
		},
	}
}

func sendRequest(t *testing.T, s *Server, method string, id any, params any) JSONRPCResponse {
	t.Helper()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		p, _ := json.Marshal(params)
		req.Params = p
	}

	reqBytes, _ := json.Marshal(req)
	input := string(reqBytes) + "\n"

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v\nraw: %s", err, out.String())
	}
	return resp
}

func TestInitialize(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "initialize", 1, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if result["protocolVersion"] != "2025-11-25" {
		t.Errorf("expected protocol version 2025-11-25, got %v", result["protocolVersion"])
	}
}

func TestToolsList(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "tools/list", 2, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatal("expected tools array")
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
}

func TestToolsListWithSchemas(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "tools/list", 3, map[string]any{"include_schemas": true})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result := resp.Result.(map[string]any)
	tools := result["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["inputSchema"] == nil {
		t.Error("expected schema when include_schemas=true")
	}
}

func TestToolsCall(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "tools/call", 4, map[string]any{
		"name":      "test_echo",
		"arguments": map[string]any{"message": "hello"},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	block := content[0].(map[string]any)
	if block["type"] != "text" {
		t.Errorf("expected text content type, got %v", block["type"])
	}
	text := block["text"].(string)
	if !strings.Contains(text, "hello") {
		t.Errorf("expected echo of 'hello', got %s", text)
	}
}

func TestToolsCall_UnknownTool(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "tools/call", 5, map[string]any{
		"name":      "nonexistent",
		"arguments": map[string]any{},
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestUnknownMethod(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "unknown/method", 6, nil)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected -32601, got %d", resp.Error.Code)
	}
}
