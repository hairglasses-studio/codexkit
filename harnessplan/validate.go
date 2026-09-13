package harnessplan

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	identifierPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	refPattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)
	shaPattern            = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	hostPattern           = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	scpRemotePattern      = regexp.MustCompile(`^([^@/:]+)@([^/:]+):(.+)$`)
)

// Issue is one stable validation finding.
type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError contains all independently detectable plan findings.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "harness plan validation failed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", issue.Path, issue.Message))
	}
	return "harness plan validation failed: " + strings.Join(parts, "; ")
}

// Unwrap makes all structured validation failures match ErrInvalidPlan.
func (e *ValidationError) Unwrap() error { return ErrInvalidPlan }

type issues []Issue

func (v *issues) add(code, field, message string) {
	*v = append(*v, Issue{Code: code, Path: field, Message: message})
}

func (v *issues) err() error {
	if len(*v) == 0 {
		return nil
	}
	sort.SliceStable(*v, func(i, j int) bool {
		if (*v)[i].Path != (*v)[j].Path {
			return (*v)[i].Path < (*v)[j].Path
		}
		if (*v)[i].Code != (*v)[j].Code {
			return (*v)[i].Code < (*v)[j].Code
		}
		return (*v)[i].Message < (*v)[j].Message
	})
	return &ValidationError{Issues: append([]Issue(nil), (*v)...)}
}

