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
)

type syncMode string

const (
	modeWrite  syncMode = "write"
	modeDryRun syncMode = "dry-run"
	modeCheck  syncMode = "check"
)

var portableFrontmatterKeys = map[string]bool{
	"name":          true,
	"description":   true,
	"allowed-tools": true,
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
	RepoPath       string       `json:"repo_path"`
	DryRun         bool         `json:"dry_run"`
	PendingChanges bool         `json:"pending_changes"`
	ValidationUsed bool         `json:"validation_used"`
	Actions        []SyncAction `json:"actions"`
	Errors         []string     `json:"errors,omitempty"`
	Warnings       []string     `json:"warnings,omitempty"`
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

// Surface is the parsed skill surface definition.
type Surface struct {
	Version       int          `json:"version"`
	PluginRoot    string       `json:"plugin_root,omitempty"`
	ClaudeManaged bool         `json:"claude_managed"`
	PluginManaged bool         `json:"plugin_managed"`
	Skills        []SkillEntry `json:"skills"`
}

type rawSurface struct {
	Version       int             `json:"version"`
	PluginRoot    string          `json:"plugin_root,omitempty"`
	ClaudeManaged *bool           `json:"claude_managed,omitempty"`
	PluginManaged *bool           `json:"plugin_managed,omitempty"`
	Skills        []rawSkillEntry `json:"skills"`
}

type rawSkillEntry struct {
	Name                   string            `json:"name"`
	ExportPlugin           *bool             `json:"export_plugin,omitempty"`
	ClaudeIncludeCanonical *bool             `json:"claude_include_canonical,omitempty"`
	ClaudeAliases          []json.RawMessage `json:"claude_aliases,omitempty"`
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
	if raw.Version != 1 {
		return nil, fmt.Errorf("unsupported surface version: %d", raw.Version)
	}
	surface := &Surface{
		Version:       raw.Version,
		PluginRoot:    raw.PluginRoot,
		ClaudeManaged: true,
		PluginManaged: true,
		Skills:        make([]SkillEntry, 0, len(raw.Skills)),
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
			ExportPlugin:           false,
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
		case strings.HasPrefix(trimmed, "plugin_root:"):
			surface.PluginRoot = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "plugin_root:")), `"'`)
		case trimmed == "claude_managed: false":
			surface.ClaudeManaged = false
		case trimmed == "plugin_managed: false":
			surface.PluginManaged = false
		case strings.HasPrefix(trimmed, "- name:"):
			name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")), `"'`)
			surface.Skills = append(surface.Skills, SkillEntry{
				Name:                   name,
				ClaudeIncludeCanonical: true,
			})
		}
	}
	if surface.Version != 1 {
		return nil, fmt.Errorf("unsupported surface version: %d", surface.Version)
	}
	return surface, nil
}

// FilterPortableFrontmatter keeps only the portable keys used by managed
// plugin mirrors.
func FilterPortableFrontmatter(content string) string {
	lines, bodyStart, ok := splitFrontmatter([]byte(content))
	if !ok {
		return content
	}
	var b strings.Builder
	b.WriteString("---\n")
	inTools := false
	wroteTools := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
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
		if !portableFrontmatterKeys[key] {
			continue
		}
		if key == "allowed-tools" {
			inTools = true
			continue
		}
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
		return run(repoPath, modeDryRun)
	}
	return run(repoPath, modeWrite)
}

// Diff returns the dry-run report.
func Diff(repoPath string) SyncReport {
	return run(repoPath, modeDryRun)
}

