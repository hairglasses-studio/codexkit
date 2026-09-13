package researchgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hairglasses-studio/codexkit/researchrefresh"
)

var testNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func TestOpenBuildsDeterministicCatalogWithFreshStaleAndMissingSources(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/zeta.md", "# Zeta\nalpha appears here\n")
	writeMarkdown(t, root, "research/local/alpha.md", "# Alpha Guide\nalpha alpha\n")
	writeMarkdown(t, root, "research/local/nested/no-title.markdown", "plain alpha notes\n")
	writeReceipts(t, root,
		testReceipt("feed.alpha", testNow.Add(-time.Hour), "a"),
		testReceipt("feed.stale", testNow.Add(-25*time.Hour), "b"),
	)

	gateway := openFixture(t, root, testNow)
	catalog := gateway.Catalog()
	if catalog.SchemaVersion != SchemaVersion || catalog.SnapshotID == "" {
		t.Fatalf("invalid catalog identity: %+v", catalog)
	}
	if catalog.Summary.Upstreams != 3 || catalog.Summary.Fresh != 1 || catalog.Summary.Stale != 1 || catalog.Summary.Missing != 1 {
		t.Fatalf("unexpected upstream summary: %+v", catalog.Summary)
	}
	if catalog.Summary.Documents != 3 || catalog.Summary.Roots != 1 || catalog.Summary.Bytes == 0 {
		t.Fatalf("unexpected local summary: %+v", catalog.Summary)
	}
	if catalog.RegistryPath != DefaultRegistryPath || catalog.ReceiptsPath != DefaultReceiptsPath {
		t.Fatalf("absolute or unexpected provenance paths: %+v", catalog)
	}
	for i := 1; i < len(catalog.Upstreams); i++ {
		if catalog.Upstreams[i-1].ID >= catalog.Upstreams[i].ID {
			t.Fatalf("upstreams are not sorted: %+v", catalog.Upstreams)
		}
	}
	for i := 1; i < len(catalog.Documents); i++ {
		if catalog.Documents[i-1].ID >= catalog.Documents[i].ID {
			t.Fatalf("documents are not sorted: %+v", catalog.Documents)
		}
	}
	if catalog.Documents[0].URI == "" || strings.HasPrefix(catalog.Documents[0].Path, root) {
		t.Fatalf("document provenance should be URI plus relative path: %+v", catalog.Documents[0])
	}

	first, err := MarshalCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCatalog(gateway.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatal("catalog JSON is not deterministic with a trailing newline")
	}

	// Catalog returns a defensive copy.
	catalog.Documents[0].Title = "mutated"
	catalog.Upstreams[0].Freshness.Status = "mutated"
	if gateway.Catalog().Documents[0].Title == "mutated" || gateway.Catalog().Upstreams[0].Freshness.Status == "mutated" {
		t.Fatal("catalog mutation escaped defensive copy")
	}
}

func TestOpenMissingReceiptsMarksEveryUpstreamMissing(t *testing.T) {
	root := newHarnessFixture(t)
	gateway := openFixture(t, root, testNow)
	if got := gateway.Overview().Summary.Missing; got != 3 {
		t.Fatalf("missing = %d, want 3", got)
	}
	for _, upstream := range gateway.Catalog().Upstreams {
		if upstream.Freshness.Status != FreshnessMissing || upstream.Receipt != nil {
			t.Fatalf("unexpected missing upstream: %+v", upstream)
		}
		encoded, err := json.Marshal(upstream.Freshness)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "0001-01-01") || strings.Contains(string(encoded), "checked_at") || strings.Contains(string(encoded), "next_due_at") {
			t.Fatalf("missing freshness emitted zero timestamps: %s", encoded)
		}
	}
}

