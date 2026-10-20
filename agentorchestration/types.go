package agentorchestration

import "time"

const SchemaVersion = 1

// ModelLane is a harness routing lane, not an official provider model name.
type ModelLane string

const (
	LaneAuto     ModelLane = "auto"
	LaneFrontier ModelLane = "frontier"
	LaneSpark    ModelLane = "spark"
)

// TaskClass describes the kind of work requested by a task packet.
type TaskClass string

const (
	TaskResearch       TaskClass = "research"
	TaskImplementation TaskClass = "implementation"
	TaskVerification   TaskClass = "verification"
	TaskExternal       TaskClass = "external"
	TaskDestructive    TaskClass = "destructive"
	TaskPublish        TaskClass = "publish"
)

// ScopeKind controls path collision semantics. A directory owns all of its
// descendants; a file owns only its exact normalized path.
type ScopeKind string

const (
	ScopeFile      ScopeKind = "file"
	ScopeDirectory ScopeKind = "directory"
)

// PathScope identifies one repository-relative or absolute file/directory
// boundary. Paths are lexical policy data; this package never opens them.
type PathScope struct {
	Path string    `json:"path"`
	Kind ScopeKind `json:"kind"`
}

// TaskScopes makes requested reads, writes, and forbidden boundaries explicit.
type TaskScopes struct {
	Read      []PathScope `json:"read,omitempty"`
	Write     []PathScope `json:"write,omitempty"`
	Forbidden []PathScope `json:"forbidden,omitempty"`
}

// TaskEffects are effects that cannot be inferred reliably from path scopes.
type TaskEffects struct {
	ExternalAction bool `json:"external_action,omitempty"`
	Destructive    bool `json:"destructive,omitempty"`
	Publish        bool `json:"publish,omitempty"`
}

// VerificationRequirement declares an independent completion check. Write
// tasks must include a required frontier verification so Spark cannot approve
// its own mutation.
type VerificationRequirement struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Lane        ModelLane `json:"lane"`
	Required    bool      `json:"required"`
}

// RetryBudget bounds attempts and defines exponential-backoff policy. Duration
// JSON fields are nanoseconds and include the unit in their field name.
type RetryBudget struct {
	MaxAttempts    int           `json:"max_attempts"`
	MaxElapsed     time.Duration `json:"max_elapsed_ns"`
	BaseBackoff    time.Duration `json:"base_backoff_ns"`
	MaxBackoff     time.Duration `json:"max_backoff_ns"`
	JitterFraction float64       `json:"jitter_fraction"`
}

// TimeoutBudget bounds a single attempt and the complete task.
type TimeoutBudget struct {
	Attempt time.Duration `json:"attempt_ns"`
	Total   time.Duration `json:"total_ns"`
}

// TaskPacket is the complete, versioned unit evaluated by Policy.
type TaskPacket struct {
	SchemaVersion            int                       `json:"schema_version"`
	ID                       string                    `json:"id"`
	Objective                string                    `json:"objective"`
	DoneWhen                 []string                  `json:"done_when"`
	Class                    TaskClass                 `json:"class"`
	RequestedLane            ModelLane                 `json:"requested_lane"`
	Scopes                   TaskScopes                `json:"scopes"`
	Effects                  TaskEffects               `json:"effects"`
	ExclusiveOwnership       bool                      `json:"exclusive_ownership"`
	VerificationRequirements []VerificationRequirement `json:"verification_requirements,omitempty"`
	RetryBudget              RetryBudget               `json:"retry_budget"`
	TimeoutBudget            TimeoutBudget             `json:"timeout_budget"`
}

// QuotaBucket is an independently enforced caller-observed quota dimension.
// ResetAt belongs to this bucket; buckets do not share reset schedules.
type QuotaBucket struct {
	ID        string    `json:"id"`
	Lane      ModelLane `json:"lane"`
	Remaining int64     `json:"remaining"`
	Limit     int64     `json:"limit"`
	ResetAt   time.Time `json:"reset_at"`
}

