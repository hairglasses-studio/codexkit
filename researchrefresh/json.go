package researchrefresh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var feedIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

const maxCadenceHours = int((1<<63 - 1) / int64(time.Hour))

// LoadRegistry decodes and validates exactly one registry JSON document.
func LoadRegistry(data []byte) (Registry, error) {
	var registry Registry
	if err := decodeStrictJSON(data, &registry); err != nil {
		return Registry{}, fmt.Errorf("%w: %v", ErrInvalidRegistry, err)
	}
	if err := ValidateRegistry(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

// EncodeRegistry validates and deterministically encodes a registry. Logical
// set-valued fields are sorted in the encoded copy; the caller is not mutated.
func EncodeRegistry(registry Registry) ([]byte, error) {
	if err := ValidateRegistry(registry); err != nil {
		return nil, err
	}
	canonical := Registry{SchemaVersion: registry.SchemaVersion, Feeds: cloneFeeds(registry.Feeds)}
	sort.Slice(canonical.Feeds, func(i, j int) bool { return canonical.Feeds[i].ID < canonical.Feeds[j].ID })
	for i := range canonical.Feeds {
		slices.Sort(canonical.Feeds[i].AllowedRedirectDomains)
		slices.Sort(canonical.Feeds[i].ContentTypes)
	}
	return encodeDeterministicJSON(canonical)
}

// ValidateRegistry fails closed on unsupported schemas, duplicate identifiers,
// unsafe URLs, invalid redirect allowlists, and unexpected media types.
func ValidateRegistry(registry Registry) error {
	if registry.SchemaVersion != RegistrySchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidRegistry, RegistrySchemaVersion)
	}
	if registry.Feeds == nil {
		return fmt.Errorf("%w: feeds must be an array", ErrInvalidRegistry)
	}
	seen := make(map[string]struct{}, len(registry.Feeds))
	for i, feed := range registry.Feeds {
		if err := validateFeed(feed); err != nil {
			return fmt.Errorf("%w: feeds[%d]: %v", ErrInvalidRegistry, i, err)
		}
		if _, ok := seen[feed.ID]; ok {
			return fmt.Errorf("%w: duplicate feed id %q", ErrInvalidRegistry, feed.ID)
		}
		seen[feed.ID] = struct{}{}
	}
	return nil
}

// Feed returns a defensive copy of the named feed.
func (r Registry) Feed(id string) (Feed, bool) {
	for _, feed := range r.Feeds {
		if feed.ID == id {
			feed.AllowedRedirectDomains = slices.Clone(feed.AllowedRedirectDomains)
			feed.ContentTypes = slices.Clone(feed.ContentTypes)
			return feed, true
		}
	}
	return Feed{}, false
}

