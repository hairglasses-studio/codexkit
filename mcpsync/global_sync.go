package mcpsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	GlobalStartMarker = "# BEGIN GENERATED MCP SERVERS: hg-global-mcp-sync"
	GlobalEndMarker   = "# END GENERATED MCP SERVERS: hg-global-mcp-sync"
)

var (
	globalNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
)

type GlobalSyncReport struct {
	WorkspaceRoot  string                    `json:"workspace_root"`
	ConfigPath     string                    `json:"config_path"`
	PolicyPath     string                    `json:"policy_path,omitempty"`
	PolicyLoaded   bool                      `json:"policy_loaded"`
	ManifestPath   string                    `json:"manifest_path,omitempty"`
	ManifestLoaded bool                      `json:"manifest_loaded"`
	DryRun         bool                      `json:"dry_run"`
	Servers        []GlobalServerInfo        `json:"servers"`
	Skipped        []GlobalSkippedServerInfo `json:"skipped,omitempty"`
	Actions        []SyncAction              `json:"actions"`
	Errors         []string                  `json:"errors,omitempty"`
}

type GlobalServerInfo struct {
	Name              string   `json:"name"`
	SourceRepo        string   `json:"source_repo"`
	SourceServer      string   `json:"source_server"`
	SourceFile        string   `json:"source_file"`
	RepoScope         string   `json:"repo_scope,omitempty"`
	RepoCategory      string   `json:"repo_category,omitempty"`
	CapabilitySummary string   `json:"capability_summary,omitempty"`
	Validation        string   `json:"validation,omitempty"`
	ValidationNotes   []string `json:"validation_notes,omitempty"`
}

type GlobalSkippedServerInfo struct {
	SourceRepo   string `json:"source_repo"`
	SourceServer string `json:"source_server"`
	SourceFile   string `json:"source_file"`
	RepoScope    string `json:"repo_scope,omitempty"`
	RepoCategory string `json:"repo_category,omitempty"`
	Reason       string `json:"reason"`
}

type globalMCPFile struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type globalMCPServer struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	CWD       string            `json:"cwd,omitempty"`
}

type globalServerCard struct {
	ToolCount    int      `json:"tool_count"`
	Categories   []string `json:"categories"`
	Capabilities struct {
		Tools     bool `json:"tools"`
		Resources bool `json:"resources"`
		Prompts   bool `json:"prompts"`
	} `json:"capabilities"`
}

type globalPolicy struct {
	Version  int                  `json:"version"`
	Readme   []string             `json:"_readme,omitempty"`
	Defaults globalPolicyDefaults `json:"defaults"`
	Manifest globalManifestPolicy `json:"manifest"`
	Repos    []globalRepoPolicy   `json:"repos,omitempty"`
	Servers  []globalServerPolicy `json:"servers,omitempty"`
}

type globalPolicyDefaults struct {
	IncludeRoot bool `json:"include_root"`
	ReadyOnly   bool `json:"ready_only"`
}

type globalManifestPolicy struct {
	UseWorkspaceManifest bool     `json:"use_workspace_manifest"`
	AllowUnlistedRepos   bool     `json:"allow_unlisted_repos"`
	IncludeScopes        []string `json:"include_scopes,omitempty"`
	ExcludeScopes        []string `json:"exclude_scopes,omitempty"`
	IncludeCategories    []string `json:"include_categories,omitempty"`
	ExcludeCategories    []string `json:"exclude_categories,omitempty"`
}

type globalRepoPolicy struct {
	Name           string   `json:"name"`
	Enabled        *bool    `json:"enabled,omitempty"`
	AliasPrefix    string   `json:"alias_prefix,omitempty"`
	IncludeServers []string `json:"include_servers,omitempty"`
	ExcludeServers []string `json:"exclude_servers,omitempty"`
}

type globalServerPolicy struct {
	Repo    string `json:"repo"`
	Server  string `json:"server"`
	Enabled *bool  `json:"enabled,omitempty"`
	Alias   string `json:"alias,omitempty"`
}

type globalManifest struct {
	Version int                  `json:"version"`
	Repos   []globalManifestRepo `json:"repos"`
}

