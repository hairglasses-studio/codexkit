package agentorchestration

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Input contains all state needed for a pure routing decision.
type Input struct {
	Now      time.Time
	Task     TaskPacket
	Quota    *QuotaSnapshot
	Capacity *CapacitySnapshot
	Ledger   LeaseLedger
}

// Policy is an immutable harness policy evaluator.
type Policy struct {
	config Config
}

// DefaultConfig returns conservative harness-owned limits. These values are
// not official provider limits and should be reviewed by each deployment.
func DefaultConfig() Config {
	return Config{
		MaxActiveTasks:      8,
		MaxActiveSparkTasks: 1,
		MaxReadScopes:       16,
		MaxWriteScopes:      8,
		MaxDoneWhen:         16,
		MaxAttempts:         4,
		MaxTaskTimeout:      2 * time.Hour,
		MaxAttemptTimeout:   30 * time.Minute,
		MaxSparkTaskTimeout: 20 * time.Minute,
		MaxSparkAttempts:    2,
		MaxLeaseDuration:    2 * time.Hour,
		QuotaMaxAge:         2 * time.Minute,
		CapacityMaxAge:      2 * time.Minute,
		SoftQuotaFraction:   0.20,
		HardQuotaFraction:   0.05,
		NativeSlotReserve:   1,
	}
}

// NewPolicy validates and copies config. At least one frontier/native slot is
// always reserved; callers cannot configure this safety invariant away.
func NewPolicy(config Config) (Policy, error) {
	if config.MaxActiveTasks < 1 || config.MaxActiveSparkTasks < 1 || config.MaxActiveSparkTasks > config.MaxActiveTasks {
		return Policy{}, fmt.Errorf("invalid active task caps")
	}
	if config.MaxReadScopes < 1 || config.MaxWriteScopes < 1 || config.MaxDoneWhen < 1 {
		return Policy{}, fmt.Errorf("invalid task shape caps")
	}
	if config.MaxAttempts < 1 || config.MaxSparkAttempts < 1 || config.MaxSparkAttempts > config.MaxAttempts {
		return Policy{}, fmt.Errorf("invalid attempt caps")
	}
	if config.MaxTaskTimeout <= 0 || config.MaxAttemptTimeout <= 0 || config.MaxSparkTaskTimeout <= 0 || config.MaxLeaseDuration <= 0 {
		return Policy{}, fmt.Errorf("invalid timeout caps")
	}
	if config.MaxAttemptTimeout > config.MaxTaskTimeout || config.MaxSparkTaskTimeout > config.MaxTaskTimeout {
		return Policy{}, fmt.Errorf("nested timeout cap exceeds task timeout cap")
	}
	if config.QuotaMaxAge <= 0 || config.CapacityMaxAge <= 0 {
		return Policy{}, fmt.Errorf("snapshot max ages must be positive")
	}
	if err := validateThresholds(config.HardQuotaFraction, config.SoftQuotaFraction); err != nil {
		return Policy{}, err
	}
	seenThreshold := make(map[string]struct{}, len(config.QuotaThresholds))
	for _, threshold := range config.QuotaThresholds {
		if strings.TrimSpace(threshold.BucketID) == "" {
			return Policy{}, fmt.Errorf("quota threshold bucket id is empty")
		}
		if _, exists := seenThreshold[threshold.BucketID]; exists {
			return Policy{}, fmt.Errorf("duplicate quota threshold for %q", threshold.BucketID)
		}
		seenThreshold[threshold.BucketID] = struct{}{}
		if err := validateThresholds(threshold.HardFraction, threshold.SoftFraction); err != nil {
			return Policy{}, fmt.Errorf("quota threshold %q: %w", threshold.BucketID, err)
		}
	}
	if config.NativeSlotReserve < 1 {
		return Policy{}, fmt.Errorf("native slot reserve must be at least one")
	}

	copyConfig := config
	copyConfig.QuotaThresholds = append([]QuotaThreshold(nil), config.QuotaThresholds...)
	copyConfig.RequiredQuotaBuckets = make(map[ModelLane][]string, len(config.RequiredQuotaBuckets))
	for lane, buckets := range config.RequiredQuotaBuckets {
		if lane != LaneFrontier && lane != LaneSpark {
			return Policy{}, fmt.Errorf("invalid required quota lane %q", lane)
		}
		copyConfig.RequiredQuotaBuckets[lane] = append([]string(nil), buckets...)
	}
	return Policy{config: copyConfig}, nil
}

