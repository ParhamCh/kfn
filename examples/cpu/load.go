package main

import (
	"context"
	"crypto/sha256"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Tunable defaults, overridable per-deployment via env and per-request via query string.
// CPU_MAX is a hard ceiling on the burn window so a single request can never pin cores for
// longer than the operator allows.
var (
	defaultDuration = envDuration("CPU_DEFAULT", 500*time.Millisecond)
	maxDuration     = envDuration("CPU_MAX", 60*time.Second)
	// maxWorkers bounds parallelism; effective parallelism is further capped by the pod's
	// CPU limit. Defaults to the runtime's GOMAXPROCS.
	maxWorkers = envInt("CPU_MAX_WORKERS", runtime.GOMAXPROCS(0))
)

// dutyTick is the granularity of the duty cycle: within each tick a worker burns for
// load×tick and idles for the remainder. Small enough to feel steady, large enough that
// the burn loop's own overhead is negligible.
const dutyTick = 100 * time.Millisecond

// cpuParams is a fully-resolved, validated request.
type cpuParams struct {
	dur     time.Duration
	workers int
	load    float64 // duty cycle in [0,1]: fraction of wall-clock each worker spends burning
}

// cpuResult is the honest summary returned to the caller.
type cpuResult struct {
	DurationMS int64   `json:"duration_ms"`
	Workers    int     `json:"workers"`
	Load       float64 `json:"load"`
	TotalOps   uint64  `json:"total_ops"`
	OpsPerSec  int64   `json:"ops_per_sec"`
	GOMAXPROCS int     `json:"gomaxprocs"`
	Canceled   bool    `json:"canceled"`
	RequestID  string  `json:"request_id,omitempty"`
}

// runCPU burns CPU across p.workers goroutines for p.dur, honoring the duty cycle, and
// reports the work done. It returns promptly if ctx is cancelled (INVOKE_TIMEOUT firing or
// the client disconnecting) rather than running the cores hot for nothing.
func runCPU(ctx context.Context, p cpuParams) cpuResult {
	burnCtx, cancel := context.WithTimeout(ctx, p.dur)
	defer cancel()

	start := time.Now()
	counts := make([]uint64, p.workers)
	var wg sync.WaitGroup
	for i := range p.workers {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			counts[slot] = burn(burnCtx, p.load)
		}(i)
	}
	wg.Wait()

	elapsed := time.Since(start)
	var total uint64
	for _, c := range counts {
		total += c
	}

	var opsPerSec int64
	if s := elapsed.Seconds(); s > 0 {
		opsPerSec = int64(float64(total) / s)
	}
	return cpuResult{
		DurationMS: p.dur.Milliseconds(),
		Workers:    p.workers,
		Load:       p.load,
		TotalOps:   total,
		OpsPerSec:  opsPerSec,
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		// The burn always runs to its own deadline (burnCtx), so report cancellation only
		// when the *parent* ctx was cancelled — INVOKE_TIMEOUT firing before p.dur, or the
		// client disconnecting. This is exact, even for sub-tick bursts.
		Canceled: ctx.Err() != nil,
	}
}

// burn runs the CPU-bound work loop until ctx is done, honoring the duty cycle, and returns
// the number of hash operations it completed. With load < 1 it burns for load×dutyTick then
// idles the rest of each tick, emulating steady fractional CPU.
func burn(ctx context.Context, load float64) uint64 {
	var buf [64]byte
	var ops uint64
	burnSlice := time.Duration(load * float64(dutyTick))
	for {
		// Check cancellation once per tick before doing a slice of work.
		if ctx.Err() != nil {
			return ops
		}
		sliceEnd := time.Now().Add(burnSlice)
		for time.Now().Before(sliceEnd) {
			// A chunk of real, non-elidable work; re-check ctx every 1024 hashes so we stop
			// promptly without paying a time.Now() per iteration.
			for range 1024 {
				h := sha256.Sum256(buf[:])
				buf[0] = h[0]
				ops++
			}
			if ctx.Err() != nil {
				return ops
			}
		}
		if load < 1 {
			if !sleepCtx(ctx, dutyTick-burnSlice) {
				return ops
			}
		}
	}
}

// --- small self-contained param helpers (kept local so this example is copy-pasteable) ---

// sleepCtx sleeps for d unless ctx is cancelled first; returns true if the full duration
// elapsed, false if cut short.
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

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func clampFloat(f, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, f)) }

func parseFloat(s string) (float64, error)          { return strconv.ParseFloat(s, 64) }
func parseInt(s string) (int, error)                { return strconv.Atoi(s) }
func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