// LoadReceipt decodes and validates exactly one sanitized receipt.
func LoadReceipt(data []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeStrictJSON(data, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// EncodeReceipt validates and deterministically encodes one receipt.
func EncodeReceipt(receipt Receipt) ([]byte, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return nil, err
	}
	canonicalizeReceiptTimes(&receipt)
	return encodeDeterministicJSON(receipt)
}

// LoadReceipts decodes and validates exactly one aggregate receipt document.
func LoadReceipts(data []byte) (Receipts, error) {
	var receipts Receipts
	if err := decodeStrictJSON(data, &receipts); err != nil {
		return Receipts{}, fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	if err := ValidateReceipts(receipts); err != nil {
		return Receipts{}, err
	}
	return receipts, nil
}

// EncodeReceipts validates and deterministically encodes receipts ordered by
// feed_id. It does not mutate the caller.
func EncodeReceipts(receipts Receipts) ([]byte, error) {
	if err := ValidateReceipts(receipts); err != nil {
		return nil, err
	}
	canonical := Receipts{
		SchemaVersion: receipts.SchemaVersion,
		Receipts:      make([]Receipt, len(receipts.Receipts)),
	}
	for i := range receipts.Receipts {
		canonical.Receipts[i] = cloneReceipt(receipts.Receipts[i])
		canonicalizeReceiptTimes(&canonical.Receipts[i])
	}
	sort.Slice(canonical.Receipts, func(i, j int) bool {
		return canonical.Receipts[i].FeedID < canonical.Receipts[j].FeedID
	})
	return encodeDeterministicJSON(canonical)
}

// ValidateReceipts fails closed on unsupported schemas or duplicate feed IDs.
func ValidateReceipts(receipts Receipts) error {
	if receipts.SchemaVersion != ReceiptSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidReceipt, ReceiptSchemaVersion)
	}
	if receipts.Receipts == nil {
		return fmt.Errorf("%w: receipts must be an array", ErrInvalidReceipt)
	}
	seen := make(map[string]struct{}, len(receipts.Receipts))
	for i, receipt := range receipts.Receipts {
		if err := ValidateReceipt(receipt); err != nil {
			return fmt.Errorf("%w: receipts[%d]: %v", ErrInvalidReceipt, i, err)
		}
		if _, ok := seen[receipt.FeedID]; ok {
			return fmt.Errorf("%w: duplicate receipt feed_id %q", ErrInvalidReceipt, receipt.FeedID)
		}
		seen[receipt.FeedID] = struct{}{}
	}
	return nil
}

// Receipt returns a defensive copy of the named aggregate receipt.
func (r Receipts) Receipt(feedID string) (Receipt, bool) {
	for _, receipt := range r.Receipts {
		if receipt.FeedID == feedID {
			return cloneReceipt(receipt), true
		}
	}
	return Receipt{}, false
}

// WithReceipt returns a defensive aggregate copy with receipt inserted or
// replaced. The result is ordered by feed_id and ready for EncodeReceipts.
func (r Receipts) WithReceipt(receipt Receipt) (Receipts, error) {
	if err := ValidateReceipt(receipt); err != nil {
		return Receipts{}, err
	}
	if r.SchemaVersion == 0 && r.Receipts == nil {
		r = Receipts{SchemaVersion: ReceiptSchemaVersion, Receipts: []Receipt{}}
	}
	if err := ValidateReceipts(r); err != nil {
		return Receipts{}, err
	}
	result := Receipts{SchemaVersion: r.SchemaVersion, Receipts: make([]Receipt, 0, len(r.Receipts)+1)}
	replaced := false
	for _, current := range r.Receipts {
		if current.FeedID == receipt.FeedID {
			result.Receipts = append(result.Receipts, cloneReceipt(receipt))
			replaced = true
			continue
		}
		result.Receipts = append(result.Receipts, cloneReceipt(current))
	}
	if !replaced {
		result.Receipts = append(result.Receipts, cloneReceipt(receipt))
	}
	sort.Slice(result.Receipts, func(i, j int) bool { return result.Receipts[i].FeedID < result.Receipts[j].FeedID })
	return result, nil
}

