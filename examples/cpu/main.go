// Command cpu is a kfn load-generator function: it deliberately burns CPU for a
// configurable wall-clock window across N workers, optionally at a fractional duty cycle.
// Drive it to push process_cpu_seconds_total up and to observe CPU throttling against the
// pod's limit — the input for CPU-based autoscaling.
//
//	GET /?duration=2s                  # burn one core for ~2s
//	GET /?duration=2s&workers=4        # burn up to 4 cores
//	GET /?duration=5s&load=0.5         # ~half a core, steady, for 5s
package main

import (
	"context"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		p, err := parseParams(req)
		if err != nil {
			return nil, err
		}
		res := runCPU(ctx, p)
		res.RequestID = runtime.RequestID(ctx)
		return runtime.JSON(200, res)
	})
}

// parseParams resolves the request's query string over the env defaults into a validated,
// clamped cpuParams. Bad input is a 400 rather than a silent default.
func parseParams(req *runtime.Request) (cpuParams, error) {
	q := req.Query

	dur := defaultDuration
	if v := q.Get("duration"); v != "" {
		d, err := parseDuration(v)
		if err != nil || d < 0 {
			return cpuParams{}, runtime.Errorf(400, "invalid duration %q: want a Go duration like 500ms or 2s", v)
		}
		dur = clampDuration(d, 0, maxDuration)
	}

	workers := 1
	if v := q.Get("workers"); v != "" {
		n, err := parseInt(v)
		if err != nil {
			return cpuParams{}, runtime.Errorf(400, "invalid workers %q: want a positive integer", v)
		}
		workers = clampInt(n, 1, maxWorkers)
	}

	load := 1.0
	if v := q.Get("load"); v != "" {
		f, err := parseFloat(v)
		if err != nil {
			return cpuParams{}, runtime.Errorf(400, "invalid load %q: want a number 0..1", v)
		}
		load = clampFloat(f, 0, 1)
	}

	return cpuParams{dur: dur, workers: workers, load: load}, nil
}
