package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAgent56Constants(t *testing.T) {
	if Agent56ID != 56 {
		t.Fatalf("expected Agent56ID 56, got %d", Agent56ID)
	}
	if FDAgentID != 56 {
		t.Fatalf("expected FDAgentID 56, got %d", FDAgentID)
	}
	expectedName := "Agent 56: Bind mount file descriptor leak detector"
	if Agent56Name != expectedName {
		t.Fatalf("expected Agent56Name %q, got %q", expectedName, Agent56Name)
	}
	if FDAgentName != expectedName {
		t.Fatalf("expected FDAgentName %q, got %q", expectedName, FDAgentName)
	}
}

func TestEnumerateProcSelfFD(t *testing.T) {
	requireBwrap(t)
	descs, err := EnumerateOpenDescriptors()
	if err != nil {
		t.Fatalf("EnumerateOpenDescriptors failed: %v", err)
	}
	if len(descs) == 0 {
		t.Fatalf("expected at least 1 open descriptor in proc self fd")
	}

	for i := 1; i < len(descs); i++ {
		if descs[i].FD <= descs[i-1].FD {
			t.Fatalf("descriptors not sorted by fd: %d <= %d", descs[i].FD, descs[i-1].FD)
		}
	}

	foundStd := false
	for _, d := range descs {
		if d.FD >= 0 && d.FD <= 2 {
			foundStd = true
			if !d.IsStd {
				t.Fatalf("descriptor %d marked IsStd=false", d.FD)
			}
		}
	}
	if !foundStd {
		t.Fatalf("expected at least one standard descriptor (0, 1, or 2)")
	}
}

func TestMockProcFDLeakDetection(t *testing.T) {
	mockDir := t.TempDir()

	mustSymlink := func(target, linkName string) {
		linkPath := filepath.Join(mockDir, linkName)
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatalf("create symlink %s -> %s: %v", linkPath, target, err)
		}
	}

	mustSymlink("/dev/null", "0")
	mustSymlink("/dev/pts/1", "1")
	mustSymlink("/dev/pts/1", "2")
	mustSymlink("/sandbox/mounts/work/file.go", "3")
	mustSymlink("pipe:[99999]", "4")
	mustSymlink("socket:[88888]", "5")
	mustSymlink("anon_inode:[eventpoll]", "6")
	mustSymlink("/unrelated/data.bin", "7")

	d := NewDetector(
		WithProcPath(mockDir),
		WithMountPaths("/sandbox/mounts/work"),
		WithIgnoreSelf(false),
	)

	rep, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	if rep.AgentID != Agent56ID {
		t.Fatalf("expected AgentID %d, got %d", Agent56ID, rep.AgentID)
	}
	if rep.TotalOpen != 8 {
		t.Fatalf("expected TotalOpen 8, got %d", rep.TotalOpen)
	}
	if rep.StandardCount != 3 {
		t.Fatalf("expected StandardCount 3, got %d", rep.StandardCount)
	}
	if rep.UnclosedCount != 5 {
		t.Fatalf("expected UnclosedCount 5, got %d", rep.UnclosedCount)
	}
	if rep.MountLeakCount != 1 {
		t.Fatalf("expected MountLeakCount 1, got %d", rep.MountLeakCount)
	}
	if !rep.HasLeaks {
		t.Fatalf("expected HasLeaks true")
	}

	leak := rep.MountLeaks[0]
	if leak.FD != 3 {
		t.Fatalf("expected mount leak FD 3, got %d", leak.FD)
	}
	if !leak.IsMountPath {
		t.Fatalf("expected IsMountPath true for FD 3")
	}
	if leak.MountPath != "/sandbox/mounts/work" {
		t.Fatalf("expected MountPath /sandbox/mounts/work, got %s", leak.MountPath)
	}

	for _, desc := range rep.Descriptors {
		switch desc.FD {
		case 4:
			if desc.Type != DescTypePipe {
				t.Fatalf("expected FD 4 type pipe, got %s", desc.Type)
			}
		case 5:
			if desc.Type != DescTypeSocket {
				t.Fatalf("expected FD 5 type socket, got %s", desc.Type)
			}
		case 6:
			if desc.Type != DescTypeAnon {
				t.Fatalf("expected FD 6 type anon, got %s", desc.Type)
			}
		}
	}
}

