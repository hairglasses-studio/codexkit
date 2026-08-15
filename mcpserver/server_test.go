package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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

type listTestModule struct {
	tools []codexkit.ToolDef
}

func (m *listTestModule) Name() string              { return "list_test" }
func (m *listTestModule) Init() error               { return nil }
func (m *listTestModule) Tools() []codexkit.ToolDef { return m.tools }

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

	return decodeLegacyResponse(t, out.Bytes())
}

func sendFramedRequest(t *testing.T, s *Server, method string, id any, params any) JSONRPCResponse {
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
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(reqBytes), reqBytes)

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}

	return decodeFramedResponse(t, &out)
}

func decodeLegacyResponse(t *testing.T, data []byte) JSONRPCResponse {
	t.Helper()

	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parsing response: %v\nraw: %s", err, string(data))
	}
	return resp
}

func decodeFramedResponse(t *testing.T, out *bytes.Buffer) JSONRPCResponse {
	t.Helper()

	payload, mode, err := readMessage(bufio.NewReader(bytes.NewReader(out.Bytes())))
	if err != nil {
		t.Fatalf("reading framed response: %v\nraw: %s", err, out.String())
	}
	if mode != transportModeFramed {
		t.Fatalf("expected framed response mode, got %v\nraw: %s", mode, out.String())
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("parsing framed response: %v\nraw: %s", err, out.String())
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
	caps := result["capabilities"].(map[string]any)
	if caps["resources"] == nil || caps["prompts"] == nil {
		t.Fatalf("expected resources and prompts capabilities, got %+v", caps)
	}
	tools := caps["tools"].(map[string]any)
	if _, exists := tools["deferredLoading"]; exists {
		t.Fatalf("deferredLoading is not an MCP tools capability: %+v", tools)
	}
}

func TestInitialize_FramedTransport(t *testing.T) {
	s := setupTestServer(t)
	resp := sendFramedRequest(t, s, "initialize", 1, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result := resp.Result.(map[string]any)
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
	tool := tools[0].(map[string]any)
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		t.Fatal("expected inputSchema object")
	}
	if schema["type"] != "object" {
		t.Fatalf("expected object schema, got %+v", schema)
	}
	properties := schema["properties"].(map[string]any)
	if properties["message"] == nil {
		t.Fatalf("tools/list must return the complete registered schema, got %+v", schema)
	}
	if _, exists := tool["defer_loading"]; exists {
		t.Fatalf("defer_loading is a client definition property, not MCP tool metadata: %+v", tool)
	}
}

func TestToolsListPaginatesWithCursor(t *testing.T) {
	reg := codexkit.NewRegistry()
	module := &listTestModule{}
	for i := 0; i < toolsListPageSize+1; i++ {
		module.tools = append(module.tools, codexkit.ToolDef{
			Name:        fmt.Sprintf("tool_%02d", i),
			Description: "A paginated test tool.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			Handler:     func(map[string]any) (any, error) { return nil, nil },
		})
	}
	if err := reg.Register(module); err != nil {
		t.Fatal(err)
	}
	s := New(reg, ServerInfo{Name: "test", Version: "0.1.0"})
	first := sendRequest(t, s, "tools/list", 1, nil)
	firstResult := first.Result.(map[string]any)
	if len(firstResult["tools"].([]any)) != toolsListPageSize || firstResult["nextCursor"] != "50" {
		t.Fatalf("unexpected first page %+v", firstResult)
	}
	second := sendRequest(t, s, "tools/list", 2, map[string]any{"cursor": "50"})
	secondResult := second.Result.(map[string]any)
	if len(secondResult["tools"].([]any)) != 1 || secondResult["nextCursor"] != nil {
		t.Fatalf("unexpected second page %+v", secondResult)
	}
	invalid := sendRequest(t, s, "tools/list", 3, map[string]any{"cursor": "bad"})
	if invalid.Error == nil || invalid.Error.Code != -32602 {
		t.Fatalf("expected invalid cursor error, got %+v", invalid)
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

func TestResourcesListAndRead(t *testing.T) {
	s := setupTestServer(t)

	listResp := sendRequest(t, s, "resources/list", 7, nil)
	if listResp.Error != nil {
		t.Fatalf("unexpected error: %v", listResp.Error.Message)
	}
	listResult := listResp.Result.(map[string]any)
	resources := listResult["resources"].([]any)
	if len(resources) != 3 {
		t.Fatalf("expected resource catalog, got %+v", listResult)
	}

	readResp := sendRequest(t, s, "resources/read", 8, map[string]any{"uri": "codexkit://catalog/overview"})
	if readResp.Error != nil {
		t.Fatalf("unexpected error: %v", readResp.Error.Message)
	}
	readResult := readResp.Result.(map[string]any)
	contents := readResult["contents"].([]any)
	block := contents[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "\"tool_count\"") {
		t.Fatalf("expected overview JSON, got %+v", block)
	}

	compatResp := sendRequest(t, s, "resources/read", 9, map[string]any{"uri": "codexkit://catalog/compatibility"})
	if compatResp.Error != nil {
		t.Fatalf("unexpected compatibility resource error: %v", compatResp.Error.Message)
	}
	compatResult := compatResp.Result.(map[string]any)
	compatBlock := compatResult["contents"].([]any)[0].(map[string]any)
	compatText := compatBlock["text"].(string)
	for _, want := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "under_development", "\"enabled\":false"} {
		if !strings.Contains(compatText, want) {
			t.Fatalf("compatibility resource missing %q: %s", want, compatText)
		}
	}
}

func TestPromptsListAndGet(t *testing.T) {
	s := setupTestServer(t)

	listResp := sendRequest(t, s, "prompts/list", 9, nil)
	if listResp.Error != nil {
		t.Fatalf("unexpected error: %v", listResp.Error.Message)
	}
	listResult := listResp.Result.(map[string]any)
	prompts := listResult["prompts"].([]any)
	if len(prompts) != codexkitPromptCount {
		t.Fatalf("expected %d prompts, got %+v", codexkitPromptCount, listResult)
	}
	promptNames := map[string]bool{}
	for _, raw := range prompts {
		prompt := raw.(map[string]any)
		promptNames[prompt["name"].(string)] = true
	}
	for _, name := range []string{
		"codexkit-rollout",
		"codexkit-baseline-runbook",
		"codexkit-config-runbook",
		"codexkit-recovery-runbook",
	} {
		if !promptNames[name] {
			t.Fatalf("expected prompt %q in %+v", name, listResult)
		}
	}

	getResp := sendRequest(t, s, "prompts/get", 10, map[string]any{
		"name":      "codexkit-rollout",
		"arguments": map[string]any{"repo": "demo"},
	})
	if getResp.Error != nil {
		t.Fatalf("unexpected error: %v", getResp.Error.Message)
	}
	getResult := getResp.Result.(map[string]any)
	messages := getResult["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].(map[string]any)
	if !strings.Contains(content["text"].(string), "demo") {
		t.Fatalf("expected repo-specific prompt text, got %+v", getResult)
	}

	baselineResp := sendRequest(t, s, "prompts/get", 11, map[string]any{
		"name":      "codexkit-baseline-runbook",
		"arguments": map[string]any{"repo": "/tmp/demo"},
	})
	if baselineResp.Error != nil {
		t.Fatalf("unexpected error: %v", baselineResp.Error.Message)
	}
	baselineText := promptResponseText(t, baselineResp)
	for _, want := range []string{"baseline check /tmp/demo --json", "skills diff /tmp/demo", "mcp diff /tmp/demo"} {
		if !strings.Contains(baselineText, want) {
			t.Fatalf("expected baseline prompt to contain %q, got %q", want, baselineText)
		}
	}

	configResp := sendRequest(t, s, "prompts/get", 12, map[string]any{
		"name": "codexkit-config-runbook",
		"arguments": map[string]any{
			"repo":      "/tmp/demo",
			"workspace": "/tmp/studio",
		},
	})
	if configResp.Error != nil {
		t.Fatalf("unexpected error: %v", configResp.Error.Message)
	}
	configText := promptResponseText(t, configResp)
	for _, want := range []string{"mcp diff /tmp/demo", "provider diff /tmp/demo", "global-mcp-sync /tmp/studio --dry-run --json"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("expected config prompt to contain %q, got %q", want, configText)
		}
	}

	recoveryResp := sendRequest(t, s, "prompts/get", 13, map[string]any{
		"name":      "codexkit-recovery-runbook",
		"arguments": map[string]any{"repo": "demo"},
	})
	if recoveryResp.Error != nil {
		t.Fatalf("unexpected error: %v", recoveryResp.Error.Message)
	}
	recoveryText := promptResponseText(t, recoveryResp)
	for _, want := range []string{"git --no-pager status --short --branch", "fresh feature branch", "stage only intentional files"} {
		if !strings.Contains(recoveryText, want) {
			t.Fatalf("expected recovery prompt to contain %q, got %q", want, recoveryText)
		}
	}
}

func promptResponseText(t *testing.T, resp JSONRPCResponse) string {
	t.Helper()
	result := resp.Result.(map[string]any)
	messages := result["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].(map[string]any)
	return content["text"].(string)
}

func TestMetaModuleAddsHealthTool(t *testing.T) {
	reg := codexkit.NewRegistry()
	if err := reg.Register(&testModule{}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(Module(reg, ServerInfo{Name: "test", Version: "0.1.0"})); err != nil {
		t.Fatal(err)
	}
	if !reg.HasTool("codexkit_server_health") {
		t.Fatal("expected codexkit_server_health to be registered")
	}
	result, err := reg.Call("codexkit_server_health", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	payload := result.(map[string]any)
	if payload["resource_count"] != 3 || payload["prompt_count"] != codexkitPromptCount {
		t.Fatalf("unexpected health payload %+v", payload)
	}
	compat := payload["compatibility"].(map[string]any)
	if compat["codex_version"] != "0.147.0" {
		t.Fatalf("unexpected compatibility payload %+v", compat)
	}
}

func TestCompatibilityPayloadPinsCodex0147Contract(t *testing.T) {
	payload := compatibilityPayload()
	features := payload["stable_features"].([]string)
	wantFeatures := []string{"apps", "goals", "hooks", "multi_agent", "plugins", "skill_search", "tool_suggest"}
	if fmt.Sprint(features) != fmt.Sprint(wantFeatures) {
		t.Fatalf("stable features = %v, want %v", features, wantFeatures)
	}
	toolSearch := payload["tool_search"].(map[string]any)
	if toolSearch["defer_loading_owner"] != "openai_client_tool_or_mcp_definition" {
		t.Fatalf("unexpected tool-search contract %+v", toolSearch)
	}
	mcp := payload["mcp"].(map[string]any)
	preview := mcp["mcp_2026_07_28"].(map[string]any)
	if preview["enabled"] != false || preview["stage"] != "under_development" {
		t.Fatalf("preview protocol must remain disabled: %+v", preview)
	}
}

func TestMetaDiscoveryGroupsFiltersAndBoundsTools(t *testing.T) {
	reg := codexkit.NewRegistry()
	if err := reg.Register(&testModule{}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(Module(reg, ServerInfo{Name: "test", Version: "0.1.0"})); err != nil {
		t.Fatal(err)
	}

	result, err := reg.Call("tool_search", map[string]any{
		"query":         "echo",
		"namespaces":    []string{"test"},
		"allowed_tools": []string{"test_echo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := result.(map[string]any)
	tools := payload["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["namespace"] != "test" || tools[0]["name"] != "test_echo" {
		t.Fatalf("unexpected discovery payload %+v", payload)
	}
	if tools[0]["schema_available"] != true || tools[0]["approval_hint"] != "on-request" {
		t.Fatalf("unexpected discovery metadata %+v", tools[0])
	}

	tooMany := make([]string, maxAllowedTools+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("tool_%d", i)
	}
	if _, err := reg.Call("tool_catalog", map[string]any{"allowed_tools": tooMany}); err == nil || !strings.Contains(err.Error(), "at most 64") {
		t.Fatalf("expected bounded allowed_tools error, got %v", err)
	}
}

func TestPing(t *testing.T) {
	s := setupTestServer(t)
	resp := sendRequest(t, s, "ping", 8, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if len(result) != 0 {
		t.Fatalf("expected empty ping result, got %+v", result)
	}
}

func TestPing_FramedTransport(t *testing.T) {
	s := setupTestServer(t)
	resp := sendFramedRequest(t, s, "ping", 9, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if len(result) != 0 {
		t.Fatalf("expected empty ping result, got %+v", result)
	}
}
