package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Finding captures one workspace validation result.
type Finding struct {
	Check   string `json:"check"`
	Repo    string `json:"repo,omitempty"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Report captures the full workspace validation result.
type Report struct {
	Root     string    `json:"root"`
	Passed   bool      `json:"passed"`
	Findings []Finding `json:"findings"`
}

func (r *Report) add(check, repo string, passed bool, message string) {
	r.Findings = append(r.Findings, Finding{
		Check:   check,
		Repo:    repo,
		Passed:  passed,
		Message: message,
	})
}

// Check validates manifest-backed workspace organization and go.work drift.
func Check(root string, manifest Manifest) Report {
	report := Report{Root: root}

	expectedGoWork := make(map[string]Repo)
	manifestRepos := make(map[string]Repo, len(manifest.Repos))
	matrix, matrixStatus := loadConsolidationMatrixForCheck(root)

	for _, repo := range manifest.Repos {
		manifestRepos[repo.Name] = repo
		repoPath := filepath.Join(root, repo.Name)
		if _, err := os.Stat(repoPath); err != nil {
			if repoDirectoryRequired(repo, matrixStatus.decisions[repo.Name]) {
				report.add("repo_directory", repo.Name, false, "missing directory")
			} else {
				report.add("repo_directory", repo.Name, true, "optional archived compatibility clone absent")
			}
			continue
		}
		report.add("repo_directory", repo.Name, true, "")
		if repo.GoWorkMember {
			expectedGoWork[repo.Name] = repo
			if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err != nil {
				report.add("go_module", repo.Name, false, "go.work member is missing go.mod")
			} else {
				report.add("go_module", repo.Name, true, "")
			}
		}
	}

	actualMembers, err := ParseGoWorkModules(filepath.Join(root, "go.work"))
	if err != nil {
		report.add("go_work_parse", "", false, err.Error())
		report.Passed = false
		return report
	}
	report.add("go_work_parse", "", true, fmt.Sprintf("%d members", len(actualMembers)))

	actualSet := make(map[string]struct{}, len(actualMembers))
	for _, member := range actualMembers {
		repoName := strings.TrimPrefix(member, "./")
		actualSet[repoName] = struct{}{}
	}

	for repoName := range expectedGoWork {
		if _, ok := actualSet[repoName]; !ok {
			report.add("go_work_member", repoName, false, "missing from go.work")
		} else {
			report.add("go_work_member", repoName, true, "")
		}
	}

	for _, member := range actualMembers {
		repoName := strings.TrimPrefix(member, "./")
		if _, ok := expectedGoWork[repoName]; ok {
			continue
		}
		report.add("go_work_member", repoName, false, "present in go.work but not marked go_work_member in workspace manifest")
	}

	report.add("consolidation_matrix", "", matrixStatus.passed, matrixStatus.message)
	if matrixStatus.passed && matrixStatus.loaded {
		for _, decision := range matrix.Decisions {
			repo, ok := manifestRepos[decision.Repo]
			if !ok {
				report.add("consolidation_manifest", decision.Repo, false, "repo is missing from workspace manifest")
				continue
			}
			if decision.State == "merged_out_of_active_surface" {
				if repo.Scope != "compatibility_only" {
					report.add("consolidation_scope", decision.Repo, false, fmt.Sprintf("repo marked %q in consolidation matrix should have compatibility_only scope", decision.State))
				} else {
					report.add("consolidation_scope", decision.Repo, true, "")
				}
				if repo.GoWorkMember {
					report.add("consolidation_go_work_member", decision.Repo, false, fmt.Sprintf("repo marked %q in consolidation matrix must not remain in go.work", decision.State))
				} else {
					report.add("consolidation_go_work_member", decision.Repo, true, "")
				}
				if repo.BaselineTarget {
					report.add("consolidation_baseline_target", decision.Repo, false, fmt.Sprintf("repo marked %q in consolidation matrix must not remain a baseline target", decision.State))
				} else {
					report.add("consolidation_baseline_target", decision.Repo, true, "")
				}
			}
			if decision.WorkspaceScope != "" {
				if repo.Scope != decision.WorkspaceScope {
					report.add("consolidation_scope_override", decision.Repo, false, fmt.Sprintf("workspace manifest scope is %q, want %q per consolidation matrix", repo.Scope, decision.WorkspaceScope))
				} else {
					report.add("consolidation_scope_override", decision.Repo, true, "")
				}
			}
			if decision.GoWorkMember != nil {
				if repo.GoWorkMember != *decision.GoWorkMember {
					report.add("consolidation_go_work_member_override", decision.Repo, false, fmt.Sprintf("workspace manifest go_work_member is %t, want %t per consolidation matrix", repo.GoWorkMember, *decision.GoWorkMember))
				} else {
					report.add("consolidation_go_work_member_override", decision.Repo, true, "")
				}
			}
			if decision.BaselineTarget != nil {
				if repo.BaselineTarget != *decision.BaselineTarget {
					report.add("consolidation_baseline_target_override", decision.Repo, false, fmt.Sprintf("workspace manifest baseline_target is %t, want %t per consolidation matrix", repo.BaselineTarget, *decision.BaselineTarget))
				} else {
					report.add("consolidation_baseline_target_override", decision.Repo, true, "")
				}
			}
		}
	}

	report.Passed = true
	for _, finding := range report.Findings {
		if !finding.Passed {
			report.Passed = false
			break
		}
	}
	return report
}

type consolidationMatrixStatus struct {
	decisions map[string]ConsolidationDecision
	loaded    bool
	passed    bool
	message   string
}

func loadConsolidationMatrixForCheck(root string) (ConsolidationMatrix, consolidationMatrixStatus) {
	matrix, err := LoadConsolidationMatrix(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ConsolidationMatrix{}, consolidationMatrixStatus{
			decisions: map[string]ConsolidationDecision{},
			passed:    true,
			message:   "not found; skipped",
		}
	case err != nil:
		return ConsolidationMatrix{}, consolidationMatrixStatus{
			decisions: map[string]ConsolidationDecision{},
			passed:    false,
			message:   err.Error(),
		}
	default:
		decisions := make(map[string]ConsolidationDecision, len(matrix.Decisions))
		for _, decision := range matrix.Decisions {
			decisions[decision.Repo] = decision
		}
		return matrix, consolidationMatrixStatus{
			decisions: decisions,
			loaded:    true,
			passed:    true,
			message:   fmt.Sprintf("%d decisions", len(matrix.Decisions)),
		}
	}
}

func repoDirectoryRequired(repo Repo, decision ConsolidationDecision) bool {
	if repo.Scope != "compatibility_only" {
		return true
	}
	if !decision.ArchiveCandidate {
		return true
	}
	switch decision.State {
	case "merged_into", "merged_out_of_active_surface":
		return false
	default:
		return true
	}
}

// ParseGoWorkModules returns relative module paths listed in a go.work use block.
func ParseGoWorkModules(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	members := make([]string, 0, 16)
	inUseBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "use ("):
			inUseBlock = true
		case inUseBlock && line == ")":
			inUseBlock = false
		case inUseBlock:
			members = append(members, strings.Trim(line, `"`))
		case strings.HasPrefix(line, "use "):
			member := strings.TrimSpace(strings.TrimPrefix(line, "use "))
			members = append(members, strings.Trim(member, `"`))
		}
	}
	slices.Sort(members)
	return slices.Compact(members), nil
}