func TestDetectorFilteringOptions(t *testing.T) {
	mockDir := t.TempDir()

	mustSymlink := func(target, linkName string) {
		linkPath := filepath.Join(mockDir, linkName)
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatalf("create symlink %s -> %s: %v", linkPath, target, err)
		}
	}

	mustSymlink("/dev/null", "0")
	mustSymlink("/dev/pts/1", "1")
	mustSymlink("/dev/pts/1", "2")
	mustSymlink("/sandbox/mounts/work/file.go", "3")
	mustSymlink("pipe:[99999]", "4")
	mustSymlink("socket:[88888]", "5")
	mustSymlink("anon_inode:[eventpoll]", "6")
	mustSymlink("/unrelated/data.bin", "7")

	d := NewDetector(
		WithProcPath(mockDir),
		WithMountPaths("/sandbox/mounts/work"),
		WithIgnoreSelf(false),
		WithIgnoreSockets(true),
		WithIgnorePipes(true),
		WithIgnoreAnonInode(true),
		WithIgnoredFDs(7),
	)

	rep, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	if rep.UnclosedCount != 1 {
		t.Fatalf("expected UnclosedCount 1 after filtering, got %d", rep.UnclosedCount)
	}
	if rep.UnclosedFDs[0].FD != 3 {
		t.Fatalf("expected remaining unclosed FD 3, got %d", rep.UnclosedFDs[0].FD)
	}
}

func TestSnapshotDiffAndDetect(t *testing.T) {
	mockDir := t.TempDir()

	mustSymlink := func(target, linkName string) {
		linkPath := filepath.Join(mockDir, linkName)
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatalf("create symlink %s -> %s: %v", linkPath, target, err)
		}
	}

	mustSymlink("/dev/null", "0")
	mustSymlink("/dev/pts/1", "1")
	mustSymlink("/dev/pts/1", "2")
	mustSymlink("/dev/urandom", "3")

	d := NewDetector(
		WithProcPath(mockDir),
		WithIgnoreSelf(false),
	)

	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snap.Count() != 4 {
		t.Fatalf("expected snapshot count 4, got %d", snap.Count())
	}
	if !snap.HasFD(3) {
		t.Fatalf("expected snapshot to have FD 3")
	}
	if snap.HasFD(4) {
		t.Fatalf("expected snapshot to not have FD 4")
	}

	mustSymlink("/mnt/mount1/secret", "4")

	d2 := NewDetector(
		WithProcPath(mockDir),
		WithMountPaths("/mnt/mount1"),
		WithIgnoreSelf(false),
	)

	rep, err := snap.DetectLeaks(d2)
	if err != nil {
		t.Fatalf("snap.DetectLeaks failed: %v", err)
	}

	if rep.UnclosedCount != 1 {
		t.Fatalf("expected 1 unclosed leak, got %d", rep.UnclosedCount)
	}
	if rep.MountLeakCount != 1 {
		t.Fatalf("expected 1 mount leak, got %d", rep.MountLeakCount)
	}
	if rep.UnclosedFDs[0].FD != 4 {
		t.Fatalf("expected leaked FD 4, got %d", rep.UnclosedFDs[0].FD)
	}
	if !rep.HasLeaks {
		t.Fatalf("expected HasLeaks true")
	}

	mustSymlink("pipe:[999]", "5")
	descs, err := d2.Enumerate()
	if err != nil {
		t.Fatalf("Enumerate failed: %v", err)
	}
	diff := snap.Diff(descs)
	if len(diff) != 2 {
		t.Fatalf("expected diff length 2 (FDs 4 and 5), got %d", len(diff))
	}
}