func validateThresholds(hard, soft float64) error {
	if hard < 0 || soft < 0 || hard > soft || soft >= 1 {
		return fmt.Errorf("quota thresholds require 0 <= hard <= soft < 1")
	}
	return nil
}

// Config returns a defensive copy of the policy configuration.
func (p Policy) Config() Config {
	config := p.config
	config.QuotaThresholds = append([]QuotaThreshold(nil), p.config.QuotaThresholds...)
	config.RequiredQuotaBuckets = make(map[ModelLane][]string, len(p.config.RequiredQuotaBuckets))
	for lane, buckets := range p.config.RequiredQuotaBuckets {
		config.RequiredQuotaBuckets[lane] = append([]string(nil), buckets...)
	}
	return config
}

// Decide evaluates task shape, ownership, quota, and capacity without side
// effects. Auto routes read-only tasks to Spark and write tasks to frontier.
func (p Policy) Decide(input Input) RoutingDecision {
	task := canonicalTask(input.Task)
	lane := selectLane(task)
	decision := RoutingDecision{
		SchemaVersion: SchemaVersion,
		TaskID:        task.ID,
		SelectedLane:  lane,
		EvaluatedAt:   input.Now,
		Admission: AdmissionState{
			Status:              AdmissionRejected,
			NativeSlotsReserved: p.config.NativeSlotReserve,
		},
	}

	decision.Reasons = append(decision.Reasons, p.validateTask(task, lane)...)
	if input.Now.IsZero() {
		decision.Reasons = append(decision.Reasons, reject(ReasonInvalidTask, "evaluation time is required"))
	}
	if hasReject(decision.Reasons) {
		return finalizeDecision(decision)
	}

	ledger := input.Ledger
	if ledger.SchemaVersion == 0 && len(ledger.Leases) == 0 {
		ledger.SchemaVersion = SchemaVersion
	}
	if err := ledger.Validate(); err != nil {
		decision.Reasons = append(decision.Reasons, reject(ReasonInvalidTask, "invalid lease ledger: "+err.Error()))
		return finalizeDecision(decision)
	}
	active := ledger.Active(input.Now)
	decision.Admission.ActiveLeases = len(active)
	for _, lease := range active {
		if lease.TaskID == task.ID {
			decision.Reasons = append(decision.Reasons, reject(
				ReasonDuplicateActiveTask,
				"task id already has an active lease; reconcile or release it before retrying",
			))
		}
		if lease.Lane == LaneSpark {
			decision.Admission.ActiveSparkLeases++
		}
	}
	if hasReject(decision.Reasons) {
		return finalizeDecision(decision)
	}

	conflicts, err := ledger.Conflicts(task, input.Now)
	if err != nil {
		decision.Reasons = append(decision.Reasons, reject(ReasonInvalidTask, "scope collision check failed: "+err.Error()))
		return finalizeDecision(decision)
	}
	decision.Reasons = append(decision.Reasons, conflictReasons(conflicts, active)...)
	if hasReject(decision.Reasons) {
		return finalizeDecision(decision)
	}

	decision.Reasons = append(decision.Reasons, p.quotaReasons(input.Now, lane, input.Quota)...)
	decision.Reasons = append(decision.Reasons, p.capacityReasons(input.Now, lane, input.Capacity, decision.Admission.ActiveLeases, decision.Admission.ActiveSparkLeases)...)
	if input.Capacity != nil && input.Capacity.TotalSlots >= input.Capacity.InUseSlots {
		decision.Admission.FreeSlots = input.Capacity.TotalSlots - input.Capacity.InUseSlots
	}
	if hasReject(decision.Reasons) || hasBackpressure(decision.Reasons) {
		return finalizeDecision(decision)
	}

	decision.Admitted = true
	decision.Reasons = append(decision.Reasons, DecisionReason{
		Code:     ReasonAccepted,
		Severity: SeverityInfo,
		Message:  "task satisfies harness routing policy",
	})
	leaseDuration := task.TimeoutBudget.Total
	if leaseDuration > p.config.MaxLeaseDuration {
		leaseDuration = p.config.MaxLeaseDuration
	}
	decision.Lease = &Lease{
		ID:                 task.ID + "@" + input.Now.UTC().Format(time.RFC3339Nano),
		TaskID:             task.ID,
		Lane:               lane,
		ReadScopes:         append([]PathScope(nil), task.Scopes.Read...),
		WriteScopes:        append([]PathScope(nil), task.Scopes.Write...),
		ExclusiveOwnership: task.ExclusiveOwnership,
		AcquiredAt:         input.Now,
		ExpiresAt:          input.Now.Add(leaseDuration),
	}
	return finalizeDecision(decision)
}