type globalManifestRepo struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Scope    string `json:"scope"`
}

type globalRepoMeta struct {
	Listed   bool
	Category string
	Scope    string
}

type workspaceSource struct {
	RepoName string
	RepoPath string
	MCPPath  string
}

type globalCandidate struct {
	Alias             string
	ExplicitAlias     bool
	SourceRepo        string
	SourceServer      string
	SourceFile        string
	RepoMeta          globalRepoMeta
	CapabilitySummary string
	Validation        validationResult
	Server            globalMCPServer
}

type validationResult struct {
	Status string
	Notes  []string
}

func SyncGlobal(workspaceRoot, configPath, policyPath string, dryRun bool) GlobalSyncReport {
	report := GlobalSyncReport{
		WorkspaceRoot: workspaceRoot,
		ConfigPath:    configPath,
		DryRun:        dryRun,
	}
	if workspaceRoot == "" {
		report.Errors = append(report.Errors, "workspace root is required")
		return report
	}
	if configPath == "" {
		report.Errors = append(report.Errors, "config path is required")
		return report
	}

	policyExplicit := policyPath != ""
	if policyPath == "" {
		policyPath = DefaultGlobalPolicyPath(workspaceRoot)
	}
	report.PolicyPath = policyPath

	policy, policyLoaded, err := loadGlobalPolicy(policyPath, policyExplicit)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	report.PolicyLoaded = policyLoaded

	manifestPath := DefaultGlobalManifestPath(workspaceRoot)
	report.ManifestPath = manifestPath
	manifestMeta, manifestLoaded, err := loadGlobalManifest(manifestPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	report.ManifestLoaded = manifestLoaded

	existingData, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		report.Errors = append(report.Errors, fmt.Sprintf("reading config: %v", err))
		return report
	}
	existingConfig := string(existingData)

	managedSet := makeNameSet(extractServerNames(extractManagedRegion(existingConfig)))
	reservedSet := makeNameSet(extractServerNames(existingConfig))
	for name := range managedSet {
		delete(reservedSet, name)
	}

	candidates, skipped, err := collectGlobalCandidates(workspaceRoot, reservedSet, policy, manifestMeta)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	report.Skipped = skipped
	report.Servers = make([]GlobalServerInfo, 0, len(candidates))
	newSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		report.Servers = append(report.Servers, GlobalServerInfo{
			Name:              candidate.Alias,
			SourceRepo:        candidate.SourceRepo,
			SourceServer:      candidate.SourceServer,
			SourceFile:        candidate.SourceFile,
			RepoScope:         candidate.RepoMeta.Scope,
			RepoCategory:      candidate.RepoMeta.Category,
			CapabilitySummary: candidate.CapabilitySummary,
			Validation:        candidate.Validation.Status,
			ValidationNotes:   candidate.Validation.Notes,
		})
		action := "create"
		if _, ok := managedSet[candidate.Alias]; ok {
			action = "unchanged"
		}
		report.Actions = append(report.Actions, SyncAction{
			Action:  action,
			Server:  candidate.Alias,
			Message: fmt.Sprintf("%s <- %s:%s", candidate.Alias, candidate.SourceRepo, candidate.SourceServer),
		})
		newSet[candidate.Alias] = struct{}{}
	}
	for name := range managedSet {
		if _, ok := newSet[name]; ok {
			continue
		}
		report.Actions = append(report.Actions, SyncAction{
			Action:  "stale",
			Server:  name,
			Message: fmt.Sprintf("remove stale generated server %s", name),
		})
	}
	sort.Slice(report.Actions, func(i, j int) bool {
		return report.Actions[i].Server < report.Actions[j].Server
	})

	block := renderGlobalBlock(workspaceRoot, candidates)
	updatedConfig, err := upsertGeneratedBlock(existingConfig, block)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	if dryRun {
		return report
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("creating config dir: %v", err))
		return report
	}
	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("writing config: %v", err))
	}
	return report
}

func DefaultGlobalPolicyPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "workspace", "mcp-global-policy.json")
}

func DefaultGlobalManifestPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "workspace", "manifest.json")
}

