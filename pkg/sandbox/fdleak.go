package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	Agent56ID       = 56
	Agent56Name     = "Agent 56: Bind mount file descriptor leak detector"
	FDAgentID       = 56
	FDAgentName     = "Agent 56: Bind mount file descriptor leak detector"
	DefaultProcPath = "/proc/self/fd"
	StdIn           = 0
	StdOut          = 1
	StdErr          = 2
)

type DescriptorType string

const (
	DescTypeFile    DescriptorType = "file"
	DescTypeDir     DescriptorType = "dir"
	DescTypePipe    DescriptorType = "pipe"
	DescTypeSocket  DescriptorType = "socket"
	DescTypeAnon    DescriptorType = "anon"
	DescTypeDevice  DescriptorType = "device"
	DescTypeUnknown DescriptorType = "unknown"
)

type Descriptor struct {
	FD          int            `json:"fd"`
	Target      string         `json:"target"`
	Type        DescriptorType `json:"type"`
	IsStd       bool           `json:"is_std"`
	IsMountPath bool           `json:"is_mount_path,omitempty"`
	MountPath   string         `json:"mount_path,omitempty"`
}

type LeakReport struct {
	AgentID        int          `json:"agent_id"`
	AgentName      string       `json:"agent_name"`
	ProcPath       string       `json:"proc_path"`
	TotalOpen      int          `json:"total_open"`
	StandardCount  int          `json:"standard_count"`
	UnclosedCount  int          `json:"unclosed_count"`
	MountLeakCount int          `json:"mount_leak_count"`
	HasLeaks       bool         `json:"has_leaks"`
	Descriptors    []Descriptor `json:"descriptors"`
	UnclosedFDs    []Descriptor `json:"unclosed_fds"`
	MountLeaks     []Descriptor `json:"mount_leaks"`
}

func (r *LeakReport) CloseLeaks() []error {
	var errs []error
	for _, desc := range r.UnclosedFDs {
		if desc.IsStd {
			continue
		}
		if err := syscall.Close(desc.FD); err != nil {
			errs = append(errs, fmt.Errorf("close fd %d: %w", desc.FD, err))
		}
	}
	return errs
}

func (r *LeakReport) FDNumbers() []int {
	nums := make([]int, len(r.UnclosedFDs))
	for i, d := range r.UnclosedFDs {
		nums[i] = d.FD
	}
	return nums
}

func (r *LeakReport) MountLeakPaths() []string {
	paths := make([]string, len(r.MountLeaks))
	for i, d := range r.MountLeaks {
		paths[i] = d.Target
	}
	return paths
}

type DetectorConfig struct {
	ProcPath        string   `json:"proc_path"`
	IgnoredFDs      []int    `json:"ignored_fds"`
	MountPaths      []string `json:"mount_paths"`
	IgnoreSelf      bool     `json:"ignore_self"`
	IgnoreSockets   bool     `json:"ignore_sockets"`
	IgnorePipes     bool     `json:"ignore_pipes"`
	IgnoreAnonInode bool     `json:"ignore_anon_inode"`
}

type Detector struct {
	cfg DetectorConfig
}

type DetectorOption func(*Detector)

func WithProcPath(path string) DetectorOption {
	return func(d *Detector) {
		d.cfg.ProcPath = path
	}
}

func WithIgnoredFDs(fds ...int) DetectorOption {
	return func(d *Detector) {
		d.cfg.IgnoredFDs = append(d.cfg.IgnoredFDs, fds...)
	}
}

func WithMountPaths(paths ...string) DetectorOption {
	return func(d *Detector) {
		d.cfg.MountPaths = append(d.cfg.MountPaths, paths...)
	}
}

func WithMounts(mounts []Mount) DetectorOption {
	return func(d *Detector) {
		for _, m := range mounts {
			if m.Source != "" {
				d.cfg.MountPaths = append(d.cfg.MountPaths, m.Source)
			}
			if m.Destination != "" {
				d.cfg.MountPaths = append(d.cfg.MountPaths, m.Destination)
			}
		}
	}
}

func WithIgnoreSelf(ignore bool) DetectorOption {
	return func(d *Detector) {
		d.cfg.IgnoreSelf = ignore
	}
}

func WithIgnoreSockets(ignore bool) DetectorOption {
	return func(d *Detector) {
		d.cfg.IgnoreSockets = ignore
	}
}

func WithIgnorePipes(ignore bool) DetectorOption {
	return func(d *Detector) {
		d.cfg.IgnorePipes = ignore
	}
}

func WithIgnoreAnonInode(ignore bool) DetectorOption {
	return func(d *Detector) {
		d.cfg.IgnoreAnonInode = ignore
	}
}

