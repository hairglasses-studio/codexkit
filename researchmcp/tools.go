package researchmcp

import (
	"context"
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxIdentifierBytes = 8192
	maxCursorBytes     = 4096
	maxFilterItems     = 100
)

const (
	catalogDescription = "Summarize the current offline research snapshot and its configured roots, registers, and freshness counts."
	listDescription    = "List a bounded page of offline research documents, typed records, or registered upstream metadata."
	searchDescription  = "Search current offline Markdown and typed research records with stable, snapshot-bound pagination."
	detailDescription  = "Read typed metadata for one known document, record, or upstream identifier."
	readDescription    = "Read one UTF-8-safe, byte-bounded chunk of a known offline Markdown document."
)

// ListKind selects one closed-world research collection.
type ListKind string

const (
	ListDocuments ListKind = "documents"
	ListRecords   ListKind = "records"
	ListUpstreams ListKind = "upstreams"
)

// CatalogInput is deliberately empty: catalog authority is server-owned.
type CatalogInput struct{}

// ListInput requests one bounded collection page. RootIDs and RegisterIDs are
// stable configured identifiers, never filesystem paths.
type ListInput struct {
	Kind        ListKind `json:"kind"`
	RootIDs     []string `json:"root_ids,omitempty"`
	RegisterIDs []string `json:"register_ids,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
}

// ListOutput contains exactly one collection response selected by Kind.
type ListOutput struct {
	Kind      ListKind                              `json:"kind"`
	Documents *researchgateway.ListResponse         `json:"documents,omitempty"`
	Records   *researchgateway.RecordListResponse   `json:"records,omitempty"`
	Upstreams *researchgateway.UpstreamListResponse `json:"upstreams,omitempty"`
}

// SearchInput requests a bounded search page over configured document roots
// and typed registers.
type SearchInput struct {
	Query       string   `json:"query"`
	RootIDs     []string `json:"root_ids,omitempty"`
	RegisterIDs []string `json:"register_ids,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
}

// DetailInput addresses one catalog item by its stable identifier.
type DetailInput struct {
	ID string `json:"id"`
}