func loadGlobalPolicy(path string, required bool) (globalPolicy, bool, error) {
	policy := defaultGlobalPolicy()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return policy, false, nil
		}
		return globalPolicy{}, false, fmt.Errorf("reading global MCP policy %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return globalPolicy{}, false, fmt.Errorf("parsing global MCP policy %s: %w", path, err)
	}
	return policy, true, nil
}

func defaultGlobalPolicy() globalPolicy {
	return globalPolicy{
		Version: 1,
		Defaults: globalPolicyDefaults{
			IncludeRoot: true,
			ReadyOnly:   false,
		},
		Manifest: globalManifestPolicy{
			UseWorkspaceManifest: true,
			AllowUnlistedRepos:   true,
		},
	}
}

func loadGlobalManifest(path string) (map[string]globalRepoMeta, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]globalRepoMeta{}, false, nil
		}
		return nil, false, fmt.Errorf("reading workspace manifest %s: %w", path, err)
	}
	var manifest globalManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, false, fmt.Errorf("parsing workspace manifest %s: %w", path, err)
	}
	meta := make(map[string]globalRepoMeta, len(manifest.Repos))
	for _, repo := range manifest.Repos {
		meta[repo.Name] = globalRepoMeta{
			Listed:   true,
			Category: repo.Category,
			Scope:    repo.Scope,
		}
	}
	return meta, true, nil
}

func collectGlobalCandidates(workspaceRoot string, reserved map[string]struct{}, policy globalPolicy, manifestMeta map[string]globalRepoMeta) ([]globalCandidate, []GlobalSkippedServerInfo, error) {
	sources, err := discoverWorkspaceSources(workspaceRoot)
	if err != nil {
		return nil, nil, err
	}
	repoRules := make(map[string]globalRepoPolicy, len(policy.Repos))
	for _, rule := range policy.Repos {
		repoRules[rule.Name] = rule
	}
	serverRules := make(map[string]globalServerPolicy, len(policy.Servers))
	for _, rule := range policy.Servers {
		serverRules[serverRuleKey(rule.Repo, rule.Server)] = rule
	}

	home, _ := os.UserHomeDir()
	candidates := make([]globalCandidate, 0, 32)
	skipped := make([]GlobalSkippedServerInfo, 0, 16)

	for _, source := range sources {
		raw, err := os.ReadFile(source.MCPPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", source.MCPPath, err)
		}
		var file globalMCPFile
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", source.MCPPath, err)
		}

		names := make([]string, 0, len(file.MCPServers))
		for name := range file.MCPServers {
			if strings.HasPrefix(name, "_") {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)

		meta := manifestMeta[source.RepoName]
		if source.RepoPath == workspaceRoot {
			meta = globalRepoMeta{
				Listed:   true,
				Category: "workspace",
				Scope:    "workspace_root",
			}
		}

		repoRule, hasRepoRule := repoRules[source.RepoName]
		if allowed, reason := repoAllowed(source, workspaceRoot, meta, policy, repoRule, hasRepoRule); !allowed {
			for _, name := range names {
				skipped = append(skipped, skippedServerInfo(source, meta, name, reason))
			}
			continue
		}

		for _, name := range names {
			serverRule, hasServerRule := serverRules[serverRuleKey(source.RepoName, name)]
			if allowed, reason := serverEnabledByPolicy(repoRule, hasRepoRule, serverRule, hasServerRule, name); !allowed {
				skipped = append(skipped, skippedServerInfo(source, meta, name, reason))
				continue
			}

			var server globalMCPServer
			if err := json.Unmarshal(file.MCPServers[name], &server); err != nil {
				return nil, nil, fmt.Errorf("parse %s server %s: %w", source.MCPPath, name, err)
			}
			server = normalizeGlobalServer(source.RepoPath, server, home)
			validation := validateGlobalServer(server)
			if policy.Defaults.ReadyOnly && validation.Status != "ready" {
				skipped = append(skipped, skippedServerInfo(source, meta, name, "server not ready: "+strings.Join(validation.Notes, "; ")))
				continue
			}

			alias, explicitAlias := configuredAlias(repoRule, hasRepoRule, serverRule, hasServerRule, name)
			candidates = append(candidates, globalCandidate{
				Alias:             alias,
				ExplicitAlias:     explicitAlias,
				SourceRepo:        source.RepoName,
				SourceServer:      name,
				SourceFile:        source.MCPPath,
				RepoMeta:          meta,
				CapabilitySummary: loadCapabilitySummary(workspaceRoot, source.RepoPath, name),
				Validation:        validation,
				Server:            server,
			})
		}
	}

	if err := assignGlobalAliases(candidates, reserved); err != nil {
		return nil, nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Alias < candidates[j].Alias
	})
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].SourceRepo == skipped[j].SourceRepo {
			return skipped[i].SourceServer < skipped[j].SourceServer
		}
		return skipped[i].SourceRepo < skipped[j].SourceRepo
	})
	return candidates, skipped, nil
}

