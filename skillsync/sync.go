// Package skillsync synchronizes canonical .agents skills onto the
// managed Claude and plugin mirrors used across the workspace.
package skillsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hairglasses-studio/codexkit"
	"gopkg.in/yaml.v3"
)

type syncMode string

const (
	modeWrite  syncMode = "write"
	modeDryRun syncMode = "dry-run"
	modeCheck  syncMode = "check"
)

// ValidatorMode controls how external Agent Skills validators are used.
type ValidatorMode string

const (
	ValidatorAuto   ValidatorMode = "auto"
	ValidatorHost   ValidatorMode = "host"
	ValidatorPinned ValidatorMode = "pinned"
	ValidatorOff    ValidatorMode = "off"
)

// Options controls skill sync and check behavior.
type Options struct {
	ValidatorMode ValidatorMode
}

// SyncAction describes one mirror operation.
type SyncAction struct {
	Action  string `json:"action"`
	SrcPath string `json:"src,omitempty"`
	DstPath string `json:"dst,omitempty"`
	Message string `json:"message"`
}

// SyncReport captures a full sync or check run.
type SyncReport struct {
	RepoPath          string       `json:"repo_path"`
	DryRun            bool         `json:"dry_run"`
	PendingChanges    bool         `json:"pending_changes"`
	ValidationUsed    bool         `json:"validation_used"`
	ValidationCommand string       `json:"validation_command,omitempty"`
	Actions           []SyncAction `json:"actions"`
	Errors            []string     `json:"errors,omitempty"`
	Warnings          []string     `json:"warnings,omitempty"`
}

type SkillAlias struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SkillEntry describes one canonical skill export rule from surface.yaml.
type SkillEntry struct {
	Name                   string       `json:"name"`
	ExportPlugin           bool         `json:"export_plugin"`
	ClaudeIncludeCanonical bool         `json:"claude_include_canonical"`
	ClaudeAliases          []SkillAlias `json:"claude_aliases,omitempty"`
}

type skillFrontmatter struct {
	Values map[string]string
	Lists  map[string][]string
}

// Surface is the parsed skill surface definition.
type Surface struct {
	Version           int          `json:"version"`
	PluginRoot        string       `json:"plugin_root,omitempty"`
	ExportAllToPlugin bool         `json:"export_all_to_plugin,omitempty"`
	ClaudeManaged     bool         `json:"claude_managed"`
	PluginManaged     bool         `json:"plugin_managed"`
	Skills            []SkillEntry `json:"skills"`
}

type rawSurface struct {
	Version           int             `json:"version"`
	PluginRoot        string          `json:"plugin_root,omitempty"`
	ExportAllToPlugin bool            `json:"export_all_to_plugin,omitempty"`
	ClaudeManaged     *bool           `json:"claude_managed,omitempty"`
	PluginManaged     *bool           `json:"plugin_managed,omitempty"`
	Skills            []rawSkillEntry `json:"skills"`
}

type rawSkillEntry struct {
	Name                   string            `json:"name"`
	ExportPlugin           *bool             `json:"export_plugin,omitempty"`
	ClaudeIncludeCanonical *bool             `json:"claude_include_canonical,omitempty"`
	ClaudeAliases          []json.RawMessage `json:"claude_aliases,omitempty"`
}

var projectedFrontmatterKeyOrder = []string{
	"compatibility",
	"license",
	"user-invocable",
	"argument-hint",
	"disable-model-invocation",
	"reload",
	"triggers",
	"capabilities",
	"see_also",
	"paths",
	"context",
	"metadata",
	"agent",
	"effort",
	"model",
}

type skillProjection struct {
	Name         string
	Description  string
	AllowedTools []string
	Frontmatter  map[string]any
	Body         string
}

var agentSkillsValidatorFrontmatterKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"allowed-tools": true,
	"compatibility": true,
	"license":       true,
	"metadata":      true,
}

// ParseSurface reads and parses the surface definition.
func ParseSurface(repoPath string) (*Surface, error) {
	surfacePath := filepath.Join(repoPath, ".agents", "skills", "surface.yaml")
	data, err := os.ReadFile(surfacePath)
	if err != nil {
		return nil, fmt.Errorf("reading surface.yaml: %w", err)
	}

	var raw rawSurface
	if err := json.Unmarshal(data, &raw); err != nil {
		parsed, parseErr := parseSimpleYAMLSurface(data)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid surface.yaml: %w", err)
		}
		return parsed, nil
	}
	return normalizeSurface(raw, filepath.Base(repoPath))
}

