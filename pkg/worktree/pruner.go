package worktree

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

const (
	AgentID               = 38
	AgentName             = "Agent 38: Stale worktree pruner"
	DefaultStaleThreshold = 2 * time.Hour

	ReasonProcessDead      = "process_dead"
	ReasonStaleActivity    = "stale_activity"
	ReasonMissingDirectory = "missing_directory"
	ReasonGitPrunable      = "git_prunable"

	ActionPruned  = "pruned"
	ActionSkipped = "skipped"
	ActionFailed  = "failed"
	ActionDryRun  = "dry_run"
)

var pidRegex = regexp.MustCompile(`(?i)(?:pid[:=\s]+)?(\b\d{2,7}\b)`)

type Worktree struct {
	Path           string    `json:"path"`
	Head           string    `json:"head"`
	Branch         string    `json:"branch"`
	Bare           bool      `json:"bare"`
	Detached       bool      `json:"detached"`
	Locked         bool      `json:"locked"`
	LockReason     string    `json:"lock_reason,omitempty"`
	Prunable       bool      `json:"prunable"`
	PrunableReason string    `json:"prunable_reason,omitempty"`
	IsMain         bool      `json:"is_main"`
	PID            int       `json:"pid,omitempty"`
	LastActivity   time.Time `json:"last_activity"`
	Exists         bool      `json:"exists"`
}

type Config struct {
	RepoPath           string                                                                             `json:"repo_path"`
	GitPath            string                                                                             `json:"git_path"`
	StaleThreshold     time.Duration                                                                      `json:"stale_threshold"`
	DryRun             bool                                                                               `json:"dry_run"`
	Force              bool                                                                               `json:"force"`
	DeleteBranch       bool                                                                               `json:"delete_branch"`
	PidFileNames       []string                                                                           `json:"pid_file_names"`
	ProcessChecker     func(pid int) bool                                                                 `json:"-"`
	LastActivityFinder func(wt Worktree) (time.Time, error)                                               `json:"-"`
	CommandRunner      func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) `json:"-"`
}

type PruneDecision struct {
	ShouldPrune bool     `json:"should_prune"`
	Reason      string   `json:"reason"`
	Worktree    Worktree `json:"worktree"`
}

type PruneRecord struct {
	Worktree Worktree `json:"worktree"`
	Action   string   `json:"action"`
	Reason   string   `json:"reason"`
	Error    string   `json:"error,omitempty"`
}

type PruneReport struct {
	RepoPath     string        `json:"repo_path"`
	AgentID      int           `json:"agent_id"`
	TotalScanned int           `json:"total_scanned"`
	PrunedCount  int           `json:"pruned_count"`
	SkippedCount int           `json:"skipped_count"`
	FailedCount  int           `json:"failed_count"`
	Records      []PruneRecord `json:"records"`
}

type Pruner struct {
	cfg Config
}

func DefaultConfig(repoPath string) Config {
	return Config{
		RepoPath:       repoPath,
		GitPath:        "git",
		StaleThreshold: DefaultStaleThreshold,
		DryRun:         false,
		Force:          false,
		DeleteBranch:   false,
		PidFileNames:   []string{".pid", "pid", ".agent.pid", ".codex.pid", "worktree.pid", ".session.pid"},
		ProcessChecker: DefaultProcessAlive,
	}
}

func NewPruner(cfg Config) *Pruner {
	if cfg.GitPath == "" {
		cfg.GitPath = "git"
	}
	if len(cfg.PidFileNames) == 0 {
		cfg.PidFileNames = []string{".pid", "pid", ".agent.pid", ".codex.pid", "worktree.pid", ".session.pid"}
	}
	if cfg.ProcessChecker == nil {
		cfg.ProcessChecker = DefaultProcessAlive
	}
	return &Pruner{cfg: cfg}
}

func DefaultProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

