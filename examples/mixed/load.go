package main

import (
	"context"
	"math/rand/v2"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Caps (hard ceilings) and defaults, overridable via env. Every load param is clamped to
// its cap so one request can't pin the pod beyond what the operator allows.
var (
	maxCPU      = envDuration("MIXED_MAX_CPU", 30*time.Second)
	maxSleep    = envDuration("MIXED_MAX_SLEEP", 30*time.Second)
	maxHold     = envDuration("MIXED_MAX_HOLD", 30*time.Second)
	maxRamp     = envDuration("MIXED_MAX_RAMP", 15*time.Second)
	maxMB       = envInt("MIXED_MAX_MB", 48)
	maxWorkers  = envInt("MIXED_MAX_WORKERS", runtime.GOMAXPROCS(0))
	defaultHold = envDuration("MIXED_DEFAULT_HOLD", 5*time.Second)
)

// mixedParams is a fully-resolved, validated request: the union of the sleep/cpu/ram params.
// A load type runs only when its trigger (cpu>0, mb>0, sleep>0) is set.
type mixedParams struct {
	cpu     time.Duration
	workers int
	load    float64
	mb      int
	hold    time.Duration
	ramp    time.Duration
	sleep   time.Duration
	dist    string
	jitter  float64
}

type cpuResult struct {
	DurationMS int64   `json:"duration_ms"`
	Workers    int     `json:"workers"`
	Load       float64 `json:"load"`
	TotalOps   uint64  `json:"total_ops"`
}

type ramResult struct {
	RequestedMB int `json:"requested_mb"`
	AllocatedMB int `json:"allocated_mb"`
	RSSPeakMB   int `json:"rss_peak_mb"`
}

type sleepResult struct {
	RequestedMS  int64  `json:"requested_ms"`
	Distribution string `json:"distribution"`
	SleptMS      int64  `json:"slept_ms"`
}

// mixedResult reports each load that ran (absent loads are omitted) plus the wall-clock total.
type mixedResult struct {
	CPU       *cpuResult   `json:"cpu,omitempty"`
	RAM       *ramResult   `json:"ram,omitempty"`
	Sleep     *sleepResult `json:"sleep,omitempty"`
	TotalMS   int64        `json:"total_ms"`
	Canceled  bool         `json:"canceled"`
	RequestID string       `json:"request_id,omitempty"`
}

// runMixed runs every requested load concurrently — memory held while CPU burns and the
// latency sleep elapses — and returns a per-type summary. Everything honors ctx, so a
// timeout or disconnect frees the memory and stops the burn promptly.
func runMixed(ctx context.Context, p mixedParams) mixedResult {
	start := time.Now()
	var res mixedResult

	// Memory: allocate up front and hold it for the request.
	var chunks [][]byte
	if p.mb > 0 {
		chunks, _ = allocate(ctx, p.mb, p.ramp)
		res.RAM = &ramResult{RequestedMB: p.mb, AllocatedMB: len(chunks), RSSPeakMB: toMB(readRSS())}
	}

	// CPU, latency, and the memory hold run concurrently; each goroutine writes its own
	// variable, read back only after Wait (so no locking is needed).
	var wg sync.WaitGroup
	var cpuRes *cpuResult
	var sleepRes *sleepResult

	if p.cpu > 0 {
		wg.Go(func() {
			ops := cpuPhase(ctx, p.cpu, p.workers, p.load)
			cpuRes = &cpuResult{DurationMS: p.cpu.Milliseconds(), Workers: p.workers, Load: p.load, TotalOps: ops}
		})
	}
	if p.sleep > 0 {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			target := pickSleep(p.sleep, p.dist, p.jitter, maxSleep, r)
			s := time.Now()
			sleepCtx(ctx, target)
			sleepRes = &sleepResult{RequestedMS: target.Milliseconds(), Distribution: p.dist, SleptMS: time.Since(s).Milliseconds()}
		})
	}
	if p.mb > 0 {
		wg.Go(func() {
			sleepCtx(ctx, p.hold)
			runtime.KeepAlive(chunks)
		})
	}
	wg.Wait()
	res.CPU = cpuRes
	res.Sleep = sleepRes

	// Release the memory back to the OS so the drop is visible.
	if p.mb > 0 {
		chunks = nil
		debug.FreeOSMemory()
	}

	res.TotalMS = time.Since(start).Milliseconds()
	res.Canceled = ctx.Err() != nil
	return res
}