// ReadInput requests one UTF-8-safe, byte-bounded document chunk.
type ReadInput struct {
	DocumentID string `json:"document_id"`
	Limit      int    `json:"limit,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
}

// ToolDefinitions returns compatibility discovery descriptors for the
// SDK-native research tools. Handlers remain registered only through Register.
func ToolDefinitions() []codexkit.ToolDef {
	annotations := codexkit.ToolAnnotations(true, false, true, false)
	return []codexkit.ToolDef{
		{Name: "research_catalog", Description: catalogDescription, Annotations: annotations, Schema: objectSchema(map[string]any{}, nil)},
		{Name: "research_list", Description: listDescription, Annotations: annotations, Schema: listInputSchema()},
		{Name: "research_search", Description: searchDescription, Annotations: annotations, Schema: searchInputSchema()},
		{Name: "research_detail", Description: detailDescription, Annotations: annotations, Schema: objectSchema(map[string]any{
			"id": stringSchema(1, maxIdentifierBytes, "^(document|record|upstream):"),
		}, []string{"id"})},
		{Name: "research_read", Description: readDescription, Annotations: annotations, Schema: objectSchema(map[string]any{
			"document_id": stringSchema(1, maxIdentifierBytes, "^document:"),
			"limit":       integerSchema(4, researchgateway.HardMaxReadBytes),
			"cursor":      stringSchema(1, maxCursorBytes, ""),
		}, []string{"document_id"})},
	}
}

func (adapter *adapter) registerTools(server *mcp.Server) {
	mcp.AddTool[CatalogInput, researchgateway.Overview](server, &mcp.Tool{
		Name:        "research_catalog",
		Description: catalogDescription,
		Annotations: readOnlyAnnotations("Research catalog"),
		InputSchema: objectSchema(map[string]any{}, nil),
	}, adapter.catalog)

	mcp.AddTool[ListInput, ListOutput](server, &mcp.Tool{
		Name:        "research_list",
		Description: listDescription,
		Annotations: readOnlyAnnotations("List research"),
		InputSchema: listInputSchema(),
	}, adapter.list)

	mcp.AddTool[SearchInput, researchgateway.SearchResponse](server, &mcp.Tool{
		Name:        "research_search",
		Description: searchDescription,
		Annotations: readOnlyAnnotations("Search research"),
		InputSchema: searchInputSchema(),
	}, adapter.search)

	mcp.AddTool[DetailInput, researchgateway.Detail](server, &mcp.Tool{
		Name:        "research_detail",
		Description: detailDescription,
		Annotations: readOnlyAnnotations("Research item detail"),
		InputSchema: objectSchema(map[string]any{
			"id": stringSchema(1, maxIdentifierBytes, "^(document|record|upstream):"),
		}, []string{"id"}),
	}, adapter.detail)

	mcp.AddTool[ReadInput, researchgateway.ReadResponse](server, &mcp.Tool{
		Name:        "research_read",
		Description: readDescription,
		Annotations: readOnlyAnnotations("Read research document"),
		InputSchema: objectSchema(map[string]any{
			"document_id": stringSchema(1, maxIdentifierBytes, "^document:"),
			"limit":       integerSchema(4, researchgateway.HardMaxReadBytes),
			"cursor":      stringSchema(1, maxCursorBytes, ""),
		}, []string{"document_id"}),
	}, adapter.read)
}

func (adapter *adapter) catalog(ctx context.Context, _ *mcp.CallToolRequest, _ CatalogInput) (*mcp.CallToolResult, researchgateway.Overview, error) {
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, researchgateway.Overview{}, err
	}
	return nil, gateway.Overview(), nil
}

func (adapter *adapter) list(ctx context.Context, _ *mcp.CallToolRequest, input ListInput) (*mcp.CallToolResult, ListOutput, error) {
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, ListOutput{}, err
	}
	output := ListOutput{Kind: input.Kind}
	switch input.Kind {
	case ListDocuments:
		if len(input.RegisterIDs) != 0 {
			return nil, ListOutput{}, fmt.Errorf("register_ids are only valid when kind is %q", ListRecords)
		}
		page, err := gateway.ListDocuments(ctx, researchgateway.ListRequest{RootIDs: input.RootIDs, Limit: input.Limit, Cursor: input.Cursor})
		if err != nil {
			return nil, ListOutput{}, err
		}
		output.Documents = &page
	case ListRecords:
		if len(input.RootIDs) != 0 {
			return nil, ListOutput{}, fmt.Errorf("root_ids are only valid when kind is %q", ListDocuments)
		}
		page, err := gateway.ListRecords(ctx, researchgateway.RecordListRequest{RegisterIDs: input.RegisterIDs, Limit: input.Limit, Cursor: input.Cursor})
		if err != nil {
			return nil, ListOutput{}, err
		}
		output.Records = &page
	case ListUpstreams:
		if len(input.RootIDs) != 0 || len(input.RegisterIDs) != 0 {
			return nil, ListOutput{}, fmt.Errorf("filters are not valid when kind is %q", ListUpstreams)
		}
		page, err := gateway.ListUpstreams(ctx, researchgateway.PageRequest{Limit: input.Limit, Cursor: input.Cursor})
		if err != nil {
			return nil, ListOutput{}, err
		}
		output.Upstreams = &page
	default:
		return nil, ListOutput{}, fmt.Errorf("unsupported research list kind %q", input.Kind)
	}
	return nil, output, nil
}

func (adapter *adapter) search(ctx context.Context, _ *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, researchgateway.SearchResponse, error) {
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, researchgateway.SearchResponse{}, err
	}
	output, err := gateway.Search(ctx, researchgateway.SearchRequest{
		Query: input.Query, RootIDs: input.RootIDs, RegisterIDs: input.RegisterIDs,
		Limit: input.Limit, Cursor: input.Cursor,
	})
	return nil, output, err
}

func (adapter *adapter) detail(ctx context.Context, _ *mcp.CallToolRequest, input DetailInput) (*mcp.CallToolResult, researchgateway.Detail, error) {
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, researchgateway.Detail{}, err
	}
	output, err := gateway.Describe(ctx, input.ID)
	return nil, output, err
}

func (adapter *adapter) read(ctx context.Context, _ *mcp.CallToolRequest, input ReadInput) (*mcp.CallToolResult, researchgateway.ReadResponse, error) {
	gateway, err := adapter.open(ctx)
	if err != nil {
		return nil, researchgateway.ReadResponse{}, err
	}
	output, err := gateway.ReadDocument(ctx, researchgateway.ReadRequest{
		DocumentID: input.DocumentID, Limit: input.Limit, Cursor: input.Cursor,
	})
	return nil, output, err
}

func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	falseValue := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: &falseValue,
		IdempotentHint:  true,
		OpenWorldHint:   &falseValue,
	}
}

func listInputSchema() map[string]any {
	kind := stringSchema(1, 16, "")
	kind["enum"] = []string{string(ListDocuments), string(ListRecords), string(ListUpstreams)}
	return objectSchema(map[string]any{
		"kind":         kind,
		"root_ids":     identifierArraySchema(),
		"register_ids": identifierArraySchema(),
		"limit":        integerSchema(1, researchgateway.HardMaxPageSize),
		"cursor":       stringSchema(1, maxCursorBytes, ""),
	}, []string{"kind"})
}

func searchInputSchema() map[string]any {
	return objectSchema(map[string]any{
		"query":        stringSchema(1, researchgateway.HardMaxQueryBytes, ""),
		"root_ids":     identifierArraySchema(),
		"register_ids": identifierArraySchema(),
		"limit":        integerSchema(1, researchgateway.HardMaxPageSize),
		"cursor":       stringSchema(1, maxCursorBytes, ""),
	}, []string{"query"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) != 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(minLength, maxLength int, pattern string) map[string]any {
	schema := map[string]any{"type": "string", "minLength": minLength, "maxLength": maxLength}
	if pattern != "" {
		schema["pattern"] = pattern
	}
	return schema
}

func integerSchema(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}

func identifierArraySchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"maxItems":    maxFilterItems,
		"uniqueItems": true,
		"items":       stringSchema(1, 128, "^[a-z0-9][a-z0-9._-]*$"),
	}
}
