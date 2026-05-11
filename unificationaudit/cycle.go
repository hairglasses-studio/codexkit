package unificationaudit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/reporeadiness"
)

const cycleGeneratedBy = "codexkit unification cycle"

// CycleOptions controls the repeatable unification loop planning surface.
type CycleOptions struct {
	AllScopes                   bool
	GeneratedAt                 string
	CycleID                     string
	NotesDir                    string
	PreviousNotes               string
	RequireNotesApplied         bool
	CarryForwardAcknowledgement string
}

// CycleReport is a per-cycle plan generated from the live unification audit
// and the previous cycle's improvement notes.
type CycleReport struct {
	GeneratedAt                 string             `json:"generated_at"`
	GeneratedBy                 string             `json:"generated_by"`
	WorkspaceRoot               string             `json:"workspace_root"`
	CycleID                     string             `json:"cycle_id"`
	Scope                       string             `json:"scope"`
	NotesDir                    string             `json:"notes_dir"`
	PreviousNotePath            string             `json:"previous_note_path,omitempty"`
	CarryForward                []ImprovementNote  `json:"carry_forward,omitempty"`
	CarryForwardAcknowledgement string             `json:"carry_forward_acknowledgement,omitempty"`
	Summary                     Summary            `json:"summary"`
	BaselineQueue               BaselineQueueDelta `json:"baseline_queue"`
	ShellCandidate              *ShellCandidate    `json:"shell_candidate,omitempty"`
	TopRepos                    []RepoPriority     `json:"top_repos,omitempty"`
	Recommendations             []Recommendation   `json:"recommendations,omitempty"`
	Commands                    []string           `json:"commands"`
	Checklist                   []string           `json:"checklist"`
	Warnings                    []string           `json:"warnings,omitempty"`
	Audit                       *Report            `json:"audit,omitempty"`
}

// ImprovementNote is one carry-forward note extracted from the previous cycle.
type ImprovementNote struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// BaselineQueueDelta compares the current failing baseline_target repos with
// the previous cycle note when that note recorded a baseline queue.
type BaselineQueueDelta struct {
	Current                 []string `json:"current,omitempty"`
	Entered                 []string `json:"entered,omitempty"`
	Left                    []string `json:"left,omitempty"`
	Unchanged               []string `json:"unchanged,omitempty"`
	FirstRemediationCommand string   `json:"first_remediation_command,omitempty"`
	PreviousAvailable       bool     `json:"previous_available"`
}

// ShellCandidate is the next concrete shell migration file suggested by the
// live audit.
type ShellCandidate struct {
	Repo  string `json:"repo"`
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	Role  string `json:"role"`
}

// BuildCycle creates a repeatable unification cycle plan.
func BuildCycle(root string, opts CycleOptions) (CycleReport, error) {
	audit, err := Audit(root, Options{AllScopes: opts.AllScopes, GeneratedAt: opts.GeneratedAt})
	if err != nil {
		return CycleReport{}, err
	}
	generatedAt := opts.GeneratedAt
	if generatedAt == "" {
		generatedAt = audit.GeneratedAt
	}
	cycleID := opts.CycleID
	if cycleID == "" {
		cycleID = cycleIDFromTime(generatedAt)
	}
	notesDir := opts.NotesDir
	if notesDir == "" {
		notesDir = filepath.Join(audit.WorkspaceRoot, "docs", "strategy", "unification-cycles")
	}

	previous := opts.PreviousNotes
	if previous == "" {
		previous = latestCycleNote(notesDir)
	}
	carryForward := extractImprovementNotes(previous)
	if opts.RequireNotesApplied && len(carryForward) > 0 && strings.TrimSpace(opts.CarryForwardAcknowledgement) == "" {
		return CycleReport{}, fmt.Errorf("previous cycle has %d unchecked improvement note(s); apply them before continuing or pass --ack-carry-forward with a reason", len(carryForward))
	}

	topRepos := audit.Summary.TopRepos
	if len(topRepos) > 5 {
		topRepos = topRepos[:5]
	}
	baselineQueue := buildBaselineQueueDelta(audit, previous)
	shellCandidate := topShellMigrationCandidate(audit)

	return CycleReport{
		GeneratedAt:                 generatedAt,
		GeneratedBy:                 cycleGeneratedBy,
		WorkspaceRoot:               audit.WorkspaceRoot,
		CycleID:                     cycleID,
		Scope:                       audit.Summary.Scope,
		NotesDir:                    notesDir,
		PreviousNotePath:            previous,
		CarryForward:                carryForward,
		CarryForwardAcknowledgement: strings.TrimSpace(opts.CarryForwardAcknowledgement),
		Summary:                     audit.Summary,
		BaselineQueue:               baselineQueue,
		ShellCandidate:              shellCandidate,
		TopRepos:                    topRepos,
		Recommendations:             audit.Recommendations,
		Commands:                    defaultCycleCommands(audit.WorkspaceRoot),
		Checklist:                   defaultCycleChecklist(),
		Warnings:                    audit.Warnings,
		Audit:                       &audit,
	}, nil
}

