package sandbox

import (
	"time"
)

type RunResult struct {
	InstanceID string        `json:"instance_id"`
	AgentID    int           `json:"agent_id,omitempty"`
	Command    []string      `json:"command"`
	ExitCode   int           `json:"exit_code"`
	Stdout     string        `json:"stdout"`
	Stderr     string        `json:"stderr"`
	Duration   time.Duration `json:"duration"`
	DurationMs float64       `json:"duration_ms"`
	TeardownMs float64       `json:"teardown_ms"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
}