func normalizeSurface(raw rawSurface, defaultPluginRoot string) (*Surface, error) {
	if !supportedSurfaceVersion(raw.Version) {
		return nil, fmt.Errorf("unsupported surface version: %d", raw.Version)
	}
	surface := &Surface{
		Version:           raw.Version,
		PluginRoot:        raw.PluginRoot,
		ExportAllToPlugin: raw.ExportAllToPlugin,
		ClaudeManaged:     true,
		PluginManaged:     true,
		Skills:            make([]SkillEntry, 0, len(raw.Skills)),
	}
	if surface.PluginRoot == "" {
		surface.PluginRoot = defaultPluginRoot
	}
	if raw.ClaudeManaged != nil {
		surface.ClaudeManaged = *raw.ClaudeManaged
	}
	if raw.PluginManaged != nil {
		surface.PluginManaged = *raw.PluginManaged
	}
	for _, entry := range raw.Skills {
		if strings.TrimSpace(entry.Name) == "" {
			return nil, fmt.Errorf("surface.yaml skill entry is missing name")
		}
		normalized := SkillEntry{
			Name:                   entry.Name,
			ExportPlugin:           surface.ExportAllToPlugin,
			ClaudeIncludeCanonical: true,
			ClaudeAliases:          make([]SkillAlias, 0, len(entry.ClaudeAliases)),
		}
		if entry.ExportPlugin != nil {
			normalized.ExportPlugin = *entry.ExportPlugin
		}
		if entry.ClaudeIncludeCanonical != nil {
			normalized.ClaudeIncludeCanonical = *entry.ClaudeIncludeCanonical
		}
		for _, rawAlias := range entry.ClaudeAliases {
			var aliasName string
			if err := json.Unmarshal(rawAlias, &aliasName); err == nil {
				normalized.ClaudeAliases = append(normalized.ClaudeAliases, SkillAlias{
					Name:        aliasName,
					Description: fmt.Sprintf("Compatibility alias for the %s workflow.", entry.Name),
				})
				continue
			}
			var alias SkillAlias
			if err := json.Unmarshal(rawAlias, &alias); err != nil {
				return nil, fmt.Errorf("decode claude_alias for %s: %w", entry.Name, err)
			}
			if strings.TrimSpace(alias.Name) == "" {
				return nil, fmt.Errorf("invalid claude_aliases entry for %s: missing name", entry.Name)
			}
			if strings.TrimSpace(alias.Description) == "" {
				alias.Description = fmt.Sprintf("Compatibility alias for the %s workflow.", entry.Name)
			}
			normalized.ClaudeAliases = append(normalized.ClaudeAliases, alias)
		}
		surface.Skills = append(surface.Skills, normalized)
	}
	return surface, nil
}

func supportedSurfaceVersion(version int) bool {
	return version == 1 || version == 2
}

func parseSimpleYAMLSurface(data []byte) (*Surface, error) {
	lines := strings.Split(string(data), "\n")
	surface := &Surface{
		ClaudeManaged: true,
		PluginManaged: true,
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "version: 1":
			surface.Version = 1
		case trimmed == "version: 2":
			surface.Version = 2
		case strings.HasPrefix(trimmed, "plugin_root:"):
			surface.PluginRoot = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "plugin_root:")), `"'`)
		case trimmed == "export_all_to_plugin: true":
			surface.ExportAllToPlugin = true
		case trimmed == "claude_managed: false":
			surface.ClaudeManaged = false
		case trimmed == "plugin_managed: false":
			surface.PluginManaged = false
		case strings.HasPrefix(trimmed, "- name:"):
			name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")), `"'`)
			surface.Skills = append(surface.Skills, SkillEntry{
				Name:                   name,
				ExportPlugin:           surface.ExportAllToPlugin,
				ClaudeIncludeCanonical: true,
			})
		}
	}
	if !supportedSurfaceVersion(surface.Version) {
		return nil, fmt.Errorf("unsupported surface version: %d", surface.Version)
	}
	return surface, nil
}

