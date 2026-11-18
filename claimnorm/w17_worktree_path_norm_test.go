package claimnorm

import (
	"testing"
)

func TestFourProviderParityMatrix(t *testing.T) {
	cases := []struct {
		input    string
		provider string
		lane     string
		expected string
	}{
		{
			input:    ".claude/worktrees/lane-claude/pkg/core.go",
			provider: ProviderClaude,
			lane:     "lane-claude",
			expected: "pkg/core.go",
		},
		{
			input:    "lane-worktrees/wt-cl1/pkg/core.go",
			provider: ProviderClaude,
			lane:     "cl1",
			expected: "pkg/core.go",
		},
		{
			input:    ".codex/worktrees/lane-codex/pkg/core.go",
			provider: ProviderCodex,
			lane:     "lane-codex",
			expected: "pkg/core.go",
		},
		{
			input:    ".local/state/agy-wave17-pods/wt-pod-08/pkg/core.go",
			provider: ProviderAGY,
			lane:     "wt-pod-08",
			expected: "pkg/core.go",
		},
		{
			input:    ".cline/worktrees/lane-cline/pkg/core.go",
			provider: ProviderCline,
			lane:     "lane-cline",
			expected: "pkg/core.go",
		},
		{
			input:    "/tmp/wt-generic-lane/pkg/core.go",
			provider: ProviderGeneric,
			lane:     "wt-generic-lane",
			expected: "pkg/core.go",
		},
		{
			input:    "wt-simple/pkg/core.go",
			provider: ProviderGeneric,
			lane:     "simple",
			expected: "pkg/core.go",
		},
	}

	for _, tc := range cases {
		res := InspectWorktreePath(tc.input)
		if !res.Stripped {
			t.Errorf("expected %s to be stripped", tc.input)
		}
		if res.Provider != tc.provider {
			t.Errorf("expected provider %s, got %s for %s", tc.provider, res.Provider, tc.input)
		}
		if res.Lane != tc.lane {
			t.Errorf("expected lane %s, got %s for %s", tc.lane, res.Lane, tc.input)
		}
		if res.Path != tc.expected {
			t.Errorf("expected path %s, got %s for %s", tc.expected, res.Path, tc.input)
		}
	}
}

func TestCrossProviderConflictParity(t *testing.T) {
	opts := Options{RepoHint: "codexkit"}
	claudePath := ".claude/worktrees/lane-a/claimnorm/w17_worktree_path_norm.go"
	codexPath := ".codex/worktrees/lane-b/codexkit/claimnorm/w17_worktree_path_norm.go"
	agyPath := ".local/state/agy-wave17-pods/wt-w17-pod-08/claimnorm/w17_worktree_path_norm.go"
	clinePath := ".cline/worktrees/lane-c/claimnorm/w17_worktree_path_norm.go"

	normClaude := NormalizeWithOptions(claudePath, opts)
	normCodex := NormalizeWithOptions(codexPath, opts)
	normAGY := NormalizeWithOptions(agyPath, opts)
	normCline := NormalizeWithOptions(clinePath, opts)

	target := "claimnorm/w17_worktree_path_norm.go"
	if normClaude.Canonical != target {
		t.Fatalf("claude canonical got %s", normClaude.Canonical)
	}
	if normCodex.Canonical != target {
		t.Fatalf("codex canonical got %s", normCodex.Canonical)
	}
	if normAGY.Canonical != target {
		t.Fatalf("agy canonical got %s", normAGY.Canonical)
	}
	if normCline.Canonical != target {
		t.Fatalf("cline canonical got %s", normCline.Canonical)
	}

	claim1 := Claim{
		ID:      "claim-1",
		Session: "session-1",
		Agent:   "agent-claude",
		Repo:    "codexkit",
		Paths:   []string{claudePath},
	}
	claim2 := Claim{
		ID:      "claim-2",
		Session: "session-2",
		Agent:   "agent-codex",
		Repo:    "codexkit",
		Paths:   []string{codexPath},
	}
	claim3 := Claim{
		ID:      "claim-3",
		Session: "session-3",
		Agent:   "agent-agy",
		Repo:    "codexkit",
		Paths:   []string{agyPath},
	}
	claim4 := Claim{
		ID:      "claim-4",
		Session: "session-4",
		Agent:   "agent-cline",
		Repo:    "codexkit",
		Paths:   []string{clinePath},
	}

	conflicts := CheckConflicts(claim1, []Claim{claim2, claim3, claim4}, opts)
	if len(conflicts) != 3 {
		t.Fatalf("expected 3 cross-provider conflicts, got %d", len(conflicts))
	}
}

