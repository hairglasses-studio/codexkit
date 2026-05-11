package mcpsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	WorkspaceGlobalStartMarker = "# BEGIN GENERATED MCP SERVERS: hg-workspace-global-sync"
	WorkspaceGlobalEndMarker   = "# END GENERATED MCP SERVERS: hg-workspace-global-sync"
)

type GlobalOverlaySyncMode string

const (
	GlobalOverlaySyncWrite  GlobalOverlaySyncMode = "write"
	GlobalOverlaySyncDryRun GlobalOverlaySyncMode = "dry-run"
	GlobalOverlaySyncCheck  GlobalOverlaySyncMode = "check"
)

type GlobalOverlaySyncOptions struct {
	WorkspaceRoot    string
	PolicyPath       string
	GeneratedAt      string
	Mode             GlobalOverlaySyncMode
	ClaudeJSONPath   string
	ClaudeProjectKey string
	CodexConfigPath  string
	GeminiConfigPath string
}

type GlobalOverlaySyncReport struct {
	WorkspaceRoot           string                    `json:"workspace_root"`
	Mode                    string                    `json:"mode"`
	Pending                 bool                      `json:"pending"`
	ManagedRepoCount        int                       `json:"managed_repo_count"`
	ClaudeToolCount         int                       `json:"claude_tool_count"`
	ClaudeFoundationalCount int                       `json:"claude_foundational_count"`
	ClaudeRawCount          int                       `json:"claude_raw_count"`
	CodexToolCount          int                       `json:"codex_tool_count"`
	CodexProfileCount       int                       `json:"codex_profile_count"`
	GeminiToolCount         int                       `json:"gemini_tool_count"`
	GeminiFoundationalCount int                       `json:"gemini_foundational_count"`
	GeminiRawCount          int                       `json:"gemini_raw_count"`
	StaleClaudeOverlayCount int                       `json:"stale_claude_overlay_count"`
	StaleCodexOverlayCount  int                       `json:"stale_codex_overlay_count"`
	StaleGeminiOverlayCount int                       `json:"stale_gemini_overlay_count"`
	Actions                 []GlobalOverlaySyncAction `json:"actions"`
	ProjectionWarningCount  int                       `json:"projection_warning_count"`
	ProjectionWarnings      []string                  `json:"projection_warnings,omitempty"`
}

type GlobalOverlaySyncAction struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

func SyncGlobalProviderOverlays(opts GlobalOverlaySyncOptions) (GlobalOverlaySyncReport, error) {
	mode := opts.Mode
	if mode == "" {
		mode = GlobalOverlaySyncWrite
	}
	switch mode {
	case GlobalOverlaySyncWrite, GlobalOverlaySyncDryRun, GlobalOverlaySyncCheck:
	default:
		return GlobalOverlaySyncReport{}, fmt.Errorf("unknown global overlay sync mode: %s", mode)
	}

	projection, err := BuildGlobalProjection(GlobalProjectionOptions{
		WorkspaceRoot: opts.WorkspaceRoot,
		PolicyPath:    opts.PolicyPath,
		GeneratedAt:   opts.GeneratedAt,
	})
	if err != nil {
		return GlobalOverlaySyncReport{}, err
	}

	report := GlobalOverlaySyncReport{
		WorkspaceRoot:          projection.WorkspaceRoot,
		Mode:                   string(mode),
		ManagedRepoCount:       projection.Manifest.RepoCount,
		ProjectionWarningCount: len(projection.Warnings),
		ProjectionWarnings:     append([]string{}, projection.Warnings...),
	}
	report.summarizeProjection(projection)

	claudePath := opts.ClaudeJSONPath
	if claudePath == "" {
		claudePath = filepath.Join(homeDir(), ".claude.json")
	}
	claudeProject := opts.ClaudeProjectKey
	if claudeProject == "" {
		claudeProject = projection.WorkspaceRoot
	}
	claudeContent, err := renderClaudeOverlay(claudePath, claudeProject, projection)
	if err != nil {
		return report, err
	}
	report.addApplyResult("claude", claudePath, "Synced workspace Claude MCP overlay", applyRenderedFile(claudePath, claudeContent, mode))

	codexPath := opts.CodexConfigPath
	if codexPath == "" {
		codexPath = filepath.Join(homeDir(), ".codex", "config.toml")
	}
	codexContent, err := renderCodexOverlay(codexPath, projection)
	if err != nil {
		return report, err
	}
	report.addApplyResult("codex", codexPath, "Synced workspace Codex MCP block", applyRenderedFile(codexPath, codexContent, mode))

	geminiPath := opts.GeminiConfigPath
	if geminiPath == "" {
		geminiPath = filepath.Join(homeDir(), ".gemini", "settings.json")
	}
	geminiContent, err := renderGeminiOverlay(geminiPath, projection)
	if err != nil {
		return report, err
	}
	report.addApplyResult("gemini", geminiPath, "Synced workspace Gemini MCP overlay", applyRenderedFile(geminiPath, geminiContent, mode))

	return report, nil
}

