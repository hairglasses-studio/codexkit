package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/harnessrender"
	"github.com/hairglasses-studio/codexkit/internal/harnessfs"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

const harnessRenderUsage = "usage: codexkit harness render-check <projection.json> --inventory <inventory.json> --plan <plan.json> --verifier-registry <verifiers.tsv> --source-root <dir> --reviewed-plan-sha256 <digest> --target-repo <namespace/repo> --manifest-repo-id <id> --marker-owner <id> --expected-source-sha <sha> --now <RFC3339> --max-age <duration> [--rendered <object-id>=<relative-path>]... [--json]"

type harnessRenderSummary struct {
	PlanSHA256      string `json:"plan_sha256"`
	ManifestDigest  string `json:"manifest_digest"`
	InventoryDigest string `json:"inventory_digest"`
	Artifacts       int    `json:"artifacts"`
	RegularFiles    int    `json:"regular_files"`
	Symlinks        int    `json:"symlinks"`
	Removals        int    `json:"removals"`
	TotalBytes      int64  `json:"total_bytes"`
	WriteCapable    bool   `json:"write_capable"`
}

type harnessRenderArguments struct {
	manifestPath       string
	inventoryPath      string
	planPath           string
	verifierRegistry   string
	sourceRoot         string
	reviewedPlanSHA256 string
	targetRepository   string
	manifestRepository string
	markerOwner        string
	expectedSourceSHA  string
	now                time.Time
	maxAge             time.Duration
	rendered           map[string]string
	jsonOutput         bool
}

