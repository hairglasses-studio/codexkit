package unificationaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditClassifiesShellHooksAndSkills(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "app/scripts/queue-sync.sh", `#!/usr/bin/env bash
set -euo pipefail
state_dir=.state
mkdir -p "$state_dir"
jq -r '.items[]' queue.json >> "$state_dir/items.jsonl"
sqlite3 "$state_dir/jobs.db" 'select 1'
mv "$state_dir/items.jsonl" "$state_dir/items.done"
`)
	writeUnificationFile(t, root, "app/scripts/start.sh", `#!/usr/bin/env bash
exec go run ./cmd/app "$@"
`)
	writeUnificationFile(t, root, "app/setup/install.sh", `#!/usr/bin/env bash
sudo apt-get install -y jq
systemctl --user daemon-reload
`)
	writeUnificationFile(t, root, "app/.claude/hooks/pre-tool.sh", `#!/usr/bin/env bash
# line 2
# line 3
# line 4
# line 5
# line 6
echo hook >> .state/hooks.log
`)
	writeUnificationFile(t, root, "app/config/python_fast_paths.tsv", "sync-plan\tsync-plan\nlocal-sync status\tlocal-sync status\n")
	writeUnificationFile(t, root, "app/.agents/skills/demo/SKILL.md", "---\nname: demo\ndescription: demo\n---\n# Demo\n")
	writeUnificationFile(t, root, "app/.claude/skills/demo/SKILL.md", "---\nname: demo\ndescription: demo\n---\n# Demo\n")
	writeUnificationFile(t, root, "app/ROADMAP.md", "# App Roadmap\n")

	report, err := Audit(root, Options{GeneratedAt: "2026-05-10T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ReposScanned != 1 {
		t.Fatalf("ReposScanned = %d, want 1", report.Summary.ReposScanned)
	}
	if report.Summary.StatefulShellFiles == 0 {
		t.Fatalf("expected stateful shell file: %+v", report.Repos[0].ShellByRole)
	}
	if report.Summary.OSBootstrapShellFiles == 0 {
		t.Fatalf("expected OS bootstrap shell file: %+v", report.Repos[0].ShellByRole)
	}
	if report.Summary.HookFiles != 1 {
		t.Fatalf("HookFiles = %d, want 1", report.Summary.HookFiles)
	}
	if report.Summary.PythonFastPathCommands != 2 {
		t.Fatalf("PythonFastPathCommands = %d, want 2", report.Summary.PythonFastPathCommands)
	}
	if report.Summary.RepoRoadmaps != 1 {
		t.Fatalf("RepoRoadmaps = %d, want 1", report.Summary.RepoRoadmaps)
	}
	if report.Summary.BaselineTargetRepos != 1 || report.Summary.BaselineCheckedRepos != 1 {
		t.Fatalf("baseline target summary = %d/%d, want 1/1", report.Summary.BaselineTargetRepos, report.Summary.BaselineCheckedRepos)
	}
	if report.Summary.BaselineFailingRepos != 1 || report.Summary.BaselineFailedFindings == 0 {
		t.Fatalf("baseline failures = %d/%d, want one failing repo with findings", report.Summary.BaselineFailingRepos, report.Summary.BaselineFailedFindings)
	}
	if report.Repos[0].Baseline == nil || report.Repos[0].Baseline.Passed {
		t.Fatalf("expected failing baseline status: %+v", report.Repos[0].Baseline)
	}
	if report.Summary.ReposMissingSkillSurface != 1 {
		t.Fatalf("ReposMissingSkillSurface = %d, want 1", report.Summary.ReposMissingSkillSurface)
	}
	if report.Repos[0].Roadmap == nil || report.Repos[0].Roadmap.Path != "ROADMAP.md" {
		t.Fatalf("expected repo roadmap: %+v", report.Repos[0].Roadmap)
	}
	if report.Repos[0].Score == 0 {
		t.Fatal("expected scored repo")
	}
	if !hasFinding(report.Repos[0], "skill_surface") {
		t.Fatalf("expected skill surface finding: %+v", report.Repos[0].Findings)
	}
	if !hasFinding(report.Repos[0], "baseline_contract") {
		t.Fatalf("expected baseline contract finding: %+v", report.Repos[0].Findings)
	}
	if len(report.Recommendations) == 0 {
		t.Fatal("expected recommendations")
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Codebase Unification Audit") || !strings.Contains(markdown, "Recommended Steps") {
		t.Fatalf("unexpected markdown:\n%s", markdown)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Python fast-path delegates") || !strings.Contains(markdown, "`local-sync status` -> `local-sync status`") {
		t.Fatalf("markdown missing Python fast path details:\n%s", markdown)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Roadmap Coverage") || !strings.Contains(markdown, "`app/ROADMAP.md`") {
		t.Fatalf("markdown missing roadmap coverage:\n%s", markdown)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Baseline targets:") || !strings.Contains(markdown, "Baseline: fail") {
		t.Fatalf("markdown missing baseline status:\n%s", markdown)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Baseline Remediation Queue") || !strings.Contains(markdown, "`app`") || !strings.Contains(markdown, "baseline check") {
		t.Fatalf("markdown missing baseline remediation queue:\n%s", markdown)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Remediation") || !strings.Contains(markdown, "restore canonical provider instruction files") {
		t.Fatalf("markdown missing baseline remediation hints:\n%s", markdown)
	}
}

