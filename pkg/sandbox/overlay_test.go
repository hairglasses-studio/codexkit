package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOverlayConfig_Validation(t *testing.T) {
	cfg := OverlayConfig{}
	_, err := NewOverlayManager(cfg)
	if err == nil {
		t.Fatalf("expected error with empty LowerDirs, got nil")
	}

	cfg.LowerDirs = []string{t.TempDir()}
	mgr, err := NewOverlayManager(cfg)
	if err != nil {
		t.Fatalf("unexpected error with valid LowerDirs: %v", err)
	}
	defer mgr.Close()
}

func TestOverlay_UpperWorkDirCreation(t *testing.T) {
	lowerDir := t.TempDir()
	cfg := DefaultOverlayConfig(lowerDir)

	mgr, err := NewOverlayManager(cfg)
	if err != nil {
		t.Fatalf("NewOverlayManager failed: %v", err)
	}
	defer mgr.Close()

	if mgr.UpperDir() == "" {
		t.Fatalf("expected non-empty UpperDir")
	}
	if mgr.WorkDir() == "" {
		t.Fatalf("expected non-empty WorkDir")
	}
	if mgr.MergedDir() == "" {
		t.Fatalf("expected non-empty MergedDir")
	}
	if mgr.MountPoint() != mgr.MergedDir() {
		t.Fatalf("expected MountPoint to match MergedDir")
	}
	if mgr.BaseDir() == "" {
		t.Fatalf("expected non-empty BaseDir")
	}
	if len(mgr.LowerDirs()) != 1 || mgr.LowerDirs()[0] != lowerDir {
		t.Fatalf("unexpected LowerDirs: %v", mgr.LowerDirs())
	}
	if !strings.HasPrefix(mgr.InstanceID(), "codexkit-agent-52-") {
		t.Fatalf("unexpected InstanceID: %s", mgr.InstanceID())
	}

	for _, d := range []string{mgr.UpperDir(), mgr.WorkDir(), mgr.MergedDir()} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("dir %s does not exist: %v", d, err)
		}
		if !info.IsDir() {
			t.Fatalf("path %s is not a directory", d)
		}
	}

	testFile := filepath.Join(mgr.UpperDir(), "test.txt")
	if err := os.WriteFile(testFile, []byte("overlay-data"), 0644); err != nil {
		t.Fatalf("write file to upperdir: %v", err)
	}
	content, err := os.ReadFile(testFile)
	if err != nil || string(content) != "overlay-data" {
		t.Fatalf("unexpected upperdir file content: %s, err: %v", string(content), err)
	}
}

