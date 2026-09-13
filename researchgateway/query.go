package researchgateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Overview returns compact catalog metadata without embedding document,
// upstream, or record detail collections.
func (g *Gateway) Overview() Overview {
	catalog := g.catalog
	registers := make([]Register, len(catalog.Registers))
	for i, register := range catalog.Registers {
		registers[i] = cloneRegister(register)
	}
	return Overview{
		SchemaVersion: catalog.SchemaVersion,
		SnapshotID:    catalog.SnapshotID,
		ObservedAt:    catalog.ObservedAt,
		RegistryPath:  catalog.RegistryPath,
		ReceiptsPath:  catalog.ReceiptsPath,
		Summary:       catalog.Summary,
		Roots:         append([]Root(nil), catalog.Roots...),
		Registers:     registers,
	}
}

// ListUpstreams returns a stable bounded page of registered upstream metadata.
func (g *Gateway) ListUpstreams(ctx context.Context, request PageRequest) (UpstreamListResponse, error) {
	if err := contextError(ctx); err != nil {
		return UpstreamListResponse{}, err
	}
	limit, err := g.pageLimit(request.Limit)
	if err != nil {
		return UpstreamListResponse{}, err
	}
	fingerprint, err := requestFingerprint(struct{}{})
	if err != nil {
		return UpstreamListResponse{}, err
	}
	offset, err := g.cursorOffset(request.Cursor, "upstreams", fingerprint)
	if err != nil || offset > int64(len(g.catalog.Upstreams)) {
		if err == nil {
			err = ErrInvalidCursor
		}
		return UpstreamListResponse{}, err
	}
	end := min(int(offset)+limit, len(g.catalog.Upstreams))
	items := make([]Upstream, end-int(offset))
	for i, upstream := range g.catalog.Upstreams[int(offset):end] {
		items[i] = cloneUpstream(upstream)
	}
	response := UpstreamListResponse{SchemaVersion: SchemaVersion, SnapshotID: g.catalog.SnapshotID, Total: len(g.catalog.Upstreams), Upstreams: items}
	if end < len(g.catalog.Upstreams) {
		response.NextCursor, err = makeCursor("upstreams", g.catalog.SnapshotID, fingerprint, int64(end))
	}
	if err == nil {
		err = g.ensureResponseSize(response)
	}
	return response, err
}

// ListRecords returns a stable bounded page of typed register records.
func (g *Gateway) ListRecords(ctx context.Context, request RecordListRequest) (RecordListResponse, error) {
	if err := contextError(ctx); err != nil {
		return RecordListResponse{}, err
	}
	registerIDs, err := g.normalizeRegisterFilter(request.RegisterIDs)
	if err != nil {
		return RecordListResponse{}, err
	}
	limit, err := g.pageLimit(request.Limit)
	if err != nil {
		return RecordListResponse{}, err
	}
	fingerprint, err := requestFingerprint(struct {
		RegisterIDs []string `json:"register_ids"`
	}{RegisterIDs: registerIDs})
	if err != nil {
		return RecordListResponse{}, err
	}
	offset, err := g.cursorOffset(request.Cursor, "records", fingerprint)
	if err != nil {
		return RecordListResponse{}, err
	}
	records := make([]ResearchRecord, 0, len(g.catalog.Records))
	for _, record := range g.catalog.Records {
		if err := contextError(ctx); err != nil {
			return RecordListResponse{}, err
		}
		if registerAllowed(record.RegisterID, registerIDs) {
			records = append(records, cloneRecord(record))
		}
	}
	if offset > int64(len(records)) {
		return RecordListResponse{}, ErrInvalidCursor
	}
	end := min(int(offset)+limit, len(records))
	response := RecordListResponse{SchemaVersion: SchemaVersion, SnapshotID: g.catalog.SnapshotID, Total: len(records), Records: append([]ResearchRecord(nil), records[int(offset):end]...)}
	if end < len(records) {
		response.NextCursor, err = makeCursor("records", g.catalog.SnapshotID, fingerprint, int64(end))
	}
	if err == nil {
		err = g.ensureResponseSize(response)
	}
	return response, err
}

