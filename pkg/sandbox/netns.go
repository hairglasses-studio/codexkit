package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	NetNSAgentID   = 54
	NetNSAgentName = "Agent 54: Network namespace unsharing verifier"
	Agent54ID      = 54
	Agent54Name    = "Agent 54: Network namespace unsharing verifier"
)

type RouteEntry struct {
	Interface   string `json:"interface"`
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Flags       string `json:"flags"`
	Mask        string `json:"mask"`
}

type NetNSVerificationConfig struct {
	Timeout              time.Duration `json:"timeout"`
	EgressTargets        []string      `json:"egress_targets"`
	AllowedInterfaces    []string      `json:"allowed_interfaces"`
	RequireLoopback      bool          `json:"require_loopback"`
	CheckLoopbackTraffic bool          `json:"check_loopback_traffic"`
	CheckEgress          bool          `json:"check_egress"`
	CheckRoutes          bool          `json:"check_routes"`
	CheckInterfaces      bool          `json:"check_interfaces"`
}

func DefaultNetNSVerificationConfig() NetNSVerificationConfig {
	return NetNSVerificationConfig{
		Timeout:              10 * time.Second,
		EgressTargets:        []string{"1.1.1.1:80", "8.8.8.8:53"},
		AllowedInterfaces:    []string{"lo"},
		RequireLoopback:      true,
		CheckLoopbackTraffic: true,
		CheckEgress:          true,
		CheckRoutes:          true,
		CheckInterfaces:      true,
	}
}

type NetNSReport struct {
	AgentID              int               `json:"agent_id"`
	AgentName            string            `json:"agent_name"`
	Isolated             bool              `json:"isolated"`
	LoopbackOnly         bool              `json:"loopback_only"`
	NoExternalRouting    bool              `json:"no_external_routing"`
	EgressBlocked        bool              `json:"egress_blocked"`
	LoopbackFunctional   bool              `json:"loopback_functional"`
	Interfaces           []string          `json:"interfaces"`
	DisallowedInterfaces []string          `json:"disallowed_interfaces,omitempty"`
	Routes               []string          `json:"routes,omitempty"`
	DisallowedRoutes     []string          `json:"disallowed_routes,omitempty"`
	EgressProbes         map[string]string `json:"egress_probes,omitempty"`
	LoopbackStatus       string            `json:"loopback_status,omitempty"`
	Violations           []string          `json:"violations,omitempty"`
	DurationMs           float64           `json:"duration_ms"`
}

type NetNSVerifier struct {
	cfg NetNSVerificationConfig
}

func NewNetNSVerifier() *NetNSVerifier {
	return &NetNSVerifier{cfg: DefaultNetNSVerificationConfig()}
}

func NewNetNSVerifierWithConfig(cfg NetNSVerificationConfig) *NetNSVerifier {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if len(cfg.AllowedInterfaces) == 0 {
		cfg.AllowedInterfaces = []string{"lo"}
	}
	if len(cfg.EgressTargets) == 0 {
		cfg.EgressTargets = []string{"1.1.1.1:80", "8.8.8.8:53"}
	}
	return &NetNSVerifier{cfg: cfg}
}

func (v *NetNSVerifier) Config() NetNSVerificationConfig {
	return v.cfg
}

func (v *NetNSVerifier) Verify(ctx context.Context, sb *Sandbox) (*NetNSReport, error) {
	if sb == nil {
		return nil, errors.New("sandbox cannot be nil")
	}

	start := time.Now()
	script := v.BuildProbeScript()

	probeCtx := ctx
	if v.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, v.cfg.Timeout)
		defer cancel()
	}

	res, err := sb.RunScript(probeCtx, script)
	if err != nil {
		return nil, fmt.Errorf("execute netns probe script: %w", err)
	}

	report := v.ParseProbeOutput(res.Stdout)
	report.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	return report, nil
}