func TestOverlay_MountCommandConstruction(t *testing.T) {
	low1 := "/tmp/lower1"
	low2 := "/tmp/lower2"
	cfg := OverlayConfig{
		InstanceID:   "test-inst-52",
		BaseDir:      "/tmp/overlay-test-base",
		LowerDirs:    []string{low1, low2},
		UpperDir:     "/tmp/overlay-test-base/upper",
		WorkDir:      "/tmp/overlay-test-base/work",
		MergedDir:    "/tmp/overlay-test-base/merged",
		UserXattr:    true,
		ExtraOptions: []string{"index=off", "xino=on"},
	}

	mgr := &OverlayManager{
		cfg:       cfg,
		baseDir:   cfg.BaseDir,
		lowerDirs: cfg.LowerDirs,
		upperDir:  cfg.UpperDir,
		workDir:   cfg.WorkDir,
		mergedDir: cfg.MergedDir,
	}

	opts := mgr.MountOptions()
	expectedPrefix := "lowerdir=/tmp/lower1:/tmp/lower2,upperdir=/tmp/overlay-test-base/upper,workdir=/tmp/overlay-test-base/work,userxattr,index=off,xino=on"
	if opts != expectedPrefix {
		t.Fatalf("unexpected mount options: got %q, want %q", opts, expectedPrefix)
	}

	mountCmd := mgr.BuildMountCommand()
	if len(mountCmd) != 7 || mountCmd[0] != "mount" || mountCmd[1] != "-t" || mountCmd[2] != "overlay" || mountCmd[3] != "overlay" || mountCmd[4] != "-o" || mountCmd[5] != opts || mountCmd[6] != cfg.MergedDir {
		t.Fatalf("unexpected BuildMountCommand: %v", mountCmd)
	}

	if strings.Join(mgr.MountCommand(), " ") != strings.Join(mountCmd, " ") {
		t.Fatalf("MountCommand does not match BuildMountCommand")
	}

	umountCmd := mgr.BuildUmountCommand(false)
	if len(umountCmd) != 2 || umountCmd[0] != "umount" || umountCmd[1] != cfg.MergedDir {
		t.Fatalf("unexpected BuildUmountCommand: %v", umountCmd)
	}
	if strings.Join(mgr.UmountCommand(), " ") != strings.Join(umountCmd, " ") {
		t.Fatalf("UmountCommand does not match BuildUmountCommand")
	}

	lazyUmountCmd := mgr.BuildUmountCommand(true)
	if len(lazyUmountCmd) != 3 || lazyUmountCmd[0] != "umount" || lazyUmountCmd[1] != "-l" || lazyUmountCmd[2] != cfg.MergedDir {
		t.Fatalf("unexpected BuildUmountCommand(true): %v", lazyUmountCmd)
	}

	fuseCmd := mgr.BuildFuseOverlayfsCommand()
	if len(fuseCmd) != 4 || fuseCmd[0] != "fuse-overlayfs" || fuseCmd[1] != "-o" || fuseCmd[2] != opts || fuseCmd[3] != cfg.MergedDir {
		t.Fatalf("unexpected BuildFuseOverlayfsCommand: %v", fuseCmd)
	}
	if strings.Join(mgr.FuseMountCommand(), " ") != strings.Join(fuseCmd, " ") {
		t.Fatalf("FuseMountCommand does not match BuildFuseOverlayfsCommand")
	}

	unshareCmd := mgr.BuildUnshareMountCommand()
	if len(unshareCmd) != 11 || unshareCmd[0] != "unshare" || unshareCmd[1] != "--user" || unshareCmd[2] != "--map-root-user" || unshareCmd[3] != "--mount" {
		t.Fatalf("unexpected BuildUnshareMountCommand: %v", unshareCmd)
	}

	bwrapArgs := mgr.BuildBwrapArgs("/workspace")
	if len(bwrapArgs) != 3 || bwrapArgs[0] != "--bind" || bwrapArgs[1] != cfg.MergedDir || bwrapArgs[2] != "/workspace" {
		t.Fatalf("unexpected BuildBwrapArgs: %v", bwrapArgs)
	}

	bwrapDefault := mgr.BwrapArgs()
	if len(bwrapDefault) != 3 || bwrapDefault[0] != "--bind" || bwrapDefault[1] != cfg.MergedDir || bwrapDefault[2] != cfg.MergedDir {
		t.Fatalf("unexpected BwrapArgs default: %v", bwrapDefault)
	}

	asMount := mgr.AsMount("/container/dest")
	if asMount.Type != MountTypeBind || asMount.Source != cfg.MergedDir || asMount.Destination != "/container/dest" {
		t.Fatalf("unexpected AsMount: %+v", asMount)
	}
}

func TestOverlay_ReadOnlyMountCommand(t *testing.T) {
	low := "/tmp/ro-lower"
	cfg := OverlayConfig{
		LowerDirs: []string{low},
		ReadOnly:  true,
		MergedDir: "/tmp/ro-merged",
	}
	mgr := &OverlayManager{
		cfg:       cfg,
		lowerDirs: cfg.LowerDirs,
		mergedDir: cfg.MergedDir,
	}

	opts := mgr.MountOptions()
	expected := "lowerdir=/tmp/ro-lower"
	if opts != expected {
		t.Fatalf("unexpected read-only mount options: got %q, want %q", opts, expected)
	}

	bwrapArgs := mgr.BuildBwrapArgs("")
	if len(bwrapArgs) != 3 || bwrapArgs[0] != "--ro-bind" {
		t.Fatalf("unexpected bwrap args for read-only: %v", bwrapArgs)
	}

	asMount := mgr.AsMount("")
	if asMount.Type != MountTypeRoBind {
		t.Fatalf("unexpected AsMount type for read-only: %s", asMount.Type)
	}
}

