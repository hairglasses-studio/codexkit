package harnessmanifest

import (
	"fmt"
	"sort"
	"strings"
)

// ValidationError contains all independently detectable schema findings.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "manifest validation failed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", issue.Path, issue.Message))
	}
	return "manifest validation failed: " + strings.Join(parts, "; ")
}

type issues struct {
	values []Issue
}

func (v *issues) add(code, path, message string) {
	v.values = append(v.values, Issue{Code: code, Path: path, Message: message})
}

func (v *issues) err() error {
	if len(v.values) == 0 {
		return nil
	}
	sort.SliceStable(v.values, func(i, j int) bool {
		if v.values[i].Path != v.values[j].Path {
			return v.values[i].Path < v.values[j].Path
		}
		if v.values[i].Code != v.values[j].Code {
			return v.values[i].Code < v.values[j].Code
		}
		return v.values[i].Message < v.values[j].Message
	})
	return &ValidationError{Issues: append([]Issue(nil), v.values...)}
}
