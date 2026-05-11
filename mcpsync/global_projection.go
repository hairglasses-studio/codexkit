package mcpsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/workspace"
)

const GlobalProjectionKind = "workspace global MCP provider projection"

type GlobalProjectionOptions struct {
	WorkspaceRoot string
	PolicyPath    string
	GeneratedAt   string
}

type GlobalProjectionCheckOptions struct {
	WorkspaceRoot string
	PolicyPath    string
	JSONPath      string
	MarkdownPath  string
	SkipArtifacts bool
}

type GlobalProjection struct {
	GeneratedAt    string                    `json:"generated_at"`
	GeneratedBy    string                    `json:"generated_by"`
	WorkspaceRoot  string                    `json:"workspace_root"`
	ProjectionKind string                    `json:"projection_kind"`
	Manifest       RuntimeManifestSummary    `json:"manifest"`
	Policy         RuntimePolicySummary      `json:"policy"`
	Runtime        RuntimeProjectionSummary  `json:"runtime"`
	Provider       ProviderProjectionSummary `json:"provider"`
	Servers        []RuntimeServer           `json:"servers"`
	Skipped        []RuntimeSkippedServer    `json:"skipped,omitempty"`
	Providers      []ProviderProjection      `json:"providers"`
	Warnings       []string                  `json:"warnings,omitempty"`
}

type ProviderProjectionSummary struct {
	ProviderCount    int `json:"provider_count"`
	TotalEntries     int `json:"total_entries"`
	ProfileEntries   int `json:"profile_entries"`
	RawSourceEntries int `json:"raw_source_entries"`
	CodexEntries     int `json:"codex_entries"`
	ClaudeEntries    int `json:"claude_entries"`
	GeminiEntries    int `json:"gemini_entries"`
}

type ProviderProjection struct {
	Provider string                    `json:"provider"`
	Prefix   string                    `json:"prefix"`
	Entries  []ProviderProjectionEntry `json:"entries"`
}

type ProviderProjectionEntry struct {
	Name          string         `json:"name"`
	SourceRepo    string         `json:"source_repo"`
	SourceServer  string         `json:"source_server"`
	SourceProfile string         `json:"source_profile,omitempty"`
	SourceMode    string         `json:"source_mode,omitempty"`
	SourceType    string         `json:"source_type"`
	SourceFile    string         `json:"source_file"`
	Comment       string         `json:"comment,omitempty"`
	Server        ProviderServer `json:"server"`
	EnabledTools  []string       `json:"enabled_tools,omitempty"`
}

type ProviderServer struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	CWD       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type GlobalProjectionCheckReport struct {
	Passed        bool                           `json:"passed"`
	WorkspaceRoot string                         `json:"workspace_root"`
	JSONPath      string                         `json:"json_path,omitempty"`
	MarkdownPath  string                         `json:"markdown_path,omitempty"`
	Projection    GlobalProjection               `json:"projection"`
	Findings      []GlobalProjectionCheckFinding `json:"findings"`
}

type GlobalProjectionCheckFinding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

func (r *GlobalProjectionCheckReport) add(check string, passed bool, message string) {
	r.Findings = append(r.Findings, GlobalProjectionCheckFinding{
		Check:   check,
		Passed:  passed,
		Message: message,
	})
}

func BuildGlobalProjection(opts GlobalProjectionOptions) (GlobalProjection, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	policyPath := opts.PolicyPath
	if policyPath == "" {
		policyPath = DefaultGlobalPolicyPath(root)
	}
	policy, policyLoaded, err := loadGlobalPolicy(policyPath, opts.PolicyPath != "")
	if err != nil {
		return GlobalProjection{}, err
	}
	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return GlobalProjection{}, err
	}
	runtime, err := BuildRuntimeInventory(RuntimeInventoryOptions{
		WorkspaceRoot: root,
		PolicyPath:    opts.PolicyPath,
		GeneratedAt:   generatedAt,
	})
	if err != nil {
		return GlobalProjection{}, err
	}
	providers, warnings, err := buildProviderProjections(root, manifest)
	if err != nil {
		return GlobalProjection{}, err
	}
	projection := GlobalProjection{
		GeneratedAt:    generatedAt,
		GeneratedBy:    "codexkit workspace global-mcp-projection",
		WorkspaceRoot:  root,
		ProjectionKind: GlobalProjectionKind,
		Manifest:       summarizeManifest(root, manifest),
		Policy:         summarizePolicy(root, policyPath, policyLoaded, policy),
		Runtime:        runtime.Projection,
		Servers:        runtime.Servers,
		Skipped:        runtime.Skipped,
		Providers:      providers,
		Warnings:       warnings,
	}
	projection.Provider = summarizeProviders(providers)
	return projection, nil
}

