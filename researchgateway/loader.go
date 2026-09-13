package researchgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var researchSourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// RegisterInput is the already-contained, size-bounded input handed to a
// format loader. Loaders never receive an arbitrary filesystem path to open.
type RegisterInput struct {
	Config       RegisterConfig
	RelativePath string
	SHA256       string
	Data         []byte
	ObservedAt   time.Time
}

// RegisterOutput is the typed result of one register format loader.
type RegisterOutput struct {
	Register Register
	Records  []ResearchRecord
}

// RegisterLoader decodes one register format. Implementations should be pure:
// parse the bounded bytes in input and return typed records without I/O.
type RegisterLoader interface {
	Format() string
	Load(context.Context, RegisterInput) (RegisterOutput, error)
}

// LoaderRegistry is an immutable format-to-loader dispatch table.
type LoaderRegistry struct {
	loaders map[string]RegisterLoader
}

// NewLoaderRegistry validates and freezes a set of modular loaders.
func NewLoaderRegistry(loaders ...RegisterLoader) (*LoaderRegistry, error) {
	registry := &LoaderRegistry{loaders: make(map[string]RegisterLoader, len(loaders))}
	for _, loader := range loaders {
		if loader == nil || !rootIDPattern.MatchString(loader.Format()) {
			return nil, fmt.Errorf("%w: invalid register loader format", ErrInvalidConfig)
		}
		if _, duplicate := registry.loaders[loader.Format()]; duplicate {
			return nil, fmt.Errorf("%w: duplicate register loader %q", ErrInvalidConfig, loader.Format())
		}
		registry.loaders[loader.Format()] = loader
	}
	return registry, nil
}

// With returns a new immutable registry containing the existing and supplied
// loaders. Existing formats cannot be replaced implicitly.
func (registry *LoaderRegistry) With(loaders ...RegisterLoader) (*LoaderRegistry, error) {
	all := make([]RegisterLoader, 0, len(loaders))
	if registry != nil {
		formats := make([]string, 0, len(registry.loaders))
		for format := range registry.loaders {
			formats = append(formats, format)
		}
		sort.Strings(formats)
		for _, format := range formats {
			all = append(all, registry.loaders[format])
		}
	}
	all = append(all, loaders...)
	return NewLoaderRegistry(all...)
}