func (r *GlobalOverlaySyncReport) summarizeProjection(projection GlobalProjection) {
	for _, provider := range projection.Providers {
		for _, entry := range provider.Entries {
			switch provider.Provider {
			case "claude":
				r.ClaudeToolCount++
				switch entry.SourceType {
				case "root_foundational":
					r.ClaudeFoundationalCount++
				case "raw_source":
					r.ClaudeRawCount++
				}
			case "codex":
				r.CodexToolCount++
				if entry.SourceType == "profile" {
					r.CodexProfileCount++
				}
			case "gemini":
				r.GeminiToolCount++
				switch entry.SourceType {
				case "root_foundational":
					r.GeminiFoundationalCount++
				case "raw_source":
					r.GeminiRawCount++
				}
			}
		}
	}
}

func (r *GlobalOverlaySyncReport) addApplyResult(provider, target, label string, action GlobalOverlaySyncAction) {
	action.Provider = provider
	action.Target = target
	action.Label = label
	if action.Status != "unchanged" {
		r.Pending = true
		switch provider {
		case "claude":
			r.StaleClaudeOverlayCount++
		case "codex":
			r.StaleCodexOverlayCount++
		case "gemini":
			r.StaleGeminiOverlayCount++
		}
	}
	r.Actions = append(r.Actions, action)
}

func renderClaudeOverlay(path, project string, projection GlobalProjection) ([]byte, error) {
	base, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	projects := objectValue(base, "projects")
	projectObj := objectValue(projects, project)
	existing := objectValue(projectObj, "mcpServers")
	projectObj["mcpServers"] = mergeProviderServers(existing, providerEntries(projection, "claude"), "studio_")
	projects[project] = projectObj
	base["projects"] = projects
	return marshalJSONFile(base)
}

func renderGeminiOverlay(path string, projection GlobalProjection) ([]byte, error) {
	base, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing := objectValue(base, "mcpServers")
	base["mcpServers"] = mergeProviderServers(existing, providerEntries(projection, "gemini"), "studio-")
	return marshalJSONFile(base)
}