func CheckGlobalProjection(opts GlobalProjectionCheckOptions) (GlobalProjectionCheckReport, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	jsonPath := opts.JSONPath
	if jsonPath == "" && !opts.SkipArtifacts {
		jsonPath = latestGlobalProjectionArtifact(root, ".json")
	}
	markdownPath := opts.MarkdownPath
	if markdownPath == "" && !opts.SkipArtifacts {
		markdownPath = markdownPathForJSONArtifact(jsonPath)
		if markdownPath == "" {
			markdownPath = latestGlobalProjectionArtifact(root, ".md")
		}
	}
	report := GlobalProjectionCheckReport{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
	}
	generatedAt := ""
	if jsonPath != "" && !opts.SkipArtifacts {
		existing, err := readGlobalProjectionArtifact(jsonPath)
		if err != nil {
			report.add("json_artifact", false, err.Error())
		} else {
			generatedAt = existing.GeneratedAt
		}
	}
	projection, err := BuildGlobalProjection(GlobalProjectionOptions{
		WorkspaceRoot: root,
		PolicyPath:    opts.PolicyPath,
		GeneratedAt:   generatedAt,
	})
	if err != nil {
		return report, err
	}
	report.Projection = projection

	if projection.Runtime.InvalidCount == 0 {
		report.add("runtime_projection", true, fmt.Sprintf("%d runtime servers ready", projection.Runtime.ReadyCount))
	} else {
		report.add("runtime_projection", false, strings.Join(invalidRuntimeServers(projection.Servers), "; "))
	}
	if len(projection.Warnings) == 0 {
		report.add("provider_projection", true, fmt.Sprintf("%d provider entries", projection.Provider.TotalEntries))
	} else {
		report.add("provider_projection", true, fmt.Sprintf("%d provider entries, %d warnings", projection.Provider.TotalEntries, len(projection.Warnings)))
	}
	if !opts.SkipArtifacts {
		checkGlobalProjectionJSONArtifact(&report, projection, jsonPath)
		checkGlobalProjectionMarkdownArtifact(&report, projection, markdownPath)
	}
	report.Passed = true
	for _, finding := range report.Findings {
		if !finding.Passed {
			report.Passed = false
			break
		}
	}
	return report, nil
}

