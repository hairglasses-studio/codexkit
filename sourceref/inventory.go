package sourceref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

var (
	repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	shaPattern            = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hostPattern           = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	scpRemotePattern      = regexp.MustCompile(`^([^@/:]+)@([^/:]+):(.+)$`)
)

// Load strictly decodes and validates exactly one inventory JSON document.
// Unknown fields, duplicate object keys, and trailing JSON are rejected.
func Load(data []byte, opts Options) (Inventory, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return Inventory{}, fmt.Errorf("%w: document size must be between 1 and %d bytes", ErrInvalidInventory, MaxDocumentBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Inventory{}, fmt.Errorf("%w: %v", ErrInvalidInventory, err)
	}

	var inventory Inventory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		return Inventory{}, fmt.Errorf("%w: decode: %v", ErrInvalidInventory, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Inventory{}, fmt.Errorf("%w: %v", ErrInvalidInventory, err)
	}
	if err := Validate(inventory, opts); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}

// Validate fails closed on unsupported schemas, stale or future evidence,
// invalid repository/remote/ref/SHA/path values, duplicate identities, unknown
// enums, and evidence-class contradictions.
func Validate(inventory Inventory, opts Options) error {
	now, maxAge, err := validationWindow(opts)
	if err != nil {
		return err
	}
	if inventory.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidInventory, SchemaVersion)
	}
	if len(inventory.Repositories) == 0 {
		return fmt.Errorf("%w: repositories must be a non-empty array", ErrInvalidInventory)
	}

	repositories := make(map[string]struct{}, len(inventory.Repositories))
	remotes := make(map[string]string, len(inventory.Repositories))
	checkouts := make(map[string]string, len(inventory.Repositories))
	for i, evidence := range inventory.Repositories {
		remoteKey, err := validateRepositoryEvidence(evidence, now, maxAge)
		if err != nil {
			return fmt.Errorf("%w: repositories[%d]: %v", ErrInvalidInventory, i, err)
		}
		repositoryKey := strings.ToLower(evidence.Repository)
		if _, exists := repositories[repositoryKey]; exists {
			return fmt.Errorf("%w: duplicate repository %q", ErrInvalidInventory, evidence.Repository)
		}
		repositories[repositoryKey] = struct{}{}
		if remoteKey != "" {
			if other, exists := remotes[remoteKey]; exists {
				return fmt.Errorf("%w: repositories %q and %q use the same remote", ErrInvalidInventory, other, evidence.Repository)
			}
			remotes[remoteKey] = evidence.Repository
		}
		if evidence.Checkout != nil {
			checkoutKey := evidence.Checkout.Path
			if other, exists := checkouts[checkoutKey]; exists {
				return fmt.Errorf("%w: repositories %q and %q use the same checkout path", ErrInvalidInventory, other, evidence.Repository)
			}
			checkouts[checkoutKey] = evidence.Repository
		}
	}
	return nil
}

// Marshal validates and returns deterministic, indented JSON with a trailing
// newline. Repositories are sorted by case-folded identity and timestamps are
// rendered in UTC. The caller's value is not mutated.
func Marshal(inventory Inventory, opts Options) ([]byte, error) {
	if err := Validate(inventory, opts); err != nil {
		return nil, err
	}
	canonical := Inventory{
		SchemaVersion: inventory.SchemaVersion,
		Repositories:  slices.Clone(inventory.Repositories),
	}
	for i := range canonical.Repositories {
		canonical.Repositories[i].ObservedAt = canonical.Repositories[i].ObservedAt.UTC().Round(0)
		if checkout := canonical.Repositories[i].Checkout; checkout != nil {
			copy := *checkout
			canonical.Repositories[i].Checkout = &copy
		}
	}
	sort.Slice(canonical.Repositories, func(i, j int) bool {
		left := strings.ToLower(canonical.Repositories[i].Repository)
		right := strings.ToLower(canonical.Repositories[j].Repository)
		if left == right {
			return canonical.Repositories[i].Repository < canonical.Repositories[j].Repository
		}
		return left < right
	})
	data, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidInventory, err)
	}
	return append(data, '\n'), nil
}

func validationWindow(opts Options) (time.Time, time.Duration, error) {
	if opts.Now.IsZero() {
		return time.Time{}, 0, fmt.Errorf("%w: validation time is required", ErrInvalidInventory)
	}
	maxAge := opts.MaxAge
	if maxAge == 0 {
		maxAge = DefaultMaxAge
	}
	if maxAge < 0 {
		return time.Time{}, 0, fmt.Errorf("%w: max age must not be negative", ErrInvalidInventory)
	}
	return opts.Now.UTC().Round(0), maxAge, nil
}

