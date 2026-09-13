package harnessmanifest

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	hashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}/[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	refPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)
	scpRemotePattern  = regexp.MustCompile(`^([^@/:]+)@([^/:]+):([A-Za-z0-9._/-]+)$`)
)

// Validate checks the complete schema, closed combinations, collisions,
// verifier linkage, and embedded coverage inputs without performing I/O.
func Validate(manifest Manifest, options ValidationOptions) error {
	var findings issues
	if manifest.SchemaVersion != SchemaVersion {
		findings.add("schema_version", "schema_version", fmt.Sprintf("must equal %d", SchemaVersion))
	}
	if !identifierPattern.MatchString(manifest.Repo) {
		findings.add("identifier", "repo", "must be a stable repository identifier")
	}

	registered := make(map[string]struct{}, len(options.RegisteredVerifierIDs))
	for index, id := range options.RegisteredVerifierIDs {
		field := fmt.Sprintf("validation_options.registered_verifier_ids[%d]", index)
		if !identifierPattern.MatchString(id) {
			findings.add("verifier_id", field, "must be a stable verifier identifier")
			continue
		}
		if _, exists := registered[id]; exists {
			findings.add("duplicate", field, "duplicates a registered verifier identifier")
		}
		registered[id] = struct{}{}
	}

	objectIDs := map[string]int{}
	destinations := map[string]int{}
	for index, object := range manifest.Objects {
		base := fmt.Sprintf("objects[%d]", index)
		validateObject(&findings, base, manifest.Repo, object, registered)
		if previous, exists := objectIDs[object.ID]; exists {
			findings.add("duplicate", base+".id", fmt.Sprintf("duplicates objects[%d].id", previous))
		} else {
			objectIDs[object.ID] = index
		}
		if previous, exists := destinations[object.Destination]; exists {
			findings.add("collision", base+".destination", fmt.Sprintf("collides with objects[%d].destination", previous))
		} else {
			destinations[object.Destination] = index
		}
	}
	for left := range manifest.Objects {
		for right := left + 1; right < len(manifest.Objects); right++ {
			if manifest.Objects[left].Destination != manifest.Objects[right].Destination && pathOverlaps(manifest.Objects[left].Destination, manifest.Objects[right].Destination) {
				findings.add("overlap", fmt.Sprintf("objects[%d].destination", right), fmt.Sprintf("overlaps objects[%d].destination", left))
			}
		}
	}

	omissionIDs := map[string]int{}
	for index, omission := range manifest.Omissions {
		base := fmt.Sprintf("omissions[%d]", index)
		validateOmission(&findings, base, omission, registered)
		if previous, exists := omissionIDs[omission.ID]; exists {
			findings.add("duplicate", base+".id", fmt.Sprintf("duplicates omissions[%d].id", previous))
		} else {
			omissionIDs[omission.ID] = index
		}
		if objectIndex, exists := objectIDs[omission.ID]; exists {
			findings.add("projection_omission_overlap", base+".id", fmt.Sprintf("also declared by objects[%d]", objectIndex))
		}
		for objectIndex, object := range manifest.Objects {
			if omissionMatches(omission, object.Destination) {
				findings.add("projection_omission_overlap", base+".path", fmt.Sprintf("matches objects[%d].destination", objectIndex))
			}
		}
	}
	for left := range manifest.Omissions {
		for right := left + 1; right < len(manifest.Omissions); right++ {
			if omissionPatternsOverlap(manifest.Omissions[left], manifest.Omissions[right]) {
				findings.add("overlap", fmt.Sprintf("omissions[%d].path", right), fmt.Sprintf("overlaps omissions[%d].path", left))
			}
		}
	}

	validateCoverageInputShape(manifest.CoverageInputs, &findings)
	if (len(manifest.Objects) > 0 || len(manifest.Omissions) > 0) && len(manifest.CoverageInputs) == 0 {
		findings.add("coverage", "coverage_inputs", "must declare discovered inputs for non-empty objects or omissions")
	}

	validateCoverage(manifest, &findings)
	return findings.err()
}

