package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/researchgateway"
	"github.com/hairglasses-studio/codexkit/researchrefresh"
)

const (
	defaultResearchRegistry = "research/upstreams/feeds.json"
	defaultResearchReceipts = "research/upstreams/refresh-receipts.json"
)

type researchCommandDeps struct {
	client *http.Client
	getwd  func() (string, error)
	getenv func(string) string
}

type researchCommandOptions struct {
	command      string
	root         string
	registryPath string
	receiptsPath string
	sourceIDs    []string
	now          time.Time
	json         bool
	write        bool
}

type refreshSummary struct {
	Sources     int `json:"sources"`
	Fetched     int `json:"fetched"`
	NotModified int `json:"not_modified"`
	NotDue      int `json:"not_due"`
	Changed     int `json:"changed"`
	Errors      int `json:"errors"`
}

type researchSourceError struct {
	FeedID string `json:"feed_id"`
	Error  string `json:"error"`
}

type refreshCommandReport struct {
	Root         string                   `json:"root"`
	RegistryPath string                   `json:"registry_path"`
	ReceiptsPath string                   `json:"receipts_path"`
	Mode         string                   `json:"mode"`
	Now          time.Time                `json:"now"`
	Passed       bool                     `json:"passed"`
	Wrote        bool                     `json:"wrote"`
	Summary      refreshSummary           `json:"summary"`
	Results      []researchrefresh.Result `json:"results"`
	Errors       []researchSourceError    `json:"errors,omitempty"`
}

type freshnessSummary struct {
	Sources int `json:"sources"`
	Fresh   int `json:"fresh"`
	Stale   int `json:"stale"`
	Missing int `json:"missing"`
}

