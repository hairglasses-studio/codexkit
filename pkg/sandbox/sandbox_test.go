package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

func TestBubblewrapInstalled(t *testing.T) {
	out, err := exec.Command("bwrap", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("bwrap --version failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "bubblewrap") {
		t.Fatalf("unexpected version output: %s", s)
	}
}

func TestInvariant1_ReadOnlyRoot(t *testing.T) {
	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := sb.RunScript(ctx, "touch /test_root_file 2>&1")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected write to root to fail, got exit 0")
	}
	output := res.Stderr + res.Stdout
	if !strings.Contains(output, "Read-only file system") {
		t.Fatalf("expected Read-only file system error, got: %s", output)
	}

	if _, err := os.Stat("/test_root_file"); !os.IsNotExist(err) {
		t.Fatalf("/test_root_file was created on host root")
	}
}

func TestInvariant2_NetworkIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnshareNet = true
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := sb.RunScript(ctx, "ip route show | wc -l")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "0" {
		t.Fatalf("expected 0 network routes in unshared net namespace, got: %s", res.Stdout)
	}

	curlRes, err := sb.RunScript(ctx, "curl -s --connect-timeout 1 http://1.1.1.1 2>&1")
	if err != nil {
		t.Fatalf("RunScript curl invocation failed: %v", err)
	}
	if curlRes.ExitCode == 0 {
		t.Fatalf("expected outbound network connect to fail, but succeeded")
	}
}

func TestInvariant3_TeardownCleanupSLA(t *testing.T) {
	for i := 0; i < 5; i++ {
		cfg := DefaultConfig()
		cfg.EphemeralWorkDir = true
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("iteration %d: New sandbox failed: %v", i, err)
		}
		hostDir := sb.WorkDir()
		if _, err := os.Stat(hostDir); os.IsNotExist(err) {
			t.Fatalf("iteration %d: host work dir does not exist before run", i)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		testFile := filepath.Join(hostDir, "payload.txt")
		if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
			cancel()
			t.Fatalf("write payload: %v", err)
		}

		res, err := sb.RunScript(ctx, "echo -n test > payload.txt")
		cancel()
		if err != nil || !res.Success {
			t.Fatalf("iteration %d: run script in sandbox failed: %v", i, err)
		}

		tdDuration, err := sb.Teardown()
		if err != nil {
			t.Fatalf("iteration %d: teardown failed: %v", i, err)
		}
		if tdDuration > 50*time.Millisecond {
			t.Fatalf("iteration %d: teardown SLA ceiling exceeded: %v > 50ms", i, tdDuration)
		}

		if _, err := os.Stat(hostDir); !os.IsNotExist(err) {
			t.Fatalf("iteration %d: ephemeral dir still exists after teardown: %s", i, hostDir)
		}
	}
}

func TestEphemeralWorkspace(t *testing.T) {
	cfg := DefaultConfig()
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	script := `
mkdir -p sub
echo "hello from micro container" > sub/message.txt
cat sub/message.txt
`
	res, err := sb.RunScript(ctx, script)
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("script execution failed: exit %d, stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello from micro container") {
		t.Fatalf("unexpected stdout: %s", res.Stdout)
	}
}

func TestSeccompDeniedSyscall(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableSeccomp = true
	cfg.DeniedSyscalls = []uint32{sysPtrace}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pyCode := `
import ctypes
libc = ctypes.CDLL(None, use_errno=True)
res = libc.ptrace(0, 0, 0, 0)
err = ctypes.get_errno()
print(f"res={res} errno={err}")
`
	res, err := sb.Run(ctx, "python3", "-c", pyCode)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("python execution failed: exit %d, stderr: %s", res.ExitCode, res.Stderr)
	}
	out := strings.TrimSpace(res.Stdout)
	if out != "res=-1 errno=1" {
		t.Fatalf("expected ptrace to return EPERM (errno=1), got: %s", out)
	}
}