func validateObject(findings *issues, base, repo string, object Object, registered map[string]struct{}) {
	if !identifierPattern.MatchString(object.ID) {
		findings.add("identifier", base+".id", "must be a stable object identifier")
	}
	if object.Repo != repo || !identifierPattern.MatchString(object.Repo) {
		findings.add("repo", base+".repo", "must equal the manifest repository")
	}
	if err := validateRelativePath(object.Source); err != nil {
		findings.add("path", base+".source", err.Error())
	}
	if err := validateRelativePath(object.Destination); err != nil {
		findings.add("path", base+".destination", err.Error())
	}
	if !validOwnership(object.OwnershipClass) {
		findings.add("enum", base+".ownership_class", "contains an unknown ownership class")
	}
	if !validSourceDisposition(object.SourceDisposition) {
		findings.add("enum", base+".source_disposition", "contains an unknown source disposition")
	}
	if !validProjectionMode(object.ProjectionMode) {
		findings.add("enum", base+".projection_mode", "contains an unknown projection mode")
	}
	if !validLifecycle(object.Lifecycle) {
		findings.add("enum", base+".lifecycle", "contains an unknown lifecycle")
	}
	if !validConflictPolicy(object.ConflictPolicy) {
		findings.add("enum", base+".conflict_policy", "contains an unknown conflict policy")
	}
	if !validRemovalPolicy(object.RemovalPolicy) {
		findings.add("enum", base+".removal_policy", "contains an unknown removal policy")
	}
	if !hashPattern.MatchString(object.SourceHash) {
		findings.add("hash", base+".source_hash", "must be a lowercase sha256 digest")
	}
	validateSourceInputs(findings, base, object)
	validateVerifierIDs(findings, base+".verifier_ids", object.VerifierIDs, registered)
	validateOwnershipCombination(findings, base, object)
	validateMaterialization(findings, base, object)
	validateLifecycleCombination(findings, base, object)
	validateDeletion(findings, base, object)
}

func validateSourceInputs(findings *issues, base string, object Object) {
	if len(object.Inputs) == 0 {
		findings.add("inputs", base+".inputs", "must contain at least one ordered exact-ref input")
		return
	}
	for index, input := range object.Inputs {
		field := fmt.Sprintf("%s.inputs[%d]", base, index)
		if !repositoryPattern.MatchString(input.Repository) {
			findings.add("repository", field+".repository", "must be a canonical namespace/repository name")
		}
		if err := validateRemote(input.Remote, input.Repository); err != nil {
			findings.add("remote", field+".remote", err.Error())
		}
		if !refPattern.MatchString(input.Ref) || strings.Contains(input.Ref, "..") {
			findings.add("ref", field+".ref", "must be a normalized explicit source ref")
		}
		if !commitPattern.MatchString(input.Commit) {
			findings.add("commit", field+".commit", "must be a lowercase 40- or 64-hex exact commit")
		}
		if err := validateRelativePath(input.Path); err != nil {
			findings.add("path", field+".path", err.Error())
		}
		if !hashPattern.MatchString(input.BlobHash) {
			findings.add("hash", field+".blob_hash", "must be a lowercase sha256 digest")
		}
		if input.NonHermetic && strings.TrimSpace(input.NonHermeticReason) == "" {
			findings.add("non_hermetic", field+".non_hermetic_reason", "must explain a declared non-hermetic input")
		}
		if !input.NonHermetic && input.NonHermeticReason != "" {
			findings.add("combination", field+".non_hermetic_reason", "requires non_hermetic=true")
		}
		for previous := 0; previous < index; previous++ {
			prior := object.Inputs[previous]
			if prior.Repository == input.Repository && prior.Ref == input.Ref && prior.Commit == input.Commit && prior.Path == input.Path {
				findings.add("duplicate", field, fmt.Sprintf("duplicates %s.inputs[%d]", base, previous))
			}
		}
	}
	if object.Source != object.Inputs[0].Path {
		findings.add("input_binding", base+".source", "must equal inputs[0].path")
	}
	if object.SourceHash != object.Inputs[0].BlobHash {
		findings.add("input_binding", base+".source_hash", "must equal inputs[0].blob_hash")
	}
}

func validateRemote(value, repository string) error {
	if match := scpRemotePattern.FindStringSubmatch(value); match != nil {
		if match[1] != "git" {
			return fmt.Errorf("scp-style repository remote user must be git")
		}
		remotePath := strings.TrimSuffix(match[3], ".git")
		if remotePath != repository {
			return fmt.Errorf("repository remote path must match the canonical repository")
		}
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must be an https, ssh, or scp-style repository remote without credentials, query, or fragment")
	}
	if parsed.Scheme == "https" && parsed.User != nil {
		return fmt.Errorf("https repository remote must not embed user information or credentials")
	}
	if parsed.Scheme == "ssh" {
		if parsed.User == nil || parsed.User.Username() != "git" {
			return fmt.Errorf("ssh repository remote user must be git")
		}
		if _, passwordSet := parsed.User.Password(); passwordSet {
			return fmt.Errorf("ssh repository remote must not embed a password")
		}
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return fmt.Errorf("must identify a repository path")
	}
	remotePath := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
	if remotePath != repository {
		return fmt.Errorf("repository remote path must match the canonical repository")
	}
	return nil
}

