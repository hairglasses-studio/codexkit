package launchercontract

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"time"
)

// NewEvidence converts a valid runtime Resolution into a sanitized Evidence
// value and validates it against the supplied trusted clock.
func NewEvidence(resolution Resolution, record EvidenceRecord, options EvidenceOptions) (Evidence, error) {
	if err := ValidateResolution(resolution); err != nil {
		return Evidence{}, err
	}
	candidateID, err := PathID(resolution.Candidate)
	if err != nil {
		return Evidence{}, err
	}
	topLevelID, err := PathID(resolution.GitTopLevel)
	if err != nil {
		return Evidence{}, err
	}
	commonDirID, err := PathID(resolution.GitCommonDir)
	if err != nil {
		return Evidence{}, err
	}
	codexkitRootID, err := PathID(resolution.CodexkitRoot)
	if err != nil {
		return Evidence{}, err
	}

	markers := make([]EvidenceMarker, 0, len(resolution.Markers))
	for _, marker := range resolution.Markers {
		markerPathID, pathErr := PathID(path.Join(resolution.Candidate, marker.RelativePath))
		if pathErr != nil {
			return Evidence{}, pathErr
		}
		authorityPathID, authorityPathErr := authorityMarkerPathID(marker.RelativePath)
		if authorityPathErr != nil {
			return Evidence{}, fmt.Errorf("%w: marker authority path: %v", ErrInvalidEvidence, authorityPathErr)
		}
		markers = append(markers, EvidenceMarker{
			ID: marker.ID, PathID: markerPathID, AuthorityMarkerPathID: authorityPathID, SHA256: marker.SHA256,
		})
	}
	sort.Slice(markers, func(i, j int) bool {
		if markers[i].ID != markers[j].ID {
			return markers[i].ID < markers[j].ID
		}
		return markers[i].PathID < markers[j].PathID
	})

	evidence := Evidence{
		SchemaVersion:               EvidenceSchemaVersion,
		Source:                      resolution.Source,
		Mode:                        resolution.Mode,
		Status:                      StatusActivationEligible,
		ObservedAt:                  record.ObservedAt,
		CandidatePathID:             candidateID,
		GitTopLevelPathID:           topLevelID,
		GitCommonDirPathID:          commonDirID,
		CodexkitRootPathID:          codexkitRootID,
		Repository:                  resolution.Repository,
		NormalizedRemote:            resolution.NormalizedRemote,
		TestedSHA:                   resolution.TestedSHA,
		AuthoritySHA256:             resolution.AuthoritySHA256,
		CheckoutClean:               resolution.CheckoutClean,
		Markers:                     markers,
		SideEffectGuardBeforeSHA256: record.SideEffectGuardBeforeSHA256,
		SideEffectGuardAfterSHA256:  record.SideEffectGuardAfterSHA256,
		ProviderStarted:             record.ProviderStarted,
		NormalLaunchExecuted:        record.NormalLaunchExecuted,
		ActivationEligible:          resolution.ActivationEligible,
	}
	if err := ValidateEvidence(evidence, options); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func evidenceWindow(options EvidenceOptions) (time.Time, time.Duration, error) {
	if options.Now.IsZero() {
		return time.Time{}, 0, fmt.Errorf("%w: trusted validation time is required", ErrInvalidEvidence)
	}
	maxAge := options.MaxAge
	if maxAge == 0 {
		maxAge = DefaultEvidenceMaxAge
	}
	if maxAge < 0 {
		return time.Time{}, 0, fmt.Errorf("%w: max age must not be negative", ErrInvalidEvidence)
	}
	if !validDigest(options.ExpectedAuthoritySHA256) {
		return time.Time{}, 0, fmt.Errorf("%w: trusted expected authority SHA-256 is required", ErrInvalidEvidence)
	}
	return options.Now.UTC().Round(0), maxAge, nil
}

// ValidateEvidence fails closed on unsupported versions, unknown enums,
// unsanitized path IDs, stale/future observations, side effects, and identity
// inconsistencies.
func ValidateEvidence(evidence Evidence, options EvidenceOptions) error {
	now, maxAge, err := evidenceWindow(options)
	if err != nil {
		return err
	}
	if evidence.SchemaVersion != EvidenceSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidEvidence, EvidenceSchemaVersion)
	}
	if !validSource(evidence.Source) {
		return fmt.Errorf("%w: unknown source %q", ErrInvalidEvidence, evidence.Source)
	}
	if !validMode(evidence.Mode) {
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidEvidence, evidence.Mode)
	}
	if evidence.Status != StatusActivationEligible {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidEvidence, evidence.Status)
	}
	if evidence.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidEvidence)
	}
	observedAt := evidence.ObservedAt.UTC().Round(0)
	if observedAt.After(now) {
		return fmt.Errorf("%w: observed_at is in the future", ErrInvalidEvidence)
	}
	if now.Sub(observedAt) > maxAge {
		return fmt.Errorf("%w: observed_at is stale", ErrInvalidEvidence)
	}
	pathIDs := []struct {
		name  string
		value string
	}{
		{"candidate_path_id", evidence.CandidatePathID},
		{"git_top_level_path_id", evidence.GitTopLevelPathID},
		{"git_common_dir_path_id", evidence.GitCommonDirPathID},
		{"codexkit_root_path_id", evidence.CodexkitRootPathID},
	}
	for _, item := range pathIDs {
		if !validPathID(item.value) {
			return fmt.Errorf("%w: %s is not a sanitized path ID", ErrInvalidEvidence, item.name)
		}
	}
	if evidence.CandidatePathID != evidence.GitTopLevelPathID {
		return fmt.Errorf("%w: git top-level path ID must equal candidate path ID", ErrInvalidEvidence)
	}
	if evidence.GitCommonDirPathID == evidence.CandidatePathID || evidence.CodexkitRootPathID == evidence.CandidatePathID {
		return fmt.Errorf("%w: common-dir and codexkit path IDs must differ from the candidate", ErrInvalidEvidence)
	}
	if evidence.Repository != CanonicalRepository || evidence.NormalizedRemote != CanonicalRemote {
		return fmt.Errorf("%w: evidence does not identify the central harness", ErrInvalidEvidence)
	}
	if !validTestedSHA(evidence.TestedSHA) {
		return fmt.Errorf("%w: tested_sha must be a nonzero lowercase 40-hex commit", ErrInvalidEvidence)
	}
	if !evidence.CheckoutClean {
		return fmt.Errorf("%w: checkout must be clean", ErrInvalidEvidence)
	}
	if len(evidence.Markers) == 0 || len(evidence.Markers) > maxMarkers {
		return fmt.Errorf("%w: markers must contain between 1 and %d entries", ErrInvalidEvidence, maxMarkers)
	}
	markerIDs := make(map[string]struct{}, len(evidence.Markers))
	markerPaths := make(map[string]struct{}, len(evidence.Markers))
	authorityMarkerPaths := make(map[string]struct{}, len(evidence.Markers))
	for index, marker := range evidence.Markers {
		if !validMarkerID(marker.ID) {
			return fmt.Errorf("%w: markers[%d].id is invalid", ErrInvalidEvidence, index)
		}
		if !validPathID(marker.PathID) {
			return fmt.Errorf("%w: markers[%d].path_id is invalid", ErrInvalidEvidence, index)
		}
		if !validAuthorityMarkerPathID(marker.AuthorityMarkerPathID) {
			return fmt.Errorf("%w: markers[%d].authority_marker_path_id is invalid", ErrInvalidEvidence, index)
		}
		if !validDigest(marker.SHA256) {
			return fmt.Errorf("%w: markers[%d].sha256 is invalid", ErrInvalidEvidence, index)
		}
		if _, exists := markerIDs[marker.ID]; exists {
			return fmt.Errorf("%w: duplicate marker ID %q", ErrInvalidEvidence, marker.ID)
		}
		if _, exists := markerPaths[marker.PathID]; exists {
			return fmt.Errorf("%w: duplicate marker path ID %q", ErrInvalidEvidence, marker.PathID)
		}
		if _, exists := authorityMarkerPaths[marker.AuthorityMarkerPathID]; exists {
			return fmt.Errorf("%w: duplicate authority marker path ID %q", ErrInvalidEvidence, marker.AuthorityMarkerPathID)
		}
		markerIDs[marker.ID] = struct{}{}
		markerPaths[marker.PathID] = struct{}{}
		authorityMarkerPaths[marker.AuthorityMarkerPathID] = struct{}{}
	}
	if !validDigest(evidence.AuthoritySHA256) || evidence.AuthoritySHA256 != options.ExpectedAuthoritySHA256 {
		return fmt.Errorf("%w: authority_sha256 does not match trusted authority", ErrInvalidEvidence)
	}
	authority, err := digestAuthority(
		evidence.Repository,
		evidence.NormalizedRemote,
		evidence.TestedSHA,
		authorityMarkersFromEvidence(evidence.Markers),
	)
	if err != nil {
		return fmt.Errorf("%w: authority digest: %v", ErrInvalidEvidence, err)
	}
	if authority != evidence.AuthoritySHA256 {
		return fmt.Errorf("%w: authority_sha256 does not bind the evidence repository, commit, and markers", ErrInvalidEvidence)
	}
	if !validDigest(evidence.SideEffectGuardBeforeSHA256) || !validDigest(evidence.SideEffectGuardAfterSHA256) {
		return fmt.Errorf("%w: side-effect guard hashes are invalid", ErrInvalidEvidence)
	}
	if evidence.SideEffectGuardBeforeSHA256 != evidence.SideEffectGuardAfterSHA256 {
		return fmt.Errorf("%w: side-effect guard hashes differ", ErrInvalidEvidence)
	}
	if evidence.ProviderStarted {
		return fmt.Errorf("%w: provider_started must be false", ErrInvalidEvidence)
	}
	if evidence.NormalLaunchExecuted {
		return fmt.Errorf("%w: normal_launch_executed must be false", ErrInvalidEvidence)
	}
	if !evidence.ActivationEligible {
		return fmt.Errorf("%w: activation_eligible must be true", ErrInvalidEvidence)
	}
	return nil
}

func canonicalEvidence(evidence Evidence) Evidence {
	evidence.ObservedAt = evidence.ObservedAt.UTC().Round(0)
	evidence.Markers = slices.Clone(evidence.Markers)
	sort.Slice(evidence.Markers, func(i, j int) bool {
		if evidence.Markers[i].ID != evidence.Markers[j].ID {
			return evidence.Markers[i].ID < evidence.Markers[j].ID
		}
		return evidence.Markers[i].PathID < evidence.Markers[j].PathID
	})
	return evidence
}