func TestSearchAndListPaginationUseSnapshotBoundCursors(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/a.md", "# Alpha alpha\nalpha first\n")
	writeMarkdown(t, root, "research/local/b.md", "# Beta\nalpha second\n")
	writeMarkdown(t, root, "research/local/c.md", "# Gamma\nalpha third\n")
	gateway := openFixture(t, root, testNow)

	page1, err := gateway.Search(context.Background(), SearchRequest{Query: "ALPHA", RootIDs: []string{"local"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page1.Total != 3 || len(page1.Results) != 1 || page1.NextCursor == "" || page1.Results[0].Document == nil {
		t.Fatalf("unexpected first page: %+v", page1)
	}
	page2, err := gateway.Search(context.Background(), SearchRequest{Query: "alpha", RootIDs: []string{"local"}, Limit: 1, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if page2.Results[0].ID == page1.Results[0].ID {
		t.Fatal("cursor repeated the first hit")
	}
	if _, err := gateway.Search(context.Background(), SearchRequest{Query: "different", RootIDs: []string{"local"}, Limit: 1, Cursor: page1.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("query-mismatched cursor error = %v", err)
	}
	if _, err := gateway.Search(context.Background(), SearchRequest{Query: "alpha", RootIDs: []string{"local"}, Limit: gateway.Limits().MaxPageSize + 1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversize search error = %v", err)
	}
	if _, err := gateway.Search(context.Background(), SearchRequest{Query: "alpha", RootIDs: []string{"missing"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown root error = %v", err)
	}
	upstreamSearch, err := gateway.Search(context.Background(), SearchRequest{Query: "feed.alpha"})
	if err != nil || upstreamSearch.Total != 1 || upstreamSearch.Results[0].Kind != "upstream" || upstreamSearch.Results[0].Upstream == nil {
		t.Fatalf("upstream provenance was not searchable: %+v, %v", upstreamSearch, err)
	}

	list1, err := gateway.ListDocuments(context.Background(), ListRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	list2, err := gateway.ListDocuments(context.Background(), ListRequest{Limit: 2, Cursor: list1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if list1.Total != 3 || len(list1.Documents) != 2 || len(list2.Documents) != 1 {
		t.Fatalf("unexpected list pages: %+v %+v", list1, list2)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(page1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var payload cursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Offset++ // Keep the old checksum to prove integrity validation.
	tamperedJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	tampered := base64.RawURLEncoding.EncodeToString(tamperedJSON)
	if _, err := gateway.Search(context.Background(), SearchRequest{Query: "alpha", RootIDs: []string{"local"}, Limit: 1, Cursor: tampered}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func TestReadDocumentChunksUTF8AndRejectsCrossDocumentCursor(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/a.md", "ab😊cdé")
	writeMarkdown(t, root, "research/local/b.md", "second")
	gateway := openFixture(t, root, testNow)
	documents := gateway.Catalog().Documents
	var firstID, secondID string
	for _, document := range documents {
		switch document.RelativePath {
		case "a.md":
			firstID = document.ID
		case "b.md":
			secondID = document.ID
		}
	}
	var assembled strings.Builder
	cursor := ""
	for {
		page, err := gateway.ReadDocument(context.Background(), ReadRequest{DocumentID: firstID, Limit: 4, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page.Content) || page.Bytes > 4 {
			t.Fatalf("invalid bounded UTF-8 page: %+v", page)
		}
		assembled.WriteString(page.Content)
		if page.NextCursor == "" {
			break
		}
		if _, err := gateway.ReadDocument(context.Background(), ReadRequest{DocumentID: secondID, Limit: 4, Cursor: page.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("cross-document cursor error = %v", err)
		}
		cursor = page.NextCursor
	}
	if assembled.String() != "ab😊cdé" {
		t.Fatalf("assembled = %q", assembled.String())
	}
	if _, err := gateway.ReadDocument(context.Background(), ReadRequest{DocumentID: firstID, Limit: 3}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("too-small read error = %v", err)
	}
	if _, err := gateway.ReadDocument(context.Background(), ReadRequest{DocumentID: "document:missing", Limit: 4}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("missing document error = %v", err)
	}
}

func TestEncodedResponseBudgetFailsClosed(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/a.md", "# Alpha\ncontent\n")
	config := fixtureConfig(root, testNow)
	config.Limits.MaxResponseBytes = 64
	gateway, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ListDocuments(context.Background(), ListRequest{Limit: 1}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized encoded response error = %v", err)
	}
}

func TestOpenRejectsPathAndSymlinkEscapes(t *testing.T) {
	t.Run("lexical registry escape", func(t *testing.T) {
		root := newHarnessFixture(t)
		_, err := Open(context.Background(), Config{HarnessRoot: root, RegistryPath: "../feeds.json", Registers: []RegisterConfig{}})
		if !errors.Is(err, ErrPathEscape) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("registry symlink escape", func(t *testing.T) {
		root := newHarnessFixture(t)
		outside := filepath.Join(t.TempDir(), "feeds.json")
		writeRaw(t, outside, `{}`)
		link := filepath.Join(root, "research", "upstreams", "linked.json")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		_, err := Open(context.Background(), Config{HarnessRoot: root, RegistryPath: "research/upstreams/linked.json", Registers: []RegisterConfig{}})
		if !errors.Is(err, ErrPathEscape) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("local root escape", func(t *testing.T) {
		root := newHarnessFixture(t)
		outside := t.TempDir()
		link := filepath.Join(root, "research", "linked")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		_, err := Open(context.Background(), Config{HarnessRoot: root, LocalRoots: []LocalRoot{{ID: "local", Path: "research/linked"}}, Registers: []RegisterConfig{}})
		if !errors.Is(err, ErrPathEscape) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("document symlink denied", func(t *testing.T) {
		root := newHarnessFixture(t)
		outside := filepath.Join(t.TempDir(), "outside.md")
		writeRaw(t, outside, "secret")
		if err := os.MkdirAll(filepath.Join(root, "research", "local"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "research", "local", "escape.md")); err != nil {
			t.Fatal(err)
		}
		_, err := Open(context.Background(), fixtureConfig(root, testNow))
		if !errors.Is(err, ErrSymlink) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestOpenEnforcesConstructionLimitsAndCancellation(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/a.md", "123456")
	config := fixtureConfig(root, testNow)
	config.Limits.MaxDocumentBytes = 5
	if _, err := Open(context.Background(), config); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("document byte limit error = %v", err)
	}

	writeMarkdown(t, root, "research/local/b.md", "x")
	config = fixtureConfig(root, testNow)
	config.Limits.MaxDocuments = 1
	if _, err := Open(context.Background(), config); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("document count limit error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(cancelled, fixtureConfig(root, testNow)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestSnapshotIgnoresObservationTimeAndFilesystemMtime(t *testing.T) {
	root := newHarnessFixture(t)
	path := filepath.Join(root, "research", "local", "a.md")
	writeMarkdown(t, root, "research/local/a.md", "# Stable\nneedle\n")
	writeMarkdown(t, root, "research/local/b.md", "# Also stable\nneedle again\n")
	first := openFixture(t, root, testNow)
	page, err := first.Search(context.Background(), SearchRequest{Query: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Fatal("expected a cursor for reopen stability check")
	}
	changedTime := testNow.Add(48 * time.Hour)
	if err := os.Chtimes(path, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	second := openFixture(t, root, testNow.Add(365*24*time.Hour))
	if first.Catalog().SnapshotID != second.Catalog().SnapshotID {
		t.Fatalf("content-identical snapshot changed: %s != %s", first.Catalog().SnapshotID, second.Catalog().SnapshotID)
	}
	if _, err := second.Search(context.Background(), SearchRequest{Query: "needle", Limit: 1, Cursor: page.NextCursor}); err != nil {
		t.Fatalf("stable cursor failed across reopen: %v", err)
	}
}

func newHarnessFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeRaw(t, filepath.Join(root, "AGENTS.md"), "# fixture\n")
	writeRaw(t, filepath.Join(root, "codexkit", "go.mod"), "module fixture\n")
	registry := researchrefresh.Registry{
		SchemaVersion: researchrefresh.RegistrySchemaVersion,
		Feeds: []researchrefresh.Feed{
			{ID: "feed.missing", URL: "https://learn.chatgpt.com/docs/models.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/markdown"}},
			{ID: "feed.stale", URL: "https://learn.chatgpt.com/docs/hooks.md", Kind: researchrefresh.KindMarkdown, CadenceHours: 24, ContentTypes: []string{"text/markdown"}},
			{ID: "feed.alpha", URL: "https://learn.chatgpt.com/docs/changelog", Kind: researchrefresh.KindHTML, CadenceHours: 24, ContentTypes: []string{"text/html"}},
		},
	}
	data, err := researchrefresh.EncodeRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, filepath.Join(root, DefaultRegistryPath), string(data))
	if err := os.MkdirAll(filepath.Join(root, "research", "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func openFixture(t *testing.T, root string, now time.Time) *Gateway {
	t.Helper()
	gateway, err := Open(context.Background(), fixtureConfig(root, now))
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func fixtureConfig(root string, now time.Time) Config {
	return Config{
		HarnessRoot: root,
		LocalRoots:  []LocalRoot{{ID: "local", Path: "research/local"}},
		Registers:   []RegisterConfig{},
		Now:         now,
	}
}

func testReceipt(feedID string, checkedAt time.Time, hashCharacter string) researchrefresh.Receipt {
	url := map[string]string{
		"feed.alpha": "https://learn.chatgpt.com/docs/changelog",
		"feed.stale": "https://learn.chatgpt.com/docs/hooks.md",
	}[feedID]
	contentType := "text/markdown"
	if feedID == "feed.alpha" {
		contentType = "text/html"
	}
	return researchrefresh.Receipt{
		SchemaVersion:  researchrefresh.ReceiptSchemaVersion,
		FeedID:         feedID,
		SourceURL:      url,
		FinalURL:       url,
		Status:         researchrefresh.StatusFetched,
		CheckedAt:      checkedAt,
		FetchedAt:      checkedAt,
		HTTPStatus:     200,
		ContentType:    contentType,
		RawSHA256:      strings.Repeat(hashCharacter, 64),
		SemanticSHA256: strings.Repeat(hashCharacter, 64),
	}
}

func writeReceipts(t *testing.T, root string, receipts ...researchrefresh.Receipt) {
	t.Helper()
	data, err := researchrefresh.EncodeReceipts(researchrefresh.Receipts{SchemaVersion: researchrefresh.ReceiptSchemaVersion, Receipts: receipts})
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, filepath.Join(root, DefaultReceiptsPath), string(data))
}

func writeMarkdown(t *testing.T, root, relative, content string) {
	t.Helper()
	writeRaw(t, filepath.Join(root, filepath.FromSlash(relative)), content)
}

func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
