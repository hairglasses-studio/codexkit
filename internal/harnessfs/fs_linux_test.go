//go:build linux

package harnessfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func openTestRoot(t *testing.T) (string, *Root) {
	t.Helper()
	directory := t.TempDir()
	root, err := Open(directory)
	if err != nil {
		t.Fatalf("Open(%q): %v", directory, err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return directory, root
}

func writeFile(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%q): %v", name, err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", name, err)
	}
	return string(content)
}

func retainStage(t *testing.T, stage *Stage) *Quarantine {
	t.Helper()
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatalf("Remove stage: %v", err)
	}
	t.Cleanup(func() { _ = quarantine.Close() })
	return quarantine
}

func retainHold(t *testing.T, hold *Hold) *Quarantine {
	t.Helper()
	quarantine, err := hold.Remove()
	if err != nil {
		t.Fatalf("Remove hold: %v", err)
	}
	t.Cleanup(func() { _ = quarantine.Close() })
	return quarantine
}

func retainSymlinkStage(t *testing.T, stage *SymlinkStage) *Quarantine {
	t.Helper()
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatalf("Remove symlink stage: %v", err)
	}
	t.Cleanup(func() { _ = quarantine.Close() })
	return quarantine
}

func TestValidatePortableRelativePathRejectsEscapeForms(t *testing.T) {
	t.Parallel()
	valid := []string{"target", ".codex/config.toml", "a/b-c_1.txt", "space allowed/file"}
	for _, value := range valid {
		if err := ValidatePortableRelativePath(value); err != nil {
			t.Errorf("ValidatePortableRelativePath(%q): %v", value, err)
		}
	}
	invalid := []string{"", ".", "..", "/absolute", "../escape", "a/../escape", "a/./b", "a//b", "a/", `a\b`, "a\x00b", "a\nb"}
	for _, value := range invalid {
		if err := ValidatePortableRelativePath(value); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ValidatePortableRelativePath(%q) error = %v, want ErrInvalidPath", value, err)
		}
	}
}

func TestOpenRejectsUnsafeRoots(t *testing.T) {
	t.Parallel()
	if _, err := Open("/"); !errors.Is(err, ErrUnsafeRoot) {
		t.Fatalf("Open(/) error = %v, want ErrUnsafeRoot", err)
	}
	if _, err := Open("relative"); !errors.Is(err, ErrUnsafeRoot) {
		t.Fatalf("Open(relative) error = %v, want ErrUnsafeRoot", err)
	}

	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(linkedRoot); !errors.Is(err, ErrUnsafeRoot) {
		t.Fatalf("Open(symlink root) error = %v, want ErrUnsafeRoot", err)
	}

	unsafeRoot := filepath.Join(parent, "unsafe")
	if err := os.Mkdir(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(unsafeRoot); !errors.Is(err, ErrUnsafeRoot) {
		t.Fatalf("Open(group/world-writable root) error = %v, want ErrUnsafeRoot", err)
	}
}

func TestStageChmodRejectsUnsafeModes(t *testing.T) {
	t.Parallel()
	_, root := openTestRoot(t)
	for _, mode := range []uint32{0o000, 0o220, 0o620, 0o666, 0o777, 0o4755} {
		stage, err := root.CreateStage("target", fmt.Sprintf(".stage-%04o", mode))
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Chmod(mode); !errors.Is(err, ErrInvalidState) {
			t.Errorf("Chmod(%04o) error = %v, want ErrInvalidState", mode, err)
		}
		retainStage(t, stage)
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParentSymlinkCannotEscapeAnchor(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "secret"), "outside", 0o600)
	if err := os.Symlink(external, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := root.Lstat("escape/secret"); err == nil {
		t.Fatal("Lstat through parent symlink unexpectedly succeeded")
	}
	if _, err := root.CreateStage("escape/secret", ".stage"); err == nil {
		t.Fatal("CreateStage through parent symlink unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(external, ".stage")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside stage exists or stat failed: %v", err)
	}
	if got := readFile(t, filepath.Join(external, "secret")); got != "outside" {
		t.Fatalf("outside content = %q, want outside", got)
	}
}

func TestFinalSymlinksAndTrailingSlashNeverFollow(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "outside"), "secret", 0o600)

	links := map[string]string{
		"absolute-link": filepath.Join(external, "outside"),
		"dangling-link": filepath.Join(external, "missing"),
		"relative-link": "missing-relative",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
		entry, err := root.Lstat(name)
		if err != nil {
			t.Fatalf("Lstat(%q): %v", name, err)
		}
		if entry.Kind != KindSymlink {
			t.Fatalf("Lstat(%q).Kind = %s, want symlink", name, entry.Kind)
		}
		if _, _, err := root.ReadRegular(name, 1024); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("ReadRegular(%q) error = %v, want ErrNotRegular", name, err)
		}
		if _, err := root.Lstat(name + "/"); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Lstat(%q/) error = %v, want ErrInvalidPath", name, err)
		}
	}

	if err := os.Symlink(external, filepath.Join(directory, "directory-link")); err != nil {
		t.Fatal(err)
	}
	// Regression for GO-2026-4970: a final symlink plus trailing slash is
	// rejected syntactically and never delegated to root-scoped path helpers.
	if _, err := root.Lstat("directory-link/"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("trailing-slash final symlink error = %v, want ErrInvalidPath", err)
	}
	if got := readFile(t, filepath.Join(external, "outside")); got != "secret" {
		t.Fatalf("outside content changed to %q", got)
	}
}