func validateRepositoryEvidence(evidence RepositoryEvidence, now time.Time, maxAge time.Duration) (string, error) {
	if err := validateRepositoryIdentity(evidence.Repository); err != nil {
		return "", err
	}
	if !validLifecycle(evidence.Lifecycle) {
		return "", fmt.Errorf("unknown lifecycle %q", evidence.Lifecycle)
	}
	if !validOwnershipClass(evidence.OwnershipClass) {
		return "", fmt.Errorf("unknown ownership_class %q", evidence.OwnershipClass)
	}
	if !validEvidenceClass(evidence.EvidenceClass) {
		return "", fmt.Errorf("unknown evidence_class %q", evidence.EvidenceClass)
	}

	remoteKey := ""
	if evidence.EvidenceClass == EvidenceLocalOnly {
		if evidence.RemoteURL != "" || evidence.RequestedRef != "" || evidence.RequestedSHA != "" {
			return "", fmt.Errorf("local-only evidence must not claim remote_url, requested_ref, or requested_sha")
		}
	} else {
		var remoteRepository string
		var err error
		remoteKey, remoteRepository, err = parseRemote(evidence.RemoteURL)
		if err != nil {
			return "", err
		}
		if remoteRepository != evidence.Repository {
			return "", fmt.Errorf("remote repository %q does not match identity %q", remoteRepository, evidence.Repository)
		}
		if err := validateRef("requested_ref", evidence.RequestedRef); err != nil {
			return "", err
		}
		if err := validateSHA("requested_sha", evidence.RequestedSHA); err != nil {
			return "", err
		}
	}
	if err := validateRef("tested_ref", evidence.TestedRef); err != nil {
		return "", err
	}
	if err := validateSHA("tested_sha", evidence.TestedSHA); err != nil {
		return "", err
	}
	if evidence.RequestedRef != "" && evidence.RequestedRef == evidence.TestedRef && evidence.RequestedSHA != evidence.TestedSHA {
		return "", fmt.Errorf("the same requested and tested ref cannot resolve to different SHAs")
	}
	if evidence.ObservedAt.IsZero() {
		return "", fmt.Errorf("observed_at is required")
	}
	observedAt := evidence.ObservedAt.UTC()
	if observedAt.After(now) {
		return "", fmt.Errorf("observed_at is in the future")
	}
	if now.Sub(observedAt) > maxAge {
		return "", fmt.Errorf("observed_at is stale (older than %s)", maxAge)
	}
	if err := validateObservationCommand(evidence.ObservationCommand); err != nil {
		return "", err
	}
	if evidence.Checkout == nil {
		return "", fmt.Errorf("checkout state is required")
	}
	if err := validateCheckout(*evidence.Checkout); err != nil {
		return "", err
	}
	if err := validateEvidenceCoherence(evidence); err != nil {
		return "", err
	}
	return remoteKey, nil
}

func validateRepositoryIdentity(repository string) error {
	if len(repository) == 0 || len(repository) > 512 || strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return fmt.Errorf("invalid repository identity")
	}
	parts := strings.Split(repository, "/")
	if len(parts) < 2 || len(parts) > 8 {
		return fmt.Errorf("repository identity must contain a namespace and name")
	}
	for index, part := range parts {
		if index == len(parts)-1 && part == ".github" {
			continue
		}
		if !repositoryPartPattern.MatchString(part) || part == "." || part == ".." || strings.HasSuffix(strings.ToLower(part), ".git") {
			return fmt.Errorf("invalid repository identity component %q", part)
		}
	}
	return nil
}

