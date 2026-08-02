// Package primitiveindex inventories workspace-owned LLM agent primitives that
// sit outside the skill/MCP source contract.
package primitiveindex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/workspace"
)

const IndexKind = "workspace agent primitive index"

// Options controls primitive-index generation.
type Options struct {
	WorkspaceRoot string
	GeneratedAt   string
	// OverlayPath, when set, points at a workspace.Overlay file applied to
	// the manifest before repo metadata (scope, baseline_target) is looked
	// up. The primitive scan itself always walks WorkspaceRoot in one pass,
	// so an overlay does not relocate which files are scanned for a repo —
	// only the manifest-derived metadata attached to its primitives.
	OverlayPath string
}

// CheckOptions controls primitive-index artifact validation.
type CheckOptions struct {
	WorkspaceRoot string
	JSONPath      string
	MarkdownPath  string
	SkipArtifacts bool
	OverlayPath   string
}

// Index captures all discovered agent primitives.
type Index struct {
	GeneratedAt   string      `json:"generated_at"`
	GeneratedBy   string      `json:"generated_by"`
	WorkspaceRoot string      `json:"workspace_root"`
	IndexKind     string      `json:"index_kind"`
	Summary       Summary     `json:"summary"`
	Primitives    []Primitive `json:"primitives"`
}

// Summary captures aggregate primitive counts.
type Summary struct {
	Total                     int            `json:"total"`
	Failed                    int            `json:"failed"`
	Warnings                  int            `json:"warnings"`
	Canonical                 int            `json:"canonical"`
	GeneratedMirrors          int            `json:"generated_mirrors"`
	LocalOverlays             int            `json:"local_overlays"`
	RuntimeProjections        int            `json:"runtime_projections"`
	CompatibilityReferences   int            `json:"compatibility_references"`
	IgnoredArchives           int            `json:"ignored_archives"`
	ByKind                    map[string]int `json:"by_kind,omitempty"`
	ByProvider                map[string]int `json:"by_provider,omitempty"`
	BlockingPrimitiveFailures int            `json:"blocking_primitive_failures"`
}