func (p Policy) validateTask(task TaskPacket, lane ModelLane) []DecisionReason {
	var reasons []DecisionReason
	if task.SchemaVersion != SchemaVersion {
		reasons = append(reasons, reject(ReasonInvalidTask, fmt.Sprintf("unsupported task schema version %d", task.SchemaVersion)))
	}
	if strings.TrimSpace(task.ID) == "" {
		reasons = append(reasons, reject(ReasonInvalidTask, "task id is required"))
	}
	if ambiguousObjective(task.Objective, task.DoneWhen) {
		reasons = append(reasons, reject(ReasonAmbiguousObjective, "objective must be specific and include non-empty done-when criteria"))
	}
	if len(task.DoneWhen) > p.config.MaxDoneWhen {
		reasons = append(reasons, reject(ReasonInvalidTask, "done-when criteria exceed policy cap"))
	}
	if !validTaskClass(task.Class) {
		reasons = append(reasons, reject(ReasonInvalidTask, "task class is missing or invalid"))
	}
	if task.RequestedLane != LaneAuto && task.RequestedLane != LaneFrontier && task.RequestedLane != LaneSpark {
		reasons = append(reasons, reject(ReasonInvalidTask, "requested lane is invalid"))
	}
	if lane != LaneFrontier && lane != LaneSpark {
		reasons = append(reasons, reject(ReasonInvalidTask, "selected lane is invalid"))
	}
	if task.Class == TaskExternal || task.Class == TaskDestructive || task.Class == TaskPublish || task.Effects.ExternalAction || task.Effects.Destructive || task.Effects.Publish {
		reasons = append(reasons, reject(ReasonForbiddenEffect, "external, destructive, and publish tasks require an operator-owned workflow"))
	}
	if len(task.Scopes.Read) > p.config.MaxReadScopes || len(task.Scopes.Write) > p.config.MaxWriteScopes {
		reasons = append(reasons, reject(ReasonInvalidTask, "task scopes exceed policy cap"))
	}
	if len(task.Scopes.Read)+len(task.Scopes.Write) == 0 {
		reasons = append(reasons, reject(ReasonInvalidTask, "task must declare a bounded read or write scope"))
	}
	for label, scopes := range map[string][]PathScope{"read": task.Scopes.Read, "write": task.Scopes.Write, "forbidden": task.Scopes.Forbidden} {
		if _, err := canonicalScopes(scopes); err != nil {
			reasons = append(reasons, reject(ReasonInvalidTask, label+" scopes: "+err.Error()))
		}
	}
	verificationIDs := make(map[string]struct{}, len(task.VerificationRequirements))
	for _, requirement := range task.VerificationRequirements {
		id := strings.TrimSpace(requirement.ID)
		if id == "" || strings.TrimSpace(requirement.Description) == "" ||
			(requirement.Lane != LaneFrontier && requirement.Lane != LaneSpark) {
			reasons = append(reasons, reject(ReasonInvalidTask, "verification requirement is incomplete or has an invalid lane"))
			continue
		}
		if _, exists := verificationIDs[id]; exists {
			reasons = append(reasons, reject(ReasonInvalidTask, "verification requirement ids must be unique"))
			continue
		}
		verificationIDs[id] = struct{}{}
	}
	for _, accessed := range [][]PathScope{task.Scopes.Read, task.Scopes.Write} {
		collisions, err := DetectScopeCollisions(accessed, task.Scopes.Forbidden)
		if err == nil && len(collisions) != 0 {
			reasons = append(reasons, reject(ReasonForbiddenScope, "requested scope intersects a forbidden boundary"))
			break
		}
	}
	if reason := p.validateBudgets(task, lane); reason != nil {
		reasons = append(reasons, *reason)
	}
	if lane == LaneSpark && len(task.Scopes.Write) != 0 {
		reasons = append(reasons, reject(ReasonSparkWrite, "Spark is read-only by default"))
	}
	if len(task.Scopes.Write) != 0 {
		if !task.ExclusiveOwnership {
			reasons = append(reasons, reject(ReasonMissingExclusiveOwnership, "write tasks require exclusive ownership"))
		}
		if !hasFrontierVerifier(task.VerificationRequirements) {
			reasons = append(reasons, reject(ReasonMissingFrontierVerifier, "write tasks require a non-Spark verification"))
		}
	}
	return reasons
}

