package mcpsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDiffPreview(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	left := filepath.Join(dir, "left.txt")
	right := filepath.Join(dir, "right.txt")
	if err := os.WriteFile(left, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatalf("write left: %v", err)
	}
	if err := os.WriteFile(right, []byte("one\nTWO\nthree\nFOUR\n"), 0o644); err != nil {
		t.Fatalf("write right: %v", err)
	}

	preview, err := BuildDiffPreview("demo.txt", left, right, "direct_copy", 2)
	if err != nil {
		t.Fatalf("BuildDiffPreview: %v", err)
	}
	if preview.Path != "demo.txt" || preview.Kind != "direct_copy" {
		t.Fatalf("unexpected identity: %+v", preview)
	}
	if preview.Added != 2 || preview.Deleted != 2 {
		t.Fatalf("unexpected numstat: %+v", preview)
	}
	if !strings.Contains(preview.Preview, "--- canonical/demo.txt") {
		t.Fatalf("missing canonical label: %q", preview.Preview)
	}
	if !strings.Contains(preview.Preview, "+++ standalone/demo.txt") {
		t.Fatalf("missing standalone label: %q", preview.Preview)
	}
	if !strings.Contains(preview.Preview, "@@ -1,4 +1,4 @@") {
		t.Fatalf("missing hunk header: %q", preview.Preview)
	}
	if !strings.Contains(preview.Preview, "\n one") {
		t.Fatalf("missing limited diff body: %q", preview.Preview)
	}
	if strings.Contains(preview.Preview, "-two") || strings.Contains(preview.Preview, "+TWO") || strings.Contains(preview.Preview, "FOUR") {
		t.Fatalf("preview exceeded requested limit: %q", preview.Preview)
	}
}

func TestRenderDiffPreviewJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	left := filepath.Join(dir, "left.txt")
	right := filepath.Join(dir, "right.txt")
	if err := os.WriteFile(left, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write left: %v", err)
	}
	if err := os.WriteFile(right, []byte("alpha\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write right: %v", err)
	}

	data, err := RenderDiffPreviewJSON("sample.txt", left, right, "go_overlap", 1)
	if err != nil {
		t.Fatalf("RenderDiffPreviewJSON: %v", err)
	}
	var preview DiffPreview
	if err := json.Unmarshal(data, &preview); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if preview.Path != "sample.txt" || preview.Kind != "go_overlap" {
		t.Fatalf("unexpected preview identity: %+v", preview)
	}
	if preview.Added != 1 || preview.Deleted != 1 {
		t.Fatalf("unexpected preview counts: %+v", preview)
	}
}
