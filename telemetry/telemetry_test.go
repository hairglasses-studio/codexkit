package telemetry

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "telemetry.jsonl")

	ev1 := Event{
		EventType: EventTypeRouting,
		Repo:      "hairglasses-studio/codexkit",
		Provider:  "codex",
		Model:     "gpt-5.6-sol",
		Profile:   "danger-full-access",
		Role:      "lead",
		Decision:  "selected gpt-5.6-sol for high-reasoning orchestration",
	}

	rec1, err := Record(store, ev1)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if rec1.ID == "" {
		t.Fatal("expected generated event ID")
	}
	if rec1.Timestamp == "" {
		t.Fatal("expected generated timestamp")
	}

	ev2 := Event{
		EventType:  EventTypeVerification,
		Repo:       "hairglasses-studio/codexkit",
		Provider:   "codex",
		CheckType:  "baseline",
		Status:     "pass",
		DurationMS: 124.5,
		Findings:   0,
	}

	_, err = Record(store, ev2)
	if err != nil {
		t.Fatalf("Record ev2 failed: %v", err)
	}

	// Query all
	all, err := Query(store, Filter{})
	if err != nil {
		t.Fatalf("Query all failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 events, got %d", len(all))
	}

	// First event in reverse order should be ev2
	if all[0].EventType != EventTypeVerification {
		t.Errorf("expected first event in result to be verification, got %s", all[0].EventType)
	}

	// Filter by event type
	routingEvents, err := Query(store, Filter{EventType: EventTypeRouting})
	if err != nil {
		t.Fatalf("Query routing failed: %v", err)
	}
	if len(routingEvents) != 1 {
		t.Fatalf("expected 1 routing event, got %d", len(routingEvents))
	}
	if routingEvents[0].Model != "gpt-5.6-sol" {
		t.Errorf("expected model gpt-5.6-sol, got %s", routingEvents[0].Model)
	}

	// Filter by status
	passEvents, err := Query(store, Filter{Status: "pass"})
	if err != nil {
		t.Fatalf("Query pass failed: %v", err)
	}
	if len(passEvents) != 1 {
		t.Fatalf("expected 1 pass event, got %d", len(passEvents))
	}
}