func executeHarnessRender(args []string) (harnessRenderSummary, bool, error) {
	parsed, err := parseHarnessRenderArguments(args)
	if err != nil {
		return harnessRenderSummary{}, false, err
	}

	registryData, err := readHarnessRenderDocument(parsed.verifierRegistry, maxVerifierRegistryBytes, "verifier registry")
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	verifierIDs, err := parseVerifierRegistry(registryData)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	manifestData, err := readHarnessRenderDocument(parsed.manifestPath, harnessmanifest.MaxDocumentBytes, "projection manifest")
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	manifestOptions := harnessmanifest.ValidationOptions{RegisteredVerifierIDs: verifierIDs}
	manifest, err := harnessmanifest.Load(manifestData, manifestOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	canonicalManifest, err := harnessmanifest.Marshal(manifest, manifestOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}

	inventoryData, err := readHarnessRenderDocument(parsed.inventoryPath, sourceref.MaxDocumentBytes, "source inventory")
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	inventoryOptions := sourceref.Options{Now: parsed.now, MaxAge: parsed.maxAge}
	inventory, err := sourceref.Load(inventoryData, inventoryOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	canonicalInventory, err := sourceref.Marshal(inventory, inventoryOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}

	planData, err := readHarnessRenderDocument(parsed.planPath, harnessplan.MaxDocumentBytes, "harness plan")
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	planOptions := harnessplan.ValidationOptions{
		ExpectedManifestDigest:  renderDigest(canonicalManifest),
		ExpectedInventoryDigest: renderDigest(canonicalInventory),
		ExpectedSourceSHA:       parsed.expectedSourceSHA,
		EvaluationTime:          parsed.now,
	}
	envelope, err := harnessplan.Load(planData, planOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	if envelope.PlanSHA256 != parsed.reviewedPlanSHA256 {
		return harnessRenderSummary{}, parsed.jsonOutput, fmt.Errorf("plan SHA does not match the independently reviewed plan SHA")
	}
	if envelope.Plan.TargetRepository != parsed.targetRepository {
		return harnessRenderSummary{}, parsed.jsonOutput, fmt.Errorf("plan target repository does not match the independently reviewed target")
	}
	if manifest.Repo != parsed.manifestRepository {
		return harnessRenderSummary{}, parsed.jsonOutput, fmt.Errorf("manifest repository does not match the independently reviewed manifest identity")
	}
	renderOptions := harnessrender.Options{
		Trust: harnessrender.Trust{
			ReviewedPlanSHA256: parsed.reviewedPlanSHA256,
			TargetRepository:   parsed.targetRepository,
			ManifestRepository: parsed.manifestRepository,
			MarkerOwner:        parsed.markerOwner,
			SourceSHA:          parsed.expectedSourceSHA,
			EvaluationTime:     parsed.now,
		},
		RegisteredVerifierIDs: verifierIDs,
		InventoryMaxAge:       parsed.maxAge,
	}
	if _, err := harnessrender.ValidateAuthority(manifest, inventory, envelope, renderOptions); err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	if err := validateHarnessRenderSingleSourceRoot(manifest, envelope); err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}

	rootPath, err := filepath.Abs(parsed.sourceRoot)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, fmt.Errorf("source root is not a safe contained directory")
	}
	root, err := harnessfs.Open(rootPath)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, fmt.Errorf("source root is not a safe contained directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	materials, err := readHarnessRenderMaterials(root, manifest, envelope, parsed.rendered)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}
	result, err := harnessrender.Build(manifest, inventory, envelope, materials, renderOptions)
	if err != nil {
		return harnessRenderSummary{}, parsed.jsonOutput, err
	}

	summary := harnessRenderSummary{
		PlanSHA256:      result.PlanSHA256,
		ManifestDigest:  result.ManifestDigest,
		InventoryDigest: result.InventoryDigest,
		Artifacts:       len(result.Artifacts),
		WriteCapable:    false,
	}
	for _, artifact := range result.Artifacts {
		summary.TotalBytes += int64(len(artifact.RegularBytes))
		switch {
		case artifact.Kind == harnessplan.OperationRemoveTombstone:
			summary.Removals++
		case artifact.After.Type == harnessplan.FileRegular:
			summary.RegularFiles++
		case artifact.After.Type == harnessplan.FileRelativeSymlink:
			summary.Symlinks++
		}
	}
	return summary, parsed.jsonOutput, nil
}

func parseHarnessRenderArguments(args []string) (harnessRenderArguments, error) {
	if len(args) < 2 || args[0] != "render-check" || strings.HasPrefix(args[1], "-") {
		return harnessRenderArguments{}, fmt.Errorf("%s", harnessRenderUsage)
	}
	result := harnessRenderArguments{manifestPath: args[1], rendered: map[string]string{}}
	values := map[string]*string{
		"--inventory":            &result.inventoryPath,
		"--plan":                 &result.planPath,
		"--verifier-registry":    &result.verifierRegistry,
		"--source-root":          &result.sourceRoot,
		"--reviewed-plan-sha256": &result.reviewedPlanSHA256,
		"--target-repo":          &result.targetRepository,
		"--manifest-repo-id":     &result.manifestRepository,
		"--marker-owner":         &result.markerOwner,
		"--expected-source-sha":  &result.expectedSourceSHA,
	}
	requiredValueFlags := []string{
		"--inventory", "--plan", "--verifier-registry", "--source-root",
		"--reviewed-plan-sha256", "--target-repo", "--manifest-repo-id",
		"--marker-owner", "--expected-source-sha",
	}
	seen := map[string]struct{}{}
	nowValue := ""
	maxAgeValue := ""
	for index := 2; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--json":
			if result.jsonOutput {
				return harnessRenderArguments{}, fmt.Errorf("--json may be specified only once")
			}
			result.jsonOutput = true
		case "--rendered":
			if index+1 >= len(args) {
				return harnessRenderArguments{}, fmt.Errorf("--rendered requires object-id=relative-path")
			}
			mapping := args[index+1]
			index++
			objectID, relativePath, ok := strings.Cut(mapping, "=")
			if !ok || objectID == "" || relativePath == "" || strings.Contains(relativePath, "=") {
				return harnessRenderArguments{}, fmt.Errorf("--rendered requires object-id=relative-path")
			}
			if _, exists := result.rendered[objectID]; exists {
				return harnessRenderArguments{}, fmt.Errorf("--rendered duplicates object id %q", objectID)
			}
			if err := harnessfs.ValidatePortableRelativePath(relativePath); err != nil {
				return harnessRenderArguments{}, fmt.Errorf("--rendered path for %q must be portable and relative", objectID)
			}
			result.rendered[objectID] = relativePath
		case "--now", "--max-age":
			if index+1 >= len(args) {
				return harnessRenderArguments{}, fmt.Errorf("%s requires a value", flag)
			}
			if _, exists := seen[flag]; exists {
				return harnessRenderArguments{}, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = struct{}{}
			if flag == "--now" {
				nowValue = args[index+1]
			} else {
				maxAgeValue = args[index+1]
			}
			index++
		default:
			destination, known := values[flag]
			if !known {
				if strings.HasPrefix(flag, "-") {
					return harnessRenderArguments{}, fmt.Errorf("unknown harness render flag %q", flag)
				}
				return harnessRenderArguments{}, fmt.Errorf("unexpected harness render argument %q", flag)
			}
			if index+1 >= len(args) {
				return harnessRenderArguments{}, fmt.Errorf("%s requires a value", flag)
			}
			if _, exists := seen[flag]; exists {
				return harnessRenderArguments{}, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = struct{}{}
			*destination = args[index+1]
			index++
		}
	}
	for _, flag := range requiredValueFlags {
		destination := values[flag]
		if *destination == "" {
			return harnessRenderArguments{}, fmt.Errorf("%s is required", flag)
		}
	}
	if nowValue == "" {
		return harnessRenderArguments{}, fmt.Errorf("--now is required")
	}
	if maxAgeValue == "" {
		return harnessRenderArguments{}, fmt.Errorf("--max-age is required")
	}
	var err error
	result.now, err = time.Parse(time.RFC3339, nowValue)
	if err != nil {
		return harnessRenderArguments{}, fmt.Errorf("parse --now: %w", err)
	}
	result.maxAge, err = time.ParseDuration(maxAgeValue)
	if err != nil || result.maxAge <= 0 {
		return harnessRenderArguments{}, fmt.Errorf("parse --max-age: must be a positive duration")
	}
	return result, nil
}

func readHarnessRenderDocument(path string, limit int64, label string) ([]byte, error) {
	data, err := readBoundedFile(path, limit, label)
	if err != nil {
		// readBoundedFile preserves the os.PathError for other commands. This
		// trust-checking surface deliberately emits only a path-free label.
		return nil, fmt.Errorf("%s is unavailable or invalid", label)
	}
	return data, nil
}

func validateHarnessRenderSingleSourceRoot(manifest harnessmanifest.Manifest, envelope harnessplan.PlanEnvelope) error {
	bindings := make(map[string]harnessplan.SourceBinding, len(envelope.Plan.SourceBindings))
	for _, binding := range envelope.Plan.SourceBindings {
		bindings[binding.ID] = binding
	}
	primary, exists := bindings[envelope.Plan.PrimarySourceBinding]
	if !exists {
		return fmt.Errorf("plan primary source binding is unavailable")
	}
	objects := make(map[string]harnessmanifest.Object, len(manifest.Objects))
	for _, object := range manifest.Objects {
		objects[object.ID] = object
	}
	for _, operation := range envelope.Plan.Operations {
		object, exists := objects[operation.ObjectID]
		if !exists {
			return fmt.Errorf("plan operation references an unknown manifest object")
		}
		if len(operation.SourceBindingIDs) != 1 || operation.SourceBindingIDs[0] != primary.ID {
			return fmt.Errorf("operated object %q is not bound exclusively to the primary source", object.ID)
		}
		for _, input := range object.Inputs {
			if !harnessrender.InputMatchesSourceBinding(input, primary) {
				return fmt.Errorf("operated object %q has an input outside the exact primary source identity", object.ID)
			}
		}
	}
	return nil
}

func readHarnessRenderMaterials(root *harnessfs.Root, manifest harnessmanifest.Manifest, envelope harnessplan.PlanEnvelope, rendered map[string]string) ([]harnessrender.Materials, error) {
	objects := make(map[string]harnessmanifest.Object, len(manifest.Objects))
	for _, object := range manifest.Objects {
		objects[object.ID] = object
	}
	requiredRendered := map[string]struct{}{}
	for _, operation := range envelope.Plan.Operations {
		object, exists := objects[operation.ObjectID]
		if !exists {
			return nil, fmt.Errorf("plan operation references an unknown manifest object")
		}
		if object.ProjectionMode == harnessmanifest.ProjectionRender {
			requiredRendered[object.ID] = struct{}{}
		}
	}
	requiredRenderedIDs := make([]string, 0, len(requiredRendered))
	for objectID := range requiredRendered {
		requiredRenderedIDs = append(requiredRenderedIDs, objectID)
	}
	sort.Strings(requiredRenderedIDs)
	for _, objectID := range requiredRenderedIDs {
		if _, exists := rendered[objectID]; !exists {
			return nil, fmt.Errorf("--rendered is required for render object %q", objectID)
		}
	}
	renderedIDs := make([]string, 0, len(rendered))
	for objectID := range rendered {
		renderedIDs = append(renderedIDs, objectID)
	}
	sort.Strings(renderedIDs)
	for _, objectID := range renderedIDs {
		if _, exists := requiredRendered[objectID]; !exists {
			return nil, fmt.Errorf("--rendered mapping for %q is not required by the plan", objectID)
		}
	}

	materials := make([]harnessrender.Materials, 0, len(envelope.Plan.Operations))
	var totalBytes int64
	for _, operation := range envelope.Plan.Operations {
		object := objects[operation.ObjectID]
		material := harnessrender.Materials{ObjectID: object.ID, Inputs: make([][]byte, 0, len(object.Inputs))}
		for _, input := range object.Inputs {
			content, err := readHarnessRenderMaterial(root, input.Path, &totalBytes)
			if err != nil {
				return nil, fmt.Errorf("read source material %q: %w", input.Path, err)
			}
			material.Inputs = append(material.Inputs, content)
		}
		if renderedPath, exists := rendered[object.ID]; exists {
			content, err := readHarnessRenderMaterial(root, renderedPath, &totalBytes)
			if err != nil {
				return nil, fmt.Errorf("read rendered material for %q: %w", object.ID, err)
			}
			material.Rendered = &harnessrender.RenderedPayload{Bytes: content}
		}
		materials = append(materials, material)
	}
	sort.Slice(materials, func(i, j int) bool { return materials[i].ObjectID < materials[j].ObjectID })
	return materials, nil
}

func readHarnessRenderMaterial(root *harnessfs.Root, relativePath string, totalBytes *int64) ([]byte, error) {
	if err := harnessfs.ValidatePortableRelativePath(relativePath); err != nil {
		return nil, fmt.Errorf("path is not portable and relative")
	}
	content, _, err := root.ReadRegular(relativePath, harnessrender.MaxMaterialBlobBytes)
	if err != nil {
		return nil, err
	}
	if *totalBytes > harnessrender.MaxAggregateMaterialBytes-int64(len(content)) {
		return nil, fmt.Errorf("aggregate material bytes exceed %d", harnessrender.MaxAggregateMaterialBytes)
	}
	*totalBytes += int64(len(content))
	return content, nil
}

func renderDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