// Primitive captures one discovered agent-facing source/config file or hook.
type Primitive struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Classification string            `json:"classification"`
	Repo           string            `json:"repo,omitempty"`
	Path           string            `json:"path"`
	Provider       string            `json:"provider,omitempty"`
	Status         string            `json:"status"`
	Warnings       []string          `json:"warnings,omitempty"`
	Errors         []string          `json:"errors,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// CheckReport captures primitive-index artifact validation.
type CheckReport struct {
	Passed        bool           `json:"passed"`
	WorkspaceRoot string         `json:"workspace_root"`
	JSONPath      string         `json:"json_path,omitempty"`
	MarkdownPath  string         `json:"markdown_path,omitempty"`
	Index         Index          `json:"index"`
	Findings      []CheckFinding `json:"findings"`
}

// CheckFinding captures one primitive-index validation result.
type CheckFinding struct {
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

func (r *CheckReport) add(check string, passed bool, message string) {
	r.Findings = append(r.Findings, CheckFinding{Check: check, Passed: passed, Message: message})
}

type repoMeta struct {
	Scope          string
	BaselineTarget bool
}

// Build builds a live primitive index.
func Build(opts Options) (Index, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	manifest, err := workspace.LoadManifestWithOverlay(root, opts.OverlayPath)
	if err != nil {
		return Index{}, err
	}
	repos := map[string]repoMeta{}
	for _, repo := range manifest.Repos {
		repos[repo.Name] = repoMeta{Scope: repo.Scope, BaselineTarget: repo.BaselineTarget}
	}

	index := Index{
		GeneratedAt:   generatedAt,
		GeneratedBy:   "codexkit workspace primitive-index",
		WorkspaceRoot: root,
		IndexKind:     IndexKind,
	}

	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		rel := relPath(root, path)
		if entry.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(rel) {
			return nil
		}
		index.Primitives = append(index.Primitives, buildPrimitivesForPath(root, repos, rel)...)
		return nil
	}); err != nil {
		return Index{}, err
	}

	sort.Slice(index.Primitives, func(i, j int) bool {
		return index.Primitives[i].ID < index.Primitives[j].ID
	})
	index.Summary = summarize(index.Primitives)
	return index, nil
}

// Check validates saved primitive-index artifacts against a live index.
func Check(opts CheckOptions) (CheckReport, error) {
	root := opts.WorkspaceRoot
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)

	jsonPath := opts.JSONPath
	markdownPath := opts.MarkdownPath
	if !opts.SkipArtifacts {
		if jsonPath == "" {
			jsonPath = latestArtifact(root, ".json")
		}
		if markdownPath == "" {
			markdownPath = markdownPathForJSON(jsonPath)
			if markdownPath == "" {
				markdownPath = latestArtifact(root, ".md")
			}
		}
	}

	generatedAt := ""
	if jsonPath != "" && !opts.SkipArtifacts {
		if existing, err := readJSONArtifact(jsonPath); err == nil {
			generatedAt = existing.GeneratedAt
		}
	}

	index, err := Build(Options{WorkspaceRoot: root, GeneratedAt: generatedAt, OverlayPath: opts.OverlayPath})
	if err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{
		WorkspaceRoot: root,
		JSONPath:      jsonPath,
		MarkdownPath:  markdownPath,
		Index:         index,
	}
	if index.Summary.BlockingPrimitiveFailures > 0 {
		report.add("primitive_validation", false, fmt.Sprintf("%d blocking primitive failures", index.Summary.BlockingPrimitiveFailures))
	} else {
		report.add("primitive_validation", true, fmt.Sprintf("%d primitives", index.Summary.Total))
	}
	if !opts.SkipArtifacts {
		checkJSONArtifact(&report, index, jsonPath)
		checkMarkdownArtifact(&report, index, markdownPath)
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

// Write writes JSON and/or Markdown primitive-index artifacts.
func Write(index Index, jsonPath, markdownPath string) error {
	if jsonPath != "" {
		data, err := MarshalJSON(index)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
			return err
		}
	}
	if markdownPath != "" {
		if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(markdownPath, []byte(RenderMarkdown(index)), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSON returns stable JSON for the primitive index.
func MarshalJSON(index Index) ([]byte, error) {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// RenderMarkdown returns a compact human-readable primitive index.
func RenderMarkdown(index Index) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Workspace Agent Primitive Index\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", index.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`, workspace manifest, Claude settings/hooks, provider agents, plugin manifests, nested MCP files, and provider prompt surfaces.\n\n", index.GeneratedBy)
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Count |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Total primitives | %d |\n", index.Summary.Total)
	fmt.Fprintf(&b, "| Canonical | %d |\n", index.Summary.Canonical)
	fmt.Fprintf(&b, "| Generated mirrors | %d |\n", index.Summary.GeneratedMirrors)
	fmt.Fprintf(&b, "| Local overlays | %d |\n", index.Summary.LocalOverlays)
	fmt.Fprintf(&b, "| Runtime projections | %d |\n", index.Summary.RuntimeProjections)
	fmt.Fprintf(&b, "| Compatibility references | %d |\n", index.Summary.CompatibilityReferences)
	fmt.Fprintf(&b, "| Warnings | %d |\n", index.Summary.Warnings)
	fmt.Fprintf(&b, "| Blocking failures | %d |\n\n", index.Summary.BlockingPrimitiveFailures)

	if len(index.Summary.ByKind) > 0 {
		fmt.Fprintf(&b, "## Kinds\n\n")
		fmt.Fprintf(&b, "| Kind | Count |\n|---|---:|\n")
		for _, kind := range sortedKeys(index.Summary.ByKind) {
			fmt.Fprintf(&b, "| `%s` | %d |\n", mdEscape(kind), index.Summary.ByKind[kind])
		}
		fmt.Fprintf(&b, "\n")
	}

	attention := attentionPrimitives(index.Primitives)
	if len(attention) > 0 {
		fmt.Fprintf(&b, "## Attention\n\n")
		fmt.Fprintf(&b, "| Status | Kind | Classification | Path | Notes |\n|---|---|---|---|---|\n")
		for _, primitive := range attention {
			notes := append([]string{}, primitive.Errors...)
			notes = append(notes, primitive.Warnings...)
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | %s |\n",
				mdEscape(primitive.Status),
				mdEscape(primitive.Kind),
				mdEscape(primitive.Classification),
				mdEscape(primitive.Path),
				mdEscape(strings.Join(notes, "; ")),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Primitives\n\n")
	fmt.Fprintf(&b, "| Kind | Classification | Repo | Provider | Status | Path |\n|---|---|---|---|---|---|\n")
	for _, primitive := range index.Primitives {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
			mdEscape(primitive.Kind),
			mdEscape(primitive.Classification),
			mdEscape(emptyLabel(primitive.Repo)),
			mdEscape(emptyLabel(primitive.Provider)),
			mdEscape(primitive.Status),
			mdEscape(primitive.Path),
		)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Regenerate\n\n")
	fmt.Fprintf(&b, "```bash\n")
	fmt.Fprintf(&b, "cd /path/to/workspace/codexkit\n")
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace primitive-index /path/to/workspace --json-out ../docs/inventory/workspace-agent-primitives-%s.json --markdown-out ../docs/inventory/workspace-agent-primitives-%s.md\n", artifactDate(index.GeneratedAt), artifactDate(index.GeneratedAt))
	fmt.Fprintf(&b, "GOWORK=off go run ./cmd/codexkit workspace primitive-index-check /path/to/workspace\n")
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

func buildPrimitivesForPath(root string, repos map[string]repoMeta, rel string) []Primitive {
	var out []Primitive
	switch {
	case isClaudeSettings(rel):
		out = append(out, buildClaudeSettingsPrimitives(root, repos, rel)...)
	case isClaudeHookScript(rel):
		out = append(out, newPrimitive(root, repos, "claude_hook_script", rel, "claude", nil))
	case isClaudeAgent(rel):
		out = append(out, newPrimitive(root, repos, "claude_agent", rel, "claude", metadataFromFrontmatter(root, rel)))
	case isClaudeOutputStyle(rel):
		out = append(out, newPrimitive(root, repos, "claude_output_style", rel, "claude", metadataFromFrontmatter(root, rel)))
	case isClaudeSkill(rel):
		out = append(out, newPrimitive(root, repos, "claude_skill", rel, "claude", metadataFromFrontmatter(root, rel)))
	case isCodexSkill(rel):
		out = append(out, newPrimitive(root, repos, "codex_skill", rel, "codex", metadataFromFrontmatter(root, rel)))
	case isCodexSkillMetadata(rel):
		out = append(out, buildJSONOrYAMLPrimitive(root, repos, "codex_skill_metadata", rel, "codex"))
	case isCodexAgent(rel):
		out = append(out, newPrimitive(root, repos, "codex_agent", rel, "codex", nil))
	case isCodexHooks(rel):
		out = append(out, buildJSONPrimitive(root, repos, "codex_hooks", rel, "codex", "object"))
	case isGeminiCommand(rel):
		out = append(out, newPrimitive(root, repos, "gemini_command", rel, "gemini", nil))
	case isGeminiExtension(rel):
		out = append(out, newPrimitive(root, repos, "gemini_extension", rel, "gemini", nil))
	case strings.HasSuffix(rel, ".codex-plugin/plugin.json"):
		out = append(out, buildJSONPrimitive(root, repos, "plugin_manifest", rel, "codex", "object"))
	case strings.HasSuffix(rel, ".mcp.json"):
		out = append(out, buildMCPPrimitive(root, repos, rel))
	case isInstructionFile(rel):
		out = append(out, newPrimitive(root, repos, "instruction_file", rel, providerForInstruction(rel), nil))
	}
	return out
}

func buildClaudeSettingsPrimitives(root string, repos map[string]repoMeta, rel string) []Primitive {
	settings := newPrimitive(root, repos, "claude_settings", rel, "claude", nil)
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		addIssue(&settings, fmt.Sprintf("reading settings: %v", err))
		finalizePrimitive(&settings)
		return []Primitive{settings}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		addIssue(&settings, fmt.Sprintf("parsing settings JSON: %v", err))
		finalizePrimitive(&settings)
		return []Primitive{settings}
	}
	addLocalOverlayPermissionWarnings(&settings, raw)
	finalizePrimitive(&settings)

	primitives := []Primitive{settings}
	primitives = append(primitives, buildClaudeHookPrimitives(root, repos, rel, raw)...)
	return primitives
}

func buildClaudeHookPrimitives(root string, repos map[string]repoMeta, settingsRel string, raw map[string]any) []Primitive {
	hooksRoot := raw
	if hooks, ok := raw["hooks"].(map[string]any); ok {
		hooksRoot = hooks
	}
	var out []Primitive
	for _, event := range sortedKeysAny(hooksRoot) {
		entries, ok := hooksRoot[event].([]any)
		if !ok || !isHookEvent(event) {
			continue
		}
		for entryIndex, entryRaw := range entries {
			entry, _ := entryRaw.(map[string]any)
			matcher, _ := entry["matcher"].(string)
			hooks, _ := entry["hooks"].([]any)
			for hookIndex, hookRaw := range hooks {
				hook, _ := hookRaw.(map[string]any)
				hookType, _ := hook["type"].(string)
				command, _ := hook["command"].(string)
				metadata := map[string]string{
					"event":         event,
					"settings_path": settingsRel,
					"entry_index":   fmt.Sprintf("%d", entryIndex),
					"hook_index":    fmt.Sprintf("%d", hookIndex),
					"hook_type":     hookType,
					"matcher":       matcher,
					"command":       command,
				}
				primitive := newPrimitive(root, repos, "claude_hook", settingsRel, "claude", metadata)
				primitive.ID = fmt.Sprintf("%s#%s:%d:%d", primitive.ID, event, entryIndex, hookIndex)
				if hookType == "command" {
					validateHookCommand(root, hookCommandBase(root, settingsRel), &primitive, command)
				}
				finalizePrimitive(&primitive)
				out = append(out, primitive)
			}
		}
	}
	return out
}

func buildJSONPrimitive(root string, repos map[string]repoMeta, kind, rel, provider, want string) Primitive {
	primitive := newPrimitive(root, repos, kind, rel, provider, nil)
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		addIssue(&primitive, fmt.Sprintf("reading JSON: %v", err))
		finalizePrimitive(&primitive)
		return primitive
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		addIssue(&primitive, fmt.Sprintf("parsing JSON: %v", err))
		finalizePrimitive(&primitive)
		return primitive
	}
	if want == "object" {
		obj, ok := raw.(map[string]any)
		if !ok {
			addIssue(&primitive, "expected JSON object")
		} else if name, ok := obj["name"].(string); ok && name != "" {
			primitive.Metadata = ensureMetadata(primitive.Metadata)
			primitive.Metadata["name"] = name
		}
	}
	finalizePrimitive(&primitive)
	return primitive
}