// Check verifies that the managed skill mirrors are current.
func Check(repoPath string) SyncReport {
	return run(repoPath, modeCheck)
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

func run(repoPath string, mode syncMode) SyncReport {
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
	validationAvailable := commandAvailable("skills-ref")
	report.ValidationUsed = validationAvailable
	if !validationAvailable {
		report.Warnings = append(report.Warnings, "skills-ref not found; skipped canonical skill validation")
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
			if err := validateWithSkillsRef(skill.Name, canonicalDir, content); err != nil {
				report.Errors = append(report.Errors, err.Error())
				continue
			}
		}

		name, description, tools, body, err := parseSkill(content)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", canonicalPath, err))
			continue
		}
		if name != skill.Name {
			report.Errors = append(report.Errors, fmt.Sprintf("canonical skill name mismatch in %s (expected %s, found %s)", canonicalPath, skill.Name, name))
			continue
		}

		if skill.ClaudeIncludeCanonical {
			if err := registerDir(claudeDirs, skill.Name, "managed Claude skill"); err != nil {
				report.Errors = append(report.Errors, err.Error())
			} else {
				rendered := renderSkill(skill.Name, description, skill.Name, tools, body, fmt.Sprintf("Compatibility mirror of the canonical `%s` skill.", skill.Name))
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
			rendered := renderSkill(alias.Name, alias.Description, skill.Name, tools, body, fmt.Sprintf("Compatibility alias for the canonical `%s` skill.", skill.Name))
			target := filepath.Join(absRepoPath, ".claude", "skills", alias.Name, "SKILL.md")
			syncContent(&report, mode, canonicalPath, target, rendered, "Claude skill")
		}

		if skill.ExportPlugin {
			if err := registerDir(pluginDirs, skill.Name, "managed plugin skill"); err != nil {
				report.Errors = append(report.Errors, err.Error())
			} else {
				rendered := renderSkill(skill.Name, description, skill.Name, tools, body, "")
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
	inTools := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if !inTools {
				return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
			}
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("non-portable frontmatter in %s: __INVALID__", path)
		}
		key := strings.TrimSpace(parts[0])
		if !portableFrontmatterKeys[key] {
			return fmt.Errorf("non-portable frontmatter in %s: %s", path, key)
		}
		inTools = key == "allowed-tools"
	}
	return nil
}

func parseSkill(content []byte) (name, description string, tools []string, body string, err error) {
	lines, bodyStart, ok := splitFrontmatter(content)
	if !ok {
		return "", "", nil, "", fmt.Errorf("missing SKILL frontmatter")
	}
	inTools := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if inTools {
				tools = append(tools, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			}
			continue
		}
		inTools = false
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		case "allowed-tools":
			inTools = true
		}
	}
	if strings.TrimSpace(name) == "" {
		return "", "", nil, "", fmt.Errorf("missing name frontmatter")
	}
	if strings.TrimSpace(description) == "" {
		return "", "", nil, "", fmt.Errorf("missing description frontmatter")
	}
	body = string(content[bodyStart:])
	body = strings.TrimLeft(body, "\n")
	return name, description, tools, body, nil
}

func renderSkill(name, description, canonicalName string, tools []string, body, banner string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(yamlQuote(description))
	b.WriteString("\n")
	if len(tools) > 0 {
		b.WriteString("allowed-tools:\n")
		for _, tool := range tools {
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
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func yamlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
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

func validateWithSkillsRef(canonicalName, canonicalDir string, canonicalSkill []byte) error {
	validateDir := canonicalDir
	cleanup := func() {}
	if strings.Contains(canonicalName, "_") {
		tmpRoot, err := os.MkdirTemp("", "codexkit-skills-ref-*")
		if err != nil {
			return fmt.Errorf("create temp dir for skills-ref: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(tmpRoot) }
		compatName := strings.ReplaceAll(canonicalName, "_", "-")
		validateDir = filepath.Join(tmpRoot, compatName)
		if err := copyDir(canonicalDir, validateDir); err != nil {
			cleanup()
			return fmt.Errorf("prepare skills-ref copy for %s: %w", canonicalName, err)
		}
		content := strings.Replace(string(canonicalSkill), "name: "+canonicalName, "name: "+compatName, 1)
		if err := os.WriteFile(filepath.Join(validateDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			cleanup()
			return fmt.Errorf("rewrite compat SKILL.md for %s: %w", canonicalName, err)
		}
	}
	defer cleanup()

	cmd := exec.Command("skills-ref", "validate", validateDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("skills-ref validation failed for %s: %s", canonicalName, msg)
	}
	return nil
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

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