func validateOwnershipCombination(findings *issues, base string, object Object) {
	allowed := false
	switch object.OwnershipClass {
	case OwnershipCodexHarnessSpecific:
		allowed = object.SourceDisposition == SourceCentralCanonical
	case OwnershipSharedProviderNeutral:
		allowed = object.SourceDisposition == SourceSharedHarness
	case OwnershipRepoLocalProjection:
		allowed = object.SourceDisposition == SourceGeneratedFromConsumer
	case OwnershipClaudeHarnessSpecific, OwnershipExternalOwner:
		allowed = object.SourceDisposition == SourceExternalOwner
	}
	if validOwnership(object.OwnershipClass) && validSourceDisposition(object.SourceDisposition) && !allowed {
		findings.add("combination", base+".source_disposition", "is incompatible with ownership_class")
	}

	switch object.SourceDisposition {
	case SourceExternalOwner:
		if object.ProjectionMode != ProjectionReference && object.ProjectionMode != ProjectionNone {
			findings.add("combination", base+".projection_mode", "external-owner sources may only be referenced or omitted")
		}
	case SourceGeneratedFromConsumer:
		if object.ProjectionMode != ProjectionRender && object.ProjectionMode != ProjectionReference && object.ProjectionMode != ProjectionNone {
			findings.add("combination", base+".projection_mode", "generated-from-consumer sources may only render, reference, or omit metadata")
		}
	}
}

func validateMaterialization(findings *issues, base string, object Object) {
	materialized := object.ProjectionMode == ProjectionCopy || object.ProjectionMode == ProjectionRender || object.ProjectionMode == ProjectionManagedBlock
	if materialized {
		if err := validateMode(object.Mode); err != nil {
			findings.add("mode", base+".mode", err.Error())
		}
		if !hashPattern.MatchString(object.ProjectedHash) {
			findings.add("hash", base+".projected_hash", "must be a lowercase sha256 digest for materialized content")
		}
	} else {
		if object.Mode != "" {
			findings.add("combination", base+".mode", "must be empty when projection_mode has no file mode")
		}
		if object.ProjectionMode != ProjectionRelativeSymlink && object.ProjectedHash != "" {
			findings.add("combination", base+".projected_hash", "must be empty when projection_mode does not materialize content")
		}
	}

	needsGenerator := object.ProjectionMode == ProjectionRender || object.ProjectionMode == ProjectionManagedBlock
	if needsGenerator {
		if !identifierPattern.MatchString(object.Generator) {
			findings.add("generator", base+".generator", "must identify the deterministic generator")
		}
		if !commitPattern.MatchString(object.GeneratorCommit) {
			findings.add("commit", base+".generator_commit", "must be a lowercase 40- or 64-hex exact commit")
		}
		if !identifierPattern.MatchString(object.Transform) {
			findings.add("transform", base+".transform", "must identify the transform version")
		}
	} else if object.Generator != "" || object.GeneratorCommit != "" || object.Transform != "" {
		findings.add("combination", base+".generator", "generator fields are only valid for render or managed-block modes")
	}

	if object.ProjectionMode == ProjectionRelativeSymlink {
		if err := validateSymlinkTarget(object.Destination, object.SymlinkTarget); err != nil {
			findings.add("symlink", base+".symlink_target", err.Error())
		}
		if !hashPattern.MatchString(object.ProjectedHash) {
			findings.add("hash", base+".projected_hash", "must hash the declared symlink target")
		}
	} else if object.SymlinkTarget != "" {
		findings.add("combination", base+".symlink_target", "is only valid for relative-symlink mode")
	}
}

func validateLifecycleCombination(findings *issues, base string, object Object) {
	if object.Lifecycle == LifecycleActive && object.ProjectionMode == ProjectionNone {
		findings.add("combination", base+".lifecycle", "active objects may not use projection_mode none")
	}
	if (object.Lifecycle == LifecycleRetired || object.Lifecycle == LifecycleArchived) && object.ProjectionMode != ProjectionNone && object.ProjectionMode != ProjectionReference {
		findings.add("combination", base+".lifecycle", "retired or archived objects may only be referenced or non-materialized")
	}
}

