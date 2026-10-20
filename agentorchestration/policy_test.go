package agentorchestration

import (
	"bytes"
	"testing"
	"time"
)

func TestDefaultConfigUsesDogfoodedSingleSparkCap(t *testing.T) {
	if got := DefaultConfig().MaxActiveSparkTasks; got != 1 {
		t.Fatalf("MaxActiveSparkTasks = %d, want the dogfooded safe default 1", got)
	}
}

func TestPolicyRoutingTable(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	policy := mustPolicy(t, DefaultConfig())

	tests := []struct {
		name       string
		mutate     func(*Input)
		status     AdmissionStatus
		lane       ModelLane
		reasonCode string
	}{
		{
			name:   "research accepted",
			status: AdmissionAdmitted,
			lane:   LaneSpark,
		},
		{
			name: "publish rejected",
			mutate: func(input *Input) {
				input.Task.Class = TaskPublish
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonForbiddenEffect,
		},
		{
			name: "Spark write rejected without verifier",
			mutate: func(input *Input) {
				input.Task.Class = TaskImplementation
				input.Task.RequestedLane = LaneSpark
				input.Task.Scopes.Write = []PathScope{{Path: "codexkit/agentorchestration/policy.go", Kind: ScopeFile}}
				input.Task.ExclusiveOwnership = true
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonSparkWrite,
		},
		{
			name: "disjoint isolated write conditionally accepted",
			mutate: func(input *Input) {
				input.Task.Class = TaskImplementation
				input.Task.Scopes.Read = []PathScope{{Path: "codexkit/agentorchestration", Kind: ScopeDirectory}}
				input.Task.Scopes.Write = []PathScope{{Path: "codexkit/agentorchestration/policy.go", Kind: ScopeFile}}
				input.Task.ExclusiveOwnership = true
				input.Task.VerificationRequirements = []VerificationRequirement{{
					ID:          "frontier-go-test",
					Description: "Frontier runs the hermetic Go test suite",
					Lane:        LaneFrontier,
					Required:    true,
				}}
				input.Ledger.Leases = []Lease{activeLease(now, "other-task", []PathScope{{Path: "research/openai", Kind: ScopeDirectory}})}
			},
			status: AdmissionAdmitted,
			lane:   LaneFrontier,
		},
		{
			name: "overlap rejection",
			mutate: func(input *Input) {
				input.Task.Class = TaskImplementation
				input.Task.Scopes.Write = []PathScope{{Path: "codexkit/agentorchestration/policy.go", Kind: ScopeFile}}
				input.Task.ExclusiveOwnership = true
				input.Task.VerificationRequirements = []VerificationRequirement{{
					ID:          "frontier-go-test",
					Description: "Frontier runs the hermetic Go test suite",
					Lane:        LaneFrontier,
					Required:    true,
				}}
				input.Ledger.Leases = []Lease{activeLease(now, "active-writer", []PathScope{{Path: "codexkit/agentorchestration", Kind: ScopeDirectory}})}
			},
			status:     AdmissionRejected,
			lane:       LaneFrontier,
			reasonCode: ReasonScopeCollision,
		},
		{
			name: "stale quota",
			mutate: func(input *Input) {
				input.Quota.ObservedAt = now.Add(-3 * time.Minute)
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonStaleQuota,
		},
		{
			name: "missing quota",
			mutate: func(input *Input) {
				input.Quota = nil
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonMissingQuota,
		},
		{
			name: "hard limit",
			mutate: func(input *Input) {
				input.Quota.Buckets[0].Remaining = 5
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonQuotaHardLimit,
		},
		{
			name: "soft limit backpressure",
			mutate: func(input *Input) {
				input.Quota.Buckets[0].Remaining = 20
			},
			status:     AdmissionBackpressure,
			lane:       LaneSpark,
			reasonCode: ReasonQuotaSoftLimit,
		},
		{
			name: "capacity reserve",
			mutate: func(input *Input) {
				input.Capacity.TotalSlots = 2
				input.Capacity.InUseSlots = 1
				input.Capacity.Lanes[0].TotalSlots = 2
				input.Capacity.Lanes[0].InUseSlots = 1
			},
			status:     AdmissionBackpressure,
			lane:       LaneSpark,
			reasonCode: ReasonNativeCapacityReserve,
		},
		{
			name: "model unavailable",
			mutate: func(input *Input) {
				input.Capacity.Lanes[0].Available = false
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonModelUnavailable,
		},
		{
			name: "ambiguous objective",
			mutate: func(input *Input) {
				input.Task.Objective = "research"
			},
			status:     AdmissionRejected,
			lane:       LaneSpark,
			reasonCode: ReasonAmbiguousObjective,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput(now)
			if test.mutate != nil {
				test.mutate(&input)
			}
			decision := policy.Decide(input)
			if decision.Admission.Status != test.status {
				t.Fatalf("status = %q, want %q; reasons: %+v", decision.Admission.Status, test.status, decision.Reasons)
			}
			if decision.SelectedLane != test.lane {
				t.Fatalf("lane = %q, want %q", decision.SelectedLane, test.lane)
			}
			if test.reasonCode != "" && !decisionHasReason(decision, test.reasonCode) {
				t.Fatalf("missing reason %q in %+v", test.reasonCode, decision.Reasons)
			}
			if test.status == AdmissionAdmitted && decision.Lease == nil {
				t.Fatal("admitted decision has no lease candidate")
			}
			if test.status != AdmissionAdmitted && decision.Lease != nil {
				t.Fatal("non-admitted decision has a lease candidate")
			}
		})
	}
}

func TestPolicyEnforcesIndependentQuotaBuckets(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	policy := mustPolicy(t, DefaultConfig())
	input := validInput(now)
	input.Quota.Buckets[1].Remaining = 0
	input.Quota.Buckets[1].ResetAt = now.Add(3 * time.Hour)

	decision := policy.Decide(input)
	if decision.Admission.Status != AdmissionRejected || !decisionHasReason(decision, ReasonQuotaHardLimit) {
		t.Fatalf("independent exhausted bucket was not enforced: %+v", decision)
	}
	var reset time.Time
	for _, reason := range decision.Reasons {
		if reason.BucketID == "spark_tokens" {
			reset = reason.RetryAt
		}
	}
	if !reset.Equal(now.Add(3 * time.Hour)) {
		t.Fatalf("bucket reset = %v, want independent reset", reset)
	}
}

func TestPolicyRejectsDuplicateActiveTaskIDBeforeRetry(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	policy := mustPolicy(t, DefaultConfig())
	input := validInput(now)
	input.Ledger.Leases = []Lease{activeLease(now, input.Task.ID, input.Task.Scopes.Write)}

	decision := policy.Decide(input)
	if decision.Admission.Status != AdmissionRejected || !decisionHasReason(decision, ReasonDuplicateActiveTask) {
		t.Fatalf("active task ID was not rejected for reconciliation: %+v", decision)
	}
	if decision.Lease != nil {
		t.Fatalf("duplicate active task received a second lease candidate: %+v", decision.Lease)
	}
}

func TestWriteRequiresFrontierVerifier(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	policy := mustPolicy(t, DefaultConfig())
	input := validInput(now)
	input.Task.Class = TaskImplementation
	input.Task.RequestedLane = LaneFrontier
	input.Task.Scopes.Write = []PathScope{{Path: "codexkit/new.go", Kind: ScopeFile}}
	input.Task.ExclusiveOwnership = true
	input.Task.VerificationRequirements = []VerificationRequirement{{
		ID:          "spark-self-check",
		Description: "Spark checks its own change",
		Lane:        LaneSpark,
		Required:    true,
	}}

	decision := policy.Decide(input)
	if !decisionHasReason(decision, ReasonMissingFrontierVerifier) {
		t.Fatalf("Spark-only verification unexpectedly accepted: %+v", decision)
	}
}

func TestConfigCannotRemoveNativeReserve(t *testing.T) {
	config := DefaultConfig()
	config.NativeSlotReserve = 0
	if _, err := NewPolicy(config); err == nil {
		t.Fatal("NewPolicy accepted a zero native slot reserve")
	}
}

func TestCompletionEvidence(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	task := validInput(now).Task
	task.Class = TaskImplementation
	task.RequestedLane = LaneFrontier
	task.Scopes.Write = []PathScope{{Path: "codexkit/new.go", Kind: ScopeFile}}
	task.ExclusiveOwnership = true
	task.VerificationRequirements = []VerificationRequirement{{
		ID:          "go-test",
		Description: "Run the Go tests",
		Lane:        LaneFrontier,
		Required:    true,
	}}
	evidence := CompletionEvidence{
		SchemaVersion: SchemaVersion,
		TaskID:        task.ID,
		Lane:          LaneFrontier,
		CompletedAt:   now.Add(time.Minute),
		Criteria: []CriterionEvidence{{
			Criterion: task.DoneWhen[0],
			Satisfied: true,
			Summary:   "A bounded summary is present.",
		}},
		Verifications: []VerificationEvidence{{
			RequirementID: "go-test",
			Lane:          LaneFrontier,
			Passed:        true,
			Summary:       "go test passed",
		}},
		Summary: "Task contract completed.",
	}
	if result := ValidateCompletion(task, LaneFrontier, evidence); !result.Complete {
		t.Fatalf("valid completion evidence rejected: %+v", result)
	}
	evidence.Verifications[0].Lane = LaneSpark
	if result := ValidateCompletion(task, LaneFrontier, evidence); result.Complete {
		t.Fatal("wrong-lane completion verification accepted")
	}
}

func TestMarshalDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	first := validInput(now).Task
	first.DoneWhen = []string{"Second criterion", "First criterion"}
	first.Scopes.Read = []PathScope{
		{Path: "research/z", Kind: ScopeFile},
		{Path: "research/a", Kind: ScopeFile},
	}
	second := first
	second.DoneWhen = []string{"First criterion", "Second criterion"}
	second.Scopes.Read = []PathScope{
		{Path: "research/a", Kind: ScopeFile},
		{Path: "research/z", Kind: ScopeFile},
	}

	firstJSON, err := MarshalDeterministic(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := MarshalDeterministic(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical JSON differs:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.DoneWhen[0] != "Second criterion" || first.Scopes.Read[0].Path != "research/z" {
		t.Fatal("deterministic marshal mutated caller-owned task slices")
	}
}

func validInput(now time.Time) Input {
	return Input{
		Now: now,
		Task: TaskPacket{
			SchemaVersion: SchemaVersion,
			ID:            "task-1",
			Objective:     "Summarize bounded orchestration policy sources",
			DoneWhen:      []string{"A source summary is recorded"},
			Class:         TaskResearch,
			RequestedLane: LaneAuto,
			Scopes: TaskScopes{
				Read: []PathScope{{Path: "research/openai", Kind: ScopeDirectory}},
			},
			RetryBudget: RetryBudget{
				MaxAttempts:    2,
				MaxElapsed:     10 * time.Minute,
				BaseBackoff:    time.Second,
				MaxBackoff:     10 * time.Second,
				JitterFraction: 0.2,
			},
			TimeoutBudget: TimeoutBudget{
				Attempt: 5 * time.Minute,
				Total:   10 * time.Minute,
			},
		},
		Quota: &QuotaSnapshot{
			SchemaVersion: SchemaVersion,
			ObservedAt:    now.Add(-10 * time.Second),
			Buckets: []QuotaBucket{
				{ID: "spark_requests", Lane: LaneSpark, Remaining: 80, Limit: 100, ResetAt: now.Add(time.Hour)},
				{ID: "spark_tokens", Lane: LaneSpark, Remaining: 70, Limit: 100, ResetAt: now.Add(2 * time.Hour)},
				{ID: "frontier_requests", Lane: LaneFrontier, Remaining: 80, Limit: 100, ResetAt: now.Add(time.Hour)},
			},
		},
		Capacity: &CapacitySnapshot{
			SchemaVersion: SchemaVersion,
			ObservedAt:    now.Add(-10 * time.Second),
			TotalSlots:    4,
			Lanes: []LaneCapacity{
				{Lane: LaneSpark, Available: true, TotalSlots: 3},
				{Lane: LaneFrontier, Available: true, TotalSlots: 2},
			},
		},
		Ledger: LeaseLedger{SchemaVersion: SchemaVersion},
	}
}

func activeLease(now time.Time, taskID string, writeScopes []PathScope) Lease {
	return Lease{
		ID:                 taskID + "-lease",
		TaskID:             taskID,
		Lane:               LaneFrontier,
		WriteScopes:        writeScopes,
		ExclusiveOwnership: true,
		AcquiredAt:         now.Add(-time.Minute),
		ExpiresAt:          now.Add(30 * time.Minute),
	}
}

func mustPolicy(t *testing.T, config Config) Policy {
	t.Helper()
	policy, err := NewPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func decisionHasReason(decision RoutingDecision, code string) bool {
	for _, reason := range decision.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
