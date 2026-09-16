package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	Agent58ID   = 58
	Agent58Name = "Agent 58: CPU and memory limits enforcer"

	DefaultCPUPeriod int64 = 100000
	UnlimitedValue         = "max"

	cgroupV2DefaultMount = "/sys/fs/cgroup"
	defaultSubtreeName   = "codexkit.sandbox"
)

var (
	cgroupRootMu         sync.RWMutex
	cgroupRootOverride   string
	cgroupParentOverride string
)

type CgroupLimits struct {
	MemoryMax int64  `json:"memory_max,omitempty"`
	SwapMax   *int64 `json:"swap_max,omitempty"`
	CPUQuota  int64  `json:"cpu_quota,omitempty"`
	CPUPeriod int64  `json:"cpu_period,omitempty"`
}

type CgroupUsage struct {
	MemoryCurrent int64 `json:"memory_current"`
	MemoryPeak    int64 `json:"memory_peak,omitempty"`
	CPUUsageUsec  int64 `json:"cpu_usage_usec"`
	UserUsec      int64 `json:"user_usec"`
	SystemUsec    int64 `json:"system_usec"`
	NrPeriods     int64 `json:"nr_periods"`
	NrThrottled   int64 `json:"nr_throttled"`
	ThrottledUsec int64 `json:"throttled_usec"`
}

type MemoryEvents struct {
	Low          int64 `json:"low"`
	High         int64 `json:"high"`
	Max          int64 `json:"max"`
	OOM          int64 `json:"oom"`
	OOMKill      int64 `json:"oom_kill"`
	OOMGroupKill int64 `json:"oom_group_kill"`
}

type CgroupSubtree struct {
	id         string
	path       string
	parentPath string
	limits     CgroupLimits
	dirFile    *os.File
	mu         sync.Mutex
	closed     bool
}

func SetCgroupRootOverride(root string) {
	cgroupRootMu.Lock()
	defer cgroupRootMu.Unlock()
	cgroupRootOverride = root
}

func SetCgroupParentOverride(parent string) {
	cgroupRootMu.Lock()
	defer cgroupRootMu.Unlock()
	cgroupParentOverride = parent
}

func ResetCgroupOverrides() {
	cgroupRootMu.Lock()
	defer cgroupRootMu.Unlock()
	cgroupRootOverride = ""
	cgroupParentOverride = ""
}

func getCgroupRoot() string {
	cgroupRootMu.RLock()
	defer cgroupRootMu.RUnlock()
	if cgroupRootOverride != "" {
		return cgroupRootOverride
	}
	return cgroupV2DefaultMount
}

func getCgroupParent() string {
	cgroupRootMu.RLock()
	defer cgroupRootMu.RUnlock()
	return cgroupParentOverride
}

func IsCgroupV2Supported() bool {
	root := getCgroupRoot()
	ctrlPath := filepath.Join(root, "cgroup.controllers")
	_, err := os.Stat(ctrlPath)
	return err == nil
}

func isWritableDirectory(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	if err := syscall.Access(dir, 2); err != nil {
		return false
	}
	testSub := filepath.Join(dir, fmt.Sprintf(".cg_test_%d", time.Now().UnixNano()))
	if err := os.Mkdir(testSub, 0755); err != nil {
		return false
	}
	_ = os.Remove(testSub)
	return true
}

func FindSubtreeParent(root string) (string, error) {
	if p := getCgroupParent(); p != "" {
		return p, nil
	}
	if root == "" {
		root = getCgroupRoot()
	}

	target := filepath.Join(root, defaultSubtreeName)
	if isWritableDirectory(root) || isWritableDirectory(target) {
		return target, nil
	}

	procCgroup := "/proc/self/cgroup"
	data, err := os.ReadFile(procCgroup)
	if err == nil {
		var cgRel string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "0::") {
				cgRel = strings.TrimPrefix(line, "0::")
				break
			}
		}
		if cgRel != "" {
			cur := filepath.Join(root, cgRel)
			for cur != root && cur != "/" && cur != "." {
				parent := filepath.Dir(cur)
				candidate := filepath.Join(parent, defaultSubtreeName)
				if isWritableDirectory(parent) || isWritableDirectory(candidate) {
					return candidate, nil
				}
				cur = parent
			}
		}
	}

	return target, nil
}