func TestCodexEmbeddedRepoTail(t *testing.T) {
	opts := Options{RepoHint: "codexkit", KnownRepos: []string{"codexkit"}}
	raw := ".codex/worktrees/lane-feature/codexkit/claimnorm/w17_worktree_path_norm.go"
	norm := NormalizeWithOptions(raw, opts)

	if norm.Canonical != "claimnorm/w17_worktree_path_norm.go" {
		t.Fatalf("expected canonical claimnorm/w17_worktree_path_norm.go, got %s", norm.Canonical)
	}
	if norm.Repo != "codexkit" {
		t.Fatalf("expected repo codexkit, got %s", norm.Repo)
	}
	if norm.Provider != ProviderCodex {
		t.Fatalf("expected provider codex, got %s", norm.Provider)
	}
	if norm.WorktreeLane != "lane-feature" {
		t.Fatalf("expected lane lane-feature, got %s", norm.WorktreeLane)
	}
	if !norm.WorktreeStripped {
		t.Fatalf("expected worktree stripped to be true")
	}
}

func TestNestedWorktreeStripping(t *testing.T) {
	raw := ".claude/worktrees/lane-outer/.codex/worktrees/lane-inner/sub/file.go"
	res := InspectWorktreePath(raw)

	if !res.Stripped {
		t.Fatalf("expected nested worktree to be stripped")
	}
	if res.NestedDepth != 2 {
		t.Fatalf("expected nested depth 2, got %d", res.NestedDepth)
	}
	if res.Lane != "lane-inner" {
		t.Fatalf("expected innermost lane lane-inner, got %s", res.Lane)
	}
	if res.Path != "sub/file.go" {
		t.Fatalf("expected path sub/file.go, got %s", res.Path)
	}
}

func TestWorkspaceRootAndHostPrefixes(t *testing.T) {
	opts := Options{
		WorkspaceRoot: "/srv/workspace",
		RepoHint:      "codexkit",
	}
	raw := "AG1:/srv/workspace/.claude/worktrees/lane-1/codexkit/pkg/foo.go"
	norm := NormalizeWithOptions(raw, opts)

	if norm.Host != "AG1" {
		t.Fatalf("expected host AG1, got %s", norm.Host)
	}
	if norm.Canonical != "pkg/foo.go" {
		t.Fatalf("expected canonical pkg/foo.go, got %s", norm.Canonical)
	}
	if norm.Physical {
		t.Fatalf("expected non-physical for worktree inside workspace")
	}
	if !norm.WorktreeStripped {
		t.Fatalf("expected worktree stripped")
	}
}

func TestWorkspaceRootNonCorruptedManifest(t *testing.T) {
	opts := Options{
		WorkspaceRoot: "/srv/workspace",
	}
	raw := "workspace/manifest.json"
	norm := NormalizeWithOptions(raw, opts)
	if norm.Canonical != "workspace/manifest.json" {
		t.Fatalf("expected workspace/manifest.json not corrupted, got %s", norm.Canonical)
	}
}

func TestKnownReposPreWorktreeDetection(t *testing.T) {
	opts := Options{
		KnownRepos: []string{"codexkit"},
	}
	raw := "codexkit/.claude/worktrees/lane-1/pkg/a.go"
	norm := NormalizeWithOptions(raw, opts)
	if norm.Repo != "codexkit" {
		t.Fatalf("expected repo codexkit, got %s", norm.Repo)
	}
	if norm.Canonical != "pkg/a.go" {
		t.Fatalf("expected canonical pkg/a.go, got %s", norm.Canonical)
	}
	if norm.Provider != ProviderClaude {
		t.Fatalf("expected provider claude, got %s", norm.Provider)
	}
}

