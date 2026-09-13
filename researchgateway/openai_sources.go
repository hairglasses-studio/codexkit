package researchgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

type openAISourcesLoader struct{}

var officialOpenAIDomains = map[string]struct{}{
	"developers.openai.com": {},
	"learn.chatgpt.com":     {},
	"platform.openai.com":   {},
}

func (openAISourcesLoader) Format() string { return OpenAISourcesFormat }

type openAISourceRegister struct {
	SchemaVersion int `json:"schema_version"`
	Snapshot      struct {
		CapturedAt     string   `json:"captured_at"`
		Scope          string   `json:"scope"`
		AllowedDomains []string `json:"allowed_domains"`
		Policy         string   `json:"policy"`
	} `json:"snapshot"`
	Cadence map[string]string `json:"cadence"`
	Sources []openAISource    `json:"sources"`
}

type openAISource struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	Domain         string   `json:"domain"`
	Category       string   `json:"category"`
	Surfaces       []string `json:"surfaces"`
	Volatility     string   `json:"volatility"`
	RefreshCadence string   `json:"refresh_cadence"`
	VerifiedFacts  []string `json:"verified_facts"`
	WatchFor       []string `json:"watch_for"`
}

var cadenceDurations = map[string]time.Duration{
	"weekly":    7 * 24 * time.Hour,
	"biweekly":  14 * 24 * time.Hour,
	"monthly":   31 * 24 * time.Hour,
	"quarterly": 92 * 24 * time.Hour,
}

var volatilityCadence = map[string]string{
	"critical": "weekly",
	"high":     "biweekly",
	"medium":   "monthly",
	"low":      "quarterly",
}

