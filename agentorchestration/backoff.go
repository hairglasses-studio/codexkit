package agentorchestration

import (
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"time"
)

// JitterFunc returns a deterministic value in [-1, 1] for a task and completed
// attempt. Injection keeps tests hermetic and avoids global randomness.
type JitterFunc func(taskID string, attempt int) float64

// DeterministicJitter returns a stable FNV-based jitter function. It is harness
// scheduling behavior, not a provider retry recommendation.
func DeterministicJitter(seed string) JitterFunc {
	return func(taskID string, attempt int) float64 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(seed))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(taskID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.Itoa(attempt)))
		unit := float64(h.Sum64()) / float64(math.MaxUint64)
		return unit*2 - 1
	}
}

// BackoffDelay returns the delay after completed attempt, before the next
// attempt. completedAttempt is one-based and must leave another budgeted
// attempt available.
func BackoffDelay(taskID string, completedAttempt int, budget RetryBudget, jitter JitterFunc) (time.Duration, error) {
	if completedAttempt < 1 || completedAttempt >= budget.MaxAttempts {
		return 0, fmt.Errorf("completed attempt %d leaves no budgeted retry", completedAttempt)
	}
	if budget.BaseBackoff <= 0 || budget.MaxBackoff < budget.BaseBackoff {
		return 0, fmt.Errorf("invalid backoff bounds")
	}
	if budget.JitterFraction < 0 || budget.JitterFraction > 1 {
		return 0, fmt.Errorf("jitter fraction must be in [0,1]")
	}

	delay := budget.BaseBackoff
	for n := 1; n < completedAttempt && delay < budget.MaxBackoff; n++ {
		if delay > budget.MaxBackoff/2 {
			delay = budget.MaxBackoff
			break
		}
		delay *= 2
	}
	if delay > budget.MaxBackoff {
		delay = budget.MaxBackoff
	}
	if jitter == nil || budget.JitterFraction == 0 {
		return delay, nil
	}
	value := jitter(taskID, completedAttempt)
	if value < -1 {
		value = -1
	}
	if value > 1 {
		value = 1
	}
	jittered := time.Duration(float64(delay) * (1 + value*budget.JitterFraction))
	if jittered < 0 {
		jittered = 0
	}
	if jittered > budget.MaxBackoff {
		jittered = budget.MaxBackoff
	}
	return jittered, nil
}
