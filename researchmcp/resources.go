package researchmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	catalogResourceURI = "research://catalog"
	itemResourcePrefix = "research://item/"
	itemResourceURI    = itemResourcePrefix + "{id}"
)

func (adapter *adapter) registerResources(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		URI:         catalogResourceURI,
		Name:        "research-catalog",
		Title:       "Offline research catalog",
		Description: "Current bounded offline research snapshot overview.",
		MIMEType:    "application/json",
	}, adapter.catalogResource)
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: itemResourceURI,
		Name:        "research-item",
		Title:       "Offline research item",
		Description: "Typed metadata for a known document, record, or upstream identifier.",
		MIMEType:    "application/json",
	}, adapter.itemResource)
}

func (adapter *adapter) catalogResource(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	if request.Params.URI != catalogResourceURI {
		return nil, mcp.ResourceNotFoundError(request.Params.URI)
	}
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, err
	}
	return jsonResource(request.Params.URI, gateway.Overview())
}

func (adapter *adapter) itemResource(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	id, err := resourceItemID(request.Params.URI)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(request.Params.URI)
	}
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, err
	}
	detail, err := gateway.Describe(ctx, id)
	if err != nil {
		if errors.Is(err, researchgateway.ErrSourceNotFound) {
			return nil, mcp.ResourceNotFoundError(request.Params.URI)
		}
		return nil, err
	}
	return jsonResource(request.Params.URI, detail)
}

func jsonResource(uri string, value any) (*mcp.ReadResourceResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode research resource: %w", err)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: "application/json", Text: string(data),
	}}}, nil
}

func resourceItemID(rawURI string) (string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "research" || parsed.Host != "item" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid research item URI")
	}
	escaped := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if escaped == "" || strings.Contains(escaped, "/") {
		return "", errors.New("invalid research item identifier")
	}
	id, err := url.PathUnescape(escaped)
	if err != nil || len(id) > maxIdentifierBytes || strings.ContainsAny(id, "/\\\x00") || strings.TrimSpace(id) != id {
		return "", errors.New("invalid research item identifier")
	}
	for _, prefix := range []string{"document:", "record:", "upstream:"} {
		if strings.HasPrefix(id, prefix) && len(id) > len(prefix) {
			return id, nil
		}
	}
	return "", errors.New("invalid research item identifier")
}
