package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAgent54Constants(t *testing.T) {
	if NetNSAgentID != 54 || Agent54ID != 54 {
		t.Fatalf("expected AgentID 54, got NetNSAgentID=%d, Agent54ID=%d", NetNSAgentID, Agent54ID)
	}
	if NetNSAgentName != "Agent 54: Network namespace unsharing verifier" || Agent54Name != "Agent 54: Network namespace unsharing verifier" {
		t.Fatalf("unexpected AgentName: %s", NetNSAgentName)
	}
}

func TestParseProcNetDev(t *testing.T) {
	isolatedOutput := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
`
	ifaces := ParseProcNetDev(isolatedOutput)
	if len(ifaces) != 1 || ifaces[0] != "lo" {
		t.Fatalf("expected ['lo'], got %v", ifaces)
	}

	multiOutput := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
docker0: 500 5 0 0 0 0 0 0 1000 10 0 0 0 0 0 0
`
	multiIfaces := ParseProcNetDev(multiOutput)
	if len(multiIfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d: %v", len(multiIfaces), multiIfaces)
	}
	expected := map[string]bool{"lo": true, "eth0": true, "docker0": true}
	for _, iface := range multiIfaces {
		if !expected[iface] {
			t.Fatalf("unexpected interface: %s", iface)
		}
	}

	emptyIfaces := ParseProcNetDev("")
	if len(emptyIfaces) != 0 {
		t.Fatalf("expected empty interfaces, got %v", emptyIfaces)
	}

	garbageIfaces := ParseProcNetDev("some random text without colons\nanother line")
	if len(garbageIfaces) != 0 {
		t.Fatalf("expected empty interfaces for garbage input, got %v", garbageIfaces)
	}
}

func TestParseIPLink(t *testing.T) {
	isolatedOutput := `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000\    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00`
	ifaces := ParseIPLink(isolatedOutput)
	if len(ifaces) != 1 || ifaces[0] != "lo" {
		t.Fatalf("expected ['lo'], got %v", ifaces)
	}

	multiOutput := `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500
3: veth123@if456: <BROADCAST,MULTICAST> mtu 1500
`
	multiIfaces := ParseIPLink(multiOutput)
	if len(multiIfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d: %v", len(multiIfaces), multiIfaces)
	}
	expected := []string{"lo", "eth0", "veth123"}
	for i, exp := range expected {
		if multiIfaces[i] != exp {
			t.Fatalf("at index %d: expected %s, got %s", i, exp, multiIfaces[i])
		}
	}

	macLine := `    link/ether 00:11:22:33:44:55 brd ff:ff:ff:ff:ff:ff`
	ifacesMac := ParseIPLink(macLine)
	if len(ifacesMac) != 0 {
		t.Fatalf("expected no interfaces from MAC address line, got %v", ifacesMac)
	}
}

func TestParseProcNetRoute(t *testing.T) {
	emptyRouteTable := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
`
	routes := ParseProcNetRoute(emptyRouteTable)
	if len(routes) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(routes))
	}

	populatedRouteTable := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
enp13s0	00000000	0100140A	0003	0	0	50	00000000	0	0	0
enp13s0	0000140A	00000000	0001	0	0	50	00FFFFFF	0	0	0
`
	routesPop := ParseProcNetRoute(populatedRouteTable)
	if len(routesPop) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routesPop))
	}
	if routesPop[0].Interface != "enp13s0" || routesPop[0].Destination != "00000000" || routesPop[0].Gateway != "0100140A" {
		t.Fatalf("unexpected route 0: %+v", routesPop[0])
	}
	if routesPop[1].Interface != "enp13s0" || routesPop[1].Destination != "0000140A" || routesPop[1].Gateway != "00000000" {
		t.Fatalf("unexpected route 1: %+v", routesPop[1])
	}
}

func TestParseIPRoute(t *testing.T) {
	emptyOutput := ""
	routes := ParseIPRoute(emptyOutput)
	if len(routes) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(routes))
	}

	routeOutput := `default via 10.20.0.1 dev enp13s0 proto dhcp src 10.20.0.56 metric 50
10.20.0.0/24 dev enp13s0 proto kernel scope link src 10.20.0.56 metric 50
`
	routes = ParseIPRoute(routeOutput)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}