func TestValidateRelativeSymlinkTargetContainment(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"file", "../shared/file", "nested/file"} {
		if err := ValidateRelativeSymlinkTarget("config/link", target); err != nil {
			t.Errorf("valid target %q: %v", target, err)
		}
	}
	invalid := []string{"", "/absolute", "../../escape", "file/", ".", "link", "bad\x00target", `bad\target`, strings.Repeat("x", maxSymlinkTargetLength+1)}
	for _, target := range invalid {
		if err := ValidateRelativeSymlinkTarget("config/link", target); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("target %q error = %v, want ErrInvalidPath", target, err)
		}
	}
}

func TestSymlinkStageCommitAndParentContainment(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	for _, name := range []string{"config", "shared"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(directory, "shared", "file"), "shared", 0o600)
	stage, err := root.CreateSymlinkStage("config/link", ".link.stage", "../shared/file")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.CommitCreate(); err != nil {
		t.Fatal(err)
	}
	if err := root.SyncParent("config/link"); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(directory, "config", "link")); err != nil || target != "../shared/file" {
		t.Fatalf("Readlink = %q, %v", target, err)
	}

	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.CreateSymlinkStage("escape/link", ".stage", "target"); err == nil {
		t.Fatal("symlink stage through destination parent symlink succeeded")
	}
	if _, err := root.CreateSymlinkStage("config/bad", ".bad.stage", "../escape/file"); err == nil {
		t.Fatal("symlink target through parent symlink succeeded")
	}
	if _, err := root.CreateSymlinkStage("config/trailing/", ".stage", "target"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("trailing destination error = %v", err)
	}
}

func TestRegularAndSymlinkStagesExchangeTypesSafely(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "referent", 0o600)
	writeFile(t, filepath.Join(directory, "regular-target"), "old regular", 0o600)
	symlinkStage, err := root.CreateSymlinkStage("regular-target", ".symlink.stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	defer symlinkStage.Close()
	if err := symlinkStage.Exchange(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(directory, "regular-target")); err != nil || target != "referent" {
		t.Fatalf("installed symlink = %q, %v", target, err)
	}
	if got := readFile(t, filepath.Join(directory, ".symlink.stage")); got != "old regular" {
		t.Fatalf("displaced regular = %q", got)
	}
	if record := retainSymlinkStage(t, symlinkStage).Record(); record.Entry.Kind != KindRegular || record.LinkTarget != "" {
		t.Fatalf("regular quarantine record = %+v", record)
	}

	if err := os.Symlink("referent", filepath.Join(directory, "symlink-target")); err != nil {
		t.Fatal(err)
	}
	regularStage, err := root.CreateStage("symlink-target", ".regular.stage")
	if err != nil {
		t.Fatal(err)
	}
	defer regularStage.Close()
	if _, err := regularStage.Write([]byte("new regular")); err != nil {
		t.Fatal(err)
	}
	if err := regularStage.Exchange(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(directory, "symlink-target")); got != "new regular" {
		t.Fatalf("installed regular = %q", got)
	}
	if target, err := os.Readlink(filepath.Join(directory, ".regular.stage")); err != nil || target != "referent" {
		t.Fatalf("displaced symlink = %q, %v", target, err)
	}
	if record := retainStage(t, regularStage).Record(); record.Entry.Kind != KindSymlink || record.LinkTarget != "referent" {
		t.Fatalf("symlink quarantine record = %+v", record)
	}
}

