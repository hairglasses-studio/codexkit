package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/hairglasses-studio/codexkit/researchrefresh"
)

func TestResearchRefreshDryRunThenAtomicWrite(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("ETag", `"fixture-v1"`)
		fmt.Fprint(w, "fixture semantic content\n")
	}))
	defer server.Close()

	root := newResearchHarness(t, []researchrefresh.Feed{{
		ID:           "alpha",
		URL:          "https://learn.chatgpt.com/docs/models.md",
		Kind:         researchrefresh.KindMarkdown,
		CadenceHours: 24,
		ContentTypes: []string{"text/plain"},
	}})
	receiptsPath := filepath.Join(root, defaultResearchReceipts)
	now := "2026-08-11T20:00:00Z"
	deps := researchCommandDeps{client: rewriteResearchClient(t, server), getenv: func(string) string { return "" }}

	var dryOut, dryErr bytes.Buffer
	code := executeResearch([]string{"refresh", root, "--json", "--now", now}, &dryOut, &dryErr, deps)
	if code != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s stdout=%s", code, dryErr.String(), dryOut.String())
	}
	if _, err := os.Stat(receiptsPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run unexpectedly created receipts: %v", err)
	}
	if strings.Contains(dryOut.String(), "fixture semantic content") {
		t.Fatalf("dry-run JSON leaked fetched content: %s", dryOut.String())
	}
	if !strings.Contains(dryOut.String(), `"mode": "dry-run"`) || !strings.Contains(dryOut.String(), `"wrote": false`) {
		t.Fatalf("unexpected dry-run JSON: %s", dryOut.String())
	}

	var writeOut, writeErr bytes.Buffer
	code = executeResearch([]string{"refresh", "--write", "--now", now, root, "--json"}, &writeOut, &writeErr, deps)
	if code != 0 {
		t.Fatalf("write exit=%d stderr=%s stdout=%s", code, writeErr.String(), writeOut.String())
	}
	if !strings.Contains(writeOut.String(), `"wrote": true`) {
		t.Fatalf("write JSON did not report mutation: %s", writeOut.String())
	}
	data, err := os.ReadFile(receiptsPath)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := researchrefresh.LoadReceipts(data)
	if err != nil {
		t.Fatalf("load written receipt aggregate: %v", err)
	}
	receipt, ok := receipts.Receipt("alpha")
	if !ok || receipt.CheckedAt.Format(time.RFC3339) != now {
		t.Fatalf("unexpected alpha receipt: %#v, found=%t", receipt, ok)
	}
	info, err := os.Stat(receiptsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("receipt mode=%#o, want 0644", got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests=%d, want one dry-run and one write fetch", got)
	}
}

func TestResearchFreshnessIsOfflineFilteredAndDeterministic(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "current\n")
	}))

	root := newResearchHarness(t, []researchrefresh.Feed{
		{ID: "beta", URL: "https://learn.chatgpt.com/docs/app-server.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/plain"}},
		{ID: "alpha", URL: "https://learn.chatgpt.com/docs/models.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/plain"}},
	})
	deps := researchCommandDeps{client: rewriteResearchClient(t, server), getenv: func(string) string { return "" }}
	var refreshOut, refreshErr bytes.Buffer
	code := executeResearch([]string{"refresh", root, "--source", "alpha", "--write", "--now", "2026-08-11T20:00:00Z"}, &refreshOut, &refreshErr, deps)
	if code != 0 {
		t.Fatalf("seed refresh exit=%d stderr=%s stdout=%s", code, refreshErr.String(), refreshOut.String())
	}
	server.Close()

	for i := 0; i < 2; i++ {
		var out, errOut bytes.Buffer
		code = executeResearch([]string{"freshness", root, "--source", "alpha", "--json", "--now", "2026-08-11T21:00:00Z"}, &out, &errOut, researchCommandDeps{getenv: func(string) string { return "" }})
		if code != 0 {
			t.Fatalf("freshness exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
		}
		if i == 0 {
			refreshOut = out
		} else if out.String() != refreshOut.String() {
			t.Fatalf("freshness JSON is nondeterministic\nfirst:\n%s\nsecond:\n%s", refreshOut.String(), out.String())
		}
	}

	var allOut, allErr bytes.Buffer
	code = executeResearch([]string{"freshness", "--json", "--now", "2026-08-11T21:00:00Z", root}, &allOut, &allErr, researchCommandDeps{getenv: func(string) string { return "" }})
	if code == 0 || !strings.Contains(allOut.String(), `"feed_id": "beta"`) || !strings.Contains(allOut.String(), `"status": "missing"`) {
		t.Fatalf("missing required source should fail: exit=%d stderr=%s stdout=%s", code, allErr.String(), allOut.String())
	}
	if strings.Contains(allOut.String(), `0001-01-01`) {
		t.Fatalf("missing receipt serialized a zero timestamp: %s", allOut.String())
	}

	var staleOut, staleErr bytes.Buffer
	code = executeResearch([]string{"freshness", root, "--source", "alpha", "--now", "2026-08-12T20:00:00Z"}, &staleOut, &staleErr, researchCommandDeps{getenv: func(string) string { return "" }})
	if code == 0 || !strings.Contains(staleOut.String(), "alpha: stale") {
		t.Fatalf("stale required source should fail: exit=%d stderr=%s stdout=%s", code, staleErr.String(), staleOut.String())
	}
}