func TestEvaluateLoopbackOnly(t *testing.T) {
	ok, disallowed := EvaluateLoopbackOnly([]string{"lo"}, []string{"lo"}, true)
	if !ok || len(disallowed) != 0 {
		t.Fatalf("expected ok=true, disallowed=[], got ok=%v, disallowed=%v", ok, disallowed)
	}

	ok, disallowed = EvaluateLoopbackOnly([]string{"lo", "eth0"}, []string{"lo"}, true)
	if ok || len(disallowed) != 1 || disallowed[0] != "eth0" {
		t.Fatalf("expected ok=false, disallowed=['eth0'], got ok=%v, disallowed=%v", ok, disallowed)
	}

	ok, disallowed = EvaluateLoopbackOnly([]string{}, []string{"lo"}, true)
	if ok {
		t.Fatalf("expected ok=false when lo is missing and requireLoopback=true")
	}

	ok, disallowed = EvaluateLoopbackOnly([]string{}, []string{"lo"}, false)
	if !ok || len(disallowed) != 0 {
		t.Fatalf("expected ok=true when requireLoopback=false and list is empty")
	}

	ok, disallowed = EvaluateLoopbackOnly([]string{"lo", "custom0"}, []string{"lo", "custom0"}, true)
	if !ok || len(disallowed) != 0 {
		t.Fatalf("expected ok=true with custom allowed interfaces, got ok=%v, disallowed=%v", ok, disallowed)
	}
}

func TestEvaluateRoutes(t *testing.T) {
	ok, violations := EvaluateRoutes(nil, nil)
	if !ok || len(violations) != 0 {
		t.Fatalf("expected ok=true, violations=0, got ok=%v, violations=%v", ok, violations)
	}

	procRoutes := []RouteEntry{
		{Interface: "lo", Destination: "0000007F", Gateway: "00000000", Flags: "0001", Mask: "000000FF"},
	}
	ok, violations = EvaluateRoutes(procRoutes, nil)
	if !ok || len(violations) != 0 {
		t.Fatalf("expected ok=true for pure loopback route without gw, got ok=%v, violations=%v", ok, violations)
	}

	procRoutesExt := []RouteEntry{
		{Interface: "eth0", Destination: "00000000", Gateway: "0100140A", Flags: "0003", Mask: "00000000"},
	}
	ok, violations = EvaluateRoutes(procRoutesExt, nil)
	if ok || len(violations) == 0 {
		t.Fatalf("expected ok=false for external route, got ok=%v, violations=%v", ok, violations)
	}

	procRoutesLoopbackGw := []RouteEntry{
		{Interface: "lo", Destination: "00000000", Gateway: "0100140A", Flags: "0003", Mask: "00000000"},
	}
	ok, violations = EvaluateRoutes(procRoutesLoopbackGw, nil)
	if ok || len(violations) == 0 {
		t.Fatalf("expected ok=false for loopback route with gateway, got ok=%v, violations=%v", ok, violations)
	}

	ipRoutesDefault := []string{
		"default via 10.20.0.1 dev enp13s0 proto dhcp",
	}
	ok, violations = EvaluateRoutes(nil, ipRoutesDefault)
	if ok || len(violations) == 0 {
		t.Fatalf("expected ok=false for ip default route, got ok=%v, violations=%v", ok, violations)
	}

	ipRoutesExt := []string{
		"10.20.0.0/24 dev enp13s0 proto kernel scope link",
	}
	ok, violations = EvaluateRoutes(nil, ipRoutesExt)
	if ok || len(violations) == 0 {
		t.Fatalf("expected ok=false for ip external route, got ok=%v, violations=%v", ok, violations)
	}
}

func TestParseEgressOutput(t *testing.T) {
	blockedOutput := `BLOCKED:1.1.1.1:80:[Errno 101] Network is unreachable
BLOCKED:8.8.8.8:53:[Errno 101] Network is unreachable
`
	res, blockedAll, violations := ParseEgressOutput(blockedOutput)
	if !blockedAll || len(violations) != 0 {
		t.Fatalf("expected blockedAll=true, violations=0, got blockedAll=%v, violations=%v", blockedAll, violations)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 egress probe records, got %d", len(res))
	}
	if !strings.Contains(res["1.1.1.1:80"], "Network is unreachable") {
		t.Fatalf("unexpected probe status: %s", res["1.1.1.1:80"])
	}

	connectedOutput := `CONNECTED:1.1.1.1:80
BLOCKED:8.8.8.8:53:[Errno 101] Network is unreachable
`
	res, blockedAll, violations = ParseEgressOutput(connectedOutput)
	if blockedAll || len(violations) != 1 {
		t.Fatalf("expected blockedAll=false, violations=1, got blockedAll=%v, violations=%v", blockedAll, violations)
	}
	if res["1.1.1.1:80"] != "CONNECTED" {
		t.Fatalf("expected CONNECTED status, got: %s", res["1.1.1.1:80"])
	}
}