func TestSymlinkStageRejectsSpecialAndHardlinkTransitions(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "referent", 0o600)
	if err := os.Mkdir(filepath.Join(directory, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	directoryStage, err := root.CreateSymlinkStage("directory", ".directory.stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	if err := directoryStage.Exchange(); !errors.Is(err, ErrNotArtifact) {
		t.Fatalf("directory exchange error = %v, want ErrNotArtifact", err)
	}
	retainSymlinkStage(t, directoryStage)
	_ = directoryStage.Close()

	writeFile(t, filepath.Join(directory, "hardlink"), "linked", 0o600)
	if err := os.Link(filepath.Join(directory, "hardlink"), filepath.Join(directory, "hardlink-peer")); err != nil {
		t.Fatal(err)
	}
	hardlinkStage, err := root.CreateSymlinkStage("hardlink", ".hardlink.stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	if err := hardlinkStage.Exchange(); !errors.Is(err, ErrHardlink) {
		t.Fatalf("hardlink exchange error = %v, want ErrHardlink", err)
	}
	retainSymlinkStage(t, hardlinkStage)
	_ = hardlinkStage.Close()
	if _, err := root.Detach("hardlink", ".hold"); !errors.Is(err, ErrHardlink) {
		t.Fatalf("hardlink detach error = %v, want ErrHardlink", err)
	}
}

func TestSymlinkNoReplaceCollisionAndDetachQuarantine(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "referent", 0o600)
	writeFile(t, filepath.Join(directory, "target"), "collision", 0o600)
	stage, err := root.CreateSymlinkStage("target", ".stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.CommitCreate(); !errors.Is(err, ErrCollision) {
		t.Fatalf("symlink commit collision error = %v", err)
	}
	if got := readFile(t, filepath.Join(directory, "target")); got != "collision" {
		t.Fatalf("collision target changed to %q", got)
	}
	retainSymlinkStage(t, stage)
	_ = stage.Close()

	if err := os.Symlink("referent", filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	hold, err := root.Detach("link", ".link.hold")
	if err != nil {
		t.Fatal(err)
	}
	quarantine := retainHold(t, hold)
	record := quarantine.Record()
	if record.Entry.Kind != KindSymlink || record.LinkTarget != "referent" {
		t.Fatalf("detached symlink quarantine = %+v", record)
	}
}

func TestStageAtomicCreateAndDurabilityPrimitives(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	if err := os.Mkdir(filepath.Join(directory, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	stage, err := root.CreateStage("config/settings.toml", ".settings.stage")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Write([]byte("model = \"terra\"\n")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Chmod(0o640); err != nil {
		t.Fatal(err)
	}
	if err := stage.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.CommitCreate(); err != nil {
		t.Fatal(err)
	}
	if err := root.SyncParent("config/settings.toml"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(directory, "config", "settings.toml")); got != "model = \"terra\"\n" {
		t.Fatalf("target content = %q", got)
	}
	stat, err := os.Stat(filepath.Join(directory, "config", "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := stat.Mode().Perm(); got != 0o640 {
		t.Fatalf("target mode = %o, want 640", got)
	}
	if _, err := stage.Remove(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Remove committed stage error = %v, want ErrInvalidState", err)
	}
}

func TestAtomicCreateCollisionPreservesRacingTargetAndStage(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("target", ".target.stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("staged")); err != nil {
		t.Fatal(err)
	}
	// Simulate a target appearing after the initial plan and stage creation.
	writeFile(t, filepath.Join(directory, "target"), "racer", 0o600)
	if err := stage.CommitCreate(); !errors.Is(err, ErrCollision) {
		t.Fatalf("CommitCreate error = %v, want ErrCollision", err)
	}
	if got := readFile(t, filepath.Join(directory, "target")); got != "racer" {
		t.Fatalf("target content = %q, want racer", got)
	}
	if got := readFile(t, filepath.Join(directory, ".target.stage")); got != "staged" {
		t.Fatalf("stage content = %q, want staged", got)
	}
	retainStage(t, stage)
}

func TestExchangeExposesDisplacedObjectThroughStageHandle(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "target"), "old", 0o600)
	stage, err := root.CreateStage("target", ".target.stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Exchange(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(directory, "target")); got != "new" {
		t.Fatalf("target content = %q, want new", got)
	}
	if got := readFile(t, filepath.Join(directory, ".target.stage")); got != "old" {
		t.Fatalf("displaced content = %q, want old", got)
	}
	hold, err := stage.Detach(".target.hold")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".target.stage")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage should be detached: %v", err)
	}
	if got := readFile(t, filepath.Join(directory, ".target.hold")); got != "old" {
		t.Fatalf("held content = %q, want old", got)
	}
	retainHold(t, hold)
	if err := root.SyncParent("target"); err != nil {
		t.Fatal(err)
	}
}

func TestDetachUsesNoReplaceAndHoldRemoval(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "target"), "target", 0o600)
	writeFile(t, filepath.Join(directory, ".hold"), "collision", 0o600)
	if _, err := root.Detach("target", ".hold"); !errors.Is(err, ErrCollision) {
		t.Fatalf("Detach collision error = %v, want ErrCollision", err)
	}
	if got := readFile(t, filepath.Join(directory, "target")); got != "target" {
		t.Fatalf("target changed to %q", got)
	}
	if err := os.Remove(filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	hold, err := root.Detach("target", ".hold")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "target")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target should be detached: %v", err)
	}
	retainHold(t, hold)
}

func TestFIFOAndSocketCollisionsAreNeverOpenedOrReplaced(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	if err := unix.Mkfifo(filepath.Join(directory, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	directoryFD, err := unix.Open(directory, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(directoryFD)
	socketFD, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(socketFD)
	if err := unix.Bind(socketFD, &unix.SockaddrUnix{Name: filepath.Join("/proc/self/fd", fmt.Sprint(directoryFD), "socket")}); err != nil {
		t.Fatal(err)
	}

	for name, wantKind := range map[string]EntryKind{"fifo": KindFIFO, "socket": KindSocket} {
		entry, err := root.Lstat(name)
		if err != nil {
			t.Fatalf("Lstat(%s): %v", name, err)
		}
		if entry.Kind != wantKind {
			t.Fatalf("Lstat(%s).Kind = %s, want %s", name, entry.Kind, wantKind)
		}
		if _, _, err := root.ReadRegular(name, 1024); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("ReadRegular(%s) error = %v, want ErrNotRegular", name, err)
		}
		stage, err := root.CreateStage(name, "."+name+".stage")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stage.Write([]byte("new")); err != nil {
			t.Fatal(err)
		}
		if err := stage.CommitCreate(); !errors.Is(err, ErrCollision) {
			t.Fatalf("CommitCreate(%s) error = %v, want ErrCollision", name, err)
		}
		if err := stage.Exchange(); !errors.Is(err, ErrNotArtifact) {
			t.Fatalf("Exchange(%s) error = %v, want ErrNotArtifact", name, err)
		}
		retainStage(t, stage)
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTypedHandlesRefuseSubstitutedTemporaryNames(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("target", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := os.Remove(filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, ".stage"), "replacement", 0o600)
	if _, err := stage.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove substituted stage error = %v, want ErrStaleHandle", err)
	}
	if got := readFile(t, filepath.Join(directory, ".stage")); got != "replacement" {
		t.Fatalf("substituted stage content = %q", got)
	}

	writeFile(t, filepath.Join(directory, "target"), "old", 0o600)
	hold, err := root.Detach("target", ".hold")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, ".hold"), "hold replacement", 0o600)
	if _, err := hold.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove substituted hold error = %v, want ErrStaleHandle", err)
	}
	if got := readFile(t, filepath.Join(directory, ".hold")); got != "hold replacement" {
		t.Fatalf("substituted hold content = %q", got)
	}
}

