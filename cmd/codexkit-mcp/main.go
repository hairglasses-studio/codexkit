// Command codexkit-mcp runs the codexkit MCP server over stdio.
//
// Managed harness launches use the official MCP Go SDK and mount the offline
// research gateway. Standalone launches retain the legacy wire server during
// the Codexkit compatibility runway unless CODEXKIT_MCP_RUNTIME=sdk is set.
// OpenAI clients defer this MCP server from their own tool/MCP definition and
// discover its catalog first; the server does not invent a deferred-loading
// MCP capability of its own.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/builtinmodules"
	"github.com/hairglasses-studio/codexkit/mcpserver"
	"github.com/hairglasses-studio/codexkit/researchmcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	info := mcpserver.ServerInfo{
		Name:    "codexkit",
		Version: "0.2.0",
	}
	harnessRoot, rootErr := resolveHarnessRoot()
	runtime, err := selectedMCPRuntime(rootErr == nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime selection error: %v\n", err)
		os.Exit(1)
	}
	var extension mcpserver.SurfaceCounts
	if runtime == "sdk" && rootErr == nil {
		extension = researchSurface()
	}
	reg, err := buildRegistry(info, extension)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry error: %v\n", err)
		os.Exit(1)
	}
	if runtime == "legacy" {
		if err := mcpserver.New(reg, info).ServeStdio(); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := mcpserver.NewSDKServer(reg, info, extension)
	if rootErr == nil {
		if err := researchmcp.Register(server, researchmcp.Config{HarnessRoot: harnessRoot}); err != nil {
			fmt.Fprintf(os.Stderr, "research gateway error: %v\n", err)
			os.Exit(1)
		}
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func researchSurface() mcpserver.SurfaceCounts {
	return mcpserver.SurfaceCounts{
		Tools:             researchmcp.ToolCount,
		Resources:         researchmcp.ResourceCount,
		ResourceTemplates: researchmcp.ResourceTemplateCount,
		Prompts:           researchmcp.PromptCount,
		ToolDefinitions:   researchmcp.ToolDefinitions(),
	}
}

func buildRegistry(info mcpserver.ServerInfo, extension mcpserver.SurfaceCounts) (*codexkit.Registry, error) {
	registry := codexkit.NewRegistry()
	for _, module := range builtinmodules.ForMCP() {
		if err := registry.Register(module); err != nil {
			return nil, fmt.Errorf("register %s: %w", module.Name(), err)
		}
	}
	if err := registry.Register(mcpserver.Module(registry, info, extension)); err != nil {
		return nil, fmt.Errorf("register server meta module: %w", err)
	}
	return registry, nil
}

func selectedMCPRuntime(harnessAvailable bool) (string, error) {
	runtime := strings.TrimSpace(os.Getenv("CODEXKIT_MCP_RUNTIME"))
	if runtime == "" {
		if harnessAvailable {
			return "sdk", nil
		}
		return "legacy", nil
	}
	if runtime != "sdk" && runtime != "legacy" {
		return "", fmt.Errorf("CODEXKIT_MCP_RUNTIME must be sdk or legacy")
	}
	return runtime, nil
}

func resolveHarnessRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HARNESS_DIR")); configured != "" {
		return configured, nil
	}
	if configured := strings.TrimSpace(os.Getenv("CODEXKIT_ROOT")); configured != "" {
		if filepath.Base(filepath.Clean(configured)) == "codexkit" {
			return filepath.Dir(filepath.Clean(configured)), nil
		}
		return configured, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for candidate := filepath.Clean(abs); ; candidate = filepath.Dir(candidate) {
		if regularFile(filepath.Join(candidate, "AGENTS.md")) && regularFile(filepath.Join(candidate, "codexkit", "go.mod")) && regularFile(filepath.Join(candidate, "research", "upstreams", "feeds.json")) {
			return candidate, nil
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return "", fmt.Errorf("no harness root found above %s", abs)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
