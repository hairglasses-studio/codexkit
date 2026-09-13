package agentorchestration

import (
	"testing"
	"time"
)

func TestScopesCollideTable(t *testing.T) {
	tests := []struct {
		name        string
		left, right PathScope
		want        bool
	}{
		{
			name:  "directory contains file",
			left:  PathScope{Path: "codexkit/agentorchestration", Kind: ScopeDirectory},
			right: PathScope{Path: "codexkit/agentorchestration/policy.go", Kind: ScopeFile},
			want:  true,
		},
		{
			name:  "nested directories",
			left:  PathScope{Path: "codexkit", Kind: ScopeDirectory},
			right: PathScope{Path: "codexkit/agentorchestration", Kind: ScopeDirectory},
			want:  true,
		},
		{
			name:  "same file",
			left:  PathScope{Path: "codexkit/a.go", Kind: ScopeFile},
			right: PathScope{Path: "codexkit/a.go", Kind: ScopeFile},
			want:  true,
		},
		{
			name:  "file prefix is not ancestor",
			left:  PathScope{Path: "codexkit/a", Kind: ScopeFile},
			right: PathScope{Path: "codexkit/ab", Kind: ScopeFile},
			want:  false,
		},
		{
			name:  "sibling directories",
			left:  PathScope{Path: "codexkit/a", Kind: ScopeDirectory},
			right: PathScope{Path: "codexkit/b", Kind: ScopeDirectory},
			want:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ScopesCollide(test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ScopesCollide() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNormalizePathScopeRejectsBroadAndTraversal(t *testing.T) {
	for _, path := range []string{".", "/", "../codexkit", "codexkit/../research"} {
		t.Run(path, func(t *testing.T) {
			if _, err := NormalizePathScope(PathScope{Path: path, Kind: ScopeDirectory}); err == nil {
				t.Fatalf("unsafe path %q accepted", path)
			}
		})
	}
}

func TestLeaseLedgerConflictAndPrune(t *testing.T) {
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	ledger := LeaseLedger{
		SchemaVersion: SchemaVersion,
		Leases: []Lease{
			activeLease(now, "active", []PathScope{{Path: "codexkit", Kind: ScopeDirectory}}),
			{
				ID:         "expired-lease",
				TaskID:     "expired",
				Lane:       LaneFrontier,
				AcquiredAt: now.Add(-2 * time.Hour),
				ExpiresAt:  now.Add(-time.Hour),
			},
		},
	}
	task := validInput(now).Task
	task.Scopes.Read = nil
	task.Scopes.Write = []PathScope{{Path: "codexkit/agentorchestration/policy.go", Kind: ScopeFile}}

	conflicts, err := ledger.Conflicts(task, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].TaskID != "active" || conflicts[0].Kind != ConflictWriteWrite {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	if active := ledger.Prune(now).Leases; len(active) != 1 || active[0].TaskID != "active" {
		t.Fatalf("unexpected pruned ledger: %+v", active)
	}
}

func TestBackoffDelayWithInjectedJitter(t *testing.T) {
	budget := RetryBudget{
		MaxAttempts:    5,
		BaseBackoff:    time.Second,
		MaxBackoff:     10 * time.Second,
		JitterFraction: 0.2,
	}
	jitter := func(string, int) float64 { return 0.5 }
	wants := []time.Duration{1100 * time.Millisecond, 2200 * time.Millisecond, 4400 * time.Millisecond}
	for i, want := range wants {
		got, err := BackoffDelay("task", i+1, budget, jitter)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("attempt %d delay = %v, want %v", i+1, got, want)
		}
	}

	stable := DeterministicJitter("test-seed")
	first, err := BackoffDelay("task", 2, budget, stable)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BackoffDelay("task", 2, budget, stable)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("deterministic jitter changed: %v != %v", first, second)
	}
}