func (v *NetNSVerifier) VerifyConfig(ctx context.Context, cfg Config) (*NetNSReport, error) {
	sb, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create sandbox for verification: %w", err)
	}
	defer sb.Close()
	return v.Verify(ctx, sb)
}

func (v *NetNSVerifier) VerifyDefault(ctx context.Context) (*NetNSReport, error) {
	return v.VerifyConfig(ctx, DefaultConfig())
}

func (v *NetNSVerifier) BuildProbeScript() string {
	return BuildProbeScript(v.cfg.EgressTargets)
}

func (v *NetNSVerifier) ParseProbeOutput(output string) *NetNSReport {
	sections := ExtractSections(output)

	var allIfaces []string
	seenIfaces := make(map[string]bool)

	ifacesDev := ParseProcNetDev(sections["IFACES_DEV"])
	for _, iface := range ifacesDev {
		if !seenIfaces[iface] {
			seenIfaces[iface] = true
			allIfaces = append(allIfaces, iface)
		}
	}

	ifacesIP := ParseIPLink(sections["IFACES_IP"])
	for _, iface := range ifacesIP {
		if !seenIfaces[iface] {
			seenIfaces[iface] = true
			allIfaces = append(allIfaces, iface)
		}
	}
	sort.Strings(allIfaces)

	procRoutes := ParseProcNetRoute(sections["ROUTES_PROC"])
	ipRoutes := ParseIPRoute(sections["ROUTES_IP"])
	ip6Routes := ParseIPRoute(sections["ROUTES_IP6"])

	var combinedIPRoutes []string
	combinedIPRoutes = append(combinedIPRoutes, ipRoutes...)
	combinedIPRoutes = append(combinedIPRoutes, ip6Routes...)

	var allRouteStrings []string
	for _, r := range procRoutes {
		allRouteStrings = append(allRouteStrings, fmt.Sprintf("%s -> dest=%s gw=%s mask=%s", r.Interface, r.Destination, r.Gateway, r.Mask))
	}
	allRouteStrings = append(allRouteStrings, combinedIPRoutes...)

	loopbackOK, loopbackStatus := ParseLoopbackOutput(sections["LOOPBACK"])
	egressProbes, egressBlocked, egressViolations := ParseEgressOutput(sections["EGRESS"])

	loopbackOnly, disallowedIfaces := EvaluateLoopbackOnly(allIfaces, v.cfg.AllowedInterfaces, v.cfg.RequireLoopback)
	noExternalRouting, routeViolations := EvaluateRoutes(procRoutes, combinedIPRoutes)

	if !v.cfg.CheckInterfaces {
		loopbackOnly = true
		disallowedIfaces = nil
	}
	if !v.cfg.CheckRoutes {
		noExternalRouting = true
		routeViolations = nil
	}
	if !v.cfg.CheckEgress {
		egressBlocked = true
		egressViolations = nil
	}
	if !v.cfg.CheckLoopbackTraffic {
		loopbackOK = true
	}

	var violations []string
	for _, iface := range disallowedIfaces {
		violations = append(violations, fmt.Sprintf("disallowed network interface: %s", iface))
	}
	if v.cfg.CheckInterfaces && v.cfg.RequireLoopback && !seenIfaces["lo"] {
		violations = append(violations, "missing required loopback interface (lo)")
	}
	violations = append(violations, routeViolations...)
	violations = append(violations, egressViolations...)
	if v.cfg.CheckLoopbackTraffic && !loopbackOK {
		violations = append(violations, fmt.Sprintf("loopback communication failed: %s", loopbackStatus))
	}

	isolated := loopbackOnly && noExternalRouting && egressBlocked && loopbackOK

	return &NetNSReport{
		AgentID:              NetNSAgentID,
		AgentName:            NetNSAgentName,
		Isolated:             isolated,
		LoopbackOnly:         loopbackOnly,
		NoExternalRouting:    noExternalRouting,
		EgressBlocked:        egressBlocked,
		LoopbackFunctional:   loopbackOK,
		Interfaces:           allIfaces,
		DisallowedInterfaces: disallowedIfaces,
		Routes:               allRouteStrings,
		DisallowedRoutes:     routeViolations,
		EgressProbes:         egressProbes,
		LoopbackStatus:       loopbackStatus,
		Violations:           violations,
	}
}

