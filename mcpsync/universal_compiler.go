package mcpsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type UniversalProvider string

const (
	ProviderClaude UniversalProvider = "claude"
	ProviderCodex  UniversalProvider = "codex"
	ProviderAGY    UniversalProvider = "agy"
	ProviderCline  UniversalProvider = "cline"
	ProviderCursor UniversalProvider = "cursor"
)

type SecretHandlingMode string

const (
	SecretModeReject SecretHandlingMode = "reject"
	SecretModeScrub  SecretHandlingMode = "scrub"
)

type UniversalSyncMode string

const (
	UniversalModeWrite  UniversalSyncMode = "write"
	UniversalModeDryRun UniversalSyncMode = "dry-run"
	UniversalModeCheck  UniversalSyncMode = "check"
)

const (
	UniversalStartMarker = "# BEGIN GENERATED MCP SERVERS: codexkit-universal-compiler"
	UniversalEndMarker   = "# END GENERATED MCP SERVERS: codexkit-universal-compiler"
)

var (
	ErrUniversalSurfaceDrift = errors.New("universal surface drift detected")
	ErrSecretViolation       = errors.New("raw secret or credential detected")
	ErrOptoinvestRefused     = errors.New("optoinvest or employer path refused")
	ErrTrustAnchorRefused    = errors.New("protected user trust-anchor path refused")
)

var (
	aliasSanitizeRe  = regexp.MustCompile(`[^a-z0-9_-]+`)
	aliasCollapseRe  = regexp.MustCompile(`-{2,}`)
	underCollapseRe  = regexp.MustCompile(`_{2,}`)
	envVarRefRe      = regexp.MustCompile(`^\$\{?[A-Za-z_][A-Za-z0-9_]*\}?$`)
	pemBlockRe       = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	jwtTokenRe       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	ghTokenRe        = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`)
	skTokenRe        = regexp.MustCompile(`\bsk-(?:proj-|live-|test-)?[A-Za-z0-9_-]{16,}\b`)
	awsKeyRe         = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	googleKeyRe      = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)
	slackTokenRe     = regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)
	authHeaderCredRe = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+\S+`)
)

var secretKeyTokens = []string{
	"API_KEY",
	"APIKEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"PRIVATE_KEY",
	"ACCESS_KEY",
	"CLIENT_SECRET",
	"SIGNING_KEY",
	"AUTHORIZATION",
	"BEARER",
	"OAUTH",
	"COOKIE",
	"SESSION_ID",
	"WEBHOOK_SECRET",
	"PAT",
}

var optoinvestMarkers = []string{
	"optoinvest",
	"opto-invest",
	"opto_invest",
}

type UniversalServerSpec struct {
	Name              string            `json:"name"`
	CanonicalAlias    string            `json:"canonical_alias,omitempty"`
	Aliases           map[string]string `json:"aliases,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	CWD               string            `json:"cwd,omitempty"`
	Transport         string            `json:"transport,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	Disabled          bool              `json:"disabled,omitempty"`
	StartupTimeoutSec *int              `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec    *int              `json:"tool_timeout_sec,omitempty"`
	EnabledTools      []string          `json:"enabled_tools,omitempty"`
	DisabledTools     []string          `json:"disabled_tools,omitempty"`
	AutoApprove       []string          `json:"auto_approve,omitempty"`
	Description       string            `json:"description,omitempty"`
}

type UniversalSkillAST struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	AllowedTools  []string          `json:"allowed_tools,omitempty"`
	Globs         []string          `json:"globs,omitempty"`
	Paths         []string          `json:"paths,omitempty"`
	AlwaysApply   bool              `json:"always_apply,omitempty"`
	UserInvocable *bool             `json:"user_invocable,omitempty"`
	Triggers      []string          `json:"triggers,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	License       string            `json:"license,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Extra         map[string]any    `json:"extra,omitempty"`
	Body          string            `json:"body"`
	SourcePath    string            `json:"source_path,omitempty"`
}

type UniversalManifest struct {
	RepoRoot string                `json:"repo_root,omitempty"`
	Servers  []UniversalServerSpec `json:"servers"`
	Skills   []UniversalSkillAST   `json:"skills"`
	AliasMap map[string]string     `json:"alias_map,omitempty"`
}

type SecretFinding struct {
	Scope   string `json:"scope"`
	Subject string `json:"subject"`
	Field   string `json:"field"`
	Key     string `json:"key,omitempty"`
	Reason  string `json:"reason"`
	Action  string `json:"action"`
}

type UniversalCompileOptions struct {
	Providers       []UniversalProvider `json:"providers,omitempty"`
	SecretMode      SecretHandlingMode  `json:"secret_mode,omitempty"`
	StripPrefixes   []string            `json:"strip_prefixes,omitempty"`
	CodexSnakeCase  bool                `json:"codex_snake_case,omitempty"`
	IncludeComments bool                `json:"include_comments,omitempty"`
}

type CompiledArtifact struct {
	Provider UniversalProvider `json:"provider"`
	Kind     string            `json:"kind"`
	RelPath  string            `json:"rel_path"`
	Content  []byte            `json:"-"`
	SHA256   string            `json:"sha256"`
	Size     int               `json:"size"`
}

type UniversalCompileResult struct {
	Providers         []UniversalProvider `json:"providers"`
	NormalizedAliases map[string]string   `json:"normalized_aliases"`
	ProviderAliases   map[string]string   `json:"provider_aliases"`
	SecretFindings    []SecretFinding     `json:"secret_findings,omitempty"`
	Artifacts         []CompiledArtifact  `json:"artifacts"`
	Digest            string              `json:"digest"`
}

type UniversalSyncOptions struct {
	Manifest   *UniversalManifest      `json:"-"`
	RepoRoot   string                  `json:"repo_root,omitempty"`
	OutputRoot string                  `json:"output_root,omitempty"`
	Mode       UniversalSyncMode       `json:"mode,omitempty"`
	Compile    UniversalCompileOptions `json:"compile,omitempty"`
}

type UniversalArtifactStatus struct {
	Provider       UniversalProvider `json:"provider"`
	Kind           string            `json:"kind"`
	RelPath        string            `json:"rel_path"`
	TargetPath     string            `json:"target_path"`
	Status         string            `json:"status"`
	ExpectedSHA256 string            `json:"expected_sha256"`
	ActualSHA256   string            `json:"actual_sha256,omitempty"`
	Size           int               `json:"size"`
}