func WriteGlobalProjection(projection GlobalProjection, jsonPath, markdownPath string) error {
	if jsonPath != "" {
		data, err := json.MarshalIndent(projection, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(jsonPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(jsonPath, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	if markdownPath != "" {
		if err := os.MkdirAll(filepath.Dir(markdownPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(markdownPath, []byte(RenderGlobalProjectionMarkdown(projection)), 0644); err != nil {
			return err
		}
	}
	return nil
}

func RenderGlobalProjectionMarkdown(projection GlobalProjection) string {
	var b bytes.Buffer
	generatedDate := runtimeInventoryDate(projection.GeneratedAt)
	fmt.Fprintf(&b, "# Workspace Global MCP Projection\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", projection.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`, `%s`, `%s`, repo-local `.mcp.json`, and `.codex/mcp-profile-policy.json` files.\n\n",
		projection.GeneratedBy, projection.Manifest.Path, projection.Policy.Path)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Count |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Runtime servers | %d |\n", projection.Runtime.ServerCount)
	fmt.Fprintf(&b, "| Ready runtime servers | %d |\n", projection.Runtime.ReadyCount)
	fmt.Fprintf(&b, "| Invalid runtime servers | %d |\n", projection.Runtime.InvalidCount)
	fmt.Fprintf(&b, "| Provider entries | %d |\n", projection.Provider.TotalEntries)
	fmt.Fprintf(&b, "| Provider profile entries | %d |\n", projection.Provider.ProfileEntries)
	fmt.Fprintf(&b, "| Provider raw source entries | %d |\n", projection.Provider.RawSourceEntries)
	fmt.Fprintf(&b, "| Codex entries | %d |\n", projection.Provider.CodexEntries)
	fmt.Fprintf(&b, "| Claude entries | %d |\n", projection.Provider.ClaudeEntries)
	fmt.Fprintf(&b, "| Gemini entries | %d |\n\n", projection.Provider.GeminiEntries)

	fmt.Fprintf(&b, "## Runtime Servers\n\n")
	fmt.Fprintf(&b, "| Alias | Source | Scope | Status |\n|---|---|---|---|\n")
	for _, server := range projection.Servers {
		fmt.Fprintf(&b, "| `%s` | `%s:%s` | `%s` | `%s` |\n",
			mdEscape(server.Name),
			mdEscape(server.SourceRepo),
			mdEscape(server.SourceServer),
			mdEscape(server.RepoScope),
			mdEscape(server.Validation),
		)
	}
	fmt.Fprintf(&b, "\n")

	for _, provider := range projection.Providers {
		fmt.Fprintf(&b, "## %s Overlay\n\n", providerDisplayName(provider.Provider))
		if len(provider.Entries) == 0 {
			fmt.Fprintf(&b, "No managed entries.\n\n")
			continue
		}
		fmt.Fprintf(&b, "| Name | Source | Type | Tools |\n|---|---|---|---:|\n")
		for _, entry := range provider.Entries {
			source := entry.SourceRepo + ":" + entry.SourceServer
			if entry.SourceProfile != "" {
				source = entry.SourceRepo + ":" + entry.SourceProfile
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d |\n",
				mdEscape(entry.Name),
				mdEscape(source),
				mdEscape(entry.SourceType),
				len(entry.EnabledTools),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(projection.Warnings) > 0 {
		fmt.Fprintf(&b, "## Warnings\n\n")
		for _, warning := range projection.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Regenerate\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "cd ~/hairglasses-studio/codexkit\n")
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace global-mcp-projection ~/hairglasses-studio --json-out ../docs/inventory/workspace-global-mcp-projection-%s.json --markdown-out ../docs/inventory/workspace-global-mcp-projection-%s.md\n", generatedDate, generatedDate)
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace global-mcp-projection-check ~/hairglasses-studio\n")
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

func buildProviderProjections(root string, manifest workspace.Manifest) ([]ProviderProjection, []string, error) {
	builder := newProviderProjectionBuilder(root)
	for _, name := range []string{"systemd", "tmux", "process"} {
		if err := builder.addRootFoundational(name); err != nil {
			return nil, nil, err
		}
	}
	for _, repo := range manifest.Filter(workspace.Filter{BaselineOnly: true}) {
		repoPath := filepath.Join(root, repo.Name)
		if _, err := os.Stat(repoPath); err != nil {
			continue
		}
		if err := builder.addRepoProfiles(repo.Name, repoPath); err != nil {
			return nil, nil, err
		}
	}
	return builder.providers(), builder.warnings, nil
}

type providerProjectionBuilder struct {
	root     string
	home     string
	entries  map[string][]ProviderProjectionEntry
	seen     map[string]map[string]struct{}
	warnings []string
}

func newProviderProjectionBuilder(root string) *providerProjectionBuilder {
	home, _ := os.UserHomeDir()
	return &providerProjectionBuilder{
		root:    root,
		home:    home,
		entries: map[string][]ProviderProjectionEntry{"codex": {}, "claude": {}, "gemini": {}},
		seen: map[string]map[string]struct{}{
			"codex":  {},
			"claude": {},
			"gemini": {},
		},
	}
}

func (b *providerProjectionBuilder) addRootFoundational(name string) error {
	mcpPath := filepath.Join(b.root, ".mcp.json")
	raw, err := os.ReadFile(mcpPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", mcpPath, err)
	}
	var file globalMCPFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("parsing %s: %w", mcpPath, err)
	}
	rawServer, ok := file.MCPServers[name]
	if !ok {
		return nil
	}
	var server globalMCPServer
	if err := json.Unmarshal(rawServer, &server); err != nil {
		return fmt.Errorf("parsing %s server %s: %w", mcpPath, name, err)
	}
	server = normalizeGlobalServer(b.root, server, b.home)
	entry := ProviderProjectionEntry{
		Name:         providerName("codex", name),
		SourceRepo:   filepath.Base(b.root),
		SourceServer: name,
		SourceType:   "root_foundational",
		SourceFile:   relPath(b.root, mcpPath),
		Server:       providerServer(server),
	}
	b.addEntry("codex", entry)
	entry.Name = providerName("claude", name)
	b.addEntry("claude", entry)
	entry.Name = providerName("gemini", name)
	b.addEntry("gemini", entry)
	return nil
}

func (b *providerProjectionBuilder) addRepoProfiles(repoName, repoPath string) error {
	mcpPath := filepath.Join(repoPath, ".mcp.json")
	profilePath := filepath.Join(repoPath, ".codex", "mcp-profile-policy.json")
	if _, err := os.Stat(mcpPath); err != nil {
		return nil
	}
	if _, err := os.Stat(profilePath); err != nil {
		return nil
	}
	mcpFile, err := Parse(repoPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", profilePath, err)
	}
	var policy policyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		return fmt.Errorf("parsing %s: %w", profilePath, err)
	}
	for _, profile := range policy.Profiles {
		source, ok := mcpFile.MCPServers[profile.From]
		if !ok {
			return fmt.Errorf("profile %s source server not found: %s", profile.Name, profile.From)
		}
		resolved := resolveProviderProfile(source, profile)
		server := normalizeGlobalServer(repoPath, globalMCPServer{
			Command:   resolved.Command,
			Args:      resolved.Args,
			Env:       resolved.Env,
			CWD:       resolved.CWD,
			Transport: resolved.Transport,
			URL:       resolved.URL,
		}, b.home)
		globalStem := profile.Name
		if profile.GlobalName != "" {
			globalStem = profile.GlobalName
		}
		comment := profile.Comment
		if comment == "" {
			comment = fmt.Sprintf("%s/.codex/mcp-profile-policy.json:%s (%s <- %s)", repoName, profile.Name, profile.Mode, profile.From)
		}
		profileEntry := ProviderProjectionEntry{
			SourceRepo:    repoName,
			SourceServer:  profile.From,
			SourceProfile: profile.Name,
			SourceMode:    profile.Mode,
			SourceType:    "profile",
			SourceFile:    relPath(b.root, profilePath),
			Comment:       comment,
			Server:        providerServer(server),
			EnabledTools:  append([]string{}, profile.EnabledTools...),
		}
		if profile.GlobalCodex {
			entry := profileEntry
			entry.Name = providerName("codex", globalStem)
			b.addEntry("codex", entry)
		}
		if profile.GlobalClaude || providerDefaultClaudeProfile(profile) {
			entry := profileEntry
			entry.Name = providerName("claude", globalStem)
			b.addEntry("claude", entry)
		}
		if profile.GlobalGemini || providerDefaultGeminiProfile(profile) {
			entry := profileEntry
			entry.Name = providerName("gemini", globalStem)
			b.addEntry("gemini", entry)
		}
		if profile.GlobalRawSource {
			rawServer := normalizeGlobalServer(repoPath, globalMCPServer{
				Command:   source.Command,
				Args:      source.Args,
				Env:       source.Env,
				CWD:       source.CWD,
				Transport: source.Transport,
				URL:       source.URL,
			}, b.home)
			rawEntry := ProviderProjectionEntry{
				SourceRepo:    repoName,
				SourceServer:  profile.From,
				SourceProfile: profile.Name,
				SourceMode:    profile.Mode,
				SourceType:    "raw_source",
				SourceFile:    relPath(b.root, mcpPath),
				Server:        providerServer(rawServer),
			}
			rawEntry.Name = providerName("claude", repoName+"_"+profile.From)
			b.addEntry("claude", rawEntry)
			rawEntry.Name = providerName("gemini", repoName+"_"+profile.From)
			b.addEntry("gemini", rawEntry)
		}
	}
	return nil
}

func resolveProviderProfile(source MCPServer, profile policyProfile) resolvedProfile {
	resolved := resolvedProfile{
		Name:         profile.Name,
		Comment:      profile.Comment,
		Command:      source.Command,
		Args:         append([]string(nil), source.Args...),
		CWD:          source.CWD,
		Env:          cloneEnv(source.Env),
		Transport:    source.Transport,
		URL:          source.URL,
		EnabledTools: append([]string(nil), profile.EnabledTools...),
	}
	if profile.Override != nil {
		if strings.TrimSpace(profile.Override.Command) != "" {
			resolved.Command = profile.Override.Command
		}
		if profile.Override.Args != nil {
			resolved.Args = append([]string(nil), profile.Override.Args...)
		}
		if profile.Override.CWD != "" {
			resolved.CWD = profile.Override.CWD
		}
		if len(profile.Override.Env) > 0 {
			if resolved.Env == nil {
				resolved.Env = map[string]string{}
			}
			for key, value := range profile.Override.Env {
				resolved.Env[key] = value
			}
		}
		if profile.Override.Transport != "" {
			resolved.Transport = profile.Override.Transport
		}
		if profile.Override.URL != "" {
			resolved.URL = profile.Override.URL
		}
	}
	return resolved
}

func providerDefaultClaudeProfile(profile policyProfile) bool {
	return profile.Mode == "review" || profile.Mode == "research"
}

func providerDefaultGeminiProfile(profile policyProfile) bool {
	return profile.Mode == "review" || profile.Mode == "research"
}

func (b *providerProjectionBuilder) addEntry(provider string, entry ProviderProjectionEntry) {
	if entry.Name == "" {
		b.warnings = append(b.warnings, fmt.Sprintf("%s entry from %s:%s resolved to empty name", provider, entry.SourceRepo, entry.SourceServer))
		return
	}
	if _, ok := b.seen[provider][entry.Name]; ok {
		b.warnings = append(b.warnings, fmt.Sprintf("duplicate %s provider entry: %s", provider, entry.Name))
		return
	}
	b.seen[provider][entry.Name] = struct{}{}
	b.entries[provider] = append(b.entries[provider], entry)
}

func (b *providerProjectionBuilder) providers() []ProviderProjection {
	providers := []ProviderProjection{
		{Provider: "codex", Prefix: "studio_", Entries: b.entries["codex"]},
		{Provider: "claude", Prefix: "studio_", Entries: b.entries["claude"]},
		{Provider: "gemini", Prefix: "studio-", Entries: b.entries["gemini"]},
	}
	for i := range providers {
		sort.Slice(providers[i].Entries, func(a, b int) bool {
			return providers[i].Entries[a].Name < providers[i].Entries[b].Name
		})
	}
	return providers
}

func summarizeProviders(providers []ProviderProjection) ProviderProjectionSummary {
	summary := ProviderProjectionSummary{ProviderCount: len(providers)}
	for _, provider := range providers {
		for _, entry := range provider.Entries {
			summary.TotalEntries++
			switch entry.SourceType {
			case "profile":
				summary.ProfileEntries++
			case "raw_source":
				summary.RawSourceEntries++
			}
			switch provider.Provider {
			case "codex":
				summary.CodexEntries++
			case "claude":
				summary.ClaudeEntries++
			case "gemini":
				summary.GeminiEntries++
			}
		}
	}
	return summary
}

func providerName(provider, stem string) string {
	stem = sanitizeGlobalName(stem)
	switch provider {
	case "gemini":
		return "studio-" + strings.ReplaceAll(stem, "_", "-")
	default:
		return "studio_" + stem
	}
}

func providerDisplayName(provider string) string {
	if provider == "" {
		return ""
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

func providerServer(server globalMCPServer) ProviderServer {
	return ProviderServer{
		Command:   server.Command,
		Args:      append([]string{}, server.Args...),
		CWD:       server.CWD,
		Env:       cloneStringMap(server.Env),
		Transport: server.Transport,
		URL:       server.URL,
		Headers:   cloneStringMap(server.Headers),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func readGlobalProjectionArtifact(path string) (GlobalProjection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GlobalProjection{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var projection GlobalProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		return GlobalProjection{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return projection, nil
}

func checkGlobalProjectionJSONArtifact(report *GlobalProjectionCheckReport, projection GlobalProjection, path string) {
	if path == "" {
		report.add("json_artifact", false, "no workspace-global-mcp-projection-*.json artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("json_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		report.add("json_artifact", false, err.Error())
		return
	}
	expected = append(expected, '\n')
	if !bytes.Equal(actual, expected) {
		report.add("json_artifact", false, fmt.Sprintf("%s does not match live global MCP projection", path))
		return
	}
	report.add("json_artifact", true, path)
}

func checkGlobalProjectionMarkdownArtifact(report *GlobalProjectionCheckReport, projection GlobalProjection, path string) {
	if path == "" {
		report.add("markdown_artifact", false, "no workspace-global-mcp-projection-*.md artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("markdown_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected := []byte(RenderGlobalProjectionMarkdown(projection))
	if !bytes.Equal(actual, expected) {
		report.add("markdown_artifact", false, fmt.Sprintf("%s does not match live global MCP projection", path))
		return
	}
	report.add("markdown_artifact", true, path)
}

func latestGlobalProjectionArtifact(root, ext string) string {
	matches, err := filepath.Glob(filepath.Join(root, "docs", "inventory", "workspace-global-mcp-projection-*"+ext))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}
