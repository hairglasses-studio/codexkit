package researchmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/hairglasses-studio/codexkit/researchrefresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKIntegrationExercisesEverySurfaceWithoutWrites(t *testing.T) {
	root := newResearchHarness(t)
	before := snapshotFiles(t, root)
	client := newTestClient(t, Config{HarnessRoot: root})
	ctx := context.Background()

	tools := collectTools(t, client)
	wantTools := []string{"research_catalog", "research_detail", "research_list", "research_read", "research_search"}
	if len(tools) != ToolCount || len(wantTools) != ToolCount {
		t.Fatalf("tool count = %d, want stable count %d", len(tools), ToolCount)
	}
	for _, name := range wantTools {
		tool := tools[name]
		if tool == nil {
			t.Fatalf("tool catalog missing %q: %v", name, mapKeys(tools))
		}
		if tool.OutputSchema == nil {
			t.Fatalf("tool %q has no typed output schema", name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %q lacks read-only closed-world annotations: %#v", name, tool.Annotations)
		}
		assertAuthorityFreeInputSchema(t, name, tool.InputSchema)
	}

	catalogCall := callTool(t, client, "research_catalog", map[string]any{})
	var overview researchgateway.Overview
	decodeStructured(t, catalogCall, &overview)
	if overview.Summary.Documents != 3 || overview.Summary.Records != 2 || overview.Summary.Upstreams != 2 {
		t.Fatalf("unexpected catalog summary: %+v", overview.Summary)
	}
	if len(overview.Roots) != 1 || overview.Roots[0].ID != DefaultLocalRootID || overview.Roots[0].Path != DefaultLocalRootPath {
		t.Fatalf("default research root not advertised: %+v", overview.Roots)
	}

	documentPage1 := listPage(t, client, map[string]any{"kind": "documents", "limit": 1})
	if documentPage1.Documents == nil || len(documentPage1.Documents.Documents) != 1 || documentPage1.Documents.NextCursor == "" {
		t.Fatalf("unexpected first document page: %+v", documentPage1)
	}
	documentPage2 := listPage(t, client, map[string]any{
		"kind": "documents", "limit": 1, "cursor": documentPage1.Documents.NextCursor,
	})
	if documentPage2.Documents == nil || len(documentPage2.Documents.Documents) != 1 ||
		documentPage1.Documents.Documents[0].ID == documentPage2.Documents.Documents[0].ID {
		t.Fatalf("unexpected second document page: %+v", documentPage2)
	}
	documentID := documentPage1.Documents.Documents[0].ID

	recordPage1 := listPage(t, client, map[string]any{"kind": "records", "limit": 1})
	if recordPage1.Records == nil || len(recordPage1.Records.Records) != 1 || recordPage1.Records.NextCursor == "" {
		t.Fatalf("unexpected first record page: %+v", recordPage1)
	}
	recordPage2 := listPage(t, client, map[string]any{
		"kind": "records", "limit": 1, "cursor": recordPage1.Records.NextCursor,
	})
	if recordPage2.Records == nil || len(recordPage2.Records.Records) != 1 {
		t.Fatalf("unexpected second record page: %+v", recordPage2)
	}
	recordID := recordPage1.Records.Records[0].ID

	upstreamPage1 := listPage(t, client, map[string]any{"kind": "upstreams", "limit": 1})
	if upstreamPage1.Upstreams == nil || len(upstreamPage1.Upstreams.Upstreams) != 1 || upstreamPage1.Upstreams.NextCursor == "" {
		t.Fatalf("unexpected first upstream page: %+v", upstreamPage1)
	}
	upstreamPage2 := listPage(t, client, map[string]any{
		"kind": "upstreams", "limit": 1, "cursor": upstreamPage1.Upstreams.NextCursor,
	})
	if upstreamPage2.Upstreams == nil || len(upstreamPage2.Upstreams.Upstreams) != 1 {
		t.Fatalf("unexpected second upstream page: %+v", upstreamPage2)
	}

	search1 := callTool(t, client, "research_search", map[string]any{"query": "needle", "limit": 1})
	var firstSearch researchgateway.SearchResponse
	decodeStructured(t, search1, &firstSearch)
	if len(firstSearch.Results) != 1 || firstSearch.NextCursor == "" || firstSearch.Total < 2 {
		t.Fatalf("unexpected first search page: %+v", firstSearch)
	}
	search2 := callTool(t, client, "research_search", map[string]any{
		"query": "needle", "limit": 1, "cursor": firstSearch.NextCursor,
	})
	var secondSearch researchgateway.SearchResponse
	decodeStructured(t, search2, &secondSearch)
	if len(secondSearch.Results) != 1 || secondSearch.Results[0].ID == firstSearch.Results[0].ID {
		t.Fatalf("unexpected second search page: %+v", secondSearch)
	}

	detailCall := callTool(t, client, "research_detail", map[string]any{"id": recordID})
	var detail researchgateway.Detail
	decodeStructured(t, detailCall, &detail)
	if detail.Record == nil || detail.Record.ID != recordID {
		t.Fatalf("unexpected record detail: %+v", detail)
	}

	read1 := callTool(t, client, "research_read", map[string]any{"document_id": documentID, "limit": 8})
	var firstRead researchgateway.ReadResponse
	decodeStructured(t, read1, &firstRead)
	if firstRead.Content == "" || firstRead.NextCursor == "" || firstRead.Bytes > 8 {
		t.Fatalf("unexpected first read chunk: %+v", firstRead)
	}
	read2 := callTool(t, client, "research_read", map[string]any{
		"document_id": documentID, "limit": 8, "cursor": firstRead.NextCursor,
	})
	var secondRead researchgateway.ReadResponse
	decodeStructured(t, read2, &secondRead)
	if secondRead.Offset <= firstRead.Offset || secondRead.Bytes > 8 {
		t.Fatalf("unexpected second read chunk: %+v", secondRead)
	}

	resources, err := client.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) != ResourceCount || resources.Resources[0].URI != catalogResourceURI {
		t.Fatalf("unexpected static resources: %#v", resources.Resources)
	}
	catalogResource, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: catalogResourceURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogResource.Contents) != 1 || !strings.Contains(catalogResource.Contents[0].Text, `"documents":3`) {
		t.Fatalf("unexpected catalog resource: %#v", catalogResource)
	}
	templates, err := client.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates.ResourceTemplates) != ResourceTemplateCount || templates.ResourceTemplates[0].URITemplate != itemResourceURI {
		t.Fatalf("unexpected resource templates: %#v", templates.ResourceTemplates)
	}
	itemURI := itemResourcePrefix + strings.ReplaceAll(documentID, ":", "%3A")
	itemResource, err := client.ReadResource(ctx, &mcp.ReadResourceParams{URI: itemURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(itemResource.Contents) != 1 || !strings.Contains(itemResource.Contents[0].Text, documentID) {
		t.Fatalf("unexpected item resource: %#v", itemResource)
	}

	prompts, err := client.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts.Prompts) != PromptCount || prompts.Prompts[0].Name != researchWorkflowPrompt {
		t.Fatalf("unexpected prompts: %#v", prompts.Prompts)
	}
	prompt, err := client.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: researchWorkflowPrompt, Arguments: map[string]string{"question": "What changed in sandboxing?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 {
		t.Fatalf("unexpected prompt result: %#v", prompt)
	}
	promptText, ok := prompt.Messages[0].Content.(*mcp.TextContent)
	if !ok || !strings.Contains(promptText.Text, "research_catalog") || !strings.Contains(promptText.Text, overview.SnapshotID) {
		t.Fatalf("unexpected workflow prompt: %#v", prompt.Messages[0].Content)
	}

	invalidKind, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "research_list", Arguments: map[string]any{"kind": "files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !invalidKind.IsError {
		t.Fatalf("invalid list kind was accepted: %#v", invalidKind)
	}
	arbitraryPath, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "research_read", Arguments: map[string]any{"document_id": documentID, "path": "/etc/passwd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !arbitraryPath.IsError {
		t.Fatalf("arbitrary path property was accepted: %#v", arbitraryPath)
	}
	invalidCursor, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "research_list", Arguments: map[string]any{"kind": "documents", "cursor": "invalid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !invalidCursor.IsError {
		t.Fatalf("invalid cursor was accepted: %#v", invalidCursor)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.CallTool(cancelled, &mcp.CallToolParams{Name: "research_catalog", Arguments: map[string]any{}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call error = %v, want context.Canceled", err)
	}

	after := snapshotFiles(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("read-only MCP calls changed harness files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestHandlersReopenSnapshotForCurrentContent(t *testing.T) {
	root := newResearchHarness(t)
	client := newTestClient(t, Config{HarnessRoot: root})

	first := callTool(t, client, "research_catalog", map[string]any{})
	var before researchgateway.Overview
	decodeStructured(t, first, &before)
	writeTestFile(t, filepath.Join(root, "research", "new-surface.md"), "# New surface\nbrand-new-marker\n")
	second := callTool(t, client, "research_catalog", map[string]any{})
	var after researchgateway.Overview
	decodeStructured(t, second, &after)
	if after.Summary.Documents != before.Summary.Documents+1 || after.SnapshotID == before.SnapshotID {
		t.Fatalf("catalog did not reopen after new Markdown: before=%+v after=%+v", before, after)
	}
	search := callTool(t, client, "research_search", map[string]any{"query": "brand-new-marker"})
	var response researchgateway.SearchResponse
	decodeStructured(t, search, &response)
	if response.Total != 1 || response.Results[0].Document == nil || response.SnapshotID != after.SnapshotID {
		t.Fatalf("search did not observe current content: %+v", response)
	}
}

func TestRegisterRejectsInvalidFixedAuthority(t *testing.T) {
	if err := Register(nil, Config{}); err == nil {
		t.Fatal("Register(nil) unexpectedly succeeded")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	if err := Register(server, Config{HarnessRoot: t.TempDir()}); err == nil {
		t.Fatal("Register accepted a directory without harness authority markers")
	}
}

type customRegisterLoader struct{}

func (customRegisterLoader) Format() string { return "custom_sources_v1" }

func (customRegisterLoader) Load(_ context.Context, input researchgateway.RegisterInput) (researchgateway.RegisterOutput, error) {
	checkedAt := input.ObservedAt.UTC()
	nextDueAt := checkedAt.Add(time.Hour)
	return researchgateway.RegisterOutput{Register: researchgateway.Register{
		ID: input.Config.ID, Format: input.Config.Format, Path: input.RelativePath, SHA256: input.SHA256,
		CapturedOn: checkedAt.Format("2006-01-02"), Scope: string(input.Data), Policy: "custom fixture",
		AllowedDomains: []string{}, Cadence: []researchgateway.CadencePolicy{},
		Freshness: researchgateway.Freshness{Status: researchgateway.FreshnessFresh, CheckedAt: &checkedAt, NextDueAt: &nextDueAt},
	}}, nil
}

func TestInjectedLoaderRegistryMountsDefaultAndCustomRegisters(t *testing.T) {
	root := newResearchHarness(t)
	writeTestFile(t, filepath.Join(root, "research", "custom.json"), "custom register payload")
	loaders, err := researchgateway.DefaultLoaderRegistry().With(customRegisterLoader{})
	if err != nil {
		t.Fatal(err)
	}
	client := newTestClient(t, Config{
		HarnessRoot: root,
		Loaders:     loaders,
		Registers: []researchgateway.RegisterConfig{
			{ID: "openai", Format: researchgateway.OpenAISourcesFormat, Path: researchgateway.DefaultOpenAISourcesPath},
			{ID: "custom", Format: "custom_sources_v1", Path: "research/custom.json"},
		},
	})
	result := callTool(t, client, "research_catalog", map[string]any{})
	var overview researchgateway.Overview
	decodeStructured(t, result, &overview)
	if overview.Summary.Registers != 2 || len(overview.Registers) != 2 || overview.Registers[0].ID != "custom" || overview.Registers[1].ID != "openai" {
		t.Fatalf("injected loader did not reach MCP catalog: %+v", overview.Registers)
	}
}

func newTestClient(t *testing.T, config Config) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "research-test", Version: "0.1.0"}, &mcp.ServerOptions{PageSize: 2})
	if err := Register(server, config); err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "research-test-client", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		serverSession.Close()
		t.Fatal(err)
	}
	if initialized := clientSession.InitializeResult(); initialized == nil || initialized.ProtocolVersion != "2026-07-28" {
		clientSession.Close()
		serverSession.Close()
		t.Fatalf("negotiated protocol = %#v, want 2026-07-28", initialized)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
	})
	return clientSession
}

