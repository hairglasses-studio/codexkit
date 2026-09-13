package researchgateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hairglasses-studio/codexkit/researchrefresh"
)

type storedDocument struct {
	metadata Document
	content  []byte
}

// Gateway is an immutable, concurrency-safe research snapshot.
type Gateway struct {
	limits           Limits
	catalog          Catalog
	documents        map[string]storedDocument
	upstreams        map[string]Upstream
	records          map[string]ResearchRecord
	knownRootIDs     map[string]struct{}
	knownRegisterIDs map[string]struct{}
}

// Open reads and validates one offline snapshot. It performs no network calls
// and writes no files. Freshness is evaluated exactly once at Catalog.ObservedAt;
// callers reopen the gateway to advance wall-clock freshness.
func Open(ctx context.Context, config Config) (*Gateway, error) {
	return OpenWithLoaderRegistry(ctx, config, DefaultLoaderRegistry())
}

// OpenWithLoaderRegistry builds a snapshot using an explicit modular format
// registry. Files are still resolved, contained, and bounded by the gateway;
// loaders receive bytes rather than filesystem authority.
func OpenWithLoaderRegistry(ctx context.Context, config Config, loaders *LoaderRegistry) (*Gateway, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	root, err := resolveHarnessRoot(config.HarnessRoot)
	if err != nil {
		return nil, err
	}
	registryPath, err := resolveExisting(root, config.RegistryPath, DefaultRegistryPath, false)
	if err != nil {
		return nil, fmt.Errorf("resolve registry: %w", err)
	}
	receiptsPath, receiptsExist, err := resolveOptionalFile(root, config.ReceiptsPath, DefaultReceiptsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve receipts: %w", err)
	}
	registryRelative, err := harnessRelative(root, registryPath)
	if err != nil {
		return nil, err
	}
	receiptsRelative, err := harnessRelative(root, receiptsPath)
	if err != nil {
		return nil, err
	}
	registryData, _, err := readBoundedFile(registryPath, maxMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	registry, err := researchrefresh.LoadRegistry(registryData)
	if err != nil {
		return nil, err
	}
	receipts := researchrefresh.Receipts{
		SchemaVersion: researchrefresh.ReceiptSchemaVersion,
		Receipts:      []researchrefresh.Receipt{},
	}
	if receiptsExist {
		receiptData, _, readErr := readBoundedFile(receiptsPath, maxMetadataBytes)
		if readErr != nil {
			return nil, fmt.Errorf("read receipts: %w", readErr)
		}
		receipts, err = researchrefresh.LoadReceipts(receiptData)
		if err != nil {
			return nil, err
		}
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	registers, records, recordMap, knownRegisters, registerSummary, err := loadRegisters(ctx, root, config.Registers, loaders, now, limits)
	if err != nil {
		return nil, err
	}

	upstreams, upstreamMap, summary, err := buildUpstreams(registry, receipts, registryRelative, receiptsRelative, now)
	if err != nil {
		return nil, err
	}
	roots, resolvedRoots, err := resolveLocalRoots(root, config.LocalRoots)
	if err != nil {
		return nil, err
	}
	documents, stored, totalBytes, err := scanDocuments(ctx, root, resolvedRoots, limits)
	if err != nil {
		return nil, err
	}
	summary.Roots = len(roots)
	summary.Documents = len(documents)
	summary.Bytes = totalBytes
	summary.Registers = registerSummary.Registers
	summary.Records = registerSummary.Records
	summary.RecordFresh = registerSummary.RecordFresh
	summary.RecordStale = registerSummary.RecordStale
	catalog := Catalog{
		SchemaVersion: SchemaVersion,
		ObservedAt:    now,
		RegistryPath:  registryRelative,
		ReceiptsPath:  receiptsRelative,
		Summary:       summary,
		Roots:         roots,
		Upstreams:     upstreams,
		Documents:     documents,
		Registers:     registers,
		Records:       records,
	}
	snapshotID, err := catalogDigest(catalog)
	if err != nil {
		return nil, fmt.Errorf("fingerprint catalog: %w", err)
	}
	catalog.SnapshotID = snapshotID
	knownRoots := make(map[string]struct{}, len(roots))
	for _, localRoot := range roots {
		knownRoots[localRoot.ID] = struct{}{}
	}
	return &Gateway{
		limits:           limits,
		catalog:          catalog,
		documents:        stored,
		upstreams:        upstreamMap,
		records:          recordMap,
		knownRootIDs:     knownRoots,
		knownRegisterIDs: knownRegisters,
	}, nil
}

func buildUpstreams(
	registry researchrefresh.Registry,
	receipts researchrefresh.Receipts,
	registryPath string,
	receiptsPath string,
	now time.Time,
) ([]Upstream, map[string]Upstream, CatalogSummary, error) {
	feeds := append([]researchrefresh.Feed(nil), registry.Feeds...)
	sort.Slice(feeds, func(i, j int) bool { return feeds[i].ID < feeds[j].ID })
	upstreams := make([]Upstream, 0, len(feeds))
	byID := make(map[string]Upstream, len(feeds))
	summary := CatalogSummary{Upstreams: len(feeds)}
	for _, feed := range feeds {
		item := Upstream{
			ID:           upstreamID(feed.ID),
			URI:          "research://upstream/" + feed.ID,
			FeedID:       feed.ID,
			URL:          feed.URL,
			Kind:         string(feed.Kind),
			CadenceHours: feed.CadenceHours,
			Freshness:    Freshness{Status: FreshnessMissing},
			RegistryPath: registryPath,
			ReceiptsPath: receiptsPath,
		}
		if receipt, ok := receipts.Receipt(feed.ID); ok {
			if receipt.SourceURL != feed.URL {
				return nil, nil, CatalogSummary{}, fmt.Errorf("receipt source URL for %q does not match registry", feed.ID)
			}
			due, err := researchrefresh.EvaluateDue(feed, &receipt, now)
			if err != nil {
				return nil, nil, CatalogSummary{}, fmt.Errorf("evaluate %q freshness: %w", feed.ID, err)
			}
			status := FreshnessFresh
			if due.Due {
				status = FreshnessStale
				summary.Stale++
			} else {
				summary.Fresh++
			}
			item.Freshness = Freshness{Status: status, CheckedAt: timePointer(due.CheckedAt), NextDueAt: timePointer(due.NextDueAt)}
			item.Receipt = &ReceiptProvenance{
				Status:         string(receipt.Status),
				SourceURL:      receipt.SourceURL,
				FinalURL:       receipt.FinalURL,
				CheckedAt:      receipt.CheckedAt.UTC(),
				FetchedAt:      receipt.FetchedAt.UTC(),
				ContentType:    receipt.ContentType,
				RawSHA256:      receipt.RawSHA256,
				SemanticSHA256: receipt.SemanticSHA256,
			}
			if receipt.Release != nil {
				item.Release = &ReleaseProvenance{
					Tag:             receipt.Release.Tag,
					PublishedAt:     receipt.Release.PublishedAt.UTC(),
					URL:             receipt.Release.URL,
					Prerelease:      receipt.Release.Prerelease,
					Draft:           receipt.Release.Draft,
					TargetCommitish: receipt.Release.TargetCommitish,
					BodySHA256:      receipt.Release.BodySHA256,
				}
			}
		} else {
			summary.Missing++
		}
		upstreams = append(upstreams, item)
		byID[item.ID] = item
	}
	return upstreams, byID, summary, nil
}

type resolvedRoot struct {
	id   string
	path string
}

func resolveLocalRoots(harnessRoot string, configured []LocalRoot) ([]Root, []resolvedRoot, error) {
	configured = append([]LocalRoot(nil), configured...)
	sort.Slice(configured, func(i, j int) bool { return configured[i].ID < configured[j].ID })
	seenIDs := make(map[string]struct{}, len(configured))
	roots := make([]Root, 0, len(configured))
	resolved := make([]resolvedRoot, 0, len(configured))
	for _, localRoot := range configured {
		if !rootIDPattern.MatchString(localRoot.ID) {
			return nil, nil, fmt.Errorf("%w: invalid local root id %q", ErrInvalidConfig, localRoot.ID)
		}
		if _, duplicate := seenIDs[localRoot.ID]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate local root id %q", ErrInvalidConfig, localRoot.ID)
		}
		seenIDs[localRoot.ID] = struct{}{}
		path, err := resolveExisting(harnessRoot, localRoot.Path, "", true)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve local root %q: %w", localRoot.ID, err)
		}
		for _, previous := range resolved {
			if rootsOverlap(previous.path, path) {
				return nil, nil, fmt.Errorf("%w: local roots %q and %q overlap", ErrInvalidConfig, previous.id, localRoot.ID)
			}
		}
		relative, err := harnessRelative(harnessRoot, path)
		if err != nil {
			return nil, nil, err
		}
		roots = append(roots, Root{ID: localRoot.ID, Path: relative})
		resolved = append(resolved, resolvedRoot{id: localRoot.ID, path: path})
	}
	return roots, resolved, nil
}