func TestCanonicalAddressParity(t *testing.T) {
	if CanonicalAddress("ag1") != "archglasses" {
		t.Fatalf("expected ag1 -> archglasses, got %s", CanonicalAddress("ag1"))
	}
	if CanonicalAddress("archglasses") != "archglasses" {
		t.Fatalf("expected archglasses -> archglasses, got %s", CanonicalAddress("archglasses"))
	}
	if CanonicalAddress("archglasses-1") != "archglasses" {
		t.Fatalf("expected archglasses-1 -> archglasses, got %s", CanonicalAddress("archglasses-1"))
	}
	if CanonicalAddress("claude:ag1-abc") != "claude:archglasses-abc" {
		t.Fatalf("expected claude:ag1-abc -> claude:archglasses-abc, got %s", CanonicalAddress("claude:ag1-abc"))
	}
	if CanonicalAddress("claude:archglasses-abc") != "claude:archglasses-abc" {
		t.Fatalf("expected claude:archglasses-abc -> claude:archglasses-abc, got %s", CanonicalAddress("claude:archglasses-abc"))
	}
	if CanonicalAddress("claude:archglasses-1-abc") != "claude:archglasses-abc" {
		t.Fatalf("expected claude:archglasses-1-abc -> claude:archglasses-abc, got %s", CanonicalAddress("claude:archglasses-1-abc"))
	}
	if CanonicalAddress("ag2") != "archglasses-2" {
		t.Fatalf("expected ag2 -> archglasses-2, got %s", CanonicalAddress("ag2"))
	}
	if CanonicalAddress("claude:ag4-session") != "claude:archglasses-4-session" {
		t.Fatalf("expected claude:ag4-session -> claude:archglasses-4-session, got %s", CanonicalAddress("claude:ag4-session"))
	}
	if CanonicalAddress("claude:archglasses-4-session") != "claude:archglasses-4-session" {
		t.Fatalf("expected claude:archglasses-4-session -> claude:archglasses-4-session, got %s", CanonicalAddress("claude:archglasses-4-session"))
	}
}

func TestPhysicalLocations(t *testing.T) {
	opts := Options{RepoHint: "codexkit"}

	phys1 := "AG1:/opt/bin/tool"
	if !IsPhysicalLocation(phys1, opts) {
		t.Fatalf("expected %s to be physical", phys1)
	}
	norm1 := NormalizeWithOptions(phys1, opts)
	if !norm1.Physical {
		t.Fatalf("expected normalized physical to be true")
	}
	if norm1.Host != "AG1" {
		t.Fatalf("expected host AG1, got %s", norm1.Host)
	}

	phys2 := "~/.local/bin/hg-tier1"
	if !IsPhysicalLocation(phys2, opts) {
		t.Fatalf("expected %s to be physical", phys2)
	}
	norm2 := NormalizeWithOptions(phys2, opts)
	if !norm2.Physical {
		t.Fatalf("expected normalized physical to be true")
	}

	claimA := Claim{
		ID:      "claim-phys-1",
		Session: "session-a",
		Agent:   "agent-a",
		Paths:   []string{"AG1:/opt/bin/tool"},
	}
	claimB := Claim{
		ID:      "claim-phys-2",
		Session: "session-b",
		Agent:   "agent-b",
		Paths:   []string{"archglasses:/opt/bin/tool"},
	}
	claimDiffHost := Claim{
		ID:      "claim-phys-3",
		Session: "session-c",
		Agent:   "agent-c",
		Paths:   []string{"AG4:/opt/bin/tool"},
	}
	claimRoot := Claim{
		ID:      "claim-phys-root",
		Session: "session-r",
		Agent:   "agent-r",
		Paths:   []string{"AG1:/"},
	}

	conflictsSame := CheckConflicts(claimA, []Claim{claimB}, opts)
	if len(conflictsSame) != 1 {
		t.Fatalf("expected conflict between AG1 and archglasses physical paths")
	}

	conflictsRoot := CheckConflicts(claimRoot, []Claim{claimB}, opts)
	if len(conflictsRoot) != 1 {
		t.Fatalf("expected root / physical conflict to overlap /opt/bin/tool")
	}

	conflictsDiff := CheckConflicts(claimA, []Claim{claimDiffHost}, opts)
	if len(conflictsDiff) != 0 {
		t.Fatalf("expected no conflict between AG1 and AG4 physical paths")
	}
}

