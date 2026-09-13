package harnessmanifest

const (
	SchemaVersion    = 1
	MaxDocumentBytes = 4 << 20
)

// OwnershipClass identifies the canonical policy owner independently from how
// an object is sourced or materialized.
type OwnershipClass string

const (
	OwnershipSharedProviderNeutral OwnershipClass = "shared-provider-neutral"
	OwnershipCodexHarnessSpecific  OwnershipClass = "codex-harness-specific"
	OwnershipClaudeHarnessSpecific OwnershipClass = "claude-harness-specific"
	OwnershipRepoLocalProjection   OwnershipClass = "repo-local-projection"
	OwnershipExternalOwner         OwnershipClass = "external-owner"
)

// SourceDisposition describes where the semantic source remains canonical.
type SourceDisposition string

const (
	SourceCentralCanonical      SourceDisposition = "central-canonical"
	SourceGeneratedFromConsumer SourceDisposition = "generated-from-consumer"
	SourceSharedHarness         SourceDisposition = "shared-harness"
	SourceExternalOwner         SourceDisposition = "external-owner"
)

// ProjectionMode describes materialization independently from ownership and
// source disposition.
type ProjectionMode string

const (
	ProjectionCopy            ProjectionMode = "copy"
	ProjectionRender          ProjectionMode = "render"
	ProjectionManagedBlock    ProjectionMode = "managed-block"
	ProjectionRelativeSymlink ProjectionMode = "relative-symlink"
	ProjectionReference       ProjectionMode = "reference"
	ProjectionNone            ProjectionMode = "none"
)

// Lifecycle is the declaration's publication and deprecation state.
type Lifecycle string

const (
	LifecyclePlanned      Lifecycle = "planned"
	LifecycleActive       Lifecycle = "active"
	LifecycleDeprecated   Lifecycle = "deprecated"
	LifecycleRetired      Lifecycle = "retired"
	LifecycleArchived     Lifecycle = "archived"
	LifecycleNeverPublish Lifecycle = "never-publish"
)

// ConflictPolicy controls what a later installer may do when a destination
// already exists. ReplaceOwned still requires runtime ownership proof.
type ConflictPolicy string

const (
	ConflictFail         ConflictPolicy = "fail"
	ConflictReplaceOwned ConflictPolicy = "replace-owned"
)

// RemovalPolicy controls whether a declaration can authorize removal.
// TombstoneOnly is valid only for an explicitly owned retired tombstone.
type RemovalPolicy string

const (
	RemovalPreserve      RemovalPolicy = "preserve"
	RemovalTombstoneOnly RemovalPolicy = "tombstone-only"
)

// OmissionKind selects the intentionally small, auditable omission language.
// Glob accepts literal segments, a whole-segment '*', and a terminal '**'.
type OmissionKind string

const (
	OmissionExact OmissionKind = "exact"
	OmissionGlob  OmissionKind = "glob"
)

// Manifest is the complete declaration for one repository.
type Manifest struct {
	SchemaVersion  int             `json:"schema_version"`
	Repo           string          `json:"repo"`
	Objects        []Object        `json:"objects,omitempty"`
	Omissions      []Omission      `json:"omissions,omitempty"`
	CoverageInputs []CoverageInput `json:"coverage_inputs,omitempty"`
}

// Object is one projected, referenced, planned, or tombstoned repository
// object. Paths use normalized slash-separated repository-relative syntax.
type Object struct {
	ID                string            `json:"id"`
	Repo              string            `json:"repo"`
	Source            string            `json:"source"`
	Inputs            []SourceInput     `json:"inputs"`
	Destination       string            `json:"destination"`
	OwnershipClass    OwnershipClass    `json:"ownership_class"`
	SourceDisposition SourceDisposition `json:"source_disposition"`
	ProjectionMode    ProjectionMode    `json:"projection_mode"`
	Generator         string            `json:"generator,omitempty"`
	GeneratorCommit   string            `json:"generator_commit,omitempty"`
	Transform         string            `json:"transform,omitempty"`
	Mode              string            `json:"mode,omitempty"`
	SourceHash        string            `json:"source_hash"`
	// ProjectedHash hashes the complete projected regular-file payload, the
	// relative symlink target, or (for managed-block) only the owned block
	// payload. A reviewed install plan owns whole-file pre/post-image hashes.
	ProjectedHash  string         `json:"projected_hash,omitempty"`
	SymlinkTarget  string         `json:"symlink_target,omitempty"`
	VerifierIDs    []string       `json:"verifier_ids"`
	Lifecycle      Lifecycle      `json:"lifecycle"`
	ConflictPolicy ConflictPolicy `json:"conflict_policy"`
	RemovalPolicy  RemovalPolicy  `json:"removal_policy"`
	Tombstone      bool           `json:"tombstone,omitempty"`
}

// SourceInput is an ordered, exact-ref render input. Repository binds the
// canonical namespace/name independently from checkout directory names.
// Non-hermetic inputs must carry an explicit reason rather than being inferred.
type SourceInput struct {
	Repository        string `json:"repository"`
	Remote            string `json:"remote"`
	Ref               string `json:"ref"`
	Commit            string `json:"commit"`
	Path              string `json:"path"`
	BlobHash          string `json:"blob_hash"`
	NonHermetic       bool   `json:"non_hermetic,omitempty"`
	NonHermeticReason string `json:"non_hermetic_reason,omitempty"`
}

// Omission assigns an intentionally unmanaged path or bounded path pattern to
// an owner and verifier rather than silently dropping it from coverage.
type Omission struct {
	ID             string         `json:"id"`
	Path           string         `json:"path"`
	Kind           OmissionKind   `json:"kind"`
	OwnershipClass OwnershipClass `json:"ownership_class"`
	Reason         string         `json:"reason"`
	VerifierIDs    []string       `json:"verifier_ids"`
	Lifecycle      Lifecycle      `json:"lifecycle"`
}

// CoverageInput is one discovered consumer path that must be assigned exactly
// once to an object destination or omission.
type CoverageInput struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// ValidationOptions provides the externally owned verifier registry. A
// referenced verifier not present here fails closed.
type ValidationOptions struct {
	RegisteredVerifierIDs []string
}

// CoverageAssignment records the unique declaration responsible for an input.
type CoverageAssignment struct {
	InputID     string `json:"input_id"`
	Path        string `json:"path"`
	ObjectID    string `json:"object_id,omitempty"`
	OmissionID  string `json:"omission_id,omitempty"`
	Disposition string `json:"disposition"`
}

// CoverageReport is deterministic, machine-readable coverage evidence.
type CoverageReport struct {
	TotalInputs int                  `json:"total_inputs"`
	Projected   int                  `json:"projected"`
	Omitted     int                  `json:"omitted"`
	Assignments []CoverageAssignment `json:"assignments,omitempty"`
}

// Issue is one stable validation finding.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
