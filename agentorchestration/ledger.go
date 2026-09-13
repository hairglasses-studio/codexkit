package agentorchestration

import (
	"fmt"
	"sort"
	"time"
)

// LeaseConflictKind identifies which lease operations intersect.
type LeaseConflictKind string

const (
	ConflictWriteWrite LeaseConflictKind = "write_write"
	ConflictWriteRead  LeaseConflictKind = "write_read"
	ConflictReadWrite  LeaseConflictKind = "read_write"
)

// LeaseConflict contains stable collision evidence against one active lease.
type LeaseConflict struct {
	LeaseID    string            `json:"lease_id"`
	TaskID     string            `json:"task_id"`
	Kind       LeaseConflictKind `json:"kind"`
	Collisions []ScopeCollision  `json:"collisions"`
}

// Active returns unexpired leases in stable order. A lease expires exactly at
// ExpiresAt. Invalid leases are excluded; Validate reports them explicitly.
func (l LeaseLedger) Active(now time.Time) []Lease {
	var active []Lease
	for _, lease := range l.Leases {
		if lease.ID == "" || lease.TaskID == "" || lease.ExpiresAt.IsZero() {
			continue
		}
		if now.Before(lease.ExpiresAt) {
			active = append(active, canonicalLease(lease))
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].TaskID == active[j].TaskID {
			return active[i].ID < active[j].ID
		}
		return active[i].TaskID < active[j].TaskID
	})
	return active
}

// Validate checks ledger schema, identities, time bounds, and path scopes.
func (l LeaseLedger) Validate() error {
	if l.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported lease ledger schema version %d", l.SchemaVersion)
	}
	ids := make(map[string]struct{}, len(l.Leases))
	for i, lease := range l.Leases {
		if lease.ID == "" || lease.TaskID == "" {
			return fmt.Errorf("lease %d has empty identity", i)
		}
		if _, exists := ids[lease.ID]; exists {
			return fmt.Errorf("duplicate lease id %q", lease.ID)
		}
		ids[lease.ID] = struct{}{}
		if lease.AcquiredAt.IsZero() || !lease.ExpiresAt.After(lease.AcquiredAt) {
			return fmt.Errorf("lease %q has invalid time bounds", lease.ID)
		}
		if _, err := canonicalScopes(lease.ReadScopes); err != nil {
			return fmt.Errorf("lease %q read scopes: %w", lease.ID, err)
		}
		if _, err := canonicalScopes(lease.WriteScopes); err != nil {
			return fmt.Errorf("lease %q write scopes: %w", lease.ID, err)
		}
	}
	return nil
}

// Conflicts returns all active read/write ownership conflicts for a task. A
// write request is exclusive against active readers and writers; an incoming
// reader waits for active writers. Read/read overlap is allowed.
func (l LeaseLedger) Conflicts(task TaskPacket, now time.Time) ([]LeaseConflict, error) {
	var conflicts []LeaseConflict
	for _, lease := range l.Active(now) {
		if lease.TaskID == task.ID {
			continue
		}
		checks := []struct {
			kind        LeaseConflictKind
			left, right []PathScope
		}{
			{kind: ConflictWriteWrite, left: task.Scopes.Write, right: lease.WriteScopes},
			{kind: ConflictWriteRead, left: task.Scopes.Write, right: lease.ReadScopes},
			{kind: ConflictReadWrite, left: task.Scopes.Read, right: lease.WriteScopes},
		}
		for _, check := range checks {
			collisions, err := DetectScopeCollisions(check.left, check.right)
			if err != nil {
				return nil, err
			}
			if len(collisions) != 0 {
				conflicts = append(conflicts, LeaseConflict{
					LeaseID:    lease.ID,
					TaskID:     lease.TaskID,
					Kind:       check.kind,
					Collisions: collisions,
				})
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].TaskID == conflicts[j].TaskID {
			if conflicts[i].LeaseID == conflicts[j].LeaseID {
				return conflicts[i].Kind < conflicts[j].Kind
			}
			return conflicts[i].LeaseID < conflicts[j].LeaseID
		}
		return conflicts[i].TaskID < conflicts[j].TaskID
	})
	return conflicts, nil
}

// WithLease returns a new ledger containing lease. Existing state is not
// mutated and duplicate lease IDs are rejected.
func (l LeaseLedger) WithLease(lease Lease) (LeaseLedger, error) {
	if err := l.Validate(); err != nil {
		return LeaseLedger{}, err
	}
	for _, existing := range l.Leases {
		if existing.ID == lease.ID {
			return LeaseLedger{}, fmt.Errorf("duplicate lease id %q", lease.ID)
		}
	}
	next := LeaseLedger{SchemaVersion: SchemaVersion, Leases: append([]Lease(nil), l.Leases...)}
	next.Leases = append(next.Leases, lease)
	if err := next.Validate(); err != nil {
		return LeaseLedger{}, err
	}
	next.Leases = next.Active(time.Time{})
	return next, nil
}

// Prune returns a copy without leases expired at now.
func (l LeaseLedger) Prune(now time.Time) LeaseLedger {
	return LeaseLedger{SchemaVersion: SchemaVersion, Leases: l.Active(now)}
}

func canonicalLease(lease Lease) Lease {
	lease.ReadScopes, _ = canonicalScopes(lease.ReadScopes)
	lease.WriteScopes, _ = canonicalScopes(lease.WriteScopes)
	return lease
}
