package mcpsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	GlobalStartMarker = "# BEGIN GENERATED MCP SERVERS: hg-global-mcp-sync"
	GlobalEndMarker   = "# END GENERATED MCP SERVERS: hg-global-mcp-sync"
)

var globalNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type GlobalSyncReport struct {
	WorkspaceRoot string             `json:"workspace_root"`
	ConfigPath    string             `json:"config_path"`
	DryRun        bool               `json:"dry_run"`
	Servers       []GlobalServerInfo `json:"servers"`
	Actions       []SyncAction       `json:"actions"`
	Errors        []string           `json:"errors,omitempty"`
}

type GlobalServerInfo struct {
	Name              string `json:"name"`
	SourceRepo        string `json:"source_repo"`
	SourceServer      string `json:"source_server"`
	SourceFile        string `json:"source_file"`
	CapabilitySummary string `json:"capability_summary,omitempty"`
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

type workspaceSource struct {
	RepoName string
	RepoPath string
	MCPPath  string
}

type globalCandidate struct {
	Alias             string
	SourceRepo        string
	SourceServer      string
	SourceFile        string
	CapabilitySummary string
	Server            globalMCPServer
}

func SyncGlobal(workspaceRoot, configPath string, dryRun bool) GlobalSyncReport {
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

	candidates, err := collectGlobalCandidates(workspaceRoot, reservedSet)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	report.Servers = make([]GlobalServerInfo, 0, len(candidates))
	newSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		report.Servers = append(report.Servers, GlobalServerInfo{
			Name:              candidate.Alias,
			SourceRepo:        candidate.SourceRepo,
			SourceServer:      candidate.SourceServer,
			SourceFile:        candidate.SourceFile,
			CapabilitySummary: candidate.CapabilitySummary,
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

func collectGlobalCandidates(workspaceRoot string, reserved map[string]struct{}) ([]globalCandidate, error) {
	sources, err := discoverWorkspaceSources(workspaceRoot)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()

	candidates := make([]globalCandidate, 0, 32)
	for _, source := range sources {
		raw, err := os.ReadFile(source.MCPPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", source.MCPPath, err)
		}
		var file globalMCPFile
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("parse %s: %w", source.MCPPath, err)
		}
		names := make([]string, 0, len(file.MCPServers))
		for name := range file.MCPServers {
			if strings.HasPrefix(name, "_") {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var server globalMCPServer
			if err := json.Unmarshal(file.MCPServers[name], &server); err != nil {
				return nil, fmt.Errorf("parse %s server %s: %w", source.MCPPath, name, err)
			}
			candidates = append(candidates, globalCandidate{
				SourceRepo:        source.RepoName,
				SourceServer:      name,
				SourceFile:        source.MCPPath,
				CapabilitySummary: loadCapabilitySummary(workspaceRoot, source.RepoPath, name),
				Server:            normalizeGlobalServer(source.RepoPath, server, home),
			})
		}
	}

	assignGlobalAliases(candidates, reserved)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Alias < candidates[j].Alias
	})
	return candidates, nil
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

func assignGlobalAliases(candidates []globalCandidate, reserved map[string]struct{}) {
	sourceCounts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		sourceCounts[candidate.SourceServer]++
	}

	used := make(map[string]struct{}, len(candidates)+len(reserved))
	for name := range reserved {
		used[name] = struct{}{}
	}

	for i := range candidates {
		alias := sanitizeGlobalName(candidates[i].SourceServer)
		if sourceCounts[candidates[i].SourceServer] > 1 {
			alias = sanitizeGlobalName(candidates[i].SourceRepo + "-" + candidates[i].SourceServer)
		}
		if _, ok := used[alias]; ok {
			alias = sanitizeGlobalName(candidates[i].SourceRepo + "-" + candidates[i].SourceServer)
		}
		base := alias
		suffix := 2
		for {
			if _, ok := used[alias]; !ok {
				break
			}
			alias = fmt.Sprintf("%s-%d", base, suffix)
			suffix++
		}
		candidates[i].Alias = alias
		used[alias] = struct{}{}
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
