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

const RuntimeInventoryKind = "manifest-backed MCP runtime projection"

var DefaultRuntimeInventoryAllowedSkipped = []string{"secretstudios-mcp:secretstudios-mcp"}

type RuntimeInventoryOptions struct {
	WorkspaceRoot string
	PolicyPath    string
	ConfigPath    string
	GeneratedAt   string
}

type RuntimeInventoryCheckOptions struct {
	WorkspaceRoot   string
	PolicyPath      string
	JSONPath        string
	MarkdownPath    string
	AllowSkipped    []string
	AllowAnySkipped bool
	SkipArtifacts   bool
}

type RuntimeInventory struct {
	GeneratedAt   string                   `json:"generated_at"`
	GeneratedBy   string                   `json:"generated_by"`
	WorkspaceRoot string                   `json:"workspace_root"`
	InventoryKind string                   `json:"inventory_kind"`
	RuntimeProbe  RuntimeProbeSummary      `json:"runtime_probe"`
	Manifest      RuntimeManifestSummary   `json:"manifest"`
	Policy        RuntimePolicySummary     `json:"policy"`
	Projection    RuntimeProjectionSummary `json:"projection"`
	Servers       []RuntimeServer          `json:"servers"`
	Skipped       []RuntimeSkippedServer   `json:"skipped"`
	KnownGaps     []string                 `json:"known_gaps,omitempty"`
}

type RuntimeProbeSummary struct {
	Mode string `json:"mode"`
	Note string `json:"note"`
}

type RuntimeManifestSummary struct {
	Path              string `json:"path"`
	RepoCount         int    `json:"repo_count"`
	ActiveOperator    int    `json:"active_operator"`
	ActiveFirstParty  int    `json:"active_first_party"`
	CompatibilityOnly int    `json:"compatibility_only"`
}

type RuntimePolicySummary struct {
	Path                 string   `json:"path"`
	Loaded               bool     `json:"loaded"`
	IncludeRoot          bool     `json:"include_root"`
	UseWorkspaceManifest bool     `json:"use_workspace_manifest"`
	AllowUnlistedRepos   bool     `json:"allow_unlisted_repos"`
	IncludeScopes        []string `json:"include_scopes,omitempty"`
	ExcludeScopes        []string `json:"exclude_scopes,omitempty"`
}

type RuntimeProjectionSummary struct {
	ManifestLoaded  bool `json:"manifest_loaded"`
	PolicyLoaded    bool `json:"policy_loaded"`
	ServerCount     int  `json:"server_count"`
	ReadyCount      int  `json:"ready_count"`
	InvalidCount    int  `json:"invalid_count"`
	RemoteCount     int  `json:"remote_count"`
	SkippedCount    int  `json:"skipped_count"`
	SourceRepoCount int  `json:"source_repo_count"`
}

type RuntimeServer struct {
	Name              string   `json:"name"`
	SourceRepo        string   `json:"source_repo"`
	SourceServer      string   `json:"source_server"`
	SourceFile        string   `json:"source_file"`
	RepoScope         string   `json:"repo_scope,omitempty"`
	RepoCategory      string   `json:"repo_category,omitempty"`
	Validation        string   `json:"validation,omitempty"`
	ValidationNotes   []string `json:"validation_notes,omitempty"`
	CapabilitySummary string   `json:"capability_summary,omitempty"`
}

type RuntimeSkippedServer struct {
	SourceRepo   string `json:"source_repo"`
	SourceServer string `json:"source_server"`
	SourceFile   string `json:"source_file"`
	RepoScope    string `json:"repo_scope,omitempty"`
	RepoCategory string `json:"repo_category,omitempty"`
	Reason       string `json:"reason"`
}

type RuntimeInventoryCheckReport struct {
	Passed        bool                           `json:"passed"`
	WorkspaceRoot string                         `json:"workspace_root"`
	JSONPath      string                         `json:"json_path,omitempty"`
	MarkdownPath  string                         `json:"markdown_path,omitempty"`
	Inventory     RuntimeInventory               `json:"inventory"`
	Findings      []RuntimeInventoryCheckFinding `json:"findings"`
}

type RuntimeInventoryCheckFinding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

func (r *RuntimeInventoryCheckReport) add(check string, passed bool, message string) {
	r.Findings = append(r.Findings, RuntimeInventoryCheckFinding{
		Check:   check,
		Passed:  passed,
		Message: message,
	})
}

