package agyloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hairglasses-studio/codexkit/baselineguard"
	"github.com/hairglasses-studio/codexkit/reporeadiness"
)

type FleetAutopilotOptions struct {
	WorkspaceRoot   string `json:"workspace_root"`
	Concurrency     int    `json:"concurrency"`
	MaxIterations   int    `json:"max_iterations"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Prompt          string `json:"prompt"`
	AutoCommit      bool   `json:"auto_commit"`
	AutoPush        bool   `json:"auto_push"`
	DryRun          bool   `json:"dry_run"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	IncludeLanes    string `json:"include_lanes"`
}

type RepoAutopilotResult struct {
	RepoName         string             `json:"repo_name"`
	RepoPath         string             `json:"repo_path"`
	ReadinessLane    string             `json:"readiness_lane"`
	ReadinessScore   int                `json:"readiness_score"`
	Success          bool               `json:"success"`
	IterationsRun    int                `json:"iterations_run"`
	SuccessfulRounds int                `json:"successful_rounds"`
	Receipts         []IterationReceipt `json:"receipts,omitempty"`
	PushedToRemote   bool               `json:"pushed_to_remote"`
	ErrorReason      string             `json:"error_reason,omitempty"`
	DurationSeconds  float64            `json:"duration_seconds"`
}

type FleetAutopilotReport struct {
	Timestamp          string                `json:"timestamp"`
	WorkspaceRoot      string                `json:"workspace_root"`
	Model              string                `json:"model"`
	TotalReposScanned  int                   `json:"total_repos_scanned"`
	EligibleRepos      int                   `json:"eligible_repos"`
	SuccessfulRepos    int                   `json:"successful_repos"`
	FailedRepos        int                   `json:"failed_repos"`
	Results            []RepoAutopilotResult `json:"results"`
	CircuitBreakerOpen bool                  `json:"circuit_breaker_open"`
	TotalDurationSec   float64               `json:"total_duration_sec"`
	MasterAttestation  string                `json:"master_attestation"`
}

func (opts *FleetAutopilotOptions) normalize() {
	if opts.WorkspaceRoot == "" {
		opts.WorkspaceRoot = "."
	}
	abs, err := filepath.Abs(opts.WorkspaceRoot)
	if err == nil {
		opts.WorkspaceRoot = abs
	}

	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Concurrency > 8 {
		opts.Concurrency = 8
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 3
	}
	if opts.Model == "" {
		opts.Model = "gemini-3.7-flash-high"
	}
	if opts.ReasoningEffort == "" {
		opts.ReasoningEffort = "high"
	}
	if opts.TimeoutSeconds <= 0 {
		opts.TimeoutSeconds = 300
	}
	if opts.Prompt == "" {
		opts.Prompt = "Execute codebase unification: enforce gofmt, modernize MCP schemas, resolve baseline guard findings, and verify clean test suites."
	}
}

