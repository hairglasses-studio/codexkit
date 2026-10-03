package surfaceparity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/workspace"
)

func RunSweep(opts SweepOptions) (*SweepReport, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return nil, fmt.Errorf("load manifest from %s: %w", root, err)
	}

	report := &SweepReport{
		Timestamp:     time.Now(),
		WorkspaceRoot: root,
		Repos:         make(map[string]*RepoSurfaceState),
	}

	globalMapByHash := make(map[string][]string)

	for _, repo := range manifest.Repos {
		if opts.ScopeFilter != "" && repo.Scope != opts.ScopeFilter {
			continue
		}

		repoPath := workspace.RepoPath(root, repo)
		if _, err := os.Stat(repoPath); os.IsNotExist(err) {
			continue
		}

		state := &RepoSurfaceState{
			RepoName:       repo.Name,
			RepoPath:       repoPath,
			Scope:          repo.Scope,
			Category:       repo.Category,
			Language:       repo.Language,
			BaselineTarget: repo.BaselineTarget,
			InSync:         true,
		}

		auditAgentsMD(repoPath, state, opts.Sync)
		auditClaudeMD(repoPath, state, opts.Sync)
		auditGeminiMD(repoPath, state)
		auditMCPConfig(repoPath, state, opts.Sync)
		auditScriptsAndStubs(repoPath, state, globalMapByHash)

		if len(state.AgentsMDIssues) > 0 || len(state.ClaudeMDIssues) > 0 || len(state.MCPErrors) > 0 {
			state.InSync = false
		}

		report.Repos[repo.Name] = state
		report.Summary.ReposChecked++

		if state.InSync {
			report.CompliantRepos++
		} else {
			report.DriftedRepos++
		}

		if !state.HasAgentsMD {
			report.Summary.MissingAgentsMD++
		}
		if len(state.AgentsMDIssues) > 0 {
			report.Summary.AgentsMDRuleGaps += len(state.AgentsMDIssues)
		}
		if !state.HasClaudeMD {
			report.Summary.MissingClaudeMD++
		}
		if !state.ClaudeMDValid && state.HasClaudeMD {
			report.Summary.InvalidClaudeMirror++
		}
		if state.HasMCPConfig {
			if state.MCPValid {
				report.Summary.ValidMCPConfigs++
			} else {
				report.Summary.InvalidMCPConfigs++
			}
		} else {
			report.Summary.MissingMCPConfigs++
		}
		report.Summary.OutdatedStubsCount += len(state.OutdatedStubs)
	}

	report.TotalRepos = len(report.Repos)

	for hash, paths := range globalMapByHash {
		if len(paths) > 1 {
			baseName := filepath.Base(paths[0])
			report.DuplicateLibs = append(report.DuplicateLibs, DuplicateLibraryGroup{
				SHA256Hash: hash,
				BaseName:   baseName,
				Instances:  paths,
			})
			for _, p := range paths {
				for _, state := range report.Repos {
					rel, relErr := filepath.Rel(state.RepoPath, p)
					if relErr == nil && !strings.HasPrefix(rel, "..") {
						state.DuplicateScripts = append(state.DuplicateScripts, rel)
					}
				}
			}
		}
	}
	sort.Slice(report.DuplicateLibs, func(i, j int) bool {
		return report.DuplicateLibs[i].BaseName < report.DuplicateLibs[j].BaseName
	})
	report.Summary.DuplicateLibGroups = len(report.DuplicateLibs)

	return report, nil
}