func BuildRuntimeInventory(opts RuntimeInventoryOptions) (RuntimeInventory, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(os.TempDir(), fmt.Sprintf("codexkit-runtime-inventory-%d.toml", os.Getpid()))
	}

	policyPath := opts.PolicyPath
	if policyPath == "" {
		policyPath = DefaultGlobalPolicyPath(root)
	}
	policy, policyLoaded, err := loadGlobalPolicy(policyPath, opts.PolicyPath != "")
	if err != nil {
		return RuntimeInventory{}, err
	}

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return RuntimeInventory{}, err
	}

	report := SyncGlobal(root, configPath, opts.PolicyPath, true)
	if len(report.Errors) > 0 {
		return RuntimeInventory{}, fmt.Errorf("%s", strings.Join(report.Errors, "; "))
	}

	inventory := RuntimeInventory{
		GeneratedAt:   generatedAt,
		GeneratedBy:   "codexkit workspace runtime-inventory",
		WorkspaceRoot: root,
		InventoryKind: RuntimeInventoryKind,
		RuntimeProbe: RuntimeProbeSummary{
			Mode: "launcher validation only",
			Note: "This artifact validates projected server commands and policy scope without invoking MCP tools or starting device-bound services. Policy-skipped servers remain visible so compatibility-only and opt-in private surfaces stay audit-visible without becoming default global MCP entries.",
		},
		Manifest:   summarizeManifest(root, manifest),
		Policy:     summarizePolicy(root, policyPath, policyLoaded, policy),
		Projection: summarizeProjection(report),
		Servers:    runtimeServers(root, report.Servers),
		Skipped:    runtimeSkipped(root, report.Skipped),
	}
	inventory.KnownGaps = knownRuntimeGaps(inventory)
	return inventory, nil
}