func (p Policy) validateBudgets(task TaskPacket, lane ModelLane) *DecisionReason {
	retry := task.RetryBudget
	timeout := task.TimeoutBudget
	invalid := retry.MaxAttempts < 1 || retry.MaxAttempts > p.config.MaxAttempts ||
		retry.MaxElapsed <= 0 || retry.BaseBackoff <= 0 || retry.MaxBackoff < retry.BaseBackoff ||
		retry.JitterFraction < 0 || retry.JitterFraction > 1 ||
		timeout.Attempt <= 0 || timeout.Total <= 0 || timeout.Attempt > timeout.Total ||
		timeout.Attempt > p.config.MaxAttemptTimeout || timeout.Total > p.config.MaxTaskTimeout ||
		retry.MaxElapsed > timeout.Total
	if lane == LaneSpark && (retry.MaxAttempts > p.config.MaxSparkAttempts || timeout.Total > p.config.MaxSparkTaskTimeout) {
		invalid = true
	}
	if invalid {
		reason := reject(ReasonInvalidTask, "retry or timeout budget is invalid or exceeds policy cap")
		return &reason
	}
	return nil
}

func (p Policy) quotaReasons(now time.Time, lane ModelLane, snapshot *QuotaSnapshot) []DecisionReason {
	if snapshot == nil || snapshot.SchemaVersion == 0 || snapshot.ObservedAt.IsZero() {
		return []DecisionReason{reject(ReasonMissingQuota, "a current quota snapshot is required")}
	}
	if snapshot.SchemaVersion != SchemaVersion {
		return []DecisionReason{reject(ReasonInvalidQuota, "unsupported quota snapshot schema")}
	}
	if snapshot.ObservedAt.After(now) || now.Sub(snapshot.ObservedAt) > p.config.QuotaMaxAge {
		return []DecisionReason{reject(ReasonStaleQuota, "quota snapshot is stale or from the future")}
	}

	byID := make(map[string]QuotaBucket)
	var buckets []QuotaBucket
	for _, bucket := range snapshot.Buckets {
		if bucket.Lane != lane {
			continue
		}
		if bucket.ID == "" || bucket.Limit <= 0 || bucket.Remaining < 0 || bucket.Remaining > bucket.Limit || bucket.ResetAt.IsZero() {
			return []DecisionReason{reject(ReasonInvalidQuota, "quota bucket is invalid")}
		}
		if _, exists := byID[bucket.ID]; exists {
			return []DecisionReason{reject(ReasonInvalidQuota, "quota bucket ids must be unique per lane")}
		}
		byID[bucket.ID] = bucket
		buckets = append(buckets, bucket)
	}
	required := p.config.RequiredQuotaBuckets[lane]
	if len(required) != 0 {
		buckets = buckets[:0]
		for _, id := range required {
			bucket, exists := byID[id]
			if !exists {
				return []DecisionReason{reject(ReasonMissingQuota, "required quota bucket is missing: "+id)}
			}
			buckets = append(buckets, bucket)
		}
	}
	if len(buckets) == 0 {
		return []DecisionReason{reject(ReasonMissingQuota, "no quota buckets are available for the selected lane")}
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].ID < buckets[j].ID })

	var reasons []DecisionReason
	for _, bucket := range buckets {
		if !bucket.ResetAt.After(now) {
			reasons = append(reasons, DecisionReason{
				Code:     ReasonStaleQuota,
				Severity: SeverityReject,
				Message:  "quota bucket reset has passed; refresh the snapshot",
				BucketID: bucket.ID,
			})
			continue
		}
		hard, soft := p.thresholds(bucket.ID)
		fraction := float64(bucket.Remaining) / float64(bucket.Limit)
		switch {
		case fraction <= hard:
			reasons = append(reasons, DecisionReason{
				Code:     ReasonQuotaHardLimit,
				Severity: SeverityReject,
				Message:  fmt.Sprintf("quota bucket remaining fraction %.4f is at or below hard threshold %.4f", fraction, hard),
				BucketID: bucket.ID,
				RetryAt:  bucket.ResetAt,
			})
		case fraction <= soft:
			reasons = append(reasons, DecisionReason{
				Code:     ReasonQuotaSoftLimit,
				Severity: SeverityBackpressure,
				Message:  fmt.Sprintf("quota bucket remaining fraction %.4f is at or below soft threshold %.4f", fraction, soft),
				BucketID: bucket.ID,
				RetryAt:  bucket.ResetAt,
			})
		}
	}
	return reasons
}

