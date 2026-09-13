package researchgateway

import (
	"errors"
	"time"
)

const (
	// SchemaVersion identifies the catalog and response schema emitted here.
	SchemaVersion = 1

	DefaultRegistryPath      = "research/upstreams/feeds.json"
	DefaultReceiptsPath      = "research/upstreams/refresh-receipts.json"
	DefaultOpenAISourcesPath = "research/openai/sources.json"
	OpenAISourcesFormat      = "openai_sources_v1"

	DefaultMaxDocuments     = 2048
	DefaultMaxDocumentBytes = int64(2 << 20)
	DefaultMaxTotalBytes    = int64(32 << 20)
	DefaultPageSize         = 20
	DefaultMaxPageSize      = 100
	DefaultMaxQueryBytes    = 1024
	DefaultReadBytes        = 16 << 10
	DefaultMaxReadBytes     = 64 << 10
	DefaultMaxSnippetBytes  = 512
	DefaultMaxRegisters     = 16
	DefaultMaxRecords       = 4096
	DefaultMaxRecordBytes   = 64 << 10
	DefaultMaxRegisterBytes = int64(16 << 20)
	DefaultMaxResponseBytes = 2 << 20

	HardMaxDocuments     = 10_000
	HardMaxDocumentBytes = int64(8 << 20)
	HardMaxTotalBytes    = int64(128 << 20)
	HardMaxPageSize      = 500
	HardMaxQueryBytes    = 4096
	HardMaxReadBytes     = 256 << 10
	HardMaxSnippetBytes  = 2048
	HardMaxRegisters     = 64
	HardMaxRecords       = 10_000
	HardMaxRecordBytes   = 256 << 10
	HardMaxRegisterBytes = int64(64 << 20)
	HardMaxResponseBytes = 8 << 20
)

var (
	ErrInvalidConfig    = errors.New("invalid research gateway configuration")
	ErrInvalidRequest   = errors.New("invalid research gateway request")
	ErrInvalidCursor    = errors.New("invalid research gateway cursor")
	ErrPathEscape       = errors.New("research path escapes harness root")
	ErrSymlink          = errors.New("symlinks are not allowed in research roots")
	ErrLimitExceeded    = errors.New("research gateway limit exceeded")
	ErrDocumentNotFound = errors.New("research document not found")
	ErrSourceNotFound   = errors.New("research source not found")
)

// Config fixes every filesystem authority used to build a Gateway. HarnessRoot
// is required. RegistryPath, ReceiptsPath, and each LocalRoot path must resolve
// within it. A missing receipts file is valid and marks every feed without a
// receipt as missing; a missing registry or local root is an error.
type Config struct {
	HarnessRoot  string           `json:"harness_root"`
	RegistryPath string           `json:"registry_path,omitempty"`
	ReceiptsPath string           `json:"receipts_path,omitempty"`
	LocalRoots   []LocalRoot      `json:"local_roots"`
	Registers    []RegisterConfig `json:"registers,omitempty"`
	Now          time.Time        `json:"now,omitempty"`
	Limits       Limits           `json:"limits,omitempty"`
}

// RegisterConfig declares a typed, read-only research register. If Registers
// is nil, Open uses DefaultRegisterConfigs; an explicitly empty slice disables
// register loading. Paths are resolved by the gateway before loader dispatch.
type RegisterConfig struct {
	ID     string `json:"id"`
	Format string `json:"format"`
	Path   string `json:"path"`
}

// DefaultRegisterConfigs returns a fresh copy of the built-in register list.
func DefaultRegisterConfigs() []RegisterConfig {
	return []RegisterConfig{{ID: "openai", Format: OpenAISourcesFormat, Path: DefaultOpenAISourcesPath}}
}

// LocalRoot is one explicitly allowlisted Markdown tree. ID becomes part of
// stable document identifiers. Path may be absolute or harness-relative, but
// it may not resolve outside HarnessRoot.
type LocalRoot struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Limits bounds snapshot construction and all query responses. Zero values use
// conservative defaults. Values above the exported hard maxima are rejected.
type Limits struct {
	MaxDocuments     int   `json:"max_documents,omitempty"`
	MaxDocumentBytes int64 `json:"max_document_bytes,omitempty"`
	MaxTotalBytes    int64 `json:"max_total_bytes,omitempty"`
	DefaultPageSize  int   `json:"default_page_size,omitempty"`
	MaxPageSize      int   `json:"max_page_size,omitempty"`
	MaxQueryBytes    int   `json:"max_query_bytes,omitempty"`
	DefaultReadBytes int   `json:"default_read_bytes,omitempty"`
	MaxReadBytes     int   `json:"max_read_bytes,omitempty"`
	MaxSnippetBytes  int   `json:"max_snippet_bytes,omitempty"`
	MaxRegisters     int   `json:"max_registers,omitempty"`
	MaxRecords       int   `json:"max_records,omitempty"`
	MaxRecordBytes   int   `json:"max_record_bytes,omitempty"`
	MaxRegisterBytes int64 `json:"max_register_bytes,omitempty"`
	MaxResponseBytes int   `json:"max_response_bytes,omitempty"`
}