func TestModuleTools(t *testing.T) {
	reg := codexkit.NewRegistry()
	mod := Module()
	if mod.Name() != "sandbox" {
		t.Fatalf("expected module name 'sandbox', got: %s", mod.Name())
	}
	if err := reg.Register(mod); err != nil {
		t.Fatalf("failed to register module: %v", err)
	}

	checkRes, err := reg.Call("sandbox_check", map[string]any{})
	if err != nil {
		t.Fatalf("sandbox_check failed: %v", err)
	}
	report, ok := checkRes.(*HealthReport)
	if !ok {
		t.Fatalf("expected *HealthReport, got: %T", checkRes)
	}
	if !report.Healthy {
		t.Fatalf("sandbox reported unhealthy: %+v", report)
	}

	scriptRes, err := reg.Call("sandbox_script", map[string]any{
		"script": "echo 'module test'",
	})
	if err != nil {
		t.Fatalf("sandbox_script failed: %v", err)
	}
	runRes, ok := scriptRes.(*RunResult)
	if !ok {
		t.Fatalf("expected *RunResult, got: %T", scriptRes)
	}
	if !runRes.Success || strings.TrimSpace(runRes.Stdout) != "module test" {
		t.Fatalf("unexpected script result: %+v", runRes)
	}

	benchRes, err := reg.Call("sandbox_benchmark", map[string]any{
		"iterations": float64(5),
	})
	if err != nil {
		t.Fatalf("sandbox_benchmark failed: %v", err)
	}
	benchReport, ok := benchRes.(*BenchmarkReport)
	if !ok {
		t.Fatalf("expected *BenchmarkReport, got: %T", benchRes)
	}
	if !benchReport.TeardownSLAOK || !benchReport.ErrorRateSLAOK {
		t.Fatalf("benchmark SLA not met: %+v", benchReport)
	}
}

func TestBenchmarkRun(t *testing.T) {
	report, err := RunBenchmark(5)
	if err != nil {
		t.Fatalf("RunBenchmark failed: %v", err)
	}
	if !report.TeardownSLAOK {
		t.Fatalf("teardown SLA failed: max %v ms > 50ms", report.MaxTeardownMs)
	}
	if !report.ErrorRateSLAOK {
		t.Fatalf("error rate SLA failed: %v > %v", report.ErrorRate, report.ErrorRateCeiling)
	}
}

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func TestAgent51_Constants(t *testing.T) {
	if Agent51ID != 51 {
		t.Fatalf("expected Agent51ID 51, got %d", Agent51ID)
	}
	expectedName := "Agent 51: Bubblewrap unprivileged user namespace runtime manager"
	if Agent51Name != expectedName {
		t.Fatalf("expected Agent51Name %q, got %q", expectedName, Agent51Name)
	}

	cfg := DefaultConfig()
	sb, err := NewRuntimeManager(cfg)
	if err != nil {
		t.Fatalf("NewRuntimeManager failed: %v", err)
	}
	defer sb.Close()

	if sb.AgentID() != 51 {
		t.Fatalf("expected sb.AgentID() 51, got %d", sb.AgentID())
	}
	if sb.AgentName() != expectedName {
		t.Fatalf("expected sb.AgentName() %q, got %q", expectedName, sb.AgentName())
	}
}

func TestAgent51_BwrapArgGenerator_DefaultArgs(t *testing.T) {
	cfg := DefaultConfig()
	gen := NewBwrapArgGenerator(cfg)

	args, files, err := gen.GenerateArgs([]string{"echo", "test"})
	if err != nil {
		t.Fatalf("GenerateArgs failed: %v", err)
	}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	requiredFlags := []string{
		"--unshare-all",
		"--ro-bind",
		"--tmpfs",
		"--die-with-parent",
		"--",
		"echo",
		"test",
	}
	for _, flag := range requiredFlags {
		if !containsArg(args, flag) {
			t.Fatalf("expected flag %q in args, got: %v", flag, args)
		}
	}
}

func TestAgent51_BwrapArgGenerator_FluentBuilder(t *testing.T) {
	gen := NewBwrapArgGenerator(Config{}).
		WithUnshareAll(true).
		WithDieWithParent(true).
		WithRoBind("/usr", "/usr").
		WithTmpfs("/tmp")

	args, files, err := gen.GenerateArgs([]string{"/bin/true"})
	if err != nil {
		t.Fatalf("GenerateArgs failed: %v", err)
	}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	expected := []string{"--unshare-all", "--die-with-parent", "--ro-bind", "--tmpfs"}
	for _, exp := range expected {
		if !containsArg(args, exp) {
			t.Fatalf("expected flag %q in args, got: %v", exp, args)
		}
	}
}

