package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoPath_DefaultsToRootJoinName(t *testing.T) {
	got := RepoPath("/root", Repo{Name: "codexkit"})
	want := filepath.Join("/root", "codexkit")
	if got != want {
		t.Fatalf("RepoPath() = %q, want %q", got, want)
	}
}

func TestRepoPath_HonorsAbsoluteOverride(t *testing.T) {
	got := RepoPath("/root", Repo{Name: "codexkit", Path: "/elsewhere/codexkit-worktree"})
	want := "/elsewhere/codexkit-worktree"
	if got != want {
		t.Fatalf("RepoPath() = %q, want %q", got, want)
	}
}

func TestRepoPath_ResolvesRelativeOverrideAgainstRoot(t *testing.T) {
	got := RepoPath("/root", Repo{Name: "codexkit", Path: "worktrees/codexkit-alt"})
	want := filepath.Join("/root", "worktrees", "codexkit-alt")
	if got != want {
		t.Fatalf("RepoPath() = %q, want %q", got, want)
	}
}

func TestApplyOverlay_MergesPathByName(t *testing.T) {
	manifest := Manifest{
		Version: 1,
		Repos: []Repo{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	overlay := Overlay{Repos: []OverlayRepo{
		{Name: "beta", Path: "/tmp/beta-worktree"},
	}}

	merged, err := ApplyOverlay(manifest, overlay)
	if err != nil {
		t.Fatalf("ApplyOverlay() error = %v", err)
	}
	if merged.Repos[0].Path != "" {
		t.Fatalf("alpha.Path = %q, want unchanged empty", merged.Repos[0].Path)
	}
	if merged.Repos[1].Path != "/tmp/beta-worktree" {
		t.Fatalf("beta.Path = %q, want /tmp/beta-worktree", merged.Repos[1].Path)
	}
	// input manifest must be untouched
	if manifest.Repos[1].Path != "" {
		t.Fatal("ApplyOverlay must not mutate the input manifest")
	}
}

func TestApplyOverlay_UnknownRepoNameIsError(t *testing.T) {
	manifest := Manifest{Repos: []Repo{{Name: "alpha"}}}
	overlay := Overlay{Repos: []OverlayRepo{{Name: "ghost", Path: "/tmp/ghost"}}}

	if _, err := ApplyOverlay(manifest, overlay); err == nil {
		t.Fatal("expected error for unknown overlay repo name")
	}
}

func TestApplyOverlay_RelativePathResolvesAgainstRootViaRepoPath(t *testing.T) {
	manifest := Manifest{Repos: []Repo{{Name: "alpha"}}}
	overlay := Overlay{Repos: []OverlayRepo{{Name: "alpha", Path: "relocated/alpha"}}}

	merged, err := ApplyOverlay(manifest, overlay)
	if err != nil {
		t.Fatalf("ApplyOverlay() error = %v", err)
	}
	got := RepoPath("/root", merged.Repos[0])
	want := filepath.Join("/root", "relocated", "alpha")
	if got != want {
		t.Fatalf("RepoPath() = %q, want %q", got, want)
	}
}

func TestLoadManifestWithOverlay_AppliesFileOnDisk(t *testing.T) {
	root := t.TempDir()
	writeManifestFixture(t, root)

	overlayDir := t.TempDir()
	overlayPath := filepath.Join(overlayDir, "overlay.json")
	overlay := Overlay{Repos: []OverlayRepo{{Name: "alpha", Path: "/elsewhere/alpha"}}}
	data, err := json.Marshal(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadManifestWithOverlay(root, overlayPath)
	if err != nil {
		t.Fatalf("LoadManifestWithOverlay() error = %v", err)
	}
	if got := RepoPath(root, manifest.Repos[0]); got != "/elsewhere/alpha" {
		t.Fatalf("RepoPath() = %q, want /elsewhere/alpha", got)
	}
}

func TestLoadManifestWithOverlay_EmptyPathLeavesManifestUnmodified(t *testing.T) {
	root := t.TempDir()
	writeManifestFixture(t, root)

	manifest, err := LoadManifestWithOverlay(root, "")
	if err != nil {
		t.Fatalf("LoadManifestWithOverlay() error = %v", err)
	}
	if manifest.Repos[0].Path != "" {
		t.Fatalf("expected no override, got %q", manifest.Repos[0].Path)
	}
}

func TestLoadManifestWithOverlay_UnknownOverlayRepoErrors(t *testing.T) {
	root := t.TempDir()
	writeManifestFixture(t, root)

	overlayDir := t.TempDir()
	overlayPath := filepath.Join(overlayDir, "overlay.json")
	overlay := Overlay{Repos: []OverlayRepo{{Name: "ghost", Path: "/elsewhere/ghost"}}}
	data, _ := json.Marshal(overlay)
	if err := os.WriteFile(overlayPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadManifestWithOverlay(root, overlayPath); err == nil {
		t.Fatal("expected error for unknown overlay repo name")
	}
}

func writeManifestFixture(t *testing.T, root string) {
	t.Helper()
	manifest := Manifest{
		Version: 2,
		Repos: []Repo{
			{Name: "alpha", Scope: "active_first_party", BaselineTarget: true},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := ManifestPath(root)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		t.Fatal(err)
	}
}
