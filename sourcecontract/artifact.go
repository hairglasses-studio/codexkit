package sourcecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ArtifactCheck captures whether a saved source-contract artifact matches the
// live report.
type ArtifactCheck struct {
	Path    string `json:"path"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// ArtifactJSON returns the stable JSON representation written to source-contract
// artifacts. The artifact omits its own artifact-check result to avoid a
// self-referential report.
func ArtifactJSON(report Report) ([]byte, error) {
	report.Artifact = nil
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteArtifact writes a stable source-contract JSON artifact.
func WriteArtifact(report Report, path string) error {
	if path == "" {
		return fmt.Errorf("source-contract artifact path is required")
	}
	data, err := ArtifactJSON(report)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// CheckArtifact compares a saved source-contract JSON artifact with the live report.
func CheckArtifact(report Report, path string) ArtifactCheck {
	check := ArtifactCheck{Path: path}
	if path == "" {
		check.Message = "source-contract artifact path is required"
		return check
	}
	expected, err := ArtifactJSON(report)
	if err != nil {
		check.Message = err.Error()
		return check
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		check.Message = fmt.Sprintf("reading %s: %v", path, err)
		return check
	}
	if !bytes.Equal(actual, expected) {
		check.Message = fmt.Sprintf("%s does not match live source contract report", path)
		return check
	}
	check.Passed = true
	check.Message = path
	return check
}
