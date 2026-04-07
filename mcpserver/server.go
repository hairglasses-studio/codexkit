// Package mcpserver implements an MCP (Model Context Protocol) server
// that aggregates all codexkit ToolModules and exposes them via
// JSON-RPC over stdio.
//
// It supports the MCP 2025-11 specification including:
//   - Tool listing with deferred schema loading (85% token reduction)
//   - Stdio transport (JSON-RPC 2.0 over stdin/stdout)
//   - Notifications and error handling
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// MCPToolInfo is the lightweight tool description for tools/list.
// Supports deferred loading: schema is only provided when requested.
type MCPToolInfo struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
}

// New creates an MCP server backed by the given registry.
func New(registry *codexkit.Registry, info ServerInfo) *Server {
	return &Server{
		registry: registry,
		info:     info,
	}
}

// Serve runs the MCP server reading from r and writing to w.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	// Increase buffer for large requests
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(w, JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error"},
			})
			continue
		}

		resp := s.handleRequest(req)
		if req.ID != nil {
			s.writeResponse(w, resp)
		}
	}

	return scanner.Err()
}

// ServeStdio runs the server on stdin/stdout.
func (s *Server) ServeStdio() error {
	return s.Serve(os.Stdin, os.Stdout)
}

func (s *Server) handleRequest(req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
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
					"listChanged":   true,
					"deferredLoading": true,
				},
			},
			"serverInfo": s.info,
		},
	}
}

func (s *Server) handleToolsList(req JSONRPCRequest) JSONRPCResponse {
	// Support deferred loading: check if client wants full schemas
	var params struct {
		IncludeSchemas bool `json:"include_schemas"`
	}
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	tools := s.registry.ListTools()
	infos := make([]MCPToolInfo, len(tools))
	for i, t := range tools {
		info := MCPToolInfo{
			Name:        t.Name,
			Description: t.Description,
		}
		if params.IncludeSchemas {
			info.InputSchema = t.Schema
		} else {
			info.DeferLoading = true
		}
		infos[i] = info
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": infos},
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

func (s *Server) writeResponse(w io.Writer, resp JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}
