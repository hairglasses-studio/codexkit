// Package baselineguard validates Codex repo baseline requirements.
//
// It checks for canonical instruction patterns, required files, Codex config,
// skill surface validity, agent naming conventions, skill sync drift,
// MCP config drift, and protocol compliance (A2A, MCP discovery).
package baselineguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/workspace"
	toml "github.com/pelletier/go-toml/v2"
)

// nowFunc is the injectable clock used by lifecycle/deprecation checks.
// Tests override it to simulate specific dates without touching the system clock.
var nowFunc = time.Now

// Finding represents a single validation result.
type Finding struct {
	Check       string        `json:"check"`
	Passed      bool          `json:"passed"`
	Message     string        `json:"message,omitempty"`
	Remediation []Remediation `json:"remediation,omitempty"`
}

// Remediation describes a concrete recovery action for a failed finding.
type Remediation struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	Command []string `json:"command,omitempty"`
}

// Report is the full baseline-guard result for a repo.
type Report struct {
	RepoPath string    `json:"repo_path"`
	Passed   bool      `json:"passed"`
	Total    int       `json:"total"`
	Failed   int       `json:"failed"`
	Findings []Finding `json:"findings"`
}

// RequiredFiles lists the files that must exist for baseline compliance.
var RequiredFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".claude/settings.json",
	".codex/config.toml",
}

// RetiredProviderMirrors lists provider projection files retired fleet-wide by
// the 2026-08-01 provider-retirement wave. AGENTS.md plus a thin CLAUDE.md
// mirror is the current contract; AGY and Codex read AGENTS.md directly. These
// paths must be ABSENT for baseline compliance — presence means a stale mirror
// was never cleaned up.
var RetiredProviderMirrors = []string{
	"GEMINI.md",
	".gemini/settings.json",
	".github/copilot-instructions.md",
	".clinerules",
}

// PortableFrontmatterKeys re-exports the canonical source-key set from the top-level package.
var PortableFrontmatterKeys = codexkit.SkillSourceFrontmatterKeys

var (
	canonicalAgentsRe = regexp.MustCompile(`(?m)^> Canonical instructions: AGENTS\.md`)
	canonicalClaudeRe = regexp.MustCompile(`This repo(sitory)? uses (\*\*)?\[AGENTS\.md\]\(AGENTS\.md\)(\*\*)? as (the|its) canonical instruction file`)
	profileRe         = regexp.MustCompile(`(?m)^\[profiles\.(\w+)\]`)
	dashInFilename    = regexp.MustCompile(`-`)
)

// Check runs all baseline-guard validations on the given repo path.
func Check(repoPath string) Report {
	report := Report{RepoPath: repoPath}

	report.addRequiredFiles(repoPath)
	report.addRetiredProviderMirrors(repoPath)
	report.addCanonicalPatterns(repoPath)
	report.addProviderSettings(repoPath)
	report.addAGYAgentLayout(repoPath)
	report.addProfiles(repoPath)
	report.addAgentNaming(repoPath)
	report.addSkillSurface(repoPath)
	report.addSkillSyncCheck(repoPath)
	report.addSyncWrapperPortability(repoPath)
	report.addMCPSyncCheck(repoPath)
	report.addMCPLauncherPortability(repoPath)
	report.addMCPDiscovery(repoPath)
	report.addA2AAwareness(repoPath)
	report.addSkillPortability(repoPath)
	report.addModelPinFreshness(repoPath)
	report.addMCPSpecTarget(repoPath)
	report.addConfigDrift(repoPath)

	report.addRemediations(repoPath)
	report.Total = len(report.Findings)
	for _, f := range report.Findings {
		if !f.Passed {
			report.Failed++
		}
	}
	report.Passed = report.Failed == 0
	return report
}

// DiscoverWorkspaceTargets returns the active/baseline repo paths that should
// participate in fleet baseline checks. Compatibility/reference repos remain
// visible to workspace checks, but they should not fail the active baseline.
func DiscoverWorkspaceTargets(scanPath string) ([]string, error) {
	return DiscoverWorkspaceTargetsWithOverlay(scanPath, "")
}

