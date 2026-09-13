// Package researchgateway provides a filesystem-backed, offline-only catalog
// and query layer for harness research. It indexes official upstream metadata
// from researchrefresh registries and receipts, plus Markdown documents found
// beneath explicitly configured harness-local roots.
//
// A Gateway is an immutable snapshot and is safe for concurrent use. Requests
// accept document identifiers rather than filesystem paths, making the API a
// small boundary for MCP tool and resource adapters without giving callers an
// arbitrary file-read primitive.
package researchgateway