func TestRepoLabelScoping(t *testing.T) {
	opts := Options{}

	claim1 := Claim{
		ID:      "claim-1",
		Session: "session-1",
		Agent:   "agent-1",
		Repo:    "codexkit",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}
	claim2DiffRepo := Claim{
		ID:      "claim-2",
		Session: "session-2",
		Agent:   "agent-2",
		Repo:    "other-repo",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}
	claim3SameRepoOrg := Claim{
		ID:      "claim-3",
		Session: "session-3",
		Agent:   "agent-3",
		Repo:    "example-org/codexkit",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}
	claim4Unlabeled := Claim{
		ID:      "claim-4",
		Session: "session-4",
		Agent:   "agent-4",
		Repo:    "",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}
	claim5DiffOrg := Claim{
		ID:      "claim-5",
		Session: "session-5",
		Agent:   "agent-5",
		Repo:    "other-org/codexkit",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}

	conflictsDiff := CheckConflicts(claim1, []Claim{claim2DiffRepo}, opts)
	if len(conflictsDiff) != 0 {
		t.Fatalf("expected repo label mismatch to suppress conflict")
	}

	conflictsOrg := CheckConflicts(claim1, []Claim{claim3SameRepoOrg}, opts)
	if len(conflictsOrg) != 1 {
		t.Fatalf("expected conflict between codexkit and example-org/codexkit")
	}

	conflictsUnlabeled := CheckConflicts(claim1, []Claim{claim4Unlabeled}, opts)
	if len(conflictsUnlabeled) != 1 {
		t.Fatalf("expected unlabeled claim to not be suppressed")
	}

	conflictsDiffOrgs := CheckConflicts(claim3SameRepoOrg, []Claim{claim5DiffOrg}, opts)
	if len(conflictsDiffOrgs) != 0 {
		t.Fatalf("expected example-org/codexkit vs other-org/codexkit to be suppressed, got %d conflicts", len(conflictsDiffOrgs))
	}

	optsWithHint := Options{RepoHint: "codexkit"}
	conflictsHintDiff := CheckConflicts(claim1, []Claim{claim2DiffRepo}, optsWithHint)
	if len(conflictsHintDiff) != 0 {
		t.Fatalf("expected repo label mismatch to suppress conflict even with Options.RepoHint set")
	}

	if RepoLabelsMatch("org1/codexkit", "org2/codexkit") {
		t.Fatalf("org1/codexkit must not match org2/codexkit")
	}
	if RepoLabelsMatch("org-a/codexkit", "org-b/codexkit") {
		t.Fatalf("org-a/codexkit must not match org-b/codexkit")
	}
	if !RepoLabelsMatch("codexkit", "example-org/codexkit") {
		t.Fatalf("codexkit must match example-org/codexkit")
	}
	if !RepoLabelsMatch("example-org/codexkit", "codexkit") {
		t.Fatalf("example-org/codexkit must match codexkit")
	}
}

func TestHostSelfExclusion(t *testing.T) {
	opts := Options{RepoHint: "codexkit"}

	claimA := Claim{
		ID:      "claim-1",
		Session: "ag4-session-1",
		Agent:   "ag4",
		Repo:    "codexkit",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}
	claimB := Claim{
		ID:      "claim-1-variant",
		Session: "archglasses-4-session-1",
		Agent:   "archglasses-4",
		Repo:    "codexkit",
		Paths:   []string{"claimnorm/w17_worktree_path_norm.go"},
	}

	conflicts := CheckConflicts(claimA, []Claim{claimB}, opts)
	if len(conflicts) != 0 {
		t.Fatalf("expected ag4 vs archglasses-4 self-exclusion to produce 0 conflicts, got %d", len(conflicts))
	}
}

func TestNegativeControls(t *testing.T) {
	if PathsOverlap("claimnorm/a", "claimnorm/ab") {
		t.Fatalf("claimnorm/a must not overlap claimnorm/ab")
	}
	if !PathsOverlap("claimnorm/a", "claimnorm/a/b.go") {
		t.Fatalf("claimnorm/a must overlap claimnorm/a/b.go")
	}
	if !PathsOverlap("claimnorm/a", "claimnorm/a") {
		t.Fatalf("claimnorm/a must overlap identical path")
	}
	if PathsOverlap("", "claimnorm/a") || PathsOverlap("claimnorm/a", "") {
		t.Fatalf("empty path must not overlap")
	}

	nonWorktrees := []string{
		".claude/skills/x/SKILL.md",
		".agents/rules/x.md",
		".codex/config.toml",
		".cline/rules.md",
		"docs/wt-guide/a.md",
		"pkg/worktrees.go",
		".claude/worktrees/lane1",
		"wt-lane",
	}
	for _, p := range nonWorktrees {
		res := InspectWorktreePath(p)
		if res.Stripped {
			t.Errorf("expected %s NOT to be stripped", p)
		}
	}

	opts := Options{RepoHint: "codexkit"}
	trav1 := NormalizeWithOptions("../../sub/file.go", opts)
	if trav1.Canonical != "sub/file.go" {
		t.Fatalf("expected traversal clamped to sub/file.go, got %s", trav1.Canonical)
	}

	emptyNorm := NormalizeWithOptions("", opts)
	if emptyNorm.Canonical != "" {
		t.Fatalf("expected empty canonical for empty raw")
	}

	dotNorm := NormalizeWithOptions(".", opts)
	if dotNorm.Canonical != "" {
		t.Fatalf("expected empty canonical for dot raw")
	}
}