func TestStageSourceRacePreservesRacingReplacement(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("target", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("pinned stage")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, ".replacement"), "replacement", 0o600)

	hookRan := false
	raceStart := make(chan struct{})
	raceDone := make(chan error, 1)
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, _ string) {
		if phase != cleanupBeforeQuarantineRename {
			return
		}
		if hookRan {
			return
		}
		hookRan = true
		close(raceStart)
		if err := <-raceDone; err != nil {
			t.Fatalf("cleanup race: %v", err)
		}
	}
	go func() {
		<-raceStart
		if err := os.Rename(filepath.Join(directory, ".stage"), filepath.Join(directory, ".original-stage")); err != nil {
			raceDone <- fmt.Errorf("preserve original: %w", err)
			return
		}
		if err := os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, ".stage")); err != nil {
			raceDone <- fmt.Errorf("substitute source: %w", err)
			return
		}
		raceDone <- nil
	}()
	if _, err := stage.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove raced stage error = %v, want ErrStaleHandle", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("cleanup race hook did not run")
	}
	if got := readFile(t, filepath.Join(directory, ".stage")); got != "replacement" {
		t.Fatalf("racing replacement = %q, want replacement", got)
	}
	if got := readFile(t, filepath.Join(directory, ".original-stage")); got != "pinned stage" {
		t.Fatalf("pinned original = %q, want pinned stage", got)
	}

	// Restore the pinned object to its typed handle and prove cleanup remains
	// retryable after the rejected substitution.
	if err := os.Remove(filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".original-stage"), filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	retainStage(t, stage)
}

func TestHoldSourceRacePreservesRacingReplacement(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "target"), "pinned hold", 0o600)
	hold, err := root.Detach("target", ".hold")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	writeFile(t, filepath.Join(directory, ".replacement"), "replacement", 0o600)

	hookRan := false
	raceStart := make(chan struct{})
	raceDone := make(chan error, 1)
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, _ string) {
		if phase != cleanupBeforeQuarantineRename {
			return
		}
		if hookRan {
			return
		}
		hookRan = true
		close(raceStart)
		if err := <-raceDone; err != nil {
			t.Fatalf("cleanup race: %v", err)
		}
	}
	go func() {
		<-raceStart
		if err := os.Rename(filepath.Join(directory, ".hold"), filepath.Join(directory, ".original-hold")); err != nil {
			raceDone <- fmt.Errorf("preserve original: %w", err)
			return
		}
		if err := os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, ".hold")); err != nil {
			raceDone <- fmt.Errorf("substitute source: %w", err)
			return
		}
		raceDone <- nil
	}()
	if _, err := hold.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove raced hold error = %v, want ErrStaleHandle", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("cleanup race hook did not run")
	}
	if got := readFile(t, filepath.Join(directory, ".hold")); got != "replacement" {
		t.Fatalf("racing replacement = %q, want replacement", got)
	}
	if got := readFile(t, filepath.Join(directory, ".original-hold")); got != "pinned hold" {
		t.Fatalf("pinned original = %q, want pinned hold", got)
	}

	if err := os.Remove(filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".original-hold"), filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	retainHold(t, hold)
}

func TestStageQuarantineRaceAfterVerificationRetainsBothObjects(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("target", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("pinned stage")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, ".replacement"), "replacement", 0o600)

	raceStart := make(chan string, 1)
	raceDone := make(chan error, 1)
	hookRan := false
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, quarantineLeaf string) {
		if phase != cleanupAfterQuarantineVerify || hookRan {
			return
		}
		hookRan = true
		raceStart <- quarantineLeaf
		if err := <-raceDone; err != nil {
			t.Fatalf("cleanup race: %v", err)
		}
	}
	go func() {
		quarantineLeaf := <-raceStart
		if err := os.Rename(filepath.Join(directory, quarantineLeaf), filepath.Join(directory, ".pinned-after-verify")); err != nil {
			raceDone <- fmt.Errorf("move pinned quarantine: %w", err)
			return
		}
		if err := os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, quarantineLeaf)); err != nil {
			raceDone <- fmt.Errorf("substitute quarantine: %w", err)
			return
		}
		raceDone <- nil
	}()
	if _, err := stage.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove quarantine-raced stage error = %v, want ErrStaleHandle", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("post-verification race hook did not run")
	}
	if got := readFile(t, filepath.Join(directory, ".stage")); got != "replacement" {
		t.Fatalf("restored racing replacement = %q", got)
	}
	if got := readFile(t, filepath.Join(directory, ".pinned-after-verify")); got != "pinned stage" {
		t.Fatalf("moved pinned object = %q", got)
	}

	if err := os.Remove(filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".pinned-after-verify"), filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	retainStage(t, stage)
}

func TestHoldQuarantineRaceAfterVerificationRetainsBothObjects(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "target"), "pinned hold", 0o600)
	hold, err := root.Detach("target", ".hold")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	writeFile(t, filepath.Join(directory, ".replacement"), "replacement", 0o600)

	raceStart := make(chan string, 1)
	raceDone := make(chan error, 1)
	hookRan := false
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, quarantineLeaf string) {
		if phase != cleanupAfterQuarantineVerify || hookRan {
			return
		}
		hookRan = true
		raceStart <- quarantineLeaf
		if err := <-raceDone; err != nil {
			t.Fatalf("cleanup race: %v", err)
		}
	}
	go func() {
		quarantineLeaf := <-raceStart
		if err := os.Rename(filepath.Join(directory, quarantineLeaf), filepath.Join(directory, ".pinned-after-verify")); err != nil {
			raceDone <- fmt.Errorf("move pinned quarantine: %w", err)
			return
		}
		if err := os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, quarantineLeaf)); err != nil {
			raceDone <- fmt.Errorf("substitute quarantine: %w", err)
			return
		}
		raceDone <- nil
	}()
	if _, err := hold.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("Remove quarantine-raced hold error = %v, want ErrStaleHandle", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("post-verification race hook did not run")
	}
	if got := readFile(t, filepath.Join(directory, ".hold")); got != "replacement" {
		t.Fatalf("restored racing replacement = %q", got)
	}
	if got := readFile(t, filepath.Join(directory, ".pinned-after-verify")); got != "pinned hold" {
		t.Fatalf("moved pinned object = %q", got)
	}

	if err := os.Remove(filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".pinned-after-verify"), filepath.Join(directory, ".hold")); err != nil {
		t.Fatal(err)
	}
	retainHold(t, hold)
}

