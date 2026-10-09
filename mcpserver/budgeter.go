package mcpserver

import (
	"fmt"
	"strings"

	"github.com/hairglasses-studio/codexkit"
)

// BudgetFinding records a tool description token budget violation.
type BudgetFinding struct {
	ToolName    string `json:"tool_name"`
	LengthChars int    `json:"length_chars"`
	Issue       string `json:"issue"`
}

// BudgetReport records registry description token budget statistics.
type BudgetReport struct {
	TotalTools     int             `json:"total_tools"`
	MaxChars       int             `json:"max_chars"`
	AverageChars   float64         `json:"average_chars"`
	Violations     []BudgetFinding `json:"violations"`
	SatisfiesLimit bool            `json:"satisfies_limit"`
}

// AuditToolDescriptionBudget audits all registered tool descriptions against character limits
// to ensure prompt cache stability and prevent resident context bloat.
func AuditToolDescriptionBudget(reg *codexkit.Registry, maxChars int) BudgetReport {
	if maxChars <= 0 {
		maxChars = 300
	}
	tools := reg.ListTools()
	report := BudgetReport{
		TotalTools: len(tools),
		MaxChars:   maxChars,
		Violations: make([]BudgetFinding, 0),
	}

	if len(tools) == 0 {
		report.SatisfiesLimit = true
		return report
	}

	totalLen := 0
	for _, t := range tools {
		desc := strings.TrimSpace(t.Description)
		descLen := len(desc)
		totalLen += descLen

		if descLen == 0 {
			report.Violations = append(report.Violations, BudgetFinding{
				ToolName:    t.Name,
				LengthChars: 0,
				Issue:       "description is empty",
			})
		} else if descLen > maxChars {
			report.Violations = append(report.Violations, BudgetFinding{
				ToolName:    t.Name,
				LengthChars: descLen,
				Issue:       fmt.Sprintf("description length %d exceeds maximum %d", descLen, maxChars),
			})
		}
	}

	report.AverageChars = float64(totalLen) / float64(len(tools))
	report.SatisfiesLimit = len(report.Violations) == 0
	return report
}
