package agentorchestration

import (
	"encoding/json"
	"sort"
)

// MarshalDeterministic canonicalizes set-like slices in known contracts before
// using encoding/json's stable map-key ordering. It emits compact JSON without
// consulting clocks, files, environment variables, or external state.
func MarshalDeterministic(value any) ([]byte, error) {
	switch typed := value.(type) {
	case TaskPacket:
		value = canonicalTask(typed)
	case *TaskPacket:
		if typed != nil {
			canonical := canonicalTask(*typed)
			value = &canonical
		}
	case QuotaSnapshot:
		value = canonicalQuota(typed)
	case *QuotaSnapshot:
		if typed != nil {
			canonical := canonicalQuota(*typed)
			value = &canonical
		}
	case CapacitySnapshot:
		value = canonicalCapacity(typed)
	case *CapacitySnapshot:
		if typed != nil {
			canonical := canonicalCapacity(*typed)
			value = &canonical
		}
	case LeaseLedger:
		value = canonicalLedger(typed)
	case *LeaseLedger:
		if typed != nil {
			canonical := canonicalLedger(*typed)
			value = &canonical
		}
	case RoutingDecision:
		value = canonicalDecision(typed)
	case *RoutingDecision:
		if typed != nil {
			canonical := canonicalDecision(*typed)
			value = &canonical
		}
	case CompletionEvidence:
		value = canonicalCompletion(typed)
	case *CompletionEvidence:
		if typed != nil {
			canonical := canonicalCompletion(*typed)
			value = &canonical
		}
	}
	return json.Marshal(value)
}

func canonicalQuota(snapshot QuotaSnapshot) QuotaSnapshot {
	snapshot.Buckets = append([]QuotaBucket(nil), snapshot.Buckets...)
	sort.Slice(snapshot.Buckets, func(i, j int) bool {
		if snapshot.Buckets[i].Lane == snapshot.Buckets[j].Lane {
			return snapshot.Buckets[i].ID < snapshot.Buckets[j].ID
		}
		return snapshot.Buckets[i].Lane < snapshot.Buckets[j].Lane
	})
	return snapshot
}

func canonicalCapacity(snapshot CapacitySnapshot) CapacitySnapshot {
	snapshot.Lanes = append([]LaneCapacity(nil), snapshot.Lanes...)
	sort.Slice(snapshot.Lanes, func(i, j int) bool { return snapshot.Lanes[i].Lane < snapshot.Lanes[j].Lane })
	return snapshot
}

func canonicalLedger(ledger LeaseLedger) LeaseLedger {
	ledger.Leases = append([]Lease(nil), ledger.Leases...)
	for i := range ledger.Leases {
		ledger.Leases[i] = canonicalLease(ledger.Leases[i])
	}
	sort.Slice(ledger.Leases, func(i, j int) bool {
		if ledger.Leases[i].TaskID == ledger.Leases[j].TaskID {
			return ledger.Leases[i].ID < ledger.Leases[j].ID
		}
		return ledger.Leases[i].TaskID < ledger.Leases[j].TaskID
	})
	return ledger
}

func canonicalDecision(decision RoutingDecision) RoutingDecision {
	decision.Reasons = append([]DecisionReason(nil), decision.Reasons...)
	sortReasons(decision.Reasons)
	decision.Admission.Backpressure.Reasons = append([]DecisionReason(nil), decision.Admission.Backpressure.Reasons...)
	sortReasons(decision.Admission.Backpressure.Reasons)
	if decision.Lease != nil {
		lease := canonicalLease(*decision.Lease)
		decision.Lease = &lease
	}
	return decision
}

func canonicalCompletion(evidence CompletionEvidence) CompletionEvidence {
	evidence.Criteria = append([]CriterionEvidence(nil), evidence.Criteria...)
	sort.Slice(evidence.Criteria, func(i, j int) bool { return evidence.Criteria[i].Criterion < evidence.Criteria[j].Criterion })
	evidence.Verifications = append([]VerificationEvidence(nil), evidence.Verifications...)
	sort.Slice(evidence.Verifications, func(i, j int) bool {
		return evidence.Verifications[i].RequirementID < evidence.Verifications[j].RequirementID
	})
	return evidence
}