func buildJSONOrYAMLPrimitive(root string, repos map[string]repoMeta, kind, rel, provider string) Primitive {
	if strings.HasSuffix(rel, ".json") {
		return buildJSONPrimitive(root, repos, kind, rel, provider, "object")
	}
	primitive := newPrimitive(root, repos, kind, rel, provider, nil)
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		addIssue(&primitive, fmt.Sprintf("reading metadata: %v", err))
		finalizePrimitive(&primitive)
		return primitive
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		addIssue(&primitive, "metadata file is empty")
	}
	finalizePrimitive(&primitive)
	return primitive
}

func buildMCPPrimitive(root string, repos map[string]repoMeta, rel string) Primitive {
	primitive := newPrimitive(root, repos, "mcp_source", rel, "mcp", nil)
	if isRootMCP(rel) || isRepoRootMCP(rel) {
		primitive.Classification = "runtime_projection"
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		addIssue(&primitive, fmt.Sprintf("reading MCP JSON: %v", err))
		finalizePrimitive(&primitive)
		return primitive
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		addIssue(&primitive, fmt.Sprintf("parsing MCP JSON: %v", err))
		finalizePrimitive(&primitive)
		return primitive
	}
	servers, ok := raw["mcpServers"].(map[string]any)
	if !ok {
		addIssue(&primitive, "expected top-level mcpServers object")
	} else {
		primitive.Metadata = ensureMetadata(primitive.Metadata)
		primitive.Metadata["server_count"] = fmt.Sprintf("%d", len(servers))
	}
	finalizePrimitive(&primitive)
	return primitive
}