// ValidateReceipt verifies that receipt evidence is structurally safe to use
// for conditional requests and cadence decisions.
func ValidateReceipt(receipt Receipt) error {
	if receipt.SchemaVersion != ReceiptSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidReceipt, ReceiptSchemaVersion)
	}
	if !feedIDPattern.MatchString(receipt.FeedID) {
		return fmt.Errorf("%w: invalid feed_id", ErrInvalidReceipt)
	}
	if err := validateCuratedReceiptURLs(receipt.SourceURL, receipt.FinalURL); err != nil {
		return fmt.Errorf("%w: receipt URLs: %v", ErrInvalidReceipt, err)
	}
	if receipt.CheckedAt.IsZero() || receipt.FetchedAt.IsZero() {
		return fmt.Errorf("%w: checked_at and fetched_at are required", ErrInvalidReceipt)
	}
	if receipt.FetchedAt.After(receipt.CheckedAt) {
		return fmt.Errorf("%w: fetched_at is after checked_at", ErrInvalidReceipt)
	}
	switch receipt.Status {
	case StatusFetched:
		if receipt.HTTPStatus != http.StatusOK {
			return fmt.Errorf("%w: fetched receipt must have HTTP status 200", ErrInvalidReceipt)
		}
	case StatusNotModified:
		if receipt.HTTPStatus != http.StatusNotModified {
			return fmt.Errorf("%w: not_modified receipt must have HTTP status 304", ErrInvalidReceipt)
		}
		if receipt.ETag == "" && receipt.LastModified == "" {
			return fmt.Errorf("%w: not_modified receipt requires a conditional validator", ErrInvalidReceipt)
		}
	default:
		return fmt.Errorf("%w: invalid status %q", ErrInvalidReceipt, receipt.Status)
	}
	if receipt.ContentType == "" {
		return fmt.Errorf("%w: content_type is required", ErrInvalidReceipt)
	}
	mediaType, _, err := mime.ParseMediaType(receipt.ContentType)
	if err != nil || mediaType != strings.ToLower(mediaType) {
		return fmt.Errorf("%w: invalid content_type", ErrInvalidReceipt)
	}
	if err := validateHeaderEvidence("etag", receipt.ETag); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	if err := validateHeaderEvidence("last_modified", receipt.LastModified); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	if receipt.LastModified != "" {
		if _, err := http.ParseTime(receipt.LastModified); err != nil {
			return fmt.Errorf("%w: invalid last_modified", ErrInvalidReceipt)
		}
	}
	for name, value := range map[string]string{
		"raw_sha256":      receipt.RawSHA256,
		"semantic_sha256": receipt.SemanticSHA256,
	} {
		if !validSHA256(value) {
			return fmt.Errorf("%w: invalid %s", ErrInvalidReceipt, name)
		}
	}
	if receipt.ProviderSHA256 != "" && !validSHA256(receipt.ProviderSHA256) {
		return fmt.Errorf("%w: invalid provider_sha256", ErrInvalidReceipt)
	}
	if receipt.ProviderSHA256 != "" && receipt.ProviderSHA256 != receipt.RawSHA256 {
		return fmt.Errorf("%w: provider_sha256 does not match raw_sha256", ErrInvalidReceipt)
	}
	if receipt.Release != nil {
		if err := validateRelease(*receipt.Release); err != nil {
			return fmt.Errorf("%w: release: %v", ErrInvalidReceipt, err)
		}
	}
	return nil
}

