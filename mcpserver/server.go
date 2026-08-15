// Package mcpserver implements an MCP (Model Context Protocol) server
// that aggregates all codexkit ToolModules and exposes them via
// JSON-RPC over stdio.
//
// It supports the MCP 2025-11 specification including:
//   - Complete tool schemas with cursor-based pagination
//   - Stdio transport (JSON-RPC 2.0 over stdin/stdout)
//   - Notifications and error handling
//
// OpenAI Tool Search deferral is selected by the client on its Responses API
// tool or MCP server definition. It is not an MCP tools/list extension.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hairglasses-studio/codexkit"
)

// Server is an MCP server that dispatches tool calls via a Registry.
type Server struct {
	registry *codexkit.Registry
	info     ServerInfo
	mu       sync.Mutex
}

// ServerInfo describes the server identity.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type transportMode int

const (
	transportModeLegacy transportMode = iota
	transportModeFramed
)

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError is a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPToolInfo is the standards-compliant tool description for tools/list.
type MCPToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Annotations map[string]any `json:"annotations,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

type MCPResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type MCPPromptInfo struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Arguments   []MCPPromptArgInfo `json:"arguments,omitempty"`
}

type MCPPromptArgInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

const codexkitPromptCount = 4
const toolsListPageSize = 50

// New creates an MCP server backed by the given registry.
func New(registry *codexkit.Registry, info ServerInfo) *Server {
	return &Server{
		registry: registry,
		info:     info,
	}
}

// Serve runs the MCP server reading from r and writing to w.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	for {
		payload, mode, err := readMessage(reader)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			s.writeResponse(w, mode, JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error"},
			})
			continue
		}

		resp := s.handleRequest(req)
		if req.ID != nil {
			s.writeResponse(w, mode, resp)
		}
	}
}

// ServeStdio runs the server on stdin/stdout.
func (s *Server) ServeStdio() error {
	return s.Serve(os.Stdin, os.Stdout)
}

func (s *Server) handleRequest(req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return s.handlePing(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	case "notifications/initialized":
		// Client acknowledgment — no response needed for notifications
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func (s *Server) handleInitialize(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
				"resources": map[string]any{
					"listChanged": false,
				},
				"prompts": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": s.info,
		},
	}
}

func (s *Server) handlePing(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{},
	}
}

func (s *Server) handleToolsList(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return invalidParamsResponse(req.ID)
		}
	}

	tools := s.registry.ListTools()
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	start := 0
	if params.Cursor != "" {
		parsed, err := strconv.Atoi(params.Cursor)
		if err != nil || parsed < 0 || parsed > len(tools) {
			return invalidParamsResponse(req.ID)
		}
		start = parsed
	}
	end := start + toolsListPageSize
	if end > len(tools) {
		end = len(tools)
	}
	infos := make([]MCPToolInfo, 0, end-start)
	for _, t := range tools[start:end] {
		infos = append(infos, MCPToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Annotations: t.Annotations,
			InputSchema: t.Schema,
		})
	}
	result := map[string]any{"tools": infos}
	if end < len(tools) {
		result["nextCursor"] = strconv.Itoa(end)
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

func invalidParamsResponse(id any) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: -32602, Message: "invalid params"},
	}
}

func (s *Server) handleToolsCall(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	result, err := s.registry.Call(params.Name, params.Arguments)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
		}
	}

	// Marshal result to JSON content block per MCP spec
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32603, Message: fmt.Sprintf("internal error marshaling result: %v", err)},
		}
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(resultJSON)},
			},
		},
	}
}

func (s *Server) handleResourcesList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"resources": s.resourceCatalog(),
		},
	}
}

func (s *Server) handleResourcesRead(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	payload, err := s.resourcePayload(params.URI)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32603, Message: fmt.Sprintf("marshal resource payload: %v", err)},
		}
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"contents": []map[string]any{
				{
					"uri":      params.URI,
					"mimeType": "application/json",
					"text":     string(body),
				},
			},
		},
	}
}

func (s *Server) handlePromptsList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"prompts": s.promptCatalog(),
		},
	}
}

func (s *Server) handlePromptsGet(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	prompt, err := s.promptPayload(params.Name, params.Arguments)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
		}
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  prompt,
	}
}

func (s *Server) resourceCatalog() []MCPResourceInfo {
	return []MCPResourceInfo{
		{
			URI:         "codexkit://catalog/overview",
			Name:        "overview",
			Description: "Compact server overview with module and tool counts.",
			MimeType:    "application/json",
		},
		{
			URI:         "codexkit://catalog/modules",
			Name:        "modules",
			Description: "Detailed list of registered modules and their tools.",
			MimeType:    "application/json",
		},
		{
			URI:         "codexkit://catalog/compatibility",
			Name:        "compatibility",
			Description: "Codex 0.147.0 models, stable features, and MCP protocol status.",
			MimeType:    "application/json",
		},
	}
}

func (s *Server) resourcePayload(uri string) (map[string]any, error) {
	switch uri {
	case "codexkit://catalog/overview":
		return map[string]any{
			"server":       s.info,
			"module_count": len(s.registry.ListModules()),
			"tool_count":   len(s.registry.ListTools()),
			"modules":      s.registry.ListModules(),
		}, nil
	case "codexkit://catalog/modules":
		moduleNames := s.registry.ListModules()
		moduleTools := map[string][]string{}
		for _, name := range moduleNames {
			moduleTools[name] = []string{}
		}
		for _, entry := range s.registry.ListRegisteredTools() {
			moduleTools[entry.Namespace] = append(moduleTools[entry.Namespace], entry.Tool.Name)
		}
		return map[string]any{
			"server":       s.info,
			"module_count": len(moduleNames),
			"modules":      moduleTools,
		}, nil
	case "codexkit://catalog/compatibility":
		return compatibilityPayload(), nil
	default:
		return nil, fmt.Errorf("unknown resource: %s", uri)
	}
}

func (s *Server) promptCatalog() []MCPPromptInfo {
	return []MCPPromptInfo{
		{
			Name:        "codexkit-rollout",
			Description: "Guide a repo parity repair or Codex migration pass with codexkit.",
			Arguments: []MCPPromptArgInfo{
				{Name: "repo", Description: "Optional repo name to target.", Required: false},
			},
		},
		{
			Name:        "codexkit-baseline-runbook",
			Description: "Run a dry-run-first baseline validation and remediation pass.",
			Arguments: []MCPPromptArgInfo{
				{Name: "repo", Description: "Optional repo path or name to target.", Required: false},
			},
		},
		{
			Name:        "codexkit-config-runbook",
			Description: "Generate or refresh Codex, Claude, and Gemini MCP config from repo-local sources.",
			Arguments: []MCPPromptArgInfo{
				{Name: "repo", Description: "Optional repo path or name to target.", Required: false},
				{Name: "workspace", Description: "Optional workspace root for global MCP overlay work.", Required: false},
			},
		},
		{
			Name:        "codexkit-recovery-runbook",
			Description: "Recover an interrupted codexkit maintenance pass from git and dry-run evidence.",
			Arguments: []MCPPromptArgInfo{
				{Name: "repo", Description: "Optional repo path or name to target.", Required: false},
			},
		},
	}
}

func (s *Server) promptPayload(name string, arguments map[string]any) (map[string]any, error) {
	switch name {
	case "codexkit-rollout":
		repo := ""
		if raw, ok := arguments["repo"].(string); ok {
			repo = raw
		}
		text := "Use codexkit tools to inspect baseline drift, skill sync state, MCP sync state, and fleet parity before making changes."
		if repo != "" {
			text = fmt.Sprintf("Use codexkit tools to inspect and repair parity drift for %s. Start with baseline and MCP sync checks, then summarize the remaining rollout gaps.", repo)
		}
		return map[string]any{
			"description": "Guide a parity repair pass with codexkit.",
			"messages": []map[string]any{
				{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": text,
					},
				},
			},
		}, nil
	case "codexkit-baseline-runbook":
		target := promptStringArg(arguments, "repo", "the target repo")
		return promptText(
			"Baseline validation runbook.",
			fmt.Sprintf(`Run a dry-run-first codexkit baseline validation pass for %s.