func TestOverlay_TeardownCleanupSLA(t *testing.T) {
	requireBwrap(t)

	for i := 0; i < 5; i++ {
		lowerDir := t.TempDir()
		cfg := DefaultOverlayConfig(lowerDir)

		mgr, err := NewOverlayManager(cfg)
		if err != nil {
			t.Fatalf("iteration %d: NewOverlayManager failed: %v", i, err)
		}

		ephemDir := mgr.BaseDir()
		upperDir := mgr.UpperDir()
		workDir := mgr.WorkDir()
		mergedDir := mgr.MergedDir()

		for _, path := range []string{
			filepath.Join(upperDir, "file1.txt"),
			filepath.Join(workDir, "workfile.tmp"),
			filepath.Join(mergedDir, "dummy.txt"),
		} {
			if err := os.WriteFile(path, []byte("sla-test-data"), 0644); err != nil {
				t.Fatalf("iteration %d: write file: %v", i, err)
			}
		}

		tdDuration, err := mgr.Teardown()
		if err != nil {
			t.Fatalf("iteration %d: Teardown failed: %v", i, err)
		}

		if tdDuration > 50*time.Millisecond {
			t.Fatalf("iteration %d: teardown SLA violated: %v > 50ms", i, tdDuration)
		}

		if mgr.TeardownLatency() != tdDuration {
			t.Fatalf("iteration %d: TeardownLatency mismatch: got %v, want %v", i, mgr.TeardownLatency(), tdDuration)
		}

		for _, dir := range []string{ephemDir, upperDir, workDir, mergedDir} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("iteration %d: dir %s still exists after teardown", i, dir)
			}
		}

		secondTd, err := mgr.Teardown()
		if err != nil {
			t.Fatalf("iteration %d: second Teardown failed: %v", i, err)
		}
		if secondTd != tdDuration {
			t.Fatalf("iteration %d: second Teardown returned different latency", i)
		}

		if err := mgr.Close(); err != nil {
			t.Fatalf("iteration %d: Close failed: %v", i, err)
		}
		if err := mgr.Cleanup(); err != nil {
			t.Fatalf("iteration %d: Cleanup failed: %v", i, err)
		}
	}
}

func TestOverlay_CustomBaseDirTeardown(t *testing.T) {
	baseDir := t.TempDir()
	lowerDir := t.TempDir()

	cfg := OverlayConfig{
		BaseDir:   baseDir,
		LowerDirs: []string{lowerDir},
	}

	mgr, err := NewOverlayManager(cfg)
	if err != nil {
		t.Fatalf("NewOverlayManager failed: %v", err)
	}

	upperDir := mgr.UpperDir()
	workDir := mgr.WorkDir()
	mergedDir := mgr.MergedDir()

	tdDuration, err := mgr.Teardown()
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if tdDuration > 50*time.Millisecond {
		t.Fatalf("teardown SLA violated: %v > 50ms", tdDuration)
	}

	for _, dir := range []string{upperDir, workDir, mergedDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("dir %s still exists after teardown", dir)
		}
	}

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		t.Fatalf("custom baseDir %s should not be removed if not ephemeral", baseDir)
	}
}

func TestOverlay_MountedTeardown(t *testing.T) {
	lowerDir := t.TempDir()
	cfg := DefaultOverlayConfig(lowerDir)

	mgr, err := NewOverlayManager(cfg)
	if err != nil {
		t.Fatalf("NewOverlayManager failed: %v", err)
	}

	mgr.SetMounted(true)
	if !mgr.IsMounted() {
		t.Fatalf("expected IsMounted to be true")
	}

	tdDuration, err := mgr.Teardown()
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if tdDuration > 50*time.Millisecond {
		t.Fatalf("teardown SLA violated: %v > 50ms", tdDuration)
	}
	if mgr.IsMounted() {
		t.Fatalf("expected IsMounted to be false after Teardown")
	}
}

func TestOverlay_LiveNamespaceMount(t *testing.T) {
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare not available")
	}

	lowerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lowerDir, "base.txt"), []byte("base-content"), 0644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}

	cfg := DefaultOverlayConfig(lowerDir)
	mgr, err := NewOverlayManager(cfg)
	if err != nil {
		t.Fatalf("NewOverlayManager failed: %v", err)
	}
	defer mgr.Close()

	script := strings.Join([]string{
		strings.Join(mgr.MountCommand(), " "),
		"echo -n 'modified' > " + filepath.Join(mgr.MergedDir(), "base.txt"),
		"echo -n 'created' > " + filepath.Join(mgr.MergedDir(), "new.txt"),
		mgr.UmountCommand()[0] + " " + mgr.UmountCommand()[1],
	}, " && ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--mount", "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("unshare mount not permitted or failed: %v, out: %s", err, string(out))
	}

	upperBase := filepath.Join(mgr.UpperDir(), "base.txt")
	upperNew := filepath.Join(mgr.UpperDir(), "new.txt")

	upBaseContent, err := os.ReadFile(upperBase)
	if err != nil || string(upBaseContent) != "modified" {
		t.Fatalf("expected upperdir base.txt to be 'modified', got: %s, err: %v", string(upBaseContent), err)
	}

	upNewContent, err := os.ReadFile(upperNew)
	if err != nil || string(upNewContent) != "created" {
		t.Fatalf("expected upperdir new.txt to be 'created', got: %s, err: %v", string(upNewContent), err)
	}

	lowerContent, err := os.ReadFile(filepath.Join(lowerDir, "base.txt"))
	if err != nil || string(lowerContent) != "base-content" {
		t.Fatalf("expected lowerdir base.txt to be unmodified 'base-content', got: %s, err: %v", string(lowerContent), err)
	}
}