func TestParseLoopbackOutput(t *testing.T) {
	ok, status := ParseLoopbackOutput("LOOPBACK_OK\n")
	if !ok || status != "functional" {
		t.Fatalf("expected ok=true, status='functional', got ok=%v, status=%s", ok, status)
	}

	ok, status = ParseLoopbackOutput("LOOPBACK_ERR:connection refused\n")
	if ok || status != "connection refused" {
		t.Fatalf("expected ok=false, status='connection refused', got ok=%v, status=%s", ok, status)
	}

	ok, status = ParseLoopbackOutput("LOOPBACK_DATA_MISMATCH\n")
	if ok || status != "data mismatch" {
		t.Fatalf("expected ok=false, status='data mismatch', got ok=%v, status=%s", ok, status)
	}

	ok, status = ParseLoopbackOutput("LOOPBACK_UNAVAILABLE\n")
	if ok || status != "loopback probe unavailable" {
		t.Fatalf("expected ok=false, status='loopback probe unavailable', got ok=%v, status=%s", ok, status)
	}

	ok, status = ParseLoopbackOutput("")
	if ok || status != "no loopback probe output" {
		t.Fatalf("expected ok=false, status='no loopback probe output', got ok=%v, status=%s", ok, status)
	}
}

func TestExtractSections(t *testing.T) {
	raw := `---IFACES_DEV---
line1
line2
---ROUTES_PROC---
line3
---END---
`
	sections := ExtractSections(raw)
	if sections["IFACES_DEV"] != "line1\nline2" {
		t.Fatalf("unexpected IFACES_DEV section: %q", sections["IFACES_DEV"])
	}
	if sections["ROUTES_PROC"] != "line3" {
		t.Fatalf("unexpected ROUTES_PROC section: %q", sections["ROUTES_PROC"])
	}
}

func TestParseProbeOutput_MockIsolated(t *testing.T) {
	mockIsolated := `---IFACES_DEV---
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
---IFACES_IP---
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536
---ROUTES_PROC---
Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
---ROUTES_IP---
---ROUTES_IP6---
---LOOPBACK---
LOOPBACK_OK
---EGRESS---
BLOCKED:1.1.1.1:80:[Errno 101] Network is unreachable
BLOCKED:8.8.8.8:53:[Errno 101] Network is unreachable
---END---
`
	verifier := NewNetNSVerifier()
	report := verifier.ParseProbeOutput(mockIsolated)

	if !report.Isolated {
		t.Fatalf("expected isolated=true, violations=%v", report.Violations)
	}
	if !report.LoopbackOnly {
		t.Fatalf("expected loopback_only=true")
	}
	if !report.NoExternalRouting {
		t.Fatalf("expected no_external_routing=true")
	}
	if !report.EgressBlocked {
		t.Fatalf("expected egress_blocked=true")
	}
	if !report.LoopbackFunctional {
		t.Fatalf("expected loopback_functional=true")
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %v", report.Violations)
	}
	if report.AgentID != NetNSAgentID {
		t.Fatalf("expected AgentID %d, got %d", NetNSAgentID, report.AgentID)
	}
}

func TestParseProbeOutput_MockUnisolated(t *testing.T) {
	mockUnisolated := `---IFACES_DEV---
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
---IFACES_IP---
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500
---ROUTES_PROC---
Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0100140A	0003	0	0	50	00000000	0	0	0
---ROUTES_IP---
default via 10.20.0.1 dev eth0 proto dhcp
---ROUTES_IP6---
---LOOPBACK---
LOOPBACK_OK
---EGRESS---
CONNECTED:1.1.1.1:80
---END---
`
	verifier := NewNetNSVerifier()
	report := verifier.ParseProbeOutput(mockUnisolated)

	if report.Isolated {
		t.Fatalf("expected isolated=false for unisolated mock")
	}
	if report.LoopbackOnly {
		t.Fatalf("expected loopback_only=false")
	}
	if report.NoExternalRouting {
		t.Fatalf("expected no_external_routing=false")
	}
	if report.EgressBlocked {
		t.Fatalf("expected egress_blocked=false")
	}
	if len(report.Violations) < 3 {
		t.Fatalf("expected multiple violations, got %d: %v", len(report.Violations), report.Violations)
	}
}