// FilterPortableFrontmatter keeps only the portable keys used by managed
// plugin mirrors.
func FilterPortableFrontmatter(content string) string {
	return filterFrontmatter(content, codexkit.PortableFrontmatterKeys)
}

func filterValidatorFrontmatter(content string) string {
	return filterFrontmatter(content, agentSkillsValidatorFrontmatterKeys)
}

func filterFrontmatter(content string, allowedKeys map[string]bool) string {
	lines, bodyStart, ok := splitFrontmatter([]byte(content))
	if !ok {
		return content
	}
	var b strings.Builder
	b.WriteString("---\n")
	inTools := false
	inMetadata := false
	inBlock := false
	wroteTools := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if lineHasIndent(line) {
			if inTools && strings.HasPrefix(trimmed, "- ") {
				if !wroteTools {
					b.WriteString("allowed-tools:\n")
					wroteTools = true
				}
				b.WriteString("  ")
				b.WriteString(trimmed)
				b.WriteString("\n")
			} else if inMetadata || inBlock {
				b.WriteString(line)
				b.WriteString("\n")
			}
			continue
		}
		inMetadata = false
		inBlock = false
		if strings.HasPrefix(trimmed, "- ") {
			if inTools {
				if !wroteTools {
					b.WriteString("allowed-tools:\n")
					wroteTools = true
				}
				b.WriteString("  ")
				b.WriteString(strings.TrimSpace(line))
				b.WriteString("\n")
			}
			continue
		}
		inTools = false
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if !allowedKeys[key] {
			continue
		}
		if key == "allowed-tools" {
			inTools = true
			if tools := parseInlineTools(strings.TrimSpace(parts[1])); len(tools) > 0 {
				if !wroteTools {
					b.WriteString("allowed-tools:\n")
					wroteTools = true
				}
				for _, tool := range tools {
					b.WriteString("  - ")
					b.WriteString(tool)
					b.WriteString("\n")
				}
			}
			continue
		}
		inMetadata = key == "metadata"
		inBlock = isYAMLBlockScalar(strings.TrimSpace(parts[1]))
		b.WriteString(line)
		b.WriteString("\n")
	}
	body := string([]byte(content)[bodyStart:])
	body = strings.TrimLeft(body, "\n")
	if b.Len() == len("---\n") {
		return body
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString(body)
	}
	return b.String()
}

// Sync writes the managed skill mirrors.
func Sync(repoPath string, dryRun bool) SyncReport {
	if dryRun {
		return run(repoPath, modeDryRun, Options{})
	}
	return run(repoPath, modeWrite, Options{})
}

// SyncWithOptions writes managed skill mirrors with explicit options.
func SyncWithOptions(repoPath string, dryRun bool, opts Options) SyncReport {
	if dryRun {
		return run(repoPath, modeDryRun, opts)
	}
	return run(repoPath, modeWrite, opts)
}

// Diff returns the dry-run report.
func Diff(repoPath string) SyncReport {
	return run(repoPath, modeDryRun, Options{})
}

// Check verifies that the managed skill mirrors are current.
func Check(repoPath string) SyncReport {
	return run(repoPath, modeCheck, Options{})
}

// CheckWithOptions verifies that managed skill mirrors are current.
func CheckWithOptions(repoPath string, opts Options) SyncReport {
	return run(repoPath, modeCheck, opts)
}

// List returns skill names from the surface definition.
func List(repoPath string) ([]string, error) {
	surface, err := ParseSurface(repoPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(surface.Skills))
	for _, entry := range surface.Skills {
		names = append(names, entry.Name)
	}
	return names, nil
}

