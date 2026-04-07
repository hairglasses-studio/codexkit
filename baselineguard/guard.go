// Package baselineguard validates Codex repo baseline requirements.
//
// It checks for canonical instruction patterns, required files, Codex profiles,
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
)

// Finding represents a single validation result.
type Finding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
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

// RequiredProfiles lists the Codex profiles that must be defined.
var RequiredProfiles = []string{
	"readonly_quiet",
	"review",
	"workspace_auto",
	"ci_json",
}

// PortableFrontmatterKeys re-exports the canonical set from the top-level package.
var PortableFrontmatterKeys = codexkit.PortableFrontmatterKeys

var (
	canonicalAgentsRe = regexp.MustCompile(`(?m)^> Canonical instructions: AGENTS\.md`)
	canonicalClaudeRe = regexp.MustCompile(`This repo uses \[AGENTS\.md\]\(AGENTS\.md\) as the canonical instruction file`)
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
	report.addMCPSyncCheck(repoPath)
	report.addMCPLauncherPortability(repoPath)
	report.addMCPDiscovery(repoPath)
	report.addA2AAwareness(repoPath)
	report.addSkillPortability(repoPath)

	report.Total = len(report.Findings)
	for _, f := range report.Findings {
		if !f.Passed {
			report.Failed++
		}
	}
	report.Passed = report.Failed == 0
	return report
}

func (r *Report) add(check string, passed bool, msg string) {
	r.Findings = append(r.Findings, Finding{Check: check, Passed: passed, Message: msg})
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
	found := make(map[string]bool)
	for _, match := range profileRe.FindAllStringSubmatch(string(data), -1) {
		found[match[1]] = true
	}
	for _, name := range RequiredProfiles {
		if found[name] {
			r.add("profile", true, name)
		} else {
			r.add("profile", false, fmt.Sprintf("missing profile: %s", name))
		}
	}
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
	agentsDir := filepath.Join(repoPath, ".agents/skills")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return // no .agents/skills is fine
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(agentsDir, entry.Name(), "SKILL.md")
		dstPath := filepath.Join(repoPath, ".claude/skills", entry.Name(), "SKILL.md")
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		dstData, err := os.ReadFile(dstPath)
		if err != nil {
			r.add("skill_sync", false, fmt.Sprintf("missing mirror: .claude/skills/%s/SKILL.md", entry.Name()))
			continue
		}
		if string(srcData) != string(dstData) {
			r.add("skill_sync", false, fmt.Sprintf("stale mirror: .claude/skills/%s/SKILL.md", entry.Name()))
		} else {
			r.add("skill_sync", true, entry.Name())
		}
	}
}

func (r *Report) addMCPSyncCheck(repoPath string) {
	mcpPath := filepath.Join(repoPath, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return // no .mcp.json is fine
	}
	var mcpFile struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcpFile); err != nil {
		r.add("mcp_sync", false, fmt.Sprintf("invalid .mcp.json: %v", err))
		return
	}
	if len(mcpFile.MCPServers) == 0 {
		r.add("mcp_sync", true, "no MCP servers defined")
		return
	}
	configPath := filepath.Join(repoPath, ".codex/config.toml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		r.add("mcp_sync", false, "MCP servers defined but .codex/config.toml missing")
		return
	}
	configStr := string(configData)
	mcpServerRe := regexp.MustCompile(`(?m)^\[mcp_servers\.([\w-]+)\]`)
	found := make(map[string]bool)
	for _, match := range mcpServerRe.FindAllStringSubmatch(configStr, -1) {
		found[match[1]] = true
	}
	for name := range mcpFile.MCPServers {
		if found[name] {
			r.add("mcp_sync", true, name)
		} else {
			r.add("mcp_sync", false, fmt.Sprintf("missing in config.toml: [mcp_servers.%s]", name))
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
		for _, line := range strings.Split(frontmatter, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) < 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			if !codexkit.PortableFrontmatterKeys[key] {
				nonPortable = append(nonPortable, key)
			}
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
	if surface.Version != 1 {
		r.add("skill_surface", false, fmt.Sprintf("version=%d, want 1", surface.Version))
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
			Description: "Run baseline-guard validation on all repos in ~/hairglasses-studio",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scan_path": map[string]any{"type": "string", "description": "Directory to scan (default ~/hairglasses-studio)"},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				scanPath, _ := params["scan_path"].(string)
				if scanPath == "" {
					home, _ := os.UserHomeDir()
					scanPath = filepath.Join(home, "hairglasses-studio")
				}
				entries, err := os.ReadDir(scanPath)
				if err != nil {
					return nil, fmt.Errorf("reading %s: %w", scanPath, err)
				}
				var reports []Report
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					repoPath := filepath.Join(scanPath, entry.Name())
					if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
						reports = append(reports, Check(repoPath))
					}
				}
				return reports, nil
			},
		},
	}
}