func auditAgentsMD(repoPath string, state *RepoSurfaceState, autoSync bool) {
	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if os.IsNotExist(err) {
		state.HasAgentsMD = false
		state.AgentsMDValid = false
		state.AgentsMDIssues = append(state.AgentsMDIssues, "AGENTS.md missing")
		if autoSync {
			content := generateCanonicalAgentsMD(state.RepoName, state.Language)
			if werr := os.WriteFile(agentsPath, []byte(content), 0o644); werr == nil {
				state.HasAgentsMD = true
				state.AgentsMDValid = true
				state.FixesApplied = append(state.FixesApplied, "Created canonical AGENTS.md")
				state.AgentsMDIssues = nil
			}
		}
		return
	}
	if err != nil {
		state.HasAgentsMD = true
		state.AgentsMDValid = false
		state.AgentsMDIssues = append(state.AgentsMDIssues, fmt.Sprintf("read error: %v", err))
		return
	}

	state.HasAgentsMD = true
	content := string(data)

	if !strings.HasPrefix(strings.TrimSpace(content), "#") {
		state.AgentsMDIssues = append(state.AgentsMDIssues, "Missing top-level markdown heading (#)")
	}

	state.AgentsMDValid = len(state.AgentsMDIssues) == 0
}

func auditClaudeMD(repoPath string, state *RepoSurfaceState, autoSync bool) {
	claudePath := filepath.Join(repoPath, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if os.IsNotExist(err) {
		state.HasClaudeMD = false
		state.ClaudeMDValid = false
		state.ClaudeMDIssues = append(state.ClaudeMDIssues, "CLAUDE.md missing")
		if autoSync {
			content := generateCanonicalClaudeMD(state.RepoName)
			if werr := os.WriteFile(claudePath, []byte(content), 0o644); werr == nil {
				state.HasClaudeMD = true
				state.ClaudeMDValid = true
				state.FixesApplied = append(state.FixesApplied, "Created canonical thin CLAUDE.md mirror")
				state.ClaudeMDIssues = nil
			}
		}
		return
	}
	if err != nil {
		state.HasClaudeMD = true
		state.ClaudeMDValid = false
		state.ClaudeMDIssues = append(state.ClaudeMDIssues, fmt.Sprintf("read error: %v", err))
		return
	}

	state.HasClaudeMD = true
	content := string(data)

	if !strings.Contains(content, "AGENTS.md") {
		state.ClaudeMDIssues = append(state.ClaudeMDIssues, "CLAUDE.md does not reference AGENTS.md as canonical instruction file")
		state.ClaudeMDValid = false
		if autoSync {
			newContent := fmt.Sprintf("# %s — Claude Code Instructions\n\nThis repo uses [AGENTS.md](AGENTS.md) as the canonical instruction file. Read it before making changes.\n\n%s", state.RepoName, content)
			if werr := os.WriteFile(claudePath, []byte(newContent), 0o644); werr == nil {
				state.ClaudeMDValid = true
				state.FixesApplied = append(state.FixesApplied, "Reconciled CLAUDE.md to reference AGENTS.md")
				state.ClaudeMDIssues = nil
			}
		}
	} else {
		state.ClaudeMDValid = true
	}
}

func auditGeminiMD(repoPath string, state *RepoSurfaceState) {
	geminiPath := filepath.Join(repoPath, "GEMINI.md")
	if _, err := os.Stat(geminiPath); err == nil {
		state.HasGeminiMD = true
	}
}

func auditMCPConfig(repoPath string, state *RepoSurfaceState, autoSync bool) {
	mcpPath := filepath.Join(repoPath, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if os.IsNotExist(err) {
		codexPath := filepath.Join(repoPath, ".codex", "config.toml")
		if _, cerr := os.Stat(codexPath); cerr == nil {
			state.HasMCPConfig = true
			state.MCPValid = true
		}
		return
	}
	if err != nil {
		state.HasMCPConfig = true
		state.MCPValid = false
		state.MCPErrors = append(state.MCPErrors, fmt.Sprintf("read .mcp.json: %v", err))
		return
	}

	state.HasMCPConfig = true
	var mcpFile struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if uerr := json.Unmarshal(data, &mcpFile); uerr != nil {
		state.MCPValid = false
		state.MCPErrors = append(state.MCPErrors, fmt.Sprintf("invalid .mcp.json schema: %v", uerr))
		return
	}

	for sName := range mcpFile.MCPServers {
		state.MCPServers = append(state.MCPServers, sName)
	}
	sort.Strings(state.MCPServers)
	state.MCPValid = true

	codexConfigPath := filepath.Join(repoPath, ".codex", "config.toml")
	if _, cerr := os.Stat(codexConfigPath); cerr == nil {
		diffReport := mcpsync.Diff(repoPath)
		if len(diffReport.Errors) > 0 {
			state.MCPValid = false
			state.MCPErrors = append(state.MCPErrors, diffReport.Errors...)
		} else if diffReport.PendingChanges {
			if autoSync {
				syncReport := mcpsync.Sync(repoPath, false)
				if len(syncReport.Errors) > 0 {
					state.MCPValid = false
					state.MCPErrors = append(state.MCPErrors, syncReport.Errors...)
				} else {
					state.FixesApplied = append(state.FixesApplied, "Synchronized .mcp.json to .codex/config.toml")
				}
			} else {
				state.MCPErrors = append(state.MCPErrors, "Pending drift between .mcp.json and .codex/config.toml")
			}
		}
	}
}

func auditScriptsAndStubs(repoPath string, state *RepoSurfaceState, globalMapByHash map[string][]string) {
	scriptsDir := filepath.Join(repoPath, "scripts")
	if _, err := os.Stat(scriptsDir); err == nil {
		_ = filepath.WalkDir(scriptsDir, func(path string, d fs.DirEntry, ferr error) error {
			if ferr != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, ".sh") || strings.HasSuffix(name, ".py") {
				state.ScriptCount++
				content, rerr := os.ReadFile(path)
				if rerr == nil && len(content) > 0 {
					h := sha256.Sum256(content)
					hashStr := hex.EncodeToString(h[:])
					globalMapByHash[hashStr] = append(globalMapByHash[hashStr], path)
				}
			}
			return nil
		})
	}

	stubCandidates := []string{
		".copilot-instructions.md",
		".gemini.json",
		".claude.json.bak",
	}
	for _, stub := range stubCandidates {
		stubPath := filepath.Join(repoPath, stub)
		if _, err := os.Stat(stubPath); err == nil {
			state.OutdatedStubs = append(state.OutdatedStubs, stub)
		}
	}
}

func generateCanonicalAgentsMD(repoName, language string) string {
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("# AGENTS.md — %s\n\n", repoName))
	b.WriteString(fmt.Sprintf("> Canonical instructions: AGENTS.md\n\n"))
	b.WriteString(fmt.Sprintf("Repository `%s` (%s component) managed by Hairglasses Studio fleet.\n\n", repoName, language))
	b.WriteString("## Fleet Invariants\n\n")
	b.WriteString("1. **Explicit staging only**: Never `git add -A` or `git add .` anywhere under the fleet tree. Stage explicit paths only.\n")
	b.WriteString("2. **Pushes to origin/main**: Direct verified pushes to `origin/main` are authorized across fleet repositories.\n")
	b.WriteString("3. **Socket unlinking hazard**: Never run `make ci` or bare `unittest discover` in `scripts/`.\n")
	b.WriteString("4. **Zero secret leakage**: Never write unmasked secrets, private keys, or tokens to disk, transcripts, or reports.\n")
	b.WriteString("5. **AG2 Isolation & Cleanroom Demarcation (Hazard #10)**: StashApp and MEGA services, mounts, databases, and caches are strictly isolated to AG2 (`archglasses-2`); completely banned on AG1 and macbook. Claude delegates StashApp/MEGA tasks to AGY subagents.\n")
	return b.String()
}