func validateDeletion(findings *issues, base string, object Object) {
	if object.ConflictPolicy == ConflictReplaceOwned && (object.OwnershipClass != OwnershipCodexHarnessSpecific || object.SourceDisposition != SourceCentralCanonical) {
		findings.add("unowned_mutation", base+".conflict_policy", "replace-owned requires a Codex-harness central-canonical object")
	}
	if object.ConflictPolicy == ConflictReplaceOwned && (object.ProjectionMode == ProjectionReference || object.ProjectionMode == ProjectionNone) {
		findings.add("combination", base+".conflict_policy", "replace-owned requires a materialized destination")
	}
	if object.Tombstone {
		if object.OwnershipClass != OwnershipCodexHarnessSpecific || object.SourceDisposition != SourceCentralCanonical {
			findings.add("unowned_deletion", base+".tombstone", "deletion requires a Codex-harness central-canonical object")
		}
		if object.RemovalPolicy != RemovalTombstoneOnly {
			findings.add("combination", base+".removal_policy", "a tombstone requires tombstone-only removal")
		}
		if object.ProjectionMode != ProjectionNone || object.Lifecycle != LifecycleRetired {
			findings.add("combination", base+".tombstone", "a tombstone must be retired and non-materialized")
		}
	} else if object.RemovalPolicy != RemovalPreserve {
		findings.add("unowned_deletion", base+".removal_policy", "tombstone-only removal requires an explicit tombstone")
	}
}

func validateOmission(findings *issues, base string, omission Omission, registered map[string]struct{}) {
	if !identifierPattern.MatchString(omission.ID) {
		findings.add("identifier", base+".id", "must be a stable omission identifier")
	}
	if !validOwnership(omission.OwnershipClass) {
		findings.add("enum", base+".ownership_class", "contains an unknown ownership class")
	}
	if !validLifecycle(omission.Lifecycle) {
		findings.add("enum", base+".lifecycle", "contains an unknown lifecycle")
	}
	if strings.TrimSpace(omission.Reason) == "" {
		findings.add("reason", base+".reason", "must explain the omission")
	}
	switch omission.Kind {
	case OmissionExact:
		if err := validateRelativePath(omission.Path); err != nil {
			findings.add("path", base+".path", err.Error())
		}
	case OmissionGlob:
		if err := validateOmissionPattern(omission.Path); err != nil {
			findings.add("path", base+".path", err.Error())
		}
	default:
		findings.add("enum", base+".kind", "contains an unknown omission kind")
	}
	validateVerifierIDs(findings, base+".verifier_ids", omission.VerifierIDs, registered)
}

func validateVerifierIDs(findings *issues, field string, ids []string, registered map[string]struct{}) {
	if len(ids) == 0 {
		findings.add("verifier", field, "must contain at least one registered verifier")
		return
	}
	seen := map[string]struct{}{}
	for index, id := range ids {
		path := fmt.Sprintf("%s[%d]", field, index)
		if !identifierPattern.MatchString(id) {
			findings.add("verifier_id", path, "must be a stable verifier identifier")
			continue
		}
		if _, exists := seen[id]; exists {
			findings.add("duplicate", path, "duplicates a verifier on the same declaration")
		}
		seen[id] = struct{}{}
		if _, exists := registered[id]; !exists {
			findings.add("unregistered_verifier", path, "is not present in the supplied verifier registry")
		}
	}
}

func validateMode(value string) error {
	if len(value) != 4 || value[0] != '0' {
		return fmt.Errorf("must be a four-digit octal permission such as 0644")
	}
	mode, err := strconv.ParseUint(value, 8, 12)
	if err != nil || mode > 0o777 {
		return fmt.Errorf("must be a four-digit octal permission without special bits")
	}
	if mode&0o400 == 0 {
		return fmt.Errorf("must be owner-readable")
	}
	if mode&0o022 != 0 {
		return fmt.Errorf("must not be group- or world-writable")
	}
	return nil
}

func validOwnership(value OwnershipClass) bool {
	switch value {
	case OwnershipSharedProviderNeutral, OwnershipCodexHarnessSpecific, OwnershipClaudeHarnessSpecific, OwnershipRepoLocalProjection, OwnershipExternalOwner:
		return true
	default:
		return false
	}
}

func validSourceDisposition(value SourceDisposition) bool {
	switch value {
	case SourceCentralCanonical, SourceGeneratedFromConsumer, SourceSharedHarness, SourceExternalOwner:
		return true
	default:
		return false
	}
}

func validProjectionMode(value ProjectionMode) bool {
	switch value {
	case ProjectionCopy, ProjectionRender, ProjectionManagedBlock, ProjectionRelativeSymlink, ProjectionReference, ProjectionNone:
		return true
	default:
		return false
	}
}

func validLifecycle(value Lifecycle) bool {
	switch value {
	case LifecyclePlanned, LifecycleActive, LifecycleDeprecated, LifecycleRetired, LifecycleArchived, LifecycleNeverPublish:
		return true
	default:
		return false
	}
}

func validConflictPolicy(value ConflictPolicy) bool {
	return value == ConflictFail || value == ConflictReplaceOwned
}

func validRemovalPolicy(value RemovalPolicy) bool {
	return value == RemovalPreserve || value == RemovalTombstoneOnly
}

// canonicalVerifierIDs returns a sorted copy for deterministic output.
func canonicalVerifierIDs(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