func scanDocuments(
	ctx context.Context,
	harnessRoot string,
	roots []resolvedRoot,
	limits Limits,
) ([]Document, map[string]storedDocument, int64, error) {
	documents := make([]Document, 0)
	stored := make(map[string]storedDocument)
	var totalBytes int64
	for _, localRoot := range roots {
		err := filepath.WalkDir(localRoot.path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path != localRoot.path && entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %s", ErrSymlink, filepath.ToSlash(path))
			}
			if entry.IsDir() {
				return nil
			}
			extension := filepath.Ext(entry.Name())
			if !strings.EqualFold(extension, ".md") && !strings.EqualFold(extension, ".markdown") {
				return nil
			}
			if len(documents) >= limits.MaxDocuments {
				return fmt.Errorf("%w: more than %d Markdown documents", ErrLimitExceeded, limits.MaxDocuments)
			}
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if !pathWithin(localRoot.path, real) || !pathWithin(harnessRoot, real) {
				return fmt.Errorf("%w: %s", ErrPathEscape, filepath.ToSlash(path))
			}
			content, info, err := readBoundedFile(real, limits.MaxDocumentBytes)
			if err != nil {
				return err
			}
			if !utf8.Valid(content) || strings.ContainsRune(string(content), '\x00') {
				return fmt.Errorf("%w: Markdown must be UTF-8 text: %s", ErrInvalidConfig, filepath.ToSlash(path))
			}
			totalBytes += int64(len(content))
			if totalBytes > limits.MaxTotalBytes {
				return fmt.Errorf("%w: Markdown content exceeds %d total bytes", ErrLimitExceeded, limits.MaxTotalBytes)
			}
			relative, err := filepath.Rel(localRoot.path, real)
			if err != nil {
				return err
			}
			harnessPath, err := harnessRelative(harnessRoot, real)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			digest := sha256.Sum256(content)
			id := documentID(localRoot.id, relative)
			metadata := Document{
				ID:           id,
				URI:          "research://document/" + strings.TrimPrefix(id, "document:"),
				RootID:       localRoot.id,
				Path:         harnessPath,
				RelativePath: relative,
				Title:        markdownTitle(content, relative),
				SizeBytes:    int64(len(content)),
				SHA256:       hex.EncodeToString(digest[:]),
				ModifiedAt:   info.ModTime().UTC(),
			}
			if _, duplicate := stored[id]; duplicate {
				return fmt.Errorf("%w: duplicate document id %q", ErrInvalidConfig, id)
			}
			documents = append(documents, metadata)
			stored[id] = storedDocument{metadata: metadata, content: content}
			return nil
		})
		if err != nil {
			return nil, nil, 0, fmt.Errorf("scan local root %q: %w", localRoot.id, err)
		}
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
	return documents, stored, totalBytes, nil
}

