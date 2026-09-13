package agentorchestration

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateCompletion checks completion evidence against the original task and
// routed lane. It validates caller-supplied proof but does not execute checks.
func ValidateCompletion(task TaskPacket, routedLane ModelLane, evidence CompletionEvidence) CompletionValidation {
	var issues []EvidenceIssue
	if evidence.SchemaVersion != SchemaVersion {
		issues = append(issues, EvidenceIssue{Code: "invalid_schema", Message: "unsupported completion evidence schema"})
	}
	if evidence.TaskID != task.ID {
		issues = append(issues, EvidenceIssue{Code: "task_mismatch", Message: "completion evidence task id does not match"})
	}
	if evidence.Lane != routedLane {
		issues = append(issues, EvidenceIssue{Code: "lane_mismatch", Message: "completion evidence lane does not match routing decision"})
	}
	if evidence.CompletedAt.IsZero() {
		issues = append(issues, EvidenceIssue{Code: "missing_completed_at", Message: "completion time is required"})
	}
	if strings.TrimSpace(evidence.Summary) == "" {
		issues = append(issues, EvidenceIssue{Code: "missing_summary", Message: "completion summary is required"})
	}

	criteria := make(map[string]CriterionEvidence, len(evidence.Criteria))
	for _, criterion := range evidence.Criteria {
		key := strings.TrimSpace(criterion.Criterion)
		if key == "" {
			issues = append(issues, EvidenceIssue{Code: "invalid_criterion", Message: "criterion evidence has an empty criterion"})
			continue
		}
		if _, exists := criteria[key]; exists {
			issues = append(issues, EvidenceIssue{Code: "duplicate_criterion", Message: "duplicate criterion evidence: " + key})
			continue
		}
		criteria[key] = criterion
	}
	for _, required := range task.DoneWhen {
		required = strings.TrimSpace(required)
		proof, exists := criteria[required]
		switch {
		case !exists:
			issues = append(issues, EvidenceIssue{Code: "missing_criterion", Message: "missing criterion evidence: " + required})
		case !proof.Satisfied:
			issues = append(issues, EvidenceIssue{Code: "failed_criterion", Message: "criterion is not satisfied: " + required})
		case strings.TrimSpace(proof.Summary) == "":
			issues = append(issues, EvidenceIssue{Code: "missing_criterion_summary", Message: "criterion evidence summary is empty: " + required})
		}
	}

	verifications := make(map[string]VerificationEvidence, len(evidence.Verifications))
	for _, verification := range evidence.Verifications {
		id := strings.TrimSpace(verification.RequirementID)
		if id == "" {
			issues = append(issues, EvidenceIssue{Code: "invalid_verification", Message: "verification evidence has an empty requirement id"})
			continue
		}
		if _, exists := verifications[id]; exists {
			issues = append(issues, EvidenceIssue{Code: "duplicate_verification", Message: "duplicate verification evidence: " + id})
			continue
		}
		verifications[id] = verification
	}
	for _, requirement := range task.VerificationRequirements {
		if !requirement.Required {
			continue
		}
		proof, exists := verifications[requirement.ID]
		switch {
		case !exists:
			issues = append(issues, EvidenceIssue{Code: "missing_verification", Message: "missing required verification: " + requirement.ID})
		case proof.Lane != requirement.Lane:
			issues = append(issues, EvidenceIssue{Code: "verification_lane_mismatch", Message: "verification lane does not match requirement: " + requirement.ID})
		case !proof.Passed:
			issues = append(issues, EvidenceIssue{Code: "failed_verification", Message: "required verification failed: " + requirement.ID})
		case strings.TrimSpace(proof.Summary) == "":
			issues = append(issues, EvidenceIssue{Code: "missing_verification_summary", Message: "verification summary is empty: " + requirement.ID})
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Code == issues[j].Code {
			return issues[i].Message < issues[j].Message
		}
		return issues[i].Code < issues[j].Code
	})
	return CompletionValidation{Complete: len(issues) == 0, Issues: issues}
}

// CompletionError turns a failed validation into an error for callers that use
// error-oriented control flow.
func (v CompletionValidation) CompletionError() error {
	if v.Complete {
		return nil
	}
	return fmt.Errorf("completion evidence has %d issue(s)", len(v.Issues))
}