func TestRealPipeLeakDetection(t *testing.T) {
	requireBwrap(t)
	d := NewDetector()
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("initial snapshot failed: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}

	rFD := int(r.Fd())
	wFD := int(w.Fd())

	rep, err := snap.DetectLeaks(d)
	if err != nil {
		_ = r.Close()
		_ = w.Close()
		t.Fatalf("DetectLeaks failed: %v", err)
	}

	if !rep.HasLeaks {
		_ = r.Close()
		_ = w.Close()
		t.Fatalf("expected leaks detected for open pipe")
	}

	fdNums := rep.FDNumbers()
	hasR := false
	hasW := false
	for _, n := range fdNums {
		if n == rFD {
			hasR = true
		}
		if n == wFD {
			hasW = true
		}
	}
	if !hasR || !hasW {
		_ = r.Close()
		_ = w.Close()
		t.Fatalf("expected pipe FDs %d and %d in leak list %v", rFD, wFD, fdNums)
	}

	_ = r.Close()
	_ = w.Close()

	repAfter, err := snap.DetectLeaks(d)
	if err != nil {
		t.Fatalf("DetectLeaks after close failed: %v", err)
	}

	for _, n := range repAfter.FDNumbers() {
		if n == rFD || n == wFD {
			t.Fatalf("closed pipe FD %d still present in leak report", n)
		}
	}
}

func TestRealBindMountLeakDetection(t *testing.T) {
	requireBwrap(t)
	mountDir := t.TempDir()
	testFile := filepath.Join(mountDir, "leak_target.txt")
	if err := os.WriteFile(testFile, []byte("leak_content"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	d := NewDetector(WithMountPaths(mountDir))
	snap, err := d.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	f, err := os.Open(testFile)
	if err != nil {
		t.Fatalf("open test file: %v", err)
	}
	defer f.Close()

	rep, err := snap.DetectLeaks(d)
	if err != nil {
		t.Fatalf("DetectLeaks failed: %v", err)
	}

	if rep.MountLeakCount == 0 {
		t.Fatalf("expected at least 1 mount leak for open file %s", testFile)
	}

	mountPaths := rep.MountLeakPaths()
	foundTarget := false
	for _, p := range mountPaths {
		if p == testFile {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		t.Fatalf("expected %s in mount leak paths %v", testFile, mountPaths)
	}

	_ = f.Close()

	repAfter, err := snap.DetectLeaks(d)
	if err != nil {
		t.Fatalf("DetectLeaks after close failed: %v", err)
	}
	for _, p := range repAfter.MountLeakPaths() {
		if p == testFile {
			t.Fatalf("closed mount file %s still present in mount leaks", p)
		}
	}
}

func TestSandboxDetectFDLeaks(t *testing.T) {
	requireBwrap(t)
	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox: %v", err)
	}
	defer sb.Close()

	rep, err := sb.DetectFDLeaks()
	if err != nil {
		t.Fatalf("DetectFDLeaks failed: %v", err)
	}
	if rep.AgentID != Agent56ID {
		t.Fatalf("expected AgentID %d, got %d", Agent56ID, rep.AgentID)
	}
}

func TestSandboxAuditFDLeaks(t *testing.T) {
	requireBwrap(t)
	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox: %v", err)
	}
	defer sb.Close()

	rep, err := sb.AuditFDLeaks(func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("AuditFDLeaks failed: %v", err)
	}
	if rep.UnclosedCount != 0 {
		t.Fatalf("expected 0 unclosed leaks for no-op audit, got %d", rep.UnclosedCount)
	}
}

func TestMatchesMountPath(t *testing.T) {
	tests := []struct {
		target   string
		mounts   []string
		expected bool
		path     string
	}{
		{"", nil, false, ""},
		{"/var/log/app.log", []string{"/var/log"}, true, "/var/log"},
		{"/var/log", []string{"/var/log"}, true, "/var/log"},
		{"/var/logging/app.log", []string{"/var/log"}, false, ""},
		{"/tmp/file.txt (deleted)", []string{"/tmp"}, true, "/tmp"},
		{"/usr/local/bin", []string{"/"}, true, "/"},
		{"/etc/passwd", []string{""}, false, ""},
	}

	for _, tc := range tests {
		matched, mPath := matchesMountPath(tc.target, tc.mounts)
		if matched != tc.expected || mPath != tc.path {
			t.Errorf("matchesMountPath(%q, %v) = (%v, %q), want (%v, %q)",
				tc.target, tc.mounts, matched, mPath, tc.expected, tc.path)
		}
	}
}

func TestClassifyDescriptor(t *testing.T) {
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(tempFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if typ := classifyDescriptor("pipe:[12345]"); typ != DescTypePipe {
		t.Fatalf("expected DescTypePipe, got %s", typ)
	}
	if typ := classifyDescriptor("socket:[54321]"); typ != DescTypeSocket {
		t.Fatalf("expected DescTypeSocket, got %s", typ)
	}
	if typ := classifyDescriptor("anon_inode:[eventpoll]"); typ != DescTypeAnon {
		t.Fatalf("expected DescTypeAnon, got %s", typ)
	}
	if typ := classifyDescriptor("/dev/null"); typ != DescTypeDevice {
		t.Fatalf("expected DescTypeDevice, got %s", typ)
	}
	if typ := classifyDescriptor(tempDir); typ != DescTypeDir {
		t.Fatalf("expected DescTypeDir, got %s", typ)
	}
	if typ := classifyDescriptor(tempFile); typ != DescTypeFile {
		t.Fatalf("expected DescTypeFile, got %s", typ)
	}
	if typ := classifyDescriptor("nonexistent_path"); typ != DescTypeUnknown {
		t.Fatalf("expected DescTypeUnknown, got %s", typ)
	}
}

func TestConvenienceFunctions(t *testing.T) {
	requireBwrap(t)
	_, err := DetectUnclosedDescriptors()
	if err != nil {
		t.Fatalf("DetectUnclosedDescriptors failed: %v", err)
	}

	rep, err := DetectLeaks("/tmp")
	if err != nil {
		t.Fatalf("DetectLeaks failed: %v", err)
	}
	if rep == nil {
		t.Fatalf("expected non-nil LeakReport")
	}

	repMounts, err := DetectBindMountLeaks([]Mount{{Source: "/tmp", Destination: "/tmp"}})
	if err != nil {
		t.Fatalf("DetectBindMountLeaks failed: %v", err)
	}
	if repMounts == nil {
		t.Fatalf("expected non-nil LeakReport")
	}

	snap, err := TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot failed: %v", err)
	}
	if snap == nil {
		t.Fatalf("expected non-nil Snapshot")
	}
}

func TestCloseLeaks(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	rFD := p[0]
	wFD := p[1]

	rep := &LeakReport{
		UnclosedFDs: []Descriptor{
			{FD: 0, IsStd: true},
			{FD: rFD, IsStd: false},
			{FD: wFD, IsStd: false},
		},
	}

	errs := rep.CloseLeaks()
	if len(errs) != 0 {
		t.Fatalf("expected 0 close errors, got %v", errs)
	}

	if err := syscall.Close(rFD); err == nil {
		t.Fatalf("expected closing already-closed rFD %d to fail", rFD)
	}
	if err := syscall.Close(wFD); err == nil {
		t.Fatalf("expected closing already-closed wFD %d to fail", wFD)
	}
}

func TestDetectorConfigGetter(t *testing.T) {
	d := NewDetector(
		WithProcPath("/proc/test"),
		WithIgnoredFDs(10, 11),
		WithMountPaths("/mnt/a", "/mnt/b"),
		WithMounts([]Mount{{Source: "/mnt/c", Destination: "/mnt/d"}}),
		WithIgnoreSelf(false),
	)

	cfg := d.Config()
	if cfg.ProcPath != "/proc/test" {
		t.Fatalf("expected proc path /proc/test, got %s", cfg.ProcPath)
	}
	if len(cfg.IgnoredFDs) != 2 {
		t.Fatalf("expected 2 ignored fds, got %d", len(cfg.IgnoredFDs))
	}
	if len(cfg.MountPaths) != 4 {
		t.Fatalf("expected 4 mount paths, got %d", len(cfg.MountPaths))
	}
	if cfg.IgnoreSelf {
		t.Fatalf("expected IgnoreSelf false")
	}
}