func discoverWorkspaceSources(workspaceRoot string) ([]workspaceSource, error) {
	sources := make([]workspaceSource, 0, 16)
	rootMCP := filepath.Join(workspaceRoot, ".mcp.json")
	if _, err := os.Stat(rootMCP); err == nil {
		sources = append(sources, workspaceSource{
			RepoName: filepath.Base(workspaceRoot),
			RepoPath: workspaceRoot,
			MCPPath:  rootMCP,
		})
	}

	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("read workspace root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoPath := filepath.Join(workspaceRoot, entry.Name())
		mcpPath := filepath.Join(repoPath, ".mcp.json")
		if _, err := os.Stat(mcpPath); err != nil {
			continue
		}
		sources = append(sources, workspaceSource{
			RepoName: entry.Name(),
			RepoPath: repoPath,
			MCPPath:  mcpPath,
		})
	}
	return sources, nil
}

func repoAllowed(source workspaceSource, workspaceRoot string, meta globalRepoMeta, policy globalPolicy, repoRule globalRepoPolicy, hasRepoRule bool) (bool, string) {
	if source.RepoPath == workspaceRoot {
		if !policy.Defaults.IncludeRoot {
			return false, "root workspace MCP servers disabled by policy"
		}
		return true, ""
	}

	if hasRepoRule && repoRule.Enabled != nil {
		if !*repoRule.Enabled {
			return false, "repo disabled by policy"
		}
		if *repoRule.Enabled {
			return true, ""
		}
	}

	if !policy.Manifest.UseWorkspaceManifest {
		return true, ""
	}
	if !meta.Listed {
		if policy.Manifest.AllowUnlistedRepos {
			return true, ""
		}
		return false, "repo not present in workspace manifest"
	}
	if len(policy.Manifest.IncludeScopes) > 0 && !matchesOne(meta.Scope, policy.Manifest.IncludeScopes) {
		return false, fmt.Sprintf("repo scope %q not in include_scopes", meta.Scope)
	}
	if matchesOne(meta.Scope, policy.Manifest.ExcludeScopes) {
		return false, fmt.Sprintf("repo scope %q excluded by policy", meta.Scope)
	}
	if len(policy.Manifest.IncludeCategories) > 0 && !matchesOne(meta.Category, policy.Manifest.IncludeCategories) {
		return false, fmt.Sprintf("repo category %q not in include_categories", meta.Category)
	}
	if matchesOne(meta.Category, policy.Manifest.ExcludeCategories) {
		return false, fmt.Sprintf("repo category %q excluded by policy", meta.Category)
	}
	return true, ""
}

func serverEnabledByPolicy(repoRule globalRepoPolicy, hasRepoRule bool, serverRule globalServerPolicy, hasServerRule bool, serverName string) (bool, string) {
	if hasServerRule && serverRule.Enabled != nil {
		if !*serverRule.Enabled {
			return false, "server disabled by policy"
		}
	}
	if hasRepoRule && len(repoRule.IncludeServers) > 0 && !matchesOne(serverName, repoRule.IncludeServers) {
		if !(hasServerRule && serverRule.Enabled != nil && *serverRule.Enabled) {
			return false, "server not listed in repo include_servers"
		}
	}
	if hasRepoRule && matchesOne(serverName, repoRule.ExcludeServers) {
		if !(hasServerRule && serverRule.Enabled != nil && *serverRule.Enabled) {
			return false, "server excluded by repo policy"
		}
	}
	return true, ""
}

