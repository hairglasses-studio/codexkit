// Package mcpsync synchronizes repo-local MCP definitions into the generated
// Codex config block used by the workspace.
package mcpsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	startMarker = "# BEGIN GENERATED MCP SERVERS: codex-mcp-sync"
	endMarker   = "# END GENERATED MCP SERVERS: codex-mcp-sync"
	ollamaStartMarker = "# BEGIN GENERATED OLLAMA PROFILES: provider-settings-sync"
	ollamaEndMarker = "# END GENERATED OLLAMA PROFILES: provider-settings-sync"
)

var mcpServerBlockRe = regexp.MustCompile(`(?m)^\[mcp_servers\.`)

// MCPServer describes one source server from .mcp.json.
type MCPServer struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	CWD       string            `json:"cwd,omitempty"`
	Transport string            `json:"transport,omitempty"`
	URL       string            `json:"url,omitempty"`
}

type MCPFile struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

type policyFile struct {
	Version  int             `json:"version"`
	Profiles []policyProfile `json:"profiles"`
}

type policyProfile struct {
	Name              string                  `json:"name"`
	From              string                  `json:"from"`
	Mode              string                  `json:"mode,omitempty"`
	Comment           string                  `json:"comment,omitempty"`
	Enabled           *bool                   `json:"enabled,omitempty"`
	Required          *bool                   `json:"required,omitempty"`
	StartupTimeoutSec *int                    `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec    *int                    `json:"tool_timeout_sec,omitempty"`
	EnabledTools      []string                `json:"enabled_tools,omitempty"`
	DisabledTools     []string                `json:"disabled_tools,omitempty"`
	ToolOverrides     map[string]toolOverride `json:"tool_overrides,omitempty"`
	Override          *MCPServer              `json:"override,omitempty"`
}

type toolOverride struct {
	ApprovalMode string `json:"approval_mode,omitempty"`
}

type resolvedProfile struct {
	Name              string
	Comment           string
	Command           string
	Args              []string
	CWD               string
	Env               map[string]string
	Transport         string
	URL               string
	Enabled           *bool
	Required          *bool
	StartupTimeoutSec *int
	ToolTimeoutSec    *int
	EnabledTools      []string
	DisabledTools     []string
	ToolOverrides     map[string]toolOverride
}

// SyncAction describes a single sync operation.
type SyncAction struct {
	Action  string `json:"action"`
	Server  string `json:"server,omitempty"`
	Message string `json:"message"`
}

// SyncReport is the result of an MCP sync operation.
type SyncReport struct {
	RepoPath       string       `json:"repo_path"`
	DryRun         bool         `json:"dry_run"`
	PendingChanges bool         `json:"pending_changes"`
	Diff           string       `json:"diff,omitempty"`
	Actions        []SyncAction `json:"actions"`
	Errors         []string     `json:"errors,omitempty"`
}

// Parse reads .mcp.json.
func Parse(repoPath string) (*MCPFile, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, ".mcp.json"))
	if err != nil {
		return nil, fmt.Errorf("reading .mcp.json: %w", err)
	}
	var f MCPFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing .mcp.json: %w", err)
	}
	if f.MCPServers == nil {
		f.MCPServers = map[string]MCPServer{}
	}
	return &f, nil
}

// List returns the rendered profile names.
func List(repoPath string) ([]string, error) {
	plan, err := buildPlan(repoPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(plan.Profiles))
	for _, profile := range plan.Profiles {
		names = append(names, profile.Name)
	}
	return names, nil
}

// Sync writes the generated MCP block.
func Sync(repoPath string, dryRun bool) SyncReport {
	return run(repoPath, dryRun)
}

// Diff returns the dry-run report.
func Diff(repoPath string) SyncReport {
	return run(repoPath, true)
}

// DiffText returns the raw unified diff for the generated block.
func DiffText(repoPath string) (string, error) {
	plan, err := buildPlan(repoPath)
	if err != nil {
		return "", err
	}
	if !plan.PendingChanges {
		return "", nil
	}
	return plan.Diff, nil
}

type plan struct {
	RepoPath        string
	ConfigPath      string
	Profiles        []resolvedProfile
	Output          string
	Diff            string
	PendingChanges  bool
	Actions         []SyncAction
}

func run(repoPath string, dryRun bool) SyncReport {
	report := SyncReport{RepoPath: repoPath, DryRun: dryRun}
	plan, err := buildPlan(repoPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	report.PendingChanges = plan.PendingChanges
	report.Actions = plan.Actions
	report.Diff = plan.Diff

	if dryRun || !plan.PendingChanges {
		return report
	}
	if err := os.WriteFile(plan.ConfigPath, []byte(plan.Output), 0o644); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write %s: %v", plan.ConfigPath, err))
	}
	return report
}

func buildPlan(repoPath string) (*plan, error) {
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	mcpFile, err := Parse(absRepoPath)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(absRepoPath, ".codex", "config.toml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config.toml: %w", err)
	}
	configText := string(configData)
	if mcpServerBlockRe.MatchString(configText) && !strings.Contains(configText, startMarker) {
		return nil, fmt.Errorf("unmanaged [mcp_servers.*] blocks already exist in %s; add markers first or clean them up before syncing", configPath)
	}

	profiles, err := resolveProfiles(absRepoPath, mcpFile)
	if err != nil {
		return nil, err
	}
	block, err := renderBlock(absRepoPath, profiles)
	if err != nil {
		return nil, err
	}

	var output string
	switch {
	case strings.Contains(configText, startMarker):
		if mcpBlockInsideOllamaRegion(configText) {
			var stripped string
			stripped, err = removeMarkedRegion(configText)
			if err == nil {
				output, err = insertNewRegion(stripped, block)
			}
		} else {
			output, err = replaceMarkedRegion(configText, block)
		}
	case len(strings.TrimSpace(configText)) == 0:
		output = block
	default:
		output, err = insertNewRegion(configText, block)
	}
	if err != nil {
		return nil, err
	}

	diffText := unifiedDiffString(configText, output)
	plan := &plan{
		RepoPath:   absRepoPath,
		ConfigPath: configPath,
		Profiles:   profiles,
		Output:     output,
		Diff:       diffText,
	}
	plan.PendingChanges = diffText != ""
	if plan.PendingChanges {
		for _, profile := range profiles {
			plan.Actions = append(plan.Actions, SyncAction{
				Action:  "update",
				Server:  profile.Name,
				Message: fmt.Sprintf("update generated profile %s", profile.Name),
			})
		}
	} else {
		for _, profile := range profiles {
			plan.Actions = append(plan.Actions, SyncAction{
				Action:  "unchanged",
				Server:  profile.Name,
				Message: fmt.Sprintf("generated profile %s is current", profile.Name),
			})
		}
	}
	return plan, nil
}

func resolveProfiles(repoPath string, mcpFile *MCPFile) ([]resolvedProfile, error) {
	profilePath := filepath.Join(repoPath, ".codex", "mcp-profile-policy.json")
	if _, err := os.Stat(profilePath); errors.Is(err, os.ErrNotExist) {
		names := make([]string, 0, len(mcpFile.MCPServers))
		for name := range mcpFile.MCPServers {
			names = append(names, name)
		}
		slices.Sort(names)
		profiles := make([]resolvedProfile, 0, len(names))
		for _, name := range names {
			source := mcpFile.MCPServers[name]
			if err := validatePortableLaunch("source server "+name, source.Command, source.Args); err != nil {
				return nil, err
			}
			if strings.TrimSpace(source.Command) == "" && strings.TrimSpace(source.URL) == "" {
				return nil, fmt.Errorf("profile %s resolved to an empty command", name)
			}
			profiles = append(profiles, resolvedProfile{
				Name:      name,
				Command:   source.Command,
				Args:      append([]string(nil), source.Args...),
				CWD:       source.CWD,
				Env:       cloneEnv(source.Env),
				Transport: source.Transport,
				URL:       source.URL,
			})
		}
		return profiles, nil
	} else if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}
	var policy policyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("invalid policy file: %w", err)
	}
	if policy.Version == 0 || len(policy.Profiles) == 0 {
		return nil, fmt.Errorf("invalid policy file: %s", profilePath)
	}
	seen := map[string]struct{}{}
	profiles := make([]resolvedProfile, 0, len(policy.Profiles))
	for _, profile := range policy.Profiles {
		if _, ok := seen[profile.Name]; ok {
			return nil, fmt.Errorf("invalid policy file: duplicate generated profile names in %s", profilePath)
		}
		seen[profile.Name] = struct{}{}
		source, ok := mcpFile.MCPServers[profile.From]
		if !ok {
			return nil, fmt.Errorf("missing source server: %s", profile.From)
		}

		resolved := resolvedProfile{
			Name:              profile.Name,
			Comment:           profile.Comment,
			Command:           source.Command,
			Args:              append([]string(nil), source.Args...),
			CWD:               source.CWD,
			Env:               cloneEnv(source.Env),
			Transport:         source.Transport,
			URL:               source.URL,
			Enabled:           profile.Enabled,
			Required:          profile.Required,
			StartupTimeoutSec: profile.StartupTimeoutSec,
			ToolTimeoutSec:    profile.ToolTimeoutSec,
			EnabledTools:      append([]string(nil), profile.EnabledTools...),
			DisabledTools:     append([]string(nil), profile.DisabledTools...),
			ToolOverrides:     cloneToolOverrides(profile.ToolOverrides),
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
		if strings.TrimSpace(resolved.Command) == "" && strings.TrimSpace(resolved.URL) == "" {
			return nil, fmt.Errorf("profile %s resolved to an empty command", resolved.Name)
		}
		if err := validatePortableLaunch("profile "+resolved.Name, resolved.Command, resolved.Args); err != nil {
			return nil, err
		}
		profiles = append(profiles, resolved)
	}
	return profiles, nil
}

func renderBlock(repoPath string, profiles []resolvedProfile) (string, error) {
	var b strings.Builder
	b.WriteString(startMarker)
	b.WriteString("\n")
	b.WriteString("# Generated by codexkit/scripts/codex-mcp-sync.sh from .mcp.json")
	if _, err := os.Stat(filepath.Join(repoPath, ".codex", "mcp-profile-policy.json")); err == nil {
		b.WriteString(" and mcp-profile-policy.json")
	}
	b.WriteString("\n")

	for i, profile := range profiles {
		if i > 0 {
			b.WriteString("\n")
		}
		if profile.Comment != "" {
			b.WriteString("# ")
			b.WriteString(profile.Comment)
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", profile.Name)
		if profile.Command != "" {
			writeScalar(&b, "command", profile.Command)
		}
		if profile.Transport != "" && profile.Transport != "stdio" {
			writeScalar(&b, "transport", profile.Transport)
		}
		if profile.URL != "" {
			writeScalar(&b, "url", profile.URL)
		}
		if profile.Enabled != nil {
			fmt.Fprintf(&b, "enabled = %t\n", *profile.Enabled)
		}
		if profile.Required != nil {
			fmt.Fprintf(&b, "required = %t\n", *profile.Required)
		}
		if profile.StartupTimeoutSec != nil {
			fmt.Fprintf(&b, "startup_timeout_sec = %d\n", *profile.StartupTimeoutSec)
		}
		if profile.ToolTimeoutSec != nil {
			fmt.Fprintf(&b, "tool_timeout_sec = %d\n", *profile.ToolTimeoutSec)
		}
		if len(profile.Args) > 0 {
			writeInlineArray(&b, "args", profile.Args)
		}
		if profile.CWD != "" && profile.Command != "" {
			writeScalar(&b, "cwd", profile.CWD)
		}
		if len(profile.EnabledTools) > 0 {
			writeMultiArray(&b, "enabled_tools", profile.EnabledTools)
		}
		if len(profile.DisabledTools) > 0 {
			writeMultiArray(&b, "disabled_tools", profile.DisabledTools)
		}
		if len(profile.Env) > 0 {
			keys := make([]string, 0, len(profile.Env))
			for key := range profile.Env {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", profile.Name)
			for _, key := range keys {
				writeScalar(&b, key, profile.Env[key])
			}
		}
		if len(profile.ToolOverrides) > 0 {
			keys := make([]string, 0, len(profile.ToolOverrides))
			for key := range profile.ToolOverrides {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				override := profile.ToolOverrides[key]
				if override.ApprovalMode == "" {
					continue
				}
				fmt.Fprintf(&b, "\n[mcp_servers.%s.tools.%s]\n", profile.Name, key)
				writeScalar(&b, "approval_mode", override.ApprovalMode)
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(endMarker)
	b.WriteString("\n")
	return b.String(), nil
}

func replaceMarkedRegion(configText, block string) (string, error) {
	start := strings.Index(configText, startMarker)
	if start < 0 {
		return "", fmt.Errorf("generated MCP block markers not found in config.toml")
	}
	end := strings.Index(configText[start:], endMarker)
	if end < 0 {
		return "", fmt.Errorf("generated MCP block start marker in config.toml is missing a matching end marker")
	}
	end += start + len(endMarker)
	if end < len(configText) && configText[end] == '\n' {
		end++
	}
	return configText[:start] + block + configText[end:], nil
}

func removeMarkedRegion(configText string) (string, error) {
	start := strings.Index(configText, startMarker)
	if start < 0 {
		return "", fmt.Errorf("generated MCP block markers not found in config.toml")
	}
	end := strings.Index(configText[start:], endMarker)
	if end < 0 {
		return "", fmt.Errorf("generated MCP block start marker in config.toml is missing a matching end marker")
	}
	end += start + len(endMarker)
	if end < len(configText) && configText[end] == '\n' {
		end++
	}
	return configText[:start] + configText[end:], nil
}

func insertNewRegion(configText, block string) (string, error) {
	if idx := strings.Index(configText, ollamaEndMarker); idx >= 0 {
		end := idx + len(ollamaEndMarker)
		if end < len(configText) && configText[end] == '\n' {
			end++
		}
		prefix := strings.TrimRight(configText[:end], "\n")
		suffix := strings.TrimLeft(configText[end:], "\n")
		if suffix == "" {
			return prefix + "\n\n" + block, nil
		}
		return prefix + "\n\n" + block + suffix, nil
	}

	lines := strings.Split(configText, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "[") {
			prefix := strings.Join(lines[:i], "\n")
			suffix := strings.Join(lines[i:], "\n")
			if strings.TrimSpace(prefix) == "" {
				return block + suffix, nil
			}
			return prefix + "\n\n" + block + suffix, nil
		}
	}
	if strings.TrimSpace(configText) == "" {
		return block, nil
	}
	if strings.HasSuffix(configText, "\n") {
		return configText + "\n" + block, nil
	}
	return configText + "\n\n" + block, nil
}

func mcpBlockInsideOllamaRegion(configText string) bool {
	mcpStart := strings.Index(configText, startMarker)
	ollamaStart := strings.Index(configText, ollamaStartMarker)
	ollamaEnd := strings.Index(configText, ollamaEndMarker)
	if mcpStart < 0 || ollamaStart < 0 || ollamaEnd < 0 {
		return false
	}
	return mcpStart > ollamaStart && mcpStart < ollamaEnd
}

func unifiedDiffString(before, after string) string {
	if before == after {
		return ""
	}
	beforeFile, err := os.CreateTemp("", "codexkit-mcp-before-*.toml")
	if err != nil {
		return fmt.Sprintf("--- before\n+++ after\n%s", after)
	}
	defer os.Remove(beforeFile.Name())
	afterFile, err := os.CreateTemp("", "codexkit-mcp-after-*.toml")
	if err != nil {
		return fmt.Sprintf("--- before\n+++ after\n%s", after)
	}
	defer os.Remove(afterFile.Name())
	_, _ = beforeFile.WriteString(before)
	_, _ = afterFile.WriteString(after)
	_ = beforeFile.Close()
	_ = afterFile.Close()
	cmd := exec.Command("diff", "-u", beforeFile.Name(), afterFile.Name())
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return string(output)
	}
	return fmt.Sprintf("--- before\n+++ after\n%s", after)
}

func validatePortableLaunch(label, command string, args []string) error {
	if strings.HasPrefix(command, "../") {
		return fmt.Errorf("%s uses repo-relative command %s that escapes the repo", label, command)
	}
	if (command == "go" || strings.HasSuffix(command, "/go")) && len(args) > 1 && args[0] == "run" && strings.HasPrefix(args[1], "./cmd/") {
		return fmt.Errorf("%s uses direct 'go run ./cmd/...'; use a portable launcher script instead", label)
	}
	switch command {
	case "bash", "sh", "zsh":
		if len(args) > 0 && strings.HasPrefix(args[0], "../") {
			return fmt.Errorf("%s uses a repo-relative shell script path (%s) that escapes the repo", label, args[0])
		}
		for _, arg := range args {
			if strings.Contains(arg, "go run ./cmd/") {
				return fmt.Errorf("%s uses inline 'go run ./cmd/...'; wrap the server in a portable launcher script", label)
			}
			if strings.Contains(arg, "cd ") && strings.Contains(arg, "&&") {
				return fmt.Errorf("%s uses inline 'cd ... && ...'; resolve the repo/module root inside a portable launcher script", label)
			}
		}
	}
	return nil
}

func writeScalar(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%s = %s\n", key, quoteTOMLString(value))
}

func writeInlineArray(b *strings.Builder, key string, values []string) {
	fmt.Fprintf(b, "%s = [", key)
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteTOMLString(value))
	}
	b.WriteString("]\n")
}

func writeMultiArray(b *strings.Builder, key string, values []string) {
	fmt.Fprintf(b, "%s = [\n", key)
	for _, value := range values {
		fmt.Fprintf(b, "  %s,\n", quoteTOMLString(value))
	}
	b.WriteString("]\n")
}

func quoteTOMLString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneToolOverrides(src map[string]toolOverride) map[string]toolOverride {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]toolOverride, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