func parseRemote(raw string) (key string, repository string, err error) {
	if len(raw) == 0 || len(raw) > 2048 || hasControl(raw) || strings.Contains(raw, `\`) {
		return "", "", fmt.Errorf("invalid remote_url")
	}

	var host, repoPath string
	if match := scpRemotePattern.FindStringSubmatch(raw); match != nil {
		if match[1] != "git" {
			return "", "", fmt.Errorf("scp-style remote_url user must be git")
		}
		host, repoPath = match[2], match[3]
	} else {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed == nil || parsed.Opaque != "" {
			return "", "", fmt.Errorf("invalid remote_url")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return "", "", fmt.Errorf("https remote_url must not contain user information")
			}
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return "", "", fmt.Errorf("ssh remote_url user must be git")
			}
			if _, set := parsed.User.Password(); set {
				return "", "", fmt.Errorf("ssh remote_url must not contain a password")
			}
		default:
			return "", "", fmt.Errorf("remote_url must use https or ssh")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
			return "", "", fmt.Errorf("remote_url must not contain a query, fragment, or encoded path")
		}
		if parsed.Port() != "" {
			return "", "", fmt.Errorf("remote_url must not contain an explicit port")
		}
		host, repoPath = parsed.Hostname(), strings.TrimPrefix(parsed.Path, "/")
	}

	if !safeRemoteHost(host) {
		return "", "", fmt.Errorf("unsafe remote_url host")
	}
	repoPath = strings.TrimSuffix(repoPath, ".git")
	if repoPath == "" || path.Clean(repoPath) != repoPath || strings.HasPrefix(repoPath, "/") || strings.HasSuffix(repoPath, "/") {
		return "", "", fmt.Errorf("invalid remote_url repository path")
	}
	if err := validateRepositoryIdentity(repoPath); err != nil {
		return "", "", fmt.Errorf("invalid remote_url repository path: %v", err)
	}
	canonical := strings.ToLower(host + "/" + repoPath)
	return canonical, repoPath, nil
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

func validateRef(field, ref string) error {
	if len(ref) == 0 || len(ref) > 255 || hasControl(ref) || strings.ContainsAny(ref, " ~^:?*[]\\") {
		return fmt.Errorf("invalid %s", field)
	}
	if shaPattern.MatchString(ref) {
		return nil
	}
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "//") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return fmt.Errorf("invalid %s", field)
	}
	for _, component := range strings.Split(ref, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(strings.ToLower(component), ".lock") {
			return fmt.Errorf("invalid %s", field)
		}
	}
	return nil
}

func validateSHA(field, sha string) error {
	if !shaPattern.MatchString(sha) || sha == strings.Repeat("0", 40) {
		return fmt.Errorf("%s must be a lowercase, nonzero 40-character Git SHA", field)
	}
	return nil
}

func validateCheckout(checkout Checkout) error {
	if len(checkout.Path) == 0 || len(checkout.Path) > 4096 || hasControl(checkout.Path) || strings.Contains(checkout.Path, `\`) {
		return fmt.Errorf("invalid checkout path")
	}
	if path.IsAbs(checkout.Path) || path.Clean(checkout.Path) != checkout.Path || checkout.Path == "." || strings.HasPrefix(checkout.Path, "../") {
		return fmt.Errorf("checkout path must be a normalized workspace-relative identifier")
	}
	parts := strings.Split(checkout.Path, "/")
	if len(parts) < 2 {
		return fmt.Errorf("checkout path must contain a workspace and repository component")
	}
	for index, part := range parts {
		if index == len(parts)-1 && part == ".github" {
			continue
		}
		if !repositoryPartPattern.MatchString(part) || part == "." || part == ".." {
			return fmt.Errorf("invalid checkout path component %q", part)
		}
	}
	return nil
}

func validateObservationCommand(command string) error {
	if len(command) == 0 || len(command) > 2048 || hasControl(command) {
		return fmt.Errorf("observation_command must be a non-empty single-line command without control characters")
	}
	if containsAbsolutePathToken(command) {
		return fmt.Errorf("observation_command must not contain an absolute path")
	}
	return nil
}

func containsAbsolutePathToken(command string) bool {
	if strings.HasPrefix(command, "/") {
		return true
	}
	for _, marker := range []string{" /", "='/", "=\"/", "\t/", " '/", " \"/"} {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

func validateEvidenceCoherence(evidence RepositoryEvidence) error {
	switch evidence.EvidenceClass {
	case EvidenceRemoteTree:
		if evidence.RequestedSHA != evidence.TestedSHA {
			return fmt.Errorf("remote-tree requested_sha and tested_sha must match")
		}
	case EvidenceFreshWorktree:
		if evidence.Checkout.Dirty {
			return fmt.Errorf("fresh-worktree evidence cannot be dirty")
		}
		if evidence.RequestedSHA != evidence.TestedSHA {
			return fmt.Errorf("fresh-worktree requested_sha and tested_sha must match")
		}
	case EvidencePrimaryCheckout, EvidenceLiveRuntime, EvidenceLocalOnly:
	}
	return nil
}

func validLifecycle(value Lifecycle) bool {
	switch value {
	case LifecycleActive, LifecycleMaintenanceOnly, LifecycleNeverPublish, LifecyclePublishMirror,
		LifecycleArchived, LifecycleDeprecated, LifecycleCompatibility,
		LifecycleDetached, LifecycleReferenceOnly:
		return true
	default:
		return false
	}
}

func validOwnershipClass(value OwnershipClass) bool {
	switch value {
	case OwnershipSharedProviderNeutral, OwnershipCodexHarnessSpecific,
		OwnershipClaudeHarnessSpecific, OwnershipRepoLocalProjection,
		OwnershipExternalOwner:
		return true
	default:
		return false
	}
}

func validEvidenceClass(value EvidenceClass) bool {
	switch value {
	case EvidenceRemoteTree, EvidenceFreshWorktree, EvidencePrimaryCheckout, EvidenceLiveRuntime, EvidenceLocalOnly:
		return true
	default:
		return false
	}
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode: %v", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key: %v", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid object closing delimiter")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid array closing delimiter")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %v", err)
	}
	return nil
}
