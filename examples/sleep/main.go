// Command sleep is a kfn load-generator function: it holds each request open for a
// configurable, optionally jittered, duration without burning CPU. It is a pure
// in-flight-concurrency generator — drive it concurrently and watch kfn_in_flight_requests
// rise, or set MAX_CONCURRENCY to see the runtime shed excess with 429.
//
//	GET /?duration=300ms                 # sleep ~300ms
//	GET /?duration=200ms&dist=uniform&jitter=0.5   # 100–300ms, uniform
//	GET /?duration=200ms&dist=exp        # exponential tail, mean 200ms
package main

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		p, err := parseParams(req)
		if err != nil {
			return nil, err
		}
		// A fresh PRNG per request keeps the handler free of shared mutable state (no
		// lock contention under concurrent load, which would distort the very latency
		// signal we are generating).
		r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))

		res := runSleep(ctx, p, r)
		res.RequestID = runtime.RequestID(ctx)
		return runtime.JSON(200, res)
	})
}

// parseParams resolves the request's query string over the env defaults into a validated,
// clamped sleepParams. Bad input is a 400 (via runtime.Errorf) rather than a silent default.
func parseParams(req *runtime.Request) (sleepParams, error) {
	q := req.Query

	base := defaultDuration
	if v := q.Get("duration"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return sleepParams{}, runtime.Errorf(400, "invalid duration %q: want a Go duration like 250ms or 2s", v)
		}
		base = clampDuration(d, 0, maxDuration)
	}

	dist := distFixed
	switch v := q.Get("dist"); v {
	case "", string(distFixed):
		dist = distFixed
	case string(distUniform):
		dist = distUniform
	case string(distExp):
		dist = distExp
	default:
		return sleepParams{}, runtime.Errorf(400, "invalid dist %q: want fixed, uniform or exp", v)
	}

	jitter := 0.25
	if v := q.Get("jitter"); v != "" {
		f, err := parseFloat(v)
		if err != nil {
			return sleepParams{}, runtime.Errorf(400, "invalid jitter %q: want a number 0..1", v)
		}
		jitter = clampFloat(f, 0, 1)
	}

	return sleepParams{base: base, dist: dist, jitter: jitter}, nil
}
