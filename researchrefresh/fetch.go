package researchrefresh

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumConfiguredResponseBytes = int64(1 << 30)

// Refresh performs one due-aware, conditional, credential-free fetch. It does
// not write files. The returned Result is a dry-run result; callers must opt in
// separately to persisting CandidateReceipt and must never persist
// SemanticContent as a receipt.
func Refresh(ctx context.Context, feed Feed, previous *Receipt, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("research refresh: nil context")
	}
	if err := validateFeed(feed); err != nil {
		return Result{}, fmt.Errorf("%w: feed: %v", ErrInvalidRegistry, err)
	}
	now, timeout, maxBytes, err := normalizedOptions(options)
	if err != nil {
		return Result{}, err
	}
	due, err := EvaluateDue(feed, previous, now)
	if err != nil {
		return Result{}, err
	}
	if !due.Due && !options.Force {
		return Result{
			FeedID:    feed.ID,
			Status:    StatusNotDue,
			Due:       false,
			Changed:   false,
			CheckedAt: due.CheckedAt,
			NextDueAt: due.NextDueAt,
		}, nil
	}

	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, feed.URL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("create research refresh request: %w", err)
	}
	request.Header.Set("Accept", acceptHeader(feed.Kind, feed.ContentTypes))
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "codexkit-researchrefresh/1")
	matchingPrevious := previous != nil && previous.SourceURL == feed.URL
	conditionalHeadersSent := false
	if matchingPrevious {
		if previous.ETag != "" {
			request.Header.Set("If-None-Match", previous.ETag)
			conditionalHeadersSent = true
		}
		if previous.LastModified != "" {
			request.Header.Set("If-Modified-Since", previous.LastModified)
			conditionalHeadersSent = true
		}
	}

	client, err := hardenedClient(options.Client, feed)
	if err != nil {
		return Result{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return Result{}, fmt.Errorf("fetch research feed %q: %w", feed.ID, err)
	}
	defer response.Body.Close()

	finalURL, err := sanitizeURL(response.Request.URL.String())
	if err != nil {
		return Result{}, fmt.Errorf("%w: final response URL", ErrUnsafeURL)
	}
	if response.StatusCode == http.StatusNotModified {
		if !conditionalHeadersSent {
			return Result{}, fmt.Errorf("research feed %q returned 304 without a conditional request", feed.ID)
		}
		return notModifiedResult(feed, *previous, response, finalURL, now)
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("research feed %q returned HTTP status %d", feed.ID, response.StatusCode)
	}

	contentType, err := validateResponseContentType(feed, response.Header)
	if err != nil {
		return Result{}, err
	}
	body, err := readCapped(response.Body, maxBytes)
	if err != nil {
		return Result{}, err
	}
	rawHash := sha256.Sum256(body)
	rawSHA256 := hex.EncodeToString(rawHash[:])
	providerSHA256, err := verifyProviderHash(feed, response.Header, rawHash[:])
	if err != nil {
		return Result{}, err
	}
	semantic, release, err := extractSemantic(feed.Kind, body)
	if err != nil {
		return Result{}, fmt.Errorf("extract semantic content for feed %q: %w", feed.ID, err)
	}
	semanticHash := sha256.Sum256(semantic)
	semanticSHA256 := hex.EncodeToString(semanticHash[:])

	etag, lastModified, err := responseValidators(response.Header, "", "")
	if err != nil {
		return Result{}, err
	}
	sourceURL, err := sanitizeURL(feed.URL)
	if err != nil {
		return Result{}, err
	}
	receipt := Receipt{
		SchemaVersion:  ReceiptSchemaVersion,
		FeedID:         feed.ID,
		SourceURL:      sourceURL,
		FinalURL:       finalURL,
		Status:         StatusFetched,
		CheckedAt:      now,
		FetchedAt:      now,
		HTTPStatus:     http.StatusOK,
		ContentType:    contentType,
		ETag:           etag,
		LastModified:   lastModified,
		RawSHA256:      rawSHA256,
		SemanticSHA256: semanticSHA256,
		ProviderSHA256: providerSHA256,
		Release:        release,
	}
	if err := ValidateReceiptForFeed(feed, receipt, now); err != nil {
		return Result{}, fmt.Errorf("build receipt: %w", err)
	}
	changed := previous == nil || previous.SemanticSHA256 != semanticSHA256
	return Result{
		FeedID:          feed.ID,
		Status:          StatusFetched,
		Due:             true,
		Changed:         changed,
		CheckedAt:       now,
		NextDueAt:       now.Add(time.Duration(feed.CadenceHours) * time.Hour),
		HTTPStatus:      http.StatusOK,
		RawSHA256:       rawSHA256,
		SemanticSHA256:  semanticSHA256,
		SemanticContent: slices.Clone(semantic),
		candidate:       &receipt,
	}, nil
}

