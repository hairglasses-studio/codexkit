package llmreduction

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/perfaudit"
	"github.com/hairglasses-studio/codexkit/primitiveindex"
	"github.com/hairglasses-studio/codexkit/unificationaudit"
	"github.com/hairglasses-studio/codexkit/workspace"
)

// Weights controls the relative contribution of each signal.
type Weights struct {
	Unification       float64 `json:"unification"`
	PrimitiveWarnings float64 `json:"primitive_warnings"`
	PerfBottleneck    float64 `json:"perf_bottleneck"`
}

// RepoDebt is one repo-level ranking row.
type RepoDebt struct {
	Repo                  string   `json:"repo"`
	Scope                 string   `json:"scope,omitempty"`
	Language              string   `json:"language,omitempty"`
	UnificationScore      int      `json:"unification_score"`
	PrimitiveWarningCount int      `json:"primitive_warning_count"`
	PerfScore             int      `json:"perf_score"`
	UnificationNorm       float64  `json:"unification_norm"`
	PrimitiveWarningsNorm float64  `json:"primitive_warnings_norm"`
	PerfNorm              float64  `json:"perf_norm"`
	DebtScore             float64  `json:"debt_score"`
	Priority              string   `json:"priority"`
	SuggestedAction       string   `json:"suggested_action"`
	Reasons               []string `json:"reasons,omitempty"`
}

// DebtAuditSummary is the aggregate ranking summary.
type DebtAuditSummary struct {
	ReposScanned int    `json:"repos_scanned"`
	P0           int    `json:"p0"`
	P1           int    `json:"p1"`
	P2           int    `json:"p2"`
	TopRepo      string `json:"top_repo,omitempty"`
}

// DebtAuditReport is the top-level debt audit output.
type DebtAuditReport struct {
	GeneratedAt   string           `json:"generated_at"`
	WorkspaceRoot string           `json:"workspace_root"`
	Scope         string           `json:"scope"`
	Weights       Weights          `json:"weights"`
	Summary       DebtAuditSummary `json:"summary"`
	Repos         []RepoDebt       `json:"repos"`
}