func EnableControllers(parentDir string) error {
	controllersPath := filepath.Join(parentDir, "cgroup.controllers")
	data, err := os.ReadFile(controllersPath)
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil
	}
	var enables []string
	for _, f := range fields {
		if f == "cpu" || f == "memory" || f == "pids" {
			enables = append(enables, "+"+f)
		}
	}
	if len(enables) == 0 {
		return nil
	}
	subtreeCtrl := filepath.Join(parentDir, "cgroup.subtree_control")
	return os.WriteFile(subtreeCtrl, []byte(strings.Join(enables, " ")+"\n"), 0644)
}

func NewCgroupSubtree(id string, limits CgroupLimits, parentDir string) (*CgroupSubtree, error) {
	if id == "" {
		id = fmt.Sprintf("codexkit-agent-58-%d", time.Now().UnixNano())
	}
	if parentDir == "" {
		p, err := FindSubtreeParent("")
		if err != nil {
			return nil, fmt.Errorf("find cgroup subtree parent: %w", err)
		}
		parentDir = p
	}

	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup parent %s: %w", parentDir, err)
	}

	_ = EnableControllers(parentDir)

	childPath := filepath.Join(parentDir, id)
	if err := os.MkdirAll(childPath, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup child %s: %w", childPath, err)
	}

	cs := &CgroupSubtree{
		id:         id,
		path:       childPath,
		parentPath: parentDir,
		limits:     limits,
	}

	if err := cs.SetLimits(limits); err != nil {
		_ = os.Remove(childPath)
		return nil, fmt.Errorf("set limits: %w", err)
	}

	if err := cs.VerifyLimitEnforcement(); err != nil {
		_ = os.Remove(childPath)
		return nil, fmt.Errorf("verify limit enforcement: %w", err)
	}

	return cs, nil
}

func (cs *CgroupSubtree) ID() string {
	return cs.id
}

func (cs *CgroupSubtree) Path() string {
	return cs.path
}

func (cs *CgroupSubtree) ParentPath() string {
	return cs.parentPath
}

func (cs *CgroupSubtree) Limits() CgroupLimits {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.limits
}

func (cs *CgroupSubtree) SetMemoryMax(bytes int64) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return errors.New("cgroup subtree is closed")
	}
	content := UnlimitedValue
	if bytes > 0 {
		content = strconv.FormatInt(bytes, 10)
	}
	memPath := filepath.Join(cs.path, "memory.max")
	if err := os.WriteFile(memPath, []byte(content+"\n"), 0644); err != nil {
		return fmt.Errorf("write memory.max: %w", err)
	}
	cs.limits.MemoryMax = bytes
	return nil
}

func (cs *CgroupSubtree) SetSwapMax(bytes int64) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return errors.New("cgroup subtree is closed")
	}
	content := UnlimitedValue
	if bytes >= 0 {
		content = strconv.FormatInt(bytes, 10)
	}
	swapPath := filepath.Join(cs.path, "memory.swap.max")
	if err := os.WriteFile(swapPath, []byte(content+"\n"), 0644); err != nil {
		return fmt.Errorf("write memory.swap.max: %w", err)
	}
	b := bytes
	cs.limits.SwapMax = &b
	return nil
}

func (cs *CgroupSubtree) SetCPUMax(quota int64, period int64) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return errors.New("cgroup subtree is closed")
	}
	if period <= 0 {
		period = DefaultCPUPeriod
	}
	var content string
	if quota > 0 {
		content = fmt.Sprintf("%d %d", quota, period)
	} else {
		content = fmt.Sprintf("%s %d", UnlimitedValue, period)
	}
	cpuPath := filepath.Join(cs.path, "cpu.max")
	if err := os.WriteFile(cpuPath, []byte(content+"\n"), 0644); err != nil {
		return fmt.Errorf("write cpu.max: %w", err)
	}
	cs.limits.CPUQuota = quota
	cs.limits.CPUPeriod = period
	return nil
}

