package telemetry

import (
	"fmt"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

// Module returns the telemetry ToolModule.
func Module() codexkit.ToolModule {
	return &module{}
}

func (m *module) Name() string {
	return "telemetry"
}

func (m *module) Init() error {
	return nil
}

type RecordParams struct {
	StorePath  string         `json:"store_path,omitempty"`
	EventType  string         `json:"event_type"`
	Repo       string         `json:"repo,omitempty"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Role       string         `json:"role,omitempty"`
	Decision   string         `json:"decision,omitempty"`
	CheckType  string         `json:"check_type,omitempty"`
	Status     string         `json:"status,omitempty"`
	DurationMS float64        `json:"duration_ms,omitempty"`
	Findings   int            `json:"findings,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type QueryParams struct {
	StorePath string `json:"store_path,omitempty"`
	EventType string `json:"event_type,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Profile   string `json:"profile,omitempty"`
	Status    string `json:"status,omitempty"`
	Since     string `json:"since,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type SearchParams struct {
	StorePath string `json:"store_path,omitempty"`
	Query     string `json:"query"`
	Limit     int    `json:"limit,omitempty"`
}

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "telemetry_record",
			Description: "Record a Codex routing decision, model/profile selection, or verification outcome in the searchable telemetry store.",
			Annotations: codexkit.ToolAnnotations(false, false, false, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"store_path":  map[string]any{"type": "string", "description": "Optional path to the telemetry JSONL store file."},
					"event_type":  map[string]any{"type": "string", "description": "Event type: 'routing', 'model_selection', or 'verification'.", "enum": []string{EventTypeRouting, EventTypeModelSelection, EventTypeVerification}},
					"repo":        map[string]any{"type": "string", "description": "Repository path or name."},
					"provider":    map[string]any{"type": "string", "description": "Provider name (e.g. 'codex', 'claude', 'agy')."},
					"model":       map[string]any{"type": "string", "description": "Model identifier or alias (e.g. 'gpt-5.6-sol')."},
					"profile":     map[string]any{"type": "string", "description": "Profile name (e.g. 'marathon_full_auto')."},
					"role":        map[string]any{"type": "string", "description": "Agent role (e.g. 'lead', 'planner_1', 'implementer_1')."},
					"decision":    map[string]any{"type": "string", "description": "Routing decision or selection rationale."},
					"check_type":  map[string]any{"type": "string", "description": "Check type for verification events."},
					"status":      map[string]any{"type": "string", "description": "Outcome status: 'pass', 'fail', or 'warn'."},
					"duration_ms": map[string]any{"type": "number", "description": "Duration in milliseconds."},
					"findings":    map[string]any{"type": "integer", "description": "Number of issues/findings detected."},
					"error":       map[string]any{"type": "string", "description": "Error message if failed."},
					"metadata":    map[string]any{"type": "object", "description": "Arbitrary key-value metadata."},
				},
				"required": []string{"event_type"},
			},
			Handler: codexkit.TypedHandler(func(p RecordParams) (any, error) {
				if p.EventType == "" {
					return nil, fmt.Errorf("event_type is required")
				}
				ev := Event{
					EventType:  p.EventType,
					Repo:       p.Repo,
					Provider:   p.Provider,
					Model:      p.Model,
					Profile:    p.Profile,
					Role:       p.Role,
					Decision:   p.Decision,
					CheckType:  p.CheckType,
					Status:     p.Status,
					DurationMS: p.DurationMS,
					Findings:   p.Findings,
					Error:      p.Error,
					Metadata:   p.Metadata,
				}
				rec, err := Record(p.StorePath, ev)
				if err != nil {
					return nil, err
				}
				return map[string]any{"success": true, "event": rec}, nil
			}),
		},
		{
			Name:        "telemetry_query",
			Description: "Query recorded Codex telemetry events filtered by type, repo, provider, model, profile, status, or timeframe.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"store_path": map[string]any{"type": "string", "description": "Optional path to the telemetry JSONL store file."},
					"event_type": map[string]any{"type": "string", "description": "Filter by event type: 'routing', 'model_selection', or 'verification'."},
					"repo":       map[string]any{"type": "string", "description": "Filter by repository name or path."},
					"provider":   map[string]any{"type": "string", "description": "Filter by provider name."},
					"model":      map[string]any{"type": "string", "description": "Filter by model identifier."},
					"profile":    map[string]any{"type": "string", "description": "Filter by profile name."},
					"status":     map[string]any{"type": "string", "description": "Filter by outcome status (e.g. 'pass', 'fail')."},
					"since":      map[string]any{"type": "string", "description": "Filter events since RFC3339 timestamp or duration (e.g. '1h', '24h', '7d')."},
					"limit":      map[string]any{"type": "integer", "description": "Maximum number of events to return (default 50)."},
				},
			},
			Handler: codexkit.TypedHandler(func(p QueryParams) (any, error) {
				var sinceTime time.Time
				if p.Since != "" {
					if dur, err := time.ParseDuration(p.Since); err == nil {
						sinceTime = time.Now().Add(-dur)
					} else if t, err := time.Parse(time.RFC3339Nano, p.Since); err == nil {
						sinceTime = t
					} else if t, err := time.Parse(time.RFC3339, p.Since); err == nil {
						sinceTime = t
					}
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				filter := Filter{
					EventType: p.EventType,
					Repo:      p.Repo,
					Provider:  p.Provider,
					Model:     p.Model,
					Profile:   p.Profile,
					Status:    p.Status,
					Since:     sinceTime,
					Limit:     limit,
				}
				events, err := Query(p.StorePath, filter)
				if err != nil {
					return nil, err
				}
				return map[string]any{"count": len(events), "events": events}, nil
			}),
		},
		{
			Name:        "telemetry_search",
			Description: "Search recorded Codex telemetry events across all fields with keyword substring matching.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"store_path": map[string]any{"type": "string", "description": "Optional path to the telemetry JSONL store file."},
					"query":      map[string]any{"type": "string", "description": "Search query string."},
					"limit":      map[string]any{"type": "integer", "description": "Maximum number of events to return (default 50)."},
				},
				"required": []string{"query"},
			},
			Handler: codexkit.TypedHandler(func(p SearchParams) (any, error) {
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				events, err := Search(p.StorePath, p.Query, limit)
				if err != nil {
					return nil, err
				}
				return map[string]any{"query": p.Query, "count": len(events), "events": events}, nil
			}),
		},
	}
}