func TestBuildProbeScript(t *testing.T) {
	targets := []string{"1.1.1.1:80", "8.8.8.8:53"}
	script := BuildProbeScript(targets)
	if !strings.Contains(script, `("1.1.1.1", 80)`) {
		t.Fatalf("script missing python target 1.1.1.1:80")
	}
	if !strings.Contains(script, `("8.8.8.8", 53)`) {
		t.Fatalf("script missing python target 8.8.8.8:53")
	}
	if !strings.Contains(script, "---IFACES_DEV---") {
		t.Fatalf("script missing IFACES_DEV section")
	}
	if !strings.Contains(script, "---LOOPBACK---") {
		t.Fatalf("script missing LOOPBACK section")
	}
	if !strings.Contains(script, "---EGRESS---") {
		t.Fatalf("script missing EGRESS section")
	}
}

func TestNewNetNSVerifierWithConfig(t *testing.T) {
	cfg := NetNSVerificationConfig{
		Timeout:              5 * time.Second,
		AllowedInterfaces:    []string{"lo", "test0"},
		EgressTargets:        []string{"9.9.9.9:443"},
		RequireLoopback:      false,
		CheckLoopbackTraffic: false,
		CheckEgress:          false,
		CheckRoutes:          false,
		CheckInterfaces:      false,
	}
	v := NewNetNSVerifierWithConfig(cfg)
	if v.Config().Timeout != 5*time.Second {
		t.Fatalf("unexpected timeout: %v", v.Config().Timeout)
	}
	if len(v.Config().AllowedInterfaces) != 2 {
		t.Fatalf("unexpected allowed interfaces: %v", v.Config().AllowedInterfaces)
	}
	if len(v.Config().EgressTargets) != 1 {
		t.Fatalf("unexpected egress targets: %v", v.Config().EgressTargets)
	}
}

func TestLiveNetNSIsolation_Default(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	verifier := NewNetNSVerifier()
	report, err := verifier.VerifyDefault(ctx)
	if err != nil {
		t.Fatalf("VerifyDefault failed: %v", err)
	}

	if report.AgentID != NetNSAgentID {
		t.Errorf("expected AgentID %d, got %d", NetNSAgentID, report.AgentID)
	}
	if !report.Isolated {
		t.Fatalf("expected sandbox to be isolated, violations=%v", report.Violations)
	}
	if !report.LoopbackOnly {
		t.Errorf("expected loopback_only=true, interfaces=%v, disallowed=%v", report.Interfaces, report.DisallowedInterfaces)
	}
	if !report.NoExternalRouting {
		t.Errorf("expected no_external_routing=true, routes=%v, disallowed=%v", report.Routes, report.DisallowedRoutes)
	}
	if !report.EgressBlocked {
		t.Errorf("expected egress_blocked=true, egressProbes=%v", report.EgressProbes)
	}
	if !report.LoopbackFunctional {
		t.Errorf("expected loopback_functional=true, status=%s", report.LoopbackStatus)
	}
	if len(report.Violations) != 0 {
		t.Errorf("expected 0 violations, got %v", report.Violations)
	}
}

func TestLiveNetNSIsolation_DisabledNetUnshare(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := DefaultConfig()
	cfg.UnshareNet = false

	verifier := NewNetNSVerifier()
	report, err := verifier.VerifyConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("VerifyConfig with unshare_net=false failed: %v", err)
	}

	if report.Isolated {
		t.Fatalf("expected sandbox with UnshareNet=false to NOT be isolated")
	}
	if len(report.Violations) == 0 {
		t.Fatalf("expected violations for host network mode, got 0")
	}
}

func TestVerifyNetNS_Helper(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sb, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	report, err := VerifyNetNS(ctx, sb)
	if err != nil {
		t.Fatalf("VerifyNetNS failed: %v", err)
	}
	if !report.Isolated {
		t.Fatalf("expected sandbox to be isolated, violations: %v", report.Violations)
	}

	reportCfg, err := VerifySandboxIsolation(ctx, DefaultConfig())
	if err != nil {
		t.Fatalf("VerifySandboxIsolation failed: %v", err)
	}
	if !reportCfg.Isolated {
		t.Fatalf("expected sandbox to be isolated via config, violations: %v", reportCfg.Violations)
	}
}
