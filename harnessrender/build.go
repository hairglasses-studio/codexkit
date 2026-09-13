package harnessrender

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

var (
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Build revalidates all reviewed values, exact source evidence, ordered input
// blobs, and plan/object transitions before returning inline artifacts. It
// never mutates caller-owned values or obtains authority from outside them.
func Build(manifest harnessmanifest.Manifest, inventory sourceref.Inventory, envelope harnessplan.PlanEnvelope, materials []Materials, options Options) (Result, error) {
	state, err := validateAuthority(manifest, inventory, envelope, options)
	if err != nil {
		return Result{}, err
	}
	objectsByID := state.objects
	if err := validateMaterialCardinality(materials, envelope.Plan.Operations, objectsByID); err != nil {
		return Result{}, err
	}

	var findings issues
	materialByObject := indexMaterials(&findings, materials, envelope.Plan.Operations, objectsByID, state.blobLimit, state.totalLimit)
	artifacts := make([]Artifact, 0, len(envelope.Plan.Operations))
	for operationIndex, operation := range envelope.Plan.Operations {
		base := fmt.Sprintf("plan.operations[%d]", operationIndex)
		object, exists := objectsByID[operation.ObjectID]
		if !exists {
			findings.add("object_binding", base+".object_id", "does not identify a manifest object")
			continue
		}
		material, exists := materialByObject[object.ID]
		if !exists {
			findings.add("material", base, "requires exactly one material entry for its object")
			continue
		}
		validateInputMaterials(&findings, base+".materials", object, material)

		artifact, ok := materializeOperation(&findings, base, operation, object, material, options.Trust)
		if ok {
			artifacts = append(artifacts, artifact)
		}
	}
	if err := findings.err(); err != nil {
		return Result{}, err
	}

	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Destination != artifacts[j].Destination {
			return artifacts[i].Destination < artifacts[j].Destination
		}
		return artifacts[i].OperationID < artifacts[j].OperationID
	})
	return Result{
		PlanSHA256:       state.authority.PlanSHA256,
		ManifestDigest:   state.authority.ManifestDigest,
		InventoryDigest:  state.authority.InventoryDigest,
		TargetRepository: state.authority.TargetRepository,
		Artifacts:        artifacts,
	}, nil
}

func validateOptions(options Options) (int64, int64, error) {
	var findings issues
	trust := options.Trust
	if !digestPattern.MatchString(trust.ReviewedPlanSHA256) {
		findings.add("digest", "options.trust.reviewed_plan_sha256", "must be a lowercase sha256 digest")
	}
	if strings.TrimSpace(trust.TargetRepository) == "" {
		findings.add("repository", "options.trust.target_repository", "is required")
	}
	if !identifierPattern.MatchString(trust.ManifestRepository) {
		findings.add("identifier", "options.trust.manifest_repository", "must be a stable manifest repository identifier")
	}
	if !identifierPattern.MatchString(trust.MarkerOwner) {
		findings.add("identifier", "options.trust.marker_owner", "must be a stable marker owner identifier")
	}
	if !shaPattern.MatchString(trust.SourceSHA) || trust.SourceSHA == strings.Repeat("0", 40) {
		findings.add("sha", "options.trust.source_sha", "must be a lowercase nonzero 40-character Git SHA")
	}
	if trust.EvaluationTime.IsZero() {
		findings.add("time", "options.trust.evaluation_time", "is required")
	}
	if options.InventoryMaxAge < 0 {
		findings.add("bounds", "options.inventory_max_age", "must not be negative")
	}
	blobLimit := options.MaxBlobBytes
	if blobLimit == 0 {
		blobLimit = MaxMaterialBlobBytes
	}
	if blobLimit < 0 || blobLimit > MaxMaterialBlobBytes {
		findings.add("bounds", "options.max_blob_bytes", fmt.Sprintf("must be between 1 and %d bytes, or zero for the default", MaxMaterialBlobBytes))
	}
	totalLimit := options.MaxTotalBytes
	if totalLimit == 0 {
		totalLimit = MaxAggregateMaterialBytes
	}
	if totalLimit < 0 || totalLimit > MaxAggregateMaterialBytes {
		findings.add("bounds", "options.max_total_bytes", fmt.Sprintf("must be between 1 and %d bytes, or zero for the default", MaxAggregateMaterialBytes))
	}
	if blobLimit > 0 && totalLimit > 0 && blobLimit > totalLimit {
		findings.add("bounds", "options.max_blob_bytes", "must not exceed max_total_bytes")
	}
	if err := findings.err(); err != nil {
		return 0, 0, err
	}
	return blobLimit, totalLimit, nil
}

