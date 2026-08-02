package workspace

import (
	"encoding/json"
	"fmt"
	"os"
)

// OverlayRepo names one repo whose checkout location the overlay relocates.
type OverlayRepo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Overlay is a small, non-canonical file that relocates one or more repos'
// checkout paths for a single command invocation — e.g. pointing a repo at a
// managed worktree instead of its default root/<name> location. It never
// touches the checked-in manifest on disk.
type Overlay struct {
	Repos []OverlayRepo `json:"repos"`
}

// LoadOverlay reads and parses an overlay file from disk.
func LoadOverlay(path string) (Overlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Overlay{}, fmt.Errorf("reading overlay %s: %w", path, err)
	}
	var overlay Overlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		return Overlay{}, fmt.Errorf("parsing overlay %s: %w", path, err)
	}
	return overlay, nil
}

// ApplyOverlay merges overlay Path values onto manifest repos by name,
// returning a new Manifest and leaving the input manifest untouched. An
// overlay entry naming a repo absent from the manifest is an error — overlays
// are meant to relocate known repos, not silently introduce new ones.
func ApplyOverlay(manifest Manifest, overlay Overlay) (Manifest, error) {
	index := make(map[string]int, len(manifest.Repos))
	for i, repo := range manifest.Repos {
		index[repo.Name] = i
	}

	merged := Manifest{
		Version: manifest.Version,
		Repos:   append([]Repo(nil), manifest.Repos...),
	}
	for _, entry := range overlay.Repos {
		i, ok := index[entry.Name]
		if !ok {
			return Manifest{}, fmt.Errorf("overlay repo %q not found in manifest", entry.Name)
		}
		merged.Repos[i].Path = entry.Path
	}
	return merged, nil
}

// LoadManifestWithOverlay loads the canonical manifest for root and, when
// overlayPath is non-empty, applies the overlay on top of it. Pass an empty
// overlayPath to load the manifest unmodified.
func LoadManifestWithOverlay(root, overlayPath string) (Manifest, error) {
	manifest, err := LoadManifest(root)
	if err != nil {
		return Manifest{}, err
	}
	if overlayPath == "" {
		return manifest, nil
	}
	overlay, err := LoadOverlay(overlayPath)
	if err != nil {
		return Manifest{}, err
	}
	return ApplyOverlay(manifest, overlay)
}
