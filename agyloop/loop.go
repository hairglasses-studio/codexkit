package agyloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type LoopOptions struct {
	RepoPath        string `json:"repo_path"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	MaxIterations   int    `json:"max_iterations"`
	Prompt          string `json:"prompt"`
	VerificationCmd string `json:"verification_cmd"`
	AutoCommit      bool   `json:"auto_commit"`
	AutoPush        bool   `json:"auto_push"`
	DryRun          bool   `json:"dry_run"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	StopOnClean     bool   `json:"stop_on_clean"`
	KeepWorktree    bool   `json:"keep_worktree"`
}

type IterationReceipt struct {
	IterationIndex     int      `json:"iteration_index"`
	Timestamp          string   `json:"timestamp"`
	Model              string   `json:"model"`
	Prompt             string   `json:"prompt"`
	GitPreSHA          string   `json:"git_pre_sha"`
	GitPostSHA         string   `json:"git_post_sha"`
	FilesModified      []string `json:"files_modified"`
	VerificationPassed bool     `json:"verification_passed"`
	VerificationOutput string   `json:"verification_output,omitempty"`
	AttestationHMAC    string   `json:"attestation_hmac"`
	ErrorReason        string   `json:"error_reason,omitempty"`
}

type LoopReport struct {
	RepoPath          string             `json:"repo_path"`
	Branch            string             `json:"branch"`
	Model             string             `json:"model"`
	TotalIterations   int                `json:"total_iterations"`
	SuccessfulRounds  int                `json:"successful_rounds"`
	ConsecutiveErrors int                `json:"consecutive_errors"`
	Receipts          []IterationReceipt `json:"receipts"`
	Status            string             `json:"status"`
	PushedToRemote    bool               `json:"pushed_to_remote"`
	DurationSeconds   float64            `json:"duration_seconds"`
}

func FindAGYBin() (string, error) {
	if custom := os.Getenv("AGY_REAL_BIN"); custom != "" {
		if _, err := os.Stat(custom); err == nil {
			return custom, nil
		}
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "agy"),
		filepath.Join(home, ".local", "bin", "agy-real"),
		filepath.Join(home, ".npm-global", "bin", "agy"),
		"/usr/local/bin/agy",
		"/usr/bin/agy",
	}

	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, nil
		}
	}

	if p, err := exec.LookPath("agy"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("antigravity CLI (agy) binary not found on PATH or in standard candidate paths")
}

func (opts *LoopOptions) normalize() {
	if opts.RepoPath == "" {
		opts.RepoPath = "."
	}
	abs, err := filepath.Abs(opts.RepoPath)
	if err == nil {
		opts.RepoPath = abs
	}

	if opts.Model == "" {
		opts.Model = "gemini-3.7-flash-high"
	}
	if opts.ReasoningEffort == "" {
		opts.ReasoningEffort = "high"
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 5
	}
	if opts.TimeoutSeconds <= 0 {
		opts.TimeoutSeconds = 300
	}
}

func getGitBranch(repo string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

func getGitHeadSHA(repo string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "0000000000000000000000000000000000000000"
	}
	return strings.TrimSpace(string(out))
}

func getModifiedFiles(repo string) []string {
	cmd := exec.Command("git", "--no-pager", "status", "--short")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) > 3 {
			files = append(files, strings.TrimSpace(trimmed[3:]))
		}
	}
	return files
}