// ListDocuments returns a stable page of local document metadata. RootIDs are
// exact configured identifiers; callers cannot supply paths.
func (g *Gateway) ListDocuments(ctx context.Context, request ListRequest) (ListResponse, error) {
	if err := contextError(ctx); err != nil {
		return ListResponse{}, err
	}
	rootIDs, err := g.normalizeRootFilter(request.RootIDs)
	if err != nil {
		return ListResponse{}, err
	}
	limit, err := g.pageLimit(request.Limit)
	if err != nil {
		return ListResponse{}, err
	}
	fingerprint, err := requestFingerprint(struct {
		RootIDs []string `json:"root_ids"`
	}{RootIDs: rootIDs})
	if err != nil {
		return ListResponse{}, err
	}
	offset, err := g.cursorOffset(request.Cursor, "list", fingerprint)
	if err != nil {
		return ListResponse{}, err
	}
	documents := make([]Document, 0, len(g.catalog.Documents))
	for _, document := range g.catalog.Documents {
		if err := contextError(ctx); err != nil {
			return ListResponse{}, err
		}
		if rootAllowed(document.RootID, rootIDs) {
			documents = append(documents, document)
		}
	}
	if offset > int64(len(documents)) {
		return ListResponse{}, ErrInvalidCursor
	}
	end := min(int(offset)+limit, len(documents))
	page := append([]Document(nil), documents[int(offset):end]...)
	response := ListResponse{
		SchemaVersion: SchemaVersion,
		SnapshotID:    g.catalog.SnapshotID,
		Total:         len(documents),
		Documents:     page,
	}
	if end < len(documents) {
		response.NextCursor, err = makeCursor("list", g.catalog.SnapshotID, fingerprint, int64(end))
		if err != nil {
			return ListResponse{}, err
		}
	}
	return response, g.ensureResponseSize(response)
}