func renderCodexOverlay(path string, projection GlobalProjection) ([]byte, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block := renderWorkspaceCodexBlock(projection)
	return []byte(upsertWorkspaceGlobalBlock(string(existing), block)), nil
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

func objectValue(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func mergeProviderServers(existing map[string]any, entries []ProviderProjectionEntry, prefix string) map[string]any {
	merged := map[string]any{}
	for key, value := range existing {
		if !strings.HasPrefix(key, prefix) {
			merged[key] = value
		}
	}
	for _, entry := range entries {
		merged[entry.Name] = compactProviderServer(entry.Server)
	}
	return merged
}

func compactProviderServer(server ProviderServer) map[string]any {
	out := map[string]any{}
	if server.Command != "" {
		out["command"] = server.Command
	}
	if len(server.Args) > 0 {
		out["args"] = append([]string{}, server.Args...)
	}
	if server.CWD != "" {
		out["cwd"] = server.CWD
	}
	if len(server.Env) > 0 {
		out["env"] = cloneStringMap(server.Env)
	}
	if server.Transport != "" {
		out["transport"] = server.Transport
	}
	if server.URL != "" {
		out["url"] = server.URL
	}
	if len(server.Headers) > 0 {
		out["headers"] = cloneStringMap(server.Headers)
	}
	return out
}

func marshalJSONFile(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func providerEntries(projection GlobalProjection, providerName string) []ProviderProjectionEntry {
	for _, provider := range projection.Providers {
		if provider.Provider == providerName {
			return provider.Entries
		}
	}
	return nil
}

func renderWorkspaceCodexBlock(projection GlobalProjection) string {
	var b strings.Builder
	b.WriteString(WorkspaceGlobalStartMarker + "\n")
	fmt.Fprintf(&b, "# Generated by codexkit workspace global-mcp-sync from %s\n", projection.WorkspaceRoot)
	for _, entry := range providerEntries(projection, "codex") {
		b.WriteString("\n")
		if entry.Comment != "" {
			fmt.Fprintf(&b, "# %s\n", entry.Comment)
		}
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", entry.Name)
		renderProviderServerTOML(&b, entry.Name, entry.Server)
		if len(entry.EnabledTools) > 0 {
			b.WriteString("enabled_tools = [\n")
			for _, tool := range entry.EnabledTools {
				fmt.Fprintf(&b, "  %s,\n", tomlQuote(tool))
			}
			b.WriteString("]\n")
		}
	}
	b.WriteString("\n" + WorkspaceGlobalEndMarker + "\n")
	return b.String()
}

func renderProviderServerTOML(b *strings.Builder, name string, server ProviderServer) {
	if server.Command != "" {
		renderStringValue(b, "command", server.Command)
	}
	if len(server.Args) > 0 {
		renderStringArray(b, "args", server.Args)
	}
	if server.CWD != "" {
		renderStringValue(b, "cwd", server.CWD)
	}
	if server.Transport != "" {
		renderStringValue(b, "transport", server.Transport)
	}
	if server.URL != "" {
		renderStringValue(b, "url", server.URL)
	}
	if len(server.Env) > 0 {
		renderStringMapSection(b, fmt.Sprintf("mcp_servers.%s.env", name), server.Env)
	}
	if len(server.Headers) > 0 {
		renderStringMapSection(b, fmt.Sprintf("mcp_servers.%s.headers", name), server.Headers)
	}
}

func upsertWorkspaceGlobalBlock(existing, block string) string {
	if replaced, ok := replaceMarkedBlock(existing, WorkspaceGlobalStartMarker, WorkspaceGlobalEndMarker, block); ok {
		return replaced
	}
	if replaced, ok := replaceMarkedBlock(existing, GlobalStartMarker, GlobalEndMarker, block); ok {
		return replaced
	}
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return block
	}
	insertAt := firstTOMLTableOffset(trimmed)
	if insertAt >= 0 {
		prefix := strings.TrimRight(trimmed[:insertAt], "\n")
		suffix := trimmed[insertAt:]
		if prefix == "" {
			return block + "\n" + suffix + "\n"
		}
		return prefix + "\n\n" + block + "\n" + suffix + "\n"
	}
	return trimmed + "\n\n" + block
}

func replaceMarkedBlock(existing, startMarker, endMarker, block string) (string, bool) {
	start := strings.Index(existing, startMarker)
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(existing[start:], endMarker)
	if endRel < 0 {
		return existing, true
	}
	end := start + endRel + len(endMarker)
	if end < len(existing) && existing[end] == '\n' {
		end++
	}
	return existing[:start] + block + existing[end:], true
}

func firstTOMLTableOffset(content string) int {
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func applyRenderedFile(path string, content []byte, mode GlobalOverlaySyncMode) GlobalOverlaySyncAction {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, content) {
		return GlobalOverlaySyncAction{Status: "unchanged"}
	}
	if err != nil && !os.IsNotExist(err) {
		return GlobalOverlaySyncAction{Status: "error", Message: err.Error()}
	}

	switch mode {
	case GlobalOverlaySyncWrite:
		if err := writeFileAtomic(path, content); err != nil {
			return GlobalOverlaySyncAction{Status: "error", Message: err.Error()}
		}
		return GlobalOverlaySyncAction{Status: "updated"}
	case GlobalOverlaySyncDryRun:
		return GlobalOverlaySyncAction{Status: "would_update"}
	case GlobalOverlaySyncCheck:
		return GlobalOverlaySyncAction{Status: "out_of_date"}
	default:
		return GlobalOverlaySyncAction{Status: "error", Message: fmt.Sprintf("unknown mode: %s", mode)}
	}
}

func writeFileAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	perm := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (r GlobalOverlaySyncReport) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace global MCP sync: %s\n", strings.ToUpper(r.Mode))
	for _, action := range r.Actions {
		if action.Status == "unchanged" {
			continue
		}
		switch action.Status {
		case "updated":
			fmt.Fprintf(&b, "  updated %-8s %s\n", action.Provider, action.Target)
		case "would_update":
			fmt.Fprintf(&b, "  would update %-8s %s\n", action.Provider, action.Target)
		case "out_of_date":
			fmt.Fprintf(&b, "  out of date %-8s %s\n", action.Provider, action.Target)
		case "error":
			fmt.Fprintf(&b, "  error %-8s %s: %s\n", action.Provider, action.Target, action.Message)
		}
	}
	if !r.Pending {
		fmt.Fprintf(&b, "  up to date for %d managed repos\n", r.ManagedRepoCount)
	}
	return b.String()
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		return home
	}
	return ""
}

func sortedOverlayActions(actions []GlobalOverlaySyncAction) []GlobalOverlaySyncAction {
	out := append([]GlobalOverlaySyncAction{}, actions...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider < out[j].Provider
	})
	return out
}
