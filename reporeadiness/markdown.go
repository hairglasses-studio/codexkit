package reporeadiness

import (
	"fmt"
	"strings"
)

// Markdown renders the scorecard as a human-readable report.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Repo Mutation Readiness\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`\n\n", r.GeneratedBy)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- Scope: `%s`\n", r.Summary.Scope)
	fmt.Fprintf(&b, "- Repos scored: `%d`\n", r.Summary.ReposScored)
	fmt.Fprintf(&b, "- Lanes: full-auto `%d`, cleanup-first `%d`, human-approved `%d`, review-only `%d`\n",
		r.Summary.FullAutoCandidates,
		r.Summary.CleanupFirst,
		r.Summary.HumanApprovedOnly,
		r.Summary.ReviewOnly,
	)
	fmt.Fprintf(&b, "- Blockers: `%d` dirty repos, `%d` behind upstream, `%d` non-main branches, `%d` failing baselines\n",
		r.Summary.DirtyRepos,
		r.Summary.BehindRepos,
		r.Summary.NonMainBranchRepos,
		r.Summary.BaselineFailingRepos,
	)
	fmt.Fprintf(&b, "- Guardrails: `%d` never-publish, `%d` compatibility-only, `%d` missing directories\n",
		r.Summary.NeverPublishRepos,
		r.Summary.CompatibilityOnlyRepos,
		r.Summary.MissingDirectories,
	)

	if len(r.Repos) > 0 {
		fmt.Fprintf(&b, "\n## Repo Scores\n\n")
		fmt.Fprintf(&b, "| Repo | Lane | Score | Git | Baseline | Action |\n")
		fmt.Fprintf(&b, "|---|---|---:|---|---|---|\n")
		for _, repo := range r.Repos {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%d` | %s | %s | %s |\n",
				mdEscape(repo.RepoName),
				mdEscape(repo.Lane),
				repo.Score,
				mdEscape(gitLabel(repo.Git)),
				mdEscape(baselineLabel(repo.Baseline, repo.BaselineTarget)),
				mdEscape(repo.RecommendedAction),
			)
		}
	}

	if len(r.Warnings) > 0 {
		fmt.Fprintf(&b, "\n## Warnings\n\n")
		for _, warning := range r.Warnings {
			fmt.Fprintf(&b, "- %s\n", mdEscape(warning))
		}
	}
	return b.String()
}

func gitLabel(status GitStatus) string {
	if status.Error != "" {
		return status.Error
	}
	branch := status.Branch
	if branch == "" {
		branch = "detached"
	}
	parts := []string{branch}
	if status.DirtyFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", status.DirtyFiles))
	} else {
		parts = append(parts, "clean")
	}
	if status.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d ahead", status.Ahead))
	}
	if status.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", status.Behind))
	}
	return strings.Join(parts, ", ")
}

func baselineLabel(status *BaselineStatus, target bool) string {
	if !target {
		return "not target"
	}
	if status == nil {
		return "not checked"
	}
	if status.Passed {
		return fmt.Sprintf("pass (%d)", status.Total)
	}
	return fmt.Sprintf("fail (%d/%d)", status.Failed, status.Total)
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
