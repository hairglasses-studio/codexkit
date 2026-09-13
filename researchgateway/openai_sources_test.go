package researchgateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenAISourcesAreCatalogedSearchedPagedAndDescribed(t *testing.T) {
	root := newHarnessFixture(t)
	writeMarkdown(t, root, "research/local/local.md", "# Local sandbox\nlocal sandbox guidance\n")
	writeOpenAISources(t, root, `
    {
      "id": "OAI-CX-002",
      "title": "Sandbox details",
      "url": "https://learn.chatgpt.com/docs/sandboxing",
      "domain": "learn.chatgpt.com",
      "category": "04-safety-auth",
      "surfaces": ["ide", "cli"],
      "volatility": "critical",
      "refresh_cadence": "weekly",
      "verified_facts": ["Sandboxing constrains filesystem and network access."],
      "watch_for": ["network defaults"]
    },
    {
      "id": "OAI-CX-001",
      "title": "Configuration reference",
      "url": "https://learn.chatgpt.com/docs/config",
      "domain": "learn.chatgpt.com",
      "category": "02-config-policy",
      "surfaces": ["cli"],
      "volatility": "high",
      "refresh_cadence": "biweekly",
      "verified_facts": ["Configuration keys are documented."],
      "watch_for": ["new keys"]
    }`)
	config := Config{
		HarnessRoot: root,
		LocalRoots:  []LocalRoot{{ID: "local", Path: "research/local"}},
		Now:         time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
	}
	gateway, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	catalog := gateway.Catalog()
	if catalog.Summary.Registers != 1 || catalog.Summary.Records != 2 || catalog.Summary.RecordFresh != 2 || catalog.Summary.RecordStale != 0 {
		t.Fatalf("unexpected register summary: %+v", catalog.Summary)
	}
	if len(catalog.Registers) != 1 || catalog.Registers[0].Path != DefaultOpenAISourcesPath || catalog.Registers[0].SHA256 == "" {
		t.Fatalf("unexpected register provenance: %+v", catalog.Registers)
	}
	if got := catalog.Records[0].SourceID; got != "OAI-CX-001" {
		t.Fatalf("records not sorted by typed ID: %q", got)
	}
	if got := catalog.Records[1].Surfaces; len(got) != 2 || got[0] != "cli" || got[1] != "ide" {
		t.Fatalf("surfaces not canonicalized: %+v", got)
	}

	page1, err := gateway.ListRecords(context.Background(), RecordListRequest{RegisterIDs: []string{"openai"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	page2, err := gateway.ListRecords(context.Background(), RecordListRequest{RegisterIDs: []string{"openai"}, Limit: 1, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if page1.Total != 2 || len(page1.Records) != 1 || len(page2.Records) != 1 || page1.Records[0].ID == page2.Records[0].ID {
		t.Fatalf("unexpected record pages: %+v %+v", page1, page2)
	}

	search, err := gateway.Search(context.Background(), SearchRequest{Query: "sandbox", RegisterIDs: []string{"openai"}})
	if err != nil {
		t.Fatal(err)
	}
	if search.Total != 1 || search.Results[0].Kind != "record" || search.Results[0].Record == nil || search.Results[0].Document != nil {
		t.Fatalf("unexpected record-only search: %+v", search)
	}
	detail, err := gateway.Describe(context.Background(), search.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Record == nil || detail.Record.SourceID != "OAI-CX-002" || detail.Record.RegisterSHA256 == "" {
		t.Fatalf("unexpected record detail: %+v", detail)
	}

	all, err := gateway.Search(context.Background(), SearchRequest{Query: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 2 {
		t.Fatalf("all-surface search total = %d, want local document and register record", all.Total)
	}
	localOnly, err := gateway.Search(context.Background(), SearchRequest{Query: "sandbox", RootIDs: []string{"local"}})
	if err != nil || localOnly.Total != 1 || localOnly.Results[0].Kind != "document" {
		t.Fatalf("local-only search = %+v, %v", localOnly, err)
	}
}

func TestOpenAISourceFreshnessIsSnapshotScopedAndSnapshotIDIsStable(t *testing.T) {
	root := newHarnessFixture(t)
	writeOpenAISources(t, root, `
    {
      "id": "OAI-CX-001",
      "title": "Sandbox",
      "url": "https://learn.chatgpt.com/docs/sandbox",
      "domain": "learn.chatgpt.com",
      "category": "safety",
      "surfaces": ["cli"],
      "volatility": "critical",
      "refresh_cadence": "weekly",
      "verified_facts": ["Sandbox fact."],
      "watch_for": ["changes"]
    }`)
	fresh, err := Open(context.Background(), Config{HarnessRoot: root, Now: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Open(context.Background(), Config{HarnessRoot: root, Now: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Catalog().Records[0].Freshness.Status != FreshnessFresh || stale.Catalog().Records[0].Freshness.Status != FreshnessStale {
		t.Fatalf("freshness did not advance on reopen: %s %s", fresh.Catalog().Records[0].Freshness.Status, stale.Catalog().Records[0].Freshness.Status)
	}
	if fresh.Catalog().SnapshotID != stale.Catalog().SnapshotID {
		t.Fatalf("wall-clock-only freshness changed snapshot ID: %s != %s", fresh.Catalog().SnapshotID, stale.Catalog().SnapshotID)
	}
	if fresh.Catalog().ObservedAt.Equal(stale.Catalog().ObservedAt) {
		t.Fatal("ObservedAt should make the fixed freshness evaluation time explicit")
	}
}

func TestOpenAISourcesRejectInvalidDomainAndRegisterEscape(t *testing.T) {
	root := newHarnessFixture(t)
	writeOpenAISources(t, root, `
    {
      "id": "OAI-CX-001",
      "title": "Bad",
      "url": "https://evil.example/docs",
      "domain": "evil.example",
      "category": "safety",
      "surfaces": ["cli"],
      "volatility": "critical",
      "refresh_cadence": "weekly",
      "verified_facts": ["Fact."],
      "watch_for": ["changes"]
    }`)
	if _, err := Open(context.Background(), Config{HarnessRoot: root, Now: testNow}); err == nil {
		t.Fatal("invalid source domain unexpectedly accepted")
	}

	outside := filepath.Join(t.TempDir(), "sources.json")
	writeRaw(t, outside, `{}`)
	_, err := Open(context.Background(), Config{
		HarnessRoot: root,
		Registers:   []RegisterConfig{{ID: "openai", Format: OpenAISourcesFormat, Path: outside}},
		Now:         testNow,
	})
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("register escape error = %v", err)
	}
}

func TestOpenAISourcesRejectUnsafeIDsPortsAndFutureSnapshots(t *testing.T) {
	for _, test := range []struct {
		name       string
		sourceID   string
		url        string
		domain     string
		observedAt time.Time
	}{
		{name: "URI punctuation", sourceID: "OAI#CX", url: "https://learn.chatgpt.com/docs/sandbox", domain: "learn.chatgpt.com", observedAt: testNow},
		{name: "explicit port", sourceID: "OAI-CX-001", url: "https://learn.chatgpt.com:8443/docs/sandbox", domain: "learn.chatgpt.com", observedAt: testNow},
		{name: "future snapshot", sourceID: "OAI-CX-001", url: "https://learn.chatgpt.com/docs/sandbox", domain: "learn.chatgpt.com", observedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newHarnessFixture(t)
			writeOpenAISources(t, root, fmt.Sprintf(`
    {
      "id": %q,
      "title": "Sandbox",
      "url": %q,
      "domain": %q,
      "category": "safety",
      "surfaces": ["cli"],
      "volatility": "critical",
      "refresh_cadence": "weekly",
      "verified_facts": ["Fact."],
      "watch_for": ["changes"]
    }`, test.sourceID, test.url, test.domain))
			if _, err := Open(context.Background(), Config{HarnessRoot: root, Now: test.observedAt}); err == nil {
				t.Fatal("unsafe source register unexpectedly accepted")
			}
		})
	}
}

type fixtureLoader struct{}

func (fixtureLoader) Format() string { return "fixture_v1" }

func (fixtureLoader) Load(_ context.Context, input RegisterInput) (RegisterOutput, error) {
	return RegisterOutput{Register: Register{
		ID: input.Config.ID, Format: input.Config.Format, Path: input.RelativePath, SHA256: input.SHA256,
		CapturedOn: "2026-08-11", Scope: string(input.Data), Policy: "fixture", AllowedDomains: []string{},
		Cadence: []CadencePolicy{}, Freshness: Freshness{Status: FreshnessFresh, CheckedAt: timePointer(input.ObservedAt), NextDueAt: timePointer(input.ObservedAt.Add(time.Hour))},
	}}, nil
}

func TestOpenWithLoaderRegistrySupportsAdditionalFormats(t *testing.T) {
	root := newHarnessFixture(t)
	writeRaw(t, filepath.Join(root, "research", "custom.json"), "custom payload")
	loaders, err := NewLoaderRegistry(openAISourcesLoader{}, fixtureLoader{})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := OpenWithLoaderRegistry(context.Background(), Config{
		HarnessRoot: root,
		Registers:   []RegisterConfig{{ID: "custom", Format: "fixture_v1", Path: "research/custom.json"}},
		Now:         testNow,
	}, loaders)
	if err != nil {
		t.Fatal(err)
	}
	if got := gateway.Catalog().Registers[0].Scope; got != "custom payload" {
		t.Fatalf("custom loader scope = %q", got)
	}
	if _, err := NewLoaderRegistry(fixtureLoader{}, fixtureLoader{}); err == nil {
		t.Fatal("duplicate loader unexpectedly accepted")
	}
}

func writeOpenAISources(t *testing.T, root, sources string) {
	t.Helper()
	content := fmt.Sprintf(`{
  "schema_version": 1,
  "snapshot": {
    "captured_at": "2026-08-09",
    "scope": "Official OpenAI documentation.",
    "allowed_domains": ["learn.chatgpt.com", "developers.openai.com", "platform.openai.com"],
    "policy": "Use as a cited source register."
  },
  "cadence": {
    "critical": "weekly",
    "high": "biweekly",
    "medium": "monthly",
    "low": "quarterly"
  },
  "sources": [%s]
}`, sources)
	writeRaw(t, filepath.Join(root, DefaultOpenAISourcesPath), content)
}