1. Inspect local risk first: git branch --show-current; git --no-pager status --short --branch.
2. Run baseline evidence: GOWORK=off go run ./cmd/codexkit baseline check %s --json.
3. If baseline reports skill or MCP drift, inspect before applying: GOWORK=off go run ./cmd/codexkit skills diff %s; GOWORK=off go run ./cmd/codexkit mcp diff %s.
4. Apply only the smallest generator-backed repair, then rerun the same baseline command and summarize remaining findings with remediation commands.`,
				target, target, target, target,
			),
		), nil
	case "codexkit-config-runbook":
		target := promptStringArg(arguments, "repo", "the target repo")
		workspace := promptStringArg(arguments, "workspace", "the workspace root")
		return promptText(
			"Config generation runbook.",
			fmt.Sprintf(`Refresh codexkit-managed provider config for %s.

1. Start with read-only previews: GOWORK=off go run ./cmd/codexkit mcp diff %s; GOWORK=off go run ./cmd/codexkit provider diff %s.
2. For workspace overlays, preview first: GOWORK=off go run ./cmd/codexkit workspace global-mcp-sync %s --dry-run --json.
3. Apply only when the preview matches the intended repo-local sources: GOWORK=off go run ./cmd/codexkit mcp sync %s, then the matching provider or workspace sync.
4. Verify with mcp check, provider check, and baseline check; report any unmanaged blocks or skipped provider entries separately.`,
				target, target, target, workspace, target,
			),
		), nil
	case "codexkit-recovery-runbook":
		target := promptStringArg(arguments, "repo", "the target repo")
		return promptText(
			"Recovery runbook.",
			fmt.Sprintf(`Recover an interrupted codexkit maintenance pass for %s without overwriting local work.

