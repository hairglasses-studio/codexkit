package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	Agent55ID            = 55
	Agent55Name          = "Agent 55: Read-only root filesystem bind mount validator"
	DefaultMountinfoPath = "/proc/self/mountinfo"
)

var DefaultSensitivePaths = []string{
	"/",
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/etc",
	"/boot",
}

type MountEntry struct {
	MountID        int      `json:"mount_id"`
	ParentID       int      `json:"parent_id"`
	MajorMinor     string   `json:"major_minor"`
	Root           string   `json:"root"`
	MountPoint     string   `json:"mount_point"`
	MountOptions   []string `json:"mount_options"`
	OptionalFields []string `json:"optional_fields"`
	FSType         string   `json:"fs_type"`
	MountSource    string   `json:"mount_source"`
	SuperOptions   []string `json:"super_options"`
}

func (m *MountEntry) IsReadOnly() bool {
	for _, opt := range m.MountOptions {
		if opt == "ro" {
			return true
		}
	}
	return false
}

func (m *MountEntry) HasOption(target string) bool {
	for _, opt := range m.MountOptions {
		if opt == target {
			return true
		}
	}
	for _, opt := range m.SuperOptions {
		if opt == target {
			return true
		}
	}
	return false
}

type PathValidation struct {
	Path         string   `json:"path"`
	MountPoint   string   `json:"mount_point"`
	MountID      int      `json:"mount_id"`
	FSType       string   `json:"fs_type"`
	MountSource  string   `json:"mount_source"`
	MountOptions []string `json:"mount_options"`
	ReadOnly     bool     `json:"read_only"`
	Violation    bool     `json:"violation"`
	ViolationMsg string   `json:"violation_msg,omitempty"`
}

type ValidationReport struct {
	AgentID       int              `json:"agent_id"`
	AgentName     string           `json:"agent_name"`
	MountinfoPath string           `json:"mountinfo_path"`
	TotalMounts   int              `json:"total_mounts"`
	TotalChecked  int              `json:"total_checked"`
	Valid         bool             `json:"valid"`
	Violations    []PathValidation `json:"violations,omitempty"`
	CheckedPaths  []PathValidation `json:"checked_paths"`
}

type MountValidator struct {
	mountinfoPath  string
	sensitivePaths []string
}

func NewMountValidator(mountinfoPath string, sensitivePaths ...string) *MountValidator {
	if mountinfoPath == "" {
		mountinfoPath = DefaultMountinfoPath
	}
	paths := sensitivePaths
	if len(paths) == 0 {
		paths = make([]string, len(DefaultSensitivePaths))
		copy(paths, DefaultSensitivePaths)
	}
	return &MountValidator{
		mountinfoPath:  mountinfoPath,
		sensitivePaths: paths,
	}
}

func (v *MountValidator) Inspect() ([]MountEntry, error) {
	return ParseMountinfoFile(v.mountinfoPath)
}

func (v *MountValidator) Validate() (*ValidationReport, error) {
	return ValidateMountinfoFile(v.mountinfoPath, v.sensitivePaths...)
}

func (v *MountValidator) ValidateReader(r io.Reader) (*ValidationReport, error) {
	return ValidateMountinfo(r, v.sensitivePaths...)
}

func ParseMountinfo(r io.Reader) ([]MountEntry, error) {
	if r == nil {
		return nil, errors.New("reader cannot be nil")
	}
	var entries []MountEntry
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, err := parseMountinfoLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		entries = append(entries, *entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func ParseMountinfoFile(path string) ([]MountEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseMountinfo(f)
}

func parseMountinfoLine(line string) (*MountEntry, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, fmt.Errorf("insufficient fields (%d < 10): %s", len(fields), line)
	}

	mountID, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("invalid mount ID %q: %w", fields[0], err)
	}
	parentID, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("invalid parent ID %q: %w", fields[1], err)
	}

	majorMinor := fields[2]
	root := unescapeMountPath(fields[3])
	mountPoint := unescapeMountPath(fields[4])

	var mountOptions []string
	if fields[5] != "" {
		mountOptions = strings.Split(fields[5], ",")
	}

	hyphenIdx := -1
	for i := 6; i < len(fields); i++ {
		if fields[i] == "-" {
			hyphenIdx = i
			break
		}
	}
	if hyphenIdx == -1 {
		return nil, fmt.Errorf("missing '-' separator: %s", line)
	}
	if hyphenIdx+2 >= len(fields) {
		return nil, fmt.Errorf("missing fields after '-' separator: %s", line)
	}

	var optionalFields []string
	if hyphenIdx > 6 {
		optionalFields = fields[6:hyphenIdx]
	}

	fsType := fields[hyphenIdx+1]
	mountSource := unescapeMountPath(fields[hyphenIdx+2])

	var superOptions []string
	if hyphenIdx+3 < len(fields) {
		superOptions = strings.Split(fields[hyphenIdx+3], ",")
	}

	return &MountEntry{
		MountID:        mountID,
		ParentID:       parentID,
		MajorMinor:     majorMinor,
		Root:           root,
		MountPoint:     mountPoint,
		MountOptions:   mountOptions,
		OptionalFields: optionalFields,
		FSType:         fsType,
		MountSource:    mountSource,
		SuperOptions:   superOptions,
	}, nil
}