func configuredAlias(repoRule globalRepoPolicy, hasRepoRule bool, serverRule globalServerPolicy, hasServerRule bool, serverName string) (string, bool) {
	if hasServerRule && serverRule.Alias != "" {
		return serverRule.Alias, true
	}
	if hasRepoRule && repoRule.AliasPrefix != "" {
		return repoRule.AliasPrefix + "-" + serverName, false
	}
	return "", false
}

func serverRuleKey(repo, server string) string {
	return repo + "\x00" + server
}

func skippedServerInfo(source workspaceSource, meta globalRepoMeta, serverName, reason string) GlobalSkippedServerInfo {
	return GlobalSkippedServerInfo{
		SourceRepo:   source.RepoName,
		SourceServer: serverName,
		SourceFile:   source.MCPPath,
		RepoScope:    meta.Scope,
		RepoCategory: meta.Category,
		Reason:       reason,
	}
}

func matchesOne(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func assignGlobalAliases(candidates []globalCandidate, reserved map[string]struct{}) error {
	sourceCounts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		if candidate.ExplicitAlias {
			continue
		}
		sourceCounts[candidate.SourceServer]++
	}

	used := make(map[string]struct{}, len(candidates)+len(reserved))
	for name := range reserved {
		used[name] = struct{}{}
	}

	for i := range candidates {
		if !candidates[i].ExplicitAlias {
			continue
		}
		alias := sanitizeGlobalName(candidates[i].Alias)
		if alias == "" {
			return fmt.Errorf("explicit alias for %s:%s resolved to empty string", candidates[i].SourceRepo, candidates[i].SourceServer)
		}
		if _, ok := used[alias]; ok {
			return fmt.Errorf("explicit alias conflict for %s:%s -> %s", candidates[i].SourceRepo, candidates[i].SourceServer, alias)
		}
		candidates[i].Alias = alias
		used[alias] = struct{}{}
	}

	for i := range candidates {
		if candidates[i].ExplicitAlias {
			continue
		}
		base := candidates[i].Alias
		if base == "" {
			base = sanitizeGlobalName(candidates[i].SourceServer)
			if sourceCounts[candidates[i].SourceServer] > 1 {
				base = sanitizeGlobalName(candidates[i].SourceRepo + "-" + candidates[i].SourceServer)
			}
		} else {
			base = sanitizeGlobalName(base)
		}
		if base == "" {
			base = "mcp"
		}
		alias := base
		if _, ok := used[alias]; ok {
			if base == sanitizeGlobalName(candidates[i].SourceServer) {
				base = sanitizeGlobalName(candidates[i].SourceRepo + "-" + candidates[i].SourceServer)
				if base == "" {
					base = "mcp"
				}
			}
			alias = uniqueGlobalAlias(base, used)
		}
		candidates[i].Alias = alias
		used[alias] = struct{}{}
	}
	return nil
}

func uniqueGlobalAlias(base string, used map[string]struct{}) string {
	alias := base
	suffix := 2
	for {
		if _, ok := used[alias]; !ok {
			return alias
		}
		alias = fmt.Sprintf("%s-%d", base, suffix)
		suffix++
	}
}

func sanitizeGlobalName(name string) string {
	name = globalNameSanitizer.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "mcp"
	}
	return name
}

func normalizeGlobalServer(repoPath string, server globalMCPServer, home string) globalMCPServer {
	server.Command = expandHomePrefix(server.Command, home)
	for i, arg := range server.Args {
		server.Args[i] = expandHomePrefix(arg, home)
	}
	server.CWD = normalizeGlobalCWD(repoPath, server.CWD, home)
	if server.CWD == "" && server.Command != "" && !isRemoteTransport(server) {
		server.CWD = repoPath
	}
	return server
}

