package harnessrender

import (
	"fmt"
	"sort"
	"strings"
)

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "harness render validation failed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", issue.Path, issue.Message))
	}
	return "harness render validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Unwrap() error { return ErrInvalidRender }

type issues []Issue

func (v *issues) add(code, field, message string) {
	*v = append(*v, Issue{Code: code, Path: field, Message: message})
}

func (v *issues) err() error {
	if len(*v) == 0 {
		return nil
	}
	sort.SliceStable(*v, func(i, j int) bool {
		if (*v)[i].Path != (*v)[j].Path {
			return (*v)[i].Path < (*v)[j].Path
		}
		if (*v)[i].Code != (*v)[j].Code {
			return (*v)[i].Code < (*v)[j].Code
		}
		return (*v)[i].Message < (*v)[j].Message
	})
	return &ValidationError{Issues: append([]Issue(nil), (*v)...)}
}

func wrapInvalid(stage string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidRender, stage, err)
}