type FreshnessStatus string

const (
	FreshnessFresh   FreshnessStatus = "fresh"
	FreshnessStale   FreshnessStatus = "stale"
	FreshnessMissing FreshnessStatus = "missing"
)

// Freshness is the offline cadence evaluation for a registered upstream.
type Freshness struct {
	Status    FreshnessStatus `json:"status"`
	CheckedAt *time.Time      `json:"checked_at,omitempty"`
	NextDueAt *time.Time      `json:"next_due_at,omitempty"`
}

// Upstream is a body-free official source. The gateway never fetches URL.
type Upstream struct {
	ID           string             `json:"id"`
	URI          string             `json:"uri"`
	FeedID       string             `json:"feed_id"`
	URL          string             `json:"url"`
	Kind         string             `json:"kind"`
	CadenceHours int                `json:"cadence_hours"`
	Freshness    Freshness          `json:"freshness"`
	Receipt      *ReceiptProvenance `json:"receipt,omitempty"`
	Release      *ReleaseProvenance `json:"release,omitempty"`
	RegistryPath string             `json:"registry_path"`
	ReceiptsPath string             `json:"receipts_path"`
}

// ReceiptProvenance is the sanitized portion of researchrefresh receipt
// evidence useful to offline clients.
type ReceiptProvenance struct {
	Status         string    `json:"status"`
	SourceURL      string    `json:"source_url"`
	FinalURL       string    `json:"final_url"`
	CheckedAt      time.Time `json:"checked_at"`
	FetchedAt      time.Time `json:"fetched_at"`
	ContentType    string    `json:"content_type"`
	RawSHA256      string    `json:"raw_sha256"`
	SemanticSHA256 string    `json:"semantic_sha256"`
}

// ReleaseProvenance is body-free release metadata retained by
// researchrefresh for latest-release feeds.
type ReleaseProvenance struct {
	Tag             string    `json:"tag"`
	PublishedAt     time.Time `json:"published_at"`
	URL             string    `json:"url"`
	Prerelease      bool      `json:"prerelease"`
	Draft           bool      `json:"draft"`
	TargetCommitish string    `json:"target_commitish"`
	BodySHA256      string    `json:"body_sha256"`
}

// Root describes one validated local research root without exposing an
// absolute host path.
type Root struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Document is immutable local Markdown metadata. Path is harness-relative and
// RelativePath is relative to its configured RootID.
type Document struct {
	ID           string    `json:"id"`
	URI          string    `json:"uri"`
	RootID       string    `json:"root_id"`
	Path         string    `json:"path"`
	RelativePath string    `json:"relative_path"`
	Title        string    `json:"title"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	ModifiedAt   time.Time `json:"modified_at"`
}

// Register describes one validated typed research register.
type Register struct {
	ID             string          `json:"id"`
	Format         string          `json:"format"`
	Path           string          `json:"path"`
	SHA256         string          `json:"sha256"`
	CapturedOn     string          `json:"captured_on"`
	Scope          string          `json:"scope"`
	Policy         string          `json:"policy"`
	AllowedDomains []string        `json:"allowed_domains"`
	Cadence        []CadencePolicy `json:"cadence"`
	Freshness      Freshness       `json:"freshness"`
	Records        int             `json:"records"`
}

// CadencePolicy retains typed volatility guidance in deterministic key order.
type CadencePolicy struct {
	Volatility string `json:"volatility"`
	Guidance   string `json:"guidance"`
}

// ResearchRecord is one searchable cited-source entry. It carries durable
// verified facts and watch points, never a fetched upstream document body.
type ResearchRecord struct {
	ID             string    `json:"id"`
	URI            string    `json:"uri"`
	RegisterID     string    `json:"register_id"`
	SourceID       string    `json:"source_id"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	Domain         string    `json:"domain"`
	Category       string    `json:"category"`
	Surfaces       []string  `json:"surfaces"`
	Volatility     string    `json:"volatility"`
	RefreshCadence string    `json:"refresh_cadence"`
	VerifiedFacts  []string  `json:"verified_facts"`
	WatchFor       []string  `json:"watch_for"`
	CapturedOn     string    `json:"captured_on"`
	Freshness      Freshness `json:"freshness"`
	RegisterPath   string    `json:"register_path"`
	RegisterSHA256 string    `json:"register_sha256"`
}