func TestResearchRefreshDoesNotWritePartialReceipts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/docs/codex-sdk.md" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "current\n")
	}))
	defer server.Close()

	root := newResearchHarness(t, []researchrefresh.Feed{
		{ID: "alpha", URL: "https://learn.chatgpt.com/docs/models.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/plain"}},
		{ID: "broken", URL: "https://learn.chatgpt.com/docs/codex-sdk.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/plain"}},
	})
	var out, errOut bytes.Buffer
	code := executeResearch(
		[]string{"refresh", root, "--write", "--now", "2026-08-11T20:00:00Z"},
		&out,
		&errOut,
		researchCommandDeps{client: rewriteResearchClient(t, server), getenv: func(string) string { return "" }},
	)
	if code == 0 || !strings.Contains(out.String(), "broken") {
		t.Fatalf("partial refresh should fail: exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	if _, err := os.Stat(filepath.Join(root, defaultResearchReceipts)); !os.IsNotExist(err) {
		t.Fatalf("failed refresh wrote a partial aggregate: %v", err)
	}
}

func TestResearchPathsStayWithinResolvedHarness(t *testing.T) {
	root := newResearchHarness(t, []researchrefresh.Feed{})
	nested := filepath.Join(root, "codexkit", "cmd", "codexkit")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside-receipts.json")

	var out, errOut bytes.Buffer
	code := executeResearch(
		[]string{"freshness", "--receipts", outside, "--now", "2026-08-11T20:00:00Z"},
		&out,
		&errOut,
		researchCommandDeps{getwd: func() (string, error) { return nested, nil }, getenv: func(string) string { return "" }},
	)
	if code == 0 || !strings.Contains(errOut.String(), "escapes harness root") {
		t.Fatalf("outside override should fail: exit=%d stderr=%s", code, errOut.String())
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside receipt path was touched: %v", err)
	}
}

func TestParseResearchCommandRejectsMutationOnFreshness(t *testing.T) {
	if _, err := parseResearchCommand([]string{"freshness", "--write"}); err == nil {
		t.Fatal("freshness accepted --write")
	}
	if _, err := parseResearchCommand([]string{"refresh", "--now", "tomorrow"}); err == nil {
		t.Fatal("refresh accepted invalid RFC3339 time")
	}
	if _, err := parseResearchCommand([]string{"catalog", "--source", "alpha"}); err == nil {
		t.Fatal("catalog accepted a source filter")
	}
}

