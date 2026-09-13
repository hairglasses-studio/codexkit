package sandbox

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestAgent53Constants(t *testing.T) {
	if Agent53ID != 53 {
		t.Fatalf("expected Agent53ID 53, got %d", Agent53ID)
	}
	if Agent53Name != "Agent 53: Seccomp-bpf system call filter generator" {
		t.Fatalf("unexpected Agent53Name: %s", Agent53Name)
	}
}

func TestDefaultAllowedSyscalls(t *testing.T) {
	allowed := DefaultAllowedSyscalls()
	if len(allowed) == 0 {
		t.Fatalf("expected non-empty default allowed syscalls")
	}

	seen := make(map[uint32]bool, len(allowed))
	for _, sc := range allowed {
		if seen[sc] {
			t.Fatalf("duplicate syscall in allowed list: %d (%s)", sc, SyscallName(sc))
		}
		seen[sc] = true
	}

	required := []uint32{
		sysRead,
		sysWrite,
		sysOpenat,
		sysPoll,
		sysFutex,
		sysClose,
		sysMmap,
		sysClone,
		sysExitGroup,
		sysRseq,
	}
	for _, sc := range required {
		if !seen[sc] {
			t.Fatalf("expected syscall %d (%s) in allowed whitelist", sc, SyscallName(sc))
		}
	}

	forbidden := DefaultDeniedSyscalls()
	for _, sc := range forbidden {
		if seen[sc] {
			t.Fatalf("forbidden syscall %d (%s) found in allowed whitelist", sc, SyscallName(sc))
		}
	}
}

func TestSyscallLookup(t *testing.T) {
	cases := []struct {
		nr   uint32
		name string
	}{
		{sysRead, "read"},
		{sysWrite, "write"},
		{sysOpenat, "openat"},
		{sysPoll, "poll"},
		{sysFutex, "futex"},
		{sysClone3, "clone3"},
		{sysRseq, "rseq"},
	}

	for _, tc := range cases {
		if got := SyscallName(tc.nr); got != tc.name {
			t.Fatalf("SyscallName(%d) = %s, want %s", tc.nr, got, tc.name)
		}
		nr, ok := SyscallNumber(tc.name)
		if !ok || nr != tc.nr {
			t.Fatalf("SyscallNumber(%s) = (%d, %v), want (%d, true)", tc.name, nr, ok, tc.nr)
		}
	}

	if got := SyscallName(999999); got != "syscall_999999" {
		t.Fatalf("expected fallback syscall_999999, got %s", got)
	}
	if _, ok := SyscallNumber("nonexistent_syscall"); ok {
		t.Fatalf("expected false for nonexistent syscall name")
	}

	names := DefaultAllowedSyscallNames()
	if len(names) != len(DefaultAllowedSyscalls()) {
		t.Fatalf("length mismatch between allowed syscalls and names")
	}
}

func TestFilterGenerator_Whitelist(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	gen := NewFilterGenerator()
	gen.Allow(sysRead, sysWrite, sysOpenat, sysPoll, sysFutex)

	insns, err := gen.BuildInstructions()
	if err != nil {
		t.Fatalf("BuildInstructions failed: %v", err)
	}

	if len(insns) != 4+(5*2)+1 {
		t.Fatalf("expected %d instructions, got %d", 4+(5*2)+1, len(insns))
	}

	if insns[0].Code != bpfLd|bpfW|bpfAbs || insns[0].K != 4 {
		t.Fatalf("insn 0 unexpected: %+v", insns[0])
	}
	if insns[1].Code != bpfJmp|bpfJeq|bpfK || insns[1].K != auditArchX8664 || insns[1].Jt != 1 || insns[1].Jf != 0 {
		t.Fatalf("insn 1 unexpected: %+v", insns[1])
	}
	if insns[2].Code != bpfRet|bpfK || insns[2].K != seccompRetKillProcess {
		t.Fatalf("insn 2 unexpected: %+v", insns[2])
	}
	if insns[3].Code != bpfLd|bpfW|bpfAbs || insns[3].K != 0 {
		t.Fatalf("insn 3 unexpected: %+v", insns[3])
	}

	offset := 4
	for i := 0; i < 5; i++ {
		jmp := insns[offset+i*2]
		ret := insns[offset+i*2+1]

		if jmp.Code != bpfJmp|bpfJeq|bpfK || jmp.Jt != 0 || jmp.Jf != 1 {
			t.Fatalf("jmp insn %d unexpected: %+v", offset+i*2, jmp)
		}
		if ret.Code != bpfRet|bpfK || ret.K != seccompRetAllow {
			t.Fatalf("ret insn %d unexpected: %+v", offset+i*2+1, ret)
		}
	}

	last := insns[len(insns)-1]
	if last.Code != bpfRet|bpfK || last.K != (seccompRetErrno|errnoEPERM) {
		t.Fatalf("last insn unexpected: %+v", last)
	}
}