type UniversalSyncReport struct {
	RepoRoot          string                    `json:"repo_root"`
	OutputRoot        string                    `json:"output_root"`
	Mode              UniversalSyncMode         `json:"mode"`
	Passed            bool                      `json:"passed"`
	Drifted           bool                      `json:"drifted"`
	NormalizedAliases map[string]string         `json:"normalized_aliases"`
	SecretFindings    []SecretFinding           `json:"secret_findings,omitempty"`
	Artifacts         []UniversalArtifactStatus `json:"artifacts"`
	Digest            string                    `json:"digest"`
	Errors            []string                  `json:"errors,omitempty"`
}

func AllUniversalProviders() []UniversalProvider {
	return []UniversalProvider{
		ProviderClaude,
		ProviderCodex,
		ProviderAGY,
		ProviderCline,
		ProviderCursor,
	}
}

func ValidateSafePath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if isOptoinvestText(trimmed) {
		return fmt.Errorf("%w: %s", ErrOptoinvestRefused, trimmed)
	}
	cleaned := filepath.Clean(trimmed)
	if isOptoinvestText(cleaned) {
		return fmt.Errorf("%w: %s", ErrOptoinvestRefused, cleaned)
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		if isOptoinvestText(abs) {
			return fmt.Errorf("%w: %s", ErrOptoinvestRefused, abs)
		}
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil && isOptoinvestText(resolved) {
			return fmt.Errorf("%w: %s", ErrOptoinvestRefused, resolved)
		}
	}
	return nil
}

func validateTargetOutputPath(outputRoot, targetPath string) error {
	if err := ValidateSafePath(targetPath); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(filepath.Clean(outputRoot))
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(filepath.Clean(targetPath))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target path escapes output root: %s", targetPath)
	}
	slash := filepath.ToSlash(absTarget)
	if strings.HasSuffix(slash, "/.gemini/settings.json") ||
		strings.HasSuffix(slash, "/.claude/settings.json") ||
		strings.HasPrefix(slash, "/etc/sudoers") {
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			cleanHome := filepath.ToSlash(filepath.Clean(home))
			if strings.HasPrefix(slash, cleanHome+"/") || strings.HasPrefix(slash, "/etc/sudoers") {
				return fmt.Errorf("%w: %s", ErrTrustAnchorRefused, targetPath)
			}
		} else if strings.HasPrefix(slash, "/etc/sudoers") {
			return fmt.Errorf("%w: %s", ErrTrustAnchorRefused, targetPath)
		}
	}
	return nil
}