func newPrimitive(root string, repos map[string]repoMeta, kind, rel, provider string, metadata map[string]string) Primitive {
	classification := classify(root, repos, rel)
	primitive := Primitive{
		ID:             kind + ":" + rel,
		Kind:           kind,
		Classification: classification,
		Repo:           repoForRel(rel),
		Path:           rel,
		Provider:       provider,
		Status:         "pass",
		Metadata:       metadata,
	}
	if isGeneratedMirror(root, rel) && classification == "canonical" {
		primitive.Classification = "generated_mirror"
	}
	finalizePrimitive(&primitive)
	return primitive
}

func classify(root string, repos map[string]repoMeta, rel string) string {
	if strings.Contains(rel, "settings.local.json") {
		return "local_overlay"
	}
	if strings.HasPrefix(rel, ".claude/") {
		if strings.Contains(rel, "settings.local.json") {
			return "local_overlay"
		}
		return "generated_mirror"
	}
	if strings.HasPrefix(rel, "docs/fleet/.claude/") {
		return "canonical"
	}
	if isRootMCP(rel) || isRepoRootMCP(rel) {
		return "runtime_projection"
	}
	repo := repoForRel(rel)
	meta, ok := repos[repo]
	if !ok {
		if repo == "workspace" {
			return "canonical"
		}
		return "compatibility_reference"
	}
	if meta.Scope == "compatibility_only" || meta.Scope == "upstream_or_reference" || !meta.BaselineTarget && isAgentPrimitive(rel) {
		return "compatibility_reference"
	}
	return "canonical"
}

