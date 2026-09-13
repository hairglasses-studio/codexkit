package sandbox

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestAgent59_Constants(t *testing.T) {
	if Agent59ID != 59 {
		t.Fatalf("expected Agent59ID 59, got %d", Agent59ID)
	}
	if !strings.Contains(Agent59Name, "Agent 59") {
		t.Fatalf("expected Agent59Name to contain 'Agent 59', got %s", Agent59Name)
	}
}

func TestFilterSensitivePatterns(t *testing.T) {
	requiredPatterns := []string{"KEYS", "TOKENS", "PASSWORDS", "OAUTH", "SECRET"}
	s := DefaultSanitizer()

	for _, pattern := range requiredPatterns {
		found := false
		for _, p := range DefaultSensitivePatterns {
			if strings.EqualFold(p, pattern) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("required pattern %s not found in DefaultSensitivePatterns", pattern)
		}
	}

	testCases := []struct {
		key       string
		sensitive bool
	}{
		{"API_KEYS", true},
		{"api_keys", true},
		{"Api_Keys", true},
		{"HOST_TOKENS", true},
		{"host_tokens", true},
		{"USER_PASSWORDS", true},
		{"user_passwords", true},
		{"OAUTH_CLIENT_ID", true},
		{"oauth_client_secret", true},
		{"OAuth_Token", true},
		{"APP_SECRET", true},
		{"app_secret", true},
		{"SECRET_KEY", true},
		{"API_KEY", true},
		{"GH_TOKEN", true},
		{"DB_PASSWORD", true},
		{"PATH", false},
		{"HOME", false},
		{"TERM", false},
		{"USER", false},
		{"SHELL", false},
		{"LANG", false},
		{"TMPDIR", false},
	}

	for _, tc := range testCases {
		res := s.IsSensitiveKey(tc.key)
		if res != tc.sensitive {
			t.Errorf("IsSensitiveKey(%q) = %v; want %v", tc.key, res, tc.sensitive)
		}
	}
}

func TestSanitizeMap(t *testing.T) {
	s := DefaultSanitizer()
	input := map[string]string{
		"HOME":                  "/tmp",
		"PATH":                  "/usr/bin:/bin",
		"TERM":                  "xterm-256color",
		"USER":                  "agent",
		"API_KEYS":              "secret-keys-list",
		"HOST_TOKENS":           "token-cluster",
		"PASSWORDS_FILE":        "/tmp/pwd",
		"OAUTH_TOKEN":           "gho_xxxx",
		"CLIENT_SECRET":         "supersecret",
		"GITHUB_TOKEN":          "ghp_1234567890",
		"AWS_SECRET_ACCESS_KEY": "secret-val",
	}

	got := s.SanitizeMap(input)

	for _, strippedKey := range []string{
		"API_KEYS",
		"HOST_TOKENS",
		"PASSWORDS_FILE",
		"OAUTH_TOKEN",
		"CLIENT_SECRET",
		"GITHUB_TOKEN",
		"AWS_SECRET_ACCESS_KEY",
	} {
		if _, exists := got[strippedKey]; exists {
			t.Errorf("expected %s to be stripped from map", strippedKey)
		}
	}

	for _, keptKey := range []string{"HOME", "PATH", "TERM", "USER"} {
		if val, exists := got[keptKey]; !exists || val != input[keptKey] {
			t.Errorf("expected %s to be kept with value %q", keptKey, input[keptKey])
		}
	}
}

func TestSanitizeSlice(t *testing.T) {
	s := DefaultSanitizer()
	input := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"API_KEYS=key1,key2",
		"HOST_TOKENS=tok1",
		"HOME=/home/test",
		"PASSWORDS=secretpwd",
		"OAUTH_SCOPE=repo",
		"APP_SECRET=xyz123",
		"TERM=vt100",
	}

	got := s.SanitizeSlice(input)

	expected := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/home/test",
		"TERM=vt100",
	}

	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("SanitizeSlice mismatch.\nGot:  %+v\nWant: %+v", got, expected)
	}

	convenienceGot := s.Sanitize(input)
	if !reflect.DeepEqual(convenienceGot, expected) {
		t.Fatalf("Sanitize mismatch.\nGot:  %+v\nWant: %+v", convenienceGot, expected)
	}
}