func isOptoinvestText(value string) bool {
	lower := strings.ToLower(filepath.ToSlash(value))
	for _, marker := range optoinvestMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func NormalizeServerAlias(raw string, stripPrefixes []string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	for _, prefix := range stripPrefixes {
		p := strings.ToLower(strings.TrimSpace(prefix))
		if p != "" && strings.HasPrefix(s, p) && len(s) > len(p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	for _, prefix := range []string{"studio_", "studio-"} {
		if strings.HasPrefix(s, prefix) && len(s) > len(prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	s = strings.ReplaceAll(s, "_", "-")
	s = aliasSanitizeRe.ReplaceAllString(s, "-")
	s = aliasCollapseRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if s == "" {
		return "mcp"
	}
	return s
}

func formatProviderServerAlias(canonical string, provider UniversalProvider, codexSnake bool) string {
	if provider == ProviderCodex && codexSnake {
		out := strings.ReplaceAll(canonical, "-", "_")
		out = underCollapseRe.ReplaceAllString(out, "_")
		out = strings.Trim(out, "_-")
		if out == "" {
			return "mcp"
		}
		return out
	}
	return canonical
}

func LoadUniversalManifest(repoRoot string) (*UniversalManifest, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, fmt.Errorf("repo_root is required")
	}
	if err := ValidateSafePath(repoRoot); err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(filepath.Clean(repoRoot))
	if err != nil {
		return nil, err
	}
	if err := ValidateSafePath(absRoot); err != nil {
		return nil, err
	}
	manifest := &UniversalManifest{
		RepoRoot: absRoot,
		Servers:  []UniversalServerSpec{},
		Skills:   []UniversalSkillAST{},
	}

	mcpPath := filepath.Join(absRoot, ".mcp.json")
	if data, err := os.ReadFile(mcpPath); err == nil {
		var raw struct {
			MCPServers map[string]struct {
				Command           string            `json:"command,omitempty"`
				Args              []string          `json:"args,omitempty"`
				Env               map[string]string `json:"env,omitempty"`
				CWD               string            `json:"cwd,omitempty"`
				Transport         string            `json:"transport,omitempty"`
				URL               string            `json:"url,omitempty"`
				Headers           map[string]string `json:"headers,omitempty"`
				Disabled          *bool             `json:"disabled,omitempty"`
				StartupTimeoutSec *int              `json:"startup_timeout_sec,omitempty"`
				ToolTimeoutSec    *int              `json:"tool_timeout_sec,omitempty"`
				AutoApprove       []string          `json:"autoApprove,omitempty"`
			} `json:"mcpServers"`
		}
		if uerr := json.Unmarshal(data, &raw); uerr != nil {
			return nil, fmt.Errorf("parse %s: %w", mcpPath, uerr)
		}
		names := make([]string, 0, len(raw.MCPServers))
		for name := range raw.MCPServers {
			if strings.HasPrefix(name, "_") {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			srv := raw.MCPServers[name]
			disabled := false
			if srv.Disabled != nil {
				disabled = *srv.Disabled
			}
			manifest.Servers = append(manifest.Servers, UniversalServerSpec{
				Name:              name,
				Command:           srv.Command,
				Args:              append([]string(nil), srv.Args...),
				Env:               cloneStringMap(srv.Env),
				CWD:               srv.CWD,
				Transport:         srv.Transport,
				URL:               srv.URL,
				Headers:           cloneStringMap(srv.Headers),
				Disabled:          disabled,
				StartupTimeoutSec: srv.StartupTimeoutSec,
				ToolTimeoutSec:    srv.ToolTimeoutSec,
				AutoApprove:       append([]string(nil), srv.AutoApprove...),
			})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", mcpPath, err)
	}

	skillsDir := filepath.Join(absRoot, ".agents", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
			if err := ValidateSafePath(skillPath); err != nil {
				return nil, err
			}
			rawSkill, rerr := os.ReadFile(skillPath)
			if errors.Is(rerr, os.ErrNotExist) {
				continue
			}
			if rerr != nil {
				return nil, fmt.Errorf("read %s: %w", skillPath, rerr)
			}
			ast, perr := ParseSkillAST(entry.Name(), skillPath, rawSkill)
			if perr != nil {
				return nil, perr
			}
			manifest.Skills = append(manifest.Skills, ast)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", skillsDir, err)
	}

	return manifest, nil
}

func ParseSkillAST(fallbackName, sourcePath string, raw []byte) (UniversalSkillAST, error) {
	if err := ValidateSafePath(sourcePath); err != nil {
		return UniversalSkillAST{}, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	ast := UniversalSkillAST{
		Name:       NormalizeServerAlias(fallbackName, nil),
		SourcePath: sourcePath,
		Metadata:   map[string]string{},
		Extra:      map[string]any{},
	}
	if !strings.HasPrefix(text, "---\n") {
		ast.Body = strings.TrimSpace(text)
		return ast, nil
	}
	rest := strings.TrimPrefix(text, "---\n")
	idx := strings.Index(rest, "\n---\n")
	var fmText string
	if idx >= 0 {
		fmText = rest[:idx]
		ast.Body = strings.TrimSpace(rest[idx+len("\n---\n"):])
	} else if strings.HasSuffix(rest, "\n---") {
		fmText = strings.TrimSuffix(rest, "\n---")
		ast.Body = ""
	} else {
		return UniversalSkillAST{}, fmt.Errorf("unterminated YAML frontmatter in %s", sourcePath)
	}

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return UniversalSkillAST{}, fmt.Errorf("invalid YAML frontmatter in %s: %w", sourcePath, err)
	}
	if fm == nil {
		fm = map[string]any{}
	}
	if v, ok := fm["name"].(string); ok && strings.TrimSpace(v) != "" {
		ast.Name = NormalizeServerAlias(v, nil)
	}
	if v, ok := fm["description"].(string); ok {
		ast.Description = strings.TrimSpace(v)
	}
	if v, ok := fm["compatibility"].(string); ok {
		ast.Compatibility = strings.TrimSpace(v)
	}
	if v, ok := fm["license"].(string); ok {
		ast.License = strings.TrimSpace(v)
	}
	if v, ok := fm["alwaysApply"].(bool); ok {
		ast.AlwaysApply = v
	}
	if v, ok := fm["user-invocable"].(bool); ok {
		b := v
		ast.UserInvocable = &b
	}
	ast.AllowedTools = stringSliceFromAny(fm["allowed-tools"])
	ast.Globs = stringSliceFromAny(fm["globs"])
	ast.Paths = stringSliceFromAny(fm["paths"])
	ast.Triggers = stringSliceFromAny(fm["triggers"])
	if metaMap, ok := fm["metadata"].(map[string]any); ok {
		for k, v := range metaMap {
			ast.Metadata[k] = fmt.Sprint(v)
		}
	}
	known := map[string]bool{
		"name":           true,
		"description":    true,
		"allowed-tools":  true,
		"globs":          true,
		"paths":          true,
		"alwaysApply":    true,
		"user-invocable": true,
		"triggers":       true,
		"compatibility":  true,
		"license":        true,
		"metadata":       true,
	}
	for k, v := range fm {
		if !known[k] {
			ast.Extra[k] = v
		}
	}
	if len(ast.Metadata) == 0 {
		ast.Metadata = nil
	}
	if len(ast.Extra) == 0 {
		ast.Extra = nil
	}
	return ast, nil
}

func stringSliceFromAny(v any) []string {
	switch items := v.(type) {
	case nil:
		return nil
	case string:
		trimmed := strings.TrimSpace(items)
		if trimmed == "" {
			return nil
		}
		parts := strings.Split(trimmed, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(items))
		for _, s := range items {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func CompileUniversalSurface(manifest UniversalManifest, opts UniversalCompileOptions) (*UniversalCompileResult, error) {
	if err := ValidateSafePath(manifest.RepoRoot); err != nil {
		return nil, err
	}
	providers, err := resolveUniversalProviders(opts.Providers)
	if err != nil {
		return nil, err
	}
	secretMode := opts.SecretMode
	if secretMode == "" {
		secretMode = SecretModeReject
	}
	if secretMode != SecretModeReject && secretMode != SecretModeScrub {
		return nil, fmt.Errorf("invalid secret handling mode: %s", secretMode)
	}

	servers, normalizedAliases, secretFindings, err := prepareServers(manifest, opts, secretMode)
	if err != nil {
		return nil, err
	}
	skills, skillFindings, err := prepareSkills(manifest.Skills, secretMode)
	if err != nil {
		return nil, err
	}
	secretFindings = append(secretFindings, skillFindings...)

	if secretMode == SecretModeReject && len(secretFindings) > 0 {
		first := secretFindings[0]
		return nil, fmt.Errorf("%w: %s %s.%s (%s)", ErrSecretViolation, first.Scope, first.Subject, first.Key, first.Reason)
	}

	providerAliases := make(map[string]string)
	artifacts := make([]CompiledArtifact, 0, len(providers)*(1+len(skills)))

	for _, provider := range providers {
		for _, srv := range servers {
			pAlias := providerServerAlias(srv, provider, opts.CodexSnakeCase)
			providerAliases[string(provider)+":"+srv.Name] = pAlias
		}
		mcpArtifacts, err := compileProviderMCP(provider, servers, opts)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, mcpArtifacts...)

		skillArtifacts, err := compileProviderSkills(provider, skills)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, skillArtifacts...)
	}

	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Provider != artifacts[j].Provider {
			return artifacts[i].Provider < artifacts[j].Provider
		}
		return artifacts[i].RelPath < artifacts[j].RelPath
	})

	overall := sha256.New()
	for i := range artifacts {
		artifacts[i].SHA256 = hashBytesSHA256(artifacts[i].Content)
		artifacts[i].Size = len(artifacts[i].Content)
		fmt.Fprintf(overall, "%s\x00%s\x00%s\n", artifacts[i].Provider, artifacts[i].RelPath, artifacts[i].SHA256)
	}

	return &UniversalCompileResult{
		Providers:         providers,
		NormalizedAliases: normalizedAliases,
		ProviderAliases:   providerAliases,
		SecretFindings:    secretFindings,
		Artifacts:         artifacts,
		Digest:            hex.EncodeToString(overall.Sum(nil)),
	}, nil
}

func resolveUniversalProviders(requested []UniversalProvider) ([]UniversalProvider, error) {
	if len(requested) == 0 {
		return AllUniversalProviders(), nil
	}
	seen := make(map[UniversalProvider]bool, len(requested))
	out := make([]UniversalProvider, 0, len(requested))
	for _, p := range requested {
		switch p {
		case ProviderClaude, ProviderCodex, ProviderAGY, ProviderCline, ProviderCursor:
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		default:
			return nil, fmt.Errorf("unsupported provider: %s", p)
		}
	}
	return out, nil
}

func prepareServers(manifest UniversalManifest, opts UniversalCompileOptions, secretMode SecretHandlingMode) ([]UniversalServerSpec, map[string]string, []SecretFinding, error) {
	sorted := make([]UniversalServerSpec, len(manifest.Servers))
	copy(sorted, manifest.Servers)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	used := make(map[string]string, len(sorted))
	aliasMap := make(map[string]string, len(sorted))
	out := make([]UniversalServerSpec, 0, len(sorted))
	findings := make([]SecretFinding, 0)

	for _, srv := range sorted {
		if strings.TrimSpace(srv.Name) == "" {
			return nil, nil, nil, fmt.Errorf("server name must not be empty")
		}
		if err := ValidateSafePath(srv.Command); err != nil {
			return nil, nil, nil, err
		}
		if err := ValidateSafePath(srv.CWD); err != nil {
			return nil, nil, nil, err
		}
		for _, arg := range srv.Args {
			if err := ValidateSafePath(arg); err != nil {
				return nil, nil, nil, err
			}
		}

		canonical := srv.CanonicalAlias
		explicit := canonical != ""
		if !explicit && manifest.AliasMap != nil {
			if mapped, ok := manifest.AliasMap[srv.Name]; ok && strings.TrimSpace(mapped) != "" {
				canonical = mapped
				explicit = true
			}
		}
		if canonical == "" {
			canonical = NormalizeServerAlias(srv.Name, opts.StripPrefixes)
		} else {
			canonical = NormalizeServerAlias(canonical, nil)
		}

		if prev, exists := used[canonical]; exists {
			if explicit {
				return nil, nil, nil, fmt.Errorf("explicit canonical alias collision %q between %q and %q", canonical, prev, srv.Name)
			}
			suffix := 2
			for {
				candidate := fmt.Sprintf("%s-%d", canonical, suffix)
				if _, taken := used[candidate]; !taken {
					canonical = candidate
					break
				}
				suffix++
			}
		}
		used[canonical] = srv.Name
		aliasMap[srv.Name] = canonical

		if strings.TrimSpace(srv.Command) == "" && strings.TrimSpace(srv.URL) == "" {
			return nil, nil, nil, fmt.Errorf("server %q must define either command or url", srv.Name)
		}

		cleanedEnv, envFindings := sanitizeKeyValueMap("server", srv.Name, "env", srv.Env, secretMode)
		findings = append(findings, envFindings...)
		cleanedHeaders, headerFindings := sanitizeKeyValueMap("server", srv.Name, "headers", srv.Headers, secretMode)
		findings = append(findings, headerFindings...)

		cleanedURL, urlFindings := sanitizeURLCredentials(srv.Name, srv.URL, secretMode)
		findings = append(findings, urlFindings...)

		cleanedArgs, argFindings := sanitizeArgs(srv.Name, srv.Args, secretMode)
		findings = append(findings, argFindings...)

		srv.CanonicalAlias = canonical
		srv.Env = cleanedEnv
		srv.Headers = cleanedHeaders
		srv.URL = cleanedURL
		srv.Args = cleanedArgs
		srv.Aliases = cloneStringMap(srv.Aliases)
		srv.EnabledTools = append([]string(nil), srv.EnabledTools...)
		srv.DisabledTools = append([]string(nil), srv.DisabledTools...)
		srv.AutoApprove = append([]string(nil), srv.AutoApprove...)
		slices.Sort(srv.AutoApprove)
		out = append(out, srv)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CanonicalAlias < out[j].CanonicalAlias
	})
	return out, aliasMap, findings, nil
}

func prepareSkills(skills []UniversalSkillAST, secretMode SecretHandlingMode) ([]UniversalSkillAST, []SecretFinding, error) {
	sorted := make([]UniversalSkillAST, len(skills))
	copy(sorted, skills)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	seen := make(map[string]bool, len(sorted))
	out := make([]UniversalSkillAST, 0, len(sorted))
	findings := make([]SecretFinding, 0)

	for _, skill := range sorted {
		if err := ValidateSafePath(skill.SourcePath); err != nil {
			return nil, nil, err
		}
		norm := NormalizeServerAlias(skill.Name, nil)
		if norm == "" {
			return nil, nil, fmt.Errorf("skill name resolved to empty")
		}
		if seen[norm] {
			return nil, nil, fmt.Errorf("duplicate normalized skill name: %s", norm)
		}
		seen[norm] = true
		skill.Name = norm

		for _, p := range skill.Paths {
			if err := ValidateSafePath(p); err != nil {
				return nil, nil, err
			}
		}
		for _, g := range skill.Globs {
			if err := ValidateSafePath(g); err != nil {
				return nil, nil, err
			}
		}

		cleanMeta, metaFindings := sanitizeKeyValueMap("skill", skill.Name, "metadata", skill.Metadata, secretMode)
		findings = append(findings, metaFindings...)
		skill.Metadata = cleanMeta

		if reason, hit := detectSecretValue(skill.Body); hit {
			action := "rejected"
			if secretMode == SecretModeScrub {
				skill.Body = scrubSecretPatterns(skill.Body)
				action = "scrubbed"
			}
			findings = append(findings, SecretFinding{
				Scope:   "skill",
				Subject: skill.Name,
				Field:   "body",
				Reason:  reason,
				Action:  action,
			})
		}

		skill.AllowedTools = append([]string(nil), skill.AllowedTools...)
		skill.Globs = append([]string(nil), skill.Globs...)
		skill.Paths = append([]string(nil), skill.Paths...)
		skill.Triggers = append([]string(nil), skill.Triggers...)
		out = append(out, skill)
	}
	return out, findings, nil
}

func sanitizeKeyValueMap(scope, subject, field string, values map[string]string, mode SecretHandlingMode) (map[string]string, []SecretFinding) {
	if len(values) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	cleaned := make(map[string]string, len(values))
	findings := make([]SecretFinding, 0)

	for _, k := range keys {
		val := values[k]
		if err := ValidateSafePath(val); err != nil {
			findings = append(findings, SecretFinding{
				Scope:   scope,
				Subject: subject,
				Field:   field,
				Key:     k,
				Reason:  err.Error(),
				Action:  "rejected",
			})
			continue
		}
		if isSafePlaceholder(val) {
			cleaned[k] = val
			continue
		}
		if reason, hit := detectSecretValue(val); hit {
			if mode == SecretModeScrub {
				cleaned[k] = placeholderForKey(k)
			}
			findings = append(findings, SecretFinding{
				Scope:   scope,
				Subject: subject,
				Field:   field,
				Key:     k,
				Reason:  reason,
				Action:  actionName(mode),
			})
			continue
		}
		if isSecretKeyName(k) && strings.TrimSpace(val) != "" {
			if mode == SecretModeScrub {
				cleaned[k] = placeholderForKey(k)
			}
			findings = append(findings, SecretFinding{
				Scope:   scope,
				Subject: subject,
				Field:   field,
				Key:     k,
				Reason:  "raw secret literal in sensitive key",
				Action:  actionName(mode),
			})
			continue
		}
		cleaned[k] = val
	}
	return cleaned, findings
}

func sanitizeURLCredentials(subject, rawURL string, mode SecretHandlingMode) (string, []SecretFinding) {
	if strings.TrimSpace(rawURL) == "" {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}
	if parsed.User != nil {
		if _, hasPass := parsed.User.Password(); hasPass || parsed.User.Username() != "" {
			action := actionName(mode)
			outURL := rawURL
			if mode == SecretModeScrub {
				parsed.User = nil
				outURL = parsed.String()
			}
			return outURL, []SecretFinding{{
				Scope:   "server",
				Subject: subject,
				Field:   "url",
				Reason:  "embedded URL userinfo credentials",
				Action:  action,
			}}
		}
	}
	return rawURL, nil
}

func sanitizeArgs(subject string, args []string, mode SecretHandlingMode) ([]string, []SecretFinding) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]string, len(args))
	copy(out, args)
	var findings []SecretFinding
	for i, arg := range out {
		if isSafePlaceholder(arg) {
			continue
		}
		if reason, hit := detectSecretValue(arg); hit {
			if mode == SecretModeScrub {
				out[i] = "[REDACTED]"
			}
			findings = append(findings, SecretFinding{
				Scope:   "server",
				Subject: subject,
				Field:   "args",
				Key:     fmt.Sprintf("[%d]", i),
				Reason:  reason,
				Action:  actionName(mode),
			})
		}
	}
	return out, findings
}

func actionName(mode SecretHandlingMode) string {
	if mode == SecretModeScrub {
		return "scrubbed"
	}
	return "rejected"
}

func isSafePlaceholder(val string) bool {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return true
	}
	if envVarRefRe.MatchString(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "sops://") ||
		strings.HasPrefix(lower, "hg-secret://") ||
		strings.HasPrefix(lower, "age://")
}