func detectVerificationCmd(repo string) string {
	if _, err := os.Stat(filepath.Join(repo, "Makefile")); err == nil {
		data, _ := os.ReadFile(filepath.Join(repo, "Makefile"))
		content := string(data)
		if strings.Contains(content, "check-dual:") {
			return "make check-dual"
		}
		if strings.Contains(content, "check-all:") {
			return "make check-all"
		}
		if strings.Contains(content, "check:") {
			return "make check"
		}
		if strings.Contains(content, "ci:") {
			return "make ci"
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err == nil {
		return "GOWORK=off go test ./... < /dev/null"
	}
	return ""
}

func RunLoop(ctx context.Context, opts LoopOptions) (*LoopReport, error) {
	opts.normalize()
	start := time.Now()

	branch := getGitBranch(opts.RepoPath)
	if opts.VerificationCmd == "" {
		opts.VerificationCmd = detectVerificationCmd(opts.RepoPath)
	}

	report := &LoopReport{
		RepoPath: opts.RepoPath,
		Branch:   branch,
		Model:    opts.Model,
		Status:   "in_progress",
		Receipts: make([]IterationReceipt, 0, opts.MaxIterations),
	}

	agyBin, err := FindAGYBin()
	if err != nil && !opts.DryRun {
		report.Status = "errored"
		return report, err
	}

	for iter := 1; iter <= opts.MaxIterations; iter++ {
		preSHA := getGitHeadSHA(opts.RepoPath)
		timestamp := time.Now().UTC().Format(time.RFC3339)

		receipt := IterationReceipt{
			IterationIndex: iter,
			Timestamp:      timestamp,
			Model:          opts.Model,
			Prompt:         opts.Prompt,
			GitPreSHA:      preSHA,
		}

		if opts.DryRun {
			receipt.VerificationPassed = true
			receipt.AttestationHMAC = SignReceipt(opts.RepoPath, branch, preSHA, opts.Prompt, iter)
			report.Receipts = append(report.Receipts, receipt)
			report.SuccessfulRounds++
			continue
		}

		iterCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.TimeoutSeconds)*time.Second)
		promptArg := opts.Prompt
		if promptArg == "" {
			promptArg = "Perform next planned iteration on this repo, ensure code correctness, enforce gofmt and clean builds."
		}

		args := []string{
			"-p", promptArg,
			"--model", opts.Model,
			"--effort", opts.ReasoningEffort,
			"--mode", "accept-edits",
			"--dangerously-skip-permissions",
			"--output-format", "json",
		}

		cmd := exec.CommandContext(iterCtx, agyBin, args...)
		cmd.Dir = opts.RepoPath
		cmd.Stdin = bytes.NewReader([]byte{})
		cmd.Env = append(os.Environ(), "GOWORK=off")

		out, runErr := cmd.CombinedOutput()
		cancel()

		if runErr != nil {
			receipt.ErrorReason = fmt.Sprintf("agy execution error: %v, output: %s", runErr, string(out))
			report.ConsecutiveErrors++
		}

		receipt.FilesModified = getModifiedFiles(opts.RepoPath)

		if opts.VerificationCmd != "" {
			verifyCmd := exec.Command("bash", "-c", opts.VerificationCmd)
			verifyCmd.Dir = opts.RepoPath
			verifyCmd.Stdin = bytes.NewReader([]byte{})
			verifyOut, verifyErr := verifyCmd.CombinedOutput()
			if verifyErr != nil {
				receipt.VerificationPassed = false
				receipt.VerificationOutput = string(verifyOut)
				if receipt.ErrorReason == "" {
					receipt.ErrorReason = fmt.Sprintf("verification failed: %v", verifyErr)
				}
				report.ConsecutiveErrors++
			} else {
				receipt.VerificationPassed = true
				report.ConsecutiveErrors = 0
			}
		} else {
			receipt.VerificationPassed = (runErr == nil)
		}

		if len(receipt.FilesModified) > 0 && receipt.VerificationPassed && opts.AutoCommit {
			addCmd := exec.Command("git", "add", ".")
			addCmd.Dir = opts.RepoPath
			_ = addCmd.Run()

			commitMsg := fmt.Sprintf("chore(loop): iteration %d - autonomous agy loop pass", iter)
			commitCmd := exec.Command("git", "commit", "--author=Mitch <mitch@hairglasses.studio>", "-m", commitMsg)
			cmd.Dir = opts.RepoPath
			_ = commitCmd.Run()
		}

		postSHA := getGitHeadSHA(opts.RepoPath)
		receipt.GitPostSHA = postSHA
		receipt.AttestationHMAC = SignReceipt(opts.RepoPath, branch, postSHA, opts.Prompt, iter)
		report.Receipts = append(report.Receipts, receipt)

		if receipt.VerificationPassed {
			report.SuccessfulRounds++
		}

		if report.ConsecutiveErrors >= 3 {
			report.Status = "circuit_breaker_open"
			break
		}

		if opts.StopOnClean && len(receipt.FilesModified) == 0 && receipt.VerificationPassed {
			report.Status = "converged_clean"
			break
		}
	}

	if opts.AutoPush && report.SuccessfulRounds > 0 && !opts.DryRun {
		pushCmd := exec.Command("git", "push", "origin", branch)
		pushCmd.Dir = opts.RepoPath
		if err := pushCmd.Run(); err == nil {
			report.PushedToRemote = true
		}
	}

	if report.Status == "in_progress" {
		report.Status = "completed"
	}

	report.TotalIterations = len(report.Receipts)
	report.DurationSeconds = time.Since(start).Seconds()

	writeLoopState(opts.RepoPath, report)

	return report, nil
}

func writeLoopState(repo string, report *LoopReport) {
	ralphDir := filepath.Join(repo, ".ralph")
	_ = os.MkdirAll(ralphDir, 0755)

	statusData, err := json.MarshalIndent(map[string]any{
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
		"status":            report.Status,
		"model":             report.Model,
		"total_iterations":  report.TotalIterations,
		"successful_rounds": report.SuccessfulRounds,
		"duration_seconds":  report.DurationSeconds,
		"pushed_to_remote":  report.PushedToRemote,
	}, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(ralphDir, "status.json"), statusData, 0644)
	}

	progressData, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(ralphDir, "progress.json"), progressData, 0644)
	}
}
