package researchrefresh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

const (
	defaultRegistryRelativePath = "research/upstreams/feeds.json"
	defaultReceiptsRelativePath = "research/upstreams/refresh-receipts.json"
)

type module struct{}

// Module returns read-only MCP tools for network refresh dry runs and offline
// freshness evaluation. Neither tool writes receipts or fetched content.
func Module() codexkit.ToolModule { return &module{} }

func (m *module) Name() string { return "researchrefresh" }
func (m *module) Init() error  { return nil }

func (m *module) Tools() []codexkit.ToolDef {
	properties := map[string]any{
		"root": map[string]any{
			"type":        "string",
			"description": "Harness root. Defaults to CODEX_HARNESS_DIR or the nearest harness root above the current directory.",
		},
		"registry_path": map[string]any{
			"type":        "string",
			"description": "Registry path constrained within the resolved harness root.",
		},
		"receipts_path": map[string]any{
			"type":        "string",
			"description": "Aggregate receipt path constrained within the resolved harness root; a missing file is treated as empty.",
		},
		"source_ids": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional exact feed IDs; omitted selects every feed.",
		},
		"now": map[string]any{
			"type":        "string",
			"format":      "date-time",
			"description": "Optional RFC3339 evaluation time.",
		},
	}
	return []codexkit.ToolDef{
		{
			Name:        "research_refresh",
			Description: "Dry-run due official-upstream research refreshes from a validated harness registry without writing files.",
			Annotations: codexkit.ToolAnnotations(true, false, true, true),
			Schema: map[string]any{
				"type": "object",
				"properties": mergeProperties(properties, map[string]any{
					"force": map[string]any{
						"type":        "boolean",
						"description": "Fetch selected registry feeds even when cadence says they are fresh; remains read-only.",
					},
				}),
				"additionalProperties": false,
			},
			Handler: codexkit.TypedHandler(func(request moduleRequest) (any, error) {
				return runModuleRefresh(request)
			}),
		},
		{
			Name:        "research_freshness",
			Description: "Evaluate official-upstream research receipt freshness offline without network access or writes.",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type":                 "object",
				"properties":           mergeProperties(properties, nil),
				"additionalProperties": false,
			},
			Handler: codexkit.TypedHandler(func(request moduleRequest) (any, error) {
				return runModuleFreshness(request)
			}),
		},
	}
}

type moduleRequest struct {
	Root         string   `json:"root"`
	RegistryPath string   `json:"registry_path"`
	ReceiptsPath string   `json:"receipts_path"`
	SourceIDs    []string `json:"source_ids"`
	Now          string   `json:"now"`
	Force        bool     `json:"force"`
}

// RefreshReport is the structured dry-run response returned by the MCP tool.
type RefreshReport struct {
	Root         string         `json:"root"`
	RegistryPath string         `json:"registry_path"`
	ReceiptsPath string         `json:"receipts_path"`
	Now          time.Time      `json:"now"`
	Passed       bool           `json:"passed"`
	Summary      RefreshSummary `json:"summary"`
	Results      []Result       `json:"results"`
	Errors       []SourceError  `json:"errors,omitempty"`
}

// RefreshSummary counts dry-run outcomes without retaining content.
type RefreshSummary struct {
	Sources     int `json:"sources"`
	Fetched     int `json:"fetched"`
	NotModified int `json:"not_modified"`
	NotDue      int `json:"not_due"`
	Changed     int `json:"changed"`
	Errors      int `json:"errors"`
}

// SourceError associates a sanitized error with its public feed ID.
type SourceError struct {
	FeedID string `json:"feed_id"`
	Error  string `json:"error"`
}

// FreshnessReport is the structured offline freshness response.
type FreshnessReport struct {
	Root         string            `json:"root"`
	RegistryPath string            `json:"registry_path"`
	ReceiptsPath string            `json:"receipts_path"`
	Now          time.Time         `json:"now"`
	Fresh        bool              `json:"fresh"`
	Summary      FreshnessSummary  `json:"summary"`
	Sources      []FreshnessSource `json:"sources"`
}

// FreshnessSummary counts fresh, stale, and receipt-missing feeds.
type FreshnessSummary struct {
	Sources int `json:"sources"`
	Fresh   int `json:"fresh"`
	Stale   int `json:"stale"`
	Missing int `json:"missing"`
}