func finalizePrimitive(primitive *Primitive) {
	sort.Strings(primitive.Errors)
	sort.Strings(primitive.Warnings)
	switch {
	case len(primitive.Errors) > 0 && isBlockingClassification(primitive.Classification):
		primitive.Status = "fail"
	case len(primitive.Errors) > 0 || len(primitive.Warnings) > 0:
		primitive.Status = "warn"
	default:
		primitive.Status = "pass"
	}
}

func addIssue(primitive *Primitive, message string) {
	if isBlockingClassification(primitive.Classification) {
		primitive.Errors = append(primitive.Errors, message)
	} else {
		primitive.Warnings = append(primitive.Warnings, message)
	}
}

func addLocalOverlayPermissionWarnings(primitive *Primitive, raw map[string]any) {
	if primitive.Classification != "local_overlay" {
		return
	}
	if enabled, ok := raw["enableAllProjectMcpServers"].(bool); ok && enabled {
		primitive.Warnings = append(primitive.Warnings, "enableAllProjectMcpServers is true")
	}
	permissions, _ := raw["permissions"].(map[string]any)
	for _, entry := range stringArray(permissions["allow"]) {
		if isHighRiskPermission(entry) {
			primitive.Warnings = append(primitive.Warnings, "high-risk local permission: "+entry)
		}
	}
}

func validateHookCommand(root, baseDir string, primitive *Primitive, command string) {
	target, viaShell := resolveCommandPath(root, baseDir, command)
	if target == "" {
		primitive.Warnings = append(primitive.Warnings, "hook command path is not statically resolvable")
		return
	}
	primitive.Metadata = ensureMetadata(primitive.Metadata)
	primitive.Metadata["resolved_command_path"] = relPath(root, target)
	info, err := os.Stat(target)
	if err != nil {
		addIssue(primitive, "hook command target missing: "+relPath(root, target))
		return
	}
	if info.IsDir() {
		addIssue(primitive, "hook command target is a directory: "+relPath(root, target))
		return
	}
	if !viaShell && info.Mode()&0o111 == 0 {
		addIssue(primitive, "hook command target is not executable: "+relPath(root, target))
	}
}