// DiscoverWorkspaceTargetsWithOverlay is DiscoverWorkspaceTargets with an
// optional --overlay file applied to the manifest before repo paths are
// derived, so a relocated checkout (e.g. a managed worktree) is what gets
// baseline-checked instead of the default root/<name> location.
func DiscoverWorkspaceTargetsWithOverlay(scanPath, overlayPath string) ([]string, error) {
	scanPath = filepath.Clean(scanPath)
	manifest, err := workspace.LoadManifestWithOverlay(scanPath, overlayPath)
	if err == nil {
		paths := make([]string, 0, len(manifest.Repos))
		for _, repo := range manifest.Repos {
			// baseline_target:false always excludes a repo, regardless of
			// scope — a repo scoped active_* (e.g. a compat mirror or
			// practice repo) is not implicitly swept back in. This mirrors
			// Manifest.Filter's BaselineOnly gate (workspace/manifest.go).
			if !repo.BaselineTarget {
				continue
			}
			repoPath := workspace.RepoPath(scanPath, repo)
			if isGitRepoPath(repoPath) {
				paths = append(paths, repoPath)
			}
		}
		return paths, nil
	}

	entries, readErr := os.ReadDir(scanPath)
	if readErr != nil {
		return nil, fmt.Errorf("reading %s: %w", scanPath, readErr)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || shouldSkipWorkspaceChild(entry.Name()) {
			continue
		}
		repoPath := filepath.Join(scanPath, entry.Name())
		if isGitRepoPath(repoPath) {
			paths = append(paths, repoPath)
		}
	}
	return paths, nil
}

func isGitRepoPath(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func shouldSkipWorkspaceChild(name string) bool {
	switch name {
	case ".graveyard", ".codex-worktrees", ".claude", ".config", ".ralph", "vault":
		return true
	default:
		return false
	}
}

func (r *Report) add(check string, passed bool, msg string) {
	r.Findings = append(r.Findings, Finding{Check: check, Passed: passed, Message: msg})
}

func (r *Report) addRemediations(repoPath string) {
	for i := range r.Findings {
		if r.Findings[i].Passed {
			continue
		}
		r.Findings[i].Remediation = remediationForCheck(repoPath, r.Findings[i].Check)
	}
}

func remediationForCheck(repoPath, check string) []Remediation {
	command := func(args ...string) []string {
		return append([]string{"codexkit"}, args...)
	}
	switch check {
	case "skill_sync":
		return []Remediation{{
			Kind:    "generator",
			Message: "regenerate skill mirrors",
			Command: command("skills", "sync", repoPath, "--quiet-warnings"),
		}}
	case "mcp_sync":
		return []Remediation{{
			Kind:    "generator",
			Message: "regenerate MCP config",
			Command: command("mcp", "sync", repoPath),
		}}
	case "claude_settings_json", "agy_hooks_json":
		return []Remediation{{
			Kind:    "edit",
			Message: "repair the provider-native JSON configuration",
		}}
	case "required_file", "canonical_agents", "canonical_claude":
		return []Remediation{{
			Kind:    "edit",
			Message: "restore canonical instruction files: AGENTS.md and CLAUDE.md",
		}}
	case "retired_provider_mirror":
		return []Remediation{{
			Kind:    "edit",
			Message: "remove retired provider mirror files (GEMINI.md, .gemini/settings.json, .github/copilot-instructions.md, .clinerules)",
		}}
	case "agy_agent_layout":
		return []Remediation{{
			Kind:    "edit",
			Message: "move AGY agents to .agents/agents/<name>/agent.md",
		}}
	case "codex_config_toml", "project_local_profiles":
		return []Remediation{{
			Kind:    "edit",
			Message: "edit .codex/config.toml; keep repo-local configs parseable and remove unsupported [profiles.*] tables",
		}}
	case "agent_naming":
		return []Remediation{{
			Kind:    "edit",
			Message: "rename .codex/agents/*.toml files to underscore_case",
		}}
	case "skill_surface", "skill_file", "skill_portability":
		return []Remediation{{
			Kind:    "edit",
			Message: "fix the canonical .agents/skills surface before regenerating provider mirrors",
		}}
	case "sync_wrapper_portability":
		return []Remediation{{
			Kind:    "edit",
			Message: "make repo-local sync wrappers path-stable and run Go helpers with GOWORK=off",
		}}
	case "mcp_portability":
		return []Remediation{{
			Kind:    "edit",
			Message: "make active .mcp.json and generated Codex MCP launchers use portable absolute commands and cwd values",
		}}
	case "mcp_discovery":
		return []Remediation{{
			Kind:    "edit",
			Message: "publish or update .well-known/mcp.json for active HTTP MCP servers",
		}}
	case "a2a_awareness":
		return []Remediation{{
			Kind:    "edit",
			Message: "fix .well-known/agent.json so the Agent2Agent metadata is valid",
		}}
	case "model_pin_freshness":
		return []Remediation{{
			Kind:    "edit",
			Message: "migrate the pinned model id to its replacement before the deprecation/retirement date",
		}}
	case "mcp_spec_target":
		return []Remediation{{
			Kind:    "edit",
			Message: "update .well-known/mcp.json protocolVersion to the fleet target/current MCP spec version",
		}}
	case "config_drift":
		return []Remediation{{
			Kind:    "edit",
			Message: "restore the provider configuration or launcher contract declared in workspace/config-expectations.json",
		}}
	default:
		return nil
	}
}

func (r *Report) addRequiredFiles(repoPath string) {
	for _, name := range RequiredFiles {
		path := filepath.Join(repoPath, name)
		if _, err := os.Stat(path); err != nil {
			r.add("required_file", false, fmt.Sprintf("missing: %s", name))
		} else {
			r.add("required_file", true, name)
		}
	}
}

func (r *Report) addRetiredProviderMirrors(repoPath string) {
	for _, name := range RetiredProviderMirrors {
		path := filepath.Join(repoPath, name)
		if _, err := os.Lstat(path); err == nil {
			r.add("retired_provider_mirror", false, fmt.Sprintf("retired provider mirror present — remove it: %s", name))
		} else {
			r.add("retired_provider_mirror", true, fmt.Sprintf("absent: %s", name))
		}
	}
}

func (r *Report) addCanonicalPatterns(repoPath string) {
	// AGENTS.md: must have "> Canonical instructions: AGENTS.md"
	if data, err := os.ReadFile(filepath.Join(repoPath, "AGENTS.md")); err == nil {
		if canonicalAgentsRe.Match(data) {
			r.add("canonical_agents", true, "")
		} else {
			r.add("canonical_agents", false, "AGENTS.md missing '> Canonical instructions: AGENTS.md'")
		}
	}

	// CLAUDE.md: must reference AGENTS.md
	if data, err := os.ReadFile(filepath.Join(repoPath, "CLAUDE.md")); err == nil {
		if canonicalClaudeRe.Match(data) {
			r.add("canonical_claude", true, "")
		} else {
			r.add("canonical_claude", false, "CLAUDE.md missing canonical AGENTS.md reference")
		}
	}

}

func (r *Report) addProviderSettings(repoPath string) {
	if data, err := os.ReadFile(filepath.Join(repoPath, ".claude/settings.json")); err == nil {
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			r.add("claude_settings_json", false, ".claude/settings.json must be valid JSON")
		} else {
			r.add("claude_settings_json", true, "")
		}
	}

	if data, err := os.ReadFile(filepath.Join(repoPath, ".agents/hooks.json")); err == nil {
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			r.add("agy_hooks_json", false, ".agents/hooks.json must be valid JSON")
		} else {
			r.add("agy_hooks_json", true, "")
		}
	}
}

