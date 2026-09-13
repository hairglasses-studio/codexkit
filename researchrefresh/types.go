// Package researchrefresh fetches and fingerprints allowlisted official upstream
// research feeds without persisting fetched content or consulting credentials.
package researchrefresh

import (
	"errors"
	"net/http"
	"time"
)

const (
	// RegistrySchemaVersion is the only registry schema understood by this package.
	RegistrySchemaVersion = 1
	// ReceiptSchemaVersion is the only receipt schema understood by this package.
	ReceiptSchemaVersion = 1

	DefaultTimeout          = 20 * time.Second
	DefaultMaxResponseBytes = int64(4 << 20)
)

var (
	ErrInvalidRegistry       = errors.New("invalid research feed registry")
	ErrInvalidReceipt        = errors.New("invalid research refresh receipt")
	ErrUnsafeURL             = errors.New("unsafe research feed URL")
	ErrRedirectDenied        = errors.New("research feed redirect denied")
	ErrUnexpectedContentType = errors.New("unexpected research feed content type")
	ErrResponseTooLarge      = errors.New("research feed response exceeds size limit")
	ErrProviderHash          = errors.New("research feed provider hash verification failed")
)

// FeedKind selects the deterministic semantic extraction applied to a feed.
type FeedKind string

const (
	KindMarkdown            FeedKind = "markdown"
	KindText                FeedKind = "text"
	KindHTML                FeedKind = "html"
	KindGitHubLatestRelease FeedKind = "github_latest_release"
)

// Registry is the typed, versioned collection of official upstream feeds.
type Registry struct {
	SchemaVersion int    `json:"schema_version"`
	Feeds         []Feed `json:"feeds"`
}

// Feed defines one credential-free, HTTPS-only upstream research source.
type Feed struct {
	ID                     string   `json:"id"`
	URL                    string   `json:"url"`
	Kind                   FeedKind `json:"kind"`
	CadenceHours           int      `json:"cadence_hours"`
	AllowedRedirectDomains []string `json:"allowed_redirect_domains,omitempty"`
	ContentTypes           []string `json:"content_types,omitempty"`
	VerifyProviderSHA256   bool     `json:"verify_x_content_sha256,omitempty"`
}

// RefreshStatus describes whether a refresh was skipped or which valid HTTP
// response produced its candidate receipt.
type RefreshStatus string

const (
	StatusNotDue      RefreshStatus = "not_due"
	StatusFetched     RefreshStatus = "fetched"
	StatusNotModified RefreshStatus = "not_modified"
)

// ReleaseMetadata is the canonical, body-free representation of a GitHub
// latest-release response. BodySHA256 fingerprints the normalized body; the
// body itself is never retained here.
type ReleaseMetadata struct {
	Tag             string    `json:"tag"`
	PublishedAt     time.Time `json:"published_at"`
	URL             string    `json:"url"`
	Prerelease      bool      `json:"prerelease"`
	Draft           bool      `json:"draft"`
	TargetCommitish string    `json:"target_commitish"`
	BodySHA256      string    `json:"body_sha256"`
}

// Receipt is the sanitized, body-free evidence that a feed was checked. URL
// query strings and fragments are omitted from SourceURL and FinalURL.
type Receipt struct {
	SchemaVersion  int              `json:"schema_version"`
	FeedID         string           `json:"feed_id"`
	SourceURL      string           `json:"source_url"`
	FinalURL       string           `json:"final_url"`
	Status         RefreshStatus    `json:"status"`
	CheckedAt      time.Time        `json:"checked_at"`
	FetchedAt      time.Time        `json:"fetched_at"`
	HTTPStatus     int              `json:"http_status"`
	ContentType    string           `json:"content_type"`
	ETag           string           `json:"etag,omitempty"`
	LastModified   string           `json:"last_modified,omitempty"`
	RawSHA256      string           `json:"raw_sha256"`
	SemanticSHA256 string           `json:"semantic_sha256"`
	ProviderSHA256 string           `json:"provider_sha256,omitempty"`
	Release        *ReleaseMetadata `json:"release,omitempty"`
}

// Receipts is the versioned aggregate receipt document. EncodeReceipts orders
// this slice by feed_id so concurrent callers can produce stable artifacts.
type Receipts struct {
	SchemaVersion int       `json:"schema_version"`
	Receipts      []Receipt `json:"receipts"`
}

// Options controls a single in-memory refresh. The package never writes a
// receipt or fetched content. A nil Client uses a credential-free client that
// does not consult proxy environment variables.
type Options struct {
	Client           *http.Client
	Now              time.Time
	Timeout          time.Duration
	MaxResponseBytes int64
	Force            bool
}

// DueResult is the deterministic cadence evaluation for a feed.
type DueResult struct {
	Due       bool      `json:"due"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	NextDueAt time.Time `json:"next_due_at,omitempty"`
}

// Result is an in-memory dry-run result. SemanticContent is returned only so a
// caller can inspect a newly fetched canonical representation; it is excluded
// from JSON and from receipts. Use CandidateReceipt to obtain persistable
// evidence explicitly.
type Result struct {
	FeedID          string        `json:"feed_id"`
	Status          RefreshStatus `json:"status"`
	Due             bool          `json:"due"`
	Changed         bool          `json:"changed"`
	CheckedAt       time.Time     `json:"checked_at,omitempty"`
	NextDueAt       time.Time     `json:"next_due_at,omitempty"`
	HTTPStatus      int           `json:"http_status,omitempty"`
	RawSHA256       string        `json:"raw_sha256,omitempty"`
	SemanticSHA256  string        `json:"semantic_sha256,omitempty"`
	SemanticContent []byte        `json:"-"`

	candidate *Receipt
}

// CandidateReceipt returns a defensive copy of the receipt a caller may
// choose to persist. A not-due result has no candidate.
func (r Result) CandidateReceipt() (Receipt, bool) {
	if r.candidate == nil {
		return Receipt{}, false
	}
	receipt := *r.candidate
	if r.candidate.Release != nil {
		release := *r.candidate.Release
		receipt.Release = &release
	}
	return receipt, true
}