func DefaultDetectorConfig() DetectorConfig {
	return DetectorConfig{
		ProcPath:   DefaultProcPath,
		IgnoreSelf: true,
	}
}

func NewDetector(opts ...DetectorOption) *Detector {
	d := &Detector{
		cfg: DefaultDetectorConfig(),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *Detector) Config() DetectorConfig {
	return d.cfg
}

func isStandardFD(fd int) bool {
	return fd >= 0 && fd <= 2
}

func classifyDescriptor(target string) DescriptorType {
	if strings.HasPrefix(target, "pipe:[") {
		return DescTypePipe
	}
	if strings.HasPrefix(target, "socket:[") {
		return DescTypeSocket
	}
	if strings.HasPrefix(target, "anon_inode:[") {
		return DescTypeAnon
	}
	if strings.HasPrefix(target, "/dev/") {
		return DescTypeDevice
	}
	cleaned := strings.TrimSuffix(target, " (deleted)")
	if fi, err := os.Stat(cleaned); err == nil {
		if fi.IsDir() {
			return DescTypeDir
		}
		return DescTypeFile
	}
	if strings.HasPrefix(target, "/") {
		return DescTypeFile
	}
	return DescTypeUnknown
}

func matchesMountPath(target string, mountPaths []string) (bool, string) {
	if len(mountPaths) == 0 {
		return false, ""
	}
	cleaned := filepath.Clean(strings.TrimSuffix(target, " (deleted)"))
	for _, mPath := range mountPaths {
		if mPath == "" {
			continue
		}
		cleanMPath := filepath.Clean(mPath)
		if cleanMPath == "/" {
			if strings.HasPrefix(cleaned, "/") {
				return true, cleanMPath
			}
			continue
		}
		if cleaned == cleanMPath || strings.HasPrefix(cleaned, cleanMPath+string(filepath.Separator)) {
			return true, cleanMPath
		}
	}
	return false, ""
}

func (d *Detector) Enumerate() ([]Descriptor, error) {
	procPath := d.cfg.ProcPath
	if procPath == "" {
		procPath = DefaultProcPath
	}

	dir, err := os.Open(procPath)
	if err != nil {
		return nil, fmt.Errorf("open proc fd dir: %w", err)
	}
	defer dir.Close()

	selfFD := -1
	if d.cfg.IgnoreSelf {
		selfFD = int(dir.Fd())
	}

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("read proc fd dir: %w", err)
	}

	var descriptors []Descriptor
	for _, name := range names {
		fdNum, err := strconv.Atoi(name)
		if err != nil || fdNum < 0 {
			continue
		}
		if d.cfg.IgnoreSelf && fdNum == selfFD {
			continue
		}

		fdPath := filepath.Join(procPath, name)
		target, err := os.Readlink(fdPath)
		if err != nil {
			target = "unknown"
		}

		if d.cfg.IgnoreSelf && selfFD >= 0 {
			if strings.HasPrefix(target, "/proc/") && strings.HasSuffix(target, "/fd") {
				continue
			}
		}

		descType := classifyDescriptor(target)
		isStd := isStandardFD(fdNum)
		isMount, mountPath := matchesMountPath(target, d.cfg.MountPaths)

		descriptors = append(descriptors, Descriptor{
			FD:          fdNum,
			Target:      target,
			Type:        descType,
			IsStd:       isStd,
			IsMountPath: isMount,
			MountPath:   mountPath,
		})
	}

	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].FD < descriptors[j].FD
	})

	return descriptors, nil
}

func (d *Detector) Detect() (*LeakReport, error) {
	descriptors, err := d.Enumerate()
	if err != nil {
		return nil, err
	}

	ignoredMap := make(map[int]bool)
	for _, fd := range d.cfg.IgnoredFDs {
		ignoredMap[fd] = true
	}

	report := &LeakReport{
		AgentID:     Agent56ID,
		AgentName:   Agent56Name,
		ProcPath:    d.cfg.ProcPath,
		TotalOpen:   len(descriptors),
		Descriptors: descriptors,
	}

	for _, desc := range descriptors {
		if desc.IsStd {
			report.StandardCount++
			continue
		}

		if ignoredMap[desc.FD] {
			continue
		}

		if d.cfg.IgnoreSockets && desc.Type == DescTypeSocket {
			continue
		}
		if d.cfg.IgnorePipes && desc.Type == DescTypePipe {
			continue
		}
		if d.cfg.IgnoreAnonInode && desc.Type == DescTypeAnon {
			continue
		}

		report.UnclosedFDs = append(report.UnclosedFDs, desc)
		if desc.IsMountPath {
			report.MountLeaks = append(report.MountLeaks, desc)
		}
	}

	report.UnclosedCount = len(report.UnclosedFDs)
	report.MountLeakCount = len(report.MountLeaks)
	report.HasLeaks = report.UnclosedCount > 0

	return report, nil
}

