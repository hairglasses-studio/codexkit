package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hairglasses-studio/codexkit"
)

type module struct{}

func Module() codexkit.ToolModule {
	return &module{}
}

func (m *module) Name() string {
	return "sandbox"
}

func (m *module) Init() error {
	return nil
}

func (m *module) Tools() []codexkit.ToolDef {
	return []codexkit.ToolDef{
		{
			Name:        "sandbox_check",
			Description: "Verify host Bubblewrap sandbox prerequisites and invariant enforcement",
			Annotations: codexkit.ToolAnnotations(true, false, true, false),
			Schema: map[string]any{
				"type": "object",
			},
			Handler: func(params map[string]any) (any, error) {
				return CheckHealth()
			},
		},
		{
			Name:        "sandbox_exec",
			Description: "Execute a command inside an ephemeral Bubblewrap micro-container",
			Annotations: codexkit.ToolAnnotations(false, false, false, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"work_dir": map[string]any{
						"type": "string",
					},
					"enable_network": map[string]any{
						"type": "boolean",
					},
					"enable_seccomp": map[string]any{
						"type": "boolean",
					},
					"timeout_seconds": map[string]any{
						"type": "integer",
					},
				},
				"required": []string{"command"},
			},
			Handler: func(params map[string]any) (any, error) {
				rawCmd, ok := params["command"].([]any)
				if !ok || len(rawCmd) == 0 {
					return nil, fmt.Errorf("command array is required and cannot be empty")
				}
				cmdList := make([]string, len(rawCmd))
				for i, v := range rawCmd {
					cmdList[i] = fmt.Sprint(v)
				}

				workDir, _ := params["work_dir"].(string)
				enableNet, _ := params["enable_network"].(bool)
				enableSeccomp := true
				if val, ok := params["enable_seccomp"].(bool); ok {
					enableSeccomp = val
				}

				timeoutSec := 30
				if val, ok := params["timeout_seconds"].(float64); ok && val > 0 {
					timeoutSec = int(val)
				}

				cfg := DefaultConfig()
				cfg.HostWorkDir = workDir
				cfg.EphemeralWorkDir = (workDir == "")
				cfg.UnshareNet = !enableNet
				cfg.EnableSeccomp = enableSeccomp
				cfg.Timeout = time.Duration(timeoutSec) * time.Second

				sb, err := New(cfg)
				if err != nil {
					return nil, err
				}
				defer sb.Close()

				ctx := context.Background()
				return sb.Run(ctx, cmdList...)
			},
		},
		{
			Name:        "sandbox_script",
			Description: "Execute a shell script inside an ephemeral Bubblewrap micro-container",
			Annotations: codexkit.ToolAnnotations(false, false, false, true),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"script": map[string]any{
						"type": "string",
					},
					"work_dir": map[string]any{
						"type": "string",
					},
					"timeout_seconds": map[string]any{
						"type": "integer",
					},
				},
				"required": []string{"script"},
			},
			Handler: func(params map[string]any) (any, error) {
				script, ok := params["script"].(string)
				if !ok || strings.TrimSpace(script) == "" {
					return nil, fmt.Errorf("script is required")
				}
				workDir, _ := params["work_dir"].(string)
				timeoutSec := 30
				if val, ok := params["timeout_seconds"].(float64); ok && val > 0 {
					timeoutSec = int(val)
				}

				cfg := DefaultConfig()
				cfg.HostWorkDir = workDir
				cfg.EphemeralWorkDir = (workDir == "")
				cfg.Timeout = time.Duration(timeoutSec) * time.Second

				sb, err := New(cfg)
				if err != nil {
					return nil, err
				}
				defer sb.Close()

				ctx := context.Background()
				return sb.RunScript(ctx, script)
			},
		},
		{
			Name:        "sandbox_benchmark",
			Description: "Run SLA latency and teardown benchmark against sandbox performance ceilings",
			Annotations: codexkit.ToolAnnotations(true, false, false, false),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"iterations": map[string]any{
						"type": "integer",
					},
				},
			},
			Handler: func(params map[string]any) (any, error) {
				iters := 10
				if val, ok := params["iterations"].(float64); ok && val > 0 {
					iters = int(val)
				}
				return RunBenchmark(iters)
			},
		},
	}
}