func (cs *CgroupSubtree) SetLimits(limits CgroupLimits) error {
	if err := cs.SetMemoryMax(limits.MemoryMax); err != nil {
		return err
	}
	if limits.SwapMax != nil {
		_ = cs.SetSwapMax(*limits.SwapMax)
	}
	if err := cs.SetCPUMax(limits.CPUQuota, limits.CPUPeriod); err != nil {
		return err
	}
	return nil
}

func (cs *CgroupSubtree) GetMemoryMax() (int64, error) {
	data, err := os.ReadFile(filepath.Join(cs.path, "memory.max"))
	if err != nil {
		return 0, fmt.Errorf("read memory.max: %w", err)
	}
	valStr := strings.TrimSpace(string(data))
	if valStr == UnlimitedValue {
		return -1, nil
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse memory.max: %w", err)
	}
	return val, nil
}

func (cs *CgroupSubtree) GetCPUMax() (int64, int64, error) {
	data, err := os.ReadFile(filepath.Join(cs.path, "cpu.max"))
	if err != nil {
		return 0, 0, fmt.Errorf("read cpu.max: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) == 0 {
		return 0, 0, errors.New("empty cpu.max")
	}
	var quota int64 = -1
	if parts[0] != UnlimitedValue {
		q, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse cpu.max quota: %w", err)
		}
		quota = q
	}
	var period int64 = DefaultCPUPeriod
	if len(parts) >= 2 {
		p, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil && p > 0 {
			period = p
		}
	}
	return quota, period, nil
}

func (cs *CgroupSubtree) VerifyLimitEnforcement() error {
	cs.mu.Lock()
	expectedLimits := cs.limits
	cs.mu.Unlock()

	mem, err := cs.GetMemoryMax()
	if err != nil {
		return fmt.Errorf("verify memory limit: %w", err)
	}
	if expectedLimits.MemoryMax > 0 {
		if mem != expectedLimits.MemoryMax {
			return fmt.Errorf("memory.max mismatch: expected %d, got %d", expectedLimits.MemoryMax, mem)
		}
	} else {
		if mem != -1 {
			return fmt.Errorf("memory.max mismatch: expected %s, got %d", UnlimitedValue, mem)
		}
	}

	quota, period, err := cs.GetCPUMax()
	if err != nil {
		return fmt.Errorf("verify cpu limit: %w", err)
	}
	if expectedLimits.CPUQuota > 0 {
		if quota != expectedLimits.CPUQuota {
			return fmt.Errorf("cpu.max quota mismatch: expected %d, got %d", expectedLimits.CPUQuota, quota)
		}
	} else {
		if quota != -1 {
			return fmt.Errorf("cpu.max quota mismatch: expected %s, got %d", UnlimitedValue, quota)
		}
	}
	expectedPeriod := expectedLimits.CPUPeriod
	if expectedPeriod <= 0 {
		expectedPeriod = DefaultCPUPeriod
	}
	if period != expectedPeriod {
		return fmt.Errorf("cpu.max period mismatch: expected %d, got %d", expectedPeriod, period)
	}

	return nil
}

func (cs *CgroupSubtree) AddPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	procsPath := filepath.Join(cs.path, "cgroup.procs")
	return os.WriteFile(procsPath, []byte(strconv.Itoa(pid)+"\n"), 0644)
}

