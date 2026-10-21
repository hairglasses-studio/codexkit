package baselineguard

// Two Codex-CLI drift checks, kept in their own file to minimize merge
// conflicts with other lanes touching guard.go's shared check registry:
//
//   - codex_version_pin: documented codex-cli version pins (AGENTS.md,
//     README.md, docs/CODEX_COMPATIBILITY.md) must agree with each other
//     and with a live `codex --version` probe.
//   - codex_policy_drift: the live user-home ~/.codex/config.toml
//     approval_policy/sandbox_mode keys must match the fleet-documented
//     default in workspace/config-expectations.json (the same registry
//     addConfigDrift reads), or carry an explicit operator-override.
//
// Both fail closed: an unavailable binary or unreadable live config is
// reported as an explicit UNKNOWN failure, never silently passed, per the
// fleet's cannot-determine-must-deny policy.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// --- codex_version_pin ---

// codexVersionScanFiles lists the repo-relative docs scanned for documented
// codex-cli version pins. Scoped to the canonical, living docs (not dated
// research/*.md point-in-time records, and not code-example/mock text that
// happens to contain a version-shaped number).
var codexVersionScanFiles = []string{
	"AGENTS.md",
	"README.md",
	filepath.Join("docs", "CODEX_COMPATIBILITY.md"),
}

// codexVersionPinRe matches a documented "Codex 0.147.0" / "Codex CLI
// `0.154.0`" / "codex-cli 0.159.3" style version pin.
var codexVersionPinRe = regexp.MustCompile(`(?i)codex(?:[\s-]+cli)?\s+` + "`" + `?(\d+\.\d+\.\d+)` + "`" + `?`)

// codexCLIVersionLineRe matches the "codex-cli X.Y.Z" line that `codex
// --version` prints. The managed launcher wrapper prints JSON log lines to
// stdout before this line, so the parser scans every line rather than
// assuming the first one.
var codexCLIVersionLineRe = regexp.MustCompile(`(?m)^codex-cli (\d+\.\d+\.\d+)\s*$`)

// codexVersionProbeTimeout bounds the live `codex --version` probe so a
// hung or misbehaving binary cannot stall the baseline check.
const codexVersionProbeTimeout = 5 * time.Second

// codexVersionProbe runs `codex --version` — never `exec`/`review`, which
// the fleet's non-interactive guard refuses — and parses the live
// "codex-cli X.Y.Z" line out of its output. CODEXKIT_CODEX_BIN overrides the
// binary for hermetic tests (a fake script printing the launcher's JSON log
// lines followed by a codex-cli line).
func codexVersionProbe() (string, error) {
	bin := "codex"
	if override := os.Getenv("CODEXKIT_CODEX_BIN"); override != "" {
		bin = override
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexVersionProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", err
	}
	m := codexCLIVersionLineRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("no 'codex-cli X.Y.Z' line found in `%s --version` output", bin)
	}
	return m[1], nil
}