func TestHostTokenPatternsInValues(t *testing.T) {
	s := DefaultSanitizer()

	tokenCases := []struct {
		key string
		val string
	}{
		{"MY_CONFIG", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"},
		{"EXTERNAL_SETTING", "gho_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"},
		{"BUILD_ID", "github_pat_11AAAAAAA012345678901234567890123456789012345678901234567890123456789012345678"},
		{"INFERENCE_PARAM", "sk-proj-1234567890abcdef1234567890abcdef"},
		{"MODEL_PARAM", "sk-ant-1234567890abcdef1234567890abcdef"},
		{"STORAGE_BUCKET_ID", "AKIAIOSFODNN7EXAMPLE"},
		{"NOTIFIER_ID", "xoxb-1234567890-1234567890-abcdef"},
		{"MAPS_CONF", "AIzaSyD-1234567890abcdefghijklmnopqrstuvw"},
		{"CUSTOM_HEADER", "Bearer abcdefghijklmnopqrstuvwxyz0123456789"},
		{"CERT_DATA", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0..."},
	}

	for _, tc := range tokenCases {
		if !s.IsSensitiveValue(tc.val) {
			t.Errorf("expected value for %s to match token pattern: %s", tc.key, tc.val)
		}
		if !s.IsSensitive(tc.key, tc.val) {
			t.Errorf("expected IsSensitive(%s, val) to be true", tc.key)
		}

		m := map[string]string{tc.key: tc.val, "SAFE_VAR": "safe_value"}
		sanitized := s.SanitizeMap(m)
		if _, exists := sanitized[tc.key]; exists {
			t.Errorf("expected %s to be stripped due to token in value", tc.key)
		}
		if sanitized["SAFE_VAR"] != "safe_value" {
			t.Errorf("expected SAFE_VAR to be preserved")
		}
	}
}

func TestRedactMapAndSlice(t *testing.T) {
	s := DefaultSanitizer()
	inputMap := map[string]string{
		"SAFE_VAR":     "normal",
		"API_KEY":      "12345",
		"OAUTH_SECRET": "topsecret",
	}

	redactedMap := s.RedactMap(inputMap)
	if redactedMap["SAFE_VAR"] != "normal" {
		t.Errorf("expected SAFE_VAR to remain normal, got %s", redactedMap["SAFE_VAR"])
	}
	if redactedMap["API_KEY"] != RedactedValue {
		t.Errorf("expected API_KEY to be redacted, got %s", redactedMap["API_KEY"])
	}
	if redactedMap["OAUTH_SECRET"] != RedactedValue {
		t.Errorf("expected OAUTH_SECRET to be redacted, got %s", redactedMap["OAUTH_SECRET"])
	}

	inputSlice := []string{"SAFE_VAR=normal", "API_KEY=12345"}
	redactedSlice := s.RedactSlice(inputSlice)
	if len(redactedSlice) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(redactedSlice))
	}
	if redactedSlice[0] != "SAFE_VAR=normal" {
		t.Errorf("expected safe slice entry, got %s", redactedSlice[0])
	}
	if redactedSlice[1] != "API_KEY="+RedactedValue {
		t.Errorf("expected redacted slice entry, got %s", redactedSlice[1])
	}
}

func TestAuditReport(t *testing.T) {
	s := DefaultSanitizer()
	env := map[string]string{
		"PATH":          "/bin",
		"HOME":          "/tmp",
		"API_KEYS":      "k1,k2",
		"USER_PASSWORD": "pass",
	}

	report := s.Audit(env)
	if report.AgentID != 59 {
		t.Errorf("expected AgentID 59, got %d", report.AgentID)
	}
	if report.TotalVars != 4 {
		t.Errorf("expected TotalVars 4, got %d", report.TotalVars)
	}
	if report.StrippedCount != 2 {
		t.Errorf("expected StrippedCount 2, got %d", report.StrippedCount)
	}
	if report.KeptCount != 2 {
		t.Errorf("expected KeptCount 2, got %d", report.KeptCount)
	}

	expectedStripped := []string{"API_KEYS", "USER_PASSWORD"}
	sort.Strings(report.StrippedKeys)
	if !reflect.DeepEqual(report.StrippedKeys, expectedStripped) {
		t.Errorf("StrippedKeys = %v; want %v", report.StrippedKeys, expectedStripped)
	}
}

func TestStrictSanitizer(t *testing.T) {
	strict := StrictSanitizer()
	input := map[string]string{
		"PATH":          "/bin",
		"HOME":          "/tmp",
		"CUSTOM_APP":    "my-app",
		"EXTRA_SETTING": "val",
	}

	res := strict.SanitizeMap(input)
	if _, ok := res["PATH"]; !ok {
		t.Errorf("expected PATH to be allowed in strict mode")
	}
	if _, ok := res["HOME"]; !ok {
		t.Errorf("expected HOME to be allowed in strict mode")
	}
	if _, ok := res["CUSTOM_APP"]; ok {
		t.Errorf("expected CUSTOM_APP to be stripped in strict mode")
	}
	if _, ok := res["EXTRA_SETTING"]; ok {
		t.Errorf("expected EXTRA_SETTING to be stripped in strict mode")
	}
}