func (p Policy) thresholds(bucketID string) (hard, soft float64) {
	for _, threshold := range p.config.QuotaThresholds {
		if threshold.BucketID == bucketID {
			return threshold.HardFraction, threshold.SoftFraction
		}
	}
	return p.config.HardQuotaFraction, p.config.SoftQuotaFraction
}

func (p Policy) capacityReasons(now time.Time, lane ModelLane, snapshot *CapacitySnapshot, active, activeSpark int) []DecisionReason {
	if snapshot == nil || snapshot.SchemaVersion == 0 || snapshot.ObservedAt.IsZero() {
		return []DecisionReason{reject(ReasonMissingCapacity, "a current capacity snapshot is required")}
	}
	if snapshot.SchemaVersion != SchemaVersion || snapshot.TotalSlots < 1 || snapshot.InUseSlots < 0 || snapshot.InUseSlots > snapshot.TotalSlots {
		return []DecisionReason{reject(ReasonMissingCapacity, "capacity snapshot is invalid")}
	}
	if snapshot.ObservedAt.After(now) || now.Sub(snapshot.ObservedAt) > p.config.CapacityMaxAge {
		return []DecisionReason{reject(ReasonStaleCapacity, "capacity snapshot is stale or from the future")}
	}
	var found *LaneCapacity
	for i := range snapshot.Lanes {
		candidate := &snapshot.Lanes[i]
		if candidate.Lane != lane {
			continue
		}
		if found != nil {
			return []DecisionReason{reject(ReasonMissingCapacity, "lane capacity entries must be unique")}
		}
		found = candidate
	}
	if found == nil || !found.Available || found.TotalSlots < 1 {
		return []DecisionReason{reject(ReasonModelUnavailable, "selected model lane is unavailable")}
	}
	if found.InUseSlots < 0 || found.InUseSlots > found.TotalSlots {
		return []DecisionReason{reject(ReasonMissingCapacity, "lane capacity is invalid")}
	}
	var reasons []DecisionReason
	if active >= p.config.MaxActiveTasks {
		reasons = append(reasons, backpressure(ReasonConcurrentTaskCap, "active task cap reached", time.Time{}))
	}
	if lane == LaneSpark && activeSpark >= p.config.MaxActiveSparkTasks {
		reasons = append(reasons, backpressure(ReasonConcurrentSparkCap, "active Spark task cap reached", time.Time{}))
	}
	if snapshot.InUseSlots >= snapshot.TotalSlots || found.InUseSlots >= found.TotalSlots {
		reasons = append(reasons, backpressure(ReasonCapacityExhausted, "executor or lane capacity is exhausted", time.Time{}))
	}
	free := snapshot.TotalSlots - snapshot.InUseSlots
	if lane == LaneSpark && free <= p.config.NativeSlotReserve {
		reasons = append(reasons, backpressure(ReasonNativeCapacityReserve, "Spark admission would consume the frontier/native slot reserve", time.Time{}))
	}
	return reasons
}

