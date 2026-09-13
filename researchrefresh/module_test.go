package researchrefresh

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestModuleExposesOnlyRegistryBoundReadOnlyTools(t *testing.T) {
	tools := Module().Tools()
	if len(tools) != 2 || tools[0].Name != "research_refresh" || tools[1].Name != "research_freshness" {
		t.Fatalf("unexpected research refresh tools: %#v", tools)
	}
	for _, tool := range tools {
		if tool.Annotations["readOnlyHint"] != true || tool.Annotations["destructiveHint"] != false {
			t.Fatalf("tool %s is not marked read-only: %#v", tool.Name, tool.Annotations)
		}
		properties, _ := tool.Schema["properties"].(map[string]any)
		for _, forbidden := range []string{"url", "feed", "receipt", "write", "authorization", "cookie"} {
			if _, exists := properties[forbidden]; exists {
				t.Fatalf("tool %s exposes forbidden parameter %q", tool.Name, forbidden)
			}
		}
		if tool.Schema["additionalProperties"] != false {
			t.Fatalf("tool %s does not reject additional properties", tool.Name)
		}
	}
	if tools[0].Annotations["openWorldHint"] != true {
		t.Fatal("network refresh tool is not marked open-world")
	}
	if tools[1].Annotations["openWorldHint"] != false {
		t.Fatal("offline freshness tool is marked open-world")
	}
}

func TestFreshnessSourceOmitsMissingReceiptTimes(t *testing.T) {
	encoded, err := json.Marshal(FreshnessSource{FeedID: "missing", Status: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "checked_at") || strings.Contains(string(encoded), "next_due_at") {
		t.Fatalf("missing freshness source serialized zero times: %s", encoded)
	}

	checkedAt := fixedTime()
	nextDueAt := checkedAt.Add(24 * time.Hour)
	encoded, err = json.Marshal(FreshnessSource{
		FeedID:    "fresh",
		Status:    "fresh",
		CheckedAt: &checkedAt,
		NextDueAt: &nextDueAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "checked_at") || !strings.Contains(string(encoded), "next_due_at") {
		t.Fatalf("present freshness times were omitted: %s", encoded)
	}
}

func TestResolveReadPathRejectsRootEscape(t *testing.T) {
	root := t.TempDir()
	_, err := resolveReadPath(root, "../outside.json", defaultRegistryRelativePath, false)
	if err == nil || !strings.Contains(err.Error(), "escapes harness root") {
		t.Fatalf("root escape error = %v", err)
	}
}
