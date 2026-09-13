package sourceref

import (
	"errors"
	"time"
)

const (
	// SchemaVersion is the only source-ref inventory schema understood here.
	SchemaVersion = 1

	// DefaultMaxAge is the default freshness window for exact-ref evidence.
	DefaultMaxAge = 24 * time.Hour

	// MaxDocumentBytes bounds an in-memory inventory document before decoding.
	MaxDocumentBytes = 4 << 20
)

var ErrInvalidInventory = errors.New("invalid source-ref inventory")

// Lifecycle is the closed repository lifecycle used by fleet source evidence.
type Lifecycle string

const (
	LifecycleActive          Lifecycle = "active"
	LifecycleMaintenanceOnly Lifecycle = "maintenance-only"
	LifecycleNeverPublish    Lifecycle = "never-publish"
	LifecyclePublishMirror   Lifecycle = "publish-mirror"
	LifecycleArchived        Lifecycle = "archived"
	LifecycleDeprecated      Lifecycle = "deprecated"
	LifecycleCompatibility   Lifecycle = "compatibility"
	LifecycleDetached        Lifecycle = "detached"
	LifecycleReferenceOnly   Lifecycle = "reference-only"
)

// OwnershipClass is the closed centralization ownership boundary.
type OwnershipClass string

const (
	OwnershipSharedProviderNeutral OwnershipClass = "shared-provider-neutral"
	OwnershipCodexHarnessSpecific  OwnershipClass = "codex-harness-specific"
	OwnershipClaudeHarnessSpecific OwnershipClass = "claude-harness-specific"
	OwnershipRepoLocalProjection   OwnershipClass = "repo-local-projection"
	OwnershipExternalOwner         OwnershipClass = "external-owner"
)

// EvidenceClass identifies how the tested Git tree was observed.
type EvidenceClass string

const (
	EvidenceRemoteTree      EvidenceClass = "remote-tree"
	EvidenceFreshWorktree   EvidenceClass = "fresh-worktree"
	EvidencePrimaryCheckout EvidenceClass = "primary-checkout"
	EvidenceLiveRuntime     EvidenceClass = "live-runtime"
	EvidenceLocalOnly       EvidenceClass = "local-only"
)

// Options supplies the validation clock and freshness bound. Now is required
// so validation never consults ambient wall-clock state. A zero MaxAge selects
// DefaultMaxAge.
type Options struct {
	Now    time.Time
	MaxAge time.Duration
}

// Inventory is one versioned set of repository source observations.
type Inventory struct {
	SchemaVersion int                  `json:"schema_version"`
	Repositories  []RepositoryEvidence `json:"repositories"`
}

// RepositoryEvidence binds a tested tree to its requested remote reference,
// repository identity, collection method, and ownership/lifecycle policy.
// Repository is a slash-separated remote path such as
// "hairglasses-studio/codex-harness-staging".
type RepositoryEvidence struct {
	Repository         string         `json:"repository"`
	RemoteURL          string         `json:"remote_url,omitempty"`
	RequestedRef       string         `json:"requested_ref,omitempty"`
	RequestedSHA       string         `json:"requested_sha,omitempty"`
	TestedRef          string         `json:"tested_ref"`
	TestedSHA          string         `json:"tested_sha"`
	Checkout           *Checkout      `json:"checkout"`
	ObservedAt         time.Time      `json:"observed_at"`
	Lifecycle          Lifecycle      `json:"lifecycle"`
	OwnershipClass     OwnershipClass `json:"ownership_class"`
	EvidenceClass      EvidenceClass  `json:"evidence_class"`
	ObservationCommand string         `json:"observation_command"`
}

// Checkout records a portable, workspace-relative checkout identity and dirty
// state even when remote-tree evidence is authoritative. It never contains a
// host-private absolute path. For remote-tree evidence it is contextual only;
// it is never allowed to stand in for the tested remote SHA.
type Checkout struct {
	Path  string `json:"path"`
	Dirty bool   `json:"dirty"`
}