// FreshnessSource is one offline feed cadence result.
type FreshnessSource struct {
	FeedID    string     `json:"feed_id"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	NextDueAt *time.Time `json:"next_due_at,omitempty"`
}

func runModuleRefresh(request moduleRequest) (RefreshReport, error) {
	root, registryPath, receiptsPath, receipts, feeds, now, err := loadModuleInputs(request)
	if err != nil {
		return RefreshReport{}, err
	}
	report := RefreshReport{
		Root:         root,
		RegistryPath: registryPath,
		ReceiptsPath: receiptsPath,
		Now:          now,
		Passed:       true,
		Summary:      RefreshSummary{Sources: len(feeds)},
		Results:      make([]Result, 0, len(feeds)),
	}
	for _, feed := range feeds {
		previous, exists := receipts.Receipt(feed.ID)
		var previousPointer *Receipt
		if exists {
			previousPointer = &previous
		}
		result, refreshErr := Refresh(context.Background(), feed, previousPointer, Options{Now: now, Force: request.Force})
		if refreshErr != nil {
			report.Passed = false
			report.Summary.Errors++
			report.Errors = append(report.Errors, SourceError{FeedID: feed.ID, Error: refreshErr.Error()})
			continue
		}
		report.Results = append(report.Results, result)
		switch result.Status {
		case StatusFetched:
			report.Summary.Fetched++
		case StatusNotModified:
			report.Summary.NotModified++
		case StatusNotDue:
			report.Summary.NotDue++
		}
		if result.Changed {
			report.Summary.Changed++
		}
	}
	return report, nil
}

func runModuleFreshness(request moduleRequest) (FreshnessReport, error) {
	root, registryPath, receiptsPath, receipts, feeds, now, err := loadModuleInputs(request)
	if err != nil {
		return FreshnessReport{}, err
	}
	report := FreshnessReport{
		Root:         root,
		RegistryPath: registryPath,
		ReceiptsPath: receiptsPath,
		Now:          now,
		Fresh:        true,
		Summary:      FreshnessSummary{Sources: len(feeds)},
		Sources:      make([]FreshnessSource, 0, len(feeds)),
	}
	for _, feed := range feeds {
		previous, exists := receipts.Receipt(feed.ID)
		if !exists {
			report.Fresh = false
			report.Summary.Missing++
			report.Sources = append(report.Sources, FreshnessSource{FeedID: feed.ID, Status: "missing"})
			continue
		}
		due, dueErr := EvaluateDue(feed, &previous, now)
		if dueErr != nil {
			return FreshnessReport{}, fmt.Errorf("evaluate feed %q: %w", feed.ID, dueErr)
		}
		status := "fresh"
		if due.Due {
			status = "stale"
			report.Fresh = false
			report.Summary.Stale++
		} else {
			report.Summary.Fresh++
		}
		report.Sources = append(report.Sources, FreshnessSource{
			FeedID:    feed.ID,
			Status:    status,
			CheckedAt: timePointer(due.CheckedAt),
			NextDueAt: timePointer(due.NextDueAt),
		})
	}
	return report, nil
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func loadModuleInputs(request moduleRequest) (string, string, string, Receipts, []Feed, time.Time, error) {
	now, err := parseModuleTime(request.Now)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, err
	}
	root, err := resolveHarnessRoot(request.Root)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, err
	}
	registryPath, err := resolveReadPath(root, request.RegistryPath, defaultRegistryRelativePath, true)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, fmt.Errorf("resolve registry path: %w", err)
	}
	receiptsPath, err := resolveReadPath(root, request.ReceiptsPath, defaultReceiptsRelativePath, false)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, fmt.Errorf("resolve receipts path: %w", err)
	}
	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, fmt.Errorf("read registry: %w", err)
	}
	registry, err := LoadRegistry(registryData)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, err
	}
	receipts := Receipts{SchemaVersion: ReceiptSchemaVersion, Receipts: []Receipt{}}
	receiptData, err := os.ReadFile(receiptsPath)
	if err == nil {
		receipts, err = LoadReceipts(receiptData)
		if err != nil {
			return "", "", "", Receipts{}, nil, time.Time{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", "", Receipts{}, nil, time.Time{}, fmt.Errorf("read receipts: %w", err)
	}
	feeds, err := selectFeeds(registry, request.SourceIDs)
	if err != nil {
		return "", "", "", Receipts{}, nil, time.Time{}, err
	}
	return root, registryPath, receiptsPath, receipts, feeds, now, nil
}

func parseModuleTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("now must be RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}

func selectFeeds(registry Registry, sourceIDs []string) ([]Feed, error) {
	wanted := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if _, duplicate := wanted[sourceID]; duplicate {
			return nil, fmt.Errorf("duplicate source_id %q", sourceID)
		}
		wanted[sourceID] = struct{}{}
	}
	feeds := make([]Feed, 0, len(registry.Feeds))
	for _, feed := range registry.Feeds {
		if len(sourceIDs) == 0 {
			feeds = append(feeds, feed)
			continue
		}
		if _, ok := wanted[feed.ID]; ok {
			feeds = append(feeds, feed)
			delete(wanted, feed.ID)
		}
	}
	if len(wanted) != 0 {
		unknown := make([]string, 0, len(wanted))
		for id := range wanted {
			unknown = append(unknown, id)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown research source(s): %s", strings.Join(unknown, ", "))
	}
	sort.Slice(feeds, func(i, j int) bool { return feeds[i].ID < feeds[j].ID })
	return feeds, nil
}

func resolveHarnessRoot(explicit string) (string, error) {
	if explicit == "" {
		explicit = strings.TrimSpace(os.Getenv("CODEX_HARNESS_DIR"))
	}
	if explicit != "" {
		return validateHarnessRoot(explicit)
	}
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	abs, err := filepath.Abs(current)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	for candidate := filepath.Clean(abs); ; candidate = filepath.Dir(candidate) {
		if isHarnessRoot(candidate) {
			return validateHarnessRoot(candidate)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return "", fmt.Errorf("no harness root found above %s", abs)
}

func validateHarnessRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve harness root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve harness root symlinks: %w", err)
	}
	if !isHarnessRoot(real) {
		return "", fmt.Errorf("%s is not a harness root", real)
	}
	return filepath.Clean(real), nil
}

func isHarnessRoot(root string) bool {
	for _, relative := range []string{"AGENTS.md", filepath.Join("codexkit", "go.mod")} {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func resolveReadPath(root, override, defaultRelative string, mustExist bool) (string, error) {
	path := override
	if path == "" {
		path = defaultRelative
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if !pathWithinRoot(root, abs) {
		return "", fmt.Errorf("path escapes harness root")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		if !pathWithinRoot(root, real) {
			return "", fmt.Errorf("path resolves outside harness root")
		}
		return filepath.Clean(real), nil
	}
	if !errors.Is(err, os.ErrNotExist) || mustExist {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, parent) {
		return "", fmt.Errorf("path parent resolves outside harness root")
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func mergeProperties(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