func isSecretKeyName(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	for _, token := range secretKeyTokens {
		if strings.Contains(upper, token) {
			return true
		}
	}
	return false
}

func detectSecretValue(val string) (string, bool) {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" || isSafePlaceholder(trimmed) {
		return "", false
	}
	if pemBlockRe.MatchString(trimmed) {
		return "private key material", true
	}
	if jwtTokenRe.MatchString(trimmed) {
		return "JWT token literal", true
	}
	if ghTokenRe.MatchString(trimmed) {
		return "GitHub token literal", true
	}
	if skTokenRe.MatchString(trimmed) {
		return "prefixed API key literal", true
	}
	if awsKeyRe.MatchString(trimmed) {
		return "AWS access key literal", true
	}
	if googleKeyRe.MatchString(trimmed) {
		return "Google API key literal", true
	}
	if slackTokenRe.MatchString(trimmed) {
		return "Slack token literal", true
	}
	if authHeaderCredRe.MatchString(trimmed) && !strings.Contains(trimmed, "$") {
		return "raw authorization header credential", true
	}
	return "", false
}

func scrubSecretPatterns(body string) string {
	out := pemBlockRe.ReplaceAllString(body, "[REDACTED_PRIVATE_KEY]")
	out = jwtTokenRe.ReplaceAllString(out, "[REDACTED_JWT]")
	out = ghTokenRe.ReplaceAllString(out, "[REDACTED_TOKEN]")
	out = skTokenRe.ReplaceAllString(out, "[REDACTED_API_KEY]")
	out = awsKeyRe.ReplaceAllString(out, "[REDACTED_AWS_KEY]")
	out = googleKeyRe.ReplaceAllString(out, "[REDACTED_GOOGLE_KEY]")
	out = slackTokenRe.ReplaceAllString(out, "[REDACTED_SLACK_TOKEN]")
	return out
}