func TestFilterGenerator_AllowByName(t *testing.T) {
	gen := NewFilterGenerator()
	if err := gen.AllowByName("read", "write", "openat", "poll", "futex"); err != nil {
		t.Fatalf("AllowByName failed: %v", err)
	}

	allowed := gen.AllowedSyscalls()
	if len(allowed) != 5 {
		t.Fatalf("expected 5 allowed syscalls, got %d", len(allowed))
	}

	if err := gen.AllowByName("invalid_call_xyz"); err == nil {
		t.Fatalf("expected error for invalid syscall name")
	}
}

func TestFilterGenerator_DenyByName(t *testing.T) {
	gen := NewFilterGenerator()
	if err := gen.DenyByName(seccompRetKillProcess, "ptrace"); err != nil {
		t.Fatalf("DenyByName failed: %v", err)
	}

	denied := gen.DeniedSyscalls()
	if denied[sysPtrace] != seccompRetKillProcess {
		t.Fatalf("expected sysPtrace denied with kill process")
	}

	if err := gen.DenyByName(seccompRetKillProcess, "invalid_call_xyz"); err == nil {
		t.Fatalf("expected error for invalid syscall name")
	}
}

func TestFilterGenerator_Remove(t *testing.T) {
	gen := NewFilterGenerator()
	gen.Allow(sysRead, sysWrite)
	if len(gen.AllowedSyscalls()) != 2 {
		t.Fatalf("expected 2 allowed")
	}

	gen.Remove(sysRead)
	allowed := gen.AllowedSyscalls()
	if len(allowed) != 1 || allowed[0] != sysWrite {
		t.Fatalf("expected only sysWrite remaining, got: %+v", allowed)
	}
}

func TestFilterGenerator_EmptyWhitelist(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	gen := NewFilterGenerator()
	_, err := gen.BuildInstructions()
	if err == nil {
		t.Fatalf("expected error building empty whitelist generator")
	}
}

func TestFilterGenerator_BlacklistMode(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	gen := NewBlacklistGenerator()
	gen.Deny(seccompRetErrno|errnoEPERM, sysPtrace)

	insns, err := gen.BuildInstructions()
	if err != nil {
		t.Fatalf("BuildInstructions failed: %v", err)
	}

	if len(insns) != 4+2+1 {
		t.Fatalf("expected 7 instructions, got %d", len(insns))
	}

	last := insns[len(insns)-1]
	if last.Code != bpfRet|bpfK || last.K != seccompRetAllow {
		t.Fatalf("expected fallback allow in blacklist mode, got: %+v", last)
	}
}

func TestFilterGenerator_BuildBinary(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	gen := NewFilterGenerator()
	gen.Allow(sysRead, sysWrite, sysOpenat, sysPoll, sysFutex)

	data, err := gen.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("expected non-empty filter binary")
	}
	if len(data)%8 != 0 {
		t.Fatalf("filter binary length %d is not a multiple of 8", len(data))
	}

	numInsns := len(data) / 8
	reader := bytes.NewReader(data)
	decoded := make([]SockFilter, numInsns)
	for i := 0; i < numInsns; i++ {
		if err := binary.Read(reader, binary.LittleEndian, &decoded[i]); err != nil {
			t.Fatalf("failed decoding insn %d: %v", i, err)
		}
	}

	origInsns, _ := gen.BuildInstructions()
	for i := range decoded {
		if decoded[i] != origInsns[i] {
			t.Fatalf("decoded instruction %d mismatch: got %+v, want %+v", i, decoded[i], origInsns[i])
		}
	}
}