// Validate checks the envelope, current trusted-input bindings, closed enums,
// source eligibility, exact file transitions, collisions, and plan digest.
func Validate(envelope PlanEnvelope, options ValidationOptions) error {
	var findings issues
	if envelope.SchemaVersion != SchemaVersion {
		findings.add("schema_version", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	validatePlanInto(&findings, envelope.Plan, options)
	if !validDigest(envelope.PlanSHA256) {
		findings.add("digest", "plan_sha256", "must be a lowercase sha256 digest")
	} else if expected, err := digestCanonicalPlan(envelope.Plan); err != nil {
		findings.add("digest", "plan_sha256", err.Error())
	} else if envelope.PlanSHA256 != expected {
		findings.add("digest_mismatch", "plan_sha256", "does not match the canonical plan digest")
	}
	return findings.err()
}

func validatePlan(plan Plan, options ValidationOptions) error {
	var findings issues
	validatePlanInto(&findings, plan, options)
	return findings.err()
}

func validatePlanInto(findings *issues, plan Plan, options ValidationOptions) {
	evaluationTime := options.EvaluationTime.UTC().Round(0)
	if options.EvaluationTime.IsZero() {
		findings.add("time", "validation_options.evaluation_time", "is required")
	}
	if !validIdentifier(plan.ID) {
		findings.add("identifier", "plan.id", "must be a stable identifier")
	}
	if !validRepository(plan.TargetRepository) {
		findings.add("repository", "plan.target_repository", "must be a normalized namespace/repository identity")
	}
	if !validSHA(plan.GeneratorSHA) {
		findings.add("sha", "plan.generator_sha", "must be a lowercase nonzero 40-character Git SHA")
	}
	validateTrustedDigest(findings, "plan.manifest_digest", plan.ManifestDigest, "validation_options.expected_manifest_digest", options.ExpectedManifestDigest)
	validateTrustedDigest(findings, "plan.inventory_digest", plan.InventoryDigest, "validation_options.expected_inventory_digest", options.ExpectedInventoryDigest)
	if !validSHA(plan.ExpectedSourceSHA) {
		findings.add("sha", "plan.expected_source_sha", "must be a lowercase nonzero 40-character Git SHA")
	}
	if !validSHA(options.ExpectedSourceSHA) {
		findings.add("sha", "validation_options.expected_source_sha", "must be a lowercase nonzero 40-character Git SHA")
	} else if plan.ExpectedSourceSHA != options.ExpectedSourceSHA {
		findings.add("binding_mismatch", "plan.expected_source_sha", "does not match the trusted expected source SHA")
	}
	if validSHA(plan.GeneratorSHA) && validSHA(plan.ExpectedSourceSHA) && plan.GeneratorSHA != plan.ExpectedSourceSHA {
		findings.add("binding_mismatch", "plan.generator_sha", "must equal expected_source_sha")
	}

	if len(plan.SourceBindings) == 0 || len(plan.SourceBindings) > MaxSourceBindings {
		findings.add("bounds", "plan.source_bindings", fmt.Sprintf("must contain between 1 and %d entries", MaxSourceBindings))
	}
	bindings := make(map[string]SourceBinding, len(plan.SourceBindings))
	repositories := make(map[string]int, len(plan.SourceBindings))
	for index, binding := range plan.SourceBindings {
		base := fmt.Sprintf("plan.source_bindings[%d]", index)
		validateSourceBinding(findings, base, binding, evaluationTime)
		if previous, exists := bindings[binding.ID]; exists {
			findings.add("duplicate", base+".id", fmt.Sprintf("duplicates source binding %q for %q", previous.ID, previous.Repository))
		} else {
			bindings[binding.ID] = binding
		}
		repositoryKey := strings.ToLower(binding.Repository)
		if previous, exists := repositories[repositoryKey]; exists {
			findings.add("duplicate", base+".repository", fmt.Sprintf("duplicates plan.source_bindings[%d].repository", previous))
		} else {
			repositories[repositoryKey] = index
		}
	}
	primary, primaryExists := bindings[plan.PrimarySourceBinding]
	if !validIdentifier(plan.PrimarySourceBinding) || !primaryExists {
		findings.add("binding", "plan.primary_source_binding", "must reference one declared source binding")
	} else if primary.TestedSHA != plan.ExpectedSourceSHA {
		findings.add("binding_mismatch", "plan.expected_source_sha", "must equal the primary source binding tested SHA")
	}

	if len(plan.Operations) > MaxOperations {
		findings.add("bounds", "plan.operations", fmt.Sprintf("must contain at most %d entries", MaxOperations))
	}
	operations := plan.Operations
	operationCapacity := len(operations)
	if len(operations) > MaxOperations {
		// The document is already invalid. Do not amplify work or findings by
		// walking or preallocating from an attacker-controlled over-limit
		// collection.
		operations = nil
		operationCapacity = 0
	}
	operationIDs := make(map[string]int, operationCapacity)
	objectIDs := make(map[string]int, operationCapacity)
	destinations := make(map[string]int, operationCapacity)
	var totalPayloadBytes int64
	for index, operation := range operations {
		base := fmt.Sprintf("plan.operations[%d]", index)
		validateOperation(findings, base, operation, bindings)
		if previous, exists := operationIDs[operation.ID]; exists {
			findings.add("duplicate", base+".id", fmt.Sprintf("duplicates plan.operations[%d].id", previous))
		} else {
			operationIDs[operation.ID] = index
		}
		if previous, exists := objectIDs[operation.ObjectID]; exists {
			findings.add("duplicate", base+".object_id", fmt.Sprintf("duplicates plan.operations[%d].object_id", previous))
		} else {
			objectIDs[operation.ObjectID] = index
		}
		if previous, exists := destinations[operation.Destination]; exists {
			findings.add("collision", base+".destination", fmt.Sprintf("collides with plan.operations[%d].destination", previous))
		} else {
			destinations[operation.Destination] = index
		}
		if operation.Payload != nil && operation.Payload.Size > 0 && operation.Payload.Size <= MaxPayloadBytes {
			if totalPayloadBytes > MaxTotalPayloadBytes-operation.Payload.Size {
				totalPayloadBytes = MaxTotalPayloadBytes + 1
			} else {
				totalPayloadBytes += operation.Payload.Size
			}
		}
	}
	if totalPayloadBytes > MaxTotalPayloadBytes {
		findings.add("bounds", "plan.operations", fmt.Sprintf("aggregate payload size must not exceed %d bytes", MaxTotalPayloadBytes))
	}
	for left := range operations {
		for right := left + 1; right < len(operations); right++ {
			if pathOverlaps(operations[left].Destination, operations[right].Destination) {
				findings.add("overlap", fmt.Sprintf("plan.operations[%d].destination", right), fmt.Sprintf("overlaps plan.operations[%d].destination", left))
			}
		}
	}
}

func validateTrustedDigest(findings *issues, field, value, trustedField, trusted string) {
	if !validDigest(value) {
		findings.add("digest", field, "must be a lowercase sha256 digest")
	}
	if !validDigest(trusted) {
		findings.add("digest", trustedField, "must be a lowercase sha256 digest")
	} else if value != trusted {
		findings.add("binding_mismatch", field, "does not match the trusted digest")
	}
}

func validateSourceBinding(findings *issues, base string, binding SourceBinding, evaluationTime time.Time) {
	if !validIdentifier(binding.ID) {
		findings.add("identifier", base+".id", "must be a stable identifier")
	}
	if !validRepository(binding.Repository) {
		findings.add("repository", base+".repository", "must be a normalized namespace/repository identity")
	}
	if remoteRepository, err := parseRemoteRepository(binding.RemoteURL); err != nil {
		findings.add("remote", base+".remote_url", err.Error())
	} else if remoteRepository != binding.Repository {
		findings.add("binding_mismatch", base+".remote_url", "repository path does not match source binding repository")
	}
	if !validRef(binding.RequestedRef) {
		findings.add("ref", base+".requested_ref", "must be a normalized explicit Git ref")
	}
	if !validRef(binding.TestedRef) {
		findings.add("ref", base+".tested_ref", "must be a normalized explicit Git ref")
	}
	if !validSHA(binding.RequestedSHA) {
		findings.add("sha", base+".requested_sha", "must be a lowercase nonzero 40-character Git SHA")
	}
	if !validSHA(binding.TestedSHA) {
		findings.add("sha", base+".tested_sha", "must be a lowercase nonzero 40-character Git SHA")
	}
	if validSHA(binding.RequestedSHA) && validSHA(binding.TestedSHA) && binding.RequestedSHA != binding.TestedSHA {
		findings.add("ineligible_source", base+".tested_sha", "must equal requested_sha")
	}
	switch binding.EvidenceClass {
	case EvidenceRemoteTree:
		// Dirty is contextual for remote-tree evidence and does not weaken it.
	case EvidenceFreshWorktree:
		if binding.CheckoutDirty {
			findings.add("ineligible_source", base+".checkout_dirty", "fresh-worktree evidence must be clean")
		}
	default:
		findings.add("enum", base+".evidence_class", "must be remote-tree or fresh-worktree")
	}
	if binding.ObservedAt.IsZero() {
		findings.add("time", base+".observed_at", "is required")
	}
	if binding.ValidUntil.IsZero() {
		findings.add("time", base+".valid_until", "is required")
	}
	observedAt := binding.ObservedAt.UTC().Round(0)
	validUntil := binding.ValidUntil.UTC().Round(0)
	if !binding.ObservedAt.IsZero() && !binding.ValidUntil.IsZero() && !validUntil.After(observedAt) {
		findings.add("time_window", base+".valid_until", "must be after observed_at")
	}
	if !evaluationTime.IsZero() {
		if !binding.ObservedAt.IsZero() && observedAt.After(evaluationTime) {
			findings.add("future_evidence", base+".observed_at", "must not be after the trusted evaluation time")
		}
		if !binding.ValidUntil.IsZero() && evaluationTime.After(validUntil) {
			findings.add("expired_evidence", base+".valid_until", "source evidence has expired")
		}
	}
}

func validateOperation(findings *issues, base string, operation Operation, bindings map[string]SourceBinding) {
	if !validIdentifier(operation.ID) {
		findings.add("identifier", base+".id", "must be a stable identifier")
	}
	if !validIdentifier(operation.ObjectID) {
		findings.add("identifier", base+".object_id", "must be a stable manifest object identifier")
	}
	if err := validateRelativePath(operation.Destination); err != nil {
		findings.add("path", base+".destination", err.Error())
	}
	if len(operation.SourceBindingIDs) == 0 || len(operation.SourceBindingIDs) > MaxSourceBindings {
		findings.add("bounds", base+".source_binding_ids", fmt.Sprintf("must contain between 1 and %d entries", MaxSourceBindings))
	}
	seenBindings := map[string]struct{}{}
	for index, id := range operation.SourceBindingIDs {
		field := fmt.Sprintf("%s.source_binding_ids[%d]", base, index)
		if _, exists := bindings[id]; !exists {
			findings.add("binding", field, "must reference a declared source binding")
		}
		if _, exists := seenBindings[id]; exists {
			findings.add("duplicate", field, "duplicates a source binding identifier")
		}
		seenBindings[id] = struct{}{}
	}
	validateFileState(findings, base+".before", operation.Destination, operation.ObjectID, operation.Before)
	validateFileState(findings, base+".after", operation.Destination, operation.ObjectID, operation.After)

	switch operation.Kind {
	case OperationCreate:
		if operation.Before.Type != FileAbsent || !materialized(operation.After.Type) {
			findings.add("transition", base+".kind", "create requires absent before and materialized after states")
		}
	case OperationReplaceOwned:
		if !materialized(operation.Before.Type) || !materialized(operation.After.Type) {
			findings.add("transition", base+".kind", "replace-owned requires materialized before and after states")
		}
	case OperationRemoveTombstone:
		if !materialized(operation.Before.Type) || operation.After.Type != FileAbsent {
			findings.add("transition", base+".kind", "remove-tombstone requires materialized before and absent after states")
		}
	default:
		findings.add("enum", base+".kind", "contains an unknown operation kind")
	}

	if operation.After.Type == FileRegular {
		if operation.Payload == nil {
			findings.add("payload", base+".payload", "regular-file after state requires a staged payload reference")
		} else {
			if !validDigest(operation.Payload.Hash) {
				findings.add("digest", base+".payload.hash", "must be a lowercase sha256 digest")
			} else if operation.Payload.Hash != operation.After.Hash {
				findings.add("binding_mismatch", base+".payload.hash", "must equal the after-state hash")
			}
			if operation.Payload.Size < 0 || operation.Payload.Size > MaxPayloadBytes {
				findings.add("bounds", base+".payload.size", fmt.Sprintf("must be between 0 and %d bytes", MaxPayloadBytes))
			}
		}
	} else if operation.Payload != nil {
		findings.add("payload", base+".payload", "is valid only for a regular-file after state")
	}
}

func validateFileState(findings *issues, base, destination, objectID string, state FileState) {
	switch state.Type {
	case FileAbsent:
		if state.Mode != "" || state.Hash != "" || state.SymlinkTarget != "" || state.Marker != nil {
			findings.add("state", base, "absent state must not contain mode, hash, symlink target, or marker")
		}
	case FileRegular:
		validateMode(findings, base+".mode", state.Mode)
		if !validDigest(state.Hash) {
			findings.add("digest", base+".hash", "must be a lowercase sha256 digest")
		}
		if state.SymlinkTarget != "" {
			findings.add("state", base+".symlink_target", "regular-file state must not contain a symlink target")
		}
		validateMarker(findings, base+".marker", objectID, state.Hash, state.Marker)
	case FileRelativeSymlink:
		if state.Mode != "" {
			findings.add("state", base+".mode", "relative-symlink state must not contain a mode")
		}
		if !validDigest(state.Hash) {
			findings.add("digest", base+".hash", "must be a lowercase sha256 digest")
		}
		if err := validateSymlinkTarget(destination, state.SymlinkTarget); err != nil {
			findings.add("path", base+".symlink_target", err.Error())
		}
		validateMarker(findings, base+".marker", objectID, state.Hash, state.Marker)
	default:
		findings.add("enum", base+".type", "contains an unknown file type")
	}
}

func validateMarker(findings *issues, base, objectID, stateHash string, marker *MarkerExpectation) {
	if marker == nil {
		findings.add("marker", base, "materialized state requires an ownership marker expectation")
		return
	}
	if !validIdentifier(marker.Owner) {
		findings.add("identifier", base+".owner", "must be a stable owner identifier")
	}
	if marker.ObjectID != objectID {
		findings.add("binding_mismatch", base+".object_id", "must equal the operation object_id")
	}
	if !validDigest(marker.StateHash) {
		findings.add("digest", base+".state_hash", "must be a lowercase sha256 digest")
	} else if marker.StateHash != stateHash {
		findings.add("binding_mismatch", base+".state_hash", "must equal the file-state hash")
	}
}

func validateMode(findings *issues, field, mode string) {
	if len(mode) != 4 || mode[0] != '0' {
		findings.add("mode", field, "must be a four-digit octal permission mode")
		return
	}
	parsed, err := strconv.ParseUint(mode, 8, 12)
	if err != nil || parsed > 0o777 {
		findings.add("mode", field, "must be a four-digit octal permission mode without special bits")
		return
	}
	if parsed&0o400 == 0 {
		findings.add("mode", field, "must be owner-readable")
	}
	if parsed&0o022 != 0 {
		findings.add("mode", field, "must not be group- or world-writable")
	}
}

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}

func validRepository(value string) bool {
	if len(value) == 0 || len(value) > 512 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, `\`) || hasControl(value) {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 8 {
		return false
	}
	for index, part := range parts {
		if index == len(parts)-1 && part == ".github" {
			continue
		}
		if !repositoryPartPattern.MatchString(part) || part == "." || part == ".." || strings.HasSuffix(strings.ToLower(part), ".git") {
			return false
		}
	}
	return true
}

func parseRemoteRepository(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > 2048 || hasControl(raw) || strings.Contains(raw, `\`) {
		return "", fmt.Errorf("must be a safe HTTPS or SSH Git remote")
	}
	var host, repositoryPath string
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil {
		if match[1] != "git" {
			return "", fmt.Errorf("scp-style remote user must be git")
		}
		host, repositoryPath = match[2], match[3]
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed == nil || parsed.Opaque != "" {
			return "", fmt.Errorf("must be a valid Git remote URL")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return "", fmt.Errorf("HTTPS remote must not contain user information")
			}
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return "", fmt.Errorf("SSH remote user must be git")
			}
			if _, set := parsed.User.Password(); set {
				return "", fmt.Errorf("SSH remote must not contain a password")
			}
		default:
			return "", fmt.Errorf("must use HTTPS or SSH")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
			return "", fmt.Errorf("must not contain a query, fragment, or encoded path")
		}
		if parsed.Port() != "" {
			return "", fmt.Errorf("must not contain an explicit port")
		}
		host, repositoryPath = parsed.Hostname(), strings.TrimPrefix(parsed.Path, "/")
	}
	if !safeRemoteHost(host) {
		return "", fmt.Errorf("contains an unsafe host")
	}
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	if !validRepository(repositoryPath) || path.Clean(repositoryPath) != repositoryPath {
		return "", fmt.Errorf("contains an invalid repository path")
	}
	return repositoryPath, nil
}

func safeRemoteHost(host string) bool {
	if host == "" || !hostPattern.MatchString(host) || strings.Contains(host, "..") {
		return false
	}
	canonical := strings.ToLower(strings.TrimSuffix(host, "."))
	if canonical == "localhost" || strings.HasSuffix(canonical, ".localhost") {
		return false
	}
	if ip := net.ParseIP(canonical); ip != nil {
		return false
	}
	return strings.Contains(canonical, ".")
}

func validRef(value string) bool {
	if !refPattern.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.Contains(value, "@{") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return false
		}
	}
	return !hasControl(value)
}

func validSHA(value string) bool {
	return shaPattern.MatchString(value) && value != strings.Repeat("0", 40)
}

func validDigest(value string) bool {
	return digestPattern.MatchString(value)
}

func validateRelativePath(value string) error {
	if value == "" || len(value) > 4096 || hasControl(value) || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return fmt.Errorf("must be a portable repository-relative path")
	}
	if path.Clean(value) != value || value == "." {
		return fmt.Errorf("must be normalized without dot or parent segments")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("must not contain empty, dot, or parent segments")
		}
	}
	return nil
}

func validateSymlinkTarget(destination, target string) error {
	if target == "" || len(target) > 4096 || hasControl(target) || strings.Contains(target, `\`) || strings.HasPrefix(target, "/") || path.IsAbs(target) {
		return fmt.Errorf("must be a portable relative symlink target")
	}
	if path.Clean(target) != target || target == "." {
		return fmt.Errorf("must be normalized")
	}
	resolved := path.Clean(path.Join(path.Dir(destination), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") {
		return fmt.Errorf("escapes the target repository")
	}
	return nil
}

func pathOverlaps(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func materialized(value FileType) bool {
	return value == FileRegular || value == FileRelativeSymlink
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