func (r *Report) addAGYAgentLayout(repoPath string) {
	agentsDir := filepath.Join(repoPath, ".agents", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			r.add("agy_agent_layout", false, fmt.Sprintf("%s must be a directory containing agent.md", entry.Name()))
			continue
		}
		agentPath := filepath.Join(agentsDir, entry.Name(), "agent.md")
		if info, statErr := os.Stat(agentPath); statErr != nil || info.IsDir() {
			r.add("agy_agent_layout", false, fmt.Sprintf("missing: .agents/agents/%s/agent.md", entry.Name()))
			continue
		}
		r.add("agy_agent_layout", true, entry.Name())
	}
}

func (r *Report) addProfiles(repoPath string) {
	data, err := os.ReadFile(filepath.Join(repoPath, ".codex/config.toml"))
	if err != nil {
		return // already covered by required_file check
	}
	if err := validateCodexConfigTOML(data); err != nil {
		r.add("codex_config_toml", false, fmt.Sprintf(".codex/config.toml must be valid TOML: %v", err))
		return
	}
	r.add("codex_config_toml", true, "")
	matches := profileRe.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		r.add("project_local_profiles", true, ".codex/config.toml has no unsupported project-local profiles")
		return
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	r.add("project_local_profiles", false, fmt.Sprintf("unsupported project-local profiles in .codex/config.toml: %s", strings.Join(names, ", ")))
}

func validateCodexConfigTOML(data []byte) error {
	var parsed map[string]any
	return toml.Unmarshal(data, &parsed)
}

func (r *Report) addAgentNaming(repoPath string) {
	agentsDir := filepath.Join(repoPath, ".codex/agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return // no agents dir is fine
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		base := strings.TrimSuffix(name, ".toml")
		if dashInFilename.MatchString(base) {
			r.add("agent_naming", false, fmt.Sprintf("%s uses dash-case (must be underscore_case)", name))
		} else {
			r.add("agent_naming", true, name)
		}
	}
}

func (r *Report) addSkillSyncCheck(repoPath string) {
	if _, err := os.Stat(filepath.Join(repoPath, ".agents", "skills")); err != nil {
		return
	}
	report := skillsync.Check(repoPath)
	for _, err := range report.Errors {
		r.add("skill_sync", false, err)
	}
	for _, action := range report.Actions {
		switch action.Action {
		case "create", "update", "remove":
			r.add("skill_sync", false, action.Message)
		case "unchanged":
			r.add("skill_sync", true, action.Message)
		}
	}
}