func placeholderForKey(key string) string {
	upper := strings.ToUpper(strings.TrimSpace(key))
	upper = regexp.MustCompile(`[^A-Z0-9_]+`).ReplaceAllString(upper, "_")
	upper = strings.Trim(upper, "_")
	if upper == "" {
		return "[REDACTED]"
	}
	return "${" + upper + "}"
}

func providerServerAlias(srv UniversalServerSpec, provider UniversalProvider, codexSnake bool) string {
	if srv.Aliases != nil {
		if override, ok := srv.Aliases[string(provider)]; ok && strings.TrimSpace(override) != "" {
			return formatProviderServerAlias(NormalizeServerAlias(override, nil), provider, codexSnake)
		}
	}
	return formatProviderServerAlias(srv.CanonicalAlias, provider, codexSnake)
}

func compileProviderMCP(provider UniversalProvider, servers []UniversalServerSpec, opts UniversalCompileOptions) ([]CompiledArtifact, error) {
	switch provider {
	case ProviderClaude:
		data, err := renderClaudeJSON(servers)
		if err != nil {
			return nil, err
		}
		return []CompiledArtifact{{
			Provider: ProviderClaude,
			Kind:     "mcp_config",
			RelPath:  ".mcp.json",
			Content:  data,
		}}, nil
	case ProviderCodex:
		data, err := renderCodexTOML(servers, opts)
		if err != nil {
			return nil, err
		}
		return []CompiledArtifact{{
			Provider: ProviderCodex,
			Kind:     "mcp_config",
			RelPath:  filepath.ToSlash(filepath.Join(".codex", "config.toml")),
			Content:  data,
		}}, nil
	case ProviderAGY:
		data, err := renderAGYJSON(servers)
		if err != nil {
			return nil, err
		}
		return []CompiledArtifact{{
			Provider: ProviderAGY,
			Kind:     "mcp_config",
			RelPath:  filepath.ToSlash(filepath.Join(".gemini", "antigravity", "mcp_config.json")),
			Content:  data,
		}}, nil
	case ProviderCline:
		data, err := renderClineJSON(servers)
		if err != nil {
			return nil, err
		}
		return []CompiledArtifact{{
			Provider: ProviderCline,
			Kind:     "mcp_config",
			RelPath:  filepath.ToSlash(filepath.Join(".cline", "cline_mcp_settings.json")),
			Content:  data,
		}}, nil
	case ProviderCursor:
		data, err := renderCursorJSON(servers)
		if err != nil {
			return nil, err
		}
		return []CompiledArtifact{{
			Provider: ProviderCursor,
			Kind:     "mcp_config",
			RelPath:  filepath.ToSlash(filepath.Join(".cursor", "mcp.json")),
			Content:  data,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

func renderClaudeJSON(servers []UniversalServerSpec) ([]byte, error) {
	mcpServers := make(map[string]map[string]any, len(servers))
	for _, srv := range servers {
		alias := providerServerAlias(srv, ProviderClaude, false)
		entry := map[string]any{}
		if srv.URL != "" {
			entry["type"] = remoteServerType(srv.Transport)
			entry["url"] = srv.URL
		} else {
			entry["command"] = srv.Command
			if len(srv.Args) > 0 {
				entry["args"] = append([]string(nil), srv.Args...)
			}
			if srv.CWD != "" {
				entry["cwd"] = srv.CWD
			}
		}
		if len(srv.Env) > 0 {
			entry["env"] = cloneStringMap(srv.Env)
		}
		if len(srv.Headers) > 0 {
			entry["headers"] = cloneStringMap(srv.Headers)
		}
		if srv.Disabled {
			entry["disabled"] = true
		}
		mcpServers[alias] = entry
	}
	payload := map[string]any{"mcpServers": mcpServers}
	data, err := marshalJSONFile(payload)
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate claude json: %w", err)
	}
	return data, nil
}

func renderCodexTOML(servers []UniversalServerSpec, opts UniversalCompileOptions) ([]byte, error) {
	var b strings.Builder
	b.WriteString(UniversalStartMarker + "\n")
	for i, srv := range servers {
		if i > 0 {
			b.WriteString("\n")
		}
		alias := providerServerAlias(srv, ProviderCodex, opts.CodexSnakeCase)
		if opts.IncludeComments && srv.Description != "" {
			fmt.Fprintf(&b, "# %s\n", strings.ReplaceAll(srv.Description, "\n", " "))
		}
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", alias)
		renderStringValue(&b, "default_tools_approval_mode", defaultToolsApprovalMode)
		if srv.Command != "" {
			renderStringValue(&b, "command", srv.Command)
		}
		if len(srv.Args) > 0 {
			renderStringArray(&b, "args", srv.Args)
		}
		if srv.CWD != "" && srv.Command != "" {
			renderStringValue(&b, "cwd", srv.CWD)
		}
		if srv.URL != "" {
			renderStringValue(&b, "url", srv.URL)
		}
		if srv.Transport != "" && srv.Transport != "stdio" {
			renderStringValue(&b, "transport", srv.Transport)
		}
		enabled := !srv.Disabled
		fmt.Fprintf(&b, "enabled = %t\n", enabled)
		if srv.StartupTimeoutSec != nil {
			fmt.Fprintf(&b, "startup_timeout_sec = %d\n", *srv.StartupTimeoutSec)
		}
		if srv.ToolTimeoutSec != nil {
			fmt.Fprintf(&b, "tool_timeout_sec = %d\n", *srv.ToolTimeoutSec)
		}
		if len(srv.EnabledTools) > 0 {
			renderStringArray(&b, "enabled_tools", srv.EnabledTools)
		}
		if len(srv.DisabledTools) > 0 {
			renderStringArray(&b, "disabled_tools", srv.DisabledTools)
		}
		if len(srv.Env) > 0 {
			renderStringMapSection(&b, fmt.Sprintf("mcp_servers.%s.env", alias), srv.Env)
		}
		if len(srv.Headers) > 0 {
			renderStringMapSection(&b, fmt.Sprintf("mcp_servers.%s.headers", alias), srv.Headers)
		}
	}
	b.WriteString(UniversalEndMarker + "\n")
	out := []byte(b.String())
	var parsed map[string]any
	if err := toml.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("validate codex toml: %w", err)
	}
	return out, nil
}

func renderAGYJSON(servers []UniversalServerSpec) ([]byte, error) {
	mcpServers := make(map[string]map[string]any, len(servers))
	for _, srv := range servers {
		alias := providerServerAlias(srv, ProviderAGY, false)
		entry := map[string]any{
			"disabled": srv.Disabled,
		}
		if srv.URL != "" {
			entry["serverUrl"] = srv.URL
		} else {
			entry["command"] = srv.Command
			if len(srv.Args) > 0 {
				entry["args"] = append([]string(nil), srv.Args...)
			}
			if srv.CWD != "" {
				entry["cwd"] = srv.CWD
			}
		}
		if len(srv.Env) > 0 {
			entry["env"] = cloneStringMap(srv.Env)
		}
		if len(srv.Headers) > 0 {
			entry["headers"] = cloneStringMap(srv.Headers)
		}
		mcpServers[alias] = entry
	}
	payload := map[string]any{"mcpServers": mcpServers}
	data, err := marshalJSONFile(payload)
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate agy json: %w", err)
	}
	return data, nil
}

func renderClineJSON(servers []UniversalServerSpec) ([]byte, error) {
	mcpServers := make(map[string]map[string]any, len(servers))
	for _, srv := range servers {
		alias := providerServerAlias(srv, ProviderCline, false)
		entry := map[string]any{
			"disabled": srv.Disabled,
		}
		if srv.URL != "" {
			entry["url"] = srv.URL
			entry["type"] = remoteServerType(srv.Transport)
		} else {
			entry["command"] = srv.Command
			if len(srv.Args) > 0 {
				entry["args"] = append([]string(nil), srv.Args...)
			}
			if srv.CWD != "" {
				entry["cwd"] = srv.CWD
			}
		}
		if len(srv.Env) > 0 {
			entry["env"] = cloneStringMap(srv.Env)
		}
		if len(srv.Headers) > 0 {
			entry["headers"] = cloneStringMap(srv.Headers)
		}
		if len(srv.AutoApprove) > 0 {
			entry["autoApprove"] = append([]string(nil), srv.AutoApprove...)
		} else {
			entry["autoApprove"] = []string{}
		}
		if srv.ToolTimeoutSec != nil {
			entry["timeout"] = *srv.ToolTimeoutSec
		}
		mcpServers[alias] = entry
	}
	payload := map[string]any{"mcpServers": mcpServers}
	data, err := marshalJSONFile(payload)
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate cline json: %w", err)
	}
	return data, nil
}

