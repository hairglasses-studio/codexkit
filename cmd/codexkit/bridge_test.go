package main

import (
	"path/filepath"
	"testing"
)

func TestParseBridgeConfigDefaults(t *testing.T) {
	cfg, err := parseBridgeConfig([]string{"/tmp/workspace/repo"}, false)
	if err != nil {
		t.Fatalf("parseBridgeConfig() error = %v", err)
	}
	if cfg.RepoPath != "/tmp/workspace/repo" {
		t.Fatalf("RepoPath = %q", cfg.RepoPath)
	}
	wantSurfacekit := filepath.Join("/tmp/workspace", "surfacekit")
	if cfg.SurfacekitRoot != wantSurfacekit {
		t.Fatalf("SurfacekitRoot = %q, want %q", cfg.SurfacekitRoot, wantSurfacekit)
	}
}

func TestParseBridgeConfigWithOptions(t *testing.T) {
	cfg, err := parseBridgeConfig([]string{
		"/tmp/workspace/repo",
		"--repo-name", "demo",
		"--surfacekit-root", "/opt/surfacekit",
	}, true)
	if err != nil {
		t.Fatalf("parseBridgeConfig() error = %v", err)
	}
	if cfg.RepoName != "demo" {
		t.Fatalf("RepoName = %q", cfg.RepoName)
	}
	if cfg.SurfacekitRoot != "/opt/surfacekit" {
		t.Fatalf("SurfacekitRoot = %q", cfg.SurfacekitRoot)
	}
}

func TestParseBridgeConfigRejectsRepoNameWhenUnsupported(t *testing.T) {
	if _, err := parseBridgeConfig([]string{"/tmp/workspace/repo", "--repo-name", "demo"}, false); err == nil {
		t.Fatal("expected error for unsupported --repo-name")
	}
}