// QuotaSnapshot contains sanitized observations supplied by the caller.
type QuotaSnapshot struct {
	SchemaVersion int           `json:"schema_version"`
	ObservedAt    time.Time     `json:"observed_at"`
	Buckets       []QuotaBucket `json:"buckets"`
}

// LaneCapacity records model availability and optional per-lane concurrency.
// Available is an observation, not a product guarantee.
type LaneCapacity struct {
	Lane       ModelLane `json:"lane"`
	Available  bool      `json:"available"`
	TotalSlots int       `json:"total_slots"`
	InUseSlots int       `json:"in_use_slots"`
}

// CapacitySnapshot combines shared executor capacity with lane availability.
type CapacitySnapshot struct {
	SchemaVersion int            `json:"schema_version"`
	ObservedAt    time.Time      `json:"observed_at"`
	TotalSlots    int            `json:"total_slots"`
	InUseSlots    int            `json:"in_use_slots"`
	Lanes         []LaneCapacity `json:"lanes"`
}

// Lease is one active task's lexical ownership claim.
type Lease struct {
	ID                 string      `json:"id"`
	TaskID             string      `json:"task_id"`
	Lane               ModelLane   `json:"lane"`
	ReadScopes         []PathScope `json:"read_scopes,omitempty"`
	WriteScopes        []PathScope `json:"write_scopes,omitempty"`
	ExclusiveOwnership bool        `json:"exclusive_ownership"`
	AcquiredAt         time.Time   `json:"acquired_at"`
	ExpiresAt          time.Time   `json:"expires_at"`
}

// LeaseLedger is caller-persistable policy state. Methods return copies and do
// not read or write a ledger file.
type LeaseLedger struct {
	SchemaVersion int     `json:"schema_version"`
	Leases        []Lease `json:"leases"`
}

// AdmissionStatus distinguishes permanent policy rejection from temporary
// backpressure.
type AdmissionStatus string

const (
	AdmissionAdmitted     AdmissionStatus = "admitted"
	AdmissionBackpressure AdmissionStatus = "backpressure"
	AdmissionRejected     AdmissionStatus = "rejected"
)

// ReasonSeverity classifies the effect of a routing reason.
type ReasonSeverity string

const (
	SeverityInfo         ReasonSeverity = "info"
	SeverityBackpressure ReasonSeverity = "backpressure"
	SeverityReject       ReasonSeverity = "reject"
)

const (
	ReasonAccepted                   = "accepted"
	ReasonAmbiguousObjective         = "ambiguous_objective"
	ReasonInvalidTask                = "invalid_task"
	ReasonForbiddenEffect            = "forbidden_effect"
	ReasonForbiddenScope             = "forbidden_scope"
	ReasonSparkWrite                 = "spark_write"
	ReasonMissingFrontierVerifier    = "missing_frontier_verifier"
	ReasonMissingExclusiveOwnership  = "missing_exclusive_ownership"
	ReasonScopeCollision             = "scope_collision"
	ReasonDuplicateActiveTask        = "duplicate_active_task"
	ReasonMissingQuota               = "missing_quota"
	ReasonStaleQuota                 = "stale_quota"
	ReasonInvalidQuota               = "invalid_quota"
	ReasonQuotaHardLimit             = "quota_hard_limit"
	ReasonQuotaSoftLimit             = "quota_soft_limit"
	ReasonMissingCapacity            = "missing_capacity"
	ReasonStaleCapacity              = "stale_capacity"
	ReasonModelUnavailable           = "model_unavailable"
	ReasonCapacityExhausted          = "capacity_exhausted"
	ReasonNativeCapacityReserve      = "native_capacity_reserve"
	ReasonConcurrentTaskCap          = "concurrent_task_cap"
	ReasonConcurrentSparkCap         = "concurrent_spark_cap"
	ReasonActiveWriterReadContention = "active_writer_read_contention"
)