type HealthReport struct {
	BwrapInstalled       bool    `json:"bwrap_installed"`
	BwrapVersion         string  `json:"bwrap_version"`
	Invariant1ReadOnlyRO bool    `json:"invariant_1_read_only_ro"`
	Invariant2NetUnshare bool    `json:"invariant_2_network_unshared"`
	Invariant3TeardownOK bool    `json:"invariant_3_teardown_ok"`
	TeardownLatencyMs    float64 `json:"teardown_latency_ms"`
	Healthy              bool    `json:"healthy"`
	Message              string  `json:"message,omitempty"`
}

func CheckHealth() (*HealthReport, error) {
	out, err := exec.Command("bwrap", "--version").CombinedOutput()
	if err != nil {
		return &HealthReport{
			BwrapInstalled: false,
			Healthy:        false,
			Message:        err.Error(),
		}, nil
	}
	versionStr := strings.TrimSpace(string(out))

	sb, err := New(DefaultConfig())
	if err != nil {
		return nil, err
	}
	defer sb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roRes, err := sb.RunScript(ctx, "touch /test_inv1 2>&1")
	inv1 := (err == nil && roRes.ExitCode != 0 && strings.Contains(roRes.Stderr+roRes.Stdout, "Read-only file system"))

	netRes, err := sb.RunScript(ctx, "ip route show | wc -l")
	inv2 := (err == nil && strings.TrimSpace(netRes.Stdout) == "0")

	tdDur, err := sb.Teardown()
	tdMs := float64(tdDur.Microseconds()) / 1000.0
	inv3 := (err == nil && tdDur <= 50*time.Millisecond)

	healthy := inv1 && inv2 && inv3

	return &HealthReport{
		BwrapInstalled:       true,
		BwrapVersion:         versionStr,
		Invariant1ReadOnlyRO: inv1,
		Invariant2NetUnshare: inv2,
		Invariant3TeardownOK: inv3,
		TeardownLatencyMs:    tdMs,
		Healthy:              healthy,
	}, nil
}

type BenchmarkReport struct {
	Iterations        int       `json:"iterations"`
	SuccessCount      int       `json:"success_count"`
	ErrorCount        int       `json:"error_count"`
	ErrorRate         float64   `json:"error_rate"`
	ErrorRateCeiling  float64   `json:"error_rate_ceiling"`
	ErrorRateSLAOK    bool      `json:"error_rate_sla_ok"`
	TeardownLatencyMs []float64 `json:"teardown_latency_ms"`
	MaxTeardownMs     float64   `json:"max_teardown_ms"`
	TeardownSLAOK     bool      `json:"teardown_sla_ok"`
}

func RunBenchmark(iterations int) (*BenchmarkReport, error) {
	if iterations <= 0 {
		iterations = 10
	}
	report := &BenchmarkReport{
		Iterations:       iterations,
		ErrorRateCeiling: 0.001,
	}
	ctx := context.Background()
	for i := 0; i < iterations; i++ {
		sb, err := New(DefaultConfig())
		if err != nil {
			report.ErrorCount++
			continue
		}
		res, err := sb.Run(ctx, "/bin/true")
		if err != nil || !res.Success {
			report.ErrorCount++
		} else {
			report.SuccessCount++
		}
		td, tdErr := sb.Teardown()
		if tdErr != nil {
			report.ErrorCount++
		}
		tdMs := float64(td.Microseconds()) / 1000.0
		report.TeardownLatencyMs = append(report.TeardownLatencyMs, tdMs)
		if tdMs > report.MaxTeardownMs {
			report.MaxTeardownMs = tdMs
		}
	}
	report.ErrorRate = float64(report.ErrorCount) / float64(iterations)
	report.ErrorRateSLAOK = report.ErrorRate <= report.ErrorRateCeiling
	report.TeardownSLAOK = report.MaxTeardownMs <= 50.0
	return report, nil
}
