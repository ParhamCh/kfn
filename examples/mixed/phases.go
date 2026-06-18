package main

import (
	"context"
	"crypto/sha256"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dutyTick   = 100 * time.Millisecond // CPU duty-cycle granularity
	chunkBytes = 1 << 20                // 1 MiB memory allocation granularity
)

var pageSize = os.Getpagesize()

// Latency distribution names (from examples/sleep).
const (
	distFixed   = "fixed"
	distUniform = "uniform"
	distExp     = "exp"
)

// --- CPU load (from examples/cpu) ---

// cpuPhase burns CPU for dur across workers goroutines at the given duty cycle and returns
// the total hash ops completed. Bounded by its own deadline; honors the parent ctx.
func cpuPhase(ctx context.Context, dur time.Duration, workers int, load float64) uint64 {
	ctx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()

	counts := make([]uint64, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			counts[slot] = burn(ctx, load)
		}(i)
	}
	wg.Wait()

	var total uint64
	for _, c := range counts {
		total += c
	}
	return total
}

// burn runs the CPU-bound work loop until ctx is done, honoring the duty cycle.
func burn(ctx context.Context, load float64) uint64 {
	var buf [64]byte
	var ops uint64
	burnSlice := time.Duration(load * float64(dutyTick))
	for {
		if ctx.Err() != nil {
			return ops
		}
		sliceEnd := time.Now().Add(burnSlice)
		for time.Now().Before(sliceEnd) {
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

// --- Memory load (from examples/ram) ---

// allocate builds mb 1 MiB chunks, touching every page so the memory commits to RSS. With
// ramp > 0 the chunks are added gradually. Returns the chunks and whether ctx cancelled it.
func allocate(ctx context.Context, mb int, ramp time.Duration) ([][]byte, bool) {
	chunks := make([][]byte, 0, mb)
	var step time.Duration
	if ramp > 0 && mb > 0 {
		step = ramp / time.Duration(mb)
	}
	for range mb {
		if ctx.Err() != nil {
			return chunks, true
		}
		c := make([]byte, chunkBytes)
		for off := 0; off < len(c); off += pageSize {
			c[off] = 1
		}
		chunks = append(chunks, c)
		if step > 0 {
			if !sleepCtx(ctx, step) {
				return chunks, true
			}
		}
	}
	return chunks, false
}

// readRSS returns the process resident set size in bytes (Linux /proc/self/statm; falls
// back to the Go runtime's view elsewhere).
func readRSS() uint64 {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages * uint64(pageSize)
			}
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}

func toMB(b uint64) int { return int(b / (1 << 20)) }

// --- Latency load (from examples/sleep) ---

// pickSleep turns a base duration into an actual sleep per the distribution, clamped to max.
func pickSleep(base time.Duration, dist string, jitter float64, max time.Duration, r *rand.Rand) time.Duration {
	b := base.Seconds()
	var secs float64
	switch dist {
	case distUniform:
		lo := b * (1 - jitter)
		hi := b * (1 + jitter)
		secs = lo + r.Float64()*(hi-lo)
	case distExp:
		secs = r.ExpFloat64() * b
	default:
		secs = b
	}
	return clampDuration(time.Duration(secs*float64(time.Second)), 0, max)
}

// sleepCtx sleeps for d unless ctx is cancelled first; true if the full duration elapsed.
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

// --- shared helpers ---

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
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

func clampFloat(f, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, f)) }

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
