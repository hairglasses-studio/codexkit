package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ManifestBuildReport describes a generated workspace manifest and its inputs.
type ManifestBuildReport struct {
	Root             string   `json:"root"`
	ManifestPath     string   `json:"manifest_path"`
	RepoCatalogPath  string   `json:"repo_catalog_path,omitempty"`
	ParityPath       string   `json:"parity_path,omitempty"`
	MatrixPath       string   `json:"matrix_path,omitempty"`
	RepoCount        int      `json:"repo_count"`
	LiveRepoCount    int      `json:"live_repo_count"`
	CatalogRepoCount int      `json:"catalog_repo_count"`
	ParityRepoCount  int      `json:"parity_repo_count"`
	LiveOnlyRepos    []string `json:"live_only_repos,omitempty"`
	Manifest         Manifest `json:"manifest"`
}

type repoCatalogFile struct {
	Repos []repoCatalogEntry `json:"repos"`
}

type repoCatalogEntry struct {
	Name            string `json:"name"`
	Group           string `json:"group"`
	Lifecycle       string `json:"lifecycle"`
	Archived        bool   `json:"archived"`
	LanguageProfile string `json:"language_profile"`
	LocalPath       string `json:"local_path"`
}

type parityInventoryFile struct {
	Repos []parityRepoEntry `json:"repos"`
}

type parityRepoEntry struct {
	Repo                  string `json:"repo"`
	Scope                 string `json:"scope"`
	ExpectedCodexBaseline int    `json:"expected_codex_baseline"`
}

// GenerateManifest builds a manifest from the live workspace plus docs metadata.
func GenerateManifest(root string) (ManifestBuildReport, error) {
	root = filepath.Clean(root)
	catalogPath := filepath.Join(root, "docs", "inventory", "repo-catalog.json")
	parityPath := filepath.Join(root, "docs", "agent-parity", "repo-inventory.json")
	matrixPath := ConsolidationMatrixPath(root)

	catalog, catalogLoaded, err := loadRepoCatalog(catalogPath)
	if err != nil {
		return ManifestBuildReport{}, err
	}
	parity, parityLoaded, err := loadParityInventory(parityPath)
	if err != nil {
		return ManifestBuildReport{}, err
	}
	matrix, matrixStatus := loadConsolidationMatrixForCheck(root)
	if !matrixStatus.passed {
		return ManifestBuildReport{}, fmt.Errorf("loading consolidation matrix: %s", matrixStatus.message)
	}

	liveRepos, err := scanLiveRepos(root)
	if err != nil {
		return ManifestBuildReport{}, err
	}
	goWorkMembers := scanRootGoWorkMembers(root)

	catalogByName := make(map[string]repoCatalogEntry, len(catalog.Repos))
	catalogByLocalPath := make(map[string]repoCatalogEntry, len(catalog.Repos))
	catalogOrder := make([]string, 0, len(catalog.Repos))
	for _, repo := range catalog.Repos {
		name := repo.Name
		if repo.LocalPath != "" {
			name = repo.LocalPath
		}
		if name == "" {
			continue
		}
		catalogByName[repo.Name] = repo
		catalogByLocalPath[name] = repo
		if liveRepos[name] {
			catalogOrder = append(catalogOrder, name)
		}
	}

	parityByName := make(map[string]parityRepoEntry, len(parity.Repos))
	for _, repo := range parity.Repos {
		if repo.Repo != "" {
			parityByName[repo.Repo] = repo
		}
	}

	decisions := matrixStatus.decisions
	if decisions == nil {
		decisions = make(map[string]ConsolidationDecision)
		for _, decision := range matrix.Decisions {
			decisions[decision.Repo] = decision
		}
	}

	order := append([]string{}, catalogOrder...)
	for _, name := range mapKeys(liveRepos) {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}

	report := ManifestBuildReport{
		Root:          root,
		ManifestPath:  ManifestPath(root),
		LiveRepoCount: len(liveRepos),
		Manifest: Manifest{
			Version: 1,
			Repos:   make([]Repo, 0, len(order)),
		},
	}
	if catalogLoaded {
		report.RepoCatalogPath = catalogPath
		report.CatalogRepoCount = len(catalog.Repos)
	}
	if parityLoaded {
		report.ParityPath = parityPath
		report.ParityRepoCount = len(parity.Repos)
	}
	if matrixStatus.loaded {
		report.MatrixPath = matrixPath
	}

	for _, name := range order {
		catalogEntry, inCatalog := catalogByLocalPath[name]
		if !inCatalog {
			catalogEntry = catalogByName[name]
		}
		parityEntry := parityByName[name]
		decision := decisions[name]
		if omitFromGeneratedManifest(decision) {
			continue
		}
		repo := Repo{
			Name:           name,
			Category:       inferCategory(name, catalogEntry),
			Scope:          inferScope(root, name, catalogEntry, parityEntry, decision),
			Language:       inferLanguage(root, name, catalogEntry),
			BaselineTarget: inferBaselineTarget(root, name, catalogEntry, parityEntry, decision),
			GoWorkMember:   goWorkMembers[name],
			Lifecycle:      inferLifecycle(root, name, catalogEntry),
		}
		if decision.GoWorkMember != nil {
			repo.GoWorkMember = *decision.GoWorkMember
		}
		report.Manifest.Repos = append(report.Manifest.Repos, repo)
		if !inCatalog {
			report.LiveOnlyRepos = append(report.LiveOnlyRepos, name)
		}
	}
	report.RepoCount = len(report.Manifest.Repos)
	return report, nil
}

