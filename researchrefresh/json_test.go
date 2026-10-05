package researchrefresh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryValidationFailsClosed(t *testing.T) {
	valid := testFeed("official", "https://learn.chatgpt.com/docs/changelog", KindHTML)
	tests := []struct {
		name  string
		feeds []Feed
	}{
		{name: "duplicate id", feeds: []Feed{valid, valid}},
		{name: "http URL", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "http://learn.chatgpt.com/docs/changelog" })}},
		{name: "URL credentials", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://user:secret@" + "learn.chatgpt.com/docs/changelog" })}},
		{name: "URL query", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://learn.chatgpt.com/docs/changelog?token=secret" })}},
		{name: "explicit port", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://learn.chatgpt.com:443/docs/changelog" })}},
		{name: "IP literal", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://127.0.0.1/docs/changelog" })}},
		{name: "localhost", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://localhost/docs/changelog" })}},
		{name: "internal host", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://research.internal/docs/changelog" })}},
		{name: "off-policy host", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://example.com/docs/changelog" })}},
		{name: "off-policy path", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.URL = "https://learn.chatgpt.com/private/changelog" })}},
		{name: "invalid id", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.ID = "UPPER" })}},
		{name: "zero cadence", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.CadenceHours = 0 })}},
		{name: "wildcard redirect", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.AllowedRedirectDomains = []string{"*.example.com"} })}},
		{name: "redirect port", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.AllowedRedirectDomains = []string{"example.com:443"} })}},
		{name: "off-policy redirect", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.AllowedRedirectDomains = []string{"developers.openai.com"} })}},
		{name: "duplicate content type", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.ContentTypes = []string{"text/html", "text/html"} })}},
		{name: "content type parameters", feeds: []Feed{withFeed(valid, func(feed *Feed) { feed.ContentTypes = []string{"text/html; charset=utf-8"} })}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRegistry(Registry{SchemaVersion: RegistrySchemaVersion, Feeds: test.feeds})
			if !errors.Is(err, ErrInvalidRegistry) {
				t.Fatalf("ValidateRegistry error = %v, want ErrInvalidRegistry", err)
			}
		})
	}
}

func TestRegistryLoadRejectsUnknownFields(t *testing.T) {
	_, err := LoadRegistry([]byte(`{"schema_version":1,"feeds":[],"surprise":true}`))
	if !errors.Is(err, ErrInvalidRegistry) {
		t.Fatalf("LoadRegistry error = %v, want ErrInvalidRegistry", err)
	}
}

func TestRegistryAndReceiptsEncodeDeterministically(t *testing.T) {
	registry := Registry{SchemaVersion: RegistrySchemaVersion, Feeds: []Feed{
		{ID: "zeta", URL: "https://developers.openai.com/codex/codex-manual.md", Kind: KindMarkdown, CadenceHours: 24, AllowedRedirectDomains: []string{"learn.chatgpt.com"}, ContentTypes: []string{"text/plain", "application/octet-stream"}},
		{ID: "alpha", URL: "https://learn.chatgpt.com/docs/changelog", Kind: KindHTML, CadenceHours: 48},
	}}
	encoded, err := EncodeRegistry(registry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(encoded), `"id": "alpha"`) > strings.Index(string(encoded), `"id": "zeta"`) {
		t.Fatalf("registry feeds were not sorted: %s", encoded)
	}
	if registry.Feeds[0].ContentTypes[0] != "text/plain" {
		t.Fatal("EncodeRegistry mutated caller")
	}
	reloaded, err := LoadRegistry(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeRegistry(reloaded)
	if err != nil || string(reencoded) != string(encoded) {
		t.Fatalf("registry roundtrip is not deterministic: err=%v\nfirst=%s\nsecond=%s", err, encoded, reencoded)
	}

	alpha := validReceipt("alpha", fixedTime())
	zeta := validReceipt("zeta", fixedTime())
	receipts := Receipts{SchemaVersion: ReceiptSchemaVersion, Receipts: []Receipt{zeta, alpha}}
	receiptJSON, err := EncodeReceipts(receipts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(receiptJSON), `"feed_id": "alpha"`) > strings.Index(string(receiptJSON), `"feed_id": "zeta"`) {
		t.Fatalf("receipts were not sorted: %s", receiptJSON)
	}
	loaded, err := LoadReceipts(receiptJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Receipt("alpha"); !ok {
		t.Fatal("loaded receipts lost alpha")
	}
	updated, err := loaded.WithReceipt(validReceipt("beta", fixedTime()))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Receipts) != 3 || updated.Receipts[1].FeedID != "beta" {
		t.Fatalf("WithReceipt did not insert in deterministic order: %#v", updated.Receipts)
	}
}

func TestReceiptsRejectDuplicateFeedIDs(t *testing.T) {
	receipt := validReceipt("duplicate", fixedTime())
	err := ValidateReceipts(Receipts{
		SchemaVersion: ReceiptSchemaVersion,
		Receipts:      []Receipt{receipt, receipt},
	})
	if !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("ValidateReceipts error = %v, want ErrInvalidReceipt", err)
	}
}