func effectiveInventoryMaxAge(value time.Duration) time.Duration {
	if value == 0 {
		return sourceref.DefaultMaxAge
	}
	return value
}

func validateBindingEvidence(findings *issues, base string, binding harnessplan.SourceBinding, evidence sourceref.RepositoryEvidence, maxAge time.Duration) {
	if evidence.EvidenceClass != sourceref.EvidenceRemoteTree && evidence.EvidenceClass != sourceref.EvidenceFreshWorktree {
		findings.add("ineligible_source", base+".evidence_class", "inventory evidence must be remote-tree or fresh-worktree")
	}
	if evidence.EvidenceClass == sourceref.EvidenceFreshWorktree && evidence.Checkout != nil && evidence.Checkout.Dirty {
		findings.add("ineligible_source", base+".checkout_dirty", "fresh-worktree inventory evidence must be clean")
	}
	expectedClass := harnessplan.EvidenceClass(evidence.EvidenceClass)
	if binding.Repository != evidence.Repository {
		findings.add("binding_mismatch", base+".repository", "does not equal inventory repository")
	}
	if !remotesEqual(binding.RemoteURL, evidence.RemoteURL) {
		findings.add("binding_mismatch", base+".remote_url", "does not identify the same normalized remote as inventory remote_url")
	}
	if binding.RequestedRef != evidence.RequestedRef {
		findings.add("binding_mismatch", base+".requested_ref", "does not exactly equal inventory requested_ref")
	}
	if binding.RequestedSHA != evidence.RequestedSHA {
		findings.add("binding_mismatch", base+".requested_sha", "does not exactly equal inventory requested_sha")
	}
	if binding.TestedRef != evidence.TestedRef {
		findings.add("binding_mismatch", base+".tested_ref", "does not exactly equal inventory tested_ref")
	}
	if binding.TestedSHA != evidence.TestedSHA {
		findings.add("binding_mismatch", base+".tested_sha", "does not exactly equal inventory tested_sha")
	}
	if binding.EvidenceClass != expectedClass {
		findings.add("binding_mismatch", base+".evidence_class", "does not equal inventory evidence_class")
	}
	dirty := evidence.Checkout != nil && evidence.Checkout.Dirty
	if binding.CheckoutDirty != dirty {
		findings.add("binding_mismatch", base+".checkout_dirty", "does not equal inventory checkout dirty state")
	}
	if !binding.ObservedAt.Equal(evidence.ObservedAt) {
		findings.add("binding_mismatch", base+".observed_at", "does not equal inventory observed_at")
	}
	latestValidUntil := evidence.ObservedAt.UTC().Round(0).Add(maxAge)
	if binding.ValidUntil.UTC().Round(0).After(latestValidUntil) {
		findings.add("evidence_window", base+".valid_until", "extends beyond the inventory freshness window")
	}
}

func validateInputEvidence(findings *issues, base string, input harnessmanifest.SourceInput, evidence sourceref.RepositoryEvidence) {
	if evidence.EvidenceClass != sourceref.EvidenceRemoteTree && evidence.EvidenceClass != sourceref.EvidenceFreshWorktree {
		findings.add("ineligible_source", base+".repository", "must bind to remote-tree or clean fresh-worktree evidence")
	}
	if evidence.EvidenceClass == sourceref.EvidenceFreshWorktree && evidence.Checkout != nil && evidence.Checkout.Dirty {
		findings.add("ineligible_source", base+".repository", "must bind to a clean fresh-worktree")
	}
	if input.Repository != evidence.Repository {
		findings.add("binding_mismatch", base+".repository", "does not exactly equal inventory repository")
	}
	if !remotesEqual(input.Remote, evidence.RemoteURL) {
		findings.add("binding_mismatch", base+".remote", "does not identify the same normalized remote as inventory remote_url")
	}
	if input.Ref != evidence.RequestedRef {
		findings.add("binding_mismatch", base+".ref", "does not exactly equal inventory requested_ref")
	}
	if input.Commit != evidence.RequestedSHA || input.Commit != evidence.TestedSHA {
		findings.add("binding_mismatch", base+".commit", "does not equal inventory requested_sha and tested_sha")
	}
}

