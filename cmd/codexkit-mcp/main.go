// Command codexkit-mcp runs the codexkit MCP server over stdio.
//
// It registers all ToolModules and serves tool calls via JSON-RPC 2.0,
// following the MCP 2025-11 specification with deferred tool loading.
package main

import (
	"fmt"
	"os"

	"github.com/hairglasses-studio/codexkit"
	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/fleetaudit"
	"github.com/hairglasses-studio/codexkit/mcpserver"
	"github.com/hairglasses-studio/codexkit/mcpsync"
	"github.com/hairglasses-studio/codexkit/skillsync"
)

func main() {
	reg := codexkit.NewRegistry()

	modules := []codexkit.ToolModule{
		baselineguard.Module(),
		skillsync.Module(),
		mcpsync.Module(),
		fleetaudit.Module(),
	}

	for _, m := range modules {
		if err := reg.Register(m); err != nil {
			fmt.Fprintf(os.Stderr, "error registering %s: %v\n", m.Name(), err)
			os.Exit(1)
		}
	}

	info := mcpserver.ServerInfo{
		Name:    "codexkit",
		Version: "0.2.0",
	}
	if err := reg.Register(mcpserver.Module(reg, info)); err != nil {
		fmt.Fprintf(os.Stderr, "error registering server meta module: %v\n", err)
		os.Exit(1)
	}

	server := mcpserver.New(reg, info)

	if err := server.ServeStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