// WriteMarkdown writes the cycle note and returns its path.
func (c CycleReport) WriteMarkdown() (string, error) {
	if c.NotesDir == "" {
		return "", fmt.Errorf("notes dir is required")
	}
	if c.CycleID == "" {
		return "", fmt.Errorf("cycle id is required")
	}
	if err := os.MkdirAll(c.NotesDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(c.NotesDir, c.CycleID+".md")
	if err := os.WriteFile(path, []byte(c.Markdown()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Markdown renders a cycle note that can be committed as the durable record.
func (c CycleReport) Markdown() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Codebase Unification Cycle: %s\n\n", c.CycleID)
	fmt.Fprintf(&b, "Generated: %s\n\n", c.GeneratedAt)
	fmt.Fprintf(&b, "Source: `%s`\n\n", c.GeneratedBy)
	fmt.Fprintf(&b, "Workspace: `%s`\n\n", c.WorkspaceRoot)

	fmt.Fprintf(&b, "## Pre-Cycle Improvement Gate\n\n")
	if c.PreviousNotePath == "" {
		fmt.Fprintf(&b, "- No previous cycle note found.\n")
	} else {
		fmt.Fprintf(&b, "- Previous note: `%s`\n", c.PreviousNotePath)
	}
	if len(c.CarryForward) == 0 {
		fmt.Fprintf(&b, "- No unchecked improvement notes carried forward.\n")
	} else {
		fmt.Fprintf(&b, "- Apply these before migration work in this cycle:\n")
		for _, note := range c.CarryForward {
			fmt.Fprintf(&b, "  - [ ] %s", mdEscape(note.Text))
			if note.Source != "" {
				fmt.Fprintf(&b, " (`%s:%d`)", mdEscape(note.Source), note.Line)
			}
			fmt.Fprintf(&b, "\n")
		}
		if c.CarryForwardAcknowledgement != "" {
			fmt.Fprintf(&b, "- Acknowledgement: %s\n", mdEscape(c.CarryForwardAcknowledgement))
		}
	}

	fmt.Fprintf(&b, "\n## Live Audit Snapshot\n\n")
	fmt.Fprintf(&b, "- Scope: `%s`\n", c.Scope)
	fmt.Fprintf(&b, "- Repos scanned: `%d`\n", c.Summary.ReposScanned)
	fmt.Fprintf(&b, "- Migratable shell: `%d` files / `%d` LOC\n", c.Summary.MigratableShellFiles, c.Summary.MigratableShellLines)
	fmt.Fprintf(&b, "- Stateful shell: `%d` files / `%d` LOC\n", c.Summary.StatefulShellFiles, c.Summary.StatefulShellLines)
	fmt.Fprintf(&b, "- Hooks: `%d` files / `%d` LOC across `%d` repos\n", c.Summary.HookFiles, c.Summary.HookLines, c.Summary.ReposWithHookFiles)
	fmt.Fprintf(&b, "- Skills: `%d` canonical / `%d` generated / `%d` plugin\n", c.Summary.CanonicalSkillFiles, c.Summary.GeneratedSkillFiles, c.Summary.PluginSkillFiles)
	fmt.Fprintf(&b, "- Roadmaps: `%d` present / `%d` missing / `%d` outside current scan\n", c.Summary.RepoRoadmaps, c.Summary.ReposMissingRoadmap, c.Summary.ExtraRoadmaps)
	fmt.Fprintf(&b, "- Baseline targets: `%d` checked / `%d` passing / `%d` failing\n", c.Summary.BaselineCheckedRepos, c.Summary.BaselinePassingRepos, c.Summary.BaselineFailingRepos)
	fmt.Fprintf(&b, "- Baseline queue: %s\n", mdCodeList(c.BaselineQueue.Current))
	if len(c.BaselineQueue.Current) > 0 {
		command := c.BaselineQueue.FirstRemediationCommand
		if command == "" {
			command = "none available"
		}
		fmt.Fprintf(&b, "- First baseline remediation command: `%s`\n", mdEscape(command))
	}
	if len(c.Summary.MutationReadinessLanes) > 0 {
		fmt.Fprintf(&b, "- Mutation readiness: full-auto `%d`, cleanup-first `%d`, human-approved `%d`, review-only `%d`\n",
			c.Summary.MutationReadinessLanes[reporeadiness.LaneFullAutoCandidate],
			c.Summary.MutationReadinessLanes[reporeadiness.LaneCleanupFirst],
			c.Summary.MutationReadinessLanes[reporeadiness.LaneHumanApprovedOnly],
			c.Summary.MutationReadinessLanes[reporeadiness.LaneReviewOnly],
		)
	}
	if c.BaselineQueue.PreviousAvailable {
		fmt.Fprintf(&b, "- Baseline queue delta: entered %s; left %s; unchanged %s\n",
			mdCodeList(c.BaselineQueue.Entered),
			mdCodeList(c.BaselineQueue.Left),
			mdCodeList(c.BaselineQueue.Unchanged),
		)
	} else {
		fmt.Fprintf(&b, "- Baseline queue delta: previous queue unavailable; current %s\n", mdCodeList(c.BaselineQueue.Current))
	}
	if c.ShellCandidate != nil {
		fmt.Fprintf(&b, "- Shell migration candidate: `%s/%s` (`%d` LOC, `%s`)\n",
			mdEscape(c.ShellCandidate.Repo),
			mdEscape(c.ShellCandidate.Path),
			c.ShellCandidate.Lines,
			mdEscape(c.ShellCandidate.Role),
		)
	} else {
		fmt.Fprintf(&b, "- Shell migration candidate: `none`\n")
	}

	if len(c.TopRepos) > 0 {
		fmt.Fprintf(&b, "\n## Current Priority Repos\n\n")
		fmt.Fprintf(&b, "| Repo | Score | Migratable shell LOC | Hooks | Finding |\n")
		fmt.Fprintf(&b, "|---|---:|---:|---:|---|\n")
		for _, repo := range c.TopRepos {
			fmt.Fprintf(&b, "| `%s` | `%d` | `%d` | `%d` | %s |\n",
				mdEscape(repo.Repo), repo.Score, repo.MigratableShellLines, repo.HookFiles, mdEscape(repo.PrimaryFinding))
		}
	}

	if len(c.Recommendations) > 0 {
		fmt.Fprintf(&b, "\n## Pick One Slice\n\n")
		for _, rec := range c.Recommendations {
			target := rec.Area
			if rec.Repo != "" {
				target += " / " + rec.Repo
			}
			fmt.Fprintf(&b, "%d. `%s` `%s`: %s\n", rec.Rank, rec.Severity, mdEscape(target), mdEscape(rec.Message))
		}
	}

	fmt.Fprintf(&b, "\n## Cycle Checklist\n\n")
	for _, item := range c.Checklist {
		fmt.Fprintf(&b, "- [ ] %s\n", item)
	}

	fmt.Fprintf(&b, "\n## Commands\n\n")
	for _, command := range c.Commands {
		fmt.Fprintf(&b, "- `%s`\n", mdEscape(command))
	}

	fmt.Fprintf(&b, "\n## Work Log\n\n")
	fmt.Fprintf(&b, "- Slice selected:\n")
	fmt.Fprintf(&b, "- Files changed:\n")
	fmt.Fprintf(&b, "- Verification:\n")
	fmt.Fprintf(&b, "- Existing failures or residual risk:\n")

	fmt.Fprintf(&b, "\n## Improvement Notes For Next Cycle\n\n")
	fmt.Fprintf(&b, "- [ ] Add one concrete loop/tool/skill/hook improvement learned from this cycle before starting the next migration slice.\n")

	return b.String()
}

func defaultCycleCommands(root string) []string {
	return []string{
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit unification cycle %s --write", root, root),
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit unification cycle %s --require-notes-applied", root, root),
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit unification report %s", root, root),
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit repo-readiness score %s --all-scopes", root, root),
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit workspace primitive-index-check %s --skip-artifacts", root, root),
		fmt.Sprintf("cd %s/codexkit && GOWORK=off go run ./cmd/codexkit workspace source-contract-check %s --skills-only", root, root),
	}
}

func defaultCycleChecklist() []string {
	return []string{
		"Read the previous cycle note and apply carry-forward improvement notes before code migration work.",
		"Run the live unification audit and choose exactly one highest-value slice.",
		"Review codexkit baseline_target status before registering another baseline repo.",
		"Prefer moving behavior behind an existing Go/Python command surface before deleting shell.",
		"Update or create the canonical skill/tool/hook surface if the cycle taught a repeatable pattern.",
		"Run the smallest meaningful repo checks plus the workspace primitive/source-contract checks.",
		"Write the work log and next-cycle improvement notes.",
	}
}

func latestCycleNote(notesDir string) string {
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		return ""
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		paths = append(paths, filepath.Join(notesDir, entry.Name()))
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return ""
	}
	return paths[len(paths)-1]
}

func extractImprovementNotes(path string) []ImprovementNote {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []ImprovementNote{{Text: "Could not read previous cycle note: " + err.Error(), Source: path}}
	}
	lines := strings.Split(string(data), "\n")
	inSection := false
	var notes []ImprovementNote
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inSection = strings.EqualFold(trimmed, "## Improvement Notes For Next Cycle")
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "- [ ] "):
			notes = append(notes, ImprovementNote{
				Text:   strings.TrimSpace(strings.TrimPrefix(trimmed, "- [ ] ")),
				Source: path,
				Line:   idx + 1,
			})
		case strings.HasPrefix(trimmed, "- [x] "), strings.HasPrefix(trimmed, "- [X] "):
			continue
		case strings.HasPrefix(trimmed, "- "):
			notes = append(notes, ImprovementNote{
				Text:   strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")),
				Source: path,
				Line:   idx + 1,
			})
		}
	}
	return notes
}

