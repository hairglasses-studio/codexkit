package launchercontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	testedSHAPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pathIDPattern     = regexp.MustCompile(`^path-sha256:[0-9a-f]{64}$`)
	markerPathPattern = regexp.MustCompile(`^marker-path-sha256:[0-9a-f]{64}$`)
	markerIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	scpRemotePattern  = regexp.MustCompile(`^git@([^/:]+):(.+)$`)
)

func cleanAbsolutePath(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || value == "/" {
		return fmt.Errorf("must be a non-root absolute clean path")
	}
	if !strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.Contains(value, `\`) || hasControl(value) {
		return fmt.Errorf("must be an absolute clean path")
	}
	return nil
}

func cleanRelativePath(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") ||
		path.Clean(value) != value || value == "." || strings.Contains(value, `\`) || hasControl(value) {
		return fmt.Errorf("must be a portable relative path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return fmt.Errorf("must be a portable relative path")
		}
	}
	return nil
}

func hasControl(value string) bool {
	for _, char := range value {
		if char == 0 || char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func validTestedSHA(value string) bool {
	return testedSHAPattern.MatchString(value) && value != strings.Repeat("0", 40)
}

func validDigest(value string) bool {
	return digestPattern.MatchString(value) && value != "sha256:"+strings.Repeat("0", 64)
}

func validPathID(value string) bool {
	return pathIDPattern.MatchString(value) && value != "path-sha256:"+strings.Repeat("0", 64)
}

func validMarkerID(value string) bool { return markerIDPattern.MatchString(value) }

func validAuthorityMarkerPathID(value string) bool {
	return markerPathPattern.MatchString(value) && value != "marker-path-sha256:"+strings.Repeat("0", 64)
}

// PathID returns a domain-separated one-way identifier for an absolute clean
// path. The original path cannot appear in canonical Evidence.
func PathID(absolutePath string) (string, error) {
	if err := cleanAbsolutePath(absolutePath); err != nil {
		return "", fmt.Errorf("%w: path ID input %v", ErrInvalidContract, err)
	}
	sum := sha256.Sum256(append([]byte("launchercontract:path:v1\x00"), []byte(absolutePath)...))
	return "path-sha256:" + hex.EncodeToString(sum[:]), nil
}

func authorityMarkerPathID(relativePath string) (string, error) {
	if err := cleanRelativePath(relativePath); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("launchercontract:authority-marker-path:v1\x00"), []byte(relativePath)...))
	return "marker-path-sha256:" + hex.EncodeToString(sum[:]), nil
}

type authorityMarker struct {
	ID                    string `json:"id"`
	AuthorityMarkerPathID string `json:"authority_marker_path_id"`
	SHA256                string `json:"sha256"`
}

type authorityDocument struct {
	SchemaVersion    int               `json:"schema_version"`
	Repository       string            `json:"repository"`
	NormalizedRemote string            `json:"normalized_remote"`
	ExpectedSHA      string            `json:"expected_sha"`
	Markers          []authorityMarker `json:"markers"`
}

func digestAuthority(repository, normalizedRemote, expectedSHA string, markers []authorityMarker) (string, error) {
	markers = slices.Clone(markers)
	sort.Slice(markers, func(i, j int) bool {
		if markers[i].ID != markers[j].ID {
			return markers[i].ID < markers[j].ID
		}
		return markers[i].AuthorityMarkerPathID < markers[j].AuthorityMarkerPathID
	})
	data, err := json.Marshal(authorityDocument{
		SchemaVersion:    1,
		Repository:       repository,
		NormalizedRemote: normalizedRemote,
		ExpectedSHA:      expectedSHA,
		Markers:          markers,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode authority: %v", ErrInvalidContract, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func authorityMarkersFromTrust(markers []MarkerTrust) ([]authorityMarker, error) {
	authority := make([]authorityMarker, 0, len(markers))
	for _, marker := range markers {
		pathID, err := authorityMarkerPathID(marker.RelativePath)
		if err != nil {
			return nil, err
		}
		authority = append(authority, authorityMarker{ID: marker.ID, AuthorityMarkerPathID: pathID, SHA256: marker.SHA256})
	}
	return authority, nil
}

func authorityMarkersFromObservations(markers []MarkerObservation) ([]authorityMarker, error) {
	authority := make([]authorityMarker, 0, len(markers))
	for _, marker := range markers {
		pathID, err := authorityMarkerPathID(marker.RelativePath)
		if err != nil {
			return nil, err
		}
		authority = append(authority, authorityMarker{ID: marker.ID, AuthorityMarkerPathID: pathID, SHA256: marker.SHA256})
	}
	return authority, nil
}

func authorityMarkersFromEvidence(markers []EvidenceMarker) []authorityMarker {
	authority := make([]authorityMarker, 0, len(markers))
	for _, marker := range markers {
		authority = append(authority, authorityMarker{
			ID: marker.ID, AuthorityMarkerPathID: marker.AuthorityMarkerPathID, SHA256: marker.SHA256,
		})
	}
	return authority
}

// AuthoritySHA256 returns the canonical digest that binds the fixed
// repository, normalized remote, exact expected commit, and required marker
// paths/content. Callers place this trusted value in EvidenceOptions.
func AuthoritySHA256(trust Trust) (string, error) {
	if err := validateTrust(trust); err != nil {
		return "", err
	}
	markers, err := authorityMarkersFromTrust(trust.RequiredMarkers)
	if err != nil {
		return "", fmt.Errorf("%w: authority markers: %v", ErrInvalidContract, err)
	}
	return digestAuthority(trust.Repository, trust.NormalizedRemote, trust.ExpectedSHA, markers)
}

func normalizeRemote(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) || hasControl(raw) || strings.Contains(raw, `\`) {
		return "", fmt.Errorf("remote_url is invalid")
	}

	host := ""
	repository := ""
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil {
		host = match[1]
		repository = match[2]
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed == nil || parsed.Opaque != "" {
			return "", fmt.Errorf("remote_url is invalid")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return "", fmt.Errorf("https remote_url must not contain user information")
			}
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return "", fmt.Errorf("ssh remote_url user must be git")
			}
			if _, present := parsed.User.Password(); present {
				return "", fmt.Errorf("ssh remote_url must not contain a password")
			}
		default:
			return "", fmt.Errorf("remote_url must use https or ssh")
		}
		if parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
			return "", fmt.Errorf("remote_url must not contain a port, query, fragment, or encoded path")
		}
		host = parsed.Hostname()
		repository = strings.TrimPrefix(parsed.Path, "/")
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	repository = strings.TrimSuffix(repository, ".git")
	if host != "github.com" || repository != CanonicalRepository {
		return "", fmt.Errorf("remote_url does not identify %s", CanonicalRemote)
	}
	return CanonicalRemote, nil
}

func validSource(source Source) bool {
	switch source {
	case SourceCodexHarnessDir, SourceLegacyCodexkitRoot, SourceLauncherDefault:
		return true
	default:
		return false
	}
}

func validMode(mode Mode) bool {
	switch mode {
	case ModePrimaryCheckout, ModeLinkedWorktree:
		return true
	default:
		return false
	}
}

func checkoutMode(candidate, commonDir string) Mode {
	if commonDir == path.Join(candidate, ".git") {
		return ModePrimaryCheckout
	}
	return ModeLinkedWorktree
}

func validateTrust(trust Trust) error {
	if trust.Repository != CanonicalRepository {
		return fmt.Errorf("%w: trust repository must be %q", ErrInvalidContract, CanonicalRepository)
	}
	if trust.NormalizedRemote != CanonicalRemote {
		return fmt.Errorf("%w: trust normalized remote must be %q", ErrInvalidContract, CanonicalRemote)
	}
	if !validTestedSHA(trust.ExpectedSHA) {
		return fmt.Errorf("%w: trust expected SHA must be a nonzero lowercase 40-hex commit", ErrInvalidContract)
	}
	if len(trust.RequiredMarkers) == 0 || len(trust.RequiredMarkers) > maxMarkers {
		return fmt.Errorf("%w: trust must contain between 1 and %d required markers", ErrInvalidContract, maxMarkers)
	}
	ids := make(map[string]struct{}, len(trust.RequiredMarkers))
	paths := make(map[string]struct{}, len(trust.RequiredMarkers))
	for index, marker := range trust.RequiredMarkers {
		if !validMarkerID(marker.ID) {
			return fmt.Errorf("%w: required marker %d has invalid ID", ErrInvalidContract, index)
		}
		if err := cleanRelativePath(marker.RelativePath); err != nil {
			return fmt.Errorf("%w: required marker %q path %v", ErrInvalidContract, marker.ID, err)
		}
		if !validDigest(marker.SHA256) {
			return fmt.Errorf("%w: required marker %q has invalid sha256", ErrInvalidContract, marker.ID)
		}
		if _, exists := ids[marker.ID]; exists {
			return fmt.Errorf("%w: duplicate required marker ID %q", ErrInvalidContract, marker.ID)
		}
		if _, exists := paths[marker.RelativePath]; exists {
			return fmt.Errorf("%w: duplicate required marker path %q", ErrInvalidContract, marker.RelativePath)
		}
		ids[marker.ID] = struct{}{}
		paths[marker.RelativePath] = struct{}{}
	}
	return nil
}

func validateMarkers(observed []MarkerObservation, trusted []MarkerTrust) error {
	if len(observed) != len(trusted) {
		return fmt.Errorf("marker observations must exactly cover the %d required markers", len(trusted))
	}
	expected := make(map[string]MarkerTrust, len(trusted))
	for _, marker := range trusted {
		expected[marker.ID] = marker
	}
	seenIDs := make(map[string]struct{}, len(observed))
	seenPaths := make(map[string]struct{}, len(observed))
	for index, marker := range observed {
		if !validMarkerID(marker.ID) {
			return fmt.Errorf("marker %d has invalid ID", index)
		}
		if _, exists := seenIDs[marker.ID]; exists {
			return fmt.Errorf("duplicate marker observation ID %q", marker.ID)
		}
		if _, exists := seenPaths[marker.RelativePath]; exists {
			return fmt.Errorf("duplicate marker observation path %q", marker.RelativePath)
		}
		seenIDs[marker.ID] = struct{}{}
		seenPaths[marker.RelativePath] = struct{}{}

		want, exists := expected[marker.ID]
		if !exists {
			return fmt.Errorf("unknown marker observation %q", marker.ID)
		}
		if marker.RelativePath != want.RelativePath {
			return fmt.Errorf("marker %q path does not match trust", marker.ID)
		}
		if err := cleanRelativePath(marker.RelativePath); err != nil {
			return fmt.Errorf("marker %q path %v", marker.ID, err)
		}
		if !marker.Contained {
			return fmt.Errorf("marker %q was not observed inside the candidate", marker.ID)
		}
		if !marker.Regular {
			return fmt.Errorf("marker %q is not a no-follow regular file", marker.ID)
		}
		if !validDigest(marker.SHA256) || marker.SHA256 != want.SHA256 {
			return fmt.Errorf("marker %q digest does not match trust", marker.ID)
		}
	}
	return nil
}

// ValidateResolution verifies a Resolution without consulting ambient state.
func ValidateResolution(resolution Resolution) error {
	if !validSource(resolution.Source) {
		return fmt.Errorf("%w: unknown resolution source %q", ErrInvalidContract, resolution.Source)
	}
	if err := cleanAbsolutePath(resolution.Candidate); err != nil {
		return fmt.Errorf("%w: resolution candidate %v", ErrInvalidContract, err)
	}
	if resolution.GitTopLevel != resolution.Candidate {
		return fmt.Errorf("%w: resolution git top-level does not equal candidate", ErrInvalidContract)
	}
	if err := cleanAbsolutePath(resolution.GitCommonDir); err != nil {
		return fmt.Errorf("%w: resolution git common-dir %v", ErrInvalidContract, err)
	}
	if resolution.GitCommonDir == resolution.Candidate {
		return fmt.Errorf("%w: resolution git common-dir must not equal the worktree", ErrInvalidContract)
	}
	if resolution.CodexkitRoot != path.Join(resolution.Candidate, "codexkit") {
		return fmt.Errorf("%w: resolution codexkit root is not contained at harness/codexkit", ErrInvalidContract)
	}
	if resolution.Repository != CanonicalRepository || resolution.NormalizedRemote != CanonicalRemote {
		return fmt.Errorf("%w: resolution does not identify the central harness", ErrInvalidContract)
	}
	if !validTestedSHA(resolution.TestedSHA) {
		return fmt.Errorf("%w: resolution tested SHA must be a nonzero lowercase 40-hex commit", ErrInvalidContract)
	}
	if !resolution.CheckoutClean {
		return fmt.Errorf("%w: resolution checkout must be clean", ErrInvalidContract)
	}
	if !resolution.ActivationEligible {
		return fmt.Errorf("%w: resolution is not activation eligible", ErrInvalidContract)
	}
	if resolution.Mode != checkoutMode(resolution.Candidate, resolution.GitCommonDir) || !validMode(resolution.Mode) {
		return fmt.Errorf("%w: resolution mode does not match git common-dir identity", ErrInvalidContract)
	}
	if len(resolution.Markers) == 0 || len(resolution.Markers) > maxMarkers {
		return fmt.Errorf("%w: resolution markers must be non-empty and bounded", ErrInvalidContract)
	}
	ids := make(map[string]struct{}, len(resolution.Markers))
	paths := make(map[string]struct{}, len(resolution.Markers))
	for _, marker := range resolution.Markers {
		if !validMarkerID(marker.ID) || cleanRelativePath(marker.RelativePath) != nil || !validDigest(marker.SHA256) || !marker.Contained || !marker.Regular {
			return fmt.Errorf("%w: resolution marker %q is invalid", ErrInvalidContract, marker.ID)
		}
		if _, exists := ids[marker.ID]; exists {
			return fmt.Errorf("%w: duplicate resolution marker ID %q", ErrInvalidContract, marker.ID)
		}
		if _, exists := paths[marker.RelativePath]; exists {
			return fmt.Errorf("%w: duplicate resolution marker path %q", ErrInvalidContract, marker.RelativePath)
		}
		ids[marker.ID] = struct{}{}
		paths[marker.RelativePath] = struct{}{}
	}
	if !validDigest(resolution.AuthoritySHA256) {
		return fmt.Errorf("%w: resolution authority SHA-256 is invalid", ErrInvalidContract)
	}
	authorityMarkers, err := authorityMarkersFromObservations(resolution.Markers)
	if err != nil {
		return fmt.Errorf("%w: resolution authority markers: %v", ErrInvalidContract, err)
	}
	authority, err := digestAuthority(resolution.Repository, resolution.NormalizedRemote, resolution.TestedSHA, authorityMarkers)
	if err != nil {
		return err
	}
	if authority != resolution.AuthoritySHA256 {
		return fmt.Errorf("%w: resolution authority digest does not match its repository, commit, and markers", ErrInvalidContract)
	}
	return nil
}

func canonicalMarkers(markers []MarkerObservation) []MarkerObservation {
	markers = slices.Clone(markers)
	sort.Slice(markers, func(i, j int) bool {
		if markers[i].ID != markers[j].ID {
			return markers[i].ID < markers[j].ID
		}
		return markers[i].RelativePath < markers[j].RelativePath
	})
	return markers
}