func normalizeGlobalCWD(repoPath, cwd, home string) string {
	if cwd == "" {
		return ""
	}
	cwd = expandHomePrefix(cwd, home)
	switch {
	case filepath.IsAbs(cwd):
		return filepath.Clean(cwd)
	case strings.HasPrefix(cwd, "${") || strings.HasPrefix(cwd, "$"):
		return cwd
	default:
		return filepath.Clean(filepath.Join(repoPath, cwd))
	}
}

func expandHomePrefix(value, home string) string {
	if value == "" || home == "" {
		return value
	}
	switch {
	case value == "~":
		return home
	case strings.HasPrefix(value, "~/"):
		return filepath.Join(home, strings.TrimPrefix(value, "~/"))
	case strings.HasPrefix(value, "${HOME}/"):
		return filepath.Join(home, strings.TrimPrefix(value, "${HOME}/"))
	case strings.HasPrefix(value, "$HOME/"):
		return filepath.Join(home, strings.TrimPrefix(value, "$HOME/"))
	default:
		return value
	}
}

func isRemoteTransport(server globalMCPServer) bool {
	transport := strings.ToLower(server.Transport)
	return server.URL != "" || transport == "http" || transport == "sse"
}

func loadCapabilitySummary(workspaceRoot, repoPath, serverName string) string {
	seen := make(map[string]struct{}, 4)
	for _, candidate := range capabilityCardCandidates(workspaceRoot, repoPath, serverName) {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var card globalServerCard
		if err := json.Unmarshal(raw, &card); err != nil {
			continue
		}
		return summarizeServerCard(card)
	}
	return ""
}

func capabilityCardCandidates(workspaceRoot, repoPath, serverName string) []string {
	return []string{
		filepath.Join(repoPath, ".well-known", "mcp.json"),
		filepath.Join(repoPath, "mcp", serverName+"-mcp", ".well-known", "mcp.json"),
		filepath.Join(workspaceRoot, serverName+"-mcp", ".well-known", "mcp.json"),
		filepath.Join(workspaceRoot, "dotfiles", "mcp", serverName+"-mcp", ".well-known", "mcp.json"),
	}
}

func summarizeServerCard(card globalServerCard) string {
	parts := make([]string, 0, 3)
	if card.ToolCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", card.ToolCount))
	}
	caps := make([]string, 0, 3)
	if card.Capabilities.Tools {
		caps = append(caps, "tools")
	}
	if card.Capabilities.Resources {
		caps = append(caps, "resources")
	}
	if card.Capabilities.Prompts {
		caps = append(caps, "prompts")
	}
	if len(caps) > 0 {
		parts = append(parts, strings.Join(caps, "/"))
	}
	if len(card.Categories) > 0 {
		parts = append(parts, strings.Join(card.Categories, ", "))
	}
	return strings.Join(parts, "; ")
}

func renderGlobalBlock(workspaceRoot string, candidates []globalCandidate) string {
	var b strings.Builder
	b.WriteString(GlobalStartMarker + "\n")
	fmt.Fprintf(&b, "# Generated by codexkit global MCP sync from %s\n", workspaceRoot)
	for i, candidate := range candidates {
		if i > 0 {
			b.WriteString("\n")
		}
		sourceLabel := candidate.SourceFile
		if rel, err := filepath.Rel(workspaceRoot, candidate.SourceFile); err == nil {
			sourceLabel = rel
		}
		comment := fmt.Sprintf("# %s <- %s:%s", candidate.Alias, sourceLabel, candidate.SourceServer)
		if candidate.CapabilitySummary != "" {
			comment += " [" + candidate.CapabilitySummary + "]"
		}
		b.WriteString(comment + "\n")
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", candidate.Alias)
		if candidate.Server.Command != "" {
			renderStringValue(&b, "command", candidate.Server.Command)
		}
		if len(candidate.Server.Args) > 0 {
			renderStringArray(&b, "args", candidate.Server.Args)
		}
		if candidate.Server.CWD != "" {
			renderStringValue(&b, "cwd", candidate.Server.CWD)
		}
		if candidate.Server.Transport != "" {
			renderStringValue(&b, "transport", candidate.Server.Transport)
		}
		if candidate.Server.URL != "" {
			renderStringValue(&b, "url", candidate.Server.URL)
		}
		if len(candidate.Server.Env) > 0 {
			renderStringMapSection(&b, fmt.Sprintf("mcp_servers.%s.env", candidate.Alias), candidate.Server.Env)
		}
		if len(candidate.Server.Headers) > 0 {
			renderStringMapSection(&b, fmt.Sprintf("mcp_servers.%s.headers", candidate.Alias), candidate.Server.Headers)
		}
	}
	b.WriteString("\n" + GlobalEndMarker + "\n")
	return b.String()
}