type freshnessSource struct {
	FeedID    string     `json:"feed_id"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	NextDueAt *time.Time `json:"next_due_at,omitempty"`
}

type freshnessCommandReport struct {
	Root         string            `json:"root"`
	RegistryPath string            `json:"registry_path"`
	ReceiptsPath string            `json:"receipts_path"`
	Now          time.Time         `json:"now"`
	Passed       bool              `json:"passed"`
	Summary      freshnessSummary  `json:"summary"`
	Sources      []freshnessSource `json:"sources"`
}

func runResearch(args []string) {
	code := executeResearch(args, os.Stdout, os.Stderr, researchCommandDeps{})
	if code != 0 {
		os.Exit(code)
	}
}

func executeResearch(args []string, stdout, stderr io.Writer, deps researchCommandDeps) int {
	opts, err := parseResearchCommand(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		fmt.Fprintln(stderr, researchUsage())
		return 1
	}

	root, err := resolveHarnessRoot(opts.root, deps)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	registryPath, err := resolveHarnessPath(root, opts.registryPath, defaultResearchRegistry, true, false)
	if err != nil {
		fmt.Fprintf(stderr, "error: registry path: %v\n", err)
		return 1
	}
	receiptsPath, err := resolveHarnessPath(root, opts.receiptsPath, defaultResearchReceipts, false, opts.write)
	if err != nil {
		fmt.Fprintf(stderr, "error: receipts path: %v\n", err)
		return 1
	}

	if opts.now.IsZero() {
		opts.now = time.Now().UTC()
	}
	if opts.command == "catalog" {
		gateway, err := researchgateway.Open(context.Background(), researchgateway.Config{
			HarnessRoot:  root,
			RegistryPath: registryPath,
			ReceiptsPath: receiptsPath,
			LocalRoots: []researchgateway.LocalRoot{{
				ID:   "research",
				Path: "research",
			}},
			Now: opts.now,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: open research catalog: %v\n", err)
			return 1
		}
		overview := gateway.Overview()
		passed := overview.Summary.Stale == 0 && overview.Summary.Missing == 0 && overview.Summary.RecordStale == 0
		if opts.json {
			writeResearchJSON(stdout, overview)
		} else {
			status := "PASS"
			if !passed {
				status = "FAIL"
			}
			fmt.Fprintf(stdout, "research catalog: %s (upstreams=%d records=%d documents=%d fresh=%d stale=%d missing=%d record-fresh=%d record-stale=%d snapshot=%s)\n", status,
				overview.Summary.Upstreams, overview.Summary.Records, overview.Summary.Documents,
				overview.Summary.Fresh, overview.Summary.Stale, overview.Summary.Missing,
				overview.Summary.RecordFresh, overview.Summary.RecordStale, overview.SnapshotID)
		}
		if !passed {
			return 1
		}
		return 0
	}

	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: read registry: %v\n", err)
		return 1
	}
	registry, err := researchrefresh.LoadRegistry(registryData)
	if err != nil {
		fmt.Fprintf(stderr, "error: load registry: %v\n", err)
		return 1
	}
	receipts, err := loadResearchReceipts(receiptsPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: load receipts: %v\n", err)
		return 1
	}

	feeds, err := selectResearchFeeds(registry, opts.sourceIDs)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if opts.command == "freshness" {
		report, err := buildFreshnessReport(root, registryPath, receiptsPath, feeds, receipts, opts.now)
		if err != nil {
			fmt.Fprintf(stderr, "error: evaluate freshness: %v\n", err)
			return 1
		}
		writeFreshnessReport(stdout, report, opts.json)
		if !report.Passed {
			return 1
		}
		return 0
	}

	report := buildRefreshReport(context.Background(), root, registryPath, receiptsPath, feeds, receipts, opts, deps.client)
	if report.Passed && opts.write {
		updated := receipts
		candidateCount := 0
		for _, result := range report.Results {
			candidate, ok := result.CandidateReceipt()
			if !ok {
				continue
			}
			updated, err = updated.WithReceipt(candidate)
			if err != nil {
				fmt.Fprintf(stderr, "error: update receipts: %v\n", err)
				return 1
			}
			candidateCount++
		}
		if candidateCount > 0 {
			data, err := researchrefresh.EncodeReceipts(updated)
			if err != nil {
				fmt.Fprintf(stderr, "error: encode receipts: %v\n", err)
				return 1
			}
			if err := atomicWriteResearchReceipts(receiptsPath, data); err != nil {
				fmt.Fprintf(stderr, "error: write receipts: %v\n", err)
				return 1
			}
			report.Wrote = true
		}
	}
	writeRefreshReport(stdout, report, opts.json)
	if !report.Passed {
		return 1
	}
	return 0
}

func parseResearchCommand(args []string) (researchCommandOptions, error) {
	var opts researchCommandOptions
	if len(args) == 0 || (args[0] != "refresh" && args[0] != "freshness" && args[0] != "catalog") {
		return opts, errors.New("research command must be catalog, refresh, or freshness")
	}
	opts.command = args[0]

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.json = true
		case "--write":
			if opts.command != "refresh" {
				return opts, errors.New("--write is only valid for research refresh")
			}
			opts.write = true
		case "--registry", "--receipts", "--source", "--now":
			flag := args[i]
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", flag)
			}
			i++
			value := args[i]
			if value == "" {
				return opts, fmt.Errorf("%s requires a nonempty value", flag)
			}
			switch flag {
			case "--registry":
				opts.registryPath = value
			case "--receipts":
				opts.receiptsPath = value
			case "--source":
				opts.sourceIDs = append(opts.sourceIDs, value)
			case "--now":
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil {
					return opts, fmt.Errorf("--now must be RFC3339: %w", err)
				}
				opts.now = parsed.UTC()
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown option %s", args[i])
			}
			if opts.root != "" {
				return opts, fmt.Errorf("unexpected argument %s", args[i])
			}
			opts.root = args[i]
		}
	}
	if opts.command == "catalog" && len(opts.sourceIDs) != 0 {
		return opts, errors.New("--source is not valid for research catalog")
	}
	return opts, nil
}

func researchUsage() string {
	return "usage: codexkit research <catalog|refresh|freshness> [root] [--registry path] [--receipts path] [--json] [--write] [--source id] [--now RFC3339]"
}

func resolveHarnessRoot(explicit string, deps researchCommandDeps) (string, error) {
	getenv := deps.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	getwd := deps.getwd
	if getwd == nil {
		getwd = os.Getwd
	}

	if explicit != "" {
		return validateHarnessRoot(explicit)
	}
	if configured := strings.TrimSpace(getenv("CODEX_HARNESS_DIR")); configured != "" {
		return validateHarnessRoot(configured)
	}
	cwd, err := getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	abs, err := filepath.Abs(cwd)
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
	return "", fmt.Errorf("no harness root found above %s (expected AGENTS.md and codexkit/go.mod)", abs)
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
		return "", fmt.Errorf("%s is not a harness root (expected AGENTS.md and codexkit/go.mod)", real)
	}
	return filepath.Clean(real), nil
}

func isHarnessRoot(root string) bool {
	for _, rel := range []string{"AGENTS.md", filepath.Join("codexkit", "go.mod")} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func resolveHarnessPath(root, override, defaultRel string, mustExist, forWrite bool) (string, error) {
	path := override
	if path == "" {
		path = defaultRel
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
		return "", fmt.Errorf("%s escapes harness root %s", abs, root)
	}

	info, statErr := os.Lstat(abs)
	if statErr == nil {
		if forWrite && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to replace symlink %s", abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", err
		}
		if !pathWithinRoot(root, real) {
			return "", fmt.Errorf("%s resolves outside harness root %s", abs, root)
		}
		return filepath.Clean(real), nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if mustExist {
		return "", statErr
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolve parent directory: %w", err)
	}
	if !pathWithinRoot(root, parent) {
		return "", fmt.Errorf("%s resolves outside harness root %s", filepath.Dir(abs), root)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func loadResearchReceipts(path string) (researchrefresh.Receipts, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return researchrefresh.Receipts{SchemaVersion: researchrefresh.ReceiptSchemaVersion, Receipts: []researchrefresh.Receipt{}}, nil
	}
	if err != nil {
		return researchrefresh.Receipts{}, err
	}
	return researchrefresh.LoadReceipts(data)
}

func selectResearchFeeds(registry researchrefresh.Registry, sourceIDs []string) ([]researchrefresh.Feed, error) {
	filtering := len(sourceIDs) > 0
	wanted := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		if wanted[id] {
			return nil, fmt.Errorf("duplicate research source %q", id)
		}
		wanted[id] = true
	}
	feeds := make([]researchrefresh.Feed, 0, len(registry.Feeds))
	for _, feed := range registry.Feeds {
		if !filtering || wanted[feed.ID] {
			feeds = append(feeds, feed)
			delete(wanted, feed.ID)
		}
	}
	if len(wanted) > 0 {
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

func buildRefreshReport(ctx context.Context, root, registryPath, receiptsPath string, feeds []researchrefresh.Feed, receipts researchrefresh.Receipts, opts researchCommandOptions, client *http.Client) refreshCommandReport {
	mode := "dry-run"
	if opts.write {
		mode = "write"
	}
	report := refreshCommandReport{
		Root:         root,
		RegistryPath: registryPath,
		ReceiptsPath: receiptsPath,
		Mode:         mode,
		Now:          opts.now,
		Passed:       true,
		Summary:      refreshSummary{Sources: len(feeds)},
		Results:      make([]researchrefresh.Result, 0, len(feeds)),
	}
	for _, feed := range feeds {
		previous, ok := receipts.Receipt(feed.ID)
		var previousPtr *researchrefresh.Receipt
		if ok {
			previousPtr = &previous
		}
		result, err := researchrefresh.Refresh(ctx, feed, previousPtr, researchrefresh.Options{
			Client: client,
			Now:    opts.now,
		})
		if err != nil {
			report.Passed = false
			report.Summary.Errors++
			report.Errors = append(report.Errors, researchSourceError{FeedID: feed.ID, Error: err.Error()})
			continue
		}
		report.Results = append(report.Results, result)
		switch result.Status {
		case researchrefresh.StatusFetched:
			report.Summary.Fetched++
		case researchrefresh.StatusNotModified:
			report.Summary.NotModified++
		case researchrefresh.StatusNotDue:
			report.Summary.NotDue++
		}
		if result.Changed {
			report.Summary.Changed++
		}
	}
	return report
}

func buildFreshnessReport(root, registryPath, receiptsPath string, feeds []researchrefresh.Feed, receipts researchrefresh.Receipts, now time.Time) (freshnessCommandReport, error) {
	report := freshnessCommandReport{
		Root:         root,
		RegistryPath: registryPath,
		ReceiptsPath: receiptsPath,
		Now:          now,
		Passed:       true,
		Summary:      freshnessSummary{Sources: len(feeds)},
		Sources:      make([]freshnessSource, 0, len(feeds)),
	}
	for _, feed := range feeds {
		previous, ok := receipts.Receipt(feed.ID)
		if !ok {
			report.Passed = false
			report.Summary.Missing++
			report.Sources = append(report.Sources, freshnessSource{FeedID: feed.ID, Status: "missing"})
			continue
		}
		due, err := researchrefresh.EvaluateDue(feed, &previous, now)
		if err != nil {
			return report, fmt.Errorf("%s: %w", feed.ID, err)
		}
		status := "fresh"
		if due.Due {
			status = "stale"
			report.Passed = false
			report.Summary.Stale++
		} else {
			report.Summary.Fresh++
		}
		report.Sources = append(report.Sources, freshnessSource{
			FeedID:    feed.ID,
			Status:    status,
			CheckedAt: timePointer(due.CheckedAt),
			NextDueAt: timePointer(due.NextDueAt),
		})
	}
	return report, nil
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func atomicWriteResearchReceipts(path string, data []byte) (retErr error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func writeRefreshReport(out io.Writer, report refreshCommandReport, jsonOut bool) {
	if jsonOut {
		writeResearchJSON(out, report)
		return
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(out, "research refresh: %s (sources=%d fetched=%d not-modified=%d not-due=%d changed=%d errors=%d mode=%s wrote=%t)\n",
		status, report.Summary.Sources, report.Summary.Fetched, report.Summary.NotModified,
		report.Summary.NotDue, report.Summary.Changed, report.Summary.Errors, report.Mode, report.Wrote)
	for _, sourceErr := range report.Errors {
		fmt.Fprintf(out, "  %s: %s\n", sourceErr.FeedID, sourceErr.Error)
	}
}

func writeFreshnessReport(out io.Writer, report freshnessCommandReport, jsonOut bool) {
	if jsonOut {
		writeResearchJSON(out, report)
		return
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(out, "research freshness: %s (sources=%d fresh=%d stale=%d missing=%d)\n",
		status, report.Summary.Sources, report.Summary.Fresh, report.Summary.Stale, report.Summary.Missing)
	for _, source := range report.Sources {
		if source.Status != "fresh" {
			fmt.Fprintf(out, "  %s: %s\n", source.FeedID, source.Status)
		}
	}
}

func writeResearchJSON(out io.Writer, value any) {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
