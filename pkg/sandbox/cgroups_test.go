package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgent58Constants(t *testing.T) {
	if Agent58ID != 58 {
		t.Fatalf("expected Agent58ID 58, got %d", Agent58ID)
	}
	if Agent58Name != "Agent 58: CPU and memory limits enforcer" {
		t.Fatalf("unexpected Agent58Name: %s", Agent58Name)
	}
}

func TestCgroupSubtree_WriteLimitsAndVerify(t *testing.T) {
	parentDir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 104857600,
		CPUQuota:  50000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-cg-1", limits, parentDir)
	if err != nil {
		t.Fatalf("NewCgroupSubtree failed: %v", err)
	}
	defer cs.Close()

	if cs.ID() != "test-cg-1" {
		t.Fatalf("expected ID test-cg-1, got %s", cs.ID())
	}
	if cs.ParentPath() != parentDir {
		t.Fatalf("expected parent %s, got %s", parentDir, cs.ParentPath())
	}

	expectedPath := filepath.Join(parentDir, "test-cg-1")
	if cs.Path() != expectedPath {
		t.Fatalf("expected path %s, got %s", expectedPath, cs.Path())
	}

	memBytes, err := os.ReadFile(filepath.Join(expectedPath, "memory.max"))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if strings.TrimSpace(string(memBytes)) != "104857600" {
		t.Fatalf("unexpected memory.max content: %s", string(memBytes))
	}

	cpuBytes, err := os.ReadFile(filepath.Join(expectedPath, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if strings.TrimSpace(string(cpuBytes)) != "50000 100000" {
		t.Fatalf("unexpected cpu.max content: %s", string(cpuBytes))
	}

	if err := cs.VerifyLimitEnforcement(); err != nil {
		t.Fatalf("VerifyLimitEnforcement failed: %v", err)
	}
}

func TestCgroupSubtree_Unlimited(t *testing.T) {
	parentDir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 0,
		CPUQuota:  0,
		CPUPeriod: 0,
	}

	cs, err := NewCgroupSubtree("test-unlimited", limits, parentDir)
	if err != nil {
		t.Fatalf("NewCgroupSubtree failed: %v", err)
	}
	defer cs.Close()

	memBytes, err := os.ReadFile(filepath.Join(cs.Path(), "memory.max"))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if strings.TrimSpace(string(memBytes)) != UnlimitedValue {
		t.Fatalf("expected %s, got %s", UnlimitedValue, string(memBytes))
	}

	cpuBytes, err := os.ReadFile(filepath.Join(cs.Path(), "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if strings.TrimSpace(string(cpuBytes)) != "max 100000" {
		t.Fatalf("expected 'max 100000', got %s", string(cpuBytes))
	}

	if err := cs.VerifyLimitEnforcement(); err != nil {
		t.Fatalf("VerifyLimitEnforcement failed: %v", err)
	}
}

func TestCgroupSubtree_UpdateLimits(t *testing.T) {
	parentDir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 52428800,
		CPUQuota:  25000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-update", limits, parentDir)
	if err != nil {
		t.Fatalf("NewCgroupSubtree failed: %v", err)
	}
	defer cs.Close()

	if err := cs.SetMemoryMax(209715200); err != nil {
		t.Fatalf("SetMemoryMax failed: %v", err)
	}
	if err := cs.SetCPUMax(75000, 100000); err != nil {
		t.Fatalf("SetCPUMax failed: %v", err)
	}
	if err := cs.VerifyLimitEnforcement(); err != nil {
		t.Fatalf("VerifyLimitEnforcement after partial update failed: %v", err)
	}

	newLimits := CgroupLimits{
		MemoryMax: 314572800,
		CPUQuota:  90000,
		CPUPeriod: 100000,
	}
	if err := cs.SetLimits(newLimits); err != nil {
		t.Fatalf("SetLimits failed: %v", err)
	}
	if err := cs.VerifyLimitEnforcement(); err != nil {
		t.Fatalf("VerifyLimitEnforcement after SetLimits failed: %v", err)
	}

	curLimits := cs.Limits()
	if curLimits.MemoryMax != 314572800 || curLimits.CPUQuota != 90000 {
		t.Fatalf("unexpected limits stored: %+v", curLimits)
	}
}

func TestCgroupSubtree_VerifyMismatch(t *testing.T) {
	parentDir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 104857600,
		CPUQuota:  50000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-mismatch", limits, parentDir)
	if err != nil {
		t.Fatalf("NewCgroupSubtree failed: %v", err)
	}
	defer cs.Close()

	_ = os.WriteFile(filepath.Join(cs.Path(), "memory.max"), []byte("999999\n"), 0644)
	if err := cs.VerifyLimitEnforcement(); err == nil {
		t.Fatalf("expected memory limit mismatch error, got nil")
	}

	_ = os.WriteFile(filepath.Join(cs.Path(), "memory.max"), []byte("104857600\n"), 0644)
	_ = os.WriteFile(filepath.Join(cs.Path(), "cpu.max"), []byte("10000 100000\n"), 0644)
	if err := cs.VerifyLimitEnforcement(); err == nil {
		t.Fatalf("expected cpu limit mismatch error, got nil")
	}
}

func TestCgroupSubtree_PIDs(t *testing.T) {
	parentDir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 104857600,
		CPUQuota:  50000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-pids", limits, parentDir)
	if err != nil {
		t.Fatalf("NewCgroupSubtree failed: %v", err)
	}
	defer cs.Close()

	if err := cs.AddPID(12345); err != nil {
		t.Fatalf("AddPID failed: %v", err)
	}
	pids, err := cs.ReadPIDs()
	if err != nil {
		t.Fatalf("ReadPIDs failed: %v", err)
	}
	if len(pids) != 1 || pids[0] != 12345 {
		t.Fatalf("expected [12345], got %v", pids)
	}

	if err := cs.AddPID(-1); err == nil {
		t.Fatalf("expected error for invalid PID -1")
	}
}

func TestParseCPUStat(t *testing.T) {
	data := `usage_usec 123456
user_usec 100000
system_usec 23456
core_sched.force_idle_usec 0
nr_periods 50
nr_throttled 5
throttled_usec 150000
`
	usage := ParseCPUStat(data)
	if usage.CPUUsageUsec != 123456 {
		t.Errorf("expected CPUUsageUsec 123456, got %d", usage.CPUUsageUsec)
	}
	if usage.UserUsec != 100000 {
		t.Errorf("expected UserUsec 100000, got %d", usage.UserUsec)
	}
	if usage.SystemUsec != 23456 {
		t.Errorf("expected SystemUsec 23456, got %d", usage.SystemUsec)
	}
	if usage.NrPeriods != 50 {
		t.Errorf("expected NrPeriods 50, got %d", usage.NrPeriods)
	}
	if usage.NrThrottled != 5 {
		t.Errorf("expected NrThrottled 5, got %d", usage.NrThrottled)
	}
	if usage.ThrottledUsec != 150000 {
		t.Errorf("expected ThrottledUsec 150000, got %d", usage.ThrottledUsec)
	}
}

func TestParseMemoryEvents(t *testing.T) {
	data := `low 1
high 2
max 3
oom 4
oom_kill 5
oom_group_kill 6
`
	events := ParseMemoryEvents(data)
	if events.Low != 1 {
		t.Errorf("expected Low 1, got %d", events.Low)
	}
	if events.High != 2 {
		t.Errorf("expected High 2, got %d", events.High)
	}
	if events.Max != 3 {
		t.Errorf("expected Max 3, got %d", events.Max)
	}
	if events.OOM != 4 {
		t.Errorf("expected OOM 4, got %d", events.OOM)
	}
	if events.OOMKill != 5 {
		t.Errorf("expected OOMKill 5, got %d", events.OOMKill)
	}
	if events.OOMGroupKill != 6 {
		t.Errorf("expected OOMGroupKill 6, got %d", events.OOMGroupKill)
	}
}

func TestEnforceLimits_Standalone(t *testing.T) {
	dir := t.TempDir()
	limits := CgroupLimits{
		MemoryMax: 67108864,
		CPUQuota:  40000,
		CPUPeriod: 100000,
	}

	if err := EnforceLimits(dir, limits); err != nil {
		t.Fatalf("EnforceLimits failed: %v", err)
	}
	if err := VerifyLimits(dir, limits); err != nil {
		t.Fatalf("VerifyLimits failed: %v", err)
	}
}

func TestCgroupSubtree_LiveKernelEnforcement(t *testing.T) {
	if !IsCgroupV2Supported() {
		t.Skip("cgroups v2 not supported on this host")
	}

	parent, err := FindSubtreeParent("")
	if err != nil {
		t.Skipf("cannot find cgroup parent: %v", err)
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Skipf("cannot create cgroup parent: %v", err)
	}

	limits := CgroupLimits{
		MemoryMax: 50 * 1024 * 1024,
		CPUQuota:  20000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-live-agent58", limits, parent)
	if err != nil {
		t.Skipf("live cgroup creation not permitted: %v", err)
	}
	defer cs.Close()

	if err := cs.VerifyLimitEnforcement(); err != nil {
		t.Fatalf("live VerifyLimitEnforcement failed: %v", err)
	}

	usage, err := cs.ReadUsage()
	if err != nil {
		t.Fatalf("ReadUsage failed: %v", err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage")
	}
}

func TestCgroupSubtree_LiveProcessAttachmentAndUsage(t *testing.T) {
	if !IsCgroupV2Supported() {
		t.Skip("cgroups v2 not supported on this host")
	}

	parent, err := FindSubtreeParent("")
	if err != nil {
		t.Skipf("cannot find cgroup parent: %v", err)
	}

	limits := CgroupLimits{
		MemoryMax: 100 * 1024 * 1024,
		CPUQuota:  50000,
		CPUPeriod: 100000,
	}

	cs, err := NewCgroupSubtree("test-live-proc-agent58", limits, parent)
	if err != nil {
		t.Skipf("live cgroup creation not permitted: %v", err)
	}
	defer cs.Close()

	cmd := exec.Command("cat", "/proc/self/cgroup")
	if err := cs.AttachCmd(cmd); err != nil {
		t.Fatalf("AttachCmd failed: %v", err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd execution failed: %v", err)
	}
	if !strings.Contains(string(out), "test-live-proc-agent58") {
		t.Fatalf("expected process cgroup to contain test-live-proc-agent58, got: %s", string(out))
	}
}
