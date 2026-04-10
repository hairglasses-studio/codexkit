package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWorkspaceRefreshArgsDefaults(t *testing.T) {
	t.Setenv("HOME", "/tmp/hg")
	t.Setenv("SURFACEKIT_ROOT", "")

	cfg, err := parseWorkspaceRefreshArgs(nil)
	if err != nil {
		t.Fatalf("parseWorkspaceRefreshArgs() error = %v", err)
	}

	wantRoot := filepath.Join("/tmp/hg", "hairglasses-studio")
	if cfg.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", cfg.Root, wantRoot)
	}
	if cfg.SurfacekitRoot != filepath.Join(wantRoot, "surfacekit") {
		t.Fatalf("SurfacekitRoot = %q", cfg.SurfacekitRoot)
	}
	if cfg.WriteWorkspaceCache {
		t.Fatal("WriteWorkspaceCache = true, want false")
	}
}

func TestParseWorkspaceRefreshArgsCustom(t *testing.T) {
	t.Setenv("SURFACEKIT_ROOT", "/tmp/custom-surfacekit")

	cfg, err := parseWorkspaceRefreshArgs([]string{"/workspace/demo", "--with-workspace-cache"})
	if err != nil {
		t.Fatalf("parseWorkspaceRefreshArgs() error = %v", err)
	}

	if cfg.Root != "/workspace/demo" {
		t.Fatalf("Root = %q, want /workspace/demo", cfg.Root)
	}
	if cfg.SurfacekitRoot != "/tmp/custom-surfacekit" {
		t.Fatalf("SurfacekitRoot = %q, want /tmp/custom-surfacekit", cfg.SurfacekitRoot)
	}
	if !cfg.WriteWorkspaceCache {
		t.Fatal("WriteWorkspaceCache = false, want true")
	}
}

func TestParseWorkspaceRefreshArgsUnknownFlag(t *testing.T) {
	if _, err := parseWorkspaceRefreshArgs([]string{"--bad-flag"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestSurfacekitParityArgs(t *testing.T) {
	got := surfacekitParityArgs("/workspace/demo", true)
	want := []string{
		"coverage",
		"workspace",
		"--root",
		"/workspace/demo",
		"--write-wiki-docs",
		"--write-json",
		"--write-workspace-cache",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("surfacekitParityArgs() = %v, want %v", got, want)
	}
}
