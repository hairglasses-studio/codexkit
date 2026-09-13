package harnessrender

import (
	"errors"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessplan"
)

const (
	// MaxMaterialBlobBytes is the hard upper bound for one source or rendered
	// material blob. Options may tighten but never raise this limit.
	MaxMaterialBlobBytes int64 = 64 << 20
	// MaxAggregateMaterialBytes bounds all source and rendered material supplied
	// to one Build call. Options may tighten but never raise this limit.
	MaxAggregateMaterialBytes int64 = 512 << 20
	// MaxMaterialInputsPerObject bounds zero-byte and small-blob cardinality for
	// one operated object independently from byte limits.
	MaxMaterialInputsPerObject = harnessplan.MaxSourceBindings
	// MaxAggregateMaterialInputs bounds total ordered input entries independently
	// from byte limits so zero-byte material cannot amplify validation work.
	MaxAggregateMaterialInputs = harnessplan.MaxOperations
)

var ErrInvalidRender = errors.New("invalid harness render")

// Trust contains the independently reviewed values that a sealed plan cannot
// authorize for itself. Every field is required.
type Trust struct {
	ReviewedPlanSHA256 string
	TargetRepository   string
	ManifestRepository string
	MarkerOwner        string
	SourceSHA          string
	EvaluationTime     time.Time
}

// Options supplies manifest registry state, inventory freshness, and optional
// tighter material limits. A zero material limit selects its package maximum.
type Options struct {
	Trust                 Trust
	RegisteredVerifierIDs []string
	InventoryMaxAge       time.Duration
	MaxBlobBytes          int64
	MaxTotalBytes         int64
}

// Authority is the material-independent result of validating the canonical
// manifest and inventory, the sealed plan, and every reviewed trust binding.
// It contains no paths or material bytes and grants no filesystem authority.
type Authority struct {
	PlanSHA256       string `json:"plan_sha256"`
	ManifestDigest   string `json:"manifest_digest"`
	InventoryDigest  string `json:"inventory_digest"`
	TargetRepository string `json:"target_repository"`
}

// Materials contains the ordered source blobs and, for render projections,
// the caller-supplied rendered payload for one operated manifest object.
// Objects without a plan operation must not have a Materials entry.
type Materials struct {
	ObjectID string
	Inputs   [][]byte
	Rendered *RenderedPayload
}

// RenderedPayload distinguishes an explicitly supplied empty render from a
// missing render.
type RenderedPayload struct {
	Bytes []byte
}

// Artifact is one fully verified operation and its inline after-state payload.
// RegularBytes is present only for regular files. Relative symlink targets are
// carried exactly in After.SymlinkTarget. Remove-tombstone artifacts have an
// absent After state and no inline payload.
type Artifact struct {
	OperationID  string                    `json:"operation_id"`
	ObjectID     string                    `json:"object_id"`
	Kind         harnessplan.OperationKind `json:"kind"`
	Destination  string                    `json:"destination"`
	Before       harnessplan.FileState     `json:"before"`
	After        harnessplan.FileState     `json:"after"`
	RegularBytes []byte                    `json:"regular_bytes"`
}

// Result binds deterministic artifacts to the exact reviewed inputs. Artifacts
// is a non-nil empty slice for a valid zero-operation plan.
type Result struct {
	PlanSHA256       string     `json:"plan_sha256"`
	ManifestDigest   string     `json:"manifest_digest"`
	InventoryDigest  string     `json:"inventory_digest"`
	TargetRepository string     `json:"target_repository"`
	Artifacts        []Artifact `json:"artifacts"`
}

// Issue is one stable cross-contract validation finding.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError contains independently detectable cross-contract findings.
type ValidationError struct {
	Issues []Issue
}
