package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hairglasses-studio/codexkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKServerRoundTripToolsResourcesAndPrompts(t *testing.T) {
	ctx := context.Background()
	registry := codexkit.NewRegistry()
	if err := registry.Register(&testModule{}); err != nil {
		t.Fatal(err)
	}
	info := ServerInfo{Name: "codexkit-test", Version: "0.1.0"}
	if err := registry.Register(Module(registry, info)); err != nil {
		t.Fatal(err)
	}

	server := NewSDKServer(registry, info)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	if initialized := clientSession.InitializeResult(); initialized == nil || initialized.ProtocolVersion != "2026-07-28" {
		t.Fatalf("official SDK negotiated unexpected protocol: %#v", initialized)
	}

	tools := map[string]*mcp.Tool{}
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	if tools["test_echo"] == nil || tools["tool_search"] == nil {
		t.Fatalf("official SDK catalog missing compatibility tools: %v", tools)
	}

	call, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "test_echo",
		Arguments: map[string]any{"message": "sdk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if call.IsError || len(call.Content) != 1 {
		t.Fatalf("unexpected tool result: %#v", call)
	}
	text, ok := call.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, `"echo":"sdk"`) {
		t.Fatalf("unexpected tool content: %#v", call.Content)
	}
	structured, ok := call.StructuredContent.(map[string]any)
	if !ok || structured["echo"] != "sdk" {
		t.Fatalf("unexpected structured tool result: %#v", call.StructuredContent)
	}
	invalid, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tool_schema",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !invalid.IsError {
		t.Fatalf("official SDK accepted input missing a required property: %#v", invalid)
	}

	resource, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "codexkit://catalog/overview"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, `"module_count"`) {
		t.Fatalf("unexpected resource: %#v", resource)
	}

	prompt, err := clientSession.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "codexkit-rollout",
		Arguments: map[string]string{"repo": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 {
		t.Fatalf("unexpected prompt: %#v", prompt)
	}
	promptText, ok := prompt.Messages[0].Content.(*mcp.TextContent)
	if !ok || !strings.Contains(promptText.Text, "demo") {
		t.Fatalf("unexpected prompt content: %#v", prompt.Messages[0].Content)
	}
}

type cancellableModule struct{ started chan struct{} }

func (cancellableModule) Name() string { return "cancellable" }
func (cancellableModule) Init() error  { return nil }
func (module cancellableModule) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{{
		Name: "cancellable_wait", Description: "Wait until cancelled",
		Schema: map[string]any{"type": "object", "properties": map[string]any{}},
		ContextHandler: func(ctx context.Context, _ map[string]any) (any, error) {
			close(module.started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}
}

func TestSDKServerPropagatesInFlightCancellationToContextHandlers(t *testing.T) {
	registry := codexkit.NewRegistry()
	started := make(chan struct{})
	if err := registry.Register(cancellableModule{started: started}); err != nil {
		t.Fatal(err)
	}
	server := NewSDKServer(registry, ServerInfo{Name: "cancel-test", Version: "0"})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "cancel-client", Version: "0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "cancellable_wait", Arguments: map[string]any{}})
		done <- callErr
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled in-flight call error = %v", err)
	}
}

func TestSDKToolAnnotationsPreserveSafetyHints(t *testing.T) {
	annotations := sdkToolAnnotations(codexkit.ToolAnnotations(true, false, true, false))
	if annotations == nil || !annotations.ReadOnlyHint || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
		t.Fatalf("unexpected safety annotations: %#v", annotations)
	}
	if !annotations.IdempotentHint || annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
		t.Fatalf("unexpected behavior annotations: %#v", annotations)
	}
}