func documentID(rootID, relative string) string {
	return "document:" + rootID + ":" + base64.RawURLEncoding.EncodeToString([]byte(relative))
}

func upstreamID(feedID string) string { return "upstream:" + feedID }

func markdownTitle(content []byte, relative string) string {
	for _, line := range strings.Split(strings.TrimPrefix(string(content), "\ufeff"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "# ") {
			continue
		}
		title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		if title != "" {
			return truncateUTF8(title, 256)
		}
	}
	base := filepath.Base(filepath.FromSlash(relative))
	return truncateUTF8(strings.TrimSuffix(base, filepath.Ext(base)), 256)
}

func catalogDigest(catalog Catalog) (string, error) {
	catalog = cloneCatalog(catalog)
	catalog.SnapshotID = ""
	catalog.ObservedAt = time.Time{}
	catalog.Summary.Fresh = 0
	catalog.Summary.Stale = 0
	catalog.Summary.RecordFresh = 0
	catalog.Summary.RecordStale = 0
	for i := range catalog.Documents {
		catalog.Documents[i].ModifiedAt = time.Time{}
	}
	for i := range catalog.Upstreams {
		catalog.Upstreams[i].Freshness.Status = ""
	}
	for i := range catalog.Registers {
		catalog.Registers[i].Freshness.Status = ""
	}
	for i := range catalog.Records {
		catalog.Records[i].Freshness.Status = ""
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeLimits(input Limits) (Limits, error) {
	limits := input
	setIntDefault := func(value *int, fallback int) {
		if *value == 0 {
			*value = fallback
		}
	}
	setInt64Default := func(value *int64, fallback int64) {
		if *value == 0 {
			*value = fallback
		}
	}
	setIntDefault(&limits.MaxDocuments, DefaultMaxDocuments)
	setInt64Default(&limits.MaxDocumentBytes, DefaultMaxDocumentBytes)
	setInt64Default(&limits.MaxTotalBytes, DefaultMaxTotalBytes)
	setIntDefault(&limits.DefaultPageSize, DefaultPageSize)
	setIntDefault(&limits.MaxPageSize, DefaultMaxPageSize)
	setIntDefault(&limits.MaxQueryBytes, DefaultMaxQueryBytes)
	setIntDefault(&limits.DefaultReadBytes, DefaultReadBytes)
	setIntDefault(&limits.MaxReadBytes, DefaultMaxReadBytes)
	setIntDefault(&limits.MaxSnippetBytes, DefaultMaxSnippetBytes)
	setIntDefault(&limits.MaxRegisters, DefaultMaxRegisters)
	setIntDefault(&limits.MaxRecords, DefaultMaxRecords)
	setIntDefault(&limits.MaxRecordBytes, DefaultMaxRecordBytes)
	setInt64Default(&limits.MaxRegisterBytes, DefaultMaxRegisterBytes)
	setIntDefault(&limits.MaxResponseBytes, DefaultMaxResponseBytes)
	checks := []struct {
		name       string
		value, max int
	}{
		{"max_documents", limits.MaxDocuments, HardMaxDocuments},
		{"default_page_size", limits.DefaultPageSize, HardMaxPageSize},
		{"max_page_size", limits.MaxPageSize, HardMaxPageSize},
		{"max_query_bytes", limits.MaxQueryBytes, HardMaxQueryBytes},
		{"default_read_bytes", limits.DefaultReadBytes, HardMaxReadBytes},
		{"max_read_bytes", limits.MaxReadBytes, HardMaxReadBytes},
		{"max_snippet_bytes", limits.MaxSnippetBytes, HardMaxSnippetBytes},
		{"max_registers", limits.MaxRegisters, HardMaxRegisters},
		{"max_records", limits.MaxRecords, HardMaxRecords},
		{"max_record_bytes", limits.MaxRecordBytes, HardMaxRecordBytes},
		{"max_response_bytes", limits.MaxResponseBytes, HardMaxResponseBytes},
	}
	for _, check := range checks {
		if check.value <= 0 || check.value > check.max {
			return Limits{}, fmt.Errorf("%w: %s must be between 1 and %d", ErrInvalidConfig, check.name, check.max)
		}
	}
	if limits.MaxDocumentBytes <= 0 || limits.MaxDocumentBytes > HardMaxDocumentBytes {
		return Limits{}, fmt.Errorf("%w: max_document_bytes must be between 1 and %d", ErrInvalidConfig, HardMaxDocumentBytes)
	}
	if limits.MaxTotalBytes <= 0 || limits.MaxTotalBytes > HardMaxTotalBytes {
		return Limits{}, fmt.Errorf("%w: max_total_bytes must be between 1 and %d", ErrInvalidConfig, HardMaxTotalBytes)
	}
	if limits.MaxRegisterBytes <= 0 || limits.MaxRegisterBytes > HardMaxRegisterBytes {
		return Limits{}, fmt.Errorf("%w: max_register_bytes must be between 1 and %d", ErrInvalidConfig, HardMaxRegisterBytes)
	}
	if limits.DefaultPageSize > limits.MaxPageSize {
		return Limits{}, fmt.Errorf("%w: default_page_size exceeds max_page_size", ErrInvalidConfig)
	}
	if limits.DefaultReadBytes > limits.MaxReadBytes {
		return Limits{}, fmt.Errorf("%w: default_read_bytes exceeds max_read_bytes", ErrInvalidConfig)
	}
	return limits, nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

// Catalog returns a defensive copy of the immutable snapshot metadata.
func (g *Gateway) Catalog() Catalog { return cloneCatalog(g.catalog) }

// Limits returns the normalized immutable request and indexing bounds.
func (g *Gateway) Limits() Limits { return g.limits }

// Describe returns metadata for an upstream or local document identifier.
func (g *Gateway) Describe(ctx context.Context, id string) (Detail, error) {
	if err := contextError(ctx); err != nil {
		return Detail{}, err
	}
	if upstream, ok := g.upstreams[id]; ok {
		copy := cloneUpstream(upstream)
		result := Detail{SchemaVersion: SchemaVersion, SnapshotID: g.catalog.SnapshotID, ID: id, Upstream: &copy}
		return result, g.ensureResponseSize(result)
	}
	if document, ok := g.documents[id]; ok {
		copy := document.metadata
		result := Detail{SchemaVersion: SchemaVersion, SnapshotID: g.catalog.SnapshotID, ID: id, Document: &copy}
		return result, g.ensureResponseSize(result)
	}
	if record, ok := g.records[id]; ok {
		copy := cloneRecord(record)
		result := Detail{SchemaVersion: SchemaVersion, SnapshotID: g.catalog.SnapshotID, ID: id, Record: &copy}
		return result, g.ensureResponseSize(result)
	}
	return Detail{}, fmt.Errorf("%w: %q", ErrSourceNotFound, id)
}

func (g *Gateway) ensureResponseSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode response for size check: %w", err)
	}
	if len(encoded) > g.limits.MaxResponseBytes {
		return fmt.Errorf("%w: encoded response exceeds %d bytes", ErrLimitExceeded, g.limits.MaxResponseBytes)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneCatalog(input Catalog) Catalog {
	result := input
	result.Roots = append([]Root(nil), input.Roots...)
	result.Documents = append([]Document(nil), input.Documents...)
	result.Registers = make([]Register, len(input.Registers))
	for i, register := range input.Registers {
		result.Registers[i] = cloneRegister(register)
	}
	result.Records = make([]ResearchRecord, len(input.Records))
	for i, record := range input.Records {
		result.Records[i] = cloneRecord(record)
	}
	result.Upstreams = make([]Upstream, len(input.Upstreams))
	for i, upstream := range input.Upstreams {
		result.Upstreams[i] = cloneUpstream(upstream)
	}
	return result
}

func cloneUpstream(input Upstream) Upstream {
	result := input
	result.Freshness = cloneFreshness(input.Freshness)
	if input.Receipt != nil {
		receipt := *input.Receipt
		result.Receipt = &receipt
	}
	if input.Release != nil {
		release := *input.Release
		result.Release = &release
	}
	return result
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func cloneFreshness(input Freshness) Freshness {
	result := input
	if input.CheckedAt != nil {
		result.CheckedAt = timePointer(*input.CheckedAt)
	}
	if input.NextDueAt != nil {
		result.NextDueAt = timePointer(*input.NextDueAt)
	}
	return result
}