func renderCursorJSON(servers []UniversalServerSpec) ([]byte, error) {
	mcpServers := make(map[string]map[string]any, len(servers))
	for _, srv := range servers {
		alias := providerServerAlias(srv, ProviderCursor, false)
		entry := map[string]any{
			"disabled": srv.Disabled,
		}
		if srv.URL != "" {
			entry["url"] = srv.URL
		} else {
			entry["command"] = srv.Command
			if len(srv.Args) > 0 {
				entry["args"] = append([]string(nil), srv.Args...)
			}
			if srv.CWD != "" {
				entry["cwd"] = srv.CWD
			}
		}
		if len(srv.Env) > 0 {
			entry["env"] = cloneStringMap(srv.Env)
		}
		if len(srv.Headers) > 0 {
			entry["headers"] = cloneStringMap(srv.Headers)
		}
		mcpServers[alias] = entry
	}
	payload := map[string]any{"mcpServers": mcpServers}
	data, err := marshalJSONFile(payload)
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate cursor json: %w", err)
	}
	return data, nil
}

func compileProviderSkills(provider UniversalProvider, skills []UniversalSkillAST) ([]CompiledArtifact, error) {
	artifacts := make([]CompiledArtifact, 0, len(skills))
	for _, skill := range skills {
		var relPath string
		var content []byte
		var err error
		switch provider {
		case ProviderClaude:
			relPath = filepath.ToSlash(filepath.Join(".claude", "skills", skill.Name, "SKILL.md"))
			content, err = renderClaudeSkill(skill)
		case ProviderCodex:
			relPath = filepath.ToSlash(filepath.Join(".codex", "skills", skill.Name, "SKILL.md"))
			content, err = renderCodexSkill(skill)
		case ProviderAGY:
			relPath = filepath.ToSlash(filepath.Join(".gemini", "skills", skill.Name, "SKILL.md"))
			content, err = renderAGYSkill(skill)
		case ProviderCline:
			relPath = filepath.ToSlash(filepath.Join(".clinerules", skill.Name+".md"))
			content, err = renderClineRule(skill)
		case ProviderCursor:
			relPath = filepath.ToSlash(filepath.Join(".cursor", "rules", skill.Name+".mdc"))
			content, err = renderCursorMDC(skill)
		default:
			return nil, fmt.Errorf("unsupported provider: %s", provider)
		}
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, CompiledArtifact{
			Provider: provider,
			Kind:     "skill_surface",
			RelPath:  relPath,
			Content:  content,
		})
	}
	return artifacts, nil
}

