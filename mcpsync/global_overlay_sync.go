package mcpsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
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
	codexContent, superseded, err := renderCodexOverlay(codexPath, projection)
	if err != nil {
		return report, err
	}
	codexAction := applyRenderedFile(codexPath, codexContent, mode)
	if len(superseded) > 0 && codexAction.Status != "error" {
		codexAction.Message = "superseded unmarked mcp_servers tables (commented out): " + strings.Join(superseded, ", ")
	}
	report.addApplyResult("codex", codexPath, "Synced workspace Codex MCP block", codexAction)

	geminiPath := opts.GeminiConfigPath
	if geminiPath == "" {
		geminiPath = filepath.Join(homeDir(), ".gemini", "settings.json")
	}
	geminiContent, err := renderGeminiOverlay(geminiPath, projection)
	if err != nil {
		return report, err
	}
	report.addApplyResult("gemini", geminiPath, "Synced workspace Gemini MCP overlay", applyRenderedFile(geminiPath, geminiContent, mode))

	var failed []string
	for _, action := range report.Actions {
		if action.Status == "error" {
			failed = append(failed, fmt.Sprintf("%s %s: %s", action.Provider, action.Target, action.Message))
		}
	}
	if len(failed) > 0 {
		return report, fmt.Errorf("workspace global MCP sync failed: %s", strings.Join(failed, "; "))
	}
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
	entries := append([]ProviderProjectionEntry(nil), providerEntries(projection, "claude")...)
	for i := range entries {
		server := &entries[i].Server
		if server.CWD == "" || server.Command == "" || server.URL != "" {
			continue
		}
		// Claude's native JSON importer discards cwd. Preserve it through argv,
		// without shell interpolation; Codex/Gemini retain their native cwd field.
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			return nil, fmt.Errorf("Claude stdio cwd projection requires a supported POSIX host")
		}
		server.Args = append([]string{"-C", server.CWD, "--", server.Command}, server.Args...)
		server.Command, server.CWD = "/usr/bin/env", ""
	}
	projectObj["mcpServers"] = mergeProviderServers(existing, entries, "studio_")
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

func renderCodexOverlay(path string, projection GlobalProjection) ([]byte, []string, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block := renderWorkspaceCodexBlock(projection)
	content, superseded := upsertWorkspaceGlobalBlock(string(existing), block, time.Now().Format("2006-01-02"))
	return []byte(content), superseded, nil
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
		// Claude Code reads "type" (stdio|http|sse); "transport" above is the
		// Codex spelling. Without "type" a url-only entry is skipped by
		// `claude doctor` / `claude mcp list` with no error at all, which is
		// how studio_context7_docs and studio_openai-developer-docs sat in
		// ~/.claude.json looking correct while being unusable. Gemini ignores
		// the extra key.
		out["type"] = remoteServerType(server.Transport)
	}
	if len(server.Headers) > 0 {
		out["headers"] = cloneStringMap(server.Headers)
	}
	return out
}

// remoteServerType maps a Codex-style transport onto the Claude Code "type"
// value for a URL-backed MCP server. An empty or unrecognized transport falls
// back to "http", the shape every remote entry in this projection uses today.
func remoteServerType(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "sse":
		return "sse"
	case "http", "streamable-http", "streamable_http", "":
		return "http"
	default:
		return "http"
	}
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
		renderProviderServerTOML(&b, entry.Name, entry.Server, entry.EnabledTools)
	}
	b.WriteString("\n" + WorkspaceGlobalEndMarker + "\n")
	return b.String()
}

func renderProviderServerTOML(b *strings.Builder, name string, server ProviderServer, enabledTools []string) {
	renderStringValue(b, "default_tools_approval_mode", defaultToolsApprovalMode)
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
	if server.StartupTimeoutSec != nil {
		fmt.Fprintf(b, "startup_timeout_sec = %d\n", *server.StartupTimeoutSec)
	}
	if len(enabledTools) > 0 {
		b.WriteString("enabled_tools = [\n")
		for _, tool := range enabledTools {
			fmt.Fprintf(b, "  %s,\n", tomlQuote(tool))
		}
		b.WriteString("]\n")
	}
	if len(server.Env) > 0 {
		renderStringMapSection(b, fmt.Sprintf("mcp_servers.%s.env", name), server.Env)
	}
	if len(server.Headers) > 0 {
		renderStringMapSection(b, fmt.Sprintf("mcp_servers.%s.headers", name), server.Headers)
	}
}