type CatalogSummary struct {
	Upstreams   int   `json:"upstreams"`
	Fresh       int   `json:"fresh"`
	Stale       int   `json:"stale"`
	Missing     int   `json:"missing"`
	Roots       int   `json:"roots"`
	Documents   int   `json:"documents"`
	Bytes       int64 `json:"bytes"`
	Registers   int   `json:"registers"`
	Records     int   `json:"records"`
	RecordFresh int   `json:"record_fresh"`
	RecordStale int   `json:"record_stale"`
}

// Catalog is the deterministic typed snapshot exposed to adapters.
type Catalog struct {
	SchemaVersion int              `json:"schema_version"`
	SnapshotID    string           `json:"snapshot_id"`
	ObservedAt    time.Time        `json:"observed_at"`
	RegistryPath  string           `json:"registry_path"`
	ReceiptsPath  string           `json:"receipts_path"`
	Summary       CatalogSummary   `json:"summary"`
	Roots         []Root           `json:"roots"`
	Upstreams     []Upstream       `json:"upstreams"`
	Documents     []Document       `json:"documents"`
	Registers     []Register       `json:"registers"`
	Records       []ResearchRecord `json:"records"`
}

type ListRequest struct {
	RootIDs []string `json:"root_ids,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Cursor  string   `json:"cursor,omitempty"`
}

type ListResponse struct {
	SchemaVersion int        `json:"schema_version"`
	SnapshotID    string     `json:"snapshot_id"`
	Total         int        `json:"total"`
	Documents     []Document `json:"documents"`
	NextCursor    string     `json:"next_cursor,omitempty"`
}

// Overview is the compact catalog header intended for MCP catalog tools.
// Detailed entries are available through the paginated list methods.
type Overview struct {
	SchemaVersion int            `json:"schema_version"`
	SnapshotID    string         `json:"snapshot_id"`
	ObservedAt    time.Time      `json:"observed_at"`
	RegistryPath  string         `json:"registry_path"`
	ReceiptsPath  string         `json:"receipts_path"`
	Summary       CatalogSummary `json:"summary"`
	Roots         []Root         `json:"roots"`
	Registers     []Register     `json:"registers"`
}

type PageRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type UpstreamListResponse struct {
	SchemaVersion int        `json:"schema_version"`
	SnapshotID    string     `json:"snapshot_id"`
	Total         int        `json:"total"`
	Upstreams     []Upstream `json:"upstreams"`
	NextCursor    string     `json:"next_cursor,omitempty"`
}

type RecordListRequest struct {
	RegisterIDs []string `json:"register_ids,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
}

type RecordListResponse struct {
	SchemaVersion int              `json:"schema_version"`
	SnapshotID    string           `json:"snapshot_id"`
	Total         int              `json:"total"`
	Records       []ResearchRecord `json:"records"`
	NextCursor    string           `json:"next_cursor,omitempty"`
}

type SearchRequest struct {
	Query       string   `json:"query"`
	RootIDs     []string `json:"root_ids,omitempty"`
	RegisterIDs []string `json:"register_ids,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
}

type SearchHit struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Document *Document       `json:"document,omitempty"`
	Record   *ResearchRecord `json:"record,omitempty"`
	Upstream *Upstream       `json:"upstream,omitempty"`
	Score    int             `json:"score"`
	Matches  int             `json:"matches"`
	Snippet  string          `json:"snippet"`
}

type SearchResponse struct {
	SchemaVersion int         `json:"schema_version"`
	SnapshotID    string      `json:"snapshot_id"`
	Query         string      `json:"query"`
	Total         int         `json:"total"`
	Results       []SearchHit `json:"results"`
	NextCursor    string      `json:"next_cursor,omitempty"`
}

type ReadRequest struct {
	DocumentID string `json:"document_id"`
	Limit      int    `json:"limit,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
}

type ReadResponse struct {
	SchemaVersion int      `json:"schema_version"`
	SnapshotID    string   `json:"snapshot_id"`
	Document      Document `json:"document"`
	Offset        int64    `json:"offset"`
	Bytes         int      `json:"bytes"`
	TotalBytes    int64    `json:"total_bytes"`
	Content       string   `json:"content"`
	NextCursor    string   `json:"next_cursor,omitempty"`
}

type Detail struct {
	SchemaVersion int             `json:"schema_version"`
	SnapshotID    string          `json:"snapshot_id"`
	ID            string          `json:"id"`
	Upstream      *Upstream       `json:"upstream,omitempty"`
	Document      *Document       `json:"document,omitempty"`
	Record        *ResearchRecord `json:"record,omitempty"`
}