// DecisionReason is stable machine-readable policy evidence.
type DecisionReason struct {
	Code               string         `json:"code"`
	Severity           ReasonSeverity `json:"severity"`
	Message            string         `json:"message"`
	BucketID           string         `json:"bucket_id,omitempty"`
	ConflictingTaskIDs []string       `json:"conflicting_task_ids,omitempty"`
	RetryAt            time.Time      `json:"retry_at,omitempty"`
}

// BackpressureState captures why an otherwise valid task should wait.
type BackpressureState struct {
	Active  bool             `json:"active"`
	RetryAt time.Time        `json:"retry_at,omitempty"`
	Reasons []DecisionReason `json:"reasons,omitempty"`
}

// AdmissionState summarizes the capacity/lease state used for a decision.
type AdmissionState struct {
	Status              AdmissionStatus   `json:"status"`
	ActiveLeases        int               `json:"active_leases"`
	ActiveSparkLeases   int               `json:"active_spark_leases"`
	FreeSlots           int               `json:"free_slots"`
	NativeSlotsReserved int               `json:"native_slots_reserved"`
	Backpressure        BackpressureState `json:"backpressure"`
}

// RoutingDecision is a deterministic, evidence-bearing policy result.
type RoutingDecision struct {
	SchemaVersion int              `json:"schema_version"`
	TaskID        string           `json:"task_id"`
	SelectedLane  ModelLane        `json:"selected_lane"`
	EvaluatedAt   time.Time        `json:"evaluated_at"`
	Admitted      bool             `json:"admitted"`
	Admission     AdmissionState   `json:"admission"`
	Reasons       []DecisionReason `json:"reasons"`
	Lease         *Lease           `json:"lease,omitempty"`
}

// CriterionEvidence records the result for one done-when criterion.
type CriterionEvidence struct {
	Criterion string `json:"criterion"`
	Satisfied bool   `json:"satisfied"`
	Summary   string `json:"summary"`
}

// VerificationEvidence records one independently executed verification. The
// library validates this record but never executes the verification.
type VerificationEvidence struct {
	RequirementID  string    `json:"requirement_id"`
	Lane           ModelLane `json:"lane"`
	Passed         bool      `json:"passed"`
	Summary        string    `json:"summary"`
	ArtifactSHA256 string    `json:"artifact_sha256,omitempty"`
}

// CompletionEvidence is the typed handoff proving a task met its contract.
type CompletionEvidence struct {
	SchemaVersion int                    `json:"schema_version"`
	TaskID        string                 `json:"task_id"`
	Lane          ModelLane              `json:"lane"`
	CompletedAt   time.Time              `json:"completed_at"`
	Criteria      []CriterionEvidence    `json:"criteria"`
	Verifications []VerificationEvidence `json:"verifications,omitempty"`
	Summary       string                 `json:"summary"`
}

// EvidenceIssue identifies missing or failed completion proof.
type EvidenceIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CompletionValidation is the pure validation result for completion evidence.
type CompletionValidation struct {
	Complete bool            `json:"complete"`
	Issues   []EvidenceIssue `json:"issues,omitempty"`
}

// QuotaThreshold overrides the global threshold for one bucket ID.
type QuotaThreshold struct {
	BucketID     string  `json:"bucket_id"`
	SoftFraction float64 `json:"soft_fraction"`
	HardFraction float64 `json:"hard_fraction"`
}

// Config is harness-owned routing policy. None of its defaults are official
// OpenAI limits or guarantees.
type Config struct {
	MaxActiveTasks       int
	MaxActiveSparkTasks  int
	MaxReadScopes        int
	MaxWriteScopes       int
	MaxDoneWhen          int
	MaxAttempts          int
	MaxTaskTimeout       time.Duration
	MaxAttemptTimeout    time.Duration
	MaxSparkTaskTimeout  time.Duration
	MaxSparkAttempts     int
	MaxLeaseDuration     time.Duration
	QuotaMaxAge          time.Duration
	CapacityMaxAge       time.Duration
	SoftQuotaFraction    float64
	HardQuotaFraction    float64
	QuotaThresholds      []QuotaThreshold
	RequiredQuotaBuckets map[ModelLane][]string
	NativeSlotReserve    int
}