// upsertWorkspaceGlobalBlock installs block into existing, replacing a prior
// generated block when its markers are present. Either way, unmarked
// [mcp_servers.<name>] tables (and sub-tables) that the block also defines are
// commented out in place, dated with date, so the result never declares a
// table twice. It returns the new content and the superseded server names.
func upsertWorkspaceGlobalBlock(existing, block, date string) (string, []string) {
	names := generatedMCPServerNames(block)
	finish := func(content string) (string, []string) {
		if !strings.Contains(content, block) {
			// A start marker without its end marker is left untouched; the
			// block is not installed, so nothing is superseded.
			return content, nil
		}
		return supersedeUnmarkedServerTables(content, names, date)
	}
	if replaced, ok := replaceMarkedBlock(existing, WorkspaceGlobalStartMarker, WorkspaceGlobalEndMarker, block); ok {
		return finish(replaced)
	}
	if replaced, ok := replaceMarkedBlock(existing, GlobalStartMarker, GlobalEndMarker, block); ok {
		return finish(replaced)
	}
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return block, nil
	}
	insertAt := firstTOMLTableOffset(trimmed)
	if insertAt >= 0 {
		prefix := strings.TrimRight(trimmed[:insertAt], "\n")
		suffix := trimmed[insertAt:]
		if prefix == "" {
			return finish(block + "\n" + suffix + "\n")
		}
		return finish(prefix + "\n\n" + block + "\n" + suffix + "\n")
	}
	return trimmed + "\n\n" + block, nil
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
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		if err := validateTOML(path, content); err != nil {
			return GlobalOverlaySyncAction{Status: "error", Message: err.Error()}
		}
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
	defer func() { _ = os.Remove(tmpPath) }()
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
			writeActionNote(&b, action)
		case "would_update":
			fmt.Fprintf(&b, "  would update %-8s %s\n", action.Provider, action.Target)
			writeActionNote(&b, action)
		case "out_of_date":
			fmt.Fprintf(&b, "  out of date %-8s %s\n", action.Provider, action.Target)
			writeActionNote(&b, action)
		case "error":
			fmt.Fprintf(&b, "  error %-8s %s: %s\n", action.Provider, action.Target, action.Message)
		}
	}
	if !r.Pending {
		fmt.Fprintf(&b, "  up to date for %d managed repos\n", r.ManagedRepoCount)
	}
	return b.String()
}

func writeActionNote(b *strings.Builder, action GlobalOverlaySyncAction) {
	if action.Message != "" {
		fmt.Fprintf(b, "    %s\n", action.Message)
	}
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

// generatedMCPServerNames returns the server names the generated block defines,
// read from its top-level [mcp_servers.<name>] headers.
func generatedMCPServerNames(block string) []string {
	var names []string
	seen := map[string]bool{}
	var scan tomlScanState
	for _, line := range strings.SplitAfter(block, "\n") {
		if scan.atStatementStart() {
			if parts, ok := tomlHeaderKey(strings.TrimSpace(line)); ok && len(parts) == 2 && parts[0] == "mcp_servers" && !seen[parts[1]] {
				seen[parts[1]] = true
				names = append(names, parts[1])
			}
		}
		scan.consume(line)
	}
	return names
}

// supersedeUnmarkedServerTables comments out, in place, every table
// [mcp_servers.<name>] and [mcp_servers.<name>.<sub>] for the given names that
// sits outside the generated markers, so the generated block is the only live
// definition. Content is kept as comments under one dated line. It returns the
// new content and the names that were superseded, in first-seen order.
func supersedeUnmarkedServerTables(content string, names []string, date string) (string, []string) {
	if len(names) == 0 {
		return content, nil
	}
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	var out strings.Builder
	var superseded []string
	seen := map[string]bool{}
	var scan tomlScanState
	inManaged, commenting, noted := false, false, false
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inManaged && scan.atStatementStart() && strings.HasPrefix(trimmed, WorkspaceGlobalStartMarker) {
			inManaged, commenting = true, false
		}
		if inManaged {
			out.WriteString(line)
			if strings.HasPrefix(trimmed, WorkspaceGlobalEndMarker) {
				inManaged = false
			}
			continue
		}
		if scan.atStatementStart() && strings.HasPrefix(trimmed, "[") {
			commenting = false
			if parts, ok := tomlHeaderKey(trimmed); ok && len(parts) >= 2 && parts[0] == "mcp_servers" && want[parts[1]] {
				commenting = true
				if !seen[parts[1]] {
					seen[parts[1]] = true
					superseded = append(superseded, parts[1])
				}
				if !noted {
					noted = true
					fmt.Fprintf(&out, "# superseded by generated block (hg-workspace-global-sync) %s\n", date)
				}
			}
		}
		scan.consume(line)
		if commenting && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			out.WriteString("# " + line)
			continue
		}
		out.WriteString(line)
	}
	if len(superseded) == 0 {
		return content, nil
	}
	return out.String(), superseded
}