func normalizedSkillName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func run(repoPath string, mode syncMode, opts Options) SyncReport {
	report := SyncReport{
		RepoPath: repoPath,
		DryRun:   mode != modeWrite,
	}

	surface, err := ParseSurface(repoPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	report.RepoPath = absRepoPath

	claudeDirs := map[string]struct{}{}
	pluginDirs := map[string]struct{}{}
	validator, validationAvailable, validationErr := resolveSkillsValidator(opts.ValidatorMode)
	report.ValidationUsed = validationAvailable
	report.ValidationCommand = validator.Display
	if validationErr != nil {
		report.Errors = append(report.Errors, validationErr.Error())
	} else if !validationAvailable && normalizedValidatorMode(opts.ValidatorMode) == ValidatorAuto {
		report.Warnings = append(report.Warnings, "skills-ref/agentskills not found; skipped canonical skill validation")
	}

	for _, skill := range surface.Skills {
		canonicalDir := filepath.Join(absRepoPath, ".agents", "skills", skill.Name)
		canonicalPath := filepath.Join(canonicalDir, "SKILL.md")
		content, err := os.ReadFile(canonicalPath)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("missing canonical skill %s: %v", canonicalPath, err))
			continue
		}
		if err := validatePortableFrontmatter(canonicalPath, content); err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		if validationAvailable {
			if err := validateWithSkillsRef(validator, skill.Name, canonicalDir, content); err != nil {
				report.Errors = append(report.Errors, err.Error())
				continue
			}
		}

		projection, err := parseSkillProjection(content)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", canonicalPath, err))
			continue
		}
		if projection.Name != skill.Name {
			report.Errors = append(report.Errors, fmt.Sprintf("canonical skill name mismatch in %s (expected %s, found %s)", canonicalPath, skill.Name, projection.Name))
			continue
		}

		if skill.ClaudeIncludeCanonical {
			if err := registerDir(claudeDirs, skill.Name, "managed Claude skill"); err != nil {
				report.Errors = append(report.Errors, err.Error())
			} else {
				rendered := renderSkill(skill.Name, projection.Description, skill.Name, projection, surface.Version >= 2, fmt.Sprintf("Compatibility mirror of the canonical `%s` skill.", skill.Name))
				target := filepath.Join(absRepoPath, ".claude", "skills", skill.Name, "SKILL.md")
				syncContent(&report, mode, canonicalPath, target, rendered, "Claude skill")
			}
		}

		claudeAliases := make([]SkillAlias, 0, len(skill.ClaudeAliases)+1)
		seenClaudeAliases := make(map[string]struct{}, len(skill.ClaudeAliases)+1)
		for _, alias := range skill.ClaudeAliases {
			claudeAliases = append(claudeAliases, alias)
			seenClaudeAliases[alias.Name] = struct{}{}
		}
		if normalized := normalizedSkillName(skill.Name); normalized != skill.Name {
			if _, ok := seenClaudeAliases[normalized]; !ok {
				claudeAliases = append(claudeAliases, SkillAlias{
					Name:        normalized,
					Description: fmt.Sprintf("Hyphenated compatibility alias for the %s workflow.", skill.Name),
				})
			}
		}

		for _, alias := range claudeAliases {
			if err := registerDir(claudeDirs, alias.Name, "managed Claude skill"); err != nil {
				report.Errors = append(report.Errors, err.Error())
				continue
			}
			rendered := renderSkill(alias.Name, alias.Description, skill.Name, projection, surface.Version >= 2, fmt.Sprintf("Compatibility alias for the canonical `%s` skill.", skill.Name))
			target := filepath.Join(absRepoPath, ".claude", "skills", alias.Name, "SKILL.md")
			syncContent(&report, mode, canonicalPath, target, rendered, "Claude skill")
		}

		if skill.ExportPlugin {
			if err := registerDir(pluginDirs, skill.Name, "managed plugin skill"); err != nil {
				report.Errors = append(report.Errors, err.Error())
			} else {
				rendered := renderSkill(skill.Name, projection.Description, skill.Name, projection, surface.Version >= 2, "")
				target := filepath.Join(absRepoPath, "plugins", surface.PluginRoot, "skills", skill.Name, "SKILL.md")
				syncContent(&report, mode, canonicalPath, target, rendered, "plugin skill")
			}
		}
	}

	if len(report.Errors) == 0 {
		purgeSkillDirs(&report, mode, filepath.Join(absRepoPath, ".claude", "skills"), "Claude skill", surface.ClaudeManaged, claudeDirs)
		purgeSkillDirs(&report, mode, filepath.Join(absRepoPath, "plugins", surface.PluginRoot, "skills"), "plugin skill", surface.PluginManaged, pluginDirs)
	}

	return report
}