func renderClaudeSkill(skill UniversalSkillAST) ([]byte, error) {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLStringField(&b, "name", skill.Name)
	writeYAMLStringField(&b, "description", skill.Description)
	if len(skill.AllowedTools) > 0 {
		writeYAMLStringList(&b, "allowed-tools", skill.AllowedTools)
	}
	if len(skill.Paths) > 0 {
		writeYAMLStringList(&b, "paths", skill.Paths)
	}
	if skill.UserInvocable != nil {
		fmt.Fprintf(&b, "user-invocable: %t\n", *skill.UserInvocable)
	}
	if skill.Compatibility != "" {
		writeYAMLStringField(&b, "compatibility", skill.Compatibility)
	}
	if skill.License != "" {
		writeYAMLStringField(&b, "license", skill.License)
	}
	if len(skill.Metadata) > 0 {
		writeYAMLStringMap(&b, "metadata", skill.Metadata)
	}
	b.WriteString("---\n")
	appendSkillBody(&b, skill.Body)
	return []byte(b.String()), nil
}

func renderCodexSkill(skill UniversalSkillAST) ([]byte, error) {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLStringField(&b, "name", skill.Name)
	writeYAMLStringField(&b, "description", skill.Description)
	if skill.Compatibility != "" {
		writeYAMLStringField(&b, "compatibility", skill.Compatibility)
	}
	if skill.License != "" {
		writeYAMLStringField(&b, "license", skill.License)
	}
	if len(skill.Metadata) > 0 {
		writeYAMLStringMap(&b, "metadata", skill.Metadata)
	}
	b.WriteString("---\n")
	appendSkillBody(&b, skill.Body)
	return []byte(b.String()), nil
}

func renderAGYSkill(skill UniversalSkillAST) ([]byte, error) {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLStringField(&b, "name", skill.Name)
	writeYAMLStringField(&b, "description", skill.Description)
	if len(skill.Triggers) > 0 {
		writeYAMLStringList(&b, "triggers", skill.Triggers)
	}
	if skill.Compatibility != "" {
		writeYAMLStringField(&b, "compatibility", skill.Compatibility)
	}
	if skill.License != "" {
		writeYAMLStringField(&b, "license", skill.License)
	}
	if len(skill.Metadata) > 0 {
		writeYAMLStringMap(&b, "metadata", skill.Metadata)
	}
	b.WriteString("---\n")
	appendSkillBody(&b, skill.Body)
	return []byte(b.String()), nil
}

func renderClineRule(skill UniversalSkillAST) ([]byte, error) {
	var b strings.Builder
	paths := skill.Paths
	if len(paths) == 0 {
		paths = skill.Globs
	}
	if len(paths) > 0 {
		b.WriteString("---\n")
		writeYAMLStringList(&b, "paths", paths)
		b.WriteString("---\n\n")
	}
	if skill.Description != "" {
		fmt.Fprintf(&b, "# %s\n\n%s\n", skill.Name, skill.Description)
		if skill.Body != "" {
			b.WriteString("\n" + skill.Body + "\n")
		}
		return []byte(b.String()), nil
	}
	if skill.Body != "" {
		b.WriteString(skill.Body + "\n")
	}
	return []byte(b.String()), nil
}

func renderCursorMDC(skill UniversalSkillAST) ([]byte, error) {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLStringField(&b, "description", skill.Description)
	globs := skill.Globs
	if len(globs) == 0 {
		globs = skill.Paths
	}
	if len(globs) > 0 {
		writeYAMLStringList(&b, "globs", globs)
	}
	fmt.Fprintf(&b, "alwaysApply: %t\n", skill.AlwaysApply)
	b.WriteString("---\n")
	appendSkillBody(&b, skill.Body)
	out := []byte(b.String())

	rest := strings.TrimPrefix(string(out), "---\n")
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		return nil, fmt.Errorf("invalid cursor mdc frontmatter for %s", skill.Name)
	}
	var check map[string]any
	if err := yaml.Unmarshal([]byte(rest[:endIdx]), &check); err != nil {
		return nil, fmt.Errorf("validate cursor mdc yaml for %s: %w", skill.Name, err)
	}
	return out, nil
}

func writeYAMLStringField(b *strings.Builder, key, value string) {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		fmt.Fprintf(b, "%s: %q\n", key, value)
		return
	}
	trimmed := strings.TrimRight(string(encoded), "\n")
	fmt.Fprintf(b, "%s: %s\n", key, trimmed)
}

func writeYAMLStringList(b *strings.Builder, key string, values []string) {
	fmt.Fprintf(b, "%s:\n", key)
	for _, v := range values {
		encoded, err := yaml.Marshal(v)
		if err != nil {
			fmt.Fprintf(b, "  - %q\n", v)
			continue
		}
		fmt.Fprintf(b, "  - %s\n", strings.TrimRight(string(encoded), "\n"))
	}
}

func writeYAMLStringMap(b *strings.Builder, key string, values map[string]string) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "%s:\n", key)
	for _, k := range keys {
		encoded, err := yaml.Marshal(values[k])
		if err != nil {
			fmt.Fprintf(b, "  %s: %q\n", k, values[k])
			continue
		}
		fmt.Fprintf(b, "  %s: %s\n", k, strings.TrimRight(string(encoded), "\n"))
	}
}

