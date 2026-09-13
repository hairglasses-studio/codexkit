package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type OverlayConfig struct {
	InstanceID       string   `json:"instance_id"`
	BaseDir          string   `json:"base_dir"`
	LowerDirs        []string `json:"lower_dirs"`
	UpperDir         string   `json:"upper_dir"`
	WorkDir          string   `json:"work_dir"`
	MergedDir        string   `json:"merged_dir"`
	MountPoint       string   `json:"mount_point"`
	ReadOnly         bool     `json:"read_only"`
	UserXattr        bool     `json:"user_xattr"`
	ExtraOptions     []string `json:"extra_options"`
	EphemeralWorkDir bool     `json:"ephemeral_work_dir"`
}

type OverlayManager struct {
	cfg             OverlayConfig
	baseDir         string
	ephemeralDir    string
	lowerDirs       []string
	upperDir        string
	workDir         string
	mergedDir       string
	isMounted       bool
	mu              sync.Mutex
	teardownDone    bool
	teardownLatency time.Duration
}

func DefaultOverlayConfig(lowerDirs ...string) OverlayConfig {
	return OverlayConfig{
		InstanceID:       generateOverlayID(),
		LowerDirs:        lowerDirs,
		EphemeralWorkDir: true,
	}
}

func generateOverlayID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("codexkit-agent-52-instance-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("codexkit-agent-52-instance-%s", hex.EncodeToString(b))
}

func NewOverlayManager(cfg OverlayConfig) (*OverlayManager, error) {
	if len(cfg.LowerDirs) == 0 {
		return nil, errors.New("at least one lowerdir is required")
	}

	if cfg.InstanceID == "" {
		cfg.InstanceID = generateOverlayID()
	}

	var ephemeralDir string
	baseDir := cfg.BaseDir
	if baseDir == "" {
		d, err := os.MkdirTemp("", fmt.Sprintf("%s-", cfg.InstanceID))
		if err != nil {
			return nil, fmt.Errorf("create ephemeral base dir: %w", err)
		}
		ephemeralDir = d
		baseDir = d
	} else {
		if _, err := os.Stat(baseDir); os.IsNotExist(err) {
			if err := os.MkdirAll(baseDir, 0755); err != nil {
				return nil, fmt.Errorf("create base dir: %w", err)
			}
		}
	}

	upperDir := cfg.UpperDir
	if upperDir == "" && !cfg.ReadOnly {
		upperDir = filepath.Join(baseDir, "upper")
	}

	workDir := cfg.WorkDir
	if workDir == "" && !cfg.ReadOnly {
		workDir = filepath.Join(baseDir, "work")
	}

	mergedDir := cfg.MergedDir
	if mergedDir == "" {
		if cfg.MountPoint != "" {
			mergedDir = cfg.MountPoint
		} else {
			mergedDir = filepath.Join(baseDir, "merged")
		}
	}

	m := &OverlayManager{
		cfg:          cfg,
		baseDir:      baseDir,
		ephemeralDir: ephemeralDir,
		lowerDirs:    append([]string(nil), cfg.LowerDirs...),
		upperDir:     upperDir,
		workDir:      workDir,
		mergedDir:    mergedDir,
	}

	if err := m.CreateDirs(); err != nil {
		_, _ = m.Teardown()
		return nil, err
	}

	return m, nil
}

func NewOverlay(cfg OverlayConfig) (*OverlayManager, error) {
	return NewOverlayManager(cfg)
}

func (m *OverlayManager) CreateDirs() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.upperDir != "" {
		if err := os.MkdirAll(m.upperDir, 0755); err != nil {
			return fmt.Errorf("create upperdir %s: %w", m.upperDir, err)
		}
	}

	if m.workDir != "" {
		if err := os.MkdirAll(m.workDir, 0755); err != nil {
			return fmt.Errorf("create workdir %s: %w", m.workDir, err)
		}
	}

	if m.mergedDir != "" {
		if err := os.MkdirAll(m.mergedDir, 0755); err != nil {
			return fmt.Errorf("create mergeddir %s: %w", m.mergedDir, err)
		}
	}

	return nil
}

func (m *OverlayManager) InstanceID() string {
	return m.cfg.InstanceID
}

func (m *OverlayManager) BaseDir() string {
	return m.baseDir
}

func (m *OverlayManager) LowerDirs() []string {
	return append([]string(nil), m.lowerDirs...)
}

func (m *OverlayManager) UpperDir() string {
	return m.upperDir
}

func (m *OverlayManager) WorkDir() string {
	return m.workDir
}

func (m *OverlayManager) MergedDir() string {
	return m.mergedDir
}

func (m *OverlayManager) MountPoint() string {
	return m.mergedDir
}

func (m *OverlayManager) IsMounted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isMounted
}

func (m *OverlayManager) SetMounted(mounted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isMounted = mounted
}

func (m *OverlayManager) TeardownLatency() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.teardownLatency
}

