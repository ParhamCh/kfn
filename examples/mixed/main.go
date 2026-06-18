// Command mixed is a kfn all-in-one load generator: it accepts the union of the sleep, cpu
// and ram parameters and produces all of those loads at once, in a single request. Send the
// params as a query string, or define them in a JSON file and POST it as the body.
//
//	GET  /?cpu=2s&workers=2&mb=64&hold=5s&sleep=500ms&dist=exp
//	POST / with body {"cpu":"2s","mb":64,"sleep":"500ms"}   (curl --data-binary @load.json)
//
// A load type runs only if its trigger param is set (cpu>0, mb>0, sleep>0); they run
// concurrently, so the pod is CPU-heavy, memory-heavy and slow at the same time.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		p, err := parseParams(req)
		if err != nil {
			return nil, err
		}
		res := runMixed(ctx, p)
		res.RequestID = runtime.RequestID(ctx)
		return runtime.JSON(200, res)
	})
}

// getter returns a parameter value by key from either the query string or a JSON body.
type getter func(key string) (string, bool)

// parseParams resolves the load spec from the JSON request body when present, otherwise from
// the query string, validating and clamping every field.
func parseParams(req *runtime.Request) (mixedParams, error) {
	get := queryGetter(req.Query)
	if len(bytes.TrimSpace(req.Body)) > 0 {
		var m map[string]any
		if err := json.Unmarshal(req.Body, &m); err != nil {
			return mixedParams{}, runtime.Errorf(400, "invalid JSON body: %v", err)
		}
		get = mapGetter(m)
	}

	cpu, err := durParam(get, "cpu", 0, maxCPU)
	if err != nil {
		return mixedParams{}, err
	}
	workers, err := intParam(get, "workers", 1, 1, maxWorkers)
	if err != nil {
		return mixedParams{}, err
	}
	load, err := floatParam(get, "load", 1, 0, 1)
	if err != nil {
		return mixedParams{}, err
	}
	mb, err := intParam(get, "mb", 0, 0, maxMB)
	if err != nil {
		return mixedParams{}, err
	}
	hold, err := durParam(get, "hold", 0, maxHold)
	if err != nil {
		return mixedParams{}, err
	}
	ramp, err := durParam(get, "ramp", 0, maxRamp)
	if err != nil {
		return mixedParams{}, err
	}
	sleep, err := durParam(get, "sleep", 0, maxSleep)
	if err != nil {
		return mixedParams{}, err
	}
	jitter, err := floatParam(get, "jitter", 0.25, 0, 1)
	if err != nil {
		return mixedParams{}, err
	}

	dist := distFixed
	if v, ok := get("dist"); ok {
		switch v {
		case distFixed, distUniform, distExp:
			dist = v
		default:
			return mixedParams{}, runtime.Errorf(400, "invalid dist %q: want fixed, uniform or exp", v)
		}
	}

	// When memory is requested but hold wasn't given explicitly, keep it resident for the
	// whole request: span the CPU and sleep work when present, otherwise fall back to the
	// default hold so a memory-only request is still observable.
	if mb > 0 {
		if _, ok := get("hold"); !ok {
			span := maxDur(cpu, sleep)
			if span == 0 {
				span = defaultHold
			}
			hold = clampDuration(span, 0, maxHold)
		}
	}

	return mixedParams{
		cpu: cpu, workers: workers, load: load,
		mb: mb, hold: hold, ramp: ramp,
		sleep: sleep, dist: dist, jitter: jitter,
	}, nil
}

func queryGetter(q url.Values) getter {
	return func(k string) (string, bool) {
		v := q.Get(k)
		return v, v != ""
	}
}

// mapGetter adapts a decoded JSON object to the getter interface, rendering numbers and
// bools to their string form so body and query parse identically.
func mapGetter(m map[string]any) getter {
	return func(k string) (string, bool) {
		v, ok := m[k]
		if !ok || v == nil {
			return "", false
		}
		switch t := v.(type) {
		case string:
			return t, t != ""
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64), true
		case bool:
			return strconv.FormatBool(t), true
		default:
			return fmt.Sprintf("%v", t), true
		}
	}
}

func durParam(get getter, key string, def, max time.Duration) (time.Duration, error) {
	v, ok := get(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, runtime.Errorf(400, "invalid %s %q: want a Go duration like 500ms or 2s", key, v)
	}
	return clampDuration(d, 0, max), nil
}

func intParam(get getter, key string, def, lo, hi int) (int, error) {
	v, ok := get(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, runtime.Errorf(400, "invalid %s %q: want a non-negative integer", key, v)
	}
	return clampInt(n, lo, hi), nil
}

func floatParam(get getter, key string, def, lo, hi float64) (float64, error) {
	v, ok := get(key)
	if !ok {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, runtime.Errorf(400, "invalid %s %q: want a number", key, v)
	}
	return clampFloat(f, lo, hi), nil
}

func maxDur(ds ...time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}
