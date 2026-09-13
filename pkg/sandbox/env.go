package sandbox

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	Agent59ID     = 59
	Agent59Name   = "Agent 59: Clean environment variable sanitizer stripping host tokens"
	RedactedValue = "[REDACTED]"
)

var DefaultSensitivePatterns = []string{
	"KEYS",
	"TOKENS",
	"PASSWORDS",
	"OAUTH",
	"SECRET",
	"KEY",
	"TOKEN",
	"PASSWORD",
	"PASS",
	"SECRETS",
	"AUTH",
	"CREDENTIAL",
	"CREDENTIALS",
	"PRIVATE",
	"BEARER",
	"APIKEY",
	"ACCESS_KEY",
	"SIGNATURE",
	"CERTIFICATE",
	"SESSION",
}

var DefaultAllowedVars = []string{
	"HOME",
	"PATH",
	"TERM",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LC_MESSAGES",
	"USER",
	"LOGNAME",
	"SHELL",
	"TMPDIR",
	"TMP",
	"TEMP",
	"PWD",
	"SHLVL",
	"HOSTNAME",
	"EDITOR",
	"PAGER",
	"TZ",
}

var DefaultTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bghp_[A-Za-z0-9_]{36,}\b`),
	regexp.MustCompile(`(?i)\bgho_[A-Za-z0-9_]{36,}\b`),
	regexp.MustCompile(`(?i)\bghu_[A-Za-z0-9_]{36,}\b`),
	regexp.MustCompile(`(?i)\bghs_[A-Za-z0-9_]{36,}\b`),
	regexp.MustCompile(`(?i)\bghr_[A-Za-z0-9_]{36,}\b`),
	regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{60,}\b`),
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`(?i)\bsk-proj-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`(?i)\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bASIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{30,45}\b`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-\.]{20,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

type Sanitizer struct {
	patterns        []string
	allowed         map[string]struct{}
	explicitAllowed map[string]struct{}
	blocked         map[string]struct{}
	tokenPatterns   []*regexp.Regexp
	strict          bool
	checkValues     bool
}

type EnvSanitizer = Sanitizer

type EnvOption func(*Sanitizer)

type SanitizeReport struct {
	AgentID       int               `json:"agent_id"`
	AgentName     string            `json:"agent_name"`
	TotalVars     int               `json:"total_vars"`
	KeptCount     int               `json:"kept_count"`
	StrippedCount int               `json:"stripped_count"`
	StrippedKeys  []string          `json:"stripped_keys"`
	RedactedEnv   map[string]string `json:"redacted_env,omitempty"`
}

func WithPatterns(patterns ...string) EnvOption {
	return func(s *Sanitizer) {
		s.patterns = append([]string(nil), patterns...)
	}
}

func WithExtraPatterns(patterns ...string) EnvOption {
	return func(s *Sanitizer) {
		s.patterns = append(s.patterns, patterns...)
	}
}

func WithAllowed(vars ...string) EnvOption {
	return func(s *Sanitizer) {
		for _, v := range vars {
			upper := strings.ToUpper(strings.TrimSpace(v))
			s.allowed[upper] = struct{}{}
			s.explicitAllowed[upper] = struct{}{}
		}
	}
}

func WithBlocked(vars ...string) EnvOption {
	return func(s *Sanitizer) {
		for _, v := range vars {
			s.blocked[strings.ToUpper(strings.TrimSpace(v))] = struct{}{}
		}
	}
}

func WithStrict(strict bool) EnvOption {
	return func(s *Sanitizer) {
		s.strict = strict
	}
}

func WithValueChecking(check bool) EnvOption {
	return func(s *Sanitizer) {
		s.checkValues = check
	}
}

func WithTokenPatterns(patterns ...*regexp.Regexp) EnvOption {
	return func(s *Sanitizer) {
		s.tokenPatterns = append([]*regexp.Regexp(nil), patterns...)
	}
}

func NewSanitizer(opts ...EnvOption) *Sanitizer {
	s := &Sanitizer{
		patterns:        append([]string(nil), DefaultSensitivePatterns...),
		allowed:         make(map[string]struct{}),
		explicitAllowed: make(map[string]struct{}),
		blocked:         make(map[string]struct{}),
		tokenPatterns:   append([]*regexp.Regexp(nil), DefaultTokenPatterns...),
		strict:          false,
		checkValues:     true,
	}
	for _, v := range DefaultAllowedVars {
		s.allowed[v] = struct{}{}
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func NewEnvSanitizer(opts ...EnvOption) *EnvSanitizer {
	return NewSanitizer(opts...)
}

func DefaultSanitizer() *Sanitizer {
	return NewSanitizer()
}

func StrictSanitizer() *Sanitizer {
	return NewSanitizer(WithStrict(true))
}

func (s *Sanitizer) IsSensitiveKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	if _, ok := s.blocked[upper]; ok {
		return true
	}
	if _, ok := s.explicitAllowed[upper]; ok {
		return false
	}
	for _, p := range s.patterns {
		if strings.Contains(upper, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

func (s *Sanitizer) IsSensitiveValue(val string) bool {
	if !s.checkValues || val == "" {
		return false
	}
	for _, re := range s.tokenPatterns {
		if re.MatchString(val) {
			return true
		}
	}
	return false
}

func (s *Sanitizer) IsSensitive(key, val string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	if _, ok := s.blocked[upper]; ok {
		return true
	}
	if s.strict {
		if _, ok := s.allowed[upper]; !ok {
			return true
		}
	}
	if s.IsSensitiveKey(upper) {
		return true
	}
	if s.IsSensitiveValue(val) {
		return true
	}
	return false
}

func (s *Sanitizer) SanitizeMap(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if !s.IsSensitive(k, v) {
			out[k] = v
		}
	}
	return out
}

func (s *Sanitizer) SanitizeSlice(env []string) []string {
	if env == nil {
		return nil
	}
	var out []string
	for _, entry := range env {
		k, v := SplitEnv(entry)
		if !s.IsSensitive(k, v) {
			out = append(out, entry)
		}
	}
	return out
}

func (s *Sanitizer) Sanitize(env []string) []string {
	return s.SanitizeSlice(env)
}

func (s *Sanitizer) RedactMap(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if s.IsSensitive(k, v) {
			out[k] = RedactedValue
		} else {
			out[k] = v
		}
	}
	return out
}

func (s *Sanitizer) RedactSlice(env []string) []string {
	if env == nil {
		return nil
	}
	var out []string
	for _, entry := range env {
		k, v := SplitEnv(entry)
		if s.IsSensitive(k, v) {
			out = append(out, fmt.Sprintf("%s=%s", k, RedactedValue))
		} else {
			out = append(out, entry)
		}
	}
	return out
}

func (s *Sanitizer) StrippedKeys(env map[string]string) []string {
	var keys []string
	for k, v := range env {
		if s.IsSensitive(k, v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *Sanitizer) StrippedSliceKeys(env []string) []string {
	var keys []string
	for _, entry := range env {
		k, v := SplitEnv(entry)
		if s.IsSensitive(k, v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *Sanitizer) Audit(env map[string]string) *SanitizeReport {
	stripped := s.StrippedKeys(env)
	redacted := s.RedactMap(env)
	total := len(env)
	strippedCount := len(stripped)
	keptCount := total - strippedCount
	return &SanitizeReport{
		AgentID:       Agent59ID,
		AgentName:     Agent59Name,
		TotalVars:     total,
		KeptCount:     keptCount,
		StrippedCount: strippedCount,
		StrippedKeys:  stripped,
		RedactedEnv:   redacted,
	}
}

func (s *Sanitizer) SanitizeConfig(cfg Config) Config {
	c := cfg
	if c.Env != nil {
		c.Env = s.SanitizeMap(c.Env)
	}
	return c
}

func SplitEnv(entry string) (string, string) {
	parts := strings.SplitN(entry, "=", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func SliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		k, v := SplitEnv(entry)
		m[k] = v
	}
	return m
}

func MapToSlice(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, env[k]))
	}
	return out
}

func SanitizeHostEnv() map[string]string {
	return DefaultSanitizer().SanitizeMap(SliceToMap(os.Environ()))
}

func SanitizeHostSlice() []string {
	return DefaultSanitizer().SanitizeSlice(os.Environ())
}

func SanitizeEnv(env []string) []string {
	return DefaultSanitizer().SanitizeSlice(env)
}

func SanitizeEnvMap(env map[string]string) map[string]string {
	return DefaultSanitizer().SanitizeMap(env)
}

func IsSensitiveEnv(key string) bool {
	return DefaultSanitizer().IsSensitiveKey(key)
}

func FilterEnv(env []string, patterns ...string) []string {
	s := NewSanitizer(WithPatterns(patterns...))
	return s.SanitizeSlice(env)
}

func FilterEnvMap(env map[string]string, patterns ...string) map[string]string {
	s := NewSanitizer(WithPatterns(patterns...))
	return s.SanitizeMap(env)
}

func CleanEnv() map[string]string {
	return map[string]string{
		"HOME":   "/tmp",
		"PATH":   "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM":   "xterm-256color",
		"TMPDIR": "/tmp",
		"LANG":   "C.UTF-8",
	}
}