func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var buf strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			val := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
			buf.WriteByte(byte(val))
			i += 4
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

func isOctal(b byte) bool {
	return b >= '0' && b <= '7'
}

func isPathUnder(path, mountPoint string) bool {
	if mountPoint == "/" {
		return true
	}
	if path == mountPoint {
		return true
	}
	return strings.HasPrefix(path, mountPoint+"/")
}

func FindEffectiveMount(mounts []MountEntry, targetPath string) (*MountEntry, error) {
	clean := filepath.Clean(targetPath)
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	var best *MountEntry
	for i := range mounts {
		mp := mounts[i].MountPoint
		if isPathUnder(clean, mp) {
			if best == nil || len(mp) > len(best.MountPoint) || (len(mp) == len(best.MountPoint) && mp == best.MountPoint) {
				best = &mounts[i]
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no mount found covering path %s", targetPath)
	}
	return best, nil
}

func VerifyMountReadOnly(m *MountEntry) bool {
	if m == nil {
		return false
	}
	return m.IsReadOnly()
}

func VerifyPathReadOnly(mounts []MountEntry, targetPath string) (*PathValidation, error) {
	effective, err := FindEffectiveMount(mounts, targetPath)
	if err != nil {
		return nil, err
	}
	isRO := effective.IsReadOnly()
	pv := &PathValidation{
		Path:         targetPath,
		MountPoint:   effective.MountPoint,
		MountID:      effective.MountID,
		FSType:       effective.FSType,
		MountSource:  effective.MountSource,
		MountOptions: effective.MountOptions,
		ReadOnly:     isRO,
		Violation:    !isRO,
	}
	if !isRO {
		pv.ViolationMsg = fmt.Sprintf("path %s (mount point %s, id %d) is read-write, expected MS_RDONLY", targetPath, effective.MountPoint, effective.MountID)
	}
	return pv, nil
}

func ValidateMounts(mounts []MountEntry, sensitivePaths []string) *ValidationReport {
	paths := sensitivePaths
	if len(paths) == 0 {
		paths = DefaultSensitivePaths
	}
	report := &ValidationReport{
		AgentID:       Agent55ID,
		AgentName:     Agent55Name,
		MountinfoPath: DefaultMountinfoPath,
		TotalMounts:   len(mounts),
		TotalChecked:  len(paths),
		Valid:         true,
		Violations:    make([]PathValidation, 0),
		CheckedPaths:  make([]PathValidation, 0, len(paths)),
	}

	for _, p := range paths {
		pv, err := VerifyPathReadOnly(mounts, p)
		if err != nil {
			violation := PathValidation{
				Path:         p,
				ReadOnly:     false,
				Violation:    true,
				ViolationMsg: err.Error(),
			}
			report.Valid = false
			report.Violations = append(report.Violations, violation)
			report.CheckedPaths = append(report.CheckedPaths, violation)
			continue
		}
		report.CheckedPaths = append(report.CheckedPaths, *pv)
		if pv.Violation {
			report.Valid = false
			report.Violations = append(report.Violations, *pv)
		}
	}

	return report
}

func ValidateMountinfo(r io.Reader, sensitivePaths ...string) (*ValidationReport, error) {
	mounts, err := ParseMountinfo(r)
	if err != nil {
		return nil, err
	}
	report := ValidateMounts(mounts, sensitivePaths)
	report.MountinfoPath = "<reader>"
	return report, nil
}

func ValidateMountinfoFile(path string, sensitivePaths ...string) (*ValidationReport, error) {
	mounts, err := ParseMountinfoFile(path)
	if err != nil {
		return nil, err
	}
	report := ValidateMounts(mounts, sensitivePaths)
	report.MountinfoPath = path
	return report, nil
}

func ValidateSelf(sensitivePaths ...string) (*ValidationReport, error) {
	return ValidateMountinfoFile(DefaultMountinfoPath, sensitivePaths...)
}

func (s *Sandbox) ValidateMounts(ctx context.Context, sensitivePaths ...string) (*ValidationReport, error) {
	paths := sensitivePaths
	if len(paths) == 0 {
		paths = DefaultSensitivePaths
	}
	res, err := s.Run(ctx, "cat", DefaultMountinfoPath)
	if err != nil {
		return nil, fmt.Errorf("read container mountinfo: %w", err)
	}
	if !res.Success {
		return nil, fmt.Errorf("read container mountinfo failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	report, err := ValidateMountinfo(strings.NewReader(res.Stdout), paths...)
	if err != nil {
		return nil, err
	}
	report.MountinfoPath = fmt.Sprintf("%s (%s)", DefaultMountinfoPath, s.InstanceID())
	return report, nil
}