func (cs *CgroupSubtree) ReadPIDs() ([]int, error) {
	data, err := os.ReadFile(filepath.Join(cs.path, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func (cs *CgroupSubtree) ReadUsage() (*CgroupUsage, error) {
	usage := &CgroupUsage{}
	memData, err := os.ReadFile(filepath.Join(cs.path, "memory.current"))
	if err == nil {
		val, parseErr := strconv.ParseInt(strings.TrimSpace(string(memData)), 10, 64)
		if parseErr == nil {
			usage.MemoryCurrent = val
		}
	}
	peakData, err := os.ReadFile(filepath.Join(cs.path, "memory.peak"))
	if err == nil {
		val, parseErr := strconv.ParseInt(strings.TrimSpace(string(peakData)), 10, 64)
		if parseErr == nil {
			usage.MemoryPeak = val
		}
	}
	cpuData, err := os.ReadFile(filepath.Join(cs.path, "cpu.stat"))
	if err == nil {
		parsed := ParseCPUStat(string(cpuData))
		usage.CPUUsageUsec = parsed.CPUUsageUsec
		usage.UserUsec = parsed.UserUsec
		usage.SystemUsec = parsed.SystemUsec
		usage.NrPeriods = parsed.NrPeriods
		usage.NrThrottled = parsed.NrThrottled
		usage.ThrottledUsec = parsed.ThrottledUsec
	}
	return usage, nil
}

func (cs *CgroupSubtree) ReadMemoryEvents() (*MemoryEvents, error) {
	data, err := os.ReadFile(filepath.Join(cs.path, "memory.events"))
	if err != nil {
		return nil, err
	}
	events := ParseMemoryEvents(string(data))
	return &events, nil
}

func (cs *CgroupSubtree) AttachCmd(cmd *exec.Cmd) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return errors.New("cgroup subtree is closed")
	}
	if cs.dirFile == nil {
		f, err := os.Open(cs.path)
		if err != nil {
			return fmt.Errorf("open cgroup dir: %w", err)
		}
		cs.dirFile = f
	}
	return attachCgroupFD(cmd, cs.dirFile)
}

func (cs *CgroupSubtree) Kill() error {
	killPath := filepath.Join(cs.path, "cgroup.kill")
	if _, err := os.Stat(killPath); err == nil {
		if err := os.WriteFile(killPath, []byte("1\n"), 0644); err == nil {
			return nil
		}
	}
	pids, err := cs.ReadPIDs()
	if err != nil {
		return err
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

func (cs *CgroupSubtree) Close() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.closed {
		return nil
	}
	cs.closed = true
	if cs.dirFile != nil {
		_ = cs.dirFile.Close()
		cs.dirFile = nil
	}
	_ = cs.Kill()
	for i := 0; i < 5; i++ {
		pids, _ := cs.ReadPIDs()
		if len(pids) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return os.Remove(cs.path)
}

func EnforceLimits(cgroupPath string, limits CgroupLimits) error {
	cs := &CgroupSubtree{
		path:   cgroupPath,
		limits: limits,
	}
	if err := cs.SetLimits(limits); err != nil {
		return err
	}
	return cs.VerifyLimitEnforcement()
}

func VerifyLimits(cgroupPath string, expected CgroupLimits) error {
	cs := &CgroupSubtree{
		path:   cgroupPath,
		limits: expected,
	}
	return cs.VerifyLimitEnforcement()
}

func ParseCPUStat(data string) CgroupUsage {
	var usage CgroupUsage
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "usage_usec":
			usage.CPUUsageUsec = val
		case "user_usec":
			usage.UserUsec = val
		case "system_usec":
			usage.SystemUsec = val
		case "nr_periods":
			usage.NrPeriods = val
		case "nr_throttled":
			usage.NrThrottled = val
		case "throttled_usec":
			usage.ThrottledUsec = val
		}
	}
	return usage
}

func ParseMemoryEvents(data string) MemoryEvents {
	var events MemoryEvents
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "low":
			events.Low = val
		case "high":
			events.High = val
		case "max":
			events.Max = val
		case "oom":
			events.OOM = val
		case "oom_kill":
			events.OOMKill = val
		case "oom_group_kill":
			events.OOMGroupKill = val
		}
	}
	return events
}