func normalizedOptions(options Options) (time.Time, time.Duration, int64, error) {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 {
		return time.Time{}, 0, 0, fmt.Errorf("research refresh timeout must be positive")
	}
	maxBytes := options.MaxResponseBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	if maxBytes < 1 || maxBytes > maximumConfiguredResponseBytes {
		return time.Time{}, 0, 0, fmt.Errorf("research refresh max response bytes must be between 1 and %d", maximumConfiguredResponseBytes)
	}
	return now, timeout, maxBytes, nil
}

func hardenedClient(configured *http.Client, feed Feed) (*http.Client, error) {
	var client http.Client
	if configured != nil {
		client = *configured
	}
	switch transport := client.Transport.(type) {
	case nil:
		client.Transport = &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true,
		}
	case *http.Transport:
		clone := transport.Clone()
		clone.Proxy = nil
		clone.ProxyConnectHeader = nil
		clone.GetProxyConnectHeader = nil
		clone.DisableCompression = true
		if clone.TLSClientConfig != nil {
			clone.TLSClientConfig = clone.TLSClientConfig.Clone()
			if len(clone.TLSClientConfig.Certificates) != 0 || clone.TLSClientConfig.GetClientCertificate != nil {
				return nil, fmt.Errorf("research refresh client certificates are not allowed")
			}
		}
		client.Transport = clone
	default:
		return nil, fmt.Errorf("research refresh client transport must be *http.Transport")
	}
	client.Jar = nil
	feedURL, err := validateHTTPSURL(feed.URL, false)
	if err != nil {
		return nil, err
	}
	allowedDomains := map[string]struct{}{strings.ToLower(feedURL.Hostname()): {}}
	for _, domain := range feed.AllowedRedirectDomains {
		allowedDomains[domain] = struct{}{}
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return ErrRedirectDenied
		}
		if request.Method != http.MethodGet {
			return ErrRedirectDenied
		}
		if err := validateRedirectTarget(feed.URL, request.URL.String()); err != nil {
			return ErrRedirectDenied
		}
		if _, allowed := allowedDomains[strings.ToLower(request.URL.Hostname())]; !allowed {
			return ErrRedirectDenied
		}
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		return nil
	}
	return &client, nil
}

func notModifiedResult(feed Feed, previous Receipt, response *http.Response, finalURL string, now time.Time) (Result, error) {
	etag, lastModified, err := responseValidators(response.Header, previous.ETag, previous.LastModified)
	if err != nil {
		return Result{}, err
	}
	receipt := cloneReceipt(previous)
	receipt.SourceURL = feed.URL
	receipt.FinalURL = finalURL
	receipt.Status = StatusNotModified
	receipt.CheckedAt = now
	receipt.HTTPStatus = http.StatusNotModified
	receipt.ETag = etag
	receipt.LastModified = lastModified
	if err := ValidateReceiptForFeed(feed, receipt, now); err != nil {
		return Result{}, fmt.Errorf("build 304 receipt: %w", err)
	}
	return Result{
		FeedID:         feed.ID,
		Status:         StatusNotModified,
		Due:            true,
		Changed:        false,
		CheckedAt:      now,
		NextDueAt:      now.Add(time.Duration(feed.CadenceHours) * time.Hour),
		HTTPStatus:     http.StatusNotModified,
		RawSHA256:      receipt.RawSHA256,
		SemanticSHA256: receipt.SemanticSHA256,
		candidate:      &receipt,
	}, nil
}

