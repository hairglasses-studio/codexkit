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
	"strings"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
	"github.com/hairglasses-studio/codexkit/workspace"
	toml "github.com/pelletier/go-toml/v2"
)

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
	"GEMINI.md",
	".claude/settings.json",
	".gemini/settings.json",
	".github/copilot-instructions.md",
	".codex/config.toml",
}

// PortableFrontmatterKeys re-exports the canonical source-key set from the top-level package.
var PortableFrontmatterKeys = codexkit.SkillSourceFrontmatterKeys

var (
	canonicalAgentsRe = regexp.MustCompile(`(?m)^> Canonical instructions: AGENTS\.md`)
	canonicalClaudeRe = regexp.MustCompile(`This repo(sitory)? uses (\*\*)?\[AGENTS\.md\]\(AGENTS\.md\)(\*\*)? as (the|its) canonical instruction file`)
	canonicalCopilot  = "AGENTS.md"
	profileRe         = regexp.MustCompile(`(?m)^\[profiles\.(\w+)\]`)
	dashInFilename    = regexp.MustCompile(`-`)
	kebabNameRe       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Check runs all baseline-guard validations on the given repo path.
func Check(repoPath string) Report {
	report := Report{RepoPath: repoPath}

	report.addRequiredFiles(repoPath)
	report.addCanonicalPatterns(repoPath)
	report.addProviderSettings(repoPath)
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
	scanPath = filepath.Clean(scanPath)
	manifest, err := workspace.LoadManifest(scanPath)
	if err == nil {
		paths := make([]string, 0, len(manifest.Repos))
		for _, repo := range manifest.Repos {
			if !repo.BaselineTarget && !strings.HasPrefix(repo.Scope, "active_") {
				continue
			}
			repoPath := filepath.Join(scanPath, repo.Name)
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
	case "claude_settings_json", "gemini_settings_json", "gemini_context_bridge", "gemini_mcp_bridge":
		return []Remediation{{
			Kind:    "generator",
			Message: "refresh provider settings",
			Command: command("provider", "sync", repoPath),
		}}
	case "required_file", "canonical_agents", "canonical_claude", "canonical_gemini", "canonical_copilot":
		return []Remediation{{
			Kind:    "edit",
			Message: "restore canonical provider instruction files: AGENTS.md, CLAUDE.md, GEMINI.md, .github/copilot-instructions.md",
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

	// GEMINI.md: must reference AGENTS.md
	if data, err := os.ReadFile(filepath.Join(repoPath, "GEMINI.md")); err == nil {
		if canonicalClaudeRe.Match(data) {
			r.add("canonical_gemini", true, "")
		} else {
			r.add("canonical_gemini", false, "GEMINI.md missing canonical AGENTS.md reference")
		}
	}

	// copilot-instructions.md: must mention AGENTS.md
	if data, err := os.ReadFile(filepath.Join(repoPath, ".github/copilot-instructions.md")); err == nil {
		if strings.Contains(string(data), canonicalCopilot) {
			r.add("canonical_copilot", true, "")
		} else {
			r.add("canonical_copilot", false, "copilot-instructions.md missing AGENTS.md reference")
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

	if data, err := os.ReadFile(filepath.Join(repoPath, ".gemini/settings.json")); err == nil {
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			r.add("gemini_settings_json", false, ".gemini/settings.json must be valid JSON")
			return
		}
		r.add("gemini_settings_json", true, "")

		context, _ := parsed["context"].(map[string]any)
		fileNames, _ := context["fileName"].([]any)
		hasAgents := false
		for _, entry := range fileNames {
			if name, ok := entry.(string); ok && name == "AGENTS.md" {
				hasAgents = true
				break
			}
		}
		if hasAgents {
			r.add("gemini_context_bridge", true, "")
		} else {
			r.add("gemini_context_bridge", false, ".gemini/settings.json missing AGENTS.md context bridge")
		}

		if activeServers, ok := activeMCPServers(repoPath); ok && len(activeServers) > 0 {
			mcpServers, _ := parsed["mcpServers"].(map[string]any)
			if len(mcpServers) == 0 {
				r.add("gemini_mcp_bridge", false, ".gemini/settings.json missing mcpServers bridge")
			} else {
				kebabOK := true
				for name := range mcpServers {
					if !kebabNameRe.MatchString(name) {
						kebabOK = false
						break
					}
				}
				if kebabOK {
					r.add("gemini_mcp_bridge", true, "")
				} else {
					r.add("gemini_mcp_bridge", false, ".gemini/settings.json mcpServers keys must use kebab-case")
				}
			}
		}
	}
}

func activeMCPServers(repoPath string) (map[string]json.RawMessage, bool) {
	data, err := os.ReadFile(filepath.Join(repoPath, ".mcp.json"))
	if err != nil {
		return nil, false
	}

	var rootMCP struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &rootMCP); err != nil {
		return nil, false
	}

	active := make(map[string]json.RawMessage)
	for name, payload := range rootMCP.MCPServers {
		if strings.HasPrefix(name, "_") {
			continue
		}
		active[name] = payload
	}
	return active, true
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