func TestBuildDefaultWhitelistFilter(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	data, err := BuildDefaultWhitelistFilter()
	if err != nil {
		t.Fatalf("BuildDefaultWhitelistFilter failed: %v", err)
	}
	if len(data) == 0 || len(data)%8 != 0 {
		t.Fatalf("invalid filter data length: %d", len(data))
	}
}

func TestBuildSeccompFilter(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	denied := DefaultDeniedSyscalls()
	data, err := BuildSeccompFilter(denied)
	if err != nil {
		t.Fatalf("BuildSeccompFilter failed: %v", err)
	}
	if len(data) == 0 || len(data)%8 != 0 {
		t.Fatalf("invalid filter data length: %d", len(data))
	}

	tooMany := make([]uint32, 201)
	_, err = BuildSeccompFilter(tooMany)
	if err == nil {
		t.Fatalf("expected error for > 200 denied syscalls")
	}
}

func TestFilterGenerator_CustomActions(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}

	gen := NewFilterGenerator()
	gen.SetArch(auditArchX8664)
	gen.SetBadArchAction(seccompRetTrap)
	gen.SetDefaultAction(seccompRetErrno | errnoEACCES)
	gen.Allow(sysRead)

	insns, err := gen.BuildInstructions()
	if err != nil {
		t.Fatalf("BuildInstructions failed: %v", err)
	}

	if insns[2].K != seccompRetTrap {
		t.Fatalf("expected bad arch trap action, got %x", insns[2].K)
	}
	last := insns[len(insns)-1]
	if last.K != (seccompRetErrno | errnoEACCES) {
		t.Fatalf("expected fallback EACCES, got %x", last.K)
	}
}

func TestSeccompWhitelist_BubblewrapExecution(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}

	filterBytes, err := BuildDefaultWhitelistFilter()
	if err != nil {
		t.Fatalf("BuildDefaultWhitelistFilter failed: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer r.Close()

	if _, err := w.Write(filterBytes); err != nil {
		w.Close()
		t.Fatalf("write filter bytes: %v", err)
	}
	w.Close()

	cmd := exec.Command(bwrapPath,
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/bin", "/bin",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--seccomp", "3",
		"--",
		"python3", "-c", "import os; print('seccomp_whitelist_active')",
	)
	cmd.ExtraFiles = []*os.File{r}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bwrap command failed: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(out), "seccomp_whitelist_active") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestSeccompWhitelist_BlocksNonWhitelisted(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("amd64 only")
	}
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed")
	}

	filterBytes, err := BuildDefaultWhitelistFilter()
	if err != nil {
		t.Fatalf("BuildDefaultWhitelistFilter failed: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer r.Close()

	if _, err := w.Write(filterBytes); err != nil {
		w.Close()
		t.Fatalf("write filter bytes: %v", err)
	}
	w.Close()

	pyCode := `
import ctypes
libc = ctypes.CDLL(None, use_errno=True)
res = libc.ptrace(0, 0, 0, 0)
err = ctypes.get_errno()
print(f"res={res} errno={err}")
`
	cmd := exec.Command(bwrapPath,
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/bin", "/bin",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--seccomp", "3",
		"--",
		"python3", "-c", pyCode,
	)
	cmd.ExtraFiles = []*os.File{r}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bwrap command failed: %v, output: %s", err, string(out))
	}
	s := strings.TrimSpace(string(out))
	if s != "res=-1 errno=1" {
		t.Fatalf("expected blocked syscall to return res=-1 errno=1, got: %s", s)
	}
}

func decodeFilters(r io.Reader) ([]SockFilter, error) {
	var filters []SockFilter
	for {
		var f SockFilter
		err := binary.Read(r, binary.LittleEndian, &f)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, nil
}
