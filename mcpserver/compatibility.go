package mcpserver

// compatibilityPayload is deliberately data, not feature detection. The
// feature contract was last reviewed against Codex 0.154.0; the installed CLI
// was observed reporting 0.163.0 on 2026-10-21.
func compatibilityPayload() map[string]any {
	return map[string]any{
		"codex_version":                  "0.154.0",
		"installed_cli_observed_version": "0.163.0",
		"models": map[string]any{
			"flagship":        map[string]any{"codex": "gpt-5.6-sol", "api_alias": "gpt-5.6"},
			"balanced_scans":  "gpt-5.6-terra",
			"narrow_low_cost": "gpt-5.6-luna",
		},
		"stable_features": []string{
			"apps",
			"goals",
			"hooks",
			"multi_agent",
			"plugins",
			"skill_search",
			"tool_suggest",
		},
		"tool_search": map[string]any{
			"available":           true,
			"feature_flag":        "removed_compatibility_noop",
			"defer_loading_owner": "openai_client_tool_or_mcp_definition",
		},
		"mcp": map[string]any{
			"active_protocol": "2025-11-25",
			"mcp_2026_07_28": map[string]any{
				"stage":   "under_development",
				"enabled": false,
			},
		},
	}
}