func VerifyNetNS(ctx context.Context, sb *Sandbox) (*NetNSReport, error) {
	return NewNetNSVerifier().Verify(ctx, sb)
}

func VerifySandboxIsolation(ctx context.Context, cfg Config) (*NetNSReport, error) {
	return NewNetNSVerifier().VerifyConfig(ctx, cfg)
}

func BuildProbeScript(targets []string) string {
	var pyTargets []string
	for _, t := range targets {
		parts := strings.Split(t, ":")
		if len(parts) == 2 {
			pyTargets = append(pyTargets, fmt.Sprintf(`("%s", %s)`, parts[0], parts[1]))
		}
	}
	pyTargetsStr := "[" + strings.Join(pyTargets, ", ") + "]"
	shellTargetsStr := strings.Join(targets, " ")

	return fmt.Sprintf(`echo "---IFACES_DEV---"
cat /proc/net/dev 2>/dev/null
echo "---IFACES_IP---"
ip -o link show 2>/dev/null
echo "---ROUTES_PROC---"
cat /proc/net/route 2>/dev/null
echo "---ROUTES_IP---"
ip route show 2>/dev/null
echo "---ROUTES_IP6---"
ip -6 route show 2>/dev/null
echo "---LOOPBACK---"
python3 -c '
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    s.listen(1)
    port = s.getsockname()[1]
    c = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    c.connect(("127.0.0.1", port))
    conn, _ = s.accept()
    conn.sendall(b"PING")
    data = c.recv(4)
    if data == b"PING":
        print("LOOPBACK_OK")
    else:
        print("LOOPBACK_DATA_MISMATCH")
    c.close()
    conn.close()
    s.close()
except Exception as e:
    print(f"LOOPBACK_ERR:{e}")
' 2>/dev/null || (nc -l 127.0.0.1 23456 & ncl_pid=$!; sleep 0.05; nc -z 127.0.0.1 23456 2>/dev/null && echo "LOOPBACK_OK" || echo "LOOPBACK_UNAVAILABLE"; kill $ncl_pid 2>/dev/null)
echo "---EGRESS---"
python3 -c '
import socket
targets = %s
for host, port in targets:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(1.0)
    try:
        s.connect((host, port))
        print(f"CONNECTED:{host}:{port}")
        s.close()
    except Exception as e:
        print(f"BLOCKED:{host}:{port}:{e}")
' 2>/dev/null || for target in %s; do
    host=$(echo $target | cut -d: -f1)
    port=$(echo $target | cut -d: -f2)
    curl -s --connect-timeout 1 http://$host:$port >/dev/null 2>&1 && echo "CONNECTED:$host:$port" || echo "BLOCKED:$host:$port:network_unreachable"
done
echo "---END---"
`, pyTargetsStr, shellTargetsStr)
}

func ExtractSections(output string) map[string]string {
	sections := make(map[string]string)
	currentSection := ""
	var currentLines []string

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "---") && strings.HasSuffix(trimmed, "---") && len(trimmed) > 6 {
			if currentSection != "" {
				sections[currentSection] = strings.Join(currentLines, "\n")
				currentLines = nil
			}
			currentSection = strings.Trim(trimmed, "-")
			continue
		}
		if currentSection != "" {
			currentLines = append(currentLines, line)
		}
	}
	if currentSection != "" && len(currentLines) > 0 {
		sections[currentSection] = strings.Join(currentLines, "\n")
	}
	return sections
}

func ParseProcNetDev(content string) []string {
	var ifaces []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			iface := strings.TrimSpace(parts[0])
			if iface != "" && iface != "face" && iface != "Inter-|" && !seen[iface] {
				seen[iface] = true
				ifaces = append(ifaces, iface)
			}
		}
	}
	return ifaces
}

