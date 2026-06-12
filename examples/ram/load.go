package main

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Tunable defaults, overridable per-deployment via env and per-request via query string.
// RAM_MAX_MB is a hard ceiling kept below the pod's memory limit so a single request can
// never OOM-kill the pod (see the README for opt-in OOM testing).
var (
	defaultMB  = envInt("RAM_DEFAULT_MB", 64)
	maxMB      = envInt("RAM_MAX_MB", 160)
	maxHold    = envDuration("RAM_MAX_HOLD", 60*time.Second)
	maxRamp    = envDuration("RAM_MAX_RAMP", 30*time.Second)
	pageSize   = os.Getpagesize()
	chunkBytes = 1 << 20 // 1 MiB allocation granularity
)

// ramParams is a fully-resolved, validated request.
type ramParams struct {
	mb   int
	hold time.Duration
	ramp time.Duration // if > 0, allocate gradually over this window (a slow-leak emulation)
}

// ramResult is the honest summary returned to the caller.
type ramResult struct {
	RequestedMB int    `json:"requested_mb"`
	AllocatedMB int    `json:"allocated_mb"`
	RampMS      int64  `json:"ramp_ms"`
	HeldMS      int64  `json:"held_ms"`
	RSSBeforeMB int    `json:"rss_before_mb"`
	RSSPeakMB   int    `json:"rss_peak_mb"`
	RSSAfterMB  int    `json:"rss_after_mb"`
	Canceled    bool   `json:"canceled"`
	RequestID   string `json:"request_id,omitempty"`
}

// runRAM allocates p.mb of resident memory (optionally ramped), holds it for p.hold, then
// releases it and returns the OS memory. It honors ctx throughout so INVOKE_TIMEOUT or a
// client disconnect frees the memory promptly instead of holding it for nothing.
func runRAM(ctx context.Context, p ramParams) ramResult {
	rssBefore := readRSS()

	allocStart := time.Now()
	chunks, canceled := allocate(ctx, p.mb, p.ramp)
	rampMS := time.Since(allocStart).Milliseconds()
	allocatedMB := len(chunks)

	rssPeak := readRSS()

	// Hold the allocation (keeping it reachable so the GC can't reclaim it) for p.hold.
	heldStart := time.Now()
	if !canceled {
		canceled = !sleepCtx(ctx, p.hold)
	}
	runtime.KeepAlive(chunks)
	heldMS := time.Since(heldStart).Milliseconds()

	// Release and hand the pages back to the OS so the drop is visible in the after reading.
	chunks = nil
	debug.FreeOSMemory()
	rssAfter := readRSS()

	return ramResult{
		RequestedMB: p.mb,
		AllocatedMB: allocatedMB,
		RampMS:      rampMS,
		HeldMS:      heldMS,
		RSSBeforeMB: toMB(rssBefore),
		RSSPeakMB:   toMB(rssPeak),
		RSSAfterMB:  toMB(rssAfter),
		Canceled:    canceled,
	}
}

// allocate builds mb 1 MiB chunks, touching every page so the memory truly commits to RSS.
// With ramp > 0 the chunks are added gradually across the window (sleeping between each) to
// emulate a slow leak. Returns the chunks (so the caller can keep them alive) and whether
// allocation was cut short by ctx cancellation.
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
		// Touch one byte per page so the kernel actually backs it with physical memory.
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

// readRSS returns the process resident set size in bytes. On Linux it reads
// /proc/self/statm (whose second field is the resident page count); elsewhere it falls back
// to the Go runtime's view of memory obtained from the OS.
func readRSS() uint64 {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) >= 2 {
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

// --- small self-contained param helpers (kept local so this example is copy-pasteable) ---

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

func parseInt(s string) (int, error)                { return strconv.Atoi(s) }
func parseDuration(s string) (time.Duration, error) { return time.ParseDuration(s) }

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