// Search performs a case-insensitive full-text substring search across bounded
// local Markdown, typed source records, and body-free upstream provenance.
func (g *Gateway) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if err := contextError(ctx); err != nil {
		return SearchResponse{}, err
	}
	query := strings.TrimSpace(request.Query)
	if query == "" || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || len(query) > g.limits.MaxQueryBytes {
		return SearchResponse{}, fmt.Errorf("%w: query must be 1-%d UTF-8 bytes", ErrInvalidRequest, g.limits.MaxQueryBytes)
	}
	rootIDs, err := g.normalizeRootFilter(request.RootIDs)
	if err != nil {
		return SearchResponse{}, err
	}
	registerIDs, err := g.normalizeRegisterFilter(request.RegisterIDs)
	if err != nil {
		return SearchResponse{}, err
	}
	limit, err := g.pageLimit(request.Limit)
	if err != nil {
		return SearchResponse{}, err
	}
	normalizedQuery := strings.ToLower(query)
	fingerprint, err := requestFingerprint(struct {
		Query       string   `json:"query"`
		RootIDs     []string `json:"root_ids"`
		RegisterIDs []string `json:"register_ids"`
	}{Query: normalizedQuery, RootIDs: rootIDs, RegisterIDs: registerIDs})
	if err != nil {
		return SearchResponse{}, err
	}
	offset, err := g.cursorOffset(request.Cursor, "search", fingerprint)
	if err != nil {
		return SearchResponse{}, err
	}
	hits := make([]SearchHit, 0)
	includeDocuments := len(request.RootIDs) > 0 || len(request.RegisterIDs) == 0
	includeRecords := len(request.RegisterIDs) > 0 || len(request.RootIDs) == 0
	includeUpstreams := len(request.RootIDs) == 0 && len(request.RegisterIDs) == 0
	if includeDocuments {
		for _, metadata := range g.catalog.Documents {
			if err := contextError(ctx); err != nil {
				return SearchResponse{}, err
			}
			if !rootAllowed(metadata.RootID, rootIDs) {
				continue
			}
			stored := g.documents[metadata.ID]
			titleMatches := strings.Count(strings.ToLower(metadata.Title), normalizedQuery)
			pathMatches := strings.Count(strings.ToLower(metadata.RelativePath), normalizedQuery)
			body := string(stored.content)
			bodyMatches := strings.Count(strings.ToLower(body), normalizedQuery)
			matches := titleMatches + pathMatches + bodyMatches
			if matches == 0 {
				continue
			}
			metadataCopy := metadata
			hits = append(hits, SearchHit{
				ID:       metadata.ID,
				Kind:     "document",
				Document: &metadataCopy,
				Score:    titleMatches*100 + pathMatches*20 + bodyMatches,
				Matches:  matches,
				Snippet:  matchingSnippet(body, normalizedQuery, g.limits.MaxSnippetBytes),
			})
		}
	}
	if includeRecords {
		for _, record := range g.catalog.Records {
			if err := contextError(ctx); err != nil {
				return SearchResponse{}, err
			}
			if !registerAllowed(record.RegisterID, registerIDs) {
				continue
			}
			titleMatches := strings.Count(strings.ToLower(record.Title), normalizedQuery)
			metadataText := strings.Join(append(append([]string{record.SourceID, record.URL, record.Domain, record.Category, record.Volatility, record.RefreshCadence}, record.Surfaces...), record.WatchFor...), "\n")
			metadataMatches := strings.Count(strings.ToLower(metadataText), normalizedQuery)
			factsText := strings.Join(record.VerifiedFacts, "\n")
			factMatches := strings.Count(strings.ToLower(factsText), normalizedQuery)
			matches := titleMatches + metadataMatches + factMatches
			if matches == 0 {
				continue
			}
			recordCopy := cloneRecord(record)
			snippet := matchingSnippet(factsText, normalizedQuery, g.limits.MaxSnippetBytes)
			if snippet == "" {
				snippet = matchingSnippet(metadataText, normalizedQuery, g.limits.MaxSnippetBytes)
			}
			hits = append(hits, SearchHit{
				ID:      record.ID,
				Kind:    "record",
				Record:  &recordCopy,
				Score:   titleMatches*100 + factMatches*20 + metadataMatches,
				Matches: matches,
				Snippet: snippet,
			})
		}
	}
	if includeUpstreams {
		for _, upstream := range g.catalog.Upstreams {
			if err := contextError(ctx); err != nil {
				return SearchResponse{}, err
			}
			metadata := strings.Join([]string{upstream.ID, upstream.FeedID, upstream.URL, upstream.Kind, string(upstream.Freshness.Status)}, "\n")
			if upstream.Release != nil {
				metadata += "\n" + upstream.Release.Tag + "\n" + upstream.Release.URL
			}
			matches := strings.Count(strings.ToLower(metadata), normalizedQuery)
			if matches == 0 {
				continue
			}
			copy := cloneUpstream(upstream)
			hits = append(hits, SearchHit{
				ID: upstream.ID, Kind: "upstream", Upstream: &copy,
				Score: matches * 10, Matches: matches,
				Snippet: matchingSnippet(metadata, normalizedQuery, g.limits.MaxSnippetBytes),
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if offset > int64(len(hits)) {
		return SearchResponse{}, ErrInvalidCursor
	}
	end := min(int(offset)+limit, len(hits))
	response := SearchResponse{
		SchemaVersion: SchemaVersion,
		SnapshotID:    g.catalog.SnapshotID,
		Query:         query,
		Total:         len(hits),
		Results:       append([]SearchHit(nil), hits[int(offset):end]...),
	}
	if end < len(hits) {
		response.NextCursor, err = makeCursor("search", g.catalog.SnapshotID, fingerprint, int64(end))
		if err != nil {
			return SearchResponse{}, err
		}
	}
	return response, g.ensureResponseSize(response)
}

// ReadDocument returns one UTF-8-safe byte-bounded chunk. The next cursor is
// bound to the gateway snapshot, document ID, and document digest.
func (g *Gateway) ReadDocument(ctx context.Context, request ReadRequest) (ReadResponse, error) {
	if err := contextError(ctx); err != nil {
		return ReadResponse{}, err
	}
	stored, ok := g.documents[request.DocumentID]
	if !ok {
		return ReadResponse{}, fmt.Errorf("%w: %q", ErrDocumentNotFound, request.DocumentID)
	}
	limit := request.Limit
	if limit == 0 {
		limit = g.limits.DefaultReadBytes
	}
	if limit < utf8.UTFMax || limit > g.limits.MaxReadBytes {
		return ReadResponse{}, fmt.Errorf("%w: read limit must be between %d and %d", ErrInvalidRequest, utf8.UTFMax, g.limits.MaxReadBytes)
	}
	fingerprint, err := requestFingerprint(struct {
		DocumentID string `json:"document_id"`
		SHA256     string `json:"sha256"`
	}{DocumentID: request.DocumentID, SHA256: stored.metadata.SHA256})
	if err != nil {
		return ReadResponse{}, err
	}
	offset, err := g.cursorOffset(request.Cursor, "read", fingerprint)
	if err != nil {
		return ReadResponse{}, err
	}
	if offset > int64(len(stored.content)) || (offset < int64(len(stored.content)) && !utf8.RuneStart(stored.content[offset])) {
		return ReadResponse{}, ErrInvalidCursor
	}
	end := min(int(offset)+limit, len(stored.content))
	if end < len(stored.content) {
		for end > int(offset) && !utf8.RuneStart(stored.content[end]) {
			end--
		}
	}
	if end == int(offset) && end < len(stored.content) {
		return ReadResponse{}, fmt.Errorf("%w: read limit cannot contain next UTF-8 rune", ErrInvalidRequest)
	}
	response := ReadResponse{
		SchemaVersion: SchemaVersion,
		SnapshotID:    g.catalog.SnapshotID,
		Document:      stored.metadata,
		Offset:        offset,
		Bytes:         end - int(offset),
		TotalBytes:    int64(len(stored.content)),
		Content:       string(stored.content[int(offset):end]),
	}
	if end < len(stored.content) {
		response.NextCursor, err = makeCursor("read", g.catalog.SnapshotID, fingerprint, int64(end))
		if err != nil {
			return ReadResponse{}, err
		}
	}
	return response, g.ensureResponseSize(response)
}

func (g *Gateway) cursorOffset(raw, operation, fingerprint string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	offset, err := parseCursor(raw, operation, g.catalog.SnapshotID, fingerprint)
	if err != nil {
		return 0, fmt.Errorf("%w: %s cursor", ErrInvalidCursor, operation)
	}
	return offset, nil
}

func (g *Gateway) pageLimit(requested int) (int, error) {
	if requested == 0 {
		return g.limits.DefaultPageSize, nil
	}
	if requested < 1 || requested > g.limits.MaxPageSize {
		return 0, fmt.Errorf("%w: page limit must be between 1 and %d", ErrInvalidRequest, g.limits.MaxPageSize)
	}
	return requested, nil
}

func (g *Gateway) normalizeRootFilter(input []string) ([]string, error) {
	result := append([]string(nil), input...)
	sort.Strings(result)
	for i, rootID := range result {
		if _, ok := g.knownRootIDs[rootID]; !ok {
			return nil, fmt.Errorf("%w: unknown root_id %q", ErrInvalidRequest, rootID)
		}
		if i > 0 && result[i-1] == rootID {
			return nil, fmt.Errorf("%w: duplicate root_id %q", ErrInvalidRequest, rootID)
		}
	}
	return result, nil
}

func (g *Gateway) normalizeRegisterFilter(input []string) ([]string, error) {
	result := append([]string(nil), input...)
	sort.Strings(result)
	for i, registerID := range result {
		if _, ok := g.knownRegisterIDs[registerID]; !ok {
			return nil, fmt.Errorf("%w: unknown register_id %q", ErrInvalidRequest, registerID)
		}
		if i > 0 && result[i-1] == registerID {
			return nil, fmt.Errorf("%w: duplicate register_id %q", ErrInvalidRequest, registerID)
		}
	}
	return result, nil
}

func rootAllowed(rootID string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	index := sort.SearchStrings(filter, rootID)
	return index < len(filter) && filter[index] == rootID
}

func registerAllowed(registerID string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	index := sort.SearchStrings(filter, registerID)
	return index < len(filter) && filter[index] == registerID
}

func matchingSnippet(body, normalizedQuery string, maxBytes int) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" && strings.Contains(strings.ToLower(line), normalizedQuery) {
			return truncateUTF8(line, maxBytes)
		}
	}
	return ""
}
