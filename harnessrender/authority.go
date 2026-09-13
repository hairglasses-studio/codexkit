package harnessrender

import (
	"fmt"
	"slices"
	"sort"

	"github.com/hairglasses-studio/codexkit/harnessmanifest"
	"github.com/hairglasses-studio/codexkit/harnessplan"
	"github.com/hairglasses-studio/codexkit/sourceref"
)

type authorityState struct {
	authority  Authority
	objects    map[string]harnessmanifest.Object
	blobLimit  int64
	totalLimit int64
}

// ValidateAuthority validates every material-independent input and trust
// binding required by Build. It is pure: it performs no filesystem, process,
// network, callback, or material-byte access.
func ValidateAuthority(manifest harnessmanifest.Manifest, inventory sourceref.Inventory, envelope harnessplan.PlanEnvelope, options Options) (Authority, error) {
	state, err := validateAuthority(manifest, inventory, envelope, options)
	if err != nil {
		return Authority{}, err
	}
	return state.authority, nil
}

func validateAuthority(manifest harnessmanifest.Manifest, inventory sourceref.Inventory, envelope harnessplan.PlanEnvelope, options Options) (authorityState, error) {
	blobLimit, totalLimit, err := validateOptions(options)
	if err != nil {
		return authorityState{}, err
	}

	manifestBytes, err := harnessmanifest.Marshal(manifest, harnessmanifest.ValidationOptions{
		RegisteredVerifierIDs: slices.Clone(options.RegisteredVerifierIDs),
	})
	if err != nil {
		return authorityState{}, wrapInvalid("validate canonical manifest", err)
	}
	manifestDigest := digestBytes(manifestBytes)

	inventoryOptions := sourceref.Options{Now: options.Trust.EvaluationTime, MaxAge: options.InventoryMaxAge}
	inventoryBytes, err := sourceref.Marshal(inventory, inventoryOptions)
	if err != nil {
		return authorityState{}, wrapInvalid("validate canonical source inventory", err)
	}
	inventoryDigest := digestBytes(inventoryBytes)

	if envelope.PlanSHA256 != options.Trust.ReviewedPlanSHA256 {
		return authorityState{}, (&issues{{
			Code:    "binding_mismatch",
			Path:    "plan.plan_sha256",
			Message: "does not match the independently reviewed plan SHA",
		}}).err()
	}
	planOptions := harnessplan.ValidationOptions{
		ExpectedManifestDigest:  manifestDigest,
		ExpectedInventoryDigest: inventoryDigest,
		ExpectedSourceSHA:       options.Trust.SourceSHA,
		EvaluationTime:          options.Trust.EvaluationTime,
	}
	if err := harnessplan.Validate(envelope, planOptions); err != nil {
		return authorityState{}, wrapInvalid("revalidate sealed plan", err)
	}

	var findings issues
	if envelope.Plan.TargetRepository != options.Trust.TargetRepository {
		findings.add("binding_mismatch", "plan.target_repository", "does not match the reviewed target repository")
	}
	if manifest.Repo != options.Trust.ManifestRepository {
		findings.add("binding_mismatch", "manifest.repo", "does not match the reviewed manifest repository")
	}

	inventoryByRepository := make(map[string]sourceref.RepositoryEvidence, len(inventory.Repositories))
	for _, evidence := range inventory.Repositories {
		inventoryByRepository[evidence.Repository] = evidence
	}
	bindingByRepository := make(map[string]harnessplan.SourceBinding, len(envelope.Plan.SourceBindings))
	for index, binding := range envelope.Plan.SourceBindings {
		base := fmt.Sprintf("plan.source_bindings[%d]", index)
		bindingByRepository[binding.Repository] = binding
		evidence, exists := inventoryByRepository[binding.Repository]
		if !exists {
			findings.add("source_evidence", base+".repository", "has no matching inventory evidence")
			continue
		}
		validateBindingEvidence(&findings, base, binding, evidence, effectiveInventoryMaxAge(options.InventoryMaxAge))
	}

	objectsByID := make(map[string]harnessmanifest.Object, len(manifest.Objects))
	expectedBindingIDs := make(map[string][]string, len(manifest.Objects))
	for objectIndex, object := range manifest.Objects {
		objectsByID[object.ID] = object
		base := fmt.Sprintf("manifest.objects[%d]", objectIndex)
		if object.ProjectionMode == harnessmanifest.ProjectionManagedBlock {
			findings.add("unsupported_projection", base+".projection_mode", "managed-block objects are unsupported until scoped ledger semantics land")
		}
		if len(object.GeneratorCommit) == 64 {
			findings.add("unsupported_commit", base+".generator_commit", "64-hex commits are unsupported in render contract v1")
		}
		seenBindingIDs := make(map[string]struct{}, len(object.Inputs))
		for inputIndex, input := range object.Inputs {
			inputBase := fmt.Sprintf("%s.inputs[%d]", base, inputIndex)
			if len(input.Commit) == 64 {
				findings.add("unsupported_commit", inputBase+".commit", "64-hex commits are unsupported in render contract v1")
			}
			if input.NonHermetic || input.NonHermeticReason != "" {
				findings.add("non_hermetic", inputBase, "non-hermetic inputs are unsupported in render contract v1")
			}
			evidence, evidenceExists := inventoryByRepository[input.Repository]
			if !evidenceExists {
				findings.add("source_evidence", inputBase+".repository", "has no matching inventory evidence")
			} else {
				validateInputEvidence(&findings, inputBase, input, evidence)
			}
			binding, bindingExists := bindingByRepository[input.Repository]
			if !bindingExists {
				findings.add("source_binding", inputBase+".repository", "has no matching plan source binding")
				continue
			}
			validateInputBinding(&findings, inputBase, input, binding)
			if _, exists := seenBindingIDs[binding.ID]; !exists {
				seenBindingIDs[binding.ID] = struct{}{}
				expectedBindingIDs[object.ID] = append(expectedBindingIDs[object.ID], binding.ID)
			}
		}
		sort.Strings(expectedBindingIDs[object.ID])
	}

	operationCounts := make(map[string]int, len(envelope.Plan.Operations))
	for _, operation := range envelope.Plan.Operations {
		operationCounts[operation.ObjectID]++
	}
	for objectIndex, object := range manifest.Objects {
		if actionableObject(object) && operationCounts[object.ID] != 1 {
			findings.add("operation_coverage", fmt.Sprintf("manifest.objects[%d].id", objectIndex), "actionable object requires exactly one plan operation")
		}
	}
	for operationIndex, operation := range envelope.Plan.Operations {
		base := fmt.Sprintf("plan.operations[%d]", operationIndex)
		object, exists := objectsByID[operation.ObjectID]
		if !exists {
			findings.add("object_binding", base+".object_id", "does not identify a manifest object")
			continue
		}
		validateOperationObject(&findings, base, operation, object, expectedBindingIDs[object.ID], options.Trust)
	}
	if err := findings.err(); err != nil {
		return authorityState{}, err
	}

	return authorityState{
		authority: Authority{
			PlanSHA256:       envelope.PlanSHA256,
			ManifestDigest:   manifestDigest,
			InventoryDigest:  inventoryDigest,
			TargetRepository: envelope.Plan.TargetRepository,
		},
		objects:    objectsByID,
		blobLimit:  blobLimit,
		totalLimit: totalLimit,
	}, nil
}

// InputMatchesSourceBinding reports whether an input has the exact repository,
// normalized remote identity, requested ref, and requested/tested commit of a
// source binding. It performs no I/O.
func InputMatchesSourceBinding(input harnessmanifest.SourceInput, binding harnessplan.SourceBinding) bool {
	return input.Repository == binding.Repository &&
		remotesEqual(input.Remote, binding.RemoteURL) &&
		input.Ref == binding.RequestedRef &&
		input.Commit == binding.RequestedSHA &&
		input.Commit == binding.TestedSHA
}
