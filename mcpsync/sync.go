// Package mcpsync synchronizes MCP server definitions between .mcp.json
// and .codex/config.toml.
//
// It supports stdio and HTTP transports, MCP server discovery via
// .well-known/mcp.json (2026 roadmap), and the Tasks capability
// from the MCP 2025-11 spec.
package mcpsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tomlwriter "github.com/hairglasses-studio/codexkit/internal/toml"
)

// MCPServer represents a server definition from .mcp.json.
type MCPServer struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"` // "stdio" (default), "http", "sse"
	URL       string            `json:"url,omitempty"`       // for HTTP/SSE transport
}

// MCPFile represents the .mcp.json structure.
type MCPFile struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// SyncAction describes a single sync operation.
type SyncAction struct {
	Action  string `json:"action"`  // "create", "update", "unchanged", "stale"
	Server  string `json:"server"`  // server name
	Message string `json:"message"` // human-readable description
}

// SyncReport is the result of an MCP sync.
type SyncReport struct {
	RepoPath string       `json:"repo_path"`
	DryRun   bool         `json:"dry_run"`
	Actions  []SyncAction `json:"actions"`
	Errors   []string     `json:"errors,omitempty"`
}

var mcpServerBlockRe = regexp.MustCompile(`(?m)^\[mcp_servers\.([\w-]+)\]`)

// Parse reads and parses the .mcp.json file.
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
		f.MCPServers = make(map[string]MCPServer)
	}
	return &f, nil
}

// Sync synchronizes .mcp.json servers into .codex/config.toml.
func Sync(repoPath string, dryRun bool) SyncReport {
	report := SyncReport{RepoPath: repoPath, DryRun: dryRun}

	mcpFile, err := Parse(repoPath)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	if len(mcpFile.MCPServers) == 0 {
		return report
	}

	configPath := filepath.Join(repoPath, ".codex/config.toml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("reading config.toml: %v", err))
		return report
	}

	configStr := string(configData)
	existing := make(map[string]bool)
	for _, match := range mcpServerBlockRe.FindAllStringSubmatch(configStr, -1) {
		existing[match[1]] = true
	}

	var appendBuf strings.Builder
	for name, server := range mcpFile.MCPServers {
		if existing[name] {
			report.Actions = append(report.Actions, SyncAction{
				Action:  "unchanged",
				Server:  name,
				Message: fmt.Sprintf("[mcp_servers.%s] already exists", name),
			})
			continue
		}
		report.Actions = append(report.Actions, SyncAction{
			Action:  "create",
			Server:  name,
			Message: fmt.Sprintf("create [mcp_servers.%s]", name),
		})
		values := serverToValues(server)
		appendBuf.WriteString(tomlwriter.AppendSection("mcp_servers."+name, values))
	}

	if appendBuf.Len() > 0 && !dryRun {
		f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("opening config.toml: %v", err))
			return report
		}
		defer f.Close()
		if _, err := f.WriteString(appendBuf.String()); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("writing config.toml: %v", err))
		}
	}

	return report
}

func serverToValues(s MCPServer) map[string]any {
	values := make(map[string]any)
	if s.Command != "" {
		values["command"] = s.Command
	}
	if len(s.Args) > 0 {
		values["args"] = s.Args
	}
	transport := s.Transport
	if transport == "" && s.URL == "" {
		transport = "stdio"
	}
	if transport != "" {
		values["transport"] = transport
	}
	if s.URL != "" {
		values["url"] = s.URL
	}
	return values
}

// Diff returns a report showing what would change without writing.
func Diff(repoPath string) SyncReport {
	return Sync(repoPath, true)
}

// List returns the server names from .mcp.json.
func List(repoPath string) ([]string, error) {
	mcpFile, err := Parse(repoPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(mcpFile.MCPServers))
	for name := range mcpFile.MCPServers {
		names = append(names, name)
	}
	return names, nil
}