func validateInputBinding(findings *issues, base string, input harnessmanifest.SourceInput, binding harnessplan.SourceBinding) {
	if input.Repository != binding.Repository {
		findings.add("binding_mismatch", base+".repository", "does not exactly equal plan source binding repository")
	}
	if !remotesEqual(input.Remote, binding.RemoteURL) {
		findings.add("binding_mismatch", base+".remote", "does not identify the same normalized remote as the plan source binding")
	}
	if input.Ref != binding.RequestedRef {
		findings.add("binding_mismatch", base+".ref", "does not exactly equal plan source binding requested_ref")
	}
	if input.Commit != binding.RequestedSHA || input.Commit != binding.TestedSHA {
		findings.add("binding_mismatch", base+".commit", "does not equal plan source binding requested_sha and tested_sha")
	}
}

func actionableObject(object harnessmanifest.Object) bool {
	switch object.ProjectionMode {
	case harnessmanifest.ProjectionCopy, harnessmanifest.ProjectionRender, harnessmanifest.ProjectionRelativeSymlink, harnessmanifest.ProjectionManagedBlock:
		return true
	case harnessmanifest.ProjectionNone:
		return object.Tombstone
	default:
		return false
	}
}

// validateMaterialCardinality is intentionally allocation-free. It rejects
// amplification before indexMaterials allocates maps or walks blob entries.
func validateMaterialCardinality(materials []Materials, operations []harnessplan.Operation, objects map[string]harnessmanifest.Object) error {
	var findings issues
	if len(materials) > harnessplan.MaxOperations {
		findings.add("bounds", "materials", fmt.Sprintf("must contain at most %d entries", harnessplan.MaxOperations))
	}
	if len(materials) > len(operations) {
		findings.add("bounds", "materials", "must not contain more entries than plan operations")
	}
	if len(findings) != 0 {
		return findings.err()
	}
	var totalInputs int
	for index, material := range materials {
		base := fmt.Sprintf("materials[%d].inputs", index)
		inputCount := len(material.Inputs)
		if inputCount > MaxMaterialInputsPerObject {
			findings.add("bounds", base, fmt.Sprintf("must contain at most %d entries", MaxMaterialInputsPerObject))
			continue
		}
		if object, exists := objects[material.ObjectID]; exists && inputCount > len(object.Inputs) {
			findings.add("bounds", base, "must not exceed the matching manifest object input count")
			continue
		}
		if totalInputs > MaxAggregateMaterialInputs-inputCount {
			findings.add("bounds", "materials", fmt.Sprintf("aggregate input entries must not exceed %d", MaxAggregateMaterialInputs))
			continue
		}
		totalInputs += inputCount
	}
	return findings.err()
}

func indexMaterials(findings *issues, materials []Materials, operations []harnessplan.Operation, objects map[string]harnessmanifest.Object, blobLimit, totalLimit int64) map[string]Materials {
	operatedObjects := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operatedObjects[operation.ObjectID] = struct{}{}
	}
	indexed := make(map[string]Materials, len(materials))
	var total int64
	for materialIndex, material := range materials {
		base := fmt.Sprintf("materials[%d]", materialIndex)
		if _, exists := indexed[material.ObjectID]; exists {
			findings.add("duplicate", base+".object_id", "duplicates a material object identifier")
		} else {
			indexed[material.ObjectID] = material
		}
		if _, exists := operatedObjects[material.ObjectID]; !exists {
			findings.add("unexpected_material", base+".object_id", "does not identify an operated manifest object")
		}
		object, objectExists := objects[material.ObjectID]
		if len(material.Inputs) > MaxMaterialInputsPerObject || objectExists && len(material.Inputs) > len(object.Inputs) {
			// validateMaterialCardinality rejects this before indexMaterials is
			// called. Keep this guard local so future callers cannot amplify work.
			continue
		}
		for inputIndex, input := range material.Inputs {
			total = addMaterialSize(findings, fmt.Sprintf("%s.inputs[%d]", base, inputIndex), int64(len(input)), total, blobLimit, totalLimit)
		}
		if material.Rendered != nil {
			total = addMaterialSize(findings, base+".rendered.bytes", int64(len(material.Rendered.Bytes)), total, blobLimit, totalLimit)
		}
	}
	return indexed
}

func addMaterialSize(findings *issues, field string, size, total, blobLimit, totalLimit int64) int64 {
	if size > blobLimit {
		findings.add("bounds", field, fmt.Sprintf("must not exceed %d bytes", blobLimit))
	}
	if total > totalLimit-size {
		findings.add("bounds", "materials", fmt.Sprintf("aggregate material bytes must not exceed %d", totalLimit))
		return totalLimit
	}
	return total + size
}

