package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseWorktreeListPorcelain(t *testing.T) {
	repoDir := "/tmp/test-repo"
	output := `worktree /tmp/test-repo
HEAD aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
branch refs/heads/main

worktree /tmp/test-repo-wt1
HEAD bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
branch refs/heads/feature-one

worktree /tmp/test-repo-wt2
HEAD cccccccccccccccccccccccccccccccccccc
detached

worktree /tmp/test-repo-wt3
bare

worktree /tmp/test-repo-wt4
HEAD dddddddddddddddddddddddddddddddddddddddd
branch refs/heads/feature-two
locked pid: 98765

worktree /tmp/test-repo-wt5
HEAD eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
branch refs/heads/feature-three
prunable gitdir file points to non-existent location
`

	wts, err := ParseWorktreeListPorcelain(output, repoDir)
	if err != nil {
		t.Fatalf("unexpected error parsing porcelain: %v", err)
	}

	if len(wts) != 6 {
		t.Fatalf("expected 6 worktrees, got %d", len(wts))
	}

	if !wts[0].IsMain {
		t.Errorf("expected first worktree to be main")
	}
	if wts[0].Path != "/tmp/test-repo" {
		t.Errorf("expected path /tmp/test-repo, got %s", wts[0].Path)
	}
	if wts[0].Branch != "refs/heads/main" {
		t.Errorf("expected branch refs/heads/main, got %s", wts[0].Branch)
	}

	if wts[1].IsMain {
		t.Errorf("expected second worktree not to be main")
	}
	if wts[1].Branch != "refs/heads/feature-one" {
		t.Errorf("expected branch refs/heads/feature-one, got %s", wts[1].Branch)
	}

	if !wts[2].Detached {
		t.Errorf("expected third worktree to be detached")
	}

	if !wts[3].Bare {
		t.Errorf("expected fourth worktree to be bare")
	}

	if !wts[4].Locked {
		t.Errorf("expected fifth worktree to be locked")
	}
	if wts[4].LockReason != "pid: 98765" {
		t.Errorf("expected lock reason 'pid: 98765', got '%s'", wts[4].LockReason)
	}

	if !wts[5].Prunable {
		t.Errorf("expected sixth worktree to be prunable")
	}
	if !strings.Contains(wts[5].PrunableReason, "gitdir file points") {
		t.Errorf("unexpected prunable reason: %s", wts[5].PrunableReason)
	}
}

func TestEvaluateMainWorktree(t *testing.T) {
	pruner := NewPruner(DefaultConfig("/tmp/repo"))
	wt := Worktree{
		Path:   "/tmp/repo",
		IsMain: true,
		Exists: false,
		PID:    99999999,
	}

	dec := pruner.Evaluate(wt, time.Now())
	if dec.ShouldPrune {
		t.Fatalf("main worktree must never be pruned")
	}
}

func TestEvaluateMissingDirectory(t *testing.T) {
	pruner := NewPruner(DefaultConfig("/tmp/repo"))
	wt := Worktree{
		Path:   "/tmp/repo-wt",
		IsMain: false,
		Exists: false,
	}

	dec := pruner.Evaluate(wt, time.Now())
	if !dec.ShouldPrune {
		t.Fatalf("missing directory worktree should be pruned")
	}
	if dec.Reason != ReasonMissingDirectory {
		t.Fatalf("expected reason %s, got %s", ReasonMissingDirectory, dec.Reason)
	}
}

func TestEvaluateGitPrunable(t *testing.T) {
	pruner := NewPruner(DefaultConfig("/tmp/repo"))
	wt := Worktree{
		Path:     "/tmp/repo-wt",
		IsMain:   false,
		Exists:   true,
		Prunable: true,
	}

	dec := pruner.Evaluate(wt, time.Now())
	if !dec.ShouldPrune {
		t.Fatalf("git prunable worktree should be pruned")
	}
	if dec.Reason != ReasonGitPrunable {
		t.Fatalf("expected reason %s, got %s", ReasonGitPrunable, dec.Reason)
	}
}

func TestEvaluateProcessDeath(t *testing.T) {
	cfg := DefaultConfig("/tmp/repo")
	cfg.ProcessChecker = func(pid int) bool {
		return pid == 100
	}
	pruner := NewPruner(cfg)

	deadWt := Worktree{
		Path:   "/tmp/repo-wt-dead",
		IsMain: false,
		Exists: true,
		PID:    200,
	}

	decDead := pruner.Evaluate(deadWt, time.Now())
	if !decDead.ShouldPrune {
		t.Fatalf("dead process worktree should be pruned")
	}
	if decDead.Reason != ReasonProcessDead {
		t.Fatalf("expected reason %s, got %s", ReasonProcessDead, decDead.Reason)
	}

	aliveWt := Worktree{
		Path:         "/tmp/repo-wt-alive",
		IsMain:       false,
		Exists:       true,
		PID:          100,
		LastActivity: time.Now().Add(-10 * time.Hour),
	}

	decAlive := pruner.Evaluate(aliveWt, time.Now())
	if decAlive.ShouldPrune {
		t.Fatalf("alive process worktree should not be pruned")
	}
}