// DedupCandidate is one likely reduction target.
type DedupCandidate struct {
	Repo      string `json:"repo"`
	Path      string `json:"path,omitempty"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	Rationale string `json:"rationale"`
}

// ReductionPlanItem is one planned tranche action.
type ReductionPlanItem struct {
	Repo      string   `json:"repo"`
	Priority  string   `json:"priority"`
	Action    string   `json:"action"`
	Rationale []string `json:"rationale,omitempty"`
}

// ReductionPlan is a staged execution plan from debt data.
type ReductionPlan struct {
	GeneratedAt string              `json:"generated_at"`
	Scope       string              `json:"scope"`
	Items       []ReductionPlanItem `json:"items"`
}

func defaultWeights() Weights {
	return Weights{
		Unification:       0.55,
		PrimitiveWarnings: 0.25,
		PerfBottleneck:    0.20,
	}
}

func includeRepo(scope string, allScopes bool) bool {
	if allScopes {
		return true
	}
	return scope == "active_operator" || scope == "active_first_party"
}

// BuildDebtAudit builds a weighted LLM traversability-debt ranking.
func BuildDebtAudit(root string, allScopes bool) (DebtAuditReport, error) {
	if root == "" {
		root = workspace.DefaultRoot()
	}
	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return DebtAuditReport{}, fmt.Errorf("load manifest: %w", err)
	}

	unif, err := unificationaudit.Audit(root, unificationaudit.Options{
		WorkspaceRoot: root,
		AllScopes:     allScopes,
	})
	if err != nil {
		return DebtAuditReport{}, fmt.Errorf("run unification audit: %w", err)
	}
	perf := perfaudit.Audit(root, perfaudit.Options{AllScopes: allScopes})
	prims, err := primitiveindex.Build(primitiveindex.Options{WorkspaceRoot: root})
	if err != nil {
		return DebtAuditReport{}, fmt.Errorf("build primitive index: %w", err)
	}

	unifByRepo := map[string]int{}
	perfByRepo := map[string]int{}
	warnByRepo := map[string]int{}

	maxUnif := 1
	maxPerf := 1
	maxWarn := 1

	for _, repo := range unif.Repos {
		unifByRepo[repo.RepoName] = repo.Score
		if repo.Score > maxUnif {
			maxUnif = repo.Score
		}
	}
	for _, repo := range perf.Repos {
		perfByRepo[repo.RepoName] = repo.Score
		if repo.Score > maxPerf {
			maxPerf = repo.Score
		}
	}
	for _, primitive := range prims.Primitives {
		if primitive.Repo == "" || len(primitive.Warnings) == 0 {
			continue
		}
		warnByRepo[primitive.Repo] += len(primitive.Warnings)
		if warnByRepo[primitive.Repo] > maxWarn {
			maxWarn = warnByRepo[primitive.Repo]
		}
	}

	weights := defaultWeights()
	rows := make([]RepoDebt, 0, len(manifest.Repos))

	for _, repo := range manifest.Repos {
		if !includeRepo(repo.Scope, allScopes) {
			continue
		}
		row := RepoDebt{
			Repo:                  repo.Name,
			Scope:                 repo.Scope,
			Language:              repo.Language,
			UnificationScore:      unifByRepo[repo.Name],
			PrimitiveWarningCount: warnByRepo[repo.Name],
			PerfScore:             perfByRepo[repo.Name],
		}
		row.UnificationNorm = float64(row.UnificationScore) / float64(maxUnif)
		row.PrimitiveWarningsNorm = float64(row.PrimitiveWarningCount) / float64(maxWarn)
		row.PerfNorm = float64(row.PerfScore) / float64(maxPerf)
		row.DebtScore = (weights.Unification * row.UnificationNorm) +
			(weights.PrimitiveWarnings * row.PrimitiveWarningsNorm) +
			(weights.PerfBottleneck * row.PerfNorm)

		switch {
		case row.DebtScore >= 0.60:
			row.Priority = "P0"
			row.SuggestedAction = "start reduction tranche"
		case row.DebtScore >= 0.35:
			row.Priority = "P1"
			row.SuggestedAction = "plan targeted dedup"
		default:
			row.Priority = "P2"
			row.SuggestedAction = "monitor / low-touch cleanup"
		}

		if row.UnificationScore > 0 {
			row.Reasons = append(row.Reasons, fmt.Sprintf("unification score %d", row.UnificationScore))
		}
		if row.PrimitiveWarningCount > 0 {
			row.Reasons = append(row.Reasons, fmt.Sprintf("primitive warnings %d", row.PrimitiveWarningCount))
		}
		if row.PerfScore > 0 {
			row.Reasons = append(row.Reasons, fmt.Sprintf("perf score %d", row.PerfScore))
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DebtScore == rows[j].DebtScore {
			return rows[i].Repo < rows[j].Repo
		}
		return rows[i].DebtScore > rows[j].DebtScore
	})

	summary := DebtAuditSummary{ReposScanned: len(rows)}
	if len(rows) > 0 {
		summary.TopRepo = rows[0].Repo
	}
	for _, row := range rows {
		switch row.Priority {
		case "P0":
			summary.P0++
		case "P1":
			summary.P1++
		default:
			summary.P2++
		}
	}

	scope := "active"
	if allScopes {
		scope = "all"
	}
	return DebtAuditReport{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot: root,
		Scope:         scope,
		Weights:       weights,
		Summary:       summary,
		Repos:         rows,
	}, nil
}

// BuildDedupCandidates returns likely low-risk reduction targets.
func BuildDedupCandidates(root string, allScopes bool, limit int) ([]DedupCandidate, error) {
	if limit <= 0 {
		limit = 25
	}
	unif, err := unificationaudit.Audit(root, unificationaudit.Options{
		WorkspaceRoot: root,
		AllScopes:     allScopes,
	})
	if err != nil {
		return nil, fmt.Errorf("run unification audit: %w", err)
	}
	prims, err := primitiveindex.Build(primitiveindex.Options{WorkspaceRoot: root})
	if err != nil {
		return nil, fmt.Errorf("build primitive index: %w", err)
	}

	out := make([]DedupCandidate, 0, limit)
	for _, repo := range unif.Repos {
		if !includeRepo(repo.Scope, allScopes) {
			continue
		}
		for _, shell := range repo.LargestShellFiles {
			if len(out) >= limit {
				return out, nil
			}
			if shell.PrimaryRole != "duplicate_implementation" && shell.PrimaryRole != "wrapper" {
				continue
			}
			out = append(out, DedupCandidate{
				Repo:      repo.RepoName,
				Path:      shell.Path,
				Kind:      shell.PrimaryRole,
				Severity:  "medium",
				Rationale: fmt.Sprintf("%d LOC shell surface classified as %s", shell.Lines, shell.PrimaryRole),
			})
		}
	}

	if len(out) >= limit {
		return out, nil
	}

	seenRepo := map[string]bool{}
	for _, primitive := range prims.Primitives {
		if len(out) >= limit {
			break
		}
		if primitive.Repo == "" || len(primitive.Warnings) == 0 || seenRepo[primitive.Repo] {
			continue
		}
		seenRepo[primitive.Repo] = true
		out = append(out, DedupCandidate{
			Repo:      primitive.Repo,
			Path:      primitive.Path,
			Kind:      "primitive_warning",
			Severity:  "low",
			Rationale: strings.Join(primitive.Warnings, "; "),
		})
	}
	return out, nil
}

// BuildReductionPlan creates tranche-ready actions for top debt repos.
func BuildReductionPlan(root string, allScopes bool, maxRepos int) (ReductionPlan, error) {
	if maxRepos <= 0 {
		maxRepos = 8
	}
	report, err := BuildDebtAudit(root, allScopes)
	if err != nil {
		return ReductionPlan{}, err
	}
	items := make([]ReductionPlanItem, 0, maxRepos)
	for _, row := range report.Repos {
		if len(items) >= maxRepos {
			break
		}
		if row.Priority == "P2" && row.DebtScore < 0.10 {
			continue
		}
		items = append(items, ReductionPlanItem{
			Repo:      row.Repo,
			Priority:  row.Priority,
			Action:    row.SuggestedAction,
			Rationale: row.Reasons,
		})
	}
	return ReductionPlan{
		GeneratedAt: report.GeneratedAt,
		Scope:       report.Scope,
		Items:       items,
	}, nil
}