func TestCustomOptions(t *testing.T) {
	s := NewSanitizer(
		WithPatterns("CUSTOM_SENSITIVE"),
		WithExtraPatterns("ANOTHER_PATTERN"),
		WithBlocked("FORBIDDEN_VAR"),
		WithAllowed("ALLOWED_CUSTOM"),
		WithValueChecking(false),
	)

	if !s.IsSensitiveKey("MY_CUSTOM_SENSITIVE_VAR") {
		t.Errorf("expected custom pattern match")
	}
	if !s.IsSensitiveKey("ANOTHER_PATTERN_TEST") {
		t.Errorf("expected extra pattern match")
	}
	if !s.IsSensitiveKey("FORBIDDEN_VAR") {
		t.Errorf("expected blocked var match")
	}
	if s.IsSensitiveKey("ALLOWED_CUSTOM") {
		t.Errorf("expected explicitly allowed var to not be sensitive")
	}
	if s.IsSensitiveValue("ghp_123456789012345678901234567890123456") {
		t.Errorf("expected value checking to be disabled")
	}
}

func TestSanitizeConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Env["API_KEY"] = "secret123"
	cfg.Env["OAUTH_TOKEN"] = "token123"

	s := DefaultSanitizer()
	cleanedCfg := s.SanitizeConfig(cfg)

	if _, ok := cleanedCfg.Env["API_KEY"]; ok {
		t.Errorf("expected API_KEY to be stripped from cleaned config")
	}
	if _, ok := cleanedCfg.Env["OAUTH_TOKEN"]; ok {
		t.Errorf("expected OAUTH_TOKEN to be stripped from cleaned config")
	}
	if cleanedCfg.Env["HOME"] != "/tmp" {
		t.Errorf("expected HOME to be preserved in cleaned config")
	}
}

func TestHelpers(t *testing.T) {
	k, v := SplitEnv("FOO=BAR=BAZ")
	if k != "FOO" || v != "BAR=BAZ" {
		t.Errorf("SplitEnv failed: %s, %s", k, v)
	}

	k2, v2 := SplitEnv("SINGLE")
	if k2 != "SINGLE" || v2 != "" {
		t.Errorf("SplitEnv single failed: %s, %s", k2, v2)
	}

	slice := []string{"B=2", "A=1"}
	m := SliceToMap(slice)
	if m["A"] != "1" || m["B"] != "2" {
		t.Errorf("SliceToMap failed: %+v", m)
	}

	sOut := MapToSlice(m)
	if len(sOut) != 2 || sOut[0] != "A=1" || sOut[1] != "B=2" {
		t.Errorf("MapToSlice failed: %+v", sOut)
	}

	clean := CleanEnv()
	if clean["HOME"] != "/tmp" || clean["PATH"] == "" {
		t.Errorf("CleanEnv invalid: %+v", clean)
	}

	filteredSlice := FilterEnv([]string{"PATH=/bin", "MY_TOKEN=xyz"}, "TOKEN")
	if len(filteredSlice) != 1 || filteredSlice[0] != "PATH=/bin" {
		t.Errorf("FilterEnv failed: %+v", filteredSlice)
	}

	filteredMap := FilterEnvMap(map[string]string{"PATH": "/bin", "MY_TOKEN": "xyz"}, "TOKEN")
	if len(filteredMap) != 1 || filteredMap["PATH"] != "/bin" {
		t.Errorf("FilterEnvMap failed: %+v", filteredMap)
	}

	if !IsSensitiveEnv("SUPER_SECRET") {
		t.Errorf("IsSensitiveEnv failed for SUPER_SECRET")
	}

	os.Setenv("TEST_SENSITIVE_KEYS_VAR", "test-key")
	defer os.Unsetenv("TEST_SENSITIVE_KEYS_VAR")
	hostEnv := SanitizeHostEnv()
	if _, ok := hostEnv["TEST_SENSITIVE_KEYS_VAR"]; ok {
		t.Errorf("expected TEST_SENSITIVE_KEYS_VAR to be stripped by SanitizeHostEnv")
	}

	hostSlice := SanitizeHostSlice()
	for _, entry := range hostSlice {
		k, _ := SplitEnv(entry)
		if k == "TEST_SENSITIVE_KEYS_VAR" {
			t.Errorf("expected TEST_SENSITIVE_KEYS_VAR to be stripped by SanitizeHostSlice")
		}
	}
}
