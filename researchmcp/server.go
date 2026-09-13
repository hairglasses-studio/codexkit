package researchmcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Stable native MCP surface counts. These supplement module-registry counts
// because research surfaces are mounted directly on the official SDK server.
const (
	ToolCount             = 5
	ResourceCount         = 1
	ResourceTemplateCount = 1
	PromptCount           = 1
)

// Register validates the fixed research authority and mounts the research
// tools, resources, resource template, and workflow prompt on server. Each
// mounted handler reopens the offline snapshot rather than retaining the
// validation snapshot.
func Register(server *mcp.Server, config Config) error {
	if server == nil {
		return errors.New("register research MCP: nil server")
	}
	gatewayConfig := config.gatewayConfig()
	if _, err := openGateway(context.Background(), gatewayConfig, config.Loaders); err != nil {
		return fmt.Errorf("register research MCP: validate authority: %w", err)
	}

	adapter := &adapter{config: gatewayConfig, loaders: config.Loaders}
	adapter.registerTools(server)
	adapter.registerResources(server)
	adapter.registerPrompt(server)
	return nil
}

type adapter struct {
	config  researchgateway.Config
	loaders *researchgateway.LoaderRegistry
}

func (adapter *adapter) open(ctx context.Context) (*researchgateway.Gateway, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gateway, err := openGateway(ctx, adapter.config, adapter.loaders)
	if err != nil {
		return nil, fmt.Errorf("open offline research snapshot: %w", err)
	}
	return gateway, nil
}

func openGateway(ctx context.Context, config researchgateway.Config, loaders *researchgateway.LoaderRegistry) (*researchgateway.Gateway, error) {
	if loaders != nil {
		return researchgateway.OpenWithLoaderRegistry(ctx, config, loaders)
	}
	return researchgateway.Open(ctx, config)
}