// DefaultLoaderRegistry returns the built-in typed register formats.
func DefaultLoaderRegistry() *LoaderRegistry {
	registry, err := NewLoaderRegistry(openAISourcesLoader{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *LoaderRegistry) loader(format string) (RegisterLoader, bool) {
	if r == nil {
		return nil, false
	}
	loader, ok := r.loaders[format]
	return loader, ok
}

func loadRegisters(
	ctx context.Context,
	harnessRoot string,
	configured []RegisterConfig,
	loaders *LoaderRegistry,
	observedAt time.Time,
	limits Limits,
) ([]Register, []ResearchRecord, map[string]ResearchRecord, map[string]struct{}, CatalogSummary, error) {
	if configured == nil {
		configured = DefaultRegisterConfigs()
	} else {
		configured = append([]RegisterConfig(nil), configured...)
	}
	sort.Slice(configured, func(i, j int) bool { return configured[i].ID < configured[j].ID })
	seenIDs := make(map[string]struct{}, len(configured))
	seenPaths := make(map[string]string, len(configured))
	registers := make([]Register, 0, len(configured))
	records := make([]ResearchRecord, 0)
	recordMap := make(map[string]ResearchRecord)
	knownRegisters := make(map[string]struct{}, len(configured))
	summary := CatalogSummary{}
	var totalRegisterBytes int64
	if len(configured) > limits.MaxRegisters {
		return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: more than %d registers", ErrLimitExceeded, limits.MaxRegisters)
	}
	for _, configuredRegister := range configured {
		if err := contextError(ctx); err != nil {
			return nil, nil, nil, nil, CatalogSummary{}, err
		}
		if !rootIDPattern.MatchString(configuredRegister.ID) {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: invalid register id %q", ErrInvalidConfig, configuredRegister.ID)
		}
		if _, duplicate := seenIDs[configuredRegister.ID]; duplicate {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: duplicate register id %q", ErrInvalidConfig, configuredRegister.ID)
		}
		seenIDs[configuredRegister.ID] = struct{}{}
		loader, ok := loaders.loader(configuredRegister.Format)
		if !ok {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: unsupported register format %q", ErrInvalidConfig, configuredRegister.Format)
		}
		path, err := resolveExisting(harnessRoot, configuredRegister.Path, "", false)
		if err != nil {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("resolve register %q: %w", configuredRegister.ID, err)
		}
		if previousID, duplicate := seenPaths[path]; duplicate {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: registers %q and %q resolve to the same path", ErrInvalidConfig, previousID, configuredRegister.ID)
		}
		seenPaths[path] = configuredRegister.ID
		relativePath, err := harnessRelative(harnessRoot, path)
		if err != nil {
			return nil, nil, nil, nil, CatalogSummary{}, err
		}
		data, _, err := readBoundedFile(path, maxMetadataBytes)
		if err != nil {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("read register %q: %w", configuredRegister.ID, err)
		}
		totalRegisterBytes += int64(len(data))
		if totalRegisterBytes > limits.MaxRegisterBytes {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: register content exceeds %d total bytes", ErrLimitExceeded, limits.MaxRegisterBytes)
		}
		digest := sha256.Sum256(data)
		input := RegisterInput{
			Config:       configuredRegister,
			RelativePath: relativePath,
			SHA256:       hex.EncodeToString(digest[:]),
			Data:         append([]byte(nil), data...),
			ObservedAt:   observedAt,
		}
		output, err := loader.Load(ctx, input)
		if err != nil {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("load register %q: %w", configuredRegister.ID, err)
		}
		if output.Register.ID != configuredRegister.ID || output.Register.Format != configuredRegister.Format || output.Register.Path != relativePath || output.Register.SHA256 != input.SHA256 {
			return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: loader %q returned inconsistent provenance", ErrInvalidConfig, configuredRegister.Format)
		}
		output.Register.Records = len(output.Records)
		registers = append(registers, cloneRegister(output.Register))
		knownRegisters[output.Register.ID] = struct{}{}
		for _, record := range output.Records {
			if len(records) >= limits.MaxRecords {
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: more than %d research records", ErrLimitExceeded, limits.MaxRecords)
			}
			if record.RegisterID != configuredRegister.ID || record.RegisterPath != relativePath || record.RegisterSHA256 != input.SHA256 {
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: register record %q has inconsistent provenance", ErrInvalidConfig, record.ID)
			}
			if err := validateLoadedRecord(configuredRegister.ID, record); err != nil {
				return nil, nil, nil, nil, CatalogSummary{}, err
			}
			if _, duplicate := recordMap[record.ID]; duplicate {
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: duplicate research record id %q", ErrInvalidConfig, record.ID)
			}
			cloned := cloneRecord(record)
			encoded, encodeErr := json.Marshal(cloned)
			if encodeErr != nil {
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("encode record %q for limit check: %w", record.ID, encodeErr)
			}
			if len(encoded) > limits.MaxRecordBytes {
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: record %q exceeds %d bytes", ErrLimitExceeded, record.ID, limits.MaxRecordBytes)
			}
			records = append(records, cloned)
			recordMap[record.ID] = cloned
			switch record.Freshness.Status {
			case FreshnessFresh:
				summary.RecordFresh++
			case FreshnessStale:
				summary.RecordStale++
			default:
				return nil, nil, nil, nil, CatalogSummary{}, fmt.Errorf("%w: record %q has invalid freshness", ErrInvalidConfig, record.ID)
			}
		}
	}
	sort.Slice(registers, func(i, j int) bool { return registers[i].ID < registers[j].ID })
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	summary.Registers = len(registers)
	summary.Records = len(records)
	return registers, records, recordMap, knownRegisters, summary, nil
}

func validateLoadedRecord(registerID string, record ResearchRecord) error {
	if !researchSourceIDPattern.MatchString(record.SourceID) || record.ID != recordID(registerID, record.SourceID) || record.URI != "research://record/"+registerID+"/"+record.SourceID {
		return fmt.Errorf("%w: register record has invalid namespaced identity", ErrInvalidConfig)
	}
	for name, value := range map[string]string{
		"title": record.Title, "domain": record.Domain, "category": record.Category,
		"volatility": record.Volatility, "refresh_cadence": record.RefreshCadence,
	} {
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%w: register record %q has invalid %s", ErrInvalidConfig, record.ID, name)
		}
	}
	parsed, err := url.ParseRequestURI(record.URL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.RawPath != "" || parsed.Hostname() == "" || parsed.Host != parsed.Hostname() || parsed.Hostname() != record.Domain {
		return fmt.Errorf("%w: register record %q has invalid URL authority", ErrInvalidConfig, record.ID)
	}
	if len(record.Surfaces) == 0 || len(record.VerifiedFacts) == 0 || len(record.WatchFor) == 0 {
		return fmt.Errorf("%w: register record %q has incomplete evidence fields", ErrInvalidConfig, record.ID)
	}
	if record.Freshness.CheckedAt == nil || record.Freshness.NextDueAt == nil || record.Freshness.NextDueAt.Before(*record.Freshness.CheckedAt) {
		return fmt.Errorf("%w: register record %q has invalid freshness evidence", ErrInvalidConfig, record.ID)
	}
	return nil
}

func cloneRegister(input Register) Register {
	result := input
	result.Freshness = cloneFreshness(input.Freshness)
	result.AllowedDomains = append([]string(nil), input.AllowedDomains...)
	result.Cadence = append([]CadencePolicy(nil), input.Cadence...)
	return result
}

func cloneRecord(input ResearchRecord) ResearchRecord {
	result := input
	result.Freshness = cloneFreshness(input.Freshness)
	result.Surfaces = append([]string(nil), input.Surfaces...)
	result.VerifiedFacts = append([]string(nil), input.VerifiedFacts...)
	result.WatchFor = append([]string(nil), input.WatchFor...)
	return result
}