type Snapshot struct {
	ProcPath    string             `json:"proc_path"`
	Descriptors map[int]Descriptor `json:"descriptors"`
	CapturedAt  time.Time          `json:"captured_at"`
}

func (s *Snapshot) HasFD(fd int) bool {
	_, ok := s.Descriptors[fd]
	return ok
}

func (s *Snapshot) Count() int {
	return len(s.Descriptors)
}

func (d *Detector) Snapshot() (*Snapshot, error) {
	descs, err := d.Enumerate()
	if err != nil {
		return nil, err
	}
	descMap := make(map[int]Descriptor, len(descs))
	for _, desc := range descs {
		descMap[desc.FD] = desc
	}
	return &Snapshot{
		ProcPath:    d.cfg.ProcPath,
		Descriptors: descMap,
		CapturedAt:  time.Now(),
	}, nil
}

func (s *Snapshot) DetectLeaks(d *Detector) (*LeakReport, error) {
	currentDescs, err := d.Enumerate()
	if err != nil {
		return nil, err
	}

	ignoredMap := make(map[int]bool)
	for _, fd := range d.cfg.IgnoredFDs {
		ignoredMap[fd] = true
	}

	report := &LeakReport{
		AgentID:     Agent56ID,
		AgentName:   Agent56Name,
		ProcPath:    d.cfg.ProcPath,
		TotalOpen:   len(currentDescs),
		Descriptors: currentDescs,
	}

	for _, desc := range currentDescs {
		if desc.IsStd {
			report.StandardCount++
			continue
		}
		if _, existed := s.Descriptors[desc.FD]; existed {
			continue
		}
		if ignoredMap[desc.FD] {
			continue
		}
		if d.cfg.IgnoreSockets && desc.Type == DescTypeSocket {
			continue
		}
		if d.cfg.IgnorePipes && desc.Type == DescTypePipe {
			continue
		}
		if d.cfg.IgnoreAnonInode && desc.Type == DescTypeAnon {
			continue
		}

		report.UnclosedFDs = append(report.UnclosedFDs, desc)
		if desc.IsMountPath {
			report.MountLeaks = append(report.MountLeaks, desc)
		}
	}

	report.UnclosedCount = len(report.UnclosedFDs)
	report.MountLeakCount = len(report.MountLeaks)
	report.HasLeaks = report.UnclosedCount > 0

	return report, nil
}

func (s *Snapshot) Diff(current []Descriptor) []Descriptor {
	var diff []Descriptor
	for _, desc := range current {
		if desc.IsStd {
			continue
		}
		if _, exists := s.Descriptors[desc.FD]; !exists {
			diff = append(diff, desc)
		}
	}
	return diff
}

func EnumerateOpenDescriptors() ([]Descriptor, error) {
	d := NewDetector()
	return d.Enumerate()
}

func DetectUnclosedDescriptors() ([]Descriptor, error) {
	d := NewDetector()
	rep, err := d.Detect()
	if err != nil {
		return nil, err
	}
	return rep.UnclosedFDs, nil
}

func DetectLeaks(mountPaths ...string) (*LeakReport, error) {
	d := NewDetector(WithMountPaths(mountPaths...))
	return d.Detect()
}

func DetectBindMountLeaks(mounts []Mount) (*LeakReport, error) {
	d := NewDetector(WithMounts(mounts))
	return d.Detect()
}

func TakeSnapshot() (*Snapshot, error) {
	d := NewDetector()
	return d.Snapshot()
}

func (s *Sandbox) DetectFDLeaks() (*LeakReport, error) {
	var mountPaths []string
	if s.cfg.HostWorkDir != "" {
		mountPaths = append(mountPaths, s.cfg.HostWorkDir)
	}
	for _, m := range s.cfg.Mounts {
		if m.Source != "" {
			mountPaths = append(mountPaths, m.Source)
		}
		if m.Destination != "" {
			mountPaths = append(mountPaths, m.Destination)
		}
	}
	for _, p := range s.cfg.TmpfsPaths {
		mountPaths = append(mountPaths, p)
	}
	d := NewDetector(WithMountPaths(mountPaths...))
	return d.Detect()
}

func (s *Sandbox) AuditFDLeaks(action func() error) (*LeakReport, error) {
	d := NewDetector(WithMounts(s.cfg.Mounts))
	if s.cfg.HostWorkDir != "" {
		d.cfg.MountPaths = append(d.cfg.MountPaths, s.cfg.HostWorkDir)
	}
	snap, err := d.Snapshot()
	if err != nil {
		return nil, err
	}
	if action != nil {
		if err := action(); err != nil {
			return nil, err
		}
	}
	return snap.DetectLeaks(d)
}