func registerDir(registry map[string]struct{}, name, label string) error {
	if _, ok := registry[name]; ok {
		return fmt.Errorf("duplicate %s name in surface manifest: %s", label, name)
	}
	registry[name] = struct{}{}
	return nil
}

func syncContent(report *SyncReport, mode syncMode, srcPath, dstPath, rendered, label string) {
	existing, err := os.ReadFile(dstPath)
	if err == nil && bytes.Equal(existing, []byte(rendered)) {
		report.Actions = append(report.Actions, SyncAction{
			Action:  "unchanged",
			SrcPath: srcPath,
			DstPath: dstPath,
			Message: fmt.Sprintf("%s current: %s", label, filepath.Base(filepath.Dir(dstPath))),
		})
		return
	}

	action := "update"
	if errorsIsNotExist(err) {
		action = "create"
	}
	report.PendingChanges = true
	report.Actions = append(report.Actions, SyncAction{
		Action:  action,
		SrcPath: srcPath,
		DstPath: dstPath,
		Message: fmt.Sprintf("%s %s: %s", action, label, dstPath),
	})
	if mode != modeWrite {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("mkdir %s: %v", filepath.Dir(dstPath), err))
		return
	}
	if fi, err := os.Lstat(dstPath); err == nil && (fi.Mode()&os.ModeSymlink != 0 || fi.IsDir()) {
		_ = os.RemoveAll(dstPath)
	}
	if err := os.WriteFile(dstPath, []byte(rendered), 0o644); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write %s: %v", dstPath, err))
	}
}

func purgeSkillDirs(report *SyncReport, mode syncMode, baseDir, label string, managed bool, expected map[string]struct{}) {
	if !managed {
		return
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if errorsIsNotExist(err) {
			return
		}
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", baseDir, err))
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if _, ok := expected[name]; ok {
			continue
		}
		target := filepath.Join(baseDir, name)
		report.PendingChanges = true
		report.Actions = append(report.Actions, SyncAction{
			Action:  "remove",
			DstPath: target,
			Message: fmt.Sprintf("remove stale %s: %s", label, target),
		})
		if mode == modeWrite {
			if err := os.RemoveAll(target); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("remove %s: %v", target, err))
			}
		}
	}
}

func validatePortableFrontmatter(path string, content []byte) error {
	lines, _, ok := splitFrontmatter(content)
	if !ok {
		return fmt.Errorf("missing frontmatter in %s", path)
	}
	inList := false
	inMetadata := false
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if lineHasIndent(line) {
			if inList && strings.HasPrefix(trimmed, "- ") {
				continue
			}
			if inMetadata || inBlock {
				continue
			}
			return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
		}
		inMetadata = false
		inBlock = false
		if strings.HasPrefix(trimmed, "- ") {
			if !inList {
				return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
			}
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
		}
		key := strings.TrimSpace(parts[0])
		if !codexkit.SkillSourceFrontmatterKeys[key] {
			return fmt.Errorf("non-portable frontmatter in %s: %s", path, key)
		}
		value := strings.TrimSpace(parts[1])
		if key == "allowed-tools" && value != "" {
			return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
		}
		inList = value == "" && isYAMLListFrontmatterKey(key)
		inMetadata = key == "metadata"
		inBlock = isYAMLBlockScalar(value)
	}
	return nil
}

func lineHasIndent(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func isYAMLBlockScalar(value string) bool {
	return value == "|" || value == "|-" || value == "|+" || value == ">" || value == ">-" || value == ">+"
}

func isYAMLListFrontmatterKey(key string) bool {
	switch key {
	case "allowed-tools", "triggers", "see_also", "capabilities", "paths":
		return true
	default:
		return false
	}
}

func parseInlineTools(value string) []string {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		tool := strings.Trim(strings.TrimSpace(part), `"'`)
		if tool != "" {
			tools = append(tools, tool)
		}
	}
	return tools
}

func parseSkill(content []byte) (name, description string, tools []string, body string, err error) {
	projection, err := parseSkillProjection(content)
	if err != nil {
		return "", "", nil, "", err
	}
	return projection.Name, projection.Description, projection.AllowedTools, projection.Body, nil
}