// addCodexVersionPinCheck compares documented codex-cli version pins against
// each other, then against a live probe. A missing/unreachable binary is
// reported UNKNOWN (failing closed), never a silent pass.
func (r *Report) addCodexVersionPinCheck(repoPath string) {
	fileByVersion := map[string][]string{}
	for _, rel := range codexVersionScanFiles {
		data, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		for _, m := range codexVersionPinRe.FindAllStringSubmatch(string(data), -1) {
			version := m[1]
			if !containsStr(fileByVersion[version], rel) {
				fileByVersion[version] = append(fileByVersion[version], rel)
			}
		}
	}
	if len(fileByVersion) == 0 {
		r.add("codex_version_pin", true, "no documented codex-cli version pins found — skipping")
		return
	}

	versions := make([]string, 0, len(fileByVersion))
	for v := range fileByVersion {
		versions = append(versions, v)
	}
	sort.Strings(versions)

	if len(versions) > 1 {
		parts := make([]string, 0, len(versions))
		for _, v := range versions {
			parts = append(parts, fmt.Sprintf("%s (%s)", v, strings.Join(fileByVersion[v], ", ")))
		}
		r.add("codex_version_pin", false, fmt.Sprintf("documented codex-cli version pins disagree: %s", strings.Join(parts, "; ")))
		return
	}

	documented := versions[0]
	live, err := codexVersionProbe()
	if err != nil {
		r.add("codex_version_pin", false, fmt.Sprintf(
			"UNKNOWN: codex binary unavailable to verify documented pin %s (%s) — failing closed per cannot-determine-must-deny: %v",
			documented, strings.Join(fileByVersion[documented], ", "), err))
		return
	}
	if live != documented {
		r.add("codex_version_pin", false, fmt.Sprintf(
			"documented codex-cli pin %s (%s) has drifted from the live binary %s",
			documented, strings.Join(fileByVersion[documented], ", "), live))
		return
	}
	r.add("codex_version_pin", true, fmt.Sprintf("documented codex-cli pin %s matches the live binary", documented))
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// --- codex_policy_drift ---

// codexPolicyKeys are the only two top-level ~/.codex/config.toml keys this
// check ever reads or reports. The rest of the document (MCP server
// command/env blocks can carry secrets) is parsed structurally — TOML
// key=value syntax cannot be reliably read with a naive grep — but never
// inspected or included in any finding message.
var codexPolicyKeys = []string{"approval_policy", "sandbox_mode"}

// codexPolicyOverrideRe matches an explicit "# operator-override: <key>"
// comment permitting a live value to diverge from the documented default for
// that key, mirroring the escape hatch config_drift's registry format
// implies for repo-local overrides.
var codexPolicyOverrideRe = regexp.MustCompile(`(?m)^\s*#\s*operator-override:\s*(\S+)`)

// resolveCodexHomeConfigPath locates the live user-home codex config.toml.
// CODEXKIT_CODEX_HOME overrides the directory for hermetic tests; CODEX_HOME
// is Codex's own real relocation mechanism and is honored the same way.
func resolveCodexHomeConfigPath() string {
	if override := os.Getenv("CODEXKIT_CODEX_HOME"); override != "" {
		return filepath.Join(override, "config.toml")
	}
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return filepath.Join(codexHome, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// addCodexPolicyDrift compares the fleet-documented approval_policy/
// sandbox_mode default — derived from workspace/config-expectations.json,
// the same registry addConfigDrift reads — against the live user-home
// ~/.codex/config.toml. An absent registry is an informational skip,
// matching addConfigDrift's own convention for standalone clones. An
// unreadable or missing live config, or a registry that disagrees with
// itself on the documented default, is reported UNKNOWN and fails closed.
func (r *Report) addCodexPolicyDrift(repoPath string) {
	registryPath := filepath.Join(filepath.Dir(filepath.Clean(repoPath)), "workspace", "config-expectations.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.add("codex_policy_drift", true, "config expectations registry absent — skipping")
			return
		}
		r.add("codex_policy_drift", false, fmt.Sprintf("read config expectations registry: %v", err))
		return
	}
	var registry configExpectationRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		r.add("codex_policy_drift", false, fmt.Sprintf("invalid config expectations registry: %v", err))
		return
	}
	if registry.Version != 1 {
		r.add("codex_policy_drift", false, fmt.Sprintf("unsupported config expectations registry version %d", registry.Version))
		return
	}

	for _, key := range codexPolicyKeys {
		values := map[string]bool{}
		for _, exp := range registry.Expectations {
			if exp.Provider != "codex" {
				continue
			}
			if v, ok := exp.Expected[key]; ok {
				if s, ok := v.(string); ok {
					values[s] = true
				}
			}
		}
		switch len(values) {
		case 0:
			r.add("codex_policy_drift", true, fmt.Sprintf("no documented codex %s default in the registry — skipping", key))
		case 1:
			r.checkLiveCodexPolicyKey(key, firstMapKey(values))
		default:
			r.add("codex_policy_drift", false, fmt.Sprintf("config expectations registry disagrees with itself on the documented codex %s default", key))
		}
	}
}

// checkLiveCodexPolicyKey reads exactly one key from the live
// ~/.codex/config.toml and compares it to the documented default.
func (r *Report) checkLiveCodexPolicyKey(key, documented string) {
	configPath := resolveCodexHomeConfigPath()
	if configPath == "" {
		r.add("codex_policy_drift", false, fmt.Sprintf(
			"UNKNOWN: cannot resolve a live ~/.codex/config.toml path to verify %s — failing closed per cannot-determine-must-deny", key))
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		r.add("codex_policy_drift", false, fmt.Sprintf(
			"UNKNOWN: cannot read the live codex config to verify %s — failing closed per cannot-determine-must-deny: %v", key, err))
		return
	}
	if err := validateCodexConfigTOML(data); err != nil {
		r.add("codex_policy_drift", false, fmt.Sprintf("UNKNOWN: live codex config is not valid TOML — cannot verify %s: %v", key, err))
		return
	}
	var doc map[string]any
	_ = toml.Unmarshal(data, &doc)
	liveRaw, ok := doc[key]
	if !ok {
		r.add("codex_policy_drift", false, fmt.Sprintf("UNKNOWN: live codex config has no top-level %s key — cannot verify", key))
		return
	}
	live, ok := liveRaw.(string)
	if !ok {
		r.add("codex_policy_drift", false, fmt.Sprintf("UNKNOWN: live codex config %s is not a string value — cannot verify", key))
		return
	}

	if live == documented {
		r.add("codex_policy_drift", true, fmt.Sprintf("live codex %s=%q matches the documented default", key, live))
		return
	}
	if hasOperatorOverride(data, key) {
		r.add("codex_policy_drift", true, fmt.Sprintf(
			"live codex %s=%q diverges from documented default %q under an explicit operator-override", key, live, documented))
		return
	}
	r.add("codex_policy_drift", false, fmt.Sprintf(
		"live codex %s=%q diverges from documented default %q with no operator-override comment", key, live, documented))
}

func hasOperatorOverride(data []byte, key string) bool {
	for _, m := range codexPolicyOverrideRe.FindAllStringSubmatch(string(data), -1) {
		if m[1] == key {
			return true
		}
	}
	return false
}

// firstMapKey returns the sole key of a single-entry map. Callers only
// invoke this once len(values) == 1 has already been established.
func firstMapKey(values map[string]bool) string {
	for k := range values {
		return k
	}
	return ""
}