func TestBaselineQueueRanksFailingTargets(t *testing.T) {
	repos := []RepoReport{
		{
			RepoName: "few",
			Score:    100,
			Baseline: &BaselineStatus{
				Passed: false,
				Failed: 2,
			},
		},
		{
			RepoName: "pass",
			Baseline: &BaselineStatus{
				Passed: true,
			},
		},
		{
			RepoName: "many",
			Score:    1,
			Baseline: &BaselineStatus{
				Passed: false,
				Failed: 5,
			},
		},
		{
			RepoName: "same-fail-higher-score",
			Score:    200,
			Baseline: &BaselineStatus{
				Passed: false,
				Failed: 2,
			},
		},
	}

	queue := baselineQueue(repos, 10)
	got := []string{queue[0].RepoName, queue[1].RepoName, queue[2].RepoName}
	want := []string{"many", "same-fail-higher-score", "few"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queue order = %+v, want %+v", got, want)
		}
	}
}

func TestBaselineCheckCommandUsesWorkspaceRoot(t *testing.T) {
	got := baselineCheckCommand("/tmp/studio", "/tmp/studio/app")
	want := "cd /tmp/studio/codexkit && GOWORK=off go run ./cmd/codexkit baseline check /tmp/studio/app"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestClassifyShell_HGModuleScriptsAreBootstrap(t *testing.T) {
	role, reasons := classifyShell("scripts/hg-llm-surface-loop.sh", []byte("#!/usr/bin/env bash\nset -euo pipefail\nexec go run ./cmd/codexkit\n"))
	if role != "os_bootstrap" {
		t.Fatalf("role = %q, want os_bootstrap", role)
	}
	if len(reasons) == 0 || !strings.Contains(reasons[0], "workspace operator") {
		t.Fatalf("unexpected reasons: %+v", reasons)
	}
}

func TestClassifyShell_FunctionDenseLibIsStructuredParser(t *testing.T) {
	content := `#!/usr/bin/env bash
set -euo pipefail
f1() { echo 1; }
f2() { echo 2; }
f3() { echo 3; }
f4() { echo 4; }
f5() { echo 5; }
f6() { echo 6; }
f7() { echo 7; }
f8() { echo 8; }
f9() { jq -r '.a'; }
`
	role, reasons := classifyShell("lib/example_commands.sh", []byte(content))
	if role != "structured_parser" {
		t.Fatalf("role = %q, want structured_parser", role)
	}
	if len(reasons) == 0 || !strings.Contains(reasons[0], "function-dense shell library") {
		t.Fatalf("unexpected reasons: %+v", reasons)
	}
}

func TestBaselineRemediationCommandUsesWorkspaceRoot(t *testing.T) {
	got := baselineRemediationCommand(
		"/tmp/studio",
		"/tmp/studio/app",
		[]string{"codexkit", "skills", "sync", "/tmp/studio/app", "--quiet-warnings"},
	)
	want := "cd /tmp/studio/codexkit && GOWORK=off go run ./cmd/codexkit skills sync /tmp/studio/app --quiet-warnings"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestInventoryShellFileListsFunctionsEntrypointsAndCallers(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "lib/sync.sh", `#!/usr/bin/env bash

load_config() {
  echo load
}

cmd_sync() {
  load_config
}
`)
	writeUnificationFile(t, root, "cr8", `#!/usr/bin/env bash
source lib/sync.sh
cmd_sync "$@"
`)
	if err := os.Chmod(filepath.Join(root, "cr8"), 0o755); err != nil {
		t.Fatal(err)
	}

	inventory, err := InventoryShellFile(root, "lib/sync.sh")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Functions) != 2 {
		t.Fatalf("functions = %+v, want 2", inventory.Functions)
	}
	if len(inventory.Entrypoints) != 1 || inventory.Entrypoints[0].Name != "cmd_sync" {
		t.Fatalf("entrypoints = %+v, want cmd_sync", inventory.Entrypoints)
	}
	if len(inventory.SourcedBy) != 1 || inventory.SourcedBy[0].Path != "cr8" {
		t.Fatalf("sourced by = %+v, want cr8", inventory.SourcedBy)
	}
	var cmdSync ShellFunction
	for _, fn := range inventory.Functions {
		if fn.Name == "cmd_sync" {
			cmdSync = fn
		}
	}
	if len(cmdSync.Callers) == 0 || cmdSync.Callers[0].Path != "cr8" {
		t.Fatalf("cmd_sync callers = %+v, want cr8", cmdSync.Callers)
	}
	markdown := inventory.Markdown()
	if !strings.Contains(markdown, "Shell File Inventory") || !strings.Contains(markdown, "`cmd_sync`") {
		t.Fatalf("unexpected markdown:\n%s", markdown)
	}
}

