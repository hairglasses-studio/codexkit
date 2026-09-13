package researchmcp

import "github.com/hairglasses-studio/codexkit/researchgateway"

const (
	// DefaultLocalRootID is the stable identifier for the default research tree.
	DefaultLocalRootID = "research"
	// DefaultLocalRootPath is resolved beneath Config.HarnessRoot.
	DefaultLocalRootPath = "research"
)

// Config fixes all filesystem authority available through the MCP adapter.
// These values are server-owned configuration and are never accepted from MCP
// callers. A nil LocalRoots slice discovers Markdown beneath research/; an
// explicitly empty slice disables local Markdown discovery.
type Config struct {
	HarnessRoot  string
	RegistryPath string
	ReceiptsPath string
	LocalRoots   []researchgateway.LocalRoot
	Registers    []researchgateway.RegisterConfig
	Limits       researchgateway.Limits
	Loaders      *researchgateway.LoaderRegistry
}

func (config Config) gatewayConfig() researchgateway.Config {
	localRoots := cloneSlice(config.LocalRoots)
	if localRoots == nil {
		localRoots = []researchgateway.LocalRoot{{
			ID:   DefaultLocalRootID,
			Path: DefaultLocalRootPath,
		}}
	}
	return researchgateway.Config{
		HarnessRoot:  config.HarnessRoot,
		RegistryPath: config.RegistryPath,
		ReceiptsPath: config.ReceiptsPath,
		LocalRoots:   localRoots,
		Registers:    cloneSlice(config.Registers),
		Limits:       config.Limits,
	}
}

func cloneSlice[T any](input []T) []T {
	if input == nil {
		return nil
	}
	return append(make([]T, 0, len(input)), input...)
}