func appendSkillBody(b *strings.Builder, body string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return
	}
	b.WriteString("\n")
	b.WriteString(trimmed)
	b.WriteString("\n")
}

func SyncUniversalSurface(opts UniversalSyncOptions) (*UniversalSyncReport, error) {
	mode := opts.Mode
	if mode == "" {
		mode = UniversalModeCheck
	}
	switch mode {
	case UniversalModeWrite, UniversalModeDryRun, UniversalModeCheck:
	default:
		return nil, fmt.Errorf("unsupported universal sync mode: %s", mode)
	}

	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" && opts.Manifest != nil {
		repoRoot = opts.Manifest.RepoRoot
	}
	if repoRoot != "" {
		if err := ValidateSafePath(repoRoot); err != nil {
			return nil, err
		}
	}

	outputRoot := strings.TrimSpace(opts.OutputRoot)
	if outputRoot == "" {
		outputRoot = repoRoot
	}
	if outputRoot == "" {
		return nil, fmt.Errorf("output_root or repo_root is required")
	}
	if err := ValidateSafePath(outputRoot); err != nil {
		return nil, err
	}
	absOutputRoot, err := filepath.Abs(filepath.Clean(outputRoot))
	if err != nil {
		return nil, err
	}
	if err := ValidateSafePath(absOutputRoot); err != nil {
		return nil, err
	}

	var manifest *UniversalManifest
	if opts.Manifest != nil {
		manifest = opts.Manifest
		if manifest.RepoRoot != "" {
			if err := ValidateSafePath(manifest.RepoRoot); err != nil {
				return nil, err
			}
		}
	} else {
		loaded, lerr := LoadUniversalManifest(repoRoot)
		if lerr != nil {
			return nil, lerr
		}
		manifest = loaded
	}

	compiled, err := CompileUniversalSurface(*manifest, opts.Compile)
	if err != nil {
		return nil, err
	}

	report := &UniversalSyncReport{
		RepoRoot:          repoRoot,
		OutputRoot:        absOutputRoot,
		Mode:              mode,
		Passed:            true,
		Drifted:           false,
		NormalizedAliases: compiled.NormalizedAliases,
		SecretFindings:    compiled.SecretFindings,
		Artifacts:         make([]UniversalArtifactStatus, 0, len(compiled.Artifacts)),
		Digest:            compiled.Digest,
	}

	for _, art := range compiled.Artifacts {
		targetPath := filepath.Join(absOutputRoot, filepath.FromSlash(art.RelPath))
		if err := validateTargetOutputPath(absOutputRoot, targetPath); err != nil {
			return nil, err
		}
		status := UniversalArtifactStatus{
			Provider:       art.Provider,
			Kind:           art.Kind,
			RelPath:        art.RelPath,
			TargetPath:     targetPath,
			ExpectedSHA256: art.SHA256,
			Size:           art.Size,
		}
		actual, rerr := os.ReadFile(targetPath)
		switch {
		case errors.Is(rerr, os.ErrNotExist):
			status.Status = "missing"
		case rerr != nil:
			status.Status = "error"
			report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", targetPath, rerr))
		default:
			status.ActualSHA256 = hashBytesSHA256(actual)
			if bytes.Equal(actual, art.Content) {
				status.Status = "in_sync"
			} else {
				status.Status = "drifted"
			}
		}

		if status.Status != "in_sync" {
			switch mode {
			case UniversalModeWrite:
				if werr := writeUniversalArtifact(targetPath, art.Content); werr != nil {
					status.Status = "error"
					report.Errors = append(report.Errors, fmt.Sprintf("write %s: %v", targetPath, werr))
					report.Passed = false
				} else {
					status.Status = "written"
					status.ActualSHA256 = art.SHA256
				}
			case UniversalModeDryRun:
				report.Drifted = true
			case UniversalModeCheck:
				report.Drifted = true
				report.Passed = false
			}
		}
		report.Artifacts = append(report.Artifacts, status)
	}

	if len(report.Errors) > 0 {
		report.Passed = false
		return report, errors.New(strings.Join(report.Errors, "; "))
	}
	if mode == UniversalModeCheck && report.Drifted {
		report.Passed = false
		return report, fmt.Errorf("%w: %s", ErrUniversalSurfaceDrift, summarizeDrift(report.Artifacts))
	}
	return report, nil
}

func writeUniversalArtifact(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func summarizeDrift(statuses []UniversalArtifactStatus) string {
	var items []string
	for _, s := range statuses {
		if s.Status != "in_sync" {
			items = append(items, fmt.Sprintf("%s:%s(%s)", s.Provider, s.RelPath, s.Status))
		}
	}
	return strings.Join(items, ", ")
}

func hashBytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func RunUniversalCompilerCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("universal-compiler", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoRoot := fs.String("repo", "", "")
	outRoot := fs.String("out", "", "")
	checkOnly := fs.Bool("check", false, "")
	dryRun := fs.Bool("dry-run", false, "")
	scrubSecrets := fs.Bool("scrub-secrets", false, "")
	codexSnake := fs.Bool("codex-snake-case", false, "")
	providersCSV := fs.String("providers", "", "")
	jsonOut := fs.Bool("json", false, "")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	mode := UniversalModeWrite
	if *checkOnly {
		mode = UniversalModeCheck
	} else if *dryRun {
		mode = UniversalModeDryRun
	}

	secretMode := SecretModeReject
	if *scrubSecrets {
		secretMode = SecretModeScrub
	}

	var providers []UniversalProvider
	if strings.TrimSpace(*providersCSV) != "" {
		for _, part := range strings.Split(*providersCSV, ",") {
			p := UniversalProvider(strings.ToLower(strings.TrimSpace(part)))
			if p != "" {
				providers = append(providers, p)
			}
		}
	}

	report, err := SyncUniversalSurface(UniversalSyncOptions{
		RepoRoot:   *repoRoot,
		OutputRoot: *outRoot,
		Mode:       mode,
		Compile: UniversalCompileOptions{
			Providers:      providers,
			SecretMode:     secretMode,
			CodexSnakeCase: *codexSnake,
		},
	})
	if *jsonOut && report != nil {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}
	if err != nil {
		fmt.Fprintf(stderr, "universal-compiler: %v\n", err)
		return 1
	}
	if !*jsonOut {
		fmt.Fprintf(stdout, "universal-compiler: %s passed=%t artifacts=%d digest=%s\n", report.Mode, report.Passed, len(report.Artifacts), report.Digest)
	}
	return 0
}
