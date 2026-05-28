package llmreduction

import (
	"fmt"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

type module struct{}

// Module returns a ToolModule exposing LLM surface reduction tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "llmreduction" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	baseSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default workspace root)"},
			"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
		},
	}

	return []codexkit.ToolDef{
		{
			Name:        "llm_surface_debt_audit",
			Description: "Rank repos by LLM traversability debt using unification, primitive, and performance signals",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema:      baseSchema,
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				return BuildDebtAudit(scanPath, allScopes)
			},
		},
		{
			Name:        "llm_surface_dedup_candidates",
			Description: "List likely low-risk dedup targets from shell role classification and primitive warnings",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default workspace root)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
					"limit":      map[string]any{"type": "integer", "description": "Max candidates to return (default 25)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				limit := intFromAny(params["limit"])
				return BuildDedupCandidates(scanPath, allScopes, limit)
			},
		},
		{
			Name:        "llm_surface_reduction_plan",
			Description: "Synthesize a tranche-ready reduction plan from current debt ranking",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default workspace root)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
					"max_repos":  map[string]any{"type": "integer", "description": "Max repos in plan (default 8)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				maxRepos := intFromAny(params["max_repos"])
				return BuildReductionPlan(scanPath, allScopes, maxRepos)
			},
		},
		{
			Name:        "llm_surface_reduction_apply",
			Description: "Apply approved reduction tranches; execute defaults to false (dry-run)",
			Annotations: codexkit.ToolAnnotations(false, true, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default workspace root)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
					"max_repos":  map[string]any{"type": "integer", "description": "Max repos in plan (default 8)"},
					"execute":    map[string]any{"type": "boolean", "description": "When true, apply actions instead of dry-run"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				maxRepos := intFromAny(params["max_repos"])
				execute, _ := params["execute"].(bool)

				plan, err := BuildReductionPlan(scanPath, allScopes, maxRepos)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"execute": execute,
					"applied": false,
					"note":    "safety default: this tool currently emits actionable tranche plans and requires repo-specific executors for mutation",
					"plan":    plan,
				}, nil
			},
		},
		{
			Name:        "llm_surface_reduction_verify",
			Description: "Re-run debt audit and summarize deltas versus a provided baseline snapshot",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path":  map[string]any{"type": "string", "description": "Workspace root to scan (default workspace root)"},
					"all_scopes": map[string]any{"type": "boolean", "description": "Include inactive and compatibility-only repos"},
					"baseline": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"repo":       map[string]any{"type": "string"},
								"debt_score": map[string]any{"type": "number"},
							},
							"required": []string{"repo", "debt_score"},
						},
						"description": "Optional baseline rows from prior llm_surface_debt_audit output",
					},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					scanPath = workspace.DefaultRoot()
				}
				allScopes, _ := params["all_scopes"].(bool)
				current, err := BuildDebtAudit(scanPath, allScopes)
				if err != nil {
					return nil, err
				}

				baselineRows, _ := params["baseline"].([]any)
				if len(baselineRows) == 0 {
					return map[string]any{
						"current": current,
						"delta":   "baseline omitted",
					}, nil
				}

				baseline := map[string]float64{}
				for _, raw := range baselineRows {
					row, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					repo, _ := row["repo"].(string)
					score, ok := row["debt_score"].(float64)
					if !ok || repo == "" {
						continue
					}
					baseline[repo] = score
				}

				improved := 0
				regressed := 0
				unchanged := 0
				for _, row := range current.Repos {
					prev, ok := baseline[row.Repo]
					if !ok {
						continue
					}
					if row.DebtScore < prev {
						improved++
					} else if row.DebtScore > prev {
						regressed++
					} else {
						unchanged++
					}
				}
				return map[string]any{
					"current": current,
					"delta": map[string]any{
						"improved":  improved,
						"regressed": regressed,
						"unchanged": unchanged,
					},
				}, nil
			},
		},
	}
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func validateNoopPlanSafety(plan ReductionPlan) error {
	if len(plan.Items) == 0 {
		return fmt.Errorf("no reduction actions produced")
	}
	return nil
}