func RunFleetAutopilot(ctx context.Context, opts FleetAutopilotOptions) (*FleetAutopilotReport, error) {
	opts.normalize()
	start := time.Now()

	discovered, err := baselineguard.DiscoverWorkspaceTargets(opts.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("discover baseline targets: %w", err)
	}

	readinessMap := make(map[string]reporeadiness.RepoScore)
	if readinessReport, err := reporeadiness.Score(opts.WorkspaceRoot, reporeadiness.Options{
		WorkspaceRoot: opts.WorkspaceRoot,
		AllScopes:     true,
	}); err == nil {
		for _, r := range readinessReport.Repos {
			readinessMap[r.RepoName] = r
			readinessMap[r.RepoPath] = r
		}
	}

	type targetCandidate struct {
		Path  string
		Name  string
		Lane  string
		Score int
	}

	var candidates []targetCandidate
	for _, p := range discovered {
		name := filepath.Base(p)
		lane := reporeadiness.LaneFullAutoCandidate
		scoreVal := 100

		if score, ok := readinessMap[name]; ok {
			lane = score.Lane
			scoreVal = score.Score
		} else if score, ok := readinessMap[p]; ok {
			lane = score.Lane
			scoreVal = score.Score
		}

		if opts.IncludeLanes != "" {
			if !strings.Contains(opts.IncludeLanes, lane) {
				continue
			}
		} else {
			if lane == reporeadiness.LaneReviewOnly {
				continue
			}
		}

		candidates = append(candidates, targetCandidate{
			Path:  p,
			Name:  name,
			Lane:  lane,
			Score: scoreVal,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Name < candidates[j].Name
	})

	report := &FleetAutopilotReport{
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot:     opts.WorkspaceRoot,
		Model:             opts.Model,
		TotalReposScanned: len(discovered),
		EligibleRepos:     len(candidates),
		Results:           make([]RepoAutopilotResult, 0, len(candidates)),
	}

	semaphore := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	consecutiveErrors := 0
	var circuitBreakerTripped bool

	for _, cand := range candidates {
		mu.Lock()
		if circuitBreakerTripped {
			mu.Unlock()
			break
		}
		mu.Unlock()

		semaphore <- struct{}{}
		wg.Add(1)

		go func(c targetCandidate) {
			defer func() {
				<-semaphore
				wg.Done()
			}()

			rStart := time.Now()
			loopOpts := LoopOptions{
				RepoPath:        c.Path,
				Model:           opts.Model,
				ReasoningEffort: opts.ReasoningEffort,
				MaxIterations:   opts.MaxIterations,
				Prompt:          opts.Prompt,
				AutoCommit:      opts.AutoCommit,
				AutoPush:        opts.AutoPush,
				DryRun:          opts.DryRun,
				TimeoutSeconds:  opts.TimeoutSeconds,
				StopOnClean:     true,
			}

			subReport, runErr := RunLoop(ctx, loopOpts)

			res := RepoAutopilotResult{
				RepoName:        c.Name,
				RepoPath:        c.Path,
				ReadinessLane:   c.Lane,
				ReadinessScore:  c.Score,
				DurationSeconds: time.Since(rStart).Seconds(),
			}

			if subReport != nil {
				res.IterationsRun = subReport.TotalIterations
				res.SuccessfulRounds = subReport.SuccessfulRounds
				res.PushedToRemote = subReport.PushedToRemote
				res.Receipts = subReport.Receipts
				res.Success = (subReport.SuccessfulRounds > 0 && subReport.Status != "circuit_breaker_open")
				if subReport.Status == "circuit_breaker_open" {
					res.ErrorReason = "circuit breaker opened during repository loop"
				}
			} else if runErr != nil {
				res.ErrorReason = runErr.Error()
				res.Success = false
			}

			mu.Lock()
			report.Results = append(report.Results, res)
			if res.Success {
				report.SuccessfulRepos++
				consecutiveErrors = 0
			} else {
				report.FailedRepos++
				consecutiveErrors++
				if consecutiveErrors >= 3 {
					circuitBreakerTripped = true
					report.CircuitBreakerOpen = true
				}
			}
			mu.Unlock()
		}(cand)
	}

	wg.Wait()

	sort.Slice(report.Results, func(i, j int) bool {
		return report.Results[i].RepoName < report.Results[j].RepoName
	})

	report.TotalDurationSec = time.Since(start).Seconds()
	report.MasterAttestation = SignReceipt(opts.WorkspaceRoot, "fleet", "HEAD", opts.Prompt, len(report.Results))

	writeFleetAutopilotState(opts.WorkspaceRoot, report)

	return report, nil
}

func writeFleetAutopilotState(root string, report *FleetAutopilotReport) {
	ralphDir := filepath.Join(root, ".ralph")
	_ = os.MkdirAll(ralphDir, 0755)

	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(ralphDir, "fleet_autopilot_status.json"), jsonData, 0644)
	}

	var md strings.Builder
	md.WriteString(fmt.Sprintf("# Fleet Autopilot Report (%s)\n\n", report.Timestamp))
	md.WriteString(fmt.Sprintf("- **Model**: `%s`\n", report.Model))
	md.WriteString(fmt.Sprintf("- **Total Repos Scanned**: `%d`\n", report.TotalReposScanned))
	md.WriteString(fmt.Sprintf("- **Eligible Repos**: `%d`\n", report.EligibleRepos))
	md.WriteString(fmt.Sprintf("- **Successful Repos**: `%d`\n", report.SuccessfulRepos))
	md.WriteString(fmt.Sprintf("- **Failed Repos**: `%d`\n", report.FailedRepos))
	md.WriteString(fmt.Sprintf("- **Total Duration**: `%.1fs`\n", report.TotalDurationSec))
	md.WriteString(fmt.Sprintf("- **Master HMAC Attestation**: `%s`\n\n", report.MasterAttestation))

	md.WriteString("| Repository | Readiness Lane | Score | Status | Rounds | Duration |\n")
	md.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, res := range report.Results {
		status := "PASS"
		if !res.Success {
			status = "FAIL"
		}
		md.WriteString(fmt.Sprintf("| `%s` | `%s` | `%d` | **%s** | %d/%d | %.1fs |\n",
			res.RepoName, res.ReadinessLane, res.ReadinessScore, status, res.SuccessfulRounds, res.IterationsRun, res.DurationSeconds))
	}

	_ = os.WriteFile(filepath.Join(ralphDir, "fleet_autopilot_report.md"), []byte(md.String()), 0644)
}