func resolveCommandPath(root, baseDir, command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", false
	}
	viaShell := false
	candidate := fields[0]
	if isEnvAssignment(candidate) && len(fields) > 1 {
		fields = fields[1:]
		candidate = fields[0]
	}
	if isShellRunner(candidate) && len(fields) > 1 {
		viaShell = true
		candidate = fields[1]
	}
	candidate = strings.Trim(candidate, `"'`)
	switch {
	case strings.HasPrefix(candidate, "$CLAUDE_PROJECT_DIR/"):
		if baseDir == "" {
			baseDir = root
		}
		return filepath.Join(baseDir, strings.TrimPrefix(candidate, "$CLAUDE_PROJECT_DIR/")), viaShell
	case strings.HasPrefix(candidate, "${CLAUDE_PROJECT_DIR}/"):
		if baseDir == "" {
			baseDir = root
		}
		return filepath.Join(baseDir, strings.TrimPrefix(candidate, "${CLAUDE_PROJECT_DIR}/")), viaShell
	case filepath.IsAbs(candidate):
		return candidate, viaShell
	case strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "../"):
		if baseDir == "" {
			baseDir = root
		}
		return filepath.Clean(filepath.Join(baseDir, candidate)), viaShell
	default:
		return "", viaShell
	}
}

func hookCommandBase(root, settingsRel string) string {
	if strings.HasPrefix(settingsRel, ".claude/") {
		return root
	}
	if idx := strings.Index(settingsRel, "/.claude/"); idx >= 0 {
		return filepath.Join(root, filepath.FromSlash(settingsRel[:idx]))
	}
	return root
}

func summarize(primitives []Primitive) Summary {
	summary := Summary{
		Total:      len(primitives),
		ByKind:     map[string]int{},
		ByProvider: map[string]int{},
	}
	for _, primitive := range primitives {
		summary.ByKind[primitive.Kind]++
		if primitive.Provider != "" {
			summary.ByProvider[primitive.Provider]++
		}
		if primitive.Status == "fail" {
			summary.Failed++
			if isBlockingClassification(primitive.Classification) {
				summary.BlockingPrimitiveFailures++
			}
		}
		if len(primitive.Warnings) > 0 {
			summary.Warnings += len(primitive.Warnings)
		}
		switch primitive.Classification {
		case "canonical":
			summary.Canonical++
		case "generated_mirror":
			summary.GeneratedMirrors++
		case "local_overlay":
			summary.LocalOverlays++
		case "runtime_projection":
			summary.RuntimeProjections++
		case "compatibility_reference":
			summary.CompatibilityReferences++
		case "ignored_archive":
			summary.IgnoredArchives++
		}
	}
	return summary
}

func shouldSkipDir(rel string) bool {
	if rel == "." {
		return false
	}
	base := pathBase(rel)
	switch base {
	case ".git", ".tools", "node_modules", ".venv", "vendor", "third_party", ".graveyard", ".codex-worktrees", ".scratch":
		return true
	}
	if strings.HasPrefix(base, "s2f_") {
		return true
	}
	return rel == "vault" ||
		rel == ".config" ||
		rel == "docs/_site" ||
		strings.Contains(rel, "/data/appdata") ||
		rel == "docs/repo-claude" ||
		rel == "docs/research" ||
		rel == "docs/agent-parity" ||
		strings.HasPrefix(rel, "docs/agent-parity/") ||
		strings.HasPrefix(rel, "agent-parity/") ||
		strings.HasPrefix(rel, ".ralph/probe/") ||
		strings.Contains(rel, "/.ralph/probe/") ||
		strings.Contains(rel, "/.ralph/worktrees")
}

