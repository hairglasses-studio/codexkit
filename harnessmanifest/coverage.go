package harnessmanifest

import (
	"fmt"
	"sort"
)

// ValidateCoverage validates the manifest's embedded discovered-path inputs
// and returns deterministic assignment evidence.
func ValidateCoverage(manifest Manifest) (CoverageReport, error) {
	var findings issues
	validateCoverageInputShape(manifest.CoverageInputs, &findings)
	report := buildCoverageReport(manifest, manifest.CoverageInputs, &findings)
	return report, findings.err()
}

// ValidateCoverageInputs validates caller-supplied discovery results against
// the manifest without mutating either value. This is useful when inventory is
// refreshed before updating the checked-in coverage_inputs field.
func ValidateCoverageInputs(manifest Manifest, inputs []CoverageInput) (CoverageReport, error) {
	copyManifest := manifest
	copyManifest.CoverageInputs = append([]CoverageInput(nil), inputs...)
	var findings issues
	validateCoverageInputShape(inputs, &findings)
	report := buildCoverageReport(copyManifest, inputs, &findings)
	return report, findings.err()
}

func validateCoverage(manifest Manifest, findings *issues) {
	buildCoverageReport(manifest, manifest.CoverageInputs, findings)
}

func validateCoverageInputShape(inputs []CoverageInput, findings *issues) {
	seenIDs := map[string]int{}
	seenPaths := map[string]int{}
	for index, input := range inputs {
		base := fmt.Sprintf("coverage_inputs[%d]", index)
		if !identifierPattern.MatchString(input.ID) {
			findings.add("identifier", base+".id", "must be a stable coverage identifier")
		}
		if err := validateRelativePath(input.Path); err != nil {
			findings.add("path", base+".path", err.Error())
		}
		if previous, exists := seenIDs[input.ID]; exists {
			findings.add("duplicate", base+".id", fmt.Sprintf("duplicates coverage_inputs[%d].id", previous))
		} else {
			seenIDs[input.ID] = index
		}
		if previous, exists := seenPaths[input.Path]; exists {
			findings.add("duplicate", base+".path", fmt.Sprintf("duplicates coverage_inputs[%d].path", previous))
		} else {
			seenPaths[input.Path] = index
		}
	}
}

func buildCoverageReport(manifest Manifest, inputs []CoverageInput, findings *issues) CoverageReport {
	report := CoverageReport{TotalInputs: len(inputs)}
	omissionMatchesInput := make([]bool, len(manifest.Omissions))
	for inputIndex, input := range inputs {
		assignment := CoverageAssignment{InputID: input.ID, Path: input.Path}
		matches := 0
		for _, object := range manifest.Objects {
			if object.Destination == input.Path {
				matches++
				assignment.ObjectID = object.ID
				assignment.Disposition = "projected"
			}
		}
		for omissionIndex, omission := range manifest.Omissions {
			if omissionMatches(omission, input.Path) {
				matches++
				omissionMatchesInput[omissionIndex] = true
				assignment.OmissionID = omission.ID
				assignment.Disposition = "omitted"
			}
		}
		switch matches {
		case 0:
			findings.add("uncovered", fmt.Sprintf("coverage_inputs[%d].path", inputIndex), "is not declared by an object or omission")
		case 1:
			if assignment.ObjectID != "" {
				report.Projected++
			} else {
				report.Omitted++
			}
			report.Assignments = append(report.Assignments, assignment)
		default:
			findings.add("multiple_coverage", fmt.Sprintf("coverage_inputs[%d].path", inputIndex), "is assigned by more than one object or omission")
		}
	}
	for index, matched := range omissionMatchesInput {
		if !matched {
			findings.add("stale_omission", fmt.Sprintf("omissions[%d].path", index), "does not match any coverage input")
		}
	}
	sort.Slice(report.Assignments, func(i, j int) bool {
		if report.Assignments[i].Path != report.Assignments[j].Path {
			return report.Assignments[i].Path < report.Assignments[j].Path
		}
		return report.Assignments[i].InputID < report.Assignments[j].InputID
	})
	return report
}