1. Capture current state: git branch --show-current; git --no-pager status --short --branch; git log --oneline -5.
2. Fetch origin, compare HEAD, upstream, and origin/main, then choose a fresh feature branch from the safest clean base.
3. Rebuild evidence with dry-run commands before editing: baseline check, skills diff, mcp diff, and provider diff for the target repo.
4. Continue with one small durable repair, stage only intentional files, run focused Go tests, and leave push or merge state explicit.`,
				target,
			),
		), nil
	default:
		return nil, fmt.Errorf("unknown prompt: %s", name)
	}
}

func promptStringArg(arguments map[string]any, name, fallback string) string {
	if raw, ok := arguments[name].(string); ok {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func promptText(description, text string) map[string]any {
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func readMessage(r *bufio.Reader) ([]byte, transportMode, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" {
					return nil, transportModeLegacy, io.EOF
				}
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
					return []byte(trimmed), transportModeLegacy, nil
				}
			}
			return nil, transportModeLegacy, err
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return []byte(trimmed), transportModeLegacy, nil
		}

		contentLength, err := readContentLength(r, trimmed)
		if err != nil {
			return nil, transportModeFramed, err
		}

		payload := make([]byte, contentLength)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, transportModeFramed, err
		}
		return payload, transportModeFramed, nil
	}
}

func readContentLength(r *bufio.Reader, firstHeader string) (int, error) {
	headers := []string{firstHeader}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return 0, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		headers = append(headers, trimmed)
	}

	contentLength := -1
	for _, header := range headers {
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			return 0, fmt.Errorf("invalid MCP header: %q", header)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("invalid Content-Length %q: %w", value, err)
		}
		contentLength = n
	}

	if contentLength < 0 {
		return 0, fmt.Errorf("missing Content-Length header")
	}
	return contentLength, nil
}

func (s *Server) writeResponse(w io.Writer, mode transportMode, resp JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(resp)
	if mode == transportModeFramed {
		fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data))
		_, _ = w.Write(data)
		return
	}
	fmt.Fprintf(w, "%s\n", data)
}