func shouldSkipFile(rel string) bool {
	return strings.Contains(rel, "/node_modules/") ||
		strings.Contains(rel, "/.tools/") ||
		strings.Contains(rel, "/.venv/") ||
		strings.Contains(rel, "/third_party/") ||
		strings.Contains(rel, "/.claude/worktrees/") ||
		strings.Contains(rel, "/.codex-worktrees/") ||
		strings.Contains(rel, "/.scratch/") ||
		strings.HasPrefix(rel, "vault/") ||
		strings.HasPrefix(rel, ".config/") ||
		strings.HasPrefix(rel, ".graveyard/") ||
		strings.HasPrefix(rel, "docs/_site/") ||
		strings.Contains(rel, "/data/appdata/") ||
		strings.HasPrefix(rel, "docs/repo-claude/") ||
		strings.HasPrefix(rel, "docs/research/") ||
		strings.HasPrefix(rel, "docs/agent-parity/") ||
		strings.HasPrefix(rel, "agent-parity/") ||
		strings.HasPrefix(rel, "s2f_") ||
		strings.Contains(rel, "/s2f_") ||
		strings.HasPrefix(rel, ".ralph/probe/") ||
		strings.Contains(rel, "/.ralph/probe/") ||
		strings.Contains(rel, "/.ralph/worktrees/")
}

func isClaudeSettings(rel string) bool {
	return strings.HasSuffix(rel, ".claude/settings.json") || strings.HasSuffix(rel, ".claude/settings.local.json")
}

func isClaudeHookScript(rel string) bool {
	return strings.Contains(rel, ".claude/hooks/") && strings.HasSuffix(rel, ".sh")
}

func isClaudeAgent(rel string) bool {
	return strings.Contains(rel, ".claude/agents/") && strings.HasSuffix(rel, ".md")
}

func isClaudeOutputStyle(rel string) bool {
	return strings.Contains(rel, ".claude/output-styles/") && strings.HasSuffix(rel, ".md")
}

func isClaudeSkill(rel string) bool {
	return strings.Contains(rel, ".claude/skills/") && strings.HasSuffix(rel, "/SKILL.md")
}

func isCodexSkill(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) >= 4 && parts[1] == "skills" && parts[len(parts)-1] == "SKILL.md" {
		return true
	}
	return strings.Contains(rel, ".codex/skills/") && strings.HasSuffix(rel, "/SKILL.md")
}

func isCodexSkillMetadata(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) >= 5 && parts[1] == "skills" && parts[len(parts)-2] == "agents" && parts[len(parts)-1] == "openai.yaml" {
		return true
	}
	return strings.Contains(rel, ".codex/skills/") && strings.HasSuffix(rel, "/agents/openai.yaml")
}

func isCodexAgent(rel string) bool {
	return strings.Contains(rel, ".codex/agents/") && strings.HasSuffix(rel, ".toml")
}

func isCodexHooks(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) == 2 && parts[1] == "hooks.json" {
		return true
	}
	return strings.HasSuffix(rel, "/.codex/hooks.json")
}

func isGeminiCommand(rel string) bool {
	return strings.Contains(rel, ".gemini/commands/") && strings.HasSuffix(rel, ".toml")
}

func isGeminiExtension(rel string) bool {
	return strings.Contains(rel, ".gemini/extensions/")
}

func isInstructionFile(rel string) bool {
	switch pathBase(rel) {
	case "AGENTS.md", "CLAUDE.md", "GEMINI.md", "copilot-instructions.md":
		return true
	default:
		return false
	}
}

func isAgentPrimitive(rel string) bool {
	return isClaudeSettings(rel) || isClaudeHookScript(rel) || isClaudeAgent(rel) ||
		isClaudeOutputStyle(rel) || isClaudeSkill(rel) || isCodexSkill(rel) ||
		isCodexSkillMetadata(rel) || isCodexAgent(rel) || isCodexHooks(rel) ||
		isGeminiCommand(rel) || isGeminiExtension(rel) ||
		strings.HasSuffix(rel, ".codex-plugin/plugin.json") ||
		strings.HasSuffix(rel, ".mcp.json") ||
		isInstructionFile(rel)
}

func isRootMCP(rel string) bool {
	return rel == ".mcp.json"
}

func isRepoRootMCP(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) == 2 && parts[1] == ".mcp.json"
}

func isGeneratedMirror(root, rel string) bool {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	if len(data) > 4096 {
		data = data[:4096]
	}
	return bytes.Contains(data, []byte("GENERATED BY"))
}

