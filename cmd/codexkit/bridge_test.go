package main

import (
	"os"
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

func TestFindCodexkitRootFallsBackToSourceTree(t *testing.T) {
	oldCodekitRoot, hadCodekitRoot := os.LookupEnv("CODEXKIT_ROOT")
	defer func() {
		if hadCodekitRoot {
			_ = os.Setenv("CODEXKIT_ROOT", oldCodekitRoot)
		} else {
			_ = os.Unsetenv("CODEXKIT_ROOT")
		}
	}()
	_ = os.Unsetenv("CODEXKIT_ROOT")

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	temp := t.TempDir()
	if err := os.Chdir(temp); err != nil {
		t.Fatalf("Chdir(%q) error = %v", temp, err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	root := findCodexkitRoot("/tmp/workspace/repo")
	if !isCodexkitRoot(root) {
		t.Fatalf("findCodexkitRoot() = %q, expected codexkit root", root)
	}
	want, ok := sourceCodexkitRoot()
	if !ok {
		t.Fatal("sourceCodexkitRoot() unavailable during test")
	}
	if got, want := filepath.Clean(root), filepath.Clean(want); got != want {
		t.Fatalf("findCodexkitRoot() = %q, want %q", got, want)
	}
}