func TestAgent51_BwrapArgGenerator_UnshareAllDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnshareAll = false

	gen := NewBwrapArgGenerator(cfg)
	args, files, err := gen.GenerateArgs([]string{"/bin/true"})
	if err != nil {
		t.Fatalf("GenerateArgs failed: %v", err)
	}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	if containsArg(args, "--unshare-all") {
		t.Fatalf("expected --unshare-all to be absent when UnshareAll is false")
	}
	expected := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-net", "--die-with-parent"}
	for _, exp := range expected {
		if !containsArg(args, exp) {
			t.Fatalf("expected flag %q when UnshareAll is false, got: %v", exp, args)
		}
	}
}

func TestAgent51_BwrapArgGenerator_ShareNet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnshareAll = true
	cfg.ShareNet = true

	args, files, err := NewBwrapArgGenerator(cfg).GenerateArgs([]string{"/bin/true"})
	if err != nil {
		t.Fatalf("GenerateArgs failed: %v", err)
	}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	if !containsArg(args, "--unshare-all") {
		t.Fatalf("expected --unshare-all in args")
	}
	if !containsArg(args, "--share-net") {
		t.Fatalf("expected --share-net in args when ShareNet is true")
	}
}

func TestAgent51_BwrapArgGenerator_EmptyCommand(t *testing.T) {
	gen := NewBwrapArgGenerator(DefaultConfig())
	if _, _, err := gen.GenerateArgs(nil); err == nil {
		t.Fatalf("expected error for nil command")
	}
	if _, _, err := gen.GenerateArgs([]string{}); err == nil {
		t.Fatalf("expected error for empty command slice")
	}
}

func TestAgent51_RuntimeManagerRun(t *testing.T) {
	sb, err := NewRuntimeManager(DefaultConfig())
	if err != nil {
		t.Fatalf("NewRuntimeManager failed: %v", err)
	}
	defer sb.Close()

	res, err := sb.Run(context.Background(), "/bin/sh", "-c", "echo agent-51-runtime")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got exit code %d: %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "agent-51-runtime" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if res.AgentID != 51 {
		t.Fatalf("expected RunResult.AgentID 51, got %d", res.AgentID)
	}
}

func TestAgent51_UnprivilegedUserNamespace(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UID = 1001
	cfg.GID = 1001

	sb, err := NewRuntimeManager(cfg)
	if err != nil {
		t.Fatalf("NewRuntimeManager failed: %v", err)
	}
	defer sb.Close()

	res, err := sb.Run(context.Background(), "id", "-u")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got exit code %d: %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "1001" {
		t.Fatalf("expected uid 1001 inside namespace, got: %q", res.Stdout)
	}
}

func TestAgent51_DieWithParentAndTmpfs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TmpfsPaths = []string{"/tmp", "/dev/shm"}

	sb, err := NewRuntimeManager(cfg)
	if err != nil {
		t.Fatalf("NewRuntimeManager failed: %v", err)
	}
	defer sb.Close()

	res, err := sb.RunScript(context.Background(), "echo test51 > /tmp/a51.txt && cat /tmp/a51.txt")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got exit code %d: %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "test51" {
		t.Fatalf("expected test51, got: %q", res.Stdout)
	}
}

func TestAgent51_ArgGeneratorAliases(t *testing.T) {
	cfg := DefaultConfig()
	gen1 := NewArgGenerator(cfg)
	gen2 := NewArgumentGenerator(cfg)

	args1, err := gen1.Args("echo", "1")
	if err != nil {
		t.Fatalf("Args failed: %v", err)
	}
	if !containsArg(args1, "--unshare-all") {
		t.Fatalf("expected --unshare-all in args1")
	}

	args2, err := gen2.Args("echo", "2")
	if err != nil {
		t.Fatalf("Args failed: %v", err)
	}
	if !containsArg(args2, "--unshare-all") {
		t.Fatalf("expected --unshare-all in args2")
	}

	argsStandAlone, files, err := GenerateBwrapArgs(cfg, []string{"echo", "3"})
	if err != nil {
		t.Fatalf("GenerateBwrapArgs failed: %v", err)
	}
	for _, f := range files {
		_ = f.Close()
	}
	if !containsArg(argsStandAlone, "--unshare-all") {
		t.Fatalf("expected --unshare-all in argsStandAlone")
	}
}
