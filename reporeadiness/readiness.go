// Package reporeadiness scores workspace repos for autonomous mutation safety.
package reporeadiness

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/workspace"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	GeneratedBy = "codexkit repo readiness score"

	LaneFullAutoCandidate = "full_auto_candidate"
	LaneCleanupFirst      = "cleanup_first"
	LaneHumanApprovedOnly = "human_approved_only"
	LaneReviewOnly        = "review_only"
)

// Options controls repo scoring scope.
type Options struct {
	WorkspaceRoot string
	AllScopes     bool
	GeneratedAt   string
}

// Report is the full fleet mutation-readiness scorecard.
type Report struct {
	GeneratedAt   string      `json:"generated_at"`
	GeneratedBy   string      `json:"generated_by"`
	WorkspaceRoot string      `json:"workspace_root"`
	Summary       Summary     `json:"summary"`
	Repos         []RepoScore `json:"repos"`
	Warnings      []string    `json:"warnings,omitempty"`
}

// Summary aggregates lane and blocker counts.
type Summary struct {
	Scope                  string         `json:"scope"`
	ReposScored            int            `json:"repos_scored"`
	Lanes                  map[string]int `json:"lanes"`
	FullAutoCandidates     int            `json:"full_auto_candidates"`
	CleanupFirst           int            `json:"cleanup_first"`
	HumanApprovedOnly      int            `json:"human_approved_only"`
	ReviewOnly             int            `json:"review_only"`
	MissingDirectories     int            `json:"missing_directories"`
	DirtyRepos             int            `json:"dirty_repos"`
	BehindRepos            int            `json:"behind_repos"`
	NonMainBranchRepos     int            `json:"non_main_branch_repos"`
	BaselineFailingRepos   int            `json:"baseline_failing_repos"`
	NeverPublishRepos      int            `json:"never_publish_repos"`
	CompatibilityOnlyRepos int            `json:"compatibility_only_repos"`
}

// RepoScore captures one repo's scored readiness lane.
type RepoScore struct {
	RepoName          string          `json:"repo_name"`
	RepoPath          string          `json:"repo_path"`
	Scope             string          `json:"scope,omitempty"`
	Category          string          `json:"category,omitempty"`
	Language          string          `json:"language,omitempty"`
	Lifecycle         string          `json:"lifecycle,omitempty"`
	BaselineTarget    bool            `json:"baseline_target,omitempty"`
	FleetMode         string          `json:"fleet_mode,omitempty"`
	Git               GitStatus       `json:"git"`
	Baseline          *BaselineStatus `json:"baseline,omitempty"`
	Score             int             `json:"score"`
	Lane              string          `json:"lane"`
	Signals           []Signal        `json:"signals,omitempty"`
	Blockers          []string        `json:"blockers,omitempty"`
	RecommendedAction string          `json:"recommended_action,omitempty"`
}