func TestQuarantineRecordCanBeReopenedAndRestored(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("target", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("retained")); err != nil {
		t.Fatal(err)
	}
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatal(err)
	}
	record := quarantine.Record()
	if !strings.HasPrefix(filepath.Base(record.Path), quarantinePrefix) || record.Entry.Kind != KindRegular {
		t.Fatalf("unexpected quarantine record: %+v", record)
	}
	if got := readFile(t, filepath.Join(directory, filepath.FromSlash(record.Path))); got != "retained" {
		t.Fatalf("quarantined content = %q", got)
	}
	if err := quarantine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := root.OpenQuarantine(record)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Restore("restored"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(directory, "restored")); got != "retained" {
		t.Fatalf("restored content = %q", got)
	}
}

func TestSymlinkQuarantineRecordCanBeReopenedAndRestored(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "referent", 0o600)
	stage, err := root.CreateSymlinkStage("link", ".link.stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatal(err)
	}
	record := quarantine.Record()
	if record.Entry.Kind != KindSymlink || record.LinkTarget != "referent" {
		t.Fatalf("unexpected symlink quarantine record: %+v", record)
	}
	if err := quarantine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := root.OpenQuarantine(record)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Restore("restored-link"); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(directory, "restored-link")); err != nil || target != "referent" {
		t.Fatalf("restored symlink = %q, %v", target, err)
	}
}

func TestSymlinkStageSourceRacePreservesBothSymlinks(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "one", 0o600)
	writeFile(t, filepath.Join(directory, "other"), "two", 0o600)
	stage, err := root.CreateSymlinkStage("link", ".stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := os.Symlink("other", filepath.Join(directory, ".replacement")); err != nil {
		t.Fatal(err)
	}

	raceStart := make(chan struct{})
	raceDone := make(chan error, 1)
	hookRan := false
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, _ string) {
		if phase != cleanupBeforeQuarantineRename || hookRan {
			return
		}
		hookRan = true
		close(raceStart)
		if err := <-raceDone; err != nil {
			t.Fatalf("source race: %v", err)
		}
	}
	go func() {
		<-raceStart
		if err := os.Rename(filepath.Join(directory, ".stage"), filepath.Join(directory, ".pinned")); err != nil {
			raceDone <- err
			return
		}
		raceDone <- os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, ".stage"))
	}()
	if _, err := stage.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("source-raced symlink error = %v", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("source race hook did not run")
	}
	if target, _ := os.Readlink(filepath.Join(directory, ".stage")); target != "other" {
		t.Fatalf("replacement symlink target = %q", target)
	}
	if target, _ := os.Readlink(filepath.Join(directory, ".pinned")); target != "referent" {
		t.Fatalf("pinned symlink target = %q", target)
	}
	if err := os.Remove(filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".pinned"), filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	retainSymlinkStage(t, stage)
}

func TestSymlinkStageQuarantineRacePreservesBothSymlinks(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "referent"), "one", 0o600)
	writeFile(t, filepath.Join(directory, "other"), "two", 0o600)
	stage, err := root.CreateSymlinkStage("link", ".stage", "referent")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := os.Symlink("other", filepath.Join(directory, ".replacement")); err != nil {
		t.Fatal(err)
	}

	raceStart := make(chan string, 1)
	raceDone := make(chan error, 1)
	hookRan := false
	root.cleanupTestHook = func(phase cleanupHookPhase, _ int, _, quarantineLeaf string) {
		if phase != cleanupAfterQuarantineVerify || hookRan {
			return
		}
		hookRan = true
		raceStart <- quarantineLeaf
		if err := <-raceDone; err != nil {
			t.Fatalf("quarantine race: %v", err)
		}
	}
	go func() {
		quarantineLeaf := <-raceStart
		if err := os.Rename(filepath.Join(directory, quarantineLeaf), filepath.Join(directory, ".pinned")); err != nil {
			raceDone <- err
			return
		}
		raceDone <- os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, quarantineLeaf))
	}()
	if _, err := stage.Remove(); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("quarantine-raced symlink error = %v", err)
	}
	root.cleanupTestHook = nil
	if !hookRan {
		t.Fatal("quarantine race hook did not run")
	}
	if target, _ := os.Readlink(filepath.Join(directory, ".stage")); target != "other" {
		t.Fatalf("replacement symlink target = %q", target)
	}
	if target, _ := os.Readlink(filepath.Join(directory, ".pinned")); target != "referent" {
		t.Fatalf("pinned symlink target = %q", target)
	}
	if err := os.Remove(filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, ".pinned"), filepath.Join(directory, ".stage")); err != nil {
		t.Fatal(err)
	}
	retainSymlinkStage(t, stage)
}

func makeTransitiveReferent(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "safe", "current"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "links"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "safe", "current", "file"), "inside", 0o600)
}