func CheckRuntimeInventory(opts RuntimeInventoryCheckOptions) (RuntimeInventoryCheckReport, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	jsonPath := opts.JSONPath
	if jsonPath == "" && !opts.SkipArtifacts {
		jsonPath = latestRuntimeInventoryArtifact(root, ".json")
	}
	markdownPath := opts.MarkdownPath
	if markdownPath == "" && !opts.SkipArtifacts {
		markdownPath = markdownPathForJSONArtifact(jsonPath)
		if markdownPath == "" {
			markdownPath = latestRuntimeInventoryArtifact(root, ".md")
		}
	}

	report := RuntimeInventoryCheckReport{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
	}

	generatedAt := ""
	if jsonPath != "" && !opts.SkipArtifacts {
		existing, err := readRuntimeInventoryArtifact(jsonPath)
		if err != nil {
			report.add("json_artifact", false, err.Error())
		} else {
			generatedAt = existing.GeneratedAt
		}
	}

	inventory, err := BuildRuntimeInventory(RuntimeInventoryOptions{
		WorkspaceRoot: root,
		PolicyPath:    opts.PolicyPath,
		GeneratedAt:   generatedAt,
	})
	if err != nil {
		return report, err
	}
	report.Inventory = inventory

	if inventory.Projection.InvalidCount == 0 {
		report.add("launcher_validation", true, fmt.Sprintf("%d projected servers ready", inventory.Projection.ReadyCount))
	} else {
		report.add("launcher_validation", false, strings.Join(invalidRuntimeServers(inventory.Servers), "; "))
	}

	unexpectedSkipped := unexpectedRuntimeSkipped(inventory.Skipped, opts.AllowSkipped, opts.AllowAnySkipped)
	if len(unexpectedSkipped) == 0 {
		report.add("policy_skipped", true, fmt.Sprintf("%d skipped servers allowed", len(inventory.Skipped)))
	} else {
		report.add("policy_skipped", false, strings.Join(unexpectedSkipped, "; "))
	}

	if !opts.SkipArtifacts {
		checkRuntimeInventoryJSONArtifact(&report, inventory, jsonPath)
		checkRuntimeInventoryMarkdownArtifact(&report, inventory, markdownPath)
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

func WriteRuntimeInventory(inventory RuntimeInventory, jsonPath, markdownPath string) error {
	if jsonPath != "" {
		data, err := json.MarshalIndent(inventory, "", "  ")
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
		if err := os.WriteFile(markdownPath, []byte(RenderRuntimeInventoryMarkdown(inventory)), 0644); err != nil {
			return err
		}
	}
	return nil
}

func RenderRuntimeInventoryMarkdown(inventory RuntimeInventory) string {
	var b bytes.Buffer
	generatedDate := runtimeInventoryDate(inventory.GeneratedAt)
	fmt.Fprintf(&b, "# MCP Runtime Projection Inventory\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", inventory.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`, `%s`, and `%s`.\n\n", inventory.GeneratedBy, inventory.Manifest.Path, inventory.Policy.Path)
	fmt.Fprintf(&b, "## Method\n\n%s\n\n", inventory.RuntimeProbe.Note)
	fmt.Fprintf(&b, "The source of truth is:\n\n")
	fmt.Fprintf(&b, "- `%s`\n", inventory.Manifest.Path)
	fmt.Fprintf(&b, "- `%s`\n", inventory.Policy.Path)
	fmt.Fprintf(&b, "- repo-local `.mcp.json` files\n\n")

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Count |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Manifest repos | %d |\n", inventory.Manifest.RepoCount)
	fmt.Fprintf(&b, "| Active operator repos | %d |\n", inventory.Manifest.ActiveOperator)
	fmt.Fprintf(&b, "| Active first-party repos | %d |\n", inventory.Manifest.ActiveFirstParty)
	fmt.Fprintf(&b, "| Compatibility-only repos | %d |\n", inventory.Manifest.CompatibilityOnly)
	fmt.Fprintf(&b, "| Projected MCP servers | %d |\n", inventory.Projection.ServerCount)
	fmt.Fprintf(&b, "| Ready projected servers | %d |\n", inventory.Projection.ReadyCount)
	fmt.Fprintf(&b, "| Invalid projected servers | %d |\n", inventory.Projection.InvalidCount)
	fmt.Fprintf(&b, "| Policy-skipped servers | %d |\n", inventory.Projection.SkippedCount)
	fmt.Fprintf(&b, "| Source repos with projected servers | %d |\n\n", inventory.Projection.SourceRepoCount)

	fmt.Fprintf(&b, "Policy includes the workspace root and active scopes only:\n\n")
	fmt.Fprintf(&b, "- Included scopes: %s\n", markdownCodeList(inventory.Policy.IncludeScopes))
	fmt.Fprintf(&b, "- Excluded scopes: %s\n", markdownCodeList(inventory.Policy.ExcludeScopes))
	fmt.Fprintf(&b, "- Unlisted repos: %s\n\n", boolPolicyLabel(inventory.Policy.AllowUnlistedRepos))

	fmt.Fprintf(&b, "## Projected By Repo\n\n")
	fmt.Fprintf(&b, "| Source repo | Scope | Servers | Projected aliases |\n|---|---|---:|---|\n")
	for _, group := range groupedRuntimeServers(inventory.Servers) {
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %s |\n",
			mdEscape(group.SourceRepo), mdEscape(group.Scope), len(group.Names), markdownCodeList(group.Names))
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Projected Servers\n\n")
	fmt.Fprintf(&b, "| Alias | Source | Scope | Status | Launcher validation |\n|---|---|---|---|---|\n")
	for _, server := range inventory.Servers {
		fmt.Fprintf(&b, "| `%s` | `%s:%s` | `%s` | %s | `%s` |\n",
			mdEscape(server.Name),
			mdEscape(server.SourceRepo),
			mdEscape(server.SourceServer),
			mdEscape(server.RepoScope),
			mdEscape(server.Validation),
			mdEscape(launcherValidation(server.ValidationNotes)),
		)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Skipped By Policy\n\n")
	if len(inventory.Skipped) == 0 {
		fmt.Fprintf(&b, "No servers are skipped by policy.\n\n")
	} else {
		fmt.Fprintf(&b, "| Source | Scope | Reason |\n|---|---|---|\n")
		for _, skipped := range inventory.Skipped {
			fmt.Fprintf(&b, "| `%s:%s` | `%s` | %s |\n",
				mdEscape(skipped.SourceRepo),
				mdEscape(skipped.SourceServer),
				mdEscape(skipped.RepoScope),
				mdEscape(skipped.Reason),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(inventory.KnownGaps) > 0 {
		fmt.Fprintf(&b, "## Known Gaps\n\n")
		for _, gap := range inventory.KnownGaps {
			fmt.Fprintf(&b, "- %s\n", gap)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Regenerate\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "cd ~/hairglasses-studio/codexkit\n")
	fmt.Fprintf(&b, "go run ./cmd/codexkit workspace generate-manifest ~/hairglasses-studio --write\n")
	fmt.Fprintf(&b, "go run ./cmd/codexkit workspace runtime-inventory ~/hairglasses-studio --json-out ../docs/inventory/mcp-runtime-inventory-%s.json --markdown-out ../docs/inventory/mcp-runtime-inventory-%s.md\n", generatedDate, generatedDate)
	fmt.Fprintf(&b, "go run ./cmd/codexkit workspace runtime-inventory-check ~/hairglasses-studio\n")
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

type runtimeServerGroup struct {
	SourceRepo string
	Scope      string
	Names      []string
}

func summarizeManifest(root string, manifest workspace.Manifest) RuntimeManifestSummary {
	summary := RuntimeManifestSummary{
		Path:      relPath(root, workspace.ManifestPath(root)),
		RepoCount: len(manifest.Repos),
	}
	for _, repo := range manifest.Repos {
		switch repo.Scope {
		case "active_operator":
			summary.ActiveOperator++
		case "active_first_party":
			summary.ActiveFirstParty++
		case "compatibility_only":
			summary.CompatibilityOnly++
		}
	}
	return summary
}

func summarizePolicy(root, path string, loaded bool, policy globalPolicy) RuntimePolicySummary {
	return RuntimePolicySummary{
		Path:                 relPath(root, path),
		Loaded:               loaded,
		IncludeRoot:          policy.Defaults.IncludeRoot,
		UseWorkspaceManifest: policy.Manifest.UseWorkspaceManifest,
		AllowUnlistedRepos:   policy.Manifest.AllowUnlistedRepos,
		IncludeScopes:        append([]string{}, policy.Manifest.IncludeScopes...),
		ExcludeScopes:        append([]string{}, policy.Manifest.ExcludeScopes...),
	}
}

func summarizeProjection(report GlobalSyncReport) RuntimeProjectionSummary {
	sourceRepos := make(map[string]struct{})
	summary := RuntimeProjectionSummary{
		ManifestLoaded: report.ManifestLoaded,
		PolicyLoaded:   report.PolicyLoaded,
		ServerCount:    len(report.Servers),
		SkippedCount:   len(report.Skipped),
	}
	for _, server := range report.Servers {
		sourceRepos[server.SourceRepo] = struct{}{}
		switch server.Validation {
		case "ready":
			summary.ReadyCount++
		case "invalid":
			summary.InvalidCount++
		case "remote":
			summary.RemoteCount++
		}
	}
	summary.SourceRepoCount = len(sourceRepos)
	return summary
}

func runtimeServers(root string, servers []GlobalServerInfo) []RuntimeServer {
	out := make([]RuntimeServer, 0, len(servers))
	for _, server := range servers {
		out = append(out, RuntimeServer{
			Name:              server.Name,
			SourceRepo:        server.SourceRepo,
			SourceServer:      server.SourceServer,
			SourceFile:        relPath(root, server.SourceFile),
			RepoScope:         server.RepoScope,
			RepoCategory:      server.RepoCategory,
			Validation:        server.Validation,
			ValidationNotes:   append([]string{}, server.ValidationNotes...),
			CapabilitySummary: server.CapabilitySummary,
		})
	}
	return out
}

func runtimeSkipped(root string, skipped []GlobalSkippedServerInfo) []RuntimeSkippedServer {
	out := make([]RuntimeSkippedServer, 0, len(skipped))
	for _, server := range skipped {
		out = append(out, RuntimeSkippedServer{
			SourceRepo:   server.SourceRepo,
			SourceServer: server.SourceServer,
			SourceFile:   relPath(root, server.SourceFile),
			RepoScope:    server.RepoScope,
			RepoCategory: server.RepoCategory,
			Reason:       server.Reason,
		})
	}
	return out
}

func knownRuntimeGaps(inventory RuntimeInventory) []string {
	gaps := make([]string, 0, len(inventory.Servers))
	for _, server := range inventory.Servers {
		if server.Validation == "invalid" {
			gaps = append(gaps, fmt.Sprintf("%s is projected but invalid: %s.", server.Name, strings.Join(server.ValidationNotes, "; ")))
		}
	}
	sort.Strings(gaps)
	return gaps
}

func readRuntimeInventoryArtifact(path string) (RuntimeInventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeInventory{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var inventory RuntimeInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return RuntimeInventory{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return inventory, nil
}

func checkRuntimeInventoryJSONArtifact(report *RuntimeInventoryCheckReport, inventory RuntimeInventory, path string) {
	if path == "" {
		report.add("json_artifact", false, "no mcp-runtime-inventory-*.json artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("json_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		report.add("json_artifact", false, err.Error())
		return
	}
	expected = append(expected, '\n')
	if !bytes.Equal(actual, expected) {
		report.add("json_artifact", false, fmt.Sprintf("%s does not match live runtime inventory", path))
		return
	}
	report.add("json_artifact", true, path)
}

func checkRuntimeInventoryMarkdownArtifact(report *RuntimeInventoryCheckReport, inventory RuntimeInventory, path string) {
	if path == "" {
		report.add("markdown_artifact", false, "no mcp-runtime-inventory-*.md artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("markdown_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected := []byte(RenderRuntimeInventoryMarkdown(inventory))
	if !bytes.Equal(actual, expected) {
		report.add("markdown_artifact", false, fmt.Sprintf("%s does not match live runtime inventory", path))
		return
	}
	report.add("markdown_artifact", true, path)
}

func invalidRuntimeServers(servers []RuntimeServer) []string {
	invalid := []string{}
	for _, server := range servers {
		if server.Validation != "invalid" {
			continue
		}
		message := server.Name
		if len(server.ValidationNotes) > 0 {
			message += ": " + strings.Join(server.ValidationNotes, "; ")
		}
		invalid = append(invalid, message)
	}
	sort.Strings(invalid)
	return invalid
}

func unexpectedRuntimeSkipped(skipped []RuntimeSkippedServer, allowList []string, allowAny bool) []string {
	if allowAny {
		return nil
	}
	if allowList == nil {
		allowList = DefaultRuntimeInventoryAllowedSkipped
	}
	allowed := make(map[string]struct{}, len(allowList))
	for _, item := range allowList {
		allowed[item] = struct{}{}
	}
	unexpected := []string{}
	for _, item := range skipped {
		key := runtimeSkippedKey(item)
		if _, ok := allowed[key]; ok {
			continue
		}
		unexpected = append(unexpected, fmt.Sprintf("%s skipped unexpectedly: %s", key, item.Reason))
	}
	sort.Strings(unexpected)
	return unexpected
}

func runtimeSkippedKey(skipped RuntimeSkippedServer) string {
	return skipped.SourceRepo + ":" + skipped.SourceServer
}

func latestRuntimeInventoryArtifact(root, ext string) string {
	matches, err := filepath.Glob(filepath.Join(root, "docs", "inventory", "mcp-runtime-inventory-*"+ext))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func markdownPathForJSONArtifact(jsonPath string) string {
	if jsonPath == "" {
		return ""
	}
	if strings.HasSuffix(jsonPath, ".json") {
		return strings.TrimSuffix(jsonPath, ".json") + ".md"
	}
	return jsonPath + ".md"
}

func groupedRuntimeServers(servers []RuntimeServer) []runtimeServerGroup {
	byRepo := make(map[string]*runtimeServerGroup)
	for _, server := range servers {
		group := byRepo[server.SourceRepo]
		if group == nil {
			group = &runtimeServerGroup{SourceRepo: server.SourceRepo, Scope: server.RepoScope}
			byRepo[server.SourceRepo] = group
		}
		group.Names = append(group.Names, server.Name)
	}
	groups := make([]runtimeServerGroup, 0, len(byRepo))
	for _, group := range byRepo {
		sort.Strings(group.Names)
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].SourceRepo < groups[j].SourceRepo
	})
	return groups
}

func launcherValidation(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	note := strings.Join(notes, "; ")
	return strings.TrimPrefix(note, "command: ")
}

func boolPolicyLabel(value bool) string {
	if value {
		return "allowed"
	}
	return "rejected"
}

func markdownCodeList(values []string) string {
	if len(values) == 0 {
		return "`none`"
	}
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		escaped = append(escaped, "`"+mdEscape(value)+"`")
	}
	return strings.Join(escaped, ", ")
}

func runtimeInventoryDate(generatedAt string) string {
	if len(generatedAt) >= len("2006-01-02") {
		return generatedAt[:len("2006-01-02")]
	}
	return "YYYY-MM-DD"
}

func relPath(root, path string) string {
	if root == "" || path == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}