func TestReceiptRejectsOffPolicyURLRelationship(t *testing.T) {
	receipt := validReceipt("off-policy", fixedTime())
	receipt.FinalURL = "https://learn.chatgpt.com/docs/changelog"
	if err := ValidateReceipt(receipt); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("ValidateReceipt error = %v, want ErrInvalidReceipt", err)
	}
}

func TestEvaluateDueCadence(t *testing.T) {
	feed := testFeed("cadence", "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json", KindText)
	previous := validReceipt(feed.ID, fixedTime())

	before, err := EvaluateDue(feed, &previous, fixedTime().Add(24*time.Hour-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if before.Due {
		t.Fatal("feed was due before cadence boundary")
	}
	at, err := EvaluateDue(feed, &previous, fixedTime().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !at.Due || !at.NextDueAt.Equal(fixedTime().Add(24*time.Hour)) {
		t.Fatalf("feed was not due at cadence boundary: %#v", at)
	}
	missing, err := EvaluateDue(feed, nil, fixedTime())
	if err != nil || !missing.Due {
		t.Fatalf("missing receipt should be due: %#v err=%v", missing, err)
	}
}

func TestEvaluateDueRejectsReceiptNotBoundToFeedOrTime(t *testing.T) {
	feed := testFeed("cadence", "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json", KindText)
	now := fixedTime().Add(time.Hour)
	tests := []struct {
		name   string
		feed   Feed
		mutate func(*Receipt)
	}{
		{name: "wrong source", feed: feed, mutate: func(receipt *Receipt) {
			receipt.SourceURL = "https://learn.chatgpt.com/docs/changelog"
			receipt.FinalURL = receipt.SourceURL
		}},
		{name: "future check", feed: feed, mutate: func(receipt *Receipt) {
			receipt.CheckedAt = now.Add(time.Hour)
			receipt.FetchedAt = receipt.CheckedAt
		}},
		{name: "wrong content type", feed: feed, mutate: func(receipt *Receipt) {
			receipt.ContentType = "text/html"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := validReceipt(test.feed.ID, fixedTime())
			test.mutate(&receipt)
			if _, err := EvaluateDue(test.feed, &receipt, now); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("EvaluateDue error = %v, want ErrInvalidReceipt", err)
			}
		})
	}
}

func TestValidateReceiptForFeedRequiresProviderAndCanonicalReleaseEvidence(t *testing.T) {
	hash := strings.Repeat("a", 64)
	manual := testFeed("manual", "https://developers.openai.com/codex/codex-manual.md", KindMarkdown)
	manual.AllowedRedirectDomains = []string{"learn.chatgpt.com"}
	manual.ContentTypes = []string{"application/octet-stream"}
	manual.VerifyProviderSHA256 = true
	manualReceipt := validReceipt(manual.ID, fixedTime())
	manualReceipt.SourceURL = manual.URL
	manualReceipt.FinalURL = "https://learn.chatgpt.com/docs/codex-manual.md"
	manualReceipt.ContentType = "application/octet-stream"
	if err := ValidateReceiptForFeed(manual, manualReceipt, fixedTime()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("missing provider evidence error = %v", err)
	}
	manualReceipt.ProviderSHA256 = hash
	if err := ValidateReceiptForFeed(manual, manualReceipt, fixedTime()); err != nil {
		t.Fatalf("valid provider evidence rejected: %v", err)
	}

	releaseFeed := testFeed("release", "https://api.github.com/repos/openai/codex/releases/latest", KindGitHubLatestRelease)
	releaseFeed.ContentTypes = []string{"application/json"}
	releaseReceipt := validReceipt(releaseFeed.ID, fixedTime())
	releaseReceipt.SourceURL = releaseFeed.URL
	releaseReceipt.FinalURL = releaseFeed.URL
	releaseReceipt.ContentType = "application/json"
	if err := ValidateReceiptForFeed(releaseFeed, releaseReceipt, fixedTime()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("missing release metadata error = %v", err)
	}
	release := &ReleaseMetadata{
		Tag: "v1.2.3", PublishedAt: fixedTime().Add(-time.Hour), URL: "https://github.com/openai/codex/releases/tag/v1.2.3",
		TargetCommitish: "main", BodySHA256: hash,
	}
	canonical, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	semanticHash := sha256.Sum256(canonical)
	releaseReceipt.Release = release
	releaseReceipt.SemanticSHA256 = hex.EncodeToString(semanticHash[:])
	if err := ValidateReceiptForFeed(releaseFeed, releaseReceipt, fixedTime()); err != nil {
		t.Fatalf("valid release evidence rejected: %v", err)
	}
	releaseReceipt.Release.Tag = "v9.9.9"
	if err := ValidateReceiptForFeed(releaseFeed, releaseReceipt, fixedTime()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("forged release metadata error = %v", err)
	}
	releaseReceipt.Release.Tag = "v1.2.3"
	releaseReceipt.Release.PublishedAt = fixedTime().Add(time.Hour)
	canonical, _ = json.Marshal(releaseReceipt.Release)
	semanticHash = sha256.Sum256(canonical)
	releaseReceipt.SemanticSHA256 = hex.EncodeToString(semanticHash[:])
	if err := ValidateReceiptForFeed(releaseFeed, releaseReceipt, fixedTime()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("future release metadata error = %v", err)
	}
}

func TestValidateReceiptRejectsValidatorlessNotModified(t *testing.T) {
	receipt := validReceipt("unchanged", fixedTime())
	receipt.Status = StatusNotModified
	receipt.HTTPStatus = http.StatusNotModified
	if err := ValidateReceipt(receipt); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("validatorless 304 error = %v", err)
	}
}

func TestCommittedRegistryMatchesCuratedPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "research", "upstreams", "feeds.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := LoadRegistry(data)
	if err != nil {
		t.Fatalf("committed registry violates curated upstream policy: %v", err)
	}
	if len(registry.Feeds) != len(curatedUpstreams) {
		t.Fatalf("committed feeds = %d, curated policies = %d", len(registry.Feeds), len(curatedUpstreams))
	}
	seen := make(map[string]bool, len(registry.Feeds))
	for _, feed := range registry.Feeds {
		seen[feed.URL] = true
		for _, domain := range feed.AllowedRedirectDomains {
			if err := validateConfiguredRedirect(feed.URL, domain); err != nil {
				t.Fatalf("feed %q redirect %q violates curated policy: %v", feed.ID, domain, err)
			}
		}
	}
	for source := range curatedUpstreams {
		if !seen[source] {
			t.Fatalf("curated upstream %q is missing from committed registry", source)
		}
	}
}

func TestCommittedReceiptsMatchCuratedPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "research", "upstreams", "refresh-receipts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReceipts(data); err != nil {
		t.Fatalf("committed receipts violate curated upstream policy: %v", err)
	}
}

func validReceipt(id string, checkedAt time.Time) Receipt {
	hash := strings.Repeat("a", 64)
	return Receipt{
		SchemaVersion:  ReceiptSchemaVersion,
		FeedID:         id,
		SourceURL:      "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json",
		FinalURL:       "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json",
		Status:         StatusFetched,
		CheckedAt:      checkedAt,
		FetchedAt:      checkedAt,
		HTTPStatus:     200,
		ContentType:    "text/plain",
		RawSHA256:      hash,
		SemanticSHA256: hash,
	}
}

func withFeed(feed Feed, mutate func(*Feed)) Feed {
	mutate(&feed)
	return feed
}
