package harnessplan

import (
	"errors"
	"time"
)

const (
	// SchemaVersion is the only plan-envelope schema understood by this package.
	SchemaVersion = 1

	// MaxDocumentBytes bounds an encoded envelope before JSON decoding.
	MaxDocumentBytes = 4 << 20
	// MaxSourceBindings and MaxOperations bound collection amplification.
	MaxSourceBindings = 1024
	MaxOperations     = 10000
	// MaxPayloadBytes bounds a single staged regular-file payload.
	MaxPayloadBytes int64 = 64 << 20
	// MaxTotalPayloadBytes bounds aggregate payload amplification in one plan.
	MaxTotalPayloadBytes int64 = 512 << 20
)

var ErrInvalidPlan = errors.New("invalid harness plan")

// EvidenceClass is the closed set of source observations eligible to
// authorize a plan. Dirty primary checkouts and live runtime observations are
// intentionally not representable.
type EvidenceClass string

const (
	EvidenceRemoteTree    EvidenceClass = "remote-tree"
	EvidenceFreshWorktree EvidenceClass = "fresh-worktree"
)

// OperationKind describes the only mutations a later installer may perform.
type OperationKind string

const (
	OperationCreate          OperationKind = "create"
	OperationReplaceOwned    OperationKind = "replace-owned"
	OperationRemoveTombstone OperationKind = "remove-tombstone"
)

// FileType is a no-follow destination state. Directories and special files are
// deliberately outside the plan language.
type FileType string

const (
	FileAbsent          FileType = "absent"
	FileRegular         FileType = "regular-file"
	FileRelativeSymlink FileType = "relative-symlink"
)

// ValidationOptions rebinds a reviewed envelope to current trusted inputs.
// Every field is required and must exactly match the corresponding plan field.
type ValidationOptions struct {
	ExpectedManifestDigest  string
	ExpectedInventoryDigest string
	ExpectedSourceSHA       string
	EvaluationTime          time.Time
}

// PlanEnvelope carries a canonical SHA-256 digest over Plan. The digest covers
// canonical compact JSON for Plan only, not envelope whitespace or its digest
// field.
type PlanEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	PlanSHA256    string `json:"plan_sha256"`
	Plan          Plan   `json:"plan"`
}

// Plan is a declarative set of repository-relative file transitions.
type Plan struct {
	ID                   string          `json:"id"`
	TargetRepository     string          `json:"target_repository"`
	GeneratorSHA         string          `json:"generator_sha"`
	ManifestDigest       string          `json:"manifest_digest"`
	InventoryDigest      string          `json:"inventory_digest"`
	PrimarySourceBinding string          `json:"primary_source_binding"`
	ExpectedSourceSHA    string          `json:"expected_source_sha"`
	SourceBindings       []SourceBinding `json:"source_bindings"`
	Operations           []Operation     `json:"operations"`
}

// SourceBinding is the exact, eligible observation from which staged payloads
// were rendered. RequestedSHA and TestedSHA must match.
type SourceBinding struct {
	ID            string        `json:"id"`
	Repository    string        `json:"repository"`
	RemoteURL     string        `json:"remote_url"`
	RequestedRef  string        `json:"requested_ref"`
	RequestedSHA  string        `json:"requested_sha"`
	TestedRef     string        `json:"tested_ref"`
	TestedSHA     string        `json:"tested_sha"`
	EvidenceClass EvidenceClass `json:"evidence_class"`
	CheckoutDirty bool          `json:"checkout_dirty"`
	ObservedAt    time.Time     `json:"observed_at"`
	ValidUntil    time.Time     `json:"valid_until"`
}

// Operation binds one manifest object to an exact before and after state. The
// source binding identifiers are a set and are canonicalized lexically.
type Operation struct {
	ID               string        `json:"id"`
	ObjectID         string        `json:"object_id"`
	Kind             OperationKind `json:"kind"`
	Destination      string        `json:"destination"`
	SourceBindingIDs []string      `json:"source_binding_ids"`
	Before           FileState     `json:"before"`
	After            FileState     `json:"after"`
	Payload          *PayloadRef   `json:"payload,omitempty"`
}

// FileState is the complete no-follow state relevant to a plan. Hash is a
// sha256 digest of regular-file bytes or relative-symlink target bytes.
type FileState struct {
	Type          FileType           `json:"type"`
	Mode          string             `json:"mode,omitempty"`
	Hash          string             `json:"hash,omitempty"`
	SymlinkTarget string             `json:"symlink_target,omitempty"`
	Marker        *MarkerExpectation `json:"marker,omitempty"`
}

// PayloadRef addresses a staged regular-file payload by content and size. It
// contains no host path and does not grant filesystem authority.
type PayloadRef struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// MarkerExpectation describes the sanitized ownership receipt required for a
// materialized state. StateHash must equal its FileState hash.
type MarkerExpectation struct {
	Owner     string `json:"owner"`
	ObjectID  string `json:"object_id"`
	StateHash string `json:"state_hash"`
}