func buildBaselineQueueDelta(audit Report, previousNotePath string) BaselineQueueDelta {
	current := failingBaselineRepoNames(audit)
	previous, ok := extractBaselineQueue(previousNotePath)
	delta := BaselineQueueDelta{
		Current:                 current,
		FirstRemediationCommand: firstBaselineRemediationCommand(audit),
		PreviousAvailable:       ok,
	}
	if !ok {
		return delta
	}
	previousSet := stringSet(previous)
	currentSet := stringSet(current)
	for _, repo := range current {
		if previousSet[repo] {
			delta.Unchanged = append(delta.Unchanged, repo)
		} else {
			delta.Entered = append(delta.Entered, repo)
		}
	}
	for _, repo := range previous {
		if !currentSet[repo] {
			delta.Left = append(delta.Left, repo)
		}
	}
	sort.Strings(delta.Entered)
	sort.Strings(delta.Left)
	sort.Strings(delta.Unchanged)
	return delta
}

func firstBaselineRemediationCommand(audit Report) string {
	for _, repo := range baselineQueue(audit.Repos, len(audit.Repos)) {
		if repo.Baseline == nil {
			continue
		}
		for _, finding := range repo.Baseline.Findings {
			for _, remediation := range finding.Remediation {
				if len(remediation.Command) == 0 {
					continue
				}
				return baselineRemediationCommand(audit.WorkspaceRoot, repo.RepoPath, remediation.Command)
			}
		}
	}
	return ""
}

