// Package researchmcp mounts the offline researchgateway catalog on an
// official Model Context Protocol Go SDK server.
//
// The adapter exposes only stable research identifiers, bounded filters, and
// opaque cursors. Filesystem authority is fixed by Config at registration
// time, and every tool or resource call reopens a read-only snapshot so local
// content and wall-clock freshness are current for that call.
package researchmcp