func ParseIPLink(content string) []string {
	var ifaces []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) >= 2 {
			var num int
			if _, err := fmt.Sscanf(parts[0], "%d", &num); err == nil {
				iface := strings.TrimSpace(parts[1])
				if atIdx := strings.Index(iface, "@"); atIdx != -1 {
					iface = iface[:atIdx]
				}
				if iface != "" && !seen[iface] {
					seen[iface] = true
					ifaces = append(ifaces, iface)
				}
			}
		}
	}
	return ifaces
}

func ParseProcNetRoute(content string) []RouteEntry {
	var routes []RouteEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 8 {
			if fields[0] == "Iface" {
				continue
			}
			routes = append(routes, RouteEntry{
				Interface:   fields[0],
				Destination: fields[1],
				Gateway:     fields[2],
				Flags:       fields[3],
				Mask:        fields[7],
			})
		}
	}
	return routes
}

func ParseIPRoute(content string) []string {
	var routes []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			routes = append(routes, line)
		}
	}
	return routes
}

func EvaluateLoopbackOnly(interfaces []string, allowed []string, requireLoopback bool) (bool, []string) {
	allowedMap := make(map[string]bool)
	for _, a := range allowed {
		allowedMap[a] = true
	}
	var disallowed []string
	hasLoopback := false
	for _, iface := range interfaces {
		if iface == "lo" {
			hasLoopback = true
		}
		if !allowedMap[iface] {
			disallowed = append(disallowed, iface)
		}
	}
	loopbackOnly := len(disallowed) == 0
	if requireLoopback && !hasLoopback {
		loopbackOnly = false
	}
	return loopbackOnly, disallowed
}

func EvaluateRoutes(procRoutes []RouteEntry, ipRoutes []string) (bool, []string) {
	var violations []string
	for _, r := range procRoutes {
		if r.Interface != "lo" {
			violations = append(violations, fmt.Sprintf("route on non-loopback interface: %s -> dest=%s gw=%s", r.Interface, r.Destination, r.Gateway))
		} else if r.Gateway != "00000000" {
			violations = append(violations, fmt.Sprintf("loopback route with gateway: gw=%s", r.Gateway))
		}
	}
	for _, r := range ipRoutes {
		if strings.Contains(r, "default") {
			violations = append(violations, fmt.Sprintf("default route detected: %s", r))
		} else if !strings.Contains(r, "dev lo") {
			violations = append(violations, fmt.Sprintf("route on external interface: %s", r))
		}
	}
	noExternalRouting := len(violations) == 0
	return noExternalRouting, violations
}

func ParseEgressOutput(output string) (map[string]string, bool, []string) {
	results := make(map[string]string)
	var violations []string
	blockedAll := true

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "CONNECTED:") {
			target := strings.TrimPrefix(line, "CONNECTED:")
			results[target] = "CONNECTED"
			blockedAll = false
			violations = append(violations, fmt.Sprintf("egress connection succeeded to %s", target))
		} else if strings.HasPrefix(line, "BLOCKED:") {
			parts := strings.SplitN(strings.TrimPrefix(line, "BLOCKED:"), ":", 3)
			if len(parts) >= 2 {
				target := parts[0] + ":" + parts[1]
				reason := "blocked"
				if len(parts) == 3 {
					reason = parts[2]
				}
				results[target] = reason
			}
		}
	}
	return results, blockedAll, violations
}

func ParseLoopbackOutput(output string) (bool, string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "LOOPBACK_OK" {
			return true, "functional"
		}
		if strings.HasPrefix(line, "LOOPBACK_ERR:") {
			return false, strings.TrimPrefix(line, "LOOPBACK_ERR:")
		}
		if line == "LOOPBACK_DATA_MISMATCH" {
			return false, "data mismatch"
		}
		if line == "LOOPBACK_UNAVAILABLE" {
			return false, "loopback probe unavailable"
		}
	}
	return false, "no loopback probe output"
}
