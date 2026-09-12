package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"time"
)

type RootMode string

const (
	RootModeHostRO  RootMode = "host-ro"
	RootModeMinimal RootMode = "minimal"
)

type MountType string

const (
	MountTypeRoBind MountType = "ro-bind"
	MountTypeBind   MountType = "bind"
	MountTypeTmpfs  MountType = "tmpfs"
	MountTypeDev    MountType = "dev"
	MountTypeProc   MountType = "proc"
)

type Mount struct {
	Type        MountType `json:"type"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
}

type Config struct {
	InstanceID             string            `json:"instance_id"`
	Hostname               string            `json:"hostname"`
	BwrapPath              string            `json:"bwrap_path"`
	RootMode               RootMode          `json:"root_mode"`
	HostWorkDir            string            `json:"host_work_dir"`
	ContainerWorkDir       string            `json:"container_work_dir"`
	EphemeralWorkDir       bool              `json:"ephemeral_work_dir"`
	UnshareUser            bool              `json:"unshare_user"`
	UnsharePID             bool              `json:"unshare_pid"`
	UnshareIPC             bool              `json:"unshare_ipc"`
	UnshareNet             bool              `json:"unshare_net"`
	UnshareUTS             bool              `json:"unshare_uts"`
	UnshareCgroup          bool              `json:"unshare_cgroup"`
	UID                    uint32            `json:"uid"`
	GID                    uint32            `json:"gid"`
	DieWithParent          bool              `json:"die_with_parent"`
	NewSession             bool              `json:"new_session"`
	TmpfsPaths             []string          `json:"tmpfs_paths"`
	Mounts                 []Mount           `json:"mounts"`
	Env                    map[string]string `json:"env"`
	ClearEnv               bool              `json:"clear_env"`
	EnableSeccomp          bool              `json:"enable_seccomp"`
	DeniedSyscalls         []uint32          `json:"denied_syscalls"`
	ApprovedProxyLoopbacks []string          `json:"approved_proxy_loopbacks"`
	Timeout                time.Duration     `json:"timeout"`
}

func generateInstanceID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("codexkit-agent-P26-instance-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("codexkit-agent-P26-instance-%s", hex.EncodeToString(b))
}

func DefaultConfig() Config {
	bwrapPath, _ := exec.LookPath("bwrap")
	if bwrapPath == "" {
		bwrapPath = "/usr/bin/bwrap"
	}
	return Config{
		InstanceID:       generateInstanceID(),
		Hostname:         "codexkit-agent-P26",
		BwrapPath:        bwrapPath,
		RootMode:         RootModeHostRO,
		ContainerWorkDir: "",
		EphemeralWorkDir: true,
		UnshareUser:      true,
		UnsharePID:       true,
		UnshareIPC:       true,
		UnshareNet:       true,
		UnshareUTS:       true,
		UnshareCgroup:    true,
		UID:              0,
		GID:              0,
		DieWithParent:    true,
		NewSession:       true,
		TmpfsPaths:       []string{"/tmp", "/dev/shm"},
		Mounts:           nil,
		Env: map[string]string{
			"HOME": "/tmp",
			"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"TERM": "xterm-256color",
		},
		ClearEnv:               false,
		EnableSeccomp:          true,
		DeniedSyscalls:         DefaultDeniedSyscalls(),
		ApprovedProxyLoopbacks: nil,
		Timeout:                30 * time.Second,
	}
}

func DefaultEphemeralConfig(workDir string) Config {
	cfg := DefaultConfig()
	cfg.HostWorkDir = workDir
	cfg.EphemeralWorkDir = (workDir == "")
	return cfg
}
