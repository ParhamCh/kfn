// Command ram is a kfn load-generator function: it allocates a configurable amount of
// resident memory, holds it for a while, then releases it. Drive it to push
// process_resident_memory_bytes up and down — the input for memory-based autoscaling.
//
//	GET /?mb=64&hold=5s            # allocate 64 MiB, hold 5s, release
//	GET /?mb=128&ramp=3s&hold=2s   # grow to 128 MiB over 3s (slow-leak style), hold 2s
//
// Safe by default: mb is hard-capped (RAM_MAX_MB) below the pod's memory limit, and
// MAX_CONCURRENCY=1 serializes requests, so it never OOM-kills the pod. See the README to
// opt into OOM testing.
package main

import (
	"context"
	"time"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		p, err := parseParams(req)
		if err != nil {
			return nil, err
		}
		res := runRAM(ctx, p)
		res.RequestID = runtime.RequestID(ctx)
		return runtime.JSON(200, res)
	})
}

// parseParams resolves the request's query string over the env defaults into a validated,
// clamped ramParams. Bad input is a 400 rather than a silent default.
func parseParams(req *runtime.Request) (ramParams, error) {
	q := req.Query

	mb := defaultMB
	if v := q.Get("mb"); v != "" {
		n, err := parseInt(v)
		if err != nil || n < 0 {
			return ramParams{}, runtime.Errorf(400, "invalid mb %q: want a non-negative integer", v)
		}
		mb = clampInt(n, 0, maxMB)
	}

	hold := 5 * time.Second
	if v := q.Get("hold"); v != "" {
		d, err := parseDuration(v)
		if err != nil || d < 0 {
			return ramParams{}, runtime.Errorf(400, "invalid hold %q: want a Go duration like 5s", v)
		}
		hold = clampDuration(d, 0, maxHold)
	}

	var ramp time.Duration
	if v := q.Get("ramp"); v != "" {
		d, err := parseDuration(v)
		if err != nil || d < 0 {
			return ramParams{}, runtime.Errorf(400, "invalid ramp %q: want a Go duration like 2s", v)
		}
		ramp = clampDuration(d, 0, maxRamp)
	}

	return ramParams{mb: mb, hold: hold, ramp: ramp}, nil
}