func validateResponseContentType(feed Feed, headers http.Header) (string, error) {
	if encoding := strings.TrimSpace(headers.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return "", fmt.Errorf("%w: unsupported Content-Encoding", ErrUnexpectedContentType)
	}
	values := headers.Values("Content-Type")
	if len(values) != 1 {
		return "", fmt.Errorf("%w: expected exactly one Content-Type header", ErrUnexpectedContentType)
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil {
		return "", fmt.Errorf("%w: malformed Content-Type", ErrUnexpectedContentType)
	}
	mediaType = strings.ToLower(mediaType)
	allowed := feed.ContentTypes
	if len(allowed) == 0 {
		allowed = defaultContentTypes(feed.Kind)
	}
	if !slices.Contains(allowed, mediaType) {
		return "", fmt.Errorf("%w: %s for kind %s", ErrUnexpectedContentType, mediaType, feed.Kind)
	}
	if charset, ok := params["charset"]; ok && !strings.EqualFold(charset, "utf-8") && !strings.EqualFold(charset, "us-ascii") {
		return "", fmt.Errorf("%w: unsupported charset", ErrUnexpectedContentType)
	}
	return mediaType, nil
}

func defaultContentTypes(kind FeedKind) []string {
	switch kind {
	case KindMarkdown:
		return []string{"text/markdown", "text/plain"}
	case KindText:
		return []string{"text/plain"}
	case KindHTML:
		return []string{"application/xhtml+xml", "text/html"}
	case KindGitHubLatestRelease:
		return []string{"application/json", "application/vnd.github+json"}
	default:
		return nil
	}
}

func acceptHeader(kind FeedKind, configured []string) string {
	contentTypes := configured
	if len(contentTypes) == 0 {
		contentTypes = defaultContentTypes(kind)
	}
	return strings.Join(contentTypes, ", ")
}

func readCapped(reader io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read research feed response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func verifyProviderHash(feed Feed, headers http.Header, actual []byte) (string, error) {
	if !feed.VerifyProviderSHA256 {
		return "", nil
	}
	values := headers.Values("X-Content-SHA256")
	if len(values) != 1 {
		return "", fmt.Errorf("%w: expected exactly one x-content-sha256 header", ErrProviderHash)
	}
	declared := strings.TrimSpace(values[0])
	declared = strings.TrimPrefix(strings.ToLower(declared), "sha256=")
	if !validSHA256(declared) {
		return "", fmt.Errorf("%w: malformed x-content-sha256 header", ErrProviderHash)
	}
	expected, _ := hex.DecodeString(declared)
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return "", fmt.Errorf("%w: x-content-sha256 mismatch", ErrProviderHash)
	}
	return declared, nil
}

func responseValidators(headers http.Header, fallbackETag, fallbackLastModified string) (string, string, error) {
	etag := fallbackETag
	if values := headers.Values("ETag"); len(values) > 1 {
		return "", "", fmt.Errorf("invalid response ETag: multiple values")
	} else if len(values) == 1 && values[0] != "" {
		etag = values[0]
	}
	lastModified := fallbackLastModified
	if values := headers.Values("Last-Modified"); len(values) > 1 {
		return "", "", fmt.Errorf("invalid response Last-Modified: multiple values")
	} else if len(values) == 1 && values[0] != "" {
		lastModified = values[0]
	}
	if err := validateHeaderEvidence("etag", etag); err != nil {
		return "", "", err
	}
	if err := validateHeaderEvidence("last_modified", lastModified); err != nil {
		return "", "", err
	}
	if lastModified != "" {
		if _, err := http.ParseTime(lastModified); err != nil {
			return "", "", fmt.Errorf("invalid response Last-Modified")
		}
	}
	return etag, lastModified, nil
}

func extractSemantic(kind FeedKind, body []byte) ([]byte, *ReleaseMetadata, error) {
	switch kind {
	case KindMarkdown, KindText, KindHTML:
		normalized, err := normalizeText(body)
		return normalized, nil, err
	case KindGitHubLatestRelease:
		return extractGitHubRelease(body)
	default:
		return nil, nil, fmt.Errorf("unsupported feed kind %q", kind)
	}
}

func normalizeText(body []byte) ([]byte, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("content is not valid UTF-8")
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []byte{}, nil
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func extractGitHubRelease(body []byte) ([]byte, *ReleaseMetadata, error) {
	var upstream struct {
		TagName         string  `json:"tag_name"`
		PublishedAt     string  `json:"published_at"`
		HTMLURL         string  `json:"html_url"`
		Prerelease      bool    `json:"prerelease"`
		Draft           bool    `json:"draft"`
		TargetCommitish string  `json:"target_commitish"`
		Body            *string `json:"body"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&upstream); err != nil {
		return nil, nil, fmt.Errorf("decode GitHub release JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, nil, fmt.Errorf("multiple JSON values in GitHub release response")
		}
		return nil, nil, fmt.Errorf("decode trailing GitHub release JSON: %w", err)
	}
	publishedAt, err := time.Parse(time.RFC3339, upstream.PublishedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid published_at")
	}
	releaseURL, err := sanitizeURL(upstream.HTMLURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid html_url: %w", err)
	}
	bodyText := ""
	if upstream.Body != nil {
		bodyText = *upstream.Body
	}
	normalizedBody, err := normalizeText([]byte(bodyText))
	if err != nil {
		return nil, nil, fmt.Errorf("normalize release body: %w", err)
	}
	bodyHash := sha256.Sum256(normalizedBody)
	release := &ReleaseMetadata{
		Tag:             upstream.TagName,
		PublishedAt:     publishedAt.UTC(),
		URL:             releaseURL,
		Prerelease:      upstream.Prerelease,
		Draft:           upstream.Draft,
		TargetCommitish: upstream.TargetCommitish,
		BodySHA256:      hex.EncodeToString(bodyHash[:]),
	}
	if err := validateRelease(*release); err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(release)
	if err != nil {
		return nil, nil, fmt.Errorf("encode canonical GitHub release: %w", err)
	}
	return canonical, release, nil
}