func renderStringMapSection(b *strings.Builder, section string, values map[string]string) {
	b.WriteString("\n")
	fmt.Fprintf(b, "[%s]\n", section)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		renderStringValue(b, key, values[key])
	}
}

func renderStringValue(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%s = %s\n", key, tomlQuote(value))
}

func renderStringArray(b *strings.Builder, key string, values []string) {
	fmt.Fprintf(b, "%s = [", key)
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlQuote(value))
	}
	b.WriteString("]\n")
}

func tomlQuote(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\t", "\\t",
	)
	return `"` + replacer.Replace(value) + `"`
}

func upsertGeneratedBlock(existing, block string) (string, error) {
	start := strings.Index(existing, GlobalStartMarker)
	if start < 0 {
		trimmed := strings.TrimRight(existing, "\n")
		if trimmed == "" {
			return block, nil
		}
		return trimmed + "\n\n" + block, nil
	}

	endRel := strings.Index(existing[start:], GlobalEndMarker)
	if endRel < 0 {
		return "", fmt.Errorf("found %q without matching %q", GlobalStartMarker, GlobalEndMarker)
	}
	end := start + endRel + len(GlobalEndMarker)
	if end < len(existing) && existing[end] == '\n' {
		end++
	}
	return existing[:start] + block + existing[end:], nil
}

func extractManagedRegion(config string) string {
	start := strings.Index(config, GlobalStartMarker)
	if start < 0 {
		return ""
	}
	endRel := strings.Index(config[start:], GlobalEndMarker)
	if endRel < 0 {
		return ""
	}
	end := start + endRel + len(GlobalEndMarker)
	return config[start:end]
}

func extractServerNames(config string) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, 16)
	for _, match := range mcpServerBlockRe.FindAllStringSubmatch(config, -1) {
		name := match[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func makeNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func validateGlobalServer(server globalMCPServer) validationResult {
	if server.CWD != "" {
		info, err := os.Stat(server.CWD)
		if err != nil {
			return validationResult{
				Status: "invalid",
				Notes:  []string{fmt.Sprintf("cwd missing: %s", server.CWD)},
			}
		}
		if !info.IsDir() {
			return validationResult{
				Status: "invalid",
				Notes:  []string{fmt.Sprintf("cwd is not a directory: %s", server.CWD)},
			}
		}
	}
	if server.Command == "" {
		if isRemoteTransport(server) {
			return validationResult{Status: "remote", Notes: []string{"remote transport; command not validated"}}
		}
		return validationResult{Status: "invalid", Notes: []string{"command missing"}}
	}

	resolved, err := resolveCommandPath(server)
	if err != nil {
		return validationResult{Status: "invalid", Notes: []string{err.Error()}}
	}
	return validationResult{Status: "ready", Notes: []string{fmt.Sprintf("command: %s", resolved)}}
}

func resolveCommandPath(server globalMCPServer) (string, error) {
	command := server.Command
	switch {
	case filepath.IsAbs(command):
		if !isExecutableFile(command) {
			return "", fmt.Errorf("command not executable: %s", command)
		}
		return filepath.Clean(command), nil
	case strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../"):
		if server.CWD == "" {
			return "", errors.New("relative command without cwd")
		}
		resolved := filepath.Clean(filepath.Join(server.CWD, command))
		if !isExecutableFile(resolved) {
			return "", fmt.Errorf("command not executable: %s", resolved)
		}
		return resolved, nil
	default:
		resolved, err := exec.LookPath(command)
		if err != nil {
			return "", fmt.Errorf("command not found in PATH: %s", command)
		}
		return resolved, nil
	}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}