func metadataFromFrontmatter(root, rel string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	out := map[string]string{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "name", "description", "model":
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerForInstruction(rel string) string {
	switch pathBase(rel) {
	case "CLAUDE.md":
		return "claude"
	case "GEMINI.md":
		return "gemini"
	case "copilot-instructions.md":
		return "github"
	default:
		return "agent"
	}
}

func repoForRel(rel string) string {
	if rel == ".mcp.json" || strings.HasPrefix(rel, ".claude/") {
		return "workspace"
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func isHookEvent(value string) bool {
	switch value {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest",
		"Stop", "StopFailure", "SessionStart", "SessionEnd", "UserPromptSubmit",
		"PreCompact", "Notification", "SubagentStart", "SubagentStop",
		"TaskStart", "TaskFinish", "TaskError", "ConfigChange", "DirectoryChange",
		"FileChange", "Elicitation":
		return true
	default:
		return false
	}
}

func isBlockingClassification(classification string) bool {
	switch classification {
	case "canonical", "generated_mirror", "runtime_projection":
		return true
	default:
		return false
	}
}

func isHighRiskPermission(value string) bool {
	risk := []string{
		"sudo", "rm ", "rm -", "kill", "pkill", "pacman", "yay", "git commit", "git add", "git push", "tee /", "dd ",
	}
	for _, token := range risk {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func isShellRunner(value string) bool {
	base := pathBase(value)
	return base == "bash" || base == "sh" || base == "zsh"
}

func isEnvAssignment(value string) bool {
	return strings.Contains(value, "=") && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "./")
}

func stringArray(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func ensureMetadata(metadata map[string]string) map[string]string {
	if metadata != nil {
		return metadata
	}
	return map[string]string{}
}

func readJSONArtifact(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return index, nil
}

func checkJSONArtifact(report *CheckReport, index Index, path string) {
	if path == "" {
		report.add("json_artifact", false, "no workspace-agent-primitives-*.json artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("json_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected, err := MarshalJSON(index)
	if err != nil {
		report.add("json_artifact", false, err.Error())
		return
	}
	if !bytes.Equal(actual, expected) {
		report.add("json_artifact", false, fmt.Sprintf("%s does not match live workspace agent primitive index", path))
		return
	}
	report.add("json_artifact", true, path)
}

func checkMarkdownArtifact(report *CheckReport, index Index, path string) {
	if path == "" {
		report.add("markdown_artifact", false, "no workspace-agent-primitives-*.md artifact found")
		return
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		report.add("markdown_artifact", false, fmt.Sprintf("reading %s: %v", path, err))
		return
	}
	expected := []byte(RenderMarkdown(index))
	if !bytes.Equal(actual, expected) {
		report.add("markdown_artifact", false, fmt.Sprintf("%s does not match live workspace agent primitive index", path))
		return
	}
	report.add("markdown_artifact", true, path)
}

func latestArtifact(root, ext string) string {
	matches, err := filepath.Glob(filepath.Join(root, "docs", "inventory", "workspace-agent-primitives-*"+ext))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func markdownPathForJSON(jsonPath string) string {
	if jsonPath == "" {
		return ""
	}
	if strings.HasSuffix(jsonPath, ".json") {
		return strings.TrimSuffix(jsonPath, ".json") + ".md"
	}
	return jsonPath + ".md"
}

func attentionPrimitives(primitives []Primitive) []Primitive {
	out := make([]Primitive, 0)
	for _, primitive := range primitives {
		if primitive.Status == "pass" {
			continue
		}
		out = append(out, primitive)
	}
	return out
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysAny(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func artifactDate(generatedAt string) string {
	if t, err := time.Parse(time.RFC3339, generatedAt); err == nil {
		return t.Format("2006-01-02")
	}
	if len(generatedAt) >= len("2006-01-02") {
		return generatedAt[:len("2006-01-02")]
	}
	return time.Now().UTC().Format("2006-01-02")
}

func relPath(root, path string) string {
	if root == "" || path == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func pathBase(path string) string {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func emptyLabel(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