func (p *Pruner) runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if p.cfg.CommandRunner != nil {
		return p.cfg.CommandRunner(ctx, dir, p.cfg.GitPath, args...)
	}
	cmd := exec.CommandContext(ctx, p.cfg.GitPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

func ParseWorktreeListPorcelain(output string, repoPath string) ([]Worktree, error) {
	var worktrees []Worktree
	scanner := bufio.NewScanner(strings.NewReader(output))
	var current *Worktree

	canonicalRepo := repoPath
	if cp, err := filepath.EvalSymlinks(repoPath); err == nil {
		canonicalRepo = cp
	}

	flush := func() {
		if current != nil && current.Path != "" {
			cp := current.Path
			if ev, err := filepath.EvalSymlinks(current.Path); err == nil {
				cp = ev
			}
			if len(worktrees) == 0 || filepath.Clean(cp) == filepath.Clean(canonicalRepo) {
				current.IsMain = true
			}
			worktrees = append(worktrees, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			flush()
			current = &Worktree{
				Path: strings.TrimPrefix(line, "worktree "),
			}
			continue
		}

		if current == nil {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		key := parts[0]
		val := ""
		if len(parts) > 1 {
			val = parts[1]
		}

		switch key {
		case "HEAD":
			current.Head = val
		case "branch":
			current.Branch = val
		case "bare":
			current.Bare = true
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = true
			current.LockReason = val
		case "prunable":
			current.Prunable = true
			current.PrunableReason = val
		}
	}
	flush()

	return worktrees, scanner.Err()
}

func (p *Pruner) FindPID(wt Worktree) int {
	if wt.Path != "" {
		for _, name := range p.cfg.PidFileNames {
			pidFile := filepath.Join(wt.Path, name)
			if data, err := os.ReadFile(pidFile); err == nil {
				text := strings.TrimSpace(string(data))
				if pid, err := strconv.Atoi(text); err == nil && pid > 0 {
					return pid
				}
			}
		}

		gitPath := filepath.Join(wt.Path, ".git")
		if data, err := os.ReadFile(gitPath); err == nil {
			line := strings.TrimSpace(string(data))
			if strings.HasPrefix(line, "gitdir:") {
				gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(wt.Path, gitDir)
				}
				for _, name := range []string{"pid", ".pid", "locked"} {
					f := filepath.Join(gitDir, name)
					if data, err := os.ReadFile(f); err == nil {
						matches := pidRegex.FindStringSubmatch(strings.TrimSpace(string(data)))
						if len(matches) > 1 {
							if pid, err := strconv.Atoi(matches[1]); err == nil && pid > 0 {
								return pid
							}
						}
					}
				}
			}
		}
	}

	if wt.LockReason != "" {
		matches := pidRegex.FindStringSubmatch(wt.LockReason)
		if len(matches) > 1 {
			if pid, err := strconv.Atoi(matches[1]); err == nil && pid > 0 {
				return pid
			}
		}
	}

	return 0
}

func (p *Pruner) FindLastActivity(wt Worktree) (time.Time, error) {
	if p.cfg.LastActivityFinder != nil {
		return p.cfg.LastActivityFinder(wt)
	}

	if wt.Path == "" {
		return time.Time{}, nil
	}

	info, err := os.Stat(wt.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}

	latest := info.ModTime()

	gitEntry := filepath.Join(wt.Path, ".git")
	if gitInfo, err := os.Stat(gitEntry); err == nil {
		if gitInfo.ModTime().After(latest) {
			latest = gitInfo.ModTime()
		}
	}

	if gitData, err := os.ReadFile(gitEntry); err == nil {
		line := strings.TrimSpace(string(gitData))
		if strings.HasPrefix(line, "gitdir:") {
			gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(wt.Path, gitDir)
			}
			for _, checkFile := range []string{"index", "HEAD", "logs/HEAD", "COMMIT_EDITMSG"} {
				target := filepath.Join(gitDir, checkFile)
				if targetInfo, err := os.Stat(target); err == nil {
					if targetInfo.ModTime().After(latest) {
						latest = targetInfo.ModTime()
					}
				}
			}
		}
	}

	entries, err := os.ReadDir(wt.Path)
	if err == nil {
		count := 0
		for _, entry := range entries {
			if count > 50 {
				break
			}
			count++
			if eInfo, err := entry.Info(); err == nil {
				if eInfo.ModTime().After(latest) {
					latest = eInfo.ModTime()
				}
			}
		}
	}

	return latest, nil
}

func (p *Pruner) Evaluate(wt Worktree, now time.Time) PruneDecision {
	if wt.IsMain {
		return PruneDecision{
			ShouldPrune: false,
			Reason:      "main worktree cannot be pruned",
			Worktree:    wt,
		}
	}

	if !wt.Exists {
		return PruneDecision{
			ShouldPrune: true,
			Reason:      ReasonMissingDirectory,
			Worktree:    wt,
		}
	}

	if wt.Prunable {
		return PruneDecision{
			ShouldPrune: true,
			Reason:      ReasonGitPrunable,
			Worktree:    wt,
		}
	}

	if wt.PID > 0 {
		alive := false
		if p.cfg.ProcessChecker != nil {
			alive = p.cfg.ProcessChecker(wt.PID)
		} else {
			alive = DefaultProcessAlive(wt.PID)
		}

		if !alive {
			return PruneDecision{
				ShouldPrune: true,
				Reason:      ReasonProcessDead,
				Worktree:    wt,
			}
		}

		if !p.cfg.Force {
			return PruneDecision{
				ShouldPrune: false,
				Reason:      fmt.Sprintf("process %d is still active", wt.PID),
				Worktree:    wt,
			}
		}
	}

	if p.cfg.StaleThreshold > 0 && !wt.LastActivity.IsZero() {
		age := now.Sub(wt.LastActivity)
		if age > p.cfg.StaleThreshold {
			return PruneDecision{
				ShouldPrune: true,
				Reason:      ReasonStaleActivity,
				Worktree:    wt,
			}
		}
		return PruneDecision{
			ShouldPrune: false,
			Reason:      fmt.Sprintf("worktree is active (last activity %s ago)", age.Truncate(time.Second)),
			Worktree:    wt,
		}
	}

	if wt.LastActivity.IsZero() {
		return PruneDecision{
			ShouldPrune: true,
			Reason:      ReasonStaleActivity,
			Worktree:    wt,
		}
	}

	return PruneDecision{
		ShouldPrune: false,
		Reason:      "worktree has no stale trigger",
		Worktree:    wt,
	}
}

func (p *Pruner) List(ctx context.Context) ([]Worktree, error) {
	out, err := p.runGit(ctx, p.cfg.RepoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list failed: %w (output: %s)", err, string(out))
	}

	worktrees, err := ParseWorktreeListPorcelain(string(out), p.cfg.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("parse git worktree list: %w", err)
	}

	for i := range worktrees {
		wt := &worktrees[i]
		if _, err := os.Stat(wt.Path); err == nil {
			wt.Exists = true
		}
		if wt.PID == 0 {
			wt.PID = p.FindPID(*wt)
		}
		if wt.LastActivity.IsZero() {
			act, _ := p.FindLastActivity(*wt)
			wt.LastActivity = act
		}
	}

	return worktrees, nil
}

func (p *Pruner) PruneOne(ctx context.Context, wt Worktree) PruneRecord {
	now := time.Now()
	decision := p.Evaluate(wt, now)
	if !decision.ShouldPrune {
		return PruneRecord{
			Worktree: wt,
			Action:   ActionSkipped,
			Reason:   decision.Reason,
		}
	}

	if p.cfg.DryRun {
		return PruneRecord{
			Worktree: wt,
			Action:   ActionDryRun,
			Reason:   decision.Reason,
		}
	}

	if wt.Locked {
		_, _ = p.runGit(ctx, p.cfg.RepoPath, "worktree", "unlock", wt.Path)
	}

	out, err := p.runGit(ctx, p.cfg.RepoPath, "worktree", "remove", "--force", wt.Path)
	if err != nil {
		_, _ = p.runGit(ctx, p.cfg.RepoPath, "worktree", "prune")
	}

	if wt.Path != "" {
		if _, statErr := os.Stat(wt.Path); statErr == nil {
			_ = os.RemoveAll(wt.Path)
		}
	}

	if p.cfg.DeleteBranch && wt.Branch != "" && !wt.Detached && !wt.Bare {
		branchName := strings.TrimPrefix(wt.Branch, "refs/heads/")
		_, _ = p.runGit(ctx, p.cfg.RepoPath, "branch", "-D", branchName)
	}

	_, _ = p.runGit(ctx, p.cfg.RepoPath, "worktree", "prune")

	if err != nil && wt.Exists {
		if _, statErr := os.Stat(wt.Path); statErr == nil {
			return PruneRecord{
				Worktree: wt,
				Action:   ActionFailed,
				Reason:   decision.Reason,
				Error:    fmt.Sprintf("%v: %s", err, string(out)),
			}
		}
	}

	return PruneRecord{
		Worktree: wt,
		Action:   ActionPruned,
		Reason:   decision.Reason,
	}
}

func (p *Pruner) Prune(ctx context.Context) (*PruneReport, error) {
	worktrees, err := p.List(ctx)
	if err != nil {
		return nil, err
	}

	report := &PruneReport{
		RepoPath:     p.cfg.RepoPath,
		AgentID:      AgentID,
		TotalScanned: len(worktrees),
		Records:      make([]PruneRecord, 0, len(worktrees)),
	}

	for _, wt := range worktrees {
		record := p.PruneOne(ctx, wt)
		report.Records = append(report.Records, record)
		switch record.Action {
		case ActionPruned, ActionDryRun:
			report.PrunedCount++
		case ActionSkipped:
			report.SkippedCount++
		case ActionFailed:
			report.FailedCount++
		}
	}

	if !p.cfg.DryRun {
		_, _ = p.runGit(ctx, p.cfg.RepoPath, "worktree", "prune")
	}

	return report, nil
}

func (p *Pruner) CleanDead(ctx context.Context) (*PruneReport, error) {
	return p.Prune(ctx)
}

func CleanDead(ctx context.Context, repoPath string) (*PruneReport, error) {
	pruner := NewPruner(DefaultConfig(repoPath))
	return pruner.CleanDead(ctx)
}

func PruneWorktrees(ctx context.Context, cfg Config) (*PruneReport, error) {
	pruner := NewPruner(cfg)
	return pruner.Prune(ctx)
}

type module struct{}

func Module() codexkit.ToolModule {
	return &module{}
}

func (m *module) Name() string {
	return "worktree"
}

func (m *module) Init() error {
	return nil
}

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "worktree_list",
			Description: "List registered git worktrees with last activity and process status",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{
						"type": "string",
					},
				},
				"required": []string{"repo_path"},
			},
			Handler: codexkit.TypedHandler(func(req struct {
				RepoPath string `json:"repo_path"`
			}) (any, error) {
				pruner := NewPruner(DefaultConfig(req.RepoPath))
				return pruner.List(context.Background())
			}),
		},
		{
			Name:        "worktree_prune",
			Description: "Prune stale worktrees based on last activity and process death",
			Annotations: codexkit.ToolAnnotations(false, true, false, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo_path": map[string]any{
						"type": "string",
					},
					"stale_seconds": map[string]any{
						"type": "integer",
					},
					"dry_run": map[string]any{
						"type": "boolean",
					},
					"force": map[string]any{
						"type": "boolean",
					},
					"delete_branch": map[string]any{
						"type": "boolean",
					},
				},
				"required": []string{"repo_path"},
			},
			Handler: codexkit.TypedHandler(func(req struct {
				RepoPath     string `json:"repo_path"`
				StaleSeconds int    `json:"stale_seconds"`
				DryRun       bool   `json:"dry_run"`
				Force        bool   `json:"force"`
				DeleteBranch bool   `json:"delete_branch"`
			}) (any, error) {
				cfg := DefaultConfig(req.RepoPath)
				if req.StaleSeconds > 0 {
					cfg.StaleThreshold = time.Duration(req.StaleSeconds) * time.Second
				}
				cfg.DryRun = req.DryRun
				cfg.Force = req.Force
				cfg.DeleteBranch = req.DeleteBranch
				pruner := NewPruner(cfg)
				return pruner.Prune(context.Background())
			}),
		},
	}
}