func selectLane(task TaskPacket) ModelLane {
	if task.RequestedLane != LaneAuto {
		return task.RequestedLane
	}
	if len(task.Scopes.Write) == 0 {
		return LaneSpark
	}
	return LaneFrontier
}

func ambiguousObjective(objective string, doneWhen []string) bool {
	if len(strings.Fields(strings.TrimSpace(objective))) < 3 || len(doneWhen) == 0 {
		return true
	}
	for _, criterion := range doneWhen {
		if strings.TrimSpace(criterion) == "" {
			return true
		}
	}
	return false
}

func validTaskClass(class TaskClass) bool {
	switch class {
	case TaskResearch, TaskImplementation, TaskVerification, TaskExternal, TaskDestructive, TaskPublish:
		return true
	default:
		return false
	}
}

func hasFrontierVerifier(requirements []VerificationRequirement) bool {
	for _, requirement := range requirements {
		if requirement.Required && strings.TrimSpace(requirement.ID) != "" && requirement.Lane == LaneFrontier {
			return true
		}
	}
	return false
}

func conflictReasons(conflicts []LeaseConflict, active []Lease) []DecisionReason {
	var writerIDs, readerIDs []string
	var retryAt time.Time
	expires := make(map[string]time.Time, len(active))
	for _, lease := range active {
		expires[lease.TaskID] = lease.ExpiresAt
	}
	for _, conflict := range conflicts {
		switch conflict.Kind {
		case ConflictWriteWrite, ConflictWriteRead:
			writerIDs = append(writerIDs, conflict.TaskID)
		case ConflictReadWrite:
			readerIDs = append(readerIDs, conflict.TaskID)
			if expires[conflict.TaskID].After(retryAt) {
				retryAt = expires[conflict.TaskID]
			}
		}
	}
	writerIDs = uniqueStrings(writerIDs)
	readerIDs = uniqueStrings(readerIDs)
	var reasons []DecisionReason
	if len(writerIDs) != 0 {
		reasons = append(reasons, DecisionReason{
			Code:               ReasonScopeCollision,
			Severity:           SeverityReject,
			Message:            "exclusive write scope overlaps an active lease",
			ConflictingTaskIDs: writerIDs,
		})
	}
	if len(readerIDs) != 0 {
		reasons = append(reasons, DecisionReason{
			Code:               ReasonActiveWriterReadContention,
			Severity:           SeverityBackpressure,
			Message:            "read scope overlaps an active writer",
			ConflictingTaskIDs: readerIDs,
			RetryAt:            retryAt,
		})
	}
	return reasons
}