func TestAuditReportsExtraRoadmapsOutsideCurrentScan(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "glass/ROADMAP.md", "# Glass Roadmap\n")

	report, err := Audit(root, Options{GeneratedAt: "2026-05-10T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ExtraRoadmaps != 1 {
		t.Fatalf("ExtraRoadmaps = %d, want 1", report.Summary.ExtraRoadmaps)
	}
	if len(report.ExtraRoadmaps) != 1 || report.ExtraRoadmaps[0].RepoName != "glass" {
		t.Fatalf("ExtraRoadmaps = %+v, want glass", report.ExtraRoadmaps)
	}
	if markdown := report.Markdown(); !strings.Contains(markdown, "Outside current scan") || !strings.Contains(markdown, "`glass/ROADMAP.md`") {
		t.Fatalf("markdown missing extra roadmap:\n%s", markdown)
	}
}

func TestAuditDetectsMissingSourceSkillEntry(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	if err := os.MkdirAll(filepath.Join(root, "app", ".agents", "skills", "broken"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Audit(root, Options{GeneratedAt: "2026-05-10T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.SourceSkillDirsMissingEntry != 1 {
		t.Fatalf("SourceSkillDirsMissingEntry = %d, want 1", report.Summary.SourceSkillDirsMissingEntry)
	}
	if !hasFinding(report.Repos[0], "skill_source") {
		t.Fatalf("expected skill source finding: %+v", report.Repos[0].Findings)
	}
}

func TestAuditFallsBackToGitRepoDiscovery(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "repo/.git/HEAD", "ref: refs/heads/main\n")
	writeUnificationFile(t, root, "repo/scripts/legacy-sync.sh", "#!/usr/bin/env bash\n# deprecated legacy implementation\n")

	report, err := Audit(root, Options{GeneratedAt: "2026-05-10T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ReposScanned != 1 {
		t.Fatalf("ReposScanned = %d, want 1", report.Summary.ReposScanned)
	}
	if len(report.Warnings) == 0 {
		t.Fatal("expected fallback warning")
	}
	if report.Repos[0].ShellByRole["duplicate_implementation"].Files != 1 {
		t.Fatalf("expected duplicate shell classification: %+v", report.Repos[0].ShellByRole)
	}
}

func TestBuildCycleCarriesForwardImprovementNotes(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "app/scripts/queue-sync.sh", `#!/usr/bin/env bash
jq -r '.items[]' queue.json | while read -r item; do
  sqlite3 queue.db "insert into queue values ('$item')"
done
`)
	previous := filepath.Join(root, "docs", "strategy", "unification-cycles", "cycle-previous.md")
	writeUnificationFile(t, root, "docs/strategy/unification-cycles/cycle-previous.md", `# Previous

## Improvement Notes For Next Cycle

- [ ] Add wrapper smoke tests before routing more shell entrypoints.
`)

	cycle, err := BuildCycle(root, CycleOptions{
		GeneratedAt:   "2026-05-10T00:00:00Z",
		CycleID:       "cycle-test",
		PreviousNotes: previous,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cycle.CycleID != "cycle-test" {
		t.Fatalf("CycleID = %q", cycle.CycleID)
	}
	if len(cycle.CarryForward) != 1 {
		t.Fatalf("CarryForward = %+v, want one note", cycle.CarryForward)
	}
	if !strings.Contains(cycle.Markdown(), "Add wrapper smoke tests") {
		t.Fatalf("cycle markdown did not include carry-forward note:\n%s", cycle.Markdown())
	}
	if !strings.Contains(cycle.Markdown(), "Roadmaps:") {
		t.Fatalf("cycle markdown did not include roadmap summary:\n%s", cycle.Markdown())
	}
	if !strings.Contains(cycle.Markdown(), "Baseline targets:") {
		t.Fatalf("cycle markdown did not include baseline summary:\n%s", cycle.Markdown())
	}
	if !strings.Contains(cycle.Markdown(), "Baseline queue:") || !strings.Contains(cycle.Markdown(), "previous queue unavailable") {
		t.Fatalf("cycle markdown did not include baseline queue delta:\n%s", cycle.Markdown())
	}
	if cycle.ShellCandidate == nil || cycle.ShellCandidate.Repo != "app" || cycle.ShellCandidate.Path != "scripts/queue-sync.sh" {
		t.Fatalf("shell candidate = %+v, want app scripts/queue-sync.sh", cycle.ShellCandidate)
	}
	if !strings.Contains(cycle.Markdown(), "Shell migration candidate: `app/scripts/queue-sync.sh`") {
		t.Fatalf("cycle markdown did not include shell migration candidate:\n%s", cycle.Markdown())
	}
	path, err := cycle.WriteMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "cycle-test.md" {
		t.Fatalf("path = %q", path)
	}
}

func TestBuildCycleRequireNotesAppliedFailsOnCarryForward(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	previous := filepath.Join(root, "docs", "strategy", "unification-cycles", "cycle-previous.md")
	writeUnificationFile(t, root, "docs/strategy/unification-cycles/cycle-previous.md", `# Previous

## Improvement Notes For Next Cycle

- [x] Already applied guard.
- [ ] Add a guard before the next cycle.
`)

	_, err := BuildCycle(root, CycleOptions{
		GeneratedAt:         "2026-05-10T00:00:00Z",
		PreviousNotes:       previous,
		RequireNotesApplied: true,
	})
	if err == nil {
		t.Fatal("expected carry-forward guard error")
	}

	cycle, err := BuildCycle(root, CycleOptions{
		GeneratedAt:                 "2026-05-10T00:00:00Z",
		PreviousNotes:               previous,
		RequireNotesApplied:         true,
		CarryForwardAcknowledgement: "guard covered by this patch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cycle.CarryForwardAcknowledgement == "" {
		t.Fatal("expected acknowledgement to be recorded")
	}
}

func TestBuildCycleReportsBaselineQueueDelta(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "app/scripts/run.sh", "#!/usr/bin/env bash\necho hi\n")
	previous := filepath.Join(root, "docs", "strategy", "unification-cycles", "cycle-previous.md")
	writeUnificationFile(t, root, "docs/strategy/unification-cycles/cycle-previous.md", `# Previous

## Live Audit Snapshot

- Baseline queue: `+"`app`, `old`"+`

## Improvement Notes For Next Cycle

- [x] Already handled.
`)

	cycle, err := BuildCycle(root, CycleOptions{
		GeneratedAt:   "2026-05-10T00:00:00Z",
		PreviousNotes: previous,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cycle.BaselineQueue.PreviousAvailable {
		t.Fatal("expected previous baseline queue to be parsed")
	}
	if strings.Join(cycle.BaselineQueue.Current, ",") != "app" {
		t.Fatalf("current queue = %+v, want app", cycle.BaselineQueue.Current)
	}
	if strings.Join(cycle.BaselineQueue.Left, ",") != "old" {
		t.Fatalf("left queue = %+v, want old", cycle.BaselineQueue.Left)
	}
	if strings.Join(cycle.BaselineQueue.Unchanged, ",") != "app" {
		t.Fatalf("unchanged queue = %+v, want app", cycle.BaselineQueue.Unchanged)
	}
	markdown := cycle.Markdown()
	if !strings.Contains(markdown, "Baseline queue delta: entered `none`; left `old`; unchanged `app`") {
		t.Fatalf("cycle markdown missing baseline queue delta:\n%s", markdown)
	}
}

func TestBuildCycleReportsFirstBaselineRemediationCommand(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "app/.agents/skills/surface.yaml", `version: 1
skills:
  - name: demo
`)
	writeUnificationFile(t, root, "app/.agents/skills/demo/SKILL.md", "---\nname: demo\ndescription: demo skill\n---\n# Demo\n")

	cycle, err := BuildCycle(root, CycleOptions{
		GeneratedAt: "2026-05-10T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cycle.BaselineQueue.FirstRemediationCommand, "skills sync") {
		t.Fatalf("first remediation command = %q, want skills sync command", cycle.BaselineQueue.FirstRemediationCommand)
	}
	markdown := cycle.Markdown()
	if !strings.Contains(markdown, "First baseline remediation command:") || !strings.Contains(markdown, "skills sync") {
		t.Fatalf("cycle markdown missing first baseline remediation command:\n%s", markdown)
	}
}

func TestBuildCycleRequireNotesAppliedDiscoversLatestNote(t *testing.T) {
	root := t.TempDir()
	writeUnificationFile(t, root, "workspace/manifest.json", `{
  "version": 1,
  "repos": [
    {"name": "app", "category": "application", "scope": "active_first_party", "language": "Go", "baseline_target": true, "go_work_member": false, "lifecycle": "active"}
  ]
}`)
	writeUnificationFile(t, root, "docs/strategy/unification-cycles/cycle-20260509-01.md", `# Older

## Improvement Notes For Next Cycle

- [x] Already handled.
`)
	writeUnificationFile(t, root, "docs/strategy/unification-cycles/cycle-20260510-01.md", `# Latest

## Improvement Notes For Next Cycle

- [ ] Add the docs front door before the next cycle.
`)

	_, err := BuildCycle(root, CycleOptions{
		GeneratedAt:         "2026-05-10T00:00:00Z",
		RequireNotesApplied: true,
	})
	if err == nil {
		t.Fatal("expected auto-discovered carry-forward guard error")
	}
	if !strings.Contains(err.Error(), "previous cycle has 1 unchecked improvement note") {
		t.Fatalf("unexpected error: %v", err)
	}

	cycle, err := BuildCycle(root, CycleOptions{
		GeneratedAt:                 "2026-05-10T00:00:00Z",
		RequireNotesApplied:         true,
		CarryForwardAcknowledgement: "documented blocker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cycle.PreviousNotePath, filepath.Join("docs", "strategy", "unification-cycles", "cycle-20260510-01.md")) {
		t.Fatalf("PreviousNotePath = %q", cycle.PreviousNotePath)
	}
}

func hasFinding(repo RepoReport, category string) bool {
	for _, finding := range repo.Findings {
		if finding.Category == category {
			return true
		}
	}
	return false
}

func writeUnificationFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