// ValidateReceiptForFeed binds structurally valid receipt evidence to one
// exact registry feed and evaluation time before it may affect freshness or a
// conditional request.
func ValidateReceiptForFeed(feed Feed, receipt Receipt, now time.Time) error {
	if err := validateFeed(feed); err != nil {
		return fmt.Errorf("%w: feed: %v", ErrInvalidRegistry, err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if receipt.FeedID != feed.ID {
		return fmt.Errorf("%w: receipt feed_id %q does not match feed %q", ErrInvalidReceipt, receipt.FeedID, feed.ID)
	}
	if receipt.SourceURL != feed.URL {
		return fmt.Errorf("%w: receipt source_url does not match feed", ErrInvalidReceipt)
	}
	if receipt.CheckedAt.UTC().After(now) {
		return fmt.Errorf("%w: checked_at is in the future", ErrInvalidReceipt)
	}
	if receipt.FinalURL != feed.URL {
		finalURL, err := url.Parse(receipt.FinalURL)
		if err != nil || !slices.Contains(feed.AllowedRedirectDomains, finalURL.Hostname()) {
			return fmt.Errorf("%w: receipt redirect is not configured for feed", ErrInvalidReceipt)
		}
	}
	mediaType, _, _ := mime.ParseMediaType(receipt.ContentType)
	allowedContentTypes := feed.ContentTypes
	if len(allowedContentTypes) == 0 {
		allowedContentTypes = defaultContentTypes(feed.Kind)
	}
	if !slices.Contains(allowedContentTypes, mediaType) {
		return fmt.Errorf("%w: receipt content_type is not allowed for feed", ErrInvalidReceipt)
	}
	if feed.VerifyProviderSHA256 != (receipt.ProviderSHA256 != "") {
		return fmt.Errorf("%w: provider hash evidence does not match feed policy", ErrInvalidReceipt)
	}
	if feed.Kind == KindGitHubLatestRelease {
		if receipt.Release == nil {
			return fmt.Errorf("%w: GitHub release receipt is missing release metadata", ErrInvalidReceipt)
		}
		if err := validateReleaseForFeed(feed, *receipt.Release); err != nil {
			return fmt.Errorf("%w: release: %v", ErrInvalidReceipt, err)
		}
		if receipt.Release.PublishedAt.UTC().After(receipt.CheckedAt.UTC()) {
			return fmt.Errorf("%w: release published_at is after checked_at", ErrInvalidReceipt)
		}
		canonicalRelease := *receipt.Release
		canonicalRelease.PublishedAt = canonicalRelease.PublishedAt.UTC()
		canonical, err := json.Marshal(&canonicalRelease)
		if err != nil {
			return fmt.Errorf("%w: encode canonical release: %v", ErrInvalidReceipt, err)
		}
		hash := sha256.Sum256(canonical)
		if receipt.SemanticSHA256 != hex.EncodeToString(hash[:]) {
			return fmt.Errorf("%w: semantic_sha256 does not match release metadata", ErrInvalidReceipt)
		}
	} else if receipt.Release != nil {
		return fmt.Errorf("%w: non-release feed contains release metadata", ErrInvalidReceipt)
	}
	return nil
}

func validateReleaseForFeed(feed Feed, release ReleaseMetadata) error {
	source, err := url.Parse(feed.URL)
	if err != nil {
		return err
	}
	const prefix = "/repos/"
	const suffix = "/releases/latest"
	if source.Hostname() != "api.github.com" || !strings.HasPrefix(source.Path, prefix) || !strings.HasSuffix(source.Path, suffix) {
		return fmt.Errorf("feed is not a GitHub latest-release endpoint")
	}
	repository := strings.TrimSuffix(strings.TrimPrefix(source.Path, prefix), suffix)
	expected := "https://github.com/" + repository + "/releases/tag/"
	if !strings.HasPrefix(release.URL, expected) || len(release.URL) == len(expected) {
		return fmt.Errorf("release URL does not match feed repository")
	}
	return nil
}

func validateFeed(feed Feed) error {
	if !feedIDPattern.MatchString(feed.ID) {
		return fmt.Errorf("invalid id %q", feed.ID)
	}
	switch feed.Kind {
	case KindMarkdown, KindText, KindHTML, KindGitHubLatestRelease:
	default:
		return fmt.Errorf("unsupported kind %q", feed.Kind)
	}
	if err := validateCuratedFeedURL(feed.URL, feed.Kind); err != nil {
		return fmt.Errorf("url: %w", err)
	}
	if feed.CadenceHours <= 0 || feed.CadenceHours > maxCadenceHours {
		return fmt.Errorf("cadence_hours must be between 1 and %d", maxCadenceHours)
	}
	seenDomains := make(map[string]struct{}, len(feed.AllowedRedirectDomains))
	for _, domain := range feed.AllowedRedirectDomains {
		normalized, err := normalizeDomain(domain)
		if err != nil || normalized != domain {
			return fmt.Errorf("invalid allowed redirect domain %q", domain)
		}
		if err := validateConfiguredRedirect(feed.URL, normalized); err != nil {
			return fmt.Errorf("invalid allowed redirect domain %q: %v", domain, err)
		}
		if _, ok := seenDomains[normalized]; ok {
			return fmt.Errorf("duplicate allowed redirect domain %q", domain)
		}
		seenDomains[normalized] = struct{}{}
	}
	seenTypes := make(map[string]struct{}, len(feed.ContentTypes))
	for _, contentType := range feed.ContentTypes {
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil || len(params) != 0 || mediaType != contentType || mediaType != strings.ToLower(mediaType) {
			return fmt.Errorf("invalid content type %q", contentType)
		}
		if _, ok := seenTypes[mediaType]; ok {
			return fmt.Errorf("duplicate content type %q", contentType)
		}
		seenTypes[mediaType] = struct{}{}
	}
	return nil
}

func validateRelease(release ReleaseMetadata) error {
	if strings.TrimSpace(release.Tag) == "" || release.Tag != strings.TrimSpace(release.Tag) {
		return fmt.Errorf("tag is required and must not have surrounding whitespace")
	}
	if release.PublishedAt.IsZero() {
		return fmt.Errorf("published_at is required")
	}
	if err := validateCuratedReleaseURL(release.URL); err != nil {
		return fmt.Errorf("url: %w", err)
	}
	if release.TargetCommitish == "" || strings.TrimSpace(release.TargetCommitish) != release.TargetCommitish {
		return fmt.Errorf("target_commitish is required and must not have surrounding whitespace")
	}
	if !validSHA256(release.BodySHA256) {
		return fmt.Errorf("invalid body_sha256")
	}
	return nil
}

func validateHTTPSURL(raw string, allowQuery bool) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, ErrUnsafeURL
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return nil, ErrUnsafeURL
	}
	if u.User != nil || u.Fragment != "" || (!allowQuery && (u.RawQuery != "" || u.ForceQuery)) {
		return nil, ErrUnsafeURL
	}
	hostname := u.Hostname()
	if hostname == "" || !isASCII(hostname) || strings.Contains(hostname, "%") {
		return nil, ErrUnsafeURL
	}
	if u.Host != hostname || net.ParseIP(hostname) != nil || isInternalHostname(hostname) {
		return nil, ErrUnsafeURL
	}
	return u, nil
}

