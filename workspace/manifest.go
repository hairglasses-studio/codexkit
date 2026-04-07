package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Repo describes one managed repo in the studio workspace.
type Repo struct {
	Name           string `json:"name"`
	Category       string `json:"category"`
	Scope          string `json:"scope"`
	Language       string `json:"language"`
	BaselineTarget bool   `json:"baseline_target"`
	GoWorkMember   bool   `json:"go_work_member"`
}

// Manifest is the canonical machine-readable workspace inventory.
type Manifest struct {
	Version int    `json:"version"`
	Repos   []Repo `json:"repos"`
}

// Filter narrows manifest results for CLI commands and checks.
type Filter struct {
	Scope        string
	BaselineOnly bool
	GoWorkOnly   bool
}

// DefaultRoot returns the default studio root on disk.
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "hairglasses-studio")
}

// ManifestPath returns the canonical manifest path for a studio root.
func ManifestPath(root string) string {
	return filepath.Join(root, "workspace", "manifest.json")
}

// LoadManifest loads the workspace manifest from disk.
func LoadManifest(root string) (Manifest, error) {
	data, err := os.ReadFile(ManifestPath(root))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Filter returns repos in manifest order that match the requested filter.
func (m Manifest) Filter(filter Filter) []Repo {
	repos := make([]Repo, 0, len(m.Repos))
	for _, repo := range m.Repos {
		if filter.Scope != "" && repo.Scope != filter.Scope {
			continue
		}
		if filter.BaselineOnly && !repo.BaselineTarget {
			continue
		}
		if filter.GoWorkOnly && !repo.GoWorkMember {
			continue
		}
		repos = append(repos, repo)
	}
	return repos
}

// RepoNames returns the names of repos matching the requested filter.
func (m Manifest) RepoNames(filter Filter) []string {
	repos := m.Filter(filter)
	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.Name)
	}
	return names
}
