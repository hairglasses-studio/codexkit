package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
)

const maxVerifierRegistryBytes = 1 << 20

type harnessManifestSummary struct {
	SchemaVersion  int    `json:"schema_version"`
	Repo           string `json:"repo"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Objects        int    `json:"objects"`
	Omissions      int    `json:"omissions"`
	CoverageInputs int    `json:"coverage_inputs"`
	Projected      int    `json:"projected"`
	Omitted        int    `json:"omitted"`
	WriteCapable   bool   `json:"write_capable"`
}

type harnessPlanSummary struct {
	SchemaVersion int    `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	PlanSHA256    string `json:"plan_sha256"`
	TargetRepo    string `json:"target_repository"`
	Sources       int    `json:"sources"`
	Operations    int    `json:"operations"`
	Creates       int    `json:"creates"`
	Replaces      int    `json:"replaces"`
	Removes       int    `json:"removes"`
	WriteCapable  bool   `json:"write_capable"`
}

func runHarness(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "harness: expected manifest-check, plan-check, or render-check")
		os.Exit(1)
	}
	switch args[0] {
	case "manifest-check":
		summary, jsonOutput, err := executeHarnessManifest(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness manifest-check: %v\n", err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSON(summary)
			return
		}
		fmt.Printf("harness manifest-check: PASS (repo=%s objects=%d omissions=%d coverage=%d projected=%d omitted=%d manifest-sha256=%s write-capable=false)\n",
			summary.Repo, summary.Objects, summary.Omissions, summary.CoverageInputs, summary.Projected, summary.Omitted, summary.ManifestSHA256)
	case "plan-check":
		summary, jsonOutput, err := executeHarnessPlan(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness plan-check: %v\n", err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSON(summary)
			return
		}
		fmt.Printf("harness plan-check: PASS (id=%s target=%s sources=%d operations=%d creates=%d replaces=%d removes=%d plan-sha256=%s write-capable=false)\n",
			summary.PlanID, summary.TargetRepo, summary.Sources, summary.Operations, summary.Creates, summary.Replaces, summary.Removes, summary.PlanSHA256)
	case "render-check":
		summary, jsonOutput, err := executeHarnessRender(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness render-check: %v\n", err)
			os.Exit(1)
		}
		if jsonOutput {
			printJSON(summary)
			return
		}
		fmt.Printf("harness render-check: PASS (artifacts=%d regular=%d symlinks=%d removals=%d total-bytes=%d plan-sha256=%s manifest-digest=%s inventory-digest=%s write-capable=false)\n",
			summary.Artifacts, summary.RegularFiles, summary.Symlinks, summary.Removals, summary.TotalBytes, summary.PlanSHA256, summary.ManifestDigest, summary.InventoryDigest)
	default:
		fmt.Fprintf(os.Stderr, "harness: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

func executeHarnessManifest(args []string) (harnessManifestSummary, bool, error) {
	const usage = "usage: codexkit harness manifest-check <projection.json> --verifier-registry <verifiers.tsv> [--json]"
	if len(args) < 2 || args[0] != "manifest-check" {
		return harnessManifestSummary{}, false, fmt.Errorf(usage)
	}
	manifestPath := args[1]
	registryPath := ""
	jsonOutput := false
	for index := 2; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--verifier-registry":
			if index+1 >= len(args) {
				return harnessManifestSummary{}, false, fmt.Errorf("--verifier-registry requires a value")
			}
			registryPath = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return harnessManifestSummary{}, false, fmt.Errorf("unknown harness manifest flag %q", args[index])
			}
			return harnessManifestSummary{}, false, fmt.Errorf("unexpected harness manifest argument %q", args[index])
		}
	}
	if registryPath == "" {
		return harnessManifestSummary{}, false, fmt.Errorf("--verifier-registry is required")
	}

	registryData, err := readBoundedFile(registryPath, maxVerifierRegistryBytes, "verifier registry")
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	verifierIDs, err := parseVerifierRegistry(registryData)
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	manifestData, err := readBoundedFile(manifestPath, harnessmanifest.MaxDocumentBytes, "projection manifest")
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	options := harnessmanifest.ValidationOptions{RegisteredVerifierIDs: verifierIDs}
	manifest, err := harnessmanifest.Load(manifestData, options)
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	canonical, err := harnessmanifest.Marshal(manifest, options)
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	coverage, err := harnessmanifest.ValidateCoverage(manifest)
	if err != nil {
		return harnessManifestSummary{}, false, err
	}
	digest := sha256.Sum256(canonical)
	return harnessManifestSummary{
		SchemaVersion:  manifest.SchemaVersion,
		Repo:           manifest.Repo,
		ManifestSHA256: hex.EncodeToString(digest[:]),
		Objects:        len(manifest.Objects),
		Omissions:      len(manifest.Omissions),
		CoverageInputs: coverage.TotalInputs,
		Projected:      coverage.Projected,
		Omitted:        coverage.Omitted,
		WriteCapable:   false,
	}, jsonOutput, nil
}