func validateInputMaterials(findings *issues, base string, object harnessmanifest.Object, material Materials) {
	if len(material.Inputs) != len(object.Inputs) {
		findings.add("input_material", base+".inputs", fmt.Sprintf("contains %d blobs; want %d ordered object inputs", len(material.Inputs), len(object.Inputs)))
	}
	if len(material.Inputs) > MaxMaterialInputsPerObject || len(material.Inputs) > len(object.Inputs) {
		return
	}
	limit := min(len(material.Inputs), len(object.Inputs))
	for index := 0; index < limit; index++ {
		if actual := digestBytes(material.Inputs[index]); actual != object.Inputs[index].BlobHash {
			findings.add("blob_hash", fmt.Sprintf("%s.inputs[%d]", base, index), "does not match the ordered manifest input blob_hash")
		}
	}
}

func validateOperationObject(findings *issues, base string, operation harnessplan.Operation, object harnessmanifest.Object, bindingIDs []string, trust Trust) {
	if operation.Destination != object.Destination {
		findings.add("binding_mismatch", base+".destination", "does not equal the manifest object destination")
	}
	actualBindingIDs := slices.Clone(operation.SourceBindingIDs)
	sort.Strings(actualBindingIDs)
	if !slices.Equal(actualBindingIDs, bindingIDs) {
		findings.add("binding_mismatch", base+".source_binding_ids", "must exactly equal the source bindings for the object's ordered inputs")
	}

	validateStateMarker(findings, base+".before.marker", operation.ObjectID, operation.Before, trust.MarkerOwner)
	validateStateMarker(findings, base+".after.marker", operation.ObjectID, operation.After, trust.MarkerOwner)

	if operation.Kind == harnessplan.OperationReplaceOwned && object.ConflictPolicy != harnessmanifest.ConflictReplaceOwned {
		findings.add("conflict_policy", base+".kind", "replace-owned requires manifest conflict_policy replace-owned")
	}
	if operation.Kind == harnessplan.OperationRemoveTombstone {
		if !object.Tombstone || object.RemovalPolicy != harnessmanifest.RemovalTombstoneOnly || object.ProjectionMode != harnessmanifest.ProjectionNone {
			findings.add("tombstone", base+".kind", "remove-tombstone requires the exact manifest tombstone object")
		}
		return
	}
	if object.Tombstone {
		findings.add("tombstone", base+".kind", "a manifest tombstone may only use remove-tombstone")
	}

	switch object.ProjectionMode {
	case harnessmanifest.ProjectionCopy, harnessmanifest.ProjectionRender:
		if operation.After.Type != harnessplan.FileRegular {
			findings.add("file_type", base+".after.type", "must be regular-file for copy or render projection")
		}
		if operation.After.Mode != object.Mode {
			findings.add("binding_mismatch", base+".after.mode", "does not equal the manifest object mode")
		}
		if operation.After.Hash != object.ProjectedHash {
			findings.add("binding_mismatch", base+".after.hash", "does not equal the manifest projected_hash")
		}
		if operation.Payload == nil || operation.Payload.Hash != object.ProjectedHash {
			findings.add("binding_mismatch", base+".payload", "must bind the manifest projected_hash")
		}
		if object.ProjectionMode == harnessmanifest.ProjectionRender && object.GeneratorCommit != trust.SourceSHA {
			findings.add("binding_mismatch", base+".generator_commit", "render generator_commit must equal the reviewed source SHA")
		}
	case harnessmanifest.ProjectionRelativeSymlink:
		if operation.After.Type != harnessplan.FileRelativeSymlink {
			findings.add("file_type", base+".after.type", "must be relative-symlink for a relative-symlink projection")
		}
		if operation.After.SymlinkTarget != object.SymlinkTarget {
			findings.add("binding_mismatch", base+".after.symlink_target", "does not equal the manifest symlink_target")
		}
		if operation.After.Hash != object.ProjectedHash {
			findings.add("binding_mismatch", base+".after.hash", "does not equal the manifest projected_hash")
		}
		if digestBytes([]byte(object.SymlinkTarget)) != object.ProjectedHash {
			findings.add("projected_hash", base+".after.symlink_target", "does not hash to the manifest projected_hash")
		}
	case harnessmanifest.ProjectionManagedBlock:
		findings.add("unsupported_projection", base+".object_id", "managed-block operations are unsupported until scoped ledger semantics land")
	case harnessmanifest.ProjectionReference, harnessmanifest.ProjectionNone:
		findings.add("non_materialized", base+".object_id", "reference and non-tombstone none objects must not have plan operations")
	default:
		findings.add("unsupported_projection", base+".object_id", "contains an unsupported projection mode")
	}
}