func topShellMigrationCandidate(audit Report) *ShellCandidate {
	for _, repo := range audit.Repos {
		if repo.MigratableShellLines == 0 {
			continue
		}
		for _, file := range repo.LargestShellFiles {
			if !isMigratableShellRole(file.PrimaryRole) {
				continue
			}
			return &ShellCandidate{
				Repo:  repo.RepoName,
				Path:  file.Path,
				Lines: file.Lines,
				Role:  file.PrimaryRole,
			}
		}
	}
	return nil
}

func isMigratableShellRole(role string) bool {
	switch role {
	case "stateful_workflow", "structured_parser", "duplicate_implementation":
		return true
	default:
		return false
	}
}

func failingBaselineRepoNames(audit Report) []string {
	queue := baselineQueue(audit.Repos, len(audit.Repos))
	names := make([]string, 0, len(queue))
	for _, repo := range queue {
		names = append(names, repo.RepoName)
	}
	return names
}

func extractBaselineQueue(path string) ([]string, bool) {
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- Baseline queue:") {
			continue
		}
		names := extractBacktickValues(trimmed)
		for _, name := range names {
			if strings.EqualFold(name, "none") {
				return nil, true
			}
		}
		sort.Strings(names)
		return names, true
	}
	return nil, false
}

func extractBacktickValues(line string) []string {
	var values []string
	for {
		start := strings.Index(line, "`")
		if start < 0 {
			break
		}
		line = line[start+1:]
		end := strings.Index(line, "`")
		if end < 0 {
			break
		}
		value := strings.TrimSpace(line[:end])
		if value != "" {
			values = append(values, value)
		}
		line = line[end+1:]
	}
	return values
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func mdCodeList(values []string) string {
	if len(values) == 0 {
		return "`none`"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("`%s`", mdEscape(value)))
	}
	return strings.Join(parts, ", ")
}

func cycleIDFromTime(value string) string {
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return "cycle-" + ts.Format("20060102-150405")
	}
	safe := strings.NewReplacer(":", "", "T", "-", "Z", "", "+", "", "/", "-", " ", "-").Replace(value)
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = time.Now().Format("20060102-150405")
	}
	return "cycle-" + safe
}