func readBoundedFile(path string, limit int64, label string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty", label)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, limit)
	}
	return data, nil
}

func parseVerifierRegistry(data []byte) ([]string, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = '\t'
	reader.FieldsPerRecord = 5
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse verifier registry: %w", err)
	}
	wantHeader := []string{"id", "command", "scope", "executable_in_manifest_lane", "notes"}
	if len(records) < 2 || !slices.Equal(records[0], wantHeader) {
		return nil, fmt.Errorf("parse verifier registry: header must equal %q", strings.Join(wantHeader, "\\t"))
	}
	ids := make([]string, 0, len(records)-1)
	for index, record := range records[1:] {
		if strings.TrimSpace(record[0]) == "" {
			return nil, fmt.Errorf("parse verifier registry: row %d has an empty id", index+2)
		}
		ids = append(ids, record[0])
	}
	return ids, nil
}

func executeHarnessPlan(args []string) (harnessPlanSummary, bool, error) {
	const usage = "usage: codexkit harness plan-check <plan.json> --manifest-sha256 <digest> --inventory-sha256 <digest> --expected-source-sha <sha> --now <RFC3339> [--json]"
	if len(args) < 2 || args[0] != "plan-check" {
		return harnessPlanSummary{}, false, fmt.Errorf(usage)
	}
	planPath := args[1]
	values := map[string]string{}
	jsonOutput := false
	for index := 2; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--manifest-sha256", "--inventory-sha256", "--expected-source-sha", "--now":
			if index+1 >= len(args) {
				return harnessPlanSummary{}, false, fmt.Errorf("%s requires a value", args[index])
			}
			values[args[index]] = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return harnessPlanSummary{}, false, fmt.Errorf("unknown harness plan flag %q", args[index])
			}
			return harnessPlanSummary{}, false, fmt.Errorf("unexpected harness plan argument %q", args[index])
		}
	}
	for _, required := range []string{"--manifest-sha256", "--inventory-sha256", "--expected-source-sha", "--now"} {
		if values[required] == "" {
			return harnessPlanSummary{}, false, fmt.Errorf("%s is required", required)
		}
	}
	evaluationTime, err := time.Parse(time.RFC3339, values["--now"])
	if err != nil {
		return harnessPlanSummary{}, false, fmt.Errorf("parse --now: %w", err)
	}
	data, err := readBoundedFile(planPath, harnessplan.MaxDocumentBytes, "harness plan")
	if err != nil {
		return harnessPlanSummary{}, false, err
	}
	options := harnessplan.ValidationOptions{
		ExpectedManifestDigest:  values["--manifest-sha256"],
		ExpectedInventoryDigest: values["--inventory-sha256"],
		ExpectedSourceSHA:       values["--expected-source-sha"],
		EvaluationTime:          evaluationTime,
	}
	envelope, err := harnessplan.Load(data, options)
	if err != nil {
		return harnessPlanSummary{}, false, err
	}
	summary := harnessPlanSummary{
		SchemaVersion: envelope.SchemaVersion,
		PlanID:        envelope.Plan.ID,
		PlanSHA256:    envelope.PlanSHA256,
		TargetRepo:    envelope.Plan.TargetRepository,
		Sources:       len(envelope.Plan.SourceBindings),
		Operations:    len(envelope.Plan.Operations),
		WriteCapable:  false,
	}
	for _, operation := range envelope.Plan.Operations {
		switch operation.Kind {
		case harnessplan.OperationCreate:
			summary.Creates++
		case harnessplan.OperationReplaceOwned:
			summary.Replaces++
		case harnessplan.OperationRemoveTombstone:
			summary.Removes++
		}
	}
	return summary, jsonOutput, nil
}