func sanitizeURL(raw string) (string, error) {
	u, err := validateHTTPSURL(raw, true)
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}

func normalizeDomain(domain string) (string, error) {
	if domain == "" || strings.TrimSpace(domain) != domain || strings.ContainsAny(domain, "/:@?#[]*%\r\n\x00") || !isASCII(domain) {
		return "", ErrUnsafeURL
	}
	normalized := strings.ToLower(strings.TrimSuffix(domain, "."))
	if normalized == "" || normalized != domain || net.ParseIP(normalized) != nil || isInternalHostname(normalized) {
		return "", ErrUnsafeURL
	}
	return normalized, nil
}

func isInternalHostname(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	return hostname == "localhost" ||
		strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") ||
		strings.HasSuffix(hostname, ".internal")
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validateHeaderEvidence(name, value string) error {
	if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errorsIsEOF(err) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func encodeDeterministicJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func cloneFeeds(feeds []Feed) []Feed {
	result := make([]Feed, len(feeds))
	for i, feed := range feeds {
		result[i] = feed
		result[i].AllowedRedirectDomains = slices.Clone(feed.AllowedRedirectDomains)
		result[i].ContentTypes = slices.Clone(feed.ContentTypes)
	}
	return result
}

func cloneReceipt(receipt Receipt) Receipt {
	if receipt.Release != nil {
		release := *receipt.Release
		receipt.Release = &release
	}
	return receipt
}

func canonicalizeReceiptTimes(receipt *Receipt) {
	receipt.CheckedAt = receipt.CheckedAt.UTC()
	receipt.FetchedAt = receipt.FetchedAt.UTC()
	if receipt.Release != nil {
		receipt.Release.PublishedAt = receipt.Release.PublishedAt.UTC()
	}
}

func isASCII(value string) bool {
	for _, ch := range value {
		if ch > 127 {
			return false
		}
	}
	return true
}
