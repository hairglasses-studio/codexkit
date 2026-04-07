// Package baselineguard validates Codex repo baseline requirements.
//
// It checks for canonical instruction patterns, required files, Codex profiles,
// skill surface validity, and agent naming conventions.
package baselineguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// PortableFrontmatterKeys are the only keys allowed in skill frontmatter.
var PortableFrontmatterKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"allowed-tools": true,
}

var (
	canonicalAgentsRe = regexp.MustCompile(`(?m)^> Canonical instructions: AGENTS\.md`)
	canonicalClaudeRe = regexp.MustCompile(`This repo uses \[AGENTS\.md\]\(AGENTS\.md\) as the canonical instruction file`)
	canonicalCopilot  = "AGENTS.md"
	profileRe         = regexp.MustCompile(`(?m)^\[profiles\.(\w+)\]`)
	dashInFilename    = regexp.MustCompile(`-`)
)

// Check runs all baseline-guard validations on the given repo path.
func Check(repoPath string) Report {
	report := Report{RepoPath: repoPath}

	report.addRequiredFiles(repoPath)
	report.addCanonicalPatterns(repoPath)
	report.addProfiles(repoPath)
	report.addAgentNaming(repoPath)
	report.addSkillSurface(repoPath)

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

func (r *Report) addSkillSurface(repoPath string) {
	surfacePath := filepath.Join(repoPath, ".agents/skills/surface.yaml")
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		r.add("skill_surface", true, "no surface.yaml (optional)")
		return
	}

	// Must be valid JSON (the convention uses JSON despite .yaml extension)
	var surface struct {
		Version int `json:"version"`
		Skills  []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &surface); err != nil {
		r.add("skill_surface", false, fmt.Sprintf("invalid JSON: %v", err))
		return
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