func TestResearchCatalogDogfoodsOfflineGateway(t *testing.T) {
	root := newResearchHarness(t, []researchrefresh.Feed{{
		ID: "alpha", URL: "https://learn.chatgpt.com/docs/models.md", Kind: researchrefresh.KindMarkdown,
		CadenceHours: 24, ContentTypes: []string{"text/markdown"},
	}})
	checkedAt := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	receipts, err := researchrefresh.EncodeReceipts(researchrefresh.Receipts{
		SchemaVersion: researchrefresh.ReceiptSchemaVersion,
		Receipts: []researchrefresh.Receipt{{
			SchemaVersion: researchrefresh.ReceiptSchemaVersion,
			FeedID:        "alpha", SourceURL: "https://learn.chatgpt.com/docs/models.md", FinalURL: "https://learn.chatgpt.com/docs/models.md",
			Status: researchrefresh.StatusFetched, CheckedAt: checkedAt, FetchedAt: checkedAt,
			HTTPStatus: http.StatusOK, ContentType: "text/markdown",
			RawSHA256: strings.Repeat("a", 64), SemanticSHA256: strings.Repeat("a", 64),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, defaultResearchReceipts), receipts, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "research", "openai"), 0o755); err != nil {
		t.Fatal(err)
	}
	sources := `{
  "schema_version": 1,
  "snapshot": {
    "captured_at": "2026-08-11",
    "scope": "Official OpenAI documentation.",
    "allowed_domains": ["learn.chatgpt.com"],
    "policy": "Use as a cited source register."
  },
  "cadence": {
    "critical": "weekly",
    "high": "biweekly",
    "medium": "monthly",
    "low": "quarterly"
  },
  "sources": [{
    "id": "OAI-CX-001",
    "title": "Models",
    "url": "https://learn.chatgpt.com/docs/models.md",
    "domain": "learn.chatgpt.com",
    "category": "models",
    "surfaces": ["cli"],
    "volatility": "critical",
    "refresh_cadence": "weekly",
    "verified_facts": ["The official model catalog is externally owned."],
    "watch_for": ["model availability changes"]
  }]
}`
	if err := os.WriteFile(filepath.Join(root, "research", "openai", "sources.json"), []byte(sources), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "research", "summary.md"), []byte("# Summary\n\nOfficial model research.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := executeResearch(
		[]string{"catalog", root, "--json", "--now", "2026-08-11T20:00:00Z"},
		&out,
		&errOut,
		researchCommandDeps{getenv: func(string) string { return "" }},
	)
	if code != 0 {
		t.Fatalf("catalog exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var overview researchgateway.Overview
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode catalog: %v\n%s", err, out.String())
	}
	if overview.SchemaVersion != 1 || overview.SnapshotID == "" || overview.Summary.Upstreams != 1 || overview.Summary.Registers != 1 || overview.Summary.Records != 1 || overview.Summary.Documents != 1 {
		t.Fatalf("unexpected catalog overview: %+v", overview)
	}
	if len(overview.Roots) != 1 || overview.Roots[0].ID != "research" || filepath.IsAbs(overview.Roots[0].Path) {
		t.Fatalf("catalog leaked or omitted root authority: %+v", overview.Roots)
	}

	out.Reset()
	errOut.Reset()
	code = executeResearch(
		[]string{"catalog", root, "--json", "--now", "2026-09-01T20:00:00Z"},
		&out,
		&errOut,
		researchCommandDeps{getenv: func(string) string { return "" }},
	)
	if code == 0 || !strings.Contains(out.String(), `"record_stale": 1`) {
		t.Fatalf("stale catalog should fail after emitting evidence: exit=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
}

func TestResearchReadOnlyToolsAreRegistered(t *testing.T) {
	for _, name := range []string{"research_refresh", "research_freshness"} {
		tool, ok := registry.GetTool(name)
		if !ok {
			t.Fatalf("CLI registry is missing %s", name)
		}
		if readOnly, _ := tool.Annotations["readOnlyHint"].(bool); !readOnly {
			t.Fatalf("%s is not annotated read-only: %#v", name, tool.Annotations)
		}
		properties, _ := tool.Schema["properties"].(map[string]any)
		if _, exposesWrite := properties["write"]; exposesWrite {
			t.Fatalf("%s exposes receipt mutation", name)
		}
	}
}

func newResearchHarness(t *testing.T, feeds []researchrefresh.Feed) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "codexkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "research", "upstreams"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# test harness\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "codexkit", "go.mod"), []byte("module test.invalid/harness\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registryData, err := researchrefresh.EncodeRegistry(researchrefresh.Registry{
		SchemaVersion: researchrefresh.RegistrySchemaVersion,
		Feeds:         feeds,
	})
	if err != nil {
		t.Fatalf("encode fixture registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, defaultResearchRegistry), registryData, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func rewriteResearchClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := server.Client()
	transport, ok := base.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("test server transport type %T", base.Transport)
	}
	transport = transport.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, target.Host)
	}
	base.Transport = transport
	return base
}
