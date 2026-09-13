package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgent55Constants(t *testing.T) {
	if Agent55ID != 55 {
		t.Fatalf("expected Agent55ID 55, got %d", Agent55ID)
	}
	if !strings.Contains(Agent55Name, "Agent 55") {
		t.Fatalf("expected Agent55Name to contain 'Agent 55', got %s", Agent55Name)
	}
	if !strings.Contains(Agent55Name, "Read-only root filesystem bind mount validator") {
		t.Fatalf("expected Agent55Name to contain validator description, got %s", Agent55Name)
	}
	if DefaultMountinfoPath != "/proc/self/mountinfo" {
		t.Fatalf("unexpected DefaultMountinfoPath: %s", DefaultMountinfoPath)
	}
}

func TestParseMountinfoLine(t *testing.T) {
	line1 := "38149 563 259:10 / / ro,nosuid,nodev,relatime master:1 - ext4 /dev/nvme3n1p2 rw"
	m1, err := parseMountinfoLine(line1)
	if err != nil {
		t.Fatalf("failed to parse line1: %v", err)
	}
	if m1.MountID != 38149 || m1.ParentID != 563 {
		t.Fatalf("unexpected IDs: %d, %d", m1.MountID, m1.ParentID)
	}
	if m1.MajorMinor != "259:10" || m1.Root != "/" || m1.MountPoint != "/" {
		t.Fatalf("unexpected root/mountpoint: %+v", m1)
	}
	if !m1.IsReadOnly() {
		t.Fatalf("expected m1 to be read-only")
	}
	if !m1.HasOption("nosuid") || !m1.HasOption("rw") {
		t.Fatalf("expected options nosuid and rw on m1")
	}
	if len(m1.OptionalFields) != 1 || m1.OptionalFields[0] != "master:1" {
		t.Fatalf("unexpected optional fields: %+v", m1.OptionalFields)
	}
	if m1.FSType != "ext4" || m1.MountSource != "/dev/nvme3n1p2" {
		t.Fatalf("unexpected fs/source: %+v", m1)
	}

	line2 := "577 44507 0:2740 / /dev rw,nosuid,nodev,relatime - tmpfs tmpfs rw,mode=755,uid=1000"
	m2, err := parseMountinfoLine(line2)
	if err != nil {
		t.Fatalf("failed to parse line2: %v", err)
	}
	if m2.IsReadOnly() {
		t.Fatalf("expected m2 to be read-write")
	}
	if len(m2.OptionalFields) != 0 {
		t.Fatalf("expected empty optional fields: %+v", m2.OptionalFields)
	}
	if m2.MountPoint != "/dev" || m2.FSType != "tmpfs" {
		t.Fatalf("unexpected mountpoint/fstype: %+v", m2)
	}

	lineEscaped := "100 1 8:1 / /path/with\\040spaces/sub rw - ext4 /dev/sda1 rw"
	mEsc, err := parseMountinfoLine(lineEscaped)
	if err != nil {
		t.Fatalf("failed to parse escaped line: %v", err)
	}
	if mEsc.MountPoint != "/path/with spaces/sub" {
		t.Fatalf("expected unescaped path, got: %s", mEsc.MountPoint)
	}
}

func TestParseMountinfoLine_Malformed(t *testing.T) {
	cases := []string{
		"",
		"1 2 3",
		"not_int 2 0:1 / / ro - ext4 /dev/sda rw",
		"1 not_int 0:1 / / ro - ext4 /dev/sda rw",
		"1 2 0:1 / / ro ext4 /dev/sda rw",
		"1 2 0:1 / / ro shared:1 - ext4",
	}

	for _, c := range cases {
		_, err := parseMountinfoLine(c)
		if err == nil {
			t.Fatalf("expected error for malformed line: %q", c)
		}
	}
}