func TestEvaluateStaleActivity(t *testing.T) {
	cfg := DefaultConfig("/tmp/repo")
	cfg.StaleThreshold = 1 * time.Hour
	pruner := NewPruner(cfg)
	now := time.Now()

	staleWt := Worktree{
		Path:         "/tmp/repo-wt-stale",
		IsMain:       false,
		Exists:       true,
		LastActivity: now.Add(-2 * time.Hour),
	}

	decStale := pruner.Evaluate(staleWt, now)
	if !decStale.ShouldPrune {
		t.Fatalf("stale activity worktree should be pruned")
	}
	if decStale.Reason != ReasonStaleActivity {
		t.Fatalf("expected reason %s, got %s", ReasonStaleActivity, decStale.Reason)
	}

	recentWt := Worktree{
		Path:         "/tmp/repo-wt-recent",
		IsMain:       false,
		Exists:       true,
		LastActivity: now.Add(-10 * time.Minute),
	}

	decRecent := pruner.Evaluate(recentWt, now)
	if decRecent.ShouldPrune {
		t.Fatalf("recent activity worktree should not be pruned")
	}
}

func TestFindPID(t *testing.T) {
	tmpDir := t.TempDir()
	pruner := NewPruner(DefaultConfig(tmpDir))

	wt1 := filepath.Join(tmpDir, "wt1")
	if err := os.MkdirAll(wt1, 0755); err != nil {
		t.Fatalf("mkdir wt1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt1, ".pid"), []byte("12345\n"), 0644); err != nil {
		t.Fatalf("write .pid: %v", err)
	}

	pid1 := pruner.FindPID(Worktree{Path: wt1})
	if pid1 != 12345 {
		t.Errorf("expected PID 12345, got %d", pid1)
	}

	wt2 := filepath.Join(tmpDir, "wt2")
	if err := os.MkdirAll(wt2, 0755); err != nil {
		t.Fatalf("mkdir wt2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt2, ".agent.pid"), []byte("54321\n"), 0644); err != nil {
		t.Fatalf("write .agent.pid: %v", err)
	}

	pid2 := pruner.FindPID(Worktree{Path: wt2})
	if pid2 != 54321 {
		t.Errorf("expected PID 54321, got %d", pid2)
	}

	pid3 := pruner.FindPID(Worktree{LockReason: "locked by agent pid: 88888"})
	if pid3 != 88888 {
		t.Errorf("expected PID 88888, got %d", pid3)
	}

	pid4 := pruner.FindPID(Worktree{LockReason: "manual inspection lock"})
	if pid4 != 0 {
		t.Errorf("expected PID 0 for text lock, got %d", pid4)
	}
}

func TestFindLastActivity(t *testing.T) {
	tmpDir := t.TempDir()
	pruner := NewPruner(DefaultConfig(tmpDir))

	wtDir := filepath.Join(tmpDir, "wt")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}

	tPast := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	tRecent := time.Now().Add(-5 * time.Minute).Truncate(time.Second)

	f1 := filepath.Join(wtDir, "old.txt")
	if err := os.WriteFile(f1, []byte("old"), 0644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	_ = os.Chtimes(f1, tPast, tPast)

	f2 := filepath.Join(wtDir, "new.txt")
	if err := os.WriteFile(f2, []byte("new"), 0644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	_ = os.Chtimes(f2, tRecent, tRecent)

	act, err := pruner.FindLastActivity(Worktree{Path: wtDir})
	if err != nil {
		t.Fatalf("find last activity: %v", err)
	}

	if act.Before(tPast) {
		t.Errorf("expected activity >= %v, got %v", tPast, act)
	}

	zeroAct, err := pruner.FindLastActivity(Worktree{Path: filepath.Join(tmpDir, "nonexistent")})
	if err != nil {
		t.Fatalf("find non-existent activity: %v", err)
	}
	if !zeroAct.IsZero() {
		t.Errorf("expected zero activity for missing path, got %v", zeroAct)
	}
}

func TestDefaultProcessAlive(t *testing.T) {
	if !DefaultProcessAlive(os.Getpid()) {
		t.Errorf("expected current process %d to be alive", os.Getpid())
	}

	if DefaultProcessAlive(0) {
		t.Errorf("expected PID 0 to be dead")
	}

	if DefaultProcessAlive(-1) {
		t.Errorf("expected PID -1 to be dead")
	}

	if DefaultProcessAlive(99999999) {
		t.Errorf("expected high non-existent PID to be dead")
	}
}

func TestPrunerDryRun(t *testing.T) {
	cfg := DefaultConfig("/tmp/test-repo")
	cfg.DryRun = true
	cfg.ProcessChecker = func(pid int) bool {
		return false
	}
	pruner := NewPruner(cfg)

	wt := Worktree{
		Path:   "/tmp/test-repo-wt",
		IsMain: false,
		Exists: true,
		PID:    99999,
	}

	rec := pruner.PruneOne(context.Background(), wt)
	if rec.Action != ActionDryRun {
		t.Fatalf("expected ActionDryRun, got %s", rec.Action)
	}
	if rec.Reason != ReasonProcessDead {
		t.Fatalf("expected reason %s, got %s", ReasonProcessDead, rec.Reason)
	}
}

func setupTestGitRepo(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v (output: %s)", args, err, string(out))
		}
	}

	run(repoDir, "init")
	run(repoDir, "config", "user.email", "tester@example.com")
	run(repoDir, "config", "user.name", "Tester")

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("test\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	run(repoDir, "add", "README.md")
	run(repoDir, "commit", "-m", "initial commit")

	wtDeadDir := filepath.Join(root, "wt-dead")
	run(repoDir, "worktree", "add", wtDeadDir, "-b", "branch-dead")

	wtAliveDir := filepath.Join(root, "wt-alive")
	run(repoDir, "worktree", "add", wtAliveDir, "-b", "branch-alive")

	return repoDir, wtDeadDir, wtAliveDir
}

func TestLiveGitIntegration(t *testing.T) {
	repoDir, wtDeadDir, wtAliveDir := setupTestGitRepo(t)

	deadPid := 99999999
	alivePid := os.Getpid()

	if err := os.WriteFile(filepath.Join(wtDeadDir, ".pid"), []byte(strconv.Itoa(deadPid)+"\n"), 0644); err != nil {
		t.Fatalf("write dead pid: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtAliveDir, ".pid"), []byte(strconv.Itoa(alivePid)+"\n"), 0644); err != nil {
		t.Fatalf("write alive pid: %v", err)
	}

	cfg := DefaultConfig(repoDir)
	cfg.ProcessChecker = func(pid int) bool {
		return pid == alivePid
	}
	pruner := NewPruner(cfg)

	wts, err := pruner.List(context.Background())
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	if len(wts) != 3 {
		t.Fatalf("expected 3 worktrees before prune, got %d", len(wts))
	}

	report, err := pruner.Prune(context.Background())
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	if report.AgentID != AgentID {
		t.Errorf("expected AgentID %d, got %d", AgentID, report.AgentID)
	}
	if report.PrunedCount != 1 {
		t.Errorf("expected 1 pruned worktree, got %d", report.PrunedCount)
	}
	if report.SkippedCount != 2 {
		t.Errorf("expected 2 skipped worktrees (main and alive), got %d", report.SkippedCount)
	}

	wtsAfter, err := pruner.List(context.Background())
	if err != nil {
		t.Fatalf("list worktrees after prune: %v", err)
	}
	if len(wtsAfter) != 2 {
		t.Fatalf("expected 2 worktrees after prune, got %d", len(wtsAfter))
	}

	for _, wt := range wtsAfter {
		if wt.Path == wtDeadDir {
			t.Errorf("dead worktree %s should have been removed", wtDeadDir)
		}
	}
}

func TestCleanDead(t *testing.T) {
	repoDir, wtDeadDir, _ := setupTestGitRepo(t)

	if err := os.RemoveAll(wtDeadDir); err != nil {
		t.Fatalf("remove wtDeadDir: %v", err)
	}

	report, err := CleanDead(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("CleanDead failed: %v", err)
	}

	if report.PrunedCount < 1 {
		t.Errorf("expected at least 1 pruned worktree, got %d", report.PrunedCount)
	}
}

func TestToolModule(t *testing.T) {
	mod := Module()
	if mod.Name() != "worktree" {
		t.Errorf("expected module name 'worktree', got '%s'", mod.Name())
	}
	if err := mod.Init(); err != nil {
		t.Errorf("unexpected error on Init: %v", err)
	}

	tools := mod.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	if !names["worktree_list"] {
		t.Errorf("missing tool 'worktree_list'")
	}
	if !names["worktree_prune"] {
		t.Errorf("missing tool 'worktree_prune'")
	}
}