func substituteReferentParent(t *testing.T, directory string) {
	t.Helper()
	current := filepath.Join(directory, "safe", "current")
	retained := filepath.Join(directory, "safe", "retained")
	if err := os.Rename(current, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), current); err != nil {
		t.Fatal(err)
	}
}

func restoreReferentParent(t *testing.T, directory string) {
	t.Helper()
	current := filepath.Join(directory, "safe", "current")
	retained := filepath.Join(directory, "safe", "retained")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, current); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkCommitRevalidatesTransitiveReferentAfterRename(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	makeTransitiveReferent(t, directory)
	stage, err := root.CreateSymlinkStage("links/link", ".stage", "../safe/current/file")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()

	hookRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		if phase != transitionBeforeSymlinkCommitRename || hookRan {
			return
		}
		hookRan = true
		substituteReferentParent(t, directory)
	}
	err = stage.CommitCreate()
	root.transitionTestHook = nil
	if !hookRan {
		t.Fatal("commit referent race hook did not run")
	}
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || !recovery.Evidence.IdentityKnown || recovery.Evidence.Path != "links/link" {
		t.Fatalf("CommitCreate race error = %#v, want pinned links/link recovery evidence", err)
	}
	if target, readErr := os.Readlink(filepath.Join(directory, "links", "link")); readErr != nil || target != "../safe/current/file" {
		t.Fatalf("retained installed symlink = %q, %v", target, readErr)
	}

	restoreReferentParent(t, directory)
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatalf("Remove retained symlink: %v", err)
	}
	defer quarantine.Close()
}

func TestSymlinkExchangeRollsBackTransitiveReferentRace(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	makeTransitiveReferent(t, directory)
	writeFile(t, filepath.Join(directory, "links", "link"), "displaced", 0o600)
	stage, err := root.CreateSymlinkStage("links/link", ".stage", "../safe/current/file")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()

	hookRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		if phase != transitionBeforeSymlinkExchange || hookRan {
			return
		}
		hookRan = true
		substituteReferentParent(t, directory)
	}
	err = stage.Exchange()
	root.transitionTestHook = nil
	if err == nil || !hookRan {
		t.Fatalf("Exchange referent race error = %v, hookRan = %t", err, hookRan)
	}
	if got := readFile(t, filepath.Join(directory, "links", "link")); got != "displaced" {
		t.Fatalf("rolled-back target content = %q", got)
	}
	if target, readErr := os.Readlink(filepath.Join(directory, "links", ".stage")); readErr != nil || target != "../safe/current/file" {
		t.Fatalf("rolled-back stage symlink = %q, %v", target, readErr)
	}

	restoreReferentParent(t, directory)
	retainSymlinkStage(t, stage)
}

func TestQuarantineOpenAndRestoreRevalidateTransitiveReferent(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	makeTransitiveReferent(t, directory)
	stage, err := root.CreateSymlinkStage("links/link", ".stage", "../safe/current/file")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatal(err)
	}
	record := quarantine.Record()
	if err := quarantine.Close(); err != nil {
		t.Fatal(err)
	}

	openHookRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		if phase != transitionBeforeQuarantineOpen || openHookRan {
			return
		}
		openHookRan = true
		substituteReferentParent(t, directory)
	}
	_, err = root.OpenQuarantine(record)
	root.transitionTestHook = nil
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || !openHookRan || !recovery.Evidence.IdentityKnown || recovery.Evidence.Path != record.Path {
		t.Fatalf("OpenQuarantine race error = %#v, hookRan = %t", err, openHookRan)
	}
	if _, statErr := os.Lstat(filepath.Join(directory, filepath.FromSlash(record.Path))); statErr != nil {
		t.Fatalf("quarantine missing after rejected open: %v", statErr)
	}
	restoreReferentParent(t, directory)

	reopened, err := root.OpenQuarantine(record)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restoreHookRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		if phase != transitionBeforeQuarantineRestore || restoreHookRan {
			return
		}
		restoreHookRan = true
		substituteReferentParent(t, directory)
	}
	err = reopened.Restore("links/restored")
	root.transitionTestHook = nil
	recovery = nil
	if !errors.As(err, &recovery) || !restoreHookRan || recovery.Evidence.Path != record.Path {
		t.Fatalf("Restore race error = %#v, hookRan = %t", err, restoreHookRan)
	}
	if _, statErr := os.Lstat(filepath.Join(directory, "links", "restored")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsafe restored path remains: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(directory, filepath.FromSlash(record.Path))); statErr != nil {
		t.Fatalf("quarantine missing after restore rollback: %v", statErr)
	}
	restoreReferentParent(t, directory)
	if err := reopened.Restore("links/restored"); err != nil {
		t.Fatalf("Restore after referent recovery: %v", err)
	}
}