func (openAISourcesLoader) Load(ctx context.Context, input RegisterInput) (RegisterOutput, error) {
	decoder := json.NewDecoder(bytes.NewReader(input.Data))
	decoder.DisallowUnknownFields()
	var raw openAISourceRegister
	if err := decoder.Decode(&raw); err != nil {
		return RegisterOutput{}, fmt.Errorf("decode %s: %w", OpenAISourcesFormat, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return RegisterOutput{}, fmt.Errorf("multiple JSON values")
		}
		return RegisterOutput{}, err
	}
	if raw.SchemaVersion != 1 {
		return RegisterOutput{}, fmt.Errorf("unsupported schema_version %d", raw.SchemaVersion)
	}
	capturedAt, err := time.Parse("2006-01-02", raw.Snapshot.CapturedAt)
	if err != nil {
		return RegisterOutput{}, fmt.Errorf("snapshot.captured_at must be YYYY-MM-DD: %w", err)
	}
	capturedAt = capturedAt.UTC()
	if err := requiredText("snapshot.scope", raw.Snapshot.Scope); err != nil {
		return RegisterOutput{}, err
	}
	if err := requiredText("snapshot.policy", raw.Snapshot.Policy); err != nil {
		return RegisterOutput{}, err
	}
	allowedDomains, err := normalizedUnique(raw.Snapshot.AllowedDomains, "snapshot.allowed_domains")
	if err != nil || len(allowedDomains) == 0 {
		if err == nil {
			err = fmt.Errorf("snapshot.allowed_domains must not be empty")
		}
		return RegisterOutput{}, err
	}
	for _, domain := range allowedDomains {
		if _, official := officialOpenAIDomains[domain]; !official {
			return RegisterOutput{}, fmt.Errorf("snapshot.allowed_domains contains a non-official OpenAI domain")
		}
	}
	allowedSet := make(map[string]struct{}, len(allowedDomains))
	for _, domain := range allowedDomains {
		if strings.ToLower(domain) != domain || strings.ContainsAny(domain, "/:@?#[]*%") {
			return RegisterOutput{}, fmt.Errorf("invalid allowed domain %q", domain)
		}
		allowedSet[domain] = struct{}{}
	}
	cadence := make([]CadencePolicy, 0, len(raw.Cadence))
	for volatility := range volatilityCadence {
		guidance, ok := raw.Cadence[volatility]
		if !ok || requiredText("cadence."+volatility, guidance) != nil {
			return RegisterOutput{}, fmt.Errorf("cadence.%s is required", volatility)
		}
		cadence = append(cadence, CadencePolicy{Volatility: volatility, Guidance: guidance})
	}
	if len(raw.Cadence) != len(volatilityCadence) {
		return RegisterOutput{}, fmt.Errorf("cadence contains unsupported volatility")
	}
	sort.Slice(cadence, func(i, j int) bool { return cadence[i].Volatility < cadence[j].Volatility })
	if raw.Sources == nil {
		return RegisterOutput{}, fmt.Errorf("sources must be an array")
	}
	if capturedAt.After(input.ObservedAt) {
		return RegisterOutput{}, fmt.Errorf("snapshot.captured_at is after the observation time")
	}

	sources := append([]openAISource(nil), raw.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	seenSourceIDs := make(map[string]struct{}, len(sources))
	records := make([]ResearchRecord, 0, len(sources))
	registerFreshness := Freshness{Status: FreshnessFresh, CheckedAt: timePointer(capturedAt)}
	for _, source := range sources {
		if err := contextError(ctx); err != nil {
			return RegisterOutput{}, err
		}
		if !researchSourceIDPattern.MatchString(source.ID) {
			return RegisterOutput{}, fmt.Errorf("invalid source id %q", source.ID)
		}
		if _, duplicate := seenSourceIDs[source.ID]; duplicate {
			return RegisterOutput{}, fmt.Errorf("duplicate source id %q", source.ID)
		}
		seenSourceIDs[source.ID] = struct{}{}
		for name, value := range map[string]string{
			"title": source.Title, "domain": source.Domain, "category": source.Category,
		} {
			if err := requiredText("sources."+source.ID+"."+name, value); err != nil {
				return RegisterOutput{}, err
			}
		}
		parsedURL, err := url.ParseRequestURI(source.URL)
		if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" || parsedURL.RawQuery != "" || parsedURL.RawPath != "" || parsedURL.Host != parsedURL.Hostname() {
			return RegisterOutput{}, fmt.Errorf("source %q has invalid HTTPS URL", source.ID)
		}
		if parsedURL.Hostname() != source.Domain {
			return RegisterOutput{}, fmt.Errorf("source %q domain does not match URL", source.ID)
		}
		if _, allowed := allowedSet[source.Domain]; !allowed {
			return RegisterOutput{}, fmt.Errorf("source %q domain is not allowlisted", source.ID)
		}
		expectedCadence, ok := volatilityCadence[source.Volatility]
		if !ok || expectedCadence != source.RefreshCadence {
			return RegisterOutput{}, fmt.Errorf("source %q has invalid volatility/cadence pair", source.ID)
		}
		surfaces, err := normalizedUnique(source.Surfaces, "sources."+source.ID+".surfaces")
		if err != nil || len(surfaces) == 0 {
			if err == nil {
				err = fmt.Errorf("source %q surfaces must not be empty", source.ID)
			}
			return RegisterOutput{}, err
		}
		if err := validateTextList(source.VerifiedFacts, "sources."+source.ID+".verified_facts"); err != nil {
			return RegisterOutput{}, err
		}
		if err := validateTextList(source.WatchFor, "sources."+source.ID+".watch_for"); err != nil {
			return RegisterOutput{}, err
		}
		nextDueAt := capturedAt.Add(cadenceDurations[source.RefreshCadence])
		freshness := Freshness{Status: FreshnessFresh, CheckedAt: timePointer(capturedAt), NextDueAt: timePointer(nextDueAt)}
		if !input.ObservedAt.Before(nextDueAt) {
			freshness.Status = FreshnessStale
			registerFreshness.Status = FreshnessStale
		}
		if registerFreshness.NextDueAt == nil || nextDueAt.Before(*registerFreshness.NextDueAt) {
			registerFreshness.NextDueAt = timePointer(nextDueAt)
		}
		records = append(records, ResearchRecord{
			ID:             recordID(input.Config.ID, source.ID),
			URI:            "research://record/" + input.Config.ID + "/" + source.ID,
			RegisterID:     input.Config.ID,
			SourceID:       source.ID,
			Title:          source.Title,
			URL:            source.URL,
			Domain:         source.Domain,
			Category:       source.Category,
			Surfaces:       surfaces,
			Volatility:     source.Volatility,
			RefreshCadence: source.RefreshCadence,
			VerifiedFacts:  append([]string(nil), source.VerifiedFacts...),
			WatchFor:       append([]string(nil), source.WatchFor...),
			CapturedOn:     raw.Snapshot.CapturedAt,
			Freshness:      freshness,
			RegisterPath:   input.RelativePath,
			RegisterSHA256: input.SHA256,
		})
	}
	return RegisterOutput{
		Register: Register{
			ID:             input.Config.ID,
			Format:         input.Config.Format,
			Path:           input.RelativePath,
			SHA256:         input.SHA256,
			CapturedOn:     raw.Snapshot.CapturedAt,
			Scope:          raw.Snapshot.Scope,
			Policy:         raw.Snapshot.Policy,
			AllowedDomains: allowedDomains,
			Cadence:        cadence,
			Freshness:      registerFreshness,
			Records:        len(records),
		},
		Records: records,
	}, nil
}

func recordID(registerID, sourceID string) string { return "record:" + registerID + ":" + sourceID }

func requiredText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", name)
	}
	return nil
}

func normalizedUnique(values []string, name string) ([]string, error) {
	if values == nil {
		return nil, fmt.Errorf("%s must be an array", name)
	}
	result := append([]string(nil), values...)
	for _, value := range result {
		if err := requiredText(name, value); err != nil {
			return nil, err
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, fmt.Errorf("%s contains duplicate %q", name, result[i])
		}
	}
	return result, nil
}

func validateTextList(values []string, name string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must be a non-empty array", name)
	}
	for _, value := range values {
		if err := requiredText(name, value); err != nil {
			return err
		}
	}
	return nil
}