func TestParseMountinfo_FullTable(t *testing.T) {
	data := `
# Comment line to ignore
1 0 259:1 / / ro,relatime - ext4 /dev/root rw
2 1 0:1 / /proc rw,nosuid,nodev,noexec - proc proc rw
3 1 0:2 / /dev rw,nosuid - devtmpfs udev rw
4 1 259:1 /usr /usr ro,nodev - ext4 /dev/root rw
5 1 0:3 / /tmp rw,nosuid,nodev - tmpfs tmpfs rw
`
	mounts, err := ParseMountinfo(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ParseMountinfo failed: %v", err)
	}
	if len(mounts) != 5 {
		t.Fatalf("expected 5 mounts, got %d", len(mounts))
	}

	_, err = ParseMountinfo(nil)
	if err == nil {
		t.Fatalf("expected error for nil reader")
	}
}

func TestFindEffectiveMount(t *testing.T) {
	mounts := []MountEntry{
		{MountID: 1, MountPoint: "/", MountOptions: []string{"ro"}},
		{MountID: 2, MountPoint: "/usr", MountOptions: []string{"ro"}},
		{MountID: 3, MountPoint: "/usr/local", MountOptions: []string{"rw"}},
		{MountID: 4, MountPoint: "/tmp", MountOptions: []string{"rw"}},
		{MountID: 5, MountPoint: "/tmp", MountOptions: []string{"ro"}},
	}

	m, err := FindEffectiveMount(mounts, "/")
	if err != nil || m.MountID != 1 {
		t.Fatalf("expected mount ID 1 for '/', got %+v, err: %v", m, err)
	}

	m, err = FindEffectiveMount(mounts, "/etc/passwd")
	if err != nil || m.MountID != 1 {
		t.Fatalf("expected mount ID 1 for '/etc/passwd', got %+v, err: %v", m, err)
	}

	m, err = FindEffectiveMount(mounts, "/usr/bin/bash")
	if err != nil || m.MountID != 2 {
		t.Fatalf("expected mount ID 2 for '/usr/bin/bash', got %+v, err: %v", m, err)
	}

	m, err = FindEffectiveMount(mounts, "/usr/local/bin/custom")
	if err != nil || m.MountID != 3 {
		t.Fatalf("expected mount ID 3 for '/usr/local/bin/custom', got %+v, err: %v", m, err)
	}

	m, err = FindEffectiveMount(mounts, "/tmp/scratch")
	if err != nil || m.MountID != 5 {
		t.Fatalf("expected shadowed mount ID 5 for '/tmp/scratch', got %+v, err: %v", m, err)
	}

	m, err = FindEffectiveMount(mounts, "usr/bin")
	if err != nil || m.MountID != 2 {
		t.Fatalf("expected mount ID 2 for 'usr/bin' without leading slash, got %+v, err: %v", m, err)
	}

	_, err = FindEffectiveMount(nil, "/anything")
	if err == nil {
		t.Fatalf("expected error when mount table is empty")
	}
}

func TestValidateMounts_AllReadOnly(t *testing.T) {
	mounts := []MountEntry{
		{MountID: 1, MountPoint: "/", MountOptions: []string{"ro", "relatime"}},
	}
	report := ValidateMounts(mounts, []string{"/", "/usr", "/etc", "/bin"})
	if !report.Valid {
		t.Fatalf("expected report to be valid, got violations: %+v", report.Violations)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(report.Violations))
	}
	if len(report.CheckedPaths) != 4 {
		t.Fatalf("expected 4 checked paths, got %d", len(report.CheckedPaths))
	}
	for _, p := range report.CheckedPaths {
		if !p.ReadOnly || p.Violation {
			t.Fatalf("expected path %s to be read-only and non-violating", p.Path)
		}
	}
}