func (m *OverlayManager) MountOptions() string {
	var opts []string
	opts = append(opts, fmt.Sprintf("lowerdir=%s", strings.Join(m.lowerDirs, ":")))

	if !m.cfg.ReadOnly && m.upperDir != "" && m.workDir != "" {
		opts = append(opts, fmt.Sprintf("upperdir=%s", m.upperDir))
		opts = append(opts, fmt.Sprintf("workdir=%s", m.workDir))
	}

	if m.cfg.UserXattr {
		opts = append(opts, "userxattr")
	}

	for _, opt := range m.cfg.ExtraOptions {
		if trimmed := strings.TrimSpace(opt); trimmed != "" {
			opts = append(opts, trimmed)
		}
	}

	return strings.Join(opts, ",")
}

func (m *OverlayManager) BuildMountCommand() []string {
	return []string{
		"mount",
		"-t", "overlay",
		"overlay",
		"-o", m.MountOptions(),
		m.mergedDir,
	}
}

func (m *OverlayManager) MountCommand() []string {
	return m.BuildMountCommand()
}

func (m *OverlayManager) BuildUmountCommand(lazy bool) []string {
	if lazy {
		return []string{"umount", "-l", m.mergedDir}
	}
	return []string{"umount", m.mergedDir}
}

func (m *OverlayManager) UmountCommand() []string {
	return m.BuildUmountCommand(false)
}

func (m *OverlayManager) BuildFuseOverlayfsCommand() []string {
	return []string{
		"fuse-overlayfs",
		"-o", m.MountOptions(),
		m.mergedDir,
	}
}

func (m *OverlayManager) FuseMountCommand() []string {
	return m.BuildFuseOverlayfsCommand()
}

func (m *OverlayManager) BuildUnshareMountCommand() []string {
	cmd := []string{"unshare", "--user", "--map-root-user", "--mount"}
	cmd = append(cmd, m.BuildMountCommand()...)
	return cmd
}

func (m *OverlayManager) BuildBwrapArgs(containerTarget string) []string {
	if containerTarget == "" {
		containerTarget = m.mergedDir
	}
	if m.cfg.ReadOnly {
		return []string{"--ro-bind", m.mergedDir, containerTarget}
	}
	return []string{"--bind", m.mergedDir, containerTarget}
}

func (m *OverlayManager) BwrapArgs() []string {
	return m.BuildBwrapArgs("")
}

func (m *OverlayManager) AsMount(containerPath string) Mount {
	if containerPath == "" {
		containerPath = m.mergedDir
	}
	mType := MountTypeBind
	if m.cfg.ReadOnly {
		mType = MountTypeRoBind
	}
	return Mount{
		Type:        mType,
		Source:      m.mergedDir,
		Destination: containerPath,
	}
}

func (m *OverlayManager) Mount(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isMounted {
		return errors.New("overlay already mounted")
	}

	cmdArgs := m.BuildMountCommand()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mount overlay: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	m.isMounted = true
	return nil
}

func (m *OverlayManager) Unmount(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isMounted {
		return nil
	}

	cmdArgs := m.BuildUmountCommand(false)
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		lazyArgs := m.BuildUmountCommand(true)
		lazyCmd := exec.CommandContext(ctx, lazyArgs[0], lazyArgs[1:]...)
		if lazyErr := lazyCmd.Run(); lazyErr != nil {
			return fmt.Errorf("unmount overlay: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}

	m.isMounted = false
	return nil
}

func isMountPoint(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

func (m *OverlayManager) Teardown() (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.teardownDone {
		return m.teardownLatency, nil
	}

	start := time.Now()
	var errs []error

	if m.isMounted || isMountPoint(m.mergedDir) {
		if isMountPoint(m.mergedDir) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := exec.CommandContext(ctx, "umount", m.mergedDir).Run(); err != nil {
				_ = exec.CommandContext(ctx, "umount", "-l", m.mergedDir).Run()
			}
			cancel()
		}
		m.isMounted = false
	}

	if m.ephemeralDir != "" {
		if err := os.RemoveAll(m.ephemeralDir); err != nil {
			errs = append(errs, err)
		}
	} else {
		if m.upperDir != "" {
			if err := os.RemoveAll(m.upperDir); err != nil {
				errs = append(errs, err)
			}
		}
		if m.workDir != "" {
			if err := os.RemoveAll(m.workDir); err != nil {
				errs = append(errs, err)
			}
		}
		if m.mergedDir != "" {
			if err := os.RemoveAll(m.mergedDir); err != nil {
				errs = append(errs, err)
			}
		}
	}

	m.teardownDone = true
	m.teardownLatency = time.Since(start)

	var retErr error
	if len(errs) > 0 {
		retErr = errors.Join(errs...)
	}

	if m.teardownLatency > 50*time.Millisecond {
		return m.teardownLatency, fmt.Errorf("teardown SLA violated: latency %v exceeded 50ms ceiling", m.teardownLatency)
	}

	return m.teardownLatency, retErr
}

func (m *OverlayManager) Close() error {
	_, err := m.Teardown()
	return err
}

func (m *OverlayManager) Cleanup() error {
	_, err := m.Teardown()
	return err
}