func reject(code, message string) DecisionReason {
	return DecisionReason{Code: code, Severity: SeverityReject, Message: message}
}

func backpressure(code, message string, retryAt time.Time) DecisionReason {
	return DecisionReason{Code: code, Severity: SeverityBackpressure, Message: message, RetryAt: retryAt}
}

func hasReject(reasons []DecisionReason) bool {
	for _, reason := range reasons {
		if reason.Severity == SeverityReject {
			return true
		}
	}
	return false
}

func hasBackpressure(reasons []DecisionReason) bool {
	for _, reason := range reasons {
		if reason.Severity == SeverityBackpressure {
			return true
		}
	}
	return false
}

func finalizeDecision(decision RoutingDecision) RoutingDecision {
	sortReasons(decision.Reasons)
	decision.Admission.Backpressure = BackpressureState{}
	switch {
	case hasReject(decision.Reasons):
		decision.Admitted = false
		decision.Admission.Status = AdmissionRejected
		decision.Lease = nil
	case hasBackpressure(decision.Reasons):
		decision.Admitted = false
		decision.Admission.Status = AdmissionBackpressure
		decision.Lease = nil
		decision.Admission.Backpressure.Active = true
		for _, reason := range decision.Reasons {
			if reason.Severity != SeverityBackpressure {
				continue
			}
			decision.Admission.Backpressure.Reasons = append(decision.Admission.Backpressure.Reasons, reason)
			if reason.RetryAt.After(decision.Admission.Backpressure.RetryAt) {
				decision.Admission.Backpressure.RetryAt = reason.RetryAt
			}
		}
	default:
		decision.Admitted = true
		decision.Admission.Status = AdmissionAdmitted
	}
	return decision
}

func sortReasons(reasons []DecisionReason) {
	for i := range reasons {
		reasons[i].ConflictingTaskIDs = uniqueStrings(reasons[i].ConflictingTaskIDs)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Severity != reasons[j].Severity {
			return reasons[i].Severity < reasons[j].Severity
		}
		if reasons[i].Code != reasons[j].Code {
			return reasons[i].Code < reasons[j].Code
		}
		if reasons[i].BucketID != reasons[j].BucketID {
			return reasons[i].BucketID < reasons[j].BucketID
		}
		return reasons[i].Message < reasons[j].Message
	})
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func canonicalTask(task TaskPacket) TaskPacket {
	task.ID = strings.TrimSpace(task.ID)
	task.Objective = strings.TrimSpace(task.Objective)
	task.DoneWhen = append([]string(nil), task.DoneWhen...)
	for i := range task.DoneWhen {
		task.DoneWhen[i] = strings.TrimSpace(task.DoneWhen[i])
	}
	task.DoneWhen = uniqueStrings(task.DoneWhen)
	task.Scopes.Read = canonicalScopesIfValid(task.Scopes.Read)
	task.Scopes.Write = canonicalScopesIfValid(task.Scopes.Write)
	task.Scopes.Forbidden = canonicalScopesIfValid(task.Scopes.Forbidden)
	task.VerificationRequirements = append([]VerificationRequirement(nil), task.VerificationRequirements...)
	sort.Slice(task.VerificationRequirements, func(i, j int) bool {
		return task.VerificationRequirements[i].ID < task.VerificationRequirements[j].ID
	})
	return task
}

func canonicalScopesIfValid(scopes []PathScope) []PathScope {
	canonical, err := canonicalScopes(scopes)
	if err == nil {
		return canonical
	}
	return append([]PathScope(nil), scopes...)
}
