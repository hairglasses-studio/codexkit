package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseWorkspaceRefreshArgsDefaults(t *testing.T) {
	t.Setenv("HOME", "/tmp/hg")
	t.Setenv("CODEXKIT_ROOT", "/tmp/codexkit")

	cfg, err := parseWorkspaceRefreshArgs(nil)
	if err != nil {
		t.Fatalf("parseWorkspaceRefreshArgs() error = %v", err)
	}

	wantRoot := filepath.Join("/tmp/hg", "hairglasses-studio")
	if cfg.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", cfg.Root, wantRoot)
	}
	if cfg.CodexkitRoot != "/tmp/codexkit" {
		t.Fatalf("CodexkitRoot = %q", cfg.CodexkitRoot)
	}
	if cfg.WriteWorkspaceCache {
		t.Fatal("WriteWorkspaceCache = true, want false")
	}
}

func TestParseWorkspaceRefreshArgsCustom(t *testing.T) {
	t.Setenv("CODEXKIT_ROOT", "/tmp/custom-codexkit")

	cfg, err := parseWorkspaceRefreshArgs([]string{"/workspace/demo", "--with-workspace-cache"})
	if err != nil {
		t.Fatalf("parseWorkspaceRefreshArgs() error = %v", err)
	}

	if cfg.Root != "/workspace/demo" {
		t.Fatalf("Root = %q, want /workspace/demo", cfg.Root)
	}
	if cfg.CodexkitRoot != "/tmp/custom-codexkit" {
		t.Fatalf("CodexkitRoot = %q, want /tmp/custom-codexkit", cfg.CodexkitRoot)
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

func TestParityAuditArgs(t *testing.T) {
	got := parityAuditArgs(true)
	want := []string{
		"--write-wiki-docs",
		"--write-json",
		"--write-workspace-cache",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parityAuditArgs() = %v, want %v", got, want)
	}
}