func (r *Report) addSyncWrapperPortability(repoPath string) {
	for _, relPath := range []string{
		filepath.Join("scripts", "hg-skill-surface-sync.sh"),
		filepath.Join("scripts", "codex-mcp-sync.sh"),
	} {
		path := filepath.Join(repoPath, relPath)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		content := string(data)
		switch {
		case strings.Contains(content, "git rev-parse --show-toplevel"):
			r.add("sync_wrapper_portability", false, fmt.Sprintf("%s depends on caller cwd via git rev-parse --show-toplevel", relPath))
		case strings.Contains(content, "go run ./cmd/") && !strings.Contains(content, "GOWORK=off"):
			r.add("sync_wrapper_portability", false, fmt.Sprintf("%s runs go run ./cmd/... without GOWORK=off", relPath))
		default:
			r.add("sync_wrapper_portability", true, relPath)
		}
	}
}

func (r *Report) addMCPSyncCheck(repoPath string) {
	if _, err := os.Stat(filepath.Join(repoPath, ".mcp.json")); err != nil {
		return
	}
	report := mcpsync.Diff(repoPath)
	for _, err := range report.Errors {
		r.add("mcp_sync", false, err)
	}
	for _, action := range report.Actions {
		switch action.Action {
		case "update", "create", "remove":
			r.add("mcp_sync", false, action.Message)
		case "unchanged":
			r.add("mcp_sync", true, action.Message)
		}
	}
}

