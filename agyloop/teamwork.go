package agyloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TeamworkTopology string

const (
	TopologyStar       TeamworkTopology = "star"
	TopologyChain      TeamworkTopology = "chain"
	TopologyBlackboard TeamworkTopology = "blackboard"
)

type TeamworkOptions struct {
	RepoPath        string           `json:"repo_path"`
	Topology        TeamworkTopology `json:"topology"`
	Workers         int              `json:"workers"`
	Prompt          string           `json:"prompt"`
	Scopes          []string         `json:"scopes,omitempty"`
	Model           string           `json:"model"`
	ReasoningEffort string           `json:"reasoning_effort"`
	AutoMerge       bool             `json:"auto_merge"`
	AutoPush        bool             `json:"auto_push"`
	DryRun          bool             `json:"dry_run"`
	TimeoutSeconds  int              `json:"timeout_seconds"`
}

type WorkerResult struct {
	WorkerID        string   `json:"worker_id"`
	Scope           string   `json:"scope"`
	FilesModified   []string `json:"files_modified"`
	Success         bool     `json:"success"`
	AttestationHMAC string   `json:"attestation_hmac"`
	ErrorReason     string   `json:"error_reason,omitempty"`
	DurationSeconds float64  `json:"duration_seconds"`
}

type TeamworkReport struct {
	TeamID            string           `json:"team_id"`
	RepoPath          string           `json:"repo_path"`
	Topology          TeamworkTopology `json:"topology"`
	LeadModel         string           `json:"lead_model"`
	WorkerModel       string           `json:"worker_model"`
	WorkersSpawned    int              `json:"workers_spawned"`
	WorkerResults     []WorkerResult   `json:"worker_results"`
	ConvergencePassed bool             `json:"convergence_passed"`
	MasterReceipt     string           `json:"master_receipt"`
	DurationSeconds   float64          `json:"duration_seconds"`
}

func (opts *TeamworkOptions) normalize() {
	if opts.RepoPath == "" {
		opts.RepoPath = "."
	}
	abs, err := filepath.Abs(opts.RepoPath)
	if err == nil {
		opts.RepoPath = abs
	}

	if opts.Topology == "" {
		opts.Topology = TopologyStar
	}
	if opts.Workers <= 0 {
		opts.Workers = 2
	}
	if opts.Workers > 4 {
		opts.Workers = 4
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
}

func RunTeamwork(ctx context.Context, opts TeamworkOptions) (*TeamworkReport, error) {
	opts.normalize()
	start := time.Now()

	teamID := fmt.Sprintf("team-agy-%d", time.Now().UnixNano()/1e6)
	branch := getGitBranch(opts.RepoPath)
	preSHA := getGitHeadSHA(opts.RepoPath)

	report := &TeamworkReport{
		TeamID:         teamID,
		RepoPath:       opts.RepoPath,
		Topology:       opts.Topology,
		LeadModel:      opts.Model,
		WorkerModel:    opts.Model,
		WorkersSpawned: opts.Workers,
		WorkerResults:  make([]WorkerResult, 0, opts.Workers),
	}

	scopes := opts.Scopes
	if len(scopes) == 0 {
		for i := 0; i < opts.Workers; i++ {
			scopes = append(scopes, fmt.Sprintf("scope-worker-%d", i+1))
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < opts.Workers && i < len(scopes); i++ {
		workerIdx := i + 1
		workerScope := scopes[i]
		wg.Add(1)

		go func(idx int, scope string) {
			defer wg.Done()
			wStart := time.Now()
			wID := fmt.Sprintf("%s-w%d", teamID, idx)

			subOpts := LoopOptions{
				RepoPath:        opts.RepoPath,
				Model:           opts.Model,
				ReasoningEffort: opts.ReasoningEffort,
				MaxIterations:   1,
				Prompt:          fmt.Sprintf("[%s] %s (Scope: %s)", wID, opts.Prompt, scope),
				AutoCommit:      opts.AutoMerge,
				AutoPush:        false,
				DryRun:          opts.DryRun,
				TimeoutSeconds:  opts.TimeoutSeconds,
			}

			subReport, _ := RunLoop(ctx, subOpts)
			success := (subReport != nil && subReport.SuccessfulRounds > 0)

			hmacSig := SignReceipt(opts.RepoPath, branch, preSHA, scope, idx)

			res := WorkerResult{
				WorkerID:        wID,
				Scope:           scope,
				Success:         success,
				AttestationHMAC: hmacSig,
				DurationSeconds: time.Since(wStart).Seconds(),
			}

			if subReport != nil && len(subReport.Receipts) > 0 {
				res.FilesModified = subReport.Receipts[0].FilesModified
				if !success && subReport.Receipts[0].ErrorReason != "" {
					res.ErrorReason = subReport.Receipts[0].ErrorReason
				}
			}

			mu.Lock()
			report.WorkerResults = append(report.WorkerResults, res)
			mu.Unlock()
		}(workerIdx, workerScope)
	}

	wg.Wait()

	allPassed := true
	for _, res := range report.WorkerResults {
		if !res.Success {
			allPassed = false
			break
		}
	}
	report.ConvergencePassed = allPassed
	report.DurationSeconds = time.Since(start).Seconds()
	report.MasterReceipt = SignReceipt(opts.RepoPath, branch, preSHA, opts.Prompt, len(report.WorkerResults))

	if opts.AutoPush && report.ConvergencePassed && !opts.DryRun {
		_ = os.WriteFile(filepath.Join(opts.RepoPath, ".ralph", "teamwork-last.json"), []byte(report.MasterReceipt), 0644)
	}

	return report, nil
}