func collectTools(t *testing.T, client *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	tools := map[string]*mcp.Tool{}
	for tool, err := range client.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	return tools
}

func callTool(t *testing.T, client *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s returned tool error: %#v", name, result.Content)
	}
	return result
}

func listPage(t *testing.T, client *mcp.ClientSession, arguments map[string]any) ListOutput {
	t.Helper()
	result := callTool(t, client, "research_list", arguments)
	var output ListOutput
	decodeStructured(t, result, &output)
	return output
}

func decodeStructured(t *testing.T, result *mcp.CallToolResult, output any) {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		t.Fatalf("decode structured content %s: %v", data, err)
	}
}

func assertAuthorityFreeInputSchema(t *testing.T, name string, schema any) {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if additional, ok := decoded["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("tool %q input schema is not closed: %s", name, data)
	}
	properties, _ := decoded["properties"].(map[string]any)
	for _, forbidden := range []string{"path", "root", "harness_root", "local_root", "url", "uri", "content", "write", "operation"} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("tool %q exposes forbidden authority property %q: %s", name, forbidden, data)
		}
	}
}

func newResearchHarness(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "AGENTS.md"), "# fixture\n")
	writeTestFile(t, filepath.Join(root, "codexkit", "go.mod"), "module fixture\n")
	registry, err := researchrefresh.EncodeRegistry(researchrefresh.Registry{
		SchemaVersion: researchrefresh.RegistrySchemaVersion,
		Feeds: []researchrefresh.Feed{
			{ID: "openai.codex.hooks", URL: "https://learn.chatgpt.com/docs/hooks.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 168, ContentTypes: []string{"text/markdown"}},
			{ID: "openai.codex.models", URL: "https://learn.chatgpt.com/docs/models.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 168, ContentTypes: []string{"text/markdown"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, researchgateway.DefaultRegistryPath), string(registry))
	writeTestFile(t, filepath.Join(root, researchgateway.DefaultOpenAISourcesPath), `{
  "schema_version": 1,
  "snapshot": {
    "captured_at": "2026-08-09",
    "scope": "Official OpenAI documentation.",
    "allowed_domains": ["developers.openai.com"],
    "policy": "Use as a cited source register."
  },
  "cadence": {
    "critical": "weekly guidance",
    "high": "biweekly guidance",
    "medium": "monthly guidance",
    "low": "quarterly guidance"
  },
  "sources": [
    {
      "id": "OAI-001",
      "title": "Sandbox guide",
      "url": "https://developers.openai.com/docs/alpha",
      "domain": "developers.openai.com",
      "category": "safety",
      "surfaces": ["cli"],
      "volatility": "critical",
      "refresh_cadence": "weekly",
      "verified_facts": ["A needle identifies the sandbox fact."],
      "watch_for": ["policy changes"]
    },
    {
      "id": "OAI-002",
      "title": "Configuration guide",
      "url": "https://developers.openai.com/docs/beta",
      "domain": "developers.openai.com",
      "category": "config",
      "surfaces": ["cli", "ide"],
      "volatility": "high",
      "refresh_cadence": "biweekly",
      "verified_facts": ["Configuration is typed."],
      "watch_for": ["new keys"]
    }
  ]
}`)
	writeTestFile(t, filepath.Join(root, "research", "alpha.md"), "# Alpha\nfirst needle document with enough content to paginate reads\n")
	writeTestFile(t, filepath.Join(root, "research", "beta.md"), "# Beta\nsecond needle document\n")
	writeTestFile(t, filepath.Join(root, "research", "nested", "gamma.markdown"), "# Gamma\nthird document\n")
	return root
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func ExampleRegister() {
	server := mcp.NewServer(&mcp.Implementation{Name: "codexkit", Version: "dev"}, nil)
	err := Register(server, Config{HarnessRoot: "/fixed/harness"})
	fmt.Println(err != nil)
	// Output: true
}