func TestCommitCreateLinkCountDriftRetainsPinnedRecovery(t *testing.T) {
	t.Parallel()
	t.Run("regular", func(t *testing.T) {
		directory, root := openTestRoot(t)
		stage, err := root.CreateStage("target", ".stage")
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		if _, err := stage.Write([]byte("regular")); err != nil {
			t.Fatal(err)
		}
		root.transitionTestHook = func(phase transitionHookPhase) {
			if phase == transitionAfterRegularCommitRename {
				if err := os.Link(filepath.Join(directory, "target"), filepath.Join(directory, "alias")); err != nil {
					t.Fatal(err)
				}
			}
		}
		err = stage.CommitCreate()
		root.transitionTestHook = nil
		var recovery *RecoveryError
		if !errors.As(err, &recovery) || !errors.Is(err, ErrHardlink) || !recovery.Evidence.IdentityKnown ||
			recovery.Evidence.Path != "target" || recovery.Evidence.Entry.Links != 2 {
			t.Fatalf("regular link drift error = %#v", err)
		}
		if stage.state != stageRetained || stage.guardFD < 0 {
			t.Fatalf("regular recovery authority not retained: state=%d guard=%d", stage.state, stage.guardFD)
		}
		if readFile(t, filepath.Join(directory, "target")) != "regular" || readFile(t, filepath.Join(directory, "alias")) != "regular" {
			t.Fatal("regular recovery links were not both retained")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		directory, root := openTestRoot(t)
		writeFile(t, filepath.Join(directory, "referent"), "referent", 0o600)
		stage, err := root.CreateSymlinkStage("target", ".stage", "referent")
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		root.transitionTestHook = func(phase transitionHookPhase) {
			if phase == transitionAfterSymlinkCommitRename {
				if err := os.Link(filepath.Join(directory, "target"), filepath.Join(directory, "alias")); err != nil {
					t.Fatal(err)
				}
			}
		}
		err = stage.CommitCreate()
		root.transitionTestHook = nil
		var recovery *RecoveryError
		if !errors.As(err, &recovery) || !errors.Is(err, ErrHardlink) || !recovery.Evidence.IdentityKnown ||
			recovery.Evidence.Path != "target" || recovery.Evidence.Entry.Links != 2 {
			t.Fatalf("symlink link drift error = %#v", err)
		}
		if stage.state != stageRetained || stage.guardFD < 0 {
			t.Fatalf("symlink recovery authority not retained: state=%d guard=%d", stage.state, stage.guardFD)
		}
		for _, name := range []string{"target", "alias"} {
			if target, readErr := os.Readlink(filepath.Join(directory, name)); readErr != nil || target != "referent" {
				t.Fatalf("retained %s symlink = %q, %v", name, target, readErr)
			}
		}
	})
}

func assertGuardOpen(t *testing.T, fd int) {
	t.Helper()
	var stat unix.Stat_t
	if fd < 0 || unix.Fstat(fd, &stat) != nil {
		t.Fatalf("descriptor guard %d is not open", fd)
	}
}

func TestExchangeRollbackReverifiesAfterSuccessfulRename(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "target"), "target-original", 0o600)
	writeFile(t, filepath.Join(directory, ".replacement-stage"), "stage-replacement", 0o600)
	writeFile(t, filepath.Join(directory, ".replacement-target"), "target-replacement", 0o600)
	stage, err := root.CreateStage("target", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("source-original")); err != nil {
		t.Fatal(err)
	}

	postTransitionRan := false
	postRollbackRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		switch phase {
		case transitionAfterExchangeRename:
			if postTransitionRan {
				return
			}
			postTransitionRan = true
			// The extra link makes the installed source fail its artifact
			// postcondition without changing either candidate name.
			if err := os.Link(filepath.Join(directory, "target"), filepath.Join(directory, ".source-alias")); err != nil {
				t.Fatal(err)
			}
		case transitionAfterExchangeRollback:
			if postRollbackRan {
				return
			}
			postRollbackRan = true
			// A successful rollback is not authority. Substitute both names
			// before descriptor re-verification and retain all four objects.
			for _, rename := range [][2]string{
				{".stage", ".source-pinned"},
				{"target", ".target-pinned"},
				{".replacement-stage", ".stage"},
				{".replacement-target", "target"},
			} {
				if err := os.Rename(filepath.Join(directory, rename[0]), filepath.Join(directory, rename[1])); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	err = stage.Exchange()
	root.transitionTestHook = nil
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || !postTransitionRan || !postRollbackRan {
		t.Fatalf("Exchange recovery error = %#v, hooks = %t/%t", err, postTransitionRan, postRollbackRan)
	}
	if recovery.Evidence.Path != "" || !recovery.Evidence.DescriptorPinned || recovery.Evidence.IdentityKnown {
		t.Fatalf("source recovery evidence = %+v", recovery.Evidence)
	}
	if len(recovery.Related) != 3 || recovery.Related[0].Path != "" || !recovery.Related[0].DescriptorPinned {
		t.Fatalf("exchange related evidence = %+v", recovery.Related)
	}
	wantReplacementPaths := map[string]bool{".stage": false, "target": false}
	for _, evidence := range recovery.Related[1:] {
		if _, exists := wantReplacementPaths[evidence.Path]; !exists || !evidence.DescriptorPinned || !evidence.IdentityKnown {
			t.Fatalf("replacement evidence = %+v", evidence)
		}
		wantReplacementPaths[evidence.Path] = true
	}
	for path, found := range wantReplacementPaths {
		if !found {
			t.Fatalf("missing replacement evidence for %q", path)
		}
	}
	if stage.name != "" || stage.state != stageRetained || len(stage.recovery) != 3 {
		t.Fatalf("stage retained state = name %q state %d pins %d", stage.name, stage.state, len(stage.recovery))
	}
	assertGuardOpen(t, stage.guardFD)
	for _, pin := range stage.recovery {
		assertGuardOpen(t, pin.guardFD)
	}
	for name, want := range map[string]string{
		".source-pinned": "source-original",
		".target-pinned": "target-original",
		".stage":         "stage-replacement",
		"target":         "target-replacement",
	} {
		if got := readFile(t, filepath.Join(directory, name)); got != want {
			t.Fatalf("%s content = %q, want %q", name, got, want)
		}
	}
}

func TestQuarantineRestoreRollbackCollisionRebindsRecord(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("unused", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("retained")); err != nil {
		t.Fatal(err)
	}
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Close()
	record := quarantine.Record()
	writeFile(t, filepath.Join(directory, ".replacement"), "replacement", 0o600)

	hookRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		if phase != transitionAfterQuarantineRestoreRename || hookRan {
			return
		}
		hookRan = true
		if err := os.Rename(filepath.Join(directory, "restored"), filepath.Join(directory, filepath.FromSlash(record.Path))); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(directory, ".replacement"), filepath.Join(directory, "restored")); err != nil {
			t.Fatal(err)
		}
	}
	err = quarantine.Restore("restored")
	root.transitionTestHook = nil
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || !hookRan || !errors.Is(err, unix.EEXIST) {
		t.Fatalf("Restore collision error = %#v, hookRan = %t", err, hookRan)
	}
	if !recovery.Evidence.IdentityKnown || !recovery.Evidence.DescriptorPinned || recovery.Evidence.Path != record.Path {
		t.Fatalf("quarantine evidence = %+v, want %q", recovery.Evidence, record.Path)
	}
	if len(recovery.Related) != 1 || recovery.Related[0].Path != "restored" || !recovery.Related[0].IdentityKnown || !recovery.Related[0].DescriptorPinned {
		t.Fatalf("replacement evidence = %+v", recovery.Related)
	}
	if current := quarantine.Record(); current.Path != record.Path || current.Entry.Inode != record.Entry.Inode {
		t.Fatalf("stale quarantine record: got %+v, want identity/path %+v", current, record)
	}
	if len(quarantine.recovery) != 1 {
		t.Fatalf("retained replacement guard count = %d", len(quarantine.recovery))
	}
	assertGuardOpen(t, quarantine.guardFD)
	assertGuardOpen(t, quarantine.recovery[0].guardFD)
	if got := readFile(t, filepath.Join(directory, filepath.FromSlash(record.Path))); got != "retained" {
		t.Fatalf("retained quarantine content = %q", got)
	}
	if got := readFile(t, filepath.Join(directory, "restored")); got != "replacement" {
		t.Fatalf("replacement content = %q", got)
	}
}