func TestSearch(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "telemetry.jsonl")

	events := []Event{
		{
			EventType: EventTypeRouting,
			Repo:      "ralphglasses",
			Model:     "claude-3-7-sonnet",
			Decision:  "fall back to claude for interactive session",
		},
		{
			EventType: EventTypeModelSelection,
			Repo:      "jobb",
			Model:     "gemini-3.1-pro",
			Decision:  "chose gemini pro for high-volume background extraction",
		},
		{
			EventType: EventTypeVerification,
			Repo:      "secretstudios-mcp",
			CheckType: "opnsense_router_gate",
			Status:    "fail",
			Error:     "gateway timeout on 192.168.1.1",
		},
	}

	for _, ev := range events {
		if _, err := Record(store, ev); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	res, err := Search(store, "fall back", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 1 || res[0].Repo != "ralphglasses" {
		t.Fatalf("expected 1 match in ralphglasses, got %+v", res)
	}

	res, err = Search(store, "gateway timeout", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 1 || res[0].Repo != "secretstudios-mcp" {
		t.Fatalf("expected 1 match in secretstudios-mcp, got %+v", res)
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "telemetry.jsonl")

	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, err := Record(store, Event{
					EventType: EventTypeRouting,
					Repo:      "codexkit",
					Model:     "gpt-5.6-sol",
					Role:      "worker",
					Metadata:  map[string]any{"worker": workerID, "iter": j},
				})
				if err != nil {
					t.Errorf("worker %d failed to record: %v", workerID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	all, err := Query(store, Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	expected := workers * iterations
	if len(all) != expected {
		t.Fatalf("expected %d events, got %d", expected, len(all))
	}
}

func TestModuleTools(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "telemetry.jsonl")

	mod := Module()
	if mod.Name() != "telemetry" {
		t.Fatalf("expected module name telemetry, got %s", mod.Name())
	}
	if err := mod.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tools := mod.Tools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	// Test telemetry_record tool
	recordTool := tools[0]
	resp, err := recordTool.Handler(map[string]any{
		"store_path": store,
		"event_type": "routing",
		"repo":       "codexkit",
		"model":      "gpt-5.6-sol",
		"decision":   "test record via mcp handler",
	})
	if err != nil {
		t.Fatalf("telemetry_record handler failed: %v", err)
	}
	respMap, ok := resp.(map[string]any)
	if !ok || respMap["success"] != true {
		t.Fatalf("expected success true in response, got %+v", resp)
	}

	// Test telemetry_query tool
	queryTool := tools[1]
	qResp, err := queryTool.Handler(map[string]any{
		"store_path": store,
		"event_type": "routing",
	})
	if err != nil {
		t.Fatalf("telemetry_query handler failed: %v", err)
	}
	qMap, ok := qResp.(map[string]any)
	if !ok || qMap["count"].(int) != 1 {
		t.Fatalf("expected 1 event in query response, got %+v", qResp)
	}

	// Test telemetry_search tool
	searchTool := tools[2]
	sResp, err := searchTool.Handler(map[string]any{
		"store_path": store,
		"query":      "mcp handler",
	})
	if err != nil {
		t.Fatalf("telemetry_search handler failed: %v", err)
	}
	sMap, ok := sResp.(map[string]any)
	if !ok || sMap["count"].(int) != 1 {
		t.Fatalf("expected 1 event in search response, got %+v", sResp)
	}
}

func TestSQLiteStoreRecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.sqlite")

	ev1 := Event{
		EventType: EventTypeRouting,
		Repo:      "hairglasses-studio/codexkit",
		Provider:  "codex",
		Model:     "gpt-5.6-sol",
		Profile:   "danger-full-access",
		Role:      "lead",
		Decision:  "selected gpt-5.6-sol for high-reasoning orchestration",
	}

	rec1, err := Record(dbPath, ev1)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if rec1.ID == "" {
		t.Fatal("expected generated event ID")
	}
	if rec1.Timestamp == "" {
		t.Fatal("expected generated timestamp")
	}

	ev2 := Event{
		EventType:  EventTypeVerification,
		Repo:       "hairglasses-studio/codexkit",
		Provider:   "codex",
		CheckType:  "baseline",
		Status:     "pass",
		DurationMS: 124.5,
		Findings:   0,
	}

	_, err = Record(dbPath, ev2)
	if err != nil {
		t.Fatalf("Record ev2 failed: %v", err)
	}

	all, err := Query(dbPath, Filter{})
	if err != nil {
		t.Fatalf("Query all failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 events, got %d", len(all))
	}

	if all[0].EventType != EventTypeVerification {
		t.Errorf("expected first event in result to be verification, got %s", all[0].EventType)
	}

	routingEvents, err := Query(dbPath, Filter{EventType: EventTypeRouting})
	if err != nil {
		t.Fatalf("Query routing failed: %v", err)
	}
	if len(routingEvents) != 1 {
		t.Fatalf("expected 1 routing event, got %d", len(routingEvents))
	}
	if routingEvents[0].Model != "gpt-5.6-sol" {
		t.Errorf("expected model gpt-5.6-sol, got %s", routingEvents[0].Model)
	}

	passEvents, err := Query(dbPath, Filter{Status: "pass"})
	if err != nil {
		t.Fatalf("Query pass failed: %v", err)
	}
	if len(passEvents) != 1 {
		t.Fatalf("expected 1 pass event, got %d", len(passEvents))
	}
}

func TestSQLiteStoreSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry.sqlite")

	events := []Event{
		{
			EventType: EventTypeRouting,
			Repo:      "ralphglasses",
			Model:     "claude-3-7-sonnet",
			Decision:  "fall back to claude for interactive session",
		},
		{
			EventType: EventTypeModelSelection,
			Repo:      "jobb",
			Model:     "gemini-3.1-pro",
			Decision:  "chose gemini pro for high-volume background extraction",
		},
		{
			EventType: EventTypeVerification,
			Repo:      "secretstudios-mcp",
			CheckType: "opnsense_router_gate",
			Status:    "fail",
			Error:     "gateway timeout on 192.168.1.1",
		},
	}

	for _, ev := range events {
		if _, err := Record(dbPath, ev); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
	}

	res, err := Search(dbPath, "fall back", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 1 || res[0].Repo != "ralphglasses" {
		t.Fatalf("expected 1 match in ralphglasses, got %+v", res)
	}

	res, err = Search(dbPath, "gateway timeout", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 1 || res[0].Repo != "secretstudios-mcp" {
		t.Fatalf("expected 1 match in secretstudios-mcp, got %+v", res)
	}
}

func TestSQLiteStoreConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telemetry_concurrent.sqlite")

	var wg sync.WaitGroup
	workers := 8
	iterations := 25

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, err := Record(dbPath, Event{
					EventType: EventTypeRouting,
					Repo:      "codexkit",
					Model:     "gpt-5.6-sol",
					Role:      "worker",
					Metadata:  map[string]any{"worker": workerID, "iter": j},
				})
				if err != nil {
					t.Errorf("worker %d failed to record: %v", workerID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	all, err := Query(dbPath, Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	expected := workers * iterations
	if len(all) != expected {
		t.Fatalf("expected %d events, got %d", expected, len(all))
	}
}

func TestRecordToolMetadataSchemaAndCustomKeys(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "telemetry.jsonl")

	tools := Module().Tools()
	recordTool := tools[0]
	if recordTool.Name != "telemetry_record" {
		t.Fatalf("expected telemetry_record first, got %s", recordTool.Name)
	}
	props, _ := recordTool.Schema["properties"].(map[string]any)
	metaSchema, ok := props["metadata"].(map[string]any)
	if !ok || metaSchema["type"] != "object" {
		t.Fatalf("expected object metadata schema, got %+v", props["metadata"])
	}
	metaProps, ok := metaSchema["properties"].(map[string]any)
	if !ok || len(metaProps) == 0 {
		t.Fatalf("expected non-empty metadata.properties, got %+v", metaSchema)
	}
	for _, key := range []string{"worker", "iter", "task_id", "lane", "reason"} {
		if _, exists := metaProps[key]; !exists {
			t.Fatalf("missing metadata property %q in %+v", key, metaProps)
		}
	}
	if _, forbidden := metaSchema["additionalProperties"]; forbidden {
		t.Fatalf("metadata schema must allow custom keys, got %+v", metaSchema)
	}

	if _, err := recordTool.Handler(map[string]any{
		"store_path": store,
		"repo":       "codexkit",
	}); err == nil {
		t.Fatal("expected error when event_type is omitted")
	}

	if _, err := recordTool.Handler(map[string]any{
		"store_path": store,
		"event_type": "routing",
		"repo":       "codexkit",
		"metadata": map[string]any{
			"worker":     "w1",
			"iter":       2,
			"task_id":    "L-codexkit-zero-advisory",
			"custom_key": "custom_value",
		},
	}); err != nil {
		t.Fatalf("telemetry_record with documented and custom metadata failed: %v", err)
	}

	events, err := Query(store, Filter{EventType: "routing"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(events) != 1 || events[0].Metadata["worker"] != "w1" || events[0].Metadata["custom_key"] != "custom_value" {
		t.Fatalf("unexpected persisted metadata: %+v", events)
	}
}