func (r *Report) addMCPLauncherPortability(repoPath string) {
	mcpPath := filepath.Join(repoPath, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return
	}
	var mcpFile struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			CWD     string   `json:"cwd"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcpFile); err != nil {
		return
	}
	if len(mcpFile.MCPServers) == 0 {
		r.add("mcp_portability", true, "no MCP servers defined")
	} else {
		for name, server := range mcpFile.MCPServers {
			if strings.HasPrefix(name, "_") {
				continue
			}
			if msg := validateMCPServerPortability(server.Command, server.Args, server.CWD); msg != "" {
				r.add("mcp_portability", false, fmt.Sprintf("%s: %s", name, msg))
			} else {
				r.add("mcp_portability", true, name)
			}
		}
	}

	configData, err := os.ReadFile(filepath.Join(repoPath, ".codex/config.toml"))
	if err != nil {
		return
	}
	configStr := string(configData)
	switch {
	case strings.Contains(configStr, `cwd = "."`) || strings.Contains(configStr, `cwd = "./"`):
		r.add("mcp_portability", false, ".codex/config.toml contains repo-relative cwd in generated MCP blocks")
	case strings.Contains(configStr, "go run ./cmd/"):
		r.add("mcp_portability", false, ".codex/config.toml contains direct go run ./cmd/... launch strings")
	case strings.Contains(configStr, "cd ") && strings.Contains(configStr, "&& go run ./cmd/"):
		r.add("mcp_portability", false, ".codex/config.toml contains inline cd && go run MCP launch strings")
	}
}

func validateMCPServerPortability(command string, args []string, cwd string) string {
	if cwd == "." || cwd == "./" {
		return "uses cwd = .; use a portable launcher instead"
	}
	if strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../") {
		return fmt.Sprintf("uses repo-relative command %s", command)
	}
	if (command == "go" || strings.HasSuffix(command, "/go")) && len(args) > 1 && args[0] == "run" && strings.HasPrefix(args[1], "./cmd/") {
		return "uses direct go run ./cmd/...; use a portable launcher script"
	}
	switch command {
	case "bash", "sh", "zsh":
		if len(args) > 0 && (strings.HasPrefix(args[0], "./") || strings.HasPrefix(args[0], "../")) {
			return fmt.Sprintf("uses repo-relative shell script path %s", args[0])
		}
	case "/bin/bash", "/bin/sh", "/bin/zsh":
		if len(args) > 0 && (strings.HasPrefix(args[0], "./") || strings.HasPrefix(args[0], "../")) {
			return fmt.Sprintf("uses repo-relative shell script path %s", args[0])
		}
	}
	for _, arg := range args {
		if msg := validateMCPShellSnippet(arg); msg != "" {
			return msg
		}
	}
	return ""
}

func validateMCPShellSnippet(snippet string) string {
	if strings.Contains(snippet, "go run ./cmd/") {
		return "uses inline go run ./cmd/...; move the launch into a portable wrapper script"
	}
	if strings.Contains(snippet, "cd ") && strings.Contains(snippet, "&&") {
		return "uses inline cd ... && ...; move repo-root resolution into a wrapper script"
	}
	return ""
}

func (r *Report) addMCPDiscovery(repoPath string) {
	// Check if HTTP MCP servers are defined and .well-known/mcp.json exists
	mcpPath := filepath.Join(repoPath, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return
	}
	var mcpFile struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcpFile); err != nil {
		return
	}
	hasHTTP := false
	for _, raw := range mcpFile.MCPServers {
		var server struct {
			Transport string `json:"transport"`
			URL       string `json:"url"`
		}
		if json.Unmarshal(raw, &server) == nil && (server.Transport == "http" || server.Transport == "sse" || server.URL != "") {
			hasHTTP = true
			break
		}
	}
	if !hasHTTP {
		return // only relevant for HTTP servers
	}
	wellKnown := filepath.Join(repoPath, ".well-known/mcp.json")
	if _, err := os.Stat(wellKnown); err != nil {
		r.add("mcp_discovery", false, "HTTP MCP servers defined but .well-known/mcp.json missing")
	} else {
		r.add("mcp_discovery", true, ".well-known/mcp.json present")
	}
}

func (r *Report) addA2AAwareness(repoPath string) {
	agentJSON := filepath.Join(repoPath, ".well-known/agent.json")
	if _, err := os.Stat(agentJSON); err != nil {
		r.add("a2a_awareness", true, "no .well-known/agent.json (optional)")
	} else {
		// Validate it's parseable JSON
		data, err := os.ReadFile(agentJSON)
		if err != nil {
			r.add("a2a_awareness", false, "cannot read .well-known/agent.json")
			return
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			r.add("a2a_awareness", false, fmt.Sprintf("invalid .well-known/agent.json: %v", err))
		} else {
			r.add("a2a_awareness", true, ".well-known/agent.json valid")
		}
	}
}

func (r *Report) addSkillPortability(repoPath string) {
	skillsDir := filepath.Join(repoPath, ".agents/skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.HasPrefix(content, "---\n") {
			continue // no frontmatter
		}
		endIdx := strings.Index(content[4:], "\n---")
		if endIdx < 0 {
			continue
		}
		frontmatter := content[4 : 4+endIdx]
		nonPortable := []string{}
		inBlock := false
		inList := false
		for _, line := range strings.Split(frontmatter, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if inBlock {
				if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
					continue
				}
				inBlock = false
			}
			if inList {
				if strings.HasPrefix(trimmed, "- ") {
					continue
				}
				inList = false
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) < 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			if !codexkit.SkillSourceFrontmatterKeys[key] {
				nonPortable = append(nonPortable, key)
			}
			value := strings.TrimSpace(parts[1])
			inBlock = value == "|" || value == "|-" || value == "|+" || value == ">" || value == ">-" || value == ">+"
			inList = value == "" && (key == "allowed-tools" || key == "triggers" || key == "see_also")
		}
		if len(nonPortable) > 0 {
			r.add("skill_portability", false, fmt.Sprintf("%s: non-portable keys: %s", entry.Name(), strings.Join(nonPortable, ", ")))
		} else {
			r.add("skill_portability", true, entry.Name())
		}
	}
}

func (r *Report) addSkillSurface(repoPath string) {
	surfacePath := filepath.Join(repoPath, ".agents/skills/surface.yaml")
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		r.add("skill_surface", true, "no surface.yaml (optional)")
		return
	}

	// Accept both JSON and simple YAML formats
	var surface struct {
		Version int `json:"version"`
		Skills  []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &surface); err != nil {
		// Try YAML-style: grep for version and skill names
		content := string(data)
		if strings.Contains(content, "version: 1") || strings.Contains(content, "\"version\": 1") {
			surface.Version = 1
		} else if strings.Contains(content, "version: 2") || strings.Contains(content, "\"version\": 2") {
			surface.Version = 2
		}
		if surface.Version == 1 || surface.Version == 2 {
			// Extract skill names from "- name: <value>" lines
			for _, line := range strings.Split(content, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "- name:") {
					name := strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
					name = strings.Trim(name, "\"'")
					surface.Skills = append(surface.Skills, struct {
						Name string `json:"name"`
					}{Name: name})
				}
			}
		} else {
			r.add("skill_surface", false, fmt.Sprintf("invalid format: %v", err))
			return
		}
	}
	if surface.Version != 1 && surface.Version != 2 {
		r.add("skill_surface", false, fmt.Sprintf("version=%d, want 1 or 2", surface.Version))
		return
	}
	r.add("skill_surface", true, fmt.Sprintf("valid, %d skills", len(surface.Skills)))

	// Verify each skill has a SKILL.md
	for _, skill := range surface.Skills {
		skillPath := filepath.Join(repoPath, ".agents/skills", skill.Name, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			r.add("skill_file", false, fmt.Sprintf("missing: .agents/skills/%s/SKILL.md", skill.Name))
		} else {
			r.add("skill_file", true, skill.Name)
		}
	}
}

// modelLifecycleEntry is one tracked model pin in the workspace-level
// model-lifecycle registry.
type modelLifecycleEntry struct {
	ID          string   `json:"id"`
	Aliases     []string `json:"aliases"`
	Provider    string   `json:"provider"`
	Status      string   `json:"status"`
	Retires     string   `json:"retires"`
	Replacement string   `json:"replacement"`
}

type modelLifecycleRegistry struct {
	Version int                   `json:"version"`
	Models  []modelLifecycleEntry `json:"models"`
}

// modelPinScanFiles lists the repo-relative files scanned for pinned model
// ids/aliases. .agents/roles/*.json is globbed separately since it is a
// directory of files rather than a single fixed path.
var modelPinScanFiles = []string{
	".codex/config.toml",
	".claude/settings.json",
	".mcp.json",
	".ralphrc",
}

// addModelPinFreshness flags model ids/aliases pinned in repo config that are
// deprecated or retiring soon, per the workspace-level model-lifecycle
// registry (outside the repo, at <workspace root>/workspace/model-lifecycle.json).
// A missing or unparseable registry is not a failure — it just means the
// fleet-wide deprecation clock data isn't available yet.
func (r *Report) addModelPinFreshness(repoPath string) {
	registryPath := filepath.Join(filepath.Dir(filepath.Clean(repoPath)), "workspace", "model-lifecycle.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		r.add("model_pin_freshness", true, "model lifecycle registry absent — skipping")
		return
	}
	var registry modelLifecycleRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		r.add("model_pin_freshness", true, "model lifecycle registry absent — skipping")
		return
	}

	type candidate struct {
		text  string
		model modelLifecycleEntry
	}
	var candidates []candidate
	for _, m := range registry.Models {
		if m.ID != "" {
			candidates = append(candidates, candidate{text: m.ID, model: m})
		}
		for _, alias := range m.Aliases {
			if alias != "" {
				candidates = append(candidates, candidate{text: alias, model: m})
			}
		}
	}
	// Scan longer ids/aliases first so e.g. "gpt-5.4-mini" is matched before
	// the shorter "gpt-5.4" id that it contains.
	sort.Slice(candidates, func(i, j int) bool { return len(candidates[i].text) > len(candidates[j].text) })

	var files []string
	for _, rel := range modelPinScanFiles {
		if _, err := os.Stat(filepath.Join(repoPath, rel)); err == nil {
			files = append(files, rel)
		}
	}
	if roleFiles, err := filepath.Glob(filepath.Join(repoPath, ".agents", "roles", "*.json")); err == nil {
		for _, p := range roleFiles {
			if rel, err := filepath.Rel(repoPath, p); err == nil {
				files = append(files, rel)
			}
		}
	}

	now := nowFunc()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		content := string(data)
		matched := map[string]bool{}
		for _, c := range candidates {
			if matched[c.model.ID] {
				continue // this model already reported for this file
			}
			if pos := findWithBoundary(content, c.text); pos >= 0 {
				matched[c.model.ID] = true
				r.addModelPinVerdict(c.model, rel, today)
			}
		}
	}
}

// findWithBoundary returns the byte offset of the first occurrence of needle
// in content where neither the preceding nor following character is part of
// a longer model-id-like token ([0-9A-Za-z.-]), or -1 if there is none.
func findWithBoundary(content, needle string) int {
	if needle == "" {
		return -1
	}
	searchFrom := 0
	for {
		idx := strings.Index(content[searchFrom:], needle)
		if idx < 0 {
			return -1
		}
		start := searchFrom + idx
		end := start + len(needle)
		searchFrom = end
		if start > 0 && isModelIDChar(content[start-1]) {
			continue
		}
		if end < len(content) && isModelIDChar(content[end]) {
			continue
		}
		return start
	}
}

func isModelIDChar(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '.' || b == '-'
}

func (r *Report) addModelPinVerdict(m modelLifecycleEntry, file string, today time.Time) {
	if m.Retires != "" {
		if retiresDate, err := time.Parse("2006-01-02", m.Retires); err == nil {
			days := int(retiresDate.Sub(today).Hours() / 24)
			switch {
			case days < 0:
				r.add("model_pin_freshness", false, fmt.Sprintf("retired model pin %s in %s — migrate to %s", m.ID, file, m.Replacement))
				return
			case days <= 14:
				r.add("model_pin_freshness", false, fmt.Sprintf("model pin %s retires %s — migrate to %s", m.ID, m.Retires, m.Replacement))
				return
			}
		}
	}
	r.add("model_pin_freshness", true, fmt.Sprintf("deprecated model pin %s — plan migration to %s", m.ID, m.Replacement))
}

// mcpSpecTargetRegistry is the workspace-level MCP protocol version target
// registry (outside the repo, at <workspace root>/workspace/mcp-spec-target.json).
type mcpSpecTargetRegistry struct {
	Version        int    `json:"version"`
	Current        string `json:"current"`
	FleetTarget    string `json:"fleet_target"`
	CompatDeadline string `json:"compat_deadline"`
}

// addMCPSpecTarget flags a repo's declared MCP protocolVersion
// (.well-known/mcp.json) against the fleet-wide spec target registry.
// Presence of .well-known/mcp.json is owned by the mcp_discovery check;
// this check only runs when the file exists and has a protocolVersion.
// Version strings are YYYY-MM-DD, so lexicographic string comparison is a
// valid date comparison.
func (r *Report) addMCPSpecTarget(repoPath string) {
	wellKnown := filepath.Join(repoPath, ".well-known", "mcp.json")
	data, err := os.ReadFile(wellKnown)
	if err != nil {
		return
	}
	var doc struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.ProtocolVersion == "" {
		return
	}

	registryPath := filepath.Join(filepath.Dir(filepath.Clean(repoPath)), "workspace", "mcp-spec-target.json")
	regData, err := os.ReadFile(registryPath)
	if err != nil {
		return
	}
	var reg mcpSpecTargetRegistry
	if err := json.Unmarshal(regData, &reg); err != nil {
		return
	}
	if reg.FleetTarget == "" || reg.Current == "" {
		return
	}

	v := doc.ProtocolVersion
	switch {
	case v < reg.FleetTarget:
		r.add("mcp_spec_target", false, fmt.Sprintf("spec %s below fleet target %s", v, reg.FleetTarget))
	case v < reg.Current:
		if reg.CompatDeadline != "" && nowFunc().Format("2006-01-02") > reg.CompatDeadline {
			r.add("mcp_spec_target", false, fmt.Sprintf("spec %s behind current %s; compat deadline %s passed", v, reg.Current, reg.CompatDeadline))
		} else {
			r.add("mcp_spec_target", true, fmt.Sprintf("spec %s behind current %s; compat window until %s", v, reg.Current, reg.CompatDeadline))
		}
	default:
		r.add("mcp_spec_target", true, fmt.Sprintf("spec %s matches current %s", v, reg.Current))
	}
}

// configExpectationRegistry is the workspace-level provider configuration
// policy. It deliberately lives outside individual repositories so a repo
// cannot weaken the baseline that validates it.
type configExpectationRegistry struct {
	Version      int                 `json:"version"`
	Expectations []configExpectation `json:"expectations"`
}

// configExpectation describes one provider-owned file contract. Structured
// JSON/TOML files use dotted key paths in Expected and Forbidden. Text files
// use the contains lists for launcher flags and other source-level contracts.
type configExpectation struct {
	ID                string         `json:"id"`
	Provider          string         `json:"provider"`
	Repos             []string       `json:"repos"`
	File              string         `json:"file"`
	Format            string         `json:"format"`
	Expected          map[string]any `json:"expected,omitempty"`
	Forbidden         []string       `json:"forbidden,omitempty"`
	ExpectedContains  []string       `json:"expected_contains,omitempty"`
	ForbiddenContains []string       `json:"forbidden_contains,omitempty"`
}

// addConfigDrift validates provider config and canonical launcher flags against
// workspace/config-expectations.json. An absent registry is an informational
// skip for standalone clones; once present, invalid policy or unreadable target
// files fail closed.
func (r *Report) addConfigDrift(repoPath string) {
	registryPath := filepath.Join(filepath.Dir(filepath.Clean(repoPath)), "workspace", "config-expectations.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.add("config_drift", true, "config expectations registry absent — skipping")
			return
		}
		r.add("config_drift", false, fmt.Sprintf("read config expectations registry: %v", err))
		return
	}

	var registry configExpectationRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		r.add("config_drift", false, fmt.Sprintf("invalid config expectations registry: %v", err))
		return
	}
	if registry.Version != 1 {
		r.add("config_drift", false, fmt.Sprintf("unsupported config expectations registry version %d", registry.Version))
		return
	}

	repoName := filepath.Base(filepath.Clean(repoPath))
	matched := 0
	for _, expectation := range registry.Expectations {
		applies, matchErr := expectationApplies(expectation.Repos, repoName)
		if matchErr != nil {
			r.add("config_drift", false, fmt.Sprintf("expectation %q has invalid repo pattern: %v", expectation.ID, matchErr))
			continue
		}
		if !applies {
			continue
		}
		matched++
		r.checkConfigExpectation(repoPath, expectation)
	}
	if matched == 0 {
		r.add("config_drift", true, fmt.Sprintf("no config expectations target %s", repoName))
	}
}

func expectationApplies(patterns []string, repoName string) (bool, error) {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, repoName)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func (r *Report) checkConfigExpectation(repoPath string, expectation configExpectation) {
	label := expectation.ID
	if label == "" {
		label = expectation.Provider + ":" + expectation.File
	}
	if expectation.ID == "" || expectation.Provider == "" || expectation.File == "" {
		r.add("config_drift", false, fmt.Sprintf("invalid config expectation %q: id, provider, and file are required", label))
		return
	}
	if filepath.IsAbs(expectation.File) || strings.HasPrefix(filepath.Clean(expectation.File), "..") {
		r.add("config_drift", false, fmt.Sprintf("invalid config expectation %q: file must stay within the repo", label))
		return
	}

	data, err := os.ReadFile(filepath.Join(repoPath, expectation.File))
	if err != nil {
		r.add("config_drift", false, fmt.Sprintf("%s: cannot read %s: %v", label, expectation.File, err))
		return
	}

	switch expectation.Format {
	case "json", "toml":
		var document map[string]any
		if expectation.Format == "json" {
			err = json.Unmarshal(data, &document)
		} else {
			err = toml.Unmarshal(data, &document)
		}
		if err != nil {
			r.add("config_drift", false, fmt.Sprintf("%s: invalid %s in %s: %v", label, expectation.Format, expectation.File, err))
			return
		}
		failed := false
		keys := make([]string, 0, len(expectation.Expected))
		for key := range expectation.Expected {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			actual, ok := lookupDottedKey(document, key)
			if !ok || !configValuesEqual(actual, expectation.Expected[key]) {
				r.add("config_drift", false, fmt.Sprintf("%s: %s key %s does not match the registered value", label, expectation.File, key))
				failed = true
			}
		}
		for _, key := range expectation.Forbidden {
			if _, ok := lookupDottedKey(document, key); ok {
				r.add("config_drift", false, fmt.Sprintf("%s: forbidden key %s is present in %s", label, key, expectation.File))
				failed = true
			}
		}
		if !failed {
			r.add("config_drift", true, fmt.Sprintf("%s: %s matches registered %s expectations", label, expectation.File, expectation.Provider))
		}
	case "text":
		content := string(data)
		failed := false
		for _, needle := range expectation.ExpectedContains {
			if !strings.Contains(content, needle) {
				r.add("config_drift", false, fmt.Sprintf("%s: %s is missing a required launcher contract", label, expectation.File))
				failed = true
			}
		}
		for _, needle := range expectation.ForbiddenContains {
			if strings.Contains(content, needle) {
				r.add("config_drift", false, fmt.Sprintf("%s: %s contains a forbidden launcher contract", label, expectation.File))
				failed = true
			}
		}
		if !failed {
			r.add("config_drift", true, fmt.Sprintf("%s: %s matches registered %s launcher expectations", label, expectation.File, expectation.Provider))
		}
	default:
		r.add("config_drift", false, fmt.Sprintf("%s: unsupported format %q", label, expectation.Format))
	}
}

func lookupDottedKey(document map[string]any, key string) (any, bool) {
	var current any = document
	for _, part := range strings.Split(key, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func configValuesEqual(actual, expected any) bool {
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && string(actualJSON) == string(expectedJSON)
}

// --- ToolModule implementation ---

type module struct{}

// Module returns a ToolModule that exposes baseline validation tools.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "baselineguard" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "baseline_check",
			Description: "Run baseline-guard validation on a single repo",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{"type": "string", "description": "Path to the repository"},
				},
				"required": []string{"repo_path"},
			},
			Handler: func(params map[string]any) (any, error) {
				repoPath, _ := params["repo_path"].(string)
				if repoPath == "" {
					return nil, fmt.Errorf("repo_path is required")
				}
				return Check(repoPath), nil
			},
		},
		{
			Name:        "baseline_check_all",
			Description: "Run baseline-guard validation on all repos in the default workspace root",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default workspace root)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					home, _ := os.UserHomeDir()
					scanPath = filepath.Join(home, "hairglasses-studio")
				}
				paths, err := DiscoverWorkspaceTargets(scanPath)
				if err != nil {
					return nil, err
				}
				var reports []Report
				for _, repoPath := range paths {
					reports = append(reports, Check(repoPath))
				}
				return reports, nil
			},
		},
	}
}
