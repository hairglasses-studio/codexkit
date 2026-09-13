package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewSDKServer adapts the codexkit module registry to the official MCP Go SDK.
// The legacy Server remains available for protocol-compatibility tests while
// production stdio serving uses this adapter.
func NewSDKServer(registry *codexkit.Registry, info ServerInfo, extensions ...SurfaceCounts) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    info.Name,
		Version: info.Version,
	}, &mcp.ServerOptions{PageSize: 100})

	for _, definition := range registry.ListTools() {
		addSDKTool(server, registry, definition)
	}
	addSDKResources(server, registry, info, extensions...)
	addSDKPrompts(server, registry, info)
	return server
}

// ServeSDKStdio runs a codexkit registry over the official SDK's stdio
// transport, including its protocol negotiation, pagination, and cancellation
// behavior.
func ServeSDKStdio(ctx context.Context, registry *codexkit.Registry, info ServerInfo, extensions ...SurfaceCounts) error {
	return NewSDKServer(registry, info, extensions...).Run(ctx, &mcp.StdioTransport{})
}

func addSDKTool(server *mcp.Server, registry *codexkit.Registry, definition codexkit.ToolDef) {
	schema := definition.Schema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	mcp.AddTool[map[string]any, any](server, &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
		Annotations: sdkToolAnnotations(definition.Annotations),
		InputSchema: schema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, arguments map[string]any) (*mcp.CallToolResult, any, error) {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		output, err := registry.CallContext(ctx, definition.Name, arguments)
		return nil, output, err
	})
}

func sdkToolAnnotations(values map[string]any) *mcp.ToolAnnotations {
	if len(values) == 0 {
		return nil
	}
	annotations := &mcp.ToolAnnotations{}
	if value, ok := values["readOnlyHint"].(bool); ok {
		annotations.ReadOnlyHint = value
	}
	if value, ok := values["destructiveHint"].(bool); ok {
		annotations.DestructiveHint = boolPointer(value)
	}
	if value, ok := values["idempotentHint"].(bool); ok {
		annotations.IdempotentHint = value
	}
	if value, ok := values["openWorldHint"].(bool); ok {
		annotations.OpenWorldHint = boolPointer(value)
	}
	return annotations
}

func boolPointer(value bool) *bool { return &value }

func addSDKResources(server *mcp.Server, registry *codexkit.Registry, info ServerInfo, extensions ...SurfaceCounts) {
	legacy := New(registry, info, extensions...)
	for _, resource := range legacy.resourceCatalog() {
		resource := resource
		server.AddResource(&mcp.Resource{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: resource.Description,
			MIMEType:    resource.MimeType,
		}, func(_ context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			payload, err := legacy.resourcePayload(request.Params.URI)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(request.Params.URI)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("encode resource %s: %w", request.Params.URI, err)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     string(encoded),
			}}}, nil
		})
	}
}

func addSDKPrompts(server *mcp.Server, registry *codexkit.Registry, info ServerInfo) {
	legacy := New(registry, info)
	for _, prompt := range legacy.promptCatalog() {
		prompt := prompt
		arguments := make([]*mcp.PromptArgument, 0, len(prompt.Arguments))
		for _, argument := range prompt.Arguments {
			arguments = append(arguments, &mcp.PromptArgument{
				Name:        argument.Name,
				Description: argument.Description,
				Required:    argument.Required,
			})
		}
		server.AddPrompt(&mcp.Prompt{
			Name:        prompt.Name,
			Description: prompt.Description,
			Arguments:   arguments,
		}, func(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			values := make(map[string]any, len(request.Params.Arguments))
			for name, value := range request.Params.Arguments {
				values[name] = value
			}
			payload, err := legacy.promptPayload(request.Params.Name, values)
			if err != nil {
				return nil, err
			}
			return sdkPromptResult(payload)
		})
	}
}

func sdkPromptResult(payload map[string]any) (*mcp.GetPromptResult, error) {
	result := &mcp.GetPromptResult{}
	result.Description, _ = payload["description"].(string)
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("prompt payload has invalid messages")
	}
	for _, message := range messages {
		role, _ := message["role"].(string)
		content, _ := message["content"].(map[string]any)
		text, _ := content["text"].(string)
		if role == "" || text == "" {
			return nil, fmt.Errorf("prompt payload has invalid message")
		}
		result.Messages = append(result.Messages, &mcp.PromptMessage{
			Role:    mcp.Role(role),
			Content: &mcp.TextContent{Text: text},
		})
	}
	return result, nil
}
