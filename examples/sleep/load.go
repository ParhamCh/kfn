package main

import (
	"context"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"time"
)

// Tunable defaults, overridable per-deployment via env and per-request via query string.
// SLEEP_MAX is a hard ceiling: every computed sleep is clamped to it so a single request
// can never pin a worker for longer than the operator allows.
var (
	defaultDuration = envDuration("SLEEP_DEFAULT", 200*time.Millisecond)
	maxDuration     = envDuration("SLEEP_MAX", 60*time.Second)
)

// distribution selects how the base duration is turned into an actual sleep, letting the
// example emulate more than a flat latency: a realistic service has a tail.
type distribution string

const (
	distFixed   distribution = "fixed"   // exactly base
	distUniform distribution = "uniform" // base ± jitter, uniform
	distExp     distribution = "exp"     // exponential with mean = base (models p99 tails)
)

// sleepParams is a fully-resolved, validated request: base duration, distribution and
// jitter, with everything already clamped to safe bounds.
type sleepParams struct {
	base   time.Duration
	dist   distribution
	jitter float64
}

// pick computes the concrete sleep for one invocation from the params, clamped to
// [0, maxDuration]. It is the creative core: the same endpoint yields a flat latency, a
// jittered one, or a heavy-tailed exponential one depending on dist.
func (p sleepParams) pick(r *rand.Rand) time.Duration {
	base := p.base.Seconds()
	var secs float64
	switch p.dist {
	case distUniform:
		// Uniform in base*(1±jitter): a symmetric spread around the target.
		lo := base * (1 - p.jitter)
		hi := base * (1 + p.jitter)
		secs = lo + r.Float64()*(hi-lo)
	case distExp:
		// Exponential with mean = base. ExpFloat64 has mean 1, so scale by base.
		// This produces the occasional long sleep a real latency tail would.
		secs = r.ExpFloat64() * base
	default: // distFixed
		secs = base
	}
	return clampDuration(time.Duration(secs*float64(time.Second)), 0, maxDuration)
}

// sleepResult is the honest summary returned to the caller.
type sleepResult struct {
	RequestedMS  int64   `json:"requested_ms"`
	Distribution string  `json:"distribution"`
	Jitter       float64 `json:"jitter"`
	SleptMS      int64   `json:"slept_ms"`
	Canceled     bool    `json:"canceled"`
	RequestID    string  `json:"request_id,omitempty"`
}

// runSleep performs one cooperative sleep and reports what actually happened. It returns
// promptly if ctx is cancelled (the runtime's INVOKE_TIMEOUT firing, or the client
// disconnecting) rather than holding the worker — the whole point of honoring ctx.
func runSleep(ctx context.Context, p sleepParams, r *rand.Rand) sleepResult {
	d := p.pick(r)
	start := time.Now()
	canceled := !sleepCtx(ctx, d)
	return sleepResult{
		RequestedMS:  d.Milliseconds(),
		Distribution: string(p.dist),
		Jitter:       p.jitter,
		SleptMS:      time.Since(start).Milliseconds(),
		Canceled:     canceled,
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first. It returns true if the full
// duration elapsed, false if it was cut short by cancellation.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// --- small self-contained param helpers (kept local so this example is copy-pasteable) ---

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func clampFloat(f, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, f))
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
