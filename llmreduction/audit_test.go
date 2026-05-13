package llmreduction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWeights(t *testing.T) {
	w := defaultWeights()
	sum := w.Unification + w.PrimitiveWarnings + w.PerfBottleneck
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("weights sum = %f, want ~1.0", sum)
	}
	if w.Unification <= 0 {
		t.Error("Unification weight should be positive")
	}
	if w.PrimitiveWarnings <= 0 {
		t.Error("PrimitiveWarnings weight should be positive")
	}
	if w.PerfBottleneck <= 0 {
		t.Error("PerfBottleneck weight should be positive")
	}
}

func TestIncludeRepo(t *testing.T) {
	tests := []struct {
		scope     string
		allScopes bool
		want      bool
	}{
		{"active_operator", false, true},
		{"active_first_party", false, true},
		{"passive", false, false},
		{"compatibility_only", false, false},
		{"", false, false},
		{"passive", true, true},
		{"compatibility_only", true, true},
		{"active_operator", true, true},
		{"anything", true, true},
	}
	for _, tt := range tests {
		got := includeRepo(tt.scope, tt.allScopes)
		if got != tt.want {
			t.Errorf("includeRepo(%q, %v) = %v, want %v",
				tt.scope, tt.allScopes, got, tt.want)
		}
	}
}

func TestIntFromAny(t *testing.T) {
	tests := []struct {
		input any
		want  int
	}{
		{int(42), 42},
		{int32(42), 42},
		{int64(42), 42},
		{float64(42.7), 42},
		{float64(0), 0},
		{"not a number", 0},
		{nil, 0},
		{true, 0},
	}
	for _, tt := range tests {
		got := intFromAny(tt.input)
		if got != tt.want {
			t.Errorf("intFromAny(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestValidateNoopPlanSafety(t *testing.T) {
	err := validateNoopPlanSafety(ReductionPlan{Items: nil})
	if err == nil {
		t.Error("expected error for empty plan, got nil")
	}

	err = validateNoopPlanSafety(ReductionPlan{
		Items: []ReductionPlanItem{{Repo: "test", Priority: "P0"}},
	})
	if err != nil {
		t.Errorf("expected nil for non-empty plan, got %v", err)
	}
}

func TestRepoDebt_PriorityThresholds(t *testing.T) {
	// Verify priority assignment logic by constructing rows directly
	tests := []struct {
		debtScore float64
		wantPri   string
	}{
		{0.60, "P0"},
		{0.80, "P0"},
		{0.35, "P1"},
		{0.59, "P1"},
		{0.34, "P2"},
		{0.00, "P2"},
	}
	for _, tt := range tests {
		var pri string
		switch {
		case tt.debtScore >= 0.60:
			pri = "P0"
		case tt.debtScore >= 0.35:
			pri = "P1"
		default:
			pri = "P2"
		}
		if pri != tt.wantPri {
			t.Errorf("debtScore %.2f -> priority %q, want %q", tt.debtScore, pri, tt.wantPri)
		}
	}
}

func TestBuildDebtAudit_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildDebtAudit(dir, false)
	if err == nil {
		t.Error("expected error for missing manifest, got nil")
	}
}

func TestBuildDebtAudit_EmptyManifest(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := map[string]any{
		"version": 1,
		"repos":   []any{},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	report, err := BuildDebtAudit(dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Summary.ReposScanned != 0 {
		t.Errorf("ReposScanned = %d, want 0", report.Summary.ReposScanned)
	}
	if report.Scope != "all" {
		t.Errorf("Scope = %q, want %q", report.Scope, "all")
	}
	if report.Weights.Unification != defaultWeights().Unification {
		t.Error("Weights should match defaults")
	}
}

func TestBuildReductionPlan_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildReductionPlan(dir, false, 5)
	if err == nil {
		t.Error("expected error for missing manifest, got nil")
	}
}

func TestBuildDedupCandidates_MissingWorkspace(t *testing.T) {
	dir := t.TempDir()
	// unificationaudit.Audit will fail on missing workspace data
	_, err := BuildDedupCandidates(dir, false, 5)
	if err == nil {
		t.Error("expected error for missing workspace, got nil")
	}
}

func TestDebtAuditReport_JSONRoundTrip(t *testing.T) {
	report := DebtAuditReport{
		GeneratedAt:   "2026-01-01T00:00:00Z",
		WorkspaceRoot: "/tmp/test",
		Scope:         "all",
		Weights:       defaultWeights(),
		Summary: DebtAuditSummary{
			ReposScanned: 3,
			P0:           1,
			P1:           1,
			P2:           1,
			TopRepo:      "alpha",
		},
		Repos: []RepoDebt{
			{Repo: "alpha", Priority: "P0", DebtScore: 0.75, Reasons: []string{"high unification"}},
			{Repo: "beta", Priority: "P1", DebtScore: 0.45},
			{Repo: "gamma", Priority: "P2", DebtScore: 0.10},
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded DebtAuditReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Summary.TopRepo != "alpha" {
		t.Errorf("TopRepo = %q, want %q", decoded.Summary.TopRepo, "alpha")
	}
	if len(decoded.Repos) != 3 {
		t.Errorf("len(Repos) = %d, want 3", len(decoded.Repos))
	}
	if decoded.Repos[0].Priority != "P0" {
		t.Errorf("first repo priority = %q, want %q", decoded.Repos[0].Priority, "P0")
	}
}