// WriteManifest writes a generated manifest to the canonical workspace path.
func WriteManifest(root string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path := ManifestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func loadRepoCatalog(path string) (repoCatalogFile, bool, error) {
	var catalog repoCatalogFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return catalog, false, nil
		}
		return catalog, false, fmt.Errorf("reading repo catalog %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return catalog, false, fmt.Errorf("parsing repo catalog %s: %w", path, err)
	}
	return catalog, true, nil
}

func loadParityInventory(path string) (parityInventoryFile, bool, error) {
	var inventory parityInventoryFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return inventory, false, nil
		}
		return inventory, false, fmt.Errorf("reading parity inventory %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		return inventory, false, fmt.Errorf("parsing parity inventory %s: %w", path, err)
	}
	return inventory, true, nil
}

func scanLiveRepos(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading workspace root: %w", err)
	}
	repos := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), ".git")); err == nil {
			repos[entry.Name()] = true
		}
	}
	return repos, nil
}

func scanRootGoWorkMembers(root string) map[string]bool {
	members := make(map[string]bool)
	paths, err := ParseGoWorkModules(filepath.Join(root, "go.work"))
	if err != nil {
		return members
	}
	for _, member := range paths {
		members[strings.TrimPrefix(member, "./")] = true
	}
	return members
}

func inferCategory(name string, catalog repoCatalogEntry) string {
	if catalog.Group != "" {
		return catalog.Group
	}
	if name == ".github" {
		return "org-platform"
	}
	return "local"
}

func inferScope(root, name string, catalog repoCatalogEntry, parity parityRepoEntry, decision ConsolidationDecision) string {
	if decision.WorkspaceScope != "" {
		return decision.WorkspaceScope
	}
	if parity.Scope != "" {
		return parity.Scope
	}
	if isPassiveRepo(root, name) {
		return "compatibility_only"
	}
	if catalog.Archived || catalog.Lifecycle == "maintenance-only" || catalog.Lifecycle == "publish-mirror" {
		return "compatibility_only"
	}
	if catalog.Lifecycle == "active" || hasAgentSurface(root, name) {
		return "active_first_party"
	}
	return "compatibility_only"
}

func inferLanguage(root, name string, catalog repoCatalogEntry) string {
	if catalog.LanguageProfile != "" && catalog.LanguageProfile != "Unknown" {
		return catalog.LanguageProfile
	}
	repoPath := filepath.Join(root, name)
	switch {
	case fileExists(filepath.Join(repoPath, "go.mod")):
		return "Go"
	case fileExists(filepath.Join(repoPath, "package.json")):
		return "TypeScript/Node"
	case fileExists(filepath.Join(repoPath, "pyproject.toml")) || fileExists(filepath.Join(repoPath, "requirements.txt")):
		return "Python"
	case fileExists(filepath.Join(repoPath, "Makefile")):
		return "Shell/Config"
	default:
		return "Markdown"
	}
}

func inferBaselineTarget(root, name string, catalog repoCatalogEntry, parity parityRepoEntry, decision ConsolidationDecision) bool {
	if decision.BaselineTarget != nil {
		return *decision.BaselineTarget
	}
	if parity.ExpectedCodexBaseline == 1 {
		return true
	}
	if parity.Scope == "compatibility_only" || parity.Scope == "upstream_or_reference" {
		return false
	}
	if decision.WorkspaceScope == "compatibility_only" {
		return false
	}
	if isPassiveRepo(root, name) {
		return false
	}
	if catalog.Archived || catalog.Lifecycle == "maintenance-only" || catalog.Lifecycle == "publish-mirror" {
		return false
	}
	return hasAgentSurface(root, name) && !isPassiveRepo(root, name)
}

func omitFromGeneratedManifest(decision ConsolidationDecision) bool {
	return decision.State == "removed_from_active_baseline"
}

func inferLifecycle(root, name string, catalog repoCatalogEntry) string {
	if catalog.Lifecycle != "" {
		return catalog.Lifecycle
	}
	if isPassiveRepo(root, name) {
		return "passive"
	}
	return "active"
}

func hasAgentSurface(root, name string) bool {
	repoPath := filepath.Join(root, name)
	return fileExists(filepath.Join(repoPath, "AGENTS.md")) &&
		(fileExists(filepath.Join(repoPath, ".codex", "config.toml")) ||
			fileExists(filepath.Join(repoPath, ".agents", "skills", "surface.yaml")) ||
			fileExists(filepath.Join(repoPath, ".mcp.json")))
}

func isPassiveRepo(root, name string) bool {
	data, err := os.ReadFile(filepath.Join(root, name, ".ralphrc"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `mode = "passive"`) || strings.Contains(string(data), "mode=passive")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
