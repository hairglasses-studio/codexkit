package surfaceparity

import (
	"time"
)

type SweepOptions struct {
	WorkspaceRoot string `json:"workspace_root"`
	Sync          bool   `json:"sync"`
	ScopeFilter   string `json:"scope_filter,omitempty"`
	GeneratedAt   string `json:"generated_at,omitempty"`
}

type SweepReport struct {
	Timestamp        time.Time                    `json:"timestamp"`
	WorkspaceRoot    string                       `json:"workspace_root"`
	TotalRepos       int                          `json:"total_repos"`
	ManifestRepos    int                          `json:"manifest_repos"`
	UnavailableRepos map[string]string            `json:"unavailable_repos,omitempty"`
	Complete         bool                         `json:"complete"`
	CompliantRepos   int                          `json:"compliant_repos"`
	DriftedRepos     int                          `json:"drifted_repos"`
	Summary          SweepSummary                 `json:"summary"`
	Repos            map[string]*RepoSurfaceState `json:"repos"`
	DuplicateLibs    []DuplicateLibraryGroup      `json:"duplicate_libraries,omitempty"`
	BespokeScripts   []BespokeScriptEntry         `json:"bespoke_scripts,omitempty"`
	SyncActions      []string                     `json:"sync_actions,omitempty"`
}

type SweepSummary struct {
	ReposChecked        int `json:"repos_checked"`
	MissingAgentsMD     int `json:"missing_agents_md"`
	MissingClaudeMD     int `json:"missing_claude_md"`
	InvalidClaudeMirror int `json:"invalid_claude_mirror"`
	AgentsMDRuleGaps    int `json:"agents_md_rule_gaps"`
	ValidMCPConfigs     int `json:"valid_mcp_configs"`
	InvalidMCPConfigs   int `json:"invalid_mcp_configs"`
	MissingMCPConfigs   int `json:"missing_mcp_configs"`
	DuplicateLibGroups  int `json:"duplicate_lib_groups"`
	BespokeScriptCount  int `json:"bespoke_script_count"`
	OutdatedStubsCount  int `json:"outdated_stubs_count"`
}

type RepoSurfaceState struct {
	RepoName         string   `json:"repo_name"`
	RepoPath         string   `json:"repo_path"`
	Scope            string   `json:"scope,omitempty"`
	Category         string   `json:"category,omitempty"`
	Language         string   `json:"language,omitempty"`
	BaselineTarget   bool     `json:"baseline_target"`
	HasAgentsMD      bool     `json:"has_agents_md"`
	AgentsMDValid    bool     `json:"agents_md_valid"`
	AgentsMDIssues   []string `json:"agents_md_issues,omitempty"`
	HasClaudeMD      bool     `json:"has_claude_md"`
	ClaudeMDValid    bool     `json:"claude_md_valid"`
	ClaudeMDIssues   []string `json:"claude_md_issues,omitempty"`
	HasGeminiMD      bool     `json:"has_gemini_md"`
	HasMCPConfig     bool     `json:"has_mcp_config"`
	MCPValid         bool     `json:"mcp_valid"`
	MCPErrors        []string `json:"mcp_errors,omitempty"`
	MCPServers       []string `json:"mcp_servers,omitempty"`
	ScriptCount      int      `json:"script_count"`
	DuplicateScripts []string `json:"duplicate_scripts,omitempty"`
	OutdatedStubs    []string `json:"outdated_stubs,omitempty"`
	InSync           bool     `json:"in_sync"`
	FixesApplied     []string `json:"fixes_applied,omitempty"`
}

type DuplicateLibraryGroup struct {
	SHA256Hash string   `json:"sha256_hash"`
	BaseName   string   `json:"base_name"`
	Instances  []string `json:"instances"`
}

type BespokeScriptEntry struct {
	RepoName string `json:"repo_name"`
	RelPath  string `json:"rel_path"`
	Reason   string `json:"reason"`
}