func TestQuarantineRestoreDoesNotTrustSuccessfulRollbackRename(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	stage, err := root.CreateStage("unused", ".stage")
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if _, err := stage.Write([]byte("retained")); err != nil {
		t.Fatal(err)
	}
	quarantine, err := stage.Remove()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Close()
	record := quarantine.Record()
	writeFile(t, filepath.Join(directory, ".replacement-quarantine"), "quarantine-replacement", 0o600)
	writeFile(t, filepath.Join(directory, ".replacement-target"), "target-replacement", 0o600)

	postTransitionRan := false
	postRollbackRan := false
	root.transitionTestHook = func(phase transitionHookPhase) {
		switch phase {
		case transitionAfterQuarantineRestoreRename:
			if postTransitionRan {
				return
			}
			postTransitionRan = true
			if err := os.Link(filepath.Join(directory, "restored"), filepath.Join(directory, ".retained-alias")); err != nil {
				t.Fatal(err)
			}
		case transitionAfterQuarantineRestoreRollback:
			if postRollbackRan {
				return
			}
			postRollbackRan = true
			for _, rename := range [][2]string{
				{filepath.FromSlash(record.Path), ".retained-pinned"},
				{".replacement-quarantine", filepath.FromSlash(record.Path)},
				{".replacement-target", "restored"},
			} {
				if err := os.Rename(filepath.Join(directory, rename[0]), filepath.Join(directory, rename[1])); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	err = quarantine.Restore("restored")
	root.transitionTestHook = nil
	var recovery *RecoveryError
	if !errors.As(err, &recovery) || !postTransitionRan || !postRollbackRan {
		t.Fatalf("Restore recovery error = %#v, hooks = %t/%t", err, postTransitionRan, postRollbackRan)
	}
	if recovery.Evidence.Path != "" || !recovery.Evidence.DescriptorPinned || recovery.Evidence.IdentityKnown {
		t.Fatalf("quarantine recovery evidence = %+v", recovery.Evidence)
	}
	if current := quarantine.Record(); current.Path != "" {
		t.Fatalf("Record retained stale path after substitution: %+v", current)
	}
	if quarantine.name != "" || len(quarantine.recovery) != 2 {
		t.Fatalf("quarantine recovery state = name %q pins %d", quarantine.name, len(quarantine.recovery))
	}
	assertGuardOpen(t, quarantine.guardFD)
	for _, pin := range quarantine.recovery {
		assertGuardOpen(t, pin.guardFD)
	}
	wantPaths := map[string]bool{record.Path: false, "restored": false}
	for _, evidence := range recovery.Related {
		if _, exists := wantPaths[evidence.Path]; exists && evidence.IdentityKnown && evidence.DescriptorPinned {
			wantPaths[evidence.Path] = true
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Fatalf("missing replacement recovery evidence for %q: %+v", path, recovery.Related)
		}
	}
}

func TestReadRegularIsBounded(t *testing.T) {
	t.Parallel()
	directory, root := openTestRoot(t)
	writeFile(t, filepath.Join(directory, "file"), "12345", 0o600)
	if _, _, err := root.ReadRegular("file", 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadRegular small limit error = %v, want ErrTooLarge", err)
	}
	content, entry, err := root.ReadRegular("file", 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "12345" || entry.Kind != KindRegular {
		t.Fatalf("ReadRegular = %q, %s", content, entry.Kind)
	}
}

func TestClosedRootRejectsFurtherPathOperations(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lstat("target"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Lstat after Close error = %v, want ErrClosed", err)
	}
}