func parseSkillProjection(content []byte) (skillProjection, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return skillProjection{}, fmt.Errorf("missing SKILL frontmatter")
	}
	end := strings.Index(text[len("---\n"):], "\n---")
	if end < 0 {
		return skillProjection{}, fmt.Errorf("unterminated frontmatter")
	}
	frontmatterEnd := len("---\n") + end
	bodyStart := frontmatterEnd + len("\n---")
	if bodyStart < len(text) && text[bodyStart] == '\n' {
		bodyStart++
	}
	frontmatter := text[len("---\n"):frontmatterEnd]

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return skillProjection{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	name := yamlString(raw["name"])
	description := yamlString(raw["description"])
	tools := yamlStringList(raw["allowed-tools"])
	if len(tools) == 0 {
		tools = yamlStringList(raw["tools"])
	}
	if name == "" {
		return skillProjection{}, fmt.Errorf("missing name frontmatter")
	}
	if description == "" {
		return skillProjection{}, fmt.Errorf("missing description frontmatter")
	}
	return skillProjection{
		Name:         name,
		Description:  description,
		AllowedTools: tools,
		Frontmatter:  raw,
		Body:         strings.TrimLeft(text[bodyStart:], "\n"),
	}, nil
}

func renderSkill(name, description, canonicalName string, projection skillProjection, includeSourceKeys bool, banner string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(yamlQuote(description))
	b.WriteString("\n")
	if includeSourceKeys {
		for _, key := range projectedFrontmatterKeyOrder {
			value, ok := projection.Frontmatter[key]
			if !ok || !codexkit.PortableFrontmatterKeys[key] {
				continue
			}
			writeYAMLFrontmatterValue(&b, key, value)
		}
	}
	if len(projection.AllowedTools) > 0 {
		b.WriteString("allowed-tools:\n")
		for _, tool := range projection.AllowedTools {
			if strings.TrimSpace(tool) == "" {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(tool)
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString("<!-- GENERATED BY hg-skill-surface-sync.sh FROM .agents/skills/")
	b.WriteString(canonicalName)
	b.WriteString("/SKILL.md; DO NOT EDIT -->\n")
	if banner != "" {
		b.WriteString("\n")
		b.WriteString(banner)
		b.WriteString("\n\n\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(projection.Body)
	if !strings.HasSuffix(projection.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func writeSourceFrontmatterValue(b *strings.Builder, frontmatter skillFrontmatter, key string) {
	value, ok := frontmatter.Values[key]
	if !ok || strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	if value == "true" || value == "false" {
		b.WriteString(value)
	} else {
		b.WriteString(yamlQuote(value))
	}
	b.WriteString("\n")
}

func writeSourceFrontmatterList(b *strings.Builder, frontmatter skillFrontmatter, key string) {
	values := frontmatter.Lists[key]
	if len(values) == 0 {
		return
	}
	b.WriteString(key)
	b.WriteString(":\n")
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		b.WriteString("  - ")
		b.WriteString(yamlQuote(value))
		b.WriteString("\n")
	}
}

func yamlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func writeYAMLFrontmatterValue(b *strings.Builder, key string, value any) {
	switch typed := value.(type) {
	case string:
		if s := strings.TrimSpace(typed); s != "" {
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(yamlQuote(s))
			b.WriteString("\n")
		}
	case bool:
		b.WriteString(key)
		b.WriteString(": ")
		if typed {
			b.WriteString("true\n")
		} else {
			b.WriteString("false\n")
		}
	case []any:
		if len(typed) == 0 {
			return
		}
		b.WriteString(key)
		b.WriteString(":\n")
		for _, item := range typed {
			if s := yamlString(item); s != "" {
				b.WriteString("  - ")
				b.WriteString(yamlQuote(s))
				b.WriteString("\n")
			}
		}
	case []string:
		if len(typed) == 0 {
			return
		}
		b.WriteString(key)
		b.WriteString(":\n")
		for _, item := range typed {
			if s := strings.TrimSpace(item); s != "" {
				b.WriteString("  - ")
				b.WriteString(yamlQuote(s))
				b.WriteString("\n")
			}
		}
	default:
		data, err := yaml.Marshal(map[string]any{key: value})
		if err == nil {
			b.Write(data)
		}
	}
}

func yamlString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func yamlStringList(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := yamlString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func splitFrontmatter(content []byte) ([]string, int, bool) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, 0, false
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, 0, false
	}
	frontmatter := rest[:end]
	bodyStart := 4 + end + 4
	if bodyStart < len(text) && text[bodyStart] == '\n' {
		bodyStart++
	}
	return strings.Split(frontmatter, "\n"), bodyStart, true
}

func validateWithSkillsRef(validator skillValidator, canonicalName, canonicalDir string, canonicalSkill []byte) error {
	validateDir, cleanup, err := prepareSkillsRefValidationDir(canonicalName, canonicalDir, canonicalSkill)
	if err != nil {
		return err
	}
	defer cleanup()

	args := append([]string{}, validator.Args...)
	args = append(args, validateDir)
	cmd := exec.Command(validator.Command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s validation failed for %s: %s", validator.Display, canonicalName, msg)
	}
	return nil
}

func prepareSkillsRefValidationDir(canonicalName, canonicalDir string, canonicalSkill []byte) (string, func(), error) {
	tmpRoot, err := os.MkdirTemp("", "codexkit-skills-ref-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp dir for skills-ref: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpRoot) }
	compatName := strings.ReplaceAll(canonicalName, "_", "-")
	validateDir := filepath.Join(tmpRoot, compatName)
	if err := copyDir(canonicalDir, validateDir); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("prepare skills-ref copy for %s: %w", canonicalName, err)
	}
	content := filterValidatorFrontmatter(string(canonicalSkill))
	if compatName != canonicalName {
		content = strings.Replace(content, "name: "+canonicalName, "name: "+compatName, 1)
	}
	if err := os.WriteFile(filepath.Join(validateDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("rewrite compat SKILL.md for %s: %w", canonicalName, err)
	}
	return validateDir, cleanup, nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

type skillValidator struct {
	Command string
	Args    []string
	Display string
}

func resolveSkillsValidator(mode ValidatorMode) (skillValidator, bool, error) {
	mode = normalizedValidatorMode(mode)
	switch mode {
	case ValidatorAuto, ValidatorHost, ValidatorPinned, ValidatorOff:
	default:
		return skillValidator{}, false, fmt.Errorf("unknown skill validator mode: %s", mode)
	}
	if mode == ValidatorOff {
		return skillValidator{}, false, nil
	}
	if mode == ValidatorPinned {
		if commandAvailable("uvx") {
			return skillValidator{
				Command: "uvx",
				Args:    []string{"--from", "skills-ref==0.1.1", "agentskills", "validate"},
				Display: "uvx --from skills-ref==0.1.1 agentskills",
			}, true, nil
		}
		if commandAvailable("npx") {
			return skillValidator{
				Command: "npx",
				Args:    []string{"-y", "skills-ref@0.1.5", "validate"},
				Display: "npx -y skills-ref@0.1.5",
			}, true, nil
		}
		return skillValidator{}, false, fmt.Errorf("skill validator mode pinned requires uvx or npx on PATH")
	}

	if command, ok := skillsValidatorCommand(); ok {
		return skillValidator{Command: command, Args: []string{"validate"}, Display: command}, true, nil
	}
	if mode == ValidatorAuto {
		return skillValidator{}, false, nil
	}
	return skillValidator{}, false, fmt.Errorf("skill validator mode host requires skills-ref or agentskills on PATH")
}

func normalizedValidatorMode(mode ValidatorMode) ValidatorMode {
	switch mode {
	case "", ValidatorAuto:
		return ValidatorAuto
	case ValidatorHost, ValidatorPinned, ValidatorOff:
		return mode
	default:
		return mode
	}
}

// ParseValidatorMode parses a CLI/API validator mode string.
func ParseValidatorMode(raw string) (ValidatorMode, error) {
	mode := normalizedValidatorMode(ValidatorMode(raw))
	switch mode {
	case ValidatorAuto, ValidatorHost, ValidatorPinned, ValidatorOff:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown skill validator mode: %s", raw)
	}
}

func skillsValidatorCommand() (string, bool) {
	for _, command := range []string{"skills-ref", "agentskills"} {
		if commandAvailable(command) {
			return command, true
		}
	}
	return "", false
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