// GitStatus captures cheap local git state needed for mutation safety.
type GitStatus struct {
	IsRepo     bool   `json:"is_repo"`
	Branch     string `json:"branch,omitempty"`
	DirtyFiles int    `json:"dirty_files,omitempty"`
	Ahead      int    `json:"ahead,omitempty"`
	Behind     int    `json:"behind,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BaselineStatus is a compact codexkit baseline result.
type BaselineStatus struct {
	Passed   bool              `json:"passed"`
	Total    int               `json:"total"`
	Failed   int               `json:"failed"`
	Findings []BaselineFinding `json:"findings,omitempty"`
}

// BaselineFinding is one failing baseline check.
type BaselineFinding struct {
	Check   string `json:"check"`
	Message string `json:"message,omitempty"`
}

// Signal records one scoring contribution.
type Signal struct {
	Name   string `json:"name"`
	Delta  int    `json:"delta"`
	Reason string `json:"reason"`
}

type fleetConfig struct {
	RepoModes map[string]string `toml:"repo_modes"`
}

// Score builds a repeatable mutation-readiness scorecard from manifest, fleet,
// baseline, and git state.
func Score(root string, opts Options) (Report, error) {
	if root == "" {
		root = opts.WorkspaceRoot
	}
	if root == "" {
		root = workspace.DefaultRoot()
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err != nil {
		return Report{}, err
	} else if !info.IsDir() {
		return Report{}, fmt.Errorf("workspace root is not a directory: %s", root)
	}

	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	manifest, err := workspace.LoadManifest(root)
	if err != nil {
		return Report{}, err
	}
	fleetModes, fleetWarnings := loadFleetModes(root)
	report := Report{
		GeneratedAt:   generatedAt,
		GeneratedBy:   GeneratedBy,
		WorkspaceRoot: root,
		Warnings:      fleetWarnings,
		Summary: Summary{
			Scope: scopeLabel(opts),
			Lanes: map[string]int{},
		},
	}

	for _, repo := range manifest.Repos {
		if !opts.AllScopes && repo.Scope != "active_operator" && repo.Scope != "active_first_party" {
			continue
		}
		score := scoreRepo(root, repo, fleetModes[repo.Name])
		report.Repos = append(report.Repos, score)
	}
	sort.Slice(report.Repos, func(i, j int) bool {
		if laneRank(report.Repos[i].Lane) == laneRank(report.Repos[j].Lane) {
			if report.Repos[i].Score == report.Repos[j].Score {
				return report.Repos[i].RepoName < report.Repos[j].RepoName
			}
			return report.Repos[i].Score > report.Repos[j].Score
		}
		return laneRank(report.Repos[i].Lane) < laneRank(report.Repos[j].Lane)
	})
	report.Summary = summarize(report.Summary.Scope, report.Repos)
	sort.Strings(report.Warnings)
	return report, nil
}

func scoreRepo(root string, repo workspace.Repo, fleetMode string) RepoScore {
	repoPath := filepath.Join(root, repo.Name)
	score := RepoScore{
		RepoName:       repo.Name,
		RepoPath:       repoPath,
		Scope:          repo.Scope,
		Category:       repo.Category,
		Language:       repo.Language,
		Lifecycle:      repo.Lifecycle,
		BaselineTarget: repo.BaselineTarget,
		FleetMode:      fleetMode,
		Score:          50,
	}
	add := func(name string, delta int, reason string) {
		score.Score += delta
		score.Signals = append(score.Signals, Signal{Name: name, Delta: delta, Reason: reason})
	}
	block := func(reason string) {
		score.Blockers = append(score.Blockers, reason)
	}

	switch repo.Scope {
	case "active_operator":
		add("scope", 15, "active operator repo")
	case "active_first_party":
		add("scope", 12, "active first-party repo")
	case "compatibility_only":
		add("scope", -28, "compatibility-only repo")
		block("compatibility-only scope")
	default:
		add("scope", -16, "non-active or unknown scope")
		block("non-active scope")
	}

	switch repo.Category {
	case "hub", "org-platform":
		add("category", 10, "shared control-plane or hub repo")
	case "application":
		add("category", 7, "active application repo")
	case "emerging":
		add("category", 3, "emerging repo")
	default:
		add("category", 0, "local or uncategorized repo")
	}

	switch repo.Lifecycle {
	case "active":
		add("lifecycle", 10, "active lifecycle")
	case "never-publish":
		add("lifecycle", -10, "sensitive never-publish lifecycle")
		block("never-publish requires human approval")
	case "maintenance-only":
		add("lifecycle", -12, "maintenance-only lifecycle")
		block("maintenance-only lifecycle")
	default:
		add("lifecycle", -4, "unknown lifecycle")
	}

	switch fleetMode {
	case "active":
		add("fleet_mode", 8, "explicitly active in .ralph/fleet.toml")
	case "passive", "archived":
		add("fleet_mode", -30, "fleet mode skips autonomous triage")
		block("fleet mode is " + fleetMode)
	case "":
		add("fleet_mode", 0, "no fleet mode override")
	default:
		add("fleet_mode", -8, "unknown fleet mode "+fleetMode)
	}

	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		add("repo_directory", -60, "repo directory missing")
		block("missing repo directory")
		score.Git = GitStatus{Error: "missing directory"}
		score.Score = clamp(score.Score, 0, 100)
		score.Lane = LaneReviewOnly
		score.RecommendedAction = recommendedAction(score)
		return score
	}

	score.Git = gitStatus(repoPath)
	if !score.Git.IsRepo {
		add("git", -25, "not a git repo or git status failed")
		block("git status unavailable")
	} else {
		switch {
		case score.Git.DirtyFiles == 0:
			add("git_dirty", 12, "clean worktree")
		case score.Git.DirtyFiles <= 5:
			add("git_dirty", -6, "small dirty worktree")
			block("dirty worktree")
		case score.Git.DirtyFiles <= 20:
			add("git_dirty", -14, "dirty worktree")
			block("dirty worktree")
		default:
			add("git_dirty", -24, "large dirty worktree")
			block("large dirty worktree")
		}
		if score.Git.Behind > 0 {
			add("git_behind", -10, "branch is behind upstream")
			block("behind upstream")
		}
		if score.Git.Ahead > 0 {
			add("git_ahead", -4, "branch has unpushed commits")
			block("unpushed commits")
		}
		if score.Git.Branch != "" && !mainlineBranch(score.Git.Branch) {
			add("git_branch", -8, "non-main branch")
			block("non-main branch")
		}
	}

	if repo.BaselineTarget {
		baseline := baselineguard.Check(repoPath)
		score.Baseline = compactBaseline(baseline)
		if baseline.Passed {
			add("baseline", 15, "baseline target passes")
		} else {
			add("baseline", -25-baseline.Failed, "baseline target fails")
			block("baseline failing")
		}
	} else {
		add("baseline", -2, "not a baseline target")
	}

	score.Score = clamp(score.Score, 0, 100)
	score.Lane = laneFor(score)
	score.RecommendedAction = recommendedAction(score)
	return score
}

func loadFleetModes(root string) (map[string]string, []string) {
	path := filepath.Join(root, ".ralph", "fleet.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read fleet.toml: %v", err)}
	}
	var cfg fleetConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, []string{fmt.Sprintf("parse fleet.toml: %v", err)}
	}
	return cfg.RepoModes, nil
}

func gitStatus(repoPath string) GitStatus {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain=v1", "-b")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return GitStatus{Error: msg}
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	status := GitStatus{IsRepo: true}
	if len(lines) == 0 {
		return status
	}
	status.Branch, status.Ahead, status.Behind = parseBranchLine(lines[0])
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		status.DirtyFiles++
	}
	return status
}

func parseBranchLine(line string) (branch string, ahead int, behind int) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "##"))
	if idx := strings.Index(line, "["); idx >= 0 {
		meta := strings.TrimSuffix(strings.TrimSpace(line[idx+1:]), "]")
		for _, part := range strings.Split(meta, ",") {
			part = strings.TrimSpace(part)
			switch {
			case strings.HasPrefix(part, "ahead "):
				ahead, _ = strconv.Atoi(strings.TrimPrefix(part, "ahead "))
			case strings.HasPrefix(part, "behind "):
				behind, _ = strconv.Atoi(strings.TrimPrefix(part, "behind "))
			}
		}
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, "..."); idx >= 0 {
		line = line[:idx]
	}
	if fields := strings.Fields(line); len(fields) > 0 {
		branch = fields[0]
	}
	if branch == "HEAD" {
		branch = ""
	}
	return branch, ahead, behind
}

func compactBaseline(report baselineguard.Report) *BaselineStatus {
	status := &BaselineStatus{
		Passed: report.Passed,
		Total:  report.Total,
		Failed: report.Failed,
	}
	for _, finding := range report.Findings {
		if finding.Passed {
			continue
		}
		status.Findings = append(status.Findings, BaselineFinding{
			Check:   finding.Check,
			Message: finding.Message,
		})
	}
	return status
}

func laneFor(repo RepoScore) string {
	if hasBlocker(repo, "missing repo directory") ||
		hasBlocker(repo, "compatibility-only scope") ||
		hasBlocker(repo, "non-active scope") ||
		strings.HasPrefix(repo.FleetMode, "passive") ||
		strings.HasPrefix(repo.FleetMode, "archived") ||
		repo.Scope == "compatibility_only" {
		return LaneReviewOnly
	}
	if hasBlocker(repo, "dirty worktree") ||
		hasBlocker(repo, "large dirty worktree") ||
		hasBlocker(repo, "behind upstream") ||
		hasBlocker(repo, "baseline failing") ||
		hasBlocker(repo, "non-main branch") {
		return LaneCleanupFirst
	}
	if hasBlocker(repo, "never-publish requires human approval") ||
		repo.Score < 80 {
		return LaneHumanApprovedOnly
	}
	return LaneFullAutoCandidate
}

func recommendedAction(repo RepoScore) string {
	switch repo.Lane {
	case LaneFullAutoCandidate:
		return "Eligible for autonomous loop mutation after task-specific preflight."
	case LaneCleanupFirst:
		return "Clean worktree, sync branch, and repair baseline before roadmap work."
	case LaneHumanApprovedOnly:
		return "Use targeted human-approved changes; avoid unattended broad mutation."
	default:
		return "Keep read-only or documentation-only until reactivated or archived."
	}
}

func hasBlocker(repo RepoScore, value string) bool {
	for _, blocker := range repo.Blockers {
		if blocker == value {
			return true
		}
	}
	return false
}

func summarize(scope string, repos []RepoScore) Summary {
	summary := Summary{
		Scope: scope,
		Lanes: map[string]int{},
	}
	for _, repo := range repos {
		summary.ReposScored++
		summary.Lanes[repo.Lane]++
		switch repo.Lane {
		case LaneFullAutoCandidate:
			summary.FullAutoCandidates++
		case LaneCleanupFirst:
			summary.CleanupFirst++
		case LaneHumanApprovedOnly:
			summary.HumanApprovedOnly++
		case LaneReviewOnly:
			summary.ReviewOnly++
		}
		if hasBlocker(repo, "missing repo directory") {
			summary.MissingDirectories++
		}
		if repo.Git.DirtyFiles > 0 {
			summary.DirtyRepos++
		}
		if repo.Git.Behind > 0 {
			summary.BehindRepos++
		}
		if repo.Git.Branch != "" && !mainlineBranch(repo.Git.Branch) {
			summary.NonMainBranchRepos++
		}
		if repo.Baseline != nil && !repo.Baseline.Passed {
			summary.BaselineFailingRepos++
		}
		if repo.Lifecycle == "never-publish" {
			summary.NeverPublishRepos++
		}
		if repo.Scope == "compatibility_only" {
			summary.CompatibilityOnlyRepos++
		}
	}
	return summary
}

func mainlineBranch(branch string) bool {
	return branch == "main" || branch == "master" || branch == "trunk"
}

func laneRank(lane string) int {
	switch lane {
	case LaneCleanupFirst:
		return 0
	case LaneFullAutoCandidate:
		return 1
	case LaneHumanApprovedOnly:
		return 2
	case LaneReviewOnly:
		return 3
	default:
		return 4
	}
}

func scopeLabel(opts Options) string {
	if opts.AllScopes {
		return "all"
	}
	return "active"
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