func TestValidateMounts_RootRWViolation(t *testing.T) {
	mounts := []MountEntry{
		{MountID: 1, MountPoint: "/", MountOptions: []string{"rw", "relatime"}},
	}
	report := ValidateMounts(mounts, []string{"/"})
	if report.Valid {
		t.Fatalf("expected report to be invalid")
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(report.Violations))
	}
	if report.Violations[0].Path != "/" {
		t.Fatalf("expected root violation, got %s", report.Violations[0].Path)
	}
	if !strings.Contains(report.Violations[0].ViolationMsg, "expected MS_RDONLY") {
		t.Fatalf("unexpected violation msg: %s", report.Violations[0].ViolationMsg)
	}
}

func TestValidateMounts_SensitivePathRWViolation(t *testing.T) {
	mounts := []MountEntry{
		{MountID: 1, MountPoint: "/", MountOptions: []string{"ro"}},
		{MountID: 2, MountPoint: "/etc", MountOptions: []string{"rw"}},
	}
	report := ValidateMounts(mounts, []string{"/", "/usr", "/etc"})
	if report.Valid {
		t.Fatalf("expected report to be invalid")
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected 1 violation for /etc, got %d", len(report.Violations))
	}
	if report.Violations[0].Path != "/etc" {
		t.Fatalf("expected /etc violation, got %s", report.Violations[0].Path)
	}
}

func TestMountValidator_Struct(t *testing.T) {
	table := `
1 0 259:1 / / ro,relatime - ext4 /dev/root rw
2 1 259:1 /usr /usr ro,nodev - ext4 /dev/root rw
`
	tmpFile := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(tmpFile, []byte(table), 0644); err != nil {
		t.Fatalf("failed to write tmp mountinfo: %v", err)
	}

	v := NewMountValidator(tmpFile, "/", "/usr")
	entries, err := v.Inspect()
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	report, err := v.Validate()
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if !report.Valid {
		t.Fatalf("expected valid report, got: %+v", report)
	}

	vReader := NewMountValidator("", "/", "/usr")
	readerReport, err := vReader.ValidateReader(strings.NewReader(table))
	if err != nil {
		t.Fatalf("ValidateReader failed: %v", err)
	}
	if !readerReport.Valid {
		t.Fatalf("expected valid reader report")
	}
}

func TestValidateMountinfoFile_Error(t *testing.T) {
	_, err := ValidateMountinfoFile("/nonexistent/path/mountinfo")
	if err == nil {
		t.Fatalf("expected error for non-existent file")
	}
}

func TestValidateSelf(t *testing.T) {
	report, err := ValidateSelf()
	if err != nil {
		t.Fatalf("ValidateSelf failed: %v", err)
	}
	if report.AgentID != 55 {
		t.Fatalf("expected AgentID 55, got %d", report.AgentID)
	}
	if report.TotalMounts == 0 {
		t.Fatalf("expected host to have mounts")
	}
}

func TestSandbox_ValidateMounts_Integration(t *testing.T) {
	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	report, err := sb.ValidateMounts(ctx)
	if err != nil {
		t.Fatalf("sb.ValidateMounts failed: %v", err)
	}
	if !report.Valid {
		t.Fatalf("expected sandbox mounts to be valid read-only, got violations: %+v", report.Violations)
	}
	if report.TotalMounts == 0 {
		t.Fatalf("expected > 0 container mounts, got %d", report.TotalMounts)
	}

	for _, p := range report.CheckedPaths {
		if !p.ReadOnly {
			t.Fatalf("expected sensitive path %s to be read-only in sandbox", p.Path)
		}
	}
}

func TestSandbox_ValidateMounts_ViolationDetected(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Mounts = []Mount{
		{
			Type:        MountTypeBind,
			Source:      tmpDir,
			Destination: "/etc/skel",
		},
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	report, err := sb.ValidateMounts(ctx, "/etc/skel")
	if err != nil {
		t.Fatalf("sb.ValidateMounts failed: %v", err)
	}
	if report.Valid {
		t.Fatalf("expected violation for read-write bind mount on /etc/violation_target")
	}
	if len(report.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(report.Violations))
	}
	if report.Violations[0].ReadOnly {
		t.Fatalf("expected ReadOnly=false for bind mount")
	}
}
