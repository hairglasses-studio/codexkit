package mcpsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// DiffPreview matches the JSON object emitted by hg-dotfiles-mcp-projection.sh previews.
type DiffPreview struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Preview string `json:"preview"`
}

// BuildDiffPreview reproduces the bash diff-preview contract for one file pair.
func BuildDiffPreview(rel, left, right, kind string, limit int) (DiffPreview, error) {
	if limit <= 0 {
		return DiffPreview{}, fmt.Errorf("limit must be positive")
	}
	preview, err := renderDiffPreview(rel, left, right, limit)
	if err != nil {
		return DiffPreview{}, err
	}
	added, deleted, err := diffNumstat(left, right)
	if err != nil {
		return DiffPreview{}, err
	}
	return DiffPreview{
		Path:    rel,
		Kind:    kind,
		Added:   added,
		Deleted: deleted,
		Preview: preview,
	}, nil
}

// RenderDiffPreviewJSON returns one JSON line compatible with the bash jq emitter.
func RenderDiffPreviewJSON(rel, left, right, kind string, limit int) ([]byte, error) {
	preview, err := BuildDiffPreview(rel, left, right, kind, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(preview)
}

func renderDiffPreview(rel, left, right string, limit int) (string, error) {
	cmd := exec.Command(
		"diff",
		"-u",
		"--label",
		"canonical/"+rel,
		"--label",
		"standalone/"+rel,
		left,
		right,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("run diff: %w", err)
		}
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "", nil
	}
	previewLines := make([]string, 0, limit+2)
	inHunk := false
	bodyLines := 0
	for idx, line := range lines {
		if idx < 2 {
			previewLines = append(previewLines, line)
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
		}
		if !inHunk {
			continue
		}
		if bodyLines < limit {
			previewLines = append(previewLines, line)
			bodyLines++
		}
	}
	return strings.Join(previewLines, "\n"), nil
}

func diffNumstat(left, right string) (int, int, error) {
	cmd := exec.Command("git", "--no-pager", "diff", "--no-index", "--numstat", "--", left, right)
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return 0, 0, fmt.Errorf("run git diff --numstat: %w", err)
		}
	}
	line := strings.TrimSpace(string(bytes.TrimSpace(output)))
	if line == "" {
		return 0, 0, nil
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, 0, nil
	}
	added, err := parseNumstatField(fields[0])
	if err != nil {
		return 0, 0, err
	}
	deleted, err := parseNumstatField(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return added, deleted, nil
}

func parseNumstatField(value string) (int, error) {
	if value == "-" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse numstat field %q: %w", value, err)
	}
	return n, nil
}