// tomlScanState tracks just enough TOML lexical state (multi-line arrays and
// multi-line strings) to tell a real table header from a continuation line.
type tomlScanState struct {
	depth int
	multi string
}

func (s *tomlScanState) atStatementStart() bool { return s.depth == 0 && s.multi == "" }

func (s *tomlScanState) consume(line string) {
	i := 0
	for i < len(line) {
		if s.multi != "" {
			if s.multi == `"""` && line[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], s.multi) {
				s.multi = ""
				i += 3
				continue
			}
			i++
			continue
		}
		c := line[i]
		switch {
		case c == '#':
			return
		case strings.HasPrefix(line[i:], `"""`):
			s.multi = `"""`
			i += 3
		case strings.HasPrefix(line[i:], `'''`):
			s.multi = `'''`
			i += 3
		case c == '"':
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '\'':
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				return
			}
			i += j + 2
		case c == '[' || c == '{':
			s.depth++
			i++
		case c == ']' || c == '}':
			if s.depth > 0 {
				s.depth--
			}
			i++
		default:
			i++
		}
	}
}

// tomlHeaderKey parses a "[a.b.c]" or "[[a.b.c]]" table header line into its
// key parts. ok is false when the line is not a well-formed header.
func tomlHeaderKey(trimmed string) ([]string, bool) {
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	rest := strings.TrimPrefix(trimmed, "[")
	closers := "]"
	if strings.HasPrefix(rest, "[") {
		rest = rest[1:]
		closers = "]]"
	}
	var parts []string
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return nil, false
		}
		var part string
		switch rest[0] {
		case '"':
			i := 1
			var b strings.Builder
			for i < len(rest) && rest[i] != '"' {
				if rest[i] == '\\' && i+1 < len(rest) {
					i++
				}
				b.WriteByte(rest[i])
				i++
			}
			if i >= len(rest) {
				return nil, false
			}
			part, rest = b.String(), rest[i+1:]
		case '\'':
			j := strings.IndexByte(rest[1:], '\'')
			if j < 0 {
				return nil, false
			}
			part, rest = rest[1:1+j], rest[j+2:]
		default:
			i := 0
			for i < len(rest) && (rest[i] == '-' || rest[i] == '_' || rest[i] >= '0' && rest[i] <= '9' || rest[i] >= 'a' && rest[i] <= 'z' || rest[i] >= 'A' && rest[i] <= 'Z') {
				i++
			}
			if i == 0 {
				return nil, false
			}
			part, rest = rest[:i], rest[i:]
		}
		parts = append(parts, part)
		rest = strings.TrimLeft(rest, " \t")
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
		case strings.HasPrefix(rest, closers):
			return parts, true
		default:
			return nil, false
		}
	}
}

// validateTOML refuses content a TOML parser rejects (duplicate tables, bad
// syntax), naming the file and the parser message.
func validateTOML(path string, content []byte) error {
	var doc map[string]any
	if err := toml.Unmarshal(content, &doc); err != nil {
		var derr *toml.DecodeError
		if errors.As(err, &derr) {
			row, col := derr.Position()
			return fmt.Errorf("refusing to write invalid TOML to %s: %s (line %d, column %d)", path, derr.Error(), row, col)
		}
		return fmt.Errorf("refusing to write invalid TOML to %s: %w", path, err)
	}
	return nil
}