func validateStateMarker(findings *issues, field, objectID string, state harnessplan.FileState, owner string) {
	if state.Type == harnessplan.FileAbsent {
		return
	}
	if state.Marker == nil {
		findings.add("marker", field, "materialized state requires the reviewed ownership marker")
		return
	}
	if state.Marker.Owner != owner {
		findings.add("binding_mismatch", field+".owner", "does not equal the reviewed marker owner")
	}
	if state.Marker.ObjectID != objectID {
		findings.add("binding_mismatch", field+".object_id", "does not equal the operation object_id")
	}
	if state.Marker.StateHash != state.Hash {
		findings.add("binding_mismatch", field+".state_hash", "does not equal the state hash")
	}
}

func materializeOperation(findings *issues, base string, operation harnessplan.Operation, object harnessmanifest.Object, material Materials, trust Trust) (Artifact, bool) {
	artifact := Artifact{
		OperationID: operation.ID,
		ObjectID:    operation.ObjectID,
		Kind:        operation.Kind,
		Destination: operation.Destination,
		Before:      cloneFileState(operation.Before),
		After:       cloneFileState(operation.After),
	}

	switch {
	case operation.Kind == harnessplan.OperationRemoveTombstone:
		if material.Rendered != nil {
			findings.add("unexpected_material", base+".materials.rendered", "remove-tombstone must not contain rendered bytes")
		}
		return artifact, true
	case object.ProjectionMode == harnessmanifest.ProjectionCopy:
		if material.Rendered != nil {
			findings.add("unexpected_material", base+".materials.rendered", "copy projection must use its exact first input")
		}
		if len(material.Inputs) == 0 {
			return Artifact{}, false
		}
		payload := material.Inputs[0]
		validateRegularPayload(findings, base, operation, object, payload)
		artifact.RegularBytes = cloneBytes(payload)
		return artifact, true
	case object.ProjectionMode == harnessmanifest.ProjectionRender:
		if object.GeneratorCommit != trust.SourceSHA {
			return Artifact{}, false
		}
		if material.Rendered == nil {
			findings.add("material", base+".materials.rendered", "render projection requires caller-supplied rendered bytes")
			return Artifact{}, false
		}
		validateRegularPayload(findings, base, operation, object, material.Rendered.Bytes)
		artifact.RegularBytes = cloneBytes(material.Rendered.Bytes)
		return artifact, true
	case object.ProjectionMode == harnessmanifest.ProjectionRelativeSymlink:
		if material.Rendered != nil {
			findings.add("unexpected_material", base+".materials.rendered", "relative-symlink target is inline in the manifest")
		}
		return artifact, true
	case object.ProjectionMode == harnessmanifest.ProjectionManagedBlock:
		return Artifact{}, false
	default:
		return Artifact{}, false
	}
}

func validateRegularPayload(findings *issues, base string, operation harnessplan.Operation, object harnessmanifest.Object, payload []byte) {
	digest := digestBytes(payload)
	if object.ProjectionMode == harnessmanifest.ProjectionCopy && len(object.Inputs) > 0 && digest != object.Inputs[0].BlobHash {
		findings.add("copy_payload", base+".payload", "is not the exact first manifest input")
	}
	if digest != object.ProjectedHash {
		findings.add("projected_hash", base+".payload", "does not hash to the manifest projected_hash")
	}
	if operation.After.Hash != digest {
		findings.add("binding_mismatch", base+".after.hash", "does not hash the supplied payload")
	}
	if operation.Payload == nil {
		findings.add("payload", base+".payload", "regular artifact requires a payload reference")
		return
	}
	if operation.Payload.Hash != digest {
		findings.add("binding_mismatch", base+".payload.hash", "does not hash the supplied payload")
	}
	if operation.Payload.Size != int64(len(payload)) {
		findings.add("binding_mismatch", base+".payload.size", "does not equal the supplied payload size")
	}
}

func cloneFileState(state harnessplan.FileState) harnessplan.FileState {
	if state.Marker != nil {
		marker := *state.Marker
		state.Marker = &marker
	}
	return state
}

func cloneBytes(value []byte) []byte {
	return append([]byte{}, value...)
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