func generateCanonicalClaudeMD(repoName string) string {
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("# %s — Claude Code Instructions\n\n", repoName))
	b.WriteString("This repo uses [AGENTS.md](AGENTS.md) as the canonical instruction file. Read it before making changes.\n\n")
	b.WriteString("## Claude Notes\n\n")
	b.WriteString("- Use [AGENTS.md](AGENTS.md) for build, test, architecture, and repo-specific conventions.\n")
	b.WriteString("- Keep [CLAUDE.md](CLAUDE.md) as a thin compatibility mirror.\n")
	b.WriteString("- Summary: Canonical instructions: AGENTS.md\n")
	return b.String()
}

func (r *SweepReport) Markdown() string {
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("# Multi-Repo Surface Parity Audit Report\n\n"))
	b.WriteString(fmt.Sprintf("- **Generated**: %s\n", r.Timestamp.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- **Workspace Root**: `%s`\n", r.WorkspaceRoot))
	b.WriteString(fmt.Sprintf("- **Total Repositories**: %d\n", r.TotalRepos))
	b.WriteString(fmt.Sprintf("- **Compliant Repositories**: %d (%.1f%%)\n", r.CompliantRepos, float64(r.CompliantRepos)/float64(max(r.TotalRepos, 1))*100.0))
	b.WriteString(fmt.Sprintf("- **Drifted Repositories**: %d\n\n", r.DriftedRepos))

	b.WriteString("## Surface Parity Summary\n\n")
	b.WriteString("| Metric | Count |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| Total Repositories Audited | %d |\n", r.Summary.ReposChecked))
	b.WriteString(fmt.Sprintf("| Missing AGENTS.md | %d |\n", r.Summary.MissingAgentsMD))
	b.WriteString(fmt.Sprintf("| Missing CLAUDE.md | %d |\n", r.Summary.MissingClaudeMD))
	b.WriteString(fmt.Sprintf("| Invalid CLAUDE.md Mirrors | %d |\n", r.Summary.InvalidClaudeMirror))
	b.WriteString(fmt.Sprintf("| Valid MCP Configurations | %d |\n", r.Summary.ValidMCPConfigs))
	b.WriteString(fmt.Sprintf("| Invalid MCP Configurations | %d |\n", r.Summary.InvalidMCPConfigs))
	b.WriteString(fmt.Sprintf("| Duplicate Library Groups | %d |\n", r.Summary.DuplicateLibGroups))
	b.WriteString(fmt.Sprintf("| Outdated Config Stubs | %d |\n\n", r.Summary.OutdatedStubsCount))

	if len(r.DuplicateLibs) > 0 {
		b.WriteString(fmt.Sprintf("## Duplicate Helper Libraries Across Repositories (%d Groups)\n\n", len(r.DuplicateLibs)))
		for _, g := range r.DuplicateLibs {
			b.WriteString(fmt.Sprintf("### `%s` (SHA: `%s...`)\n", g.BaseName, g.SHA256Hash[:12]))
			for _, inst := range g.Instances {
				rel, _ := filepath.Rel(r.WorkspaceRoot, inst)
				b.WriteString(fmt.Sprintf("- `%s`\n", rel))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Repository Parity Breakdown\n\n")
	b.WriteString("| Repository | Scope | AGENTS.md | CLAUDE.md | MCP | Status |\n")
	b.WriteString("|---|---|---|---|---|---|\n")

	var sortedRepoNames []string
	for name := range r.Repos {
		sortedRepoNames = append(sortedRepoNames, name)
	}
	sort.Strings(sortedRepoNames)

	for _, name := range sortedRepoNames {
		st := r.Repos[name]
		agentsStatus := "PASS"
		if !st.HasAgentsMD || !st.AgentsMDValid {
			agentsStatus = "DRIFT"
		}
		claudeStatus := "PASS"
		if !st.HasClaudeMD || !st.ClaudeMDValid {
			claudeStatus = "DRIFT"
		}
		mcpStatus := "N/A"
		if st.HasMCPConfig {
			if st.MCPValid {
				mcpStatus = "PASS"
			} else {
				mcpStatus = "FAIL"
			}
		}
		overall := "IN SYNC"
		if !st.InSync {
			overall = "DRIFTED"
		}
		b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | **%s** |\n", st.RepoName, st.Scope, agentsStatus, claudeStatus, mcpStatus, overall))
	}

	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
