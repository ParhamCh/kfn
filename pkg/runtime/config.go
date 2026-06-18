package runtime

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// config holds runtime settings sourced from the environment. M1 wires the values
// needed to serve and shut down cleanly; per-invocation timeouts, concurrency limits
// and metrics are introduced in later milestones (see DESIGN.md §3).
type config struct {
	// Name identifies the function. It is attached to every log line and every metric
	// (as the `function` label) so a per-function autoscaler can query signals scoped to
	// a single function. The manifest generator injects it as FUNCTION_NAME from the
	// function's name; locally it may be left unset.
	Name          string
	Port          string
	ShutdownGrace time.Duration
	LogLevel      slog.Level

	// MetricsPort is the dedicated port for the Prometheus /metrics endpoint, kept off
	// the function port so metrics are never exposed through the function's Ingress.
	MetricsPort string

	// InvokeTimeout bounds how long a single invocation may run before the runtime
	// gives up and returns 504. 0 disables the timeout.
	InvokeTimeout time.Duration
	// MaxConcurrency caps simultaneous in-flight invocations per pod; excess requests
	// get 429 immediately. 0 means unlimited. The 429 rate is a saturation signal a
	// per-function autoscaler can scale on, and the value is exported as kfn_max_concurrency.
	MaxConcurrency int

	// LatencyBuckets sets the kfn_request_duration_seconds histogram buckets (seconds).
	// Sourced from METRICS_BUCKETS (comma-separated); empty uses Prometheus default buckets.
	// Tuning these to the function's real latency profile makes percentiles trustworthy.
	LatencyBuckets []float64
}

func loadConfig() config {
	return config{
		Name:           envString("FUNCTION_NAME", ""),
		Port:           envString("PORT", "8080"),
		MetricsPort:    envString("METRICS_PORT", "9090"),
		ShutdownGrace:  envDuration("SHUTDOWN_GRACE", 15*time.Second),
		LogLevel:       envLevel("LOG_LEVEL", slog.LevelInfo),
		InvokeTimeout:  envDuration("INVOKE_TIMEOUT", 30*time.Second),
		MaxConcurrency: envInt("MAX_CONCURRENCY", 0),
		LatencyBuckets: envFloats("METRICS_BUCKETS", nil),
	}
}

// envFloats parses a comma-separated list of floats (e.g. "0.01,0.05,0.1") from the
// environment. An empty or malformed value falls back to def.
func envFloats(key string, def []float64) []float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out []float64
	for p := range strings.SplitSeq(v, ",") {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			slog.Warn("invalid float in env var, using default", "key", key, "value", v)
			return def
		}
		out = append(out, f)
	}
	return out
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration env var, using default",
			"key", key, "value", v, "default", def)
		return def
	}
	return d
}

func envLevel(key string, def slog.Level) slog.Level {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		slog.Warn("invalid log level env var, using default",
			"key", key, "value", v, "default", def)
		return def
	}
	return lvl
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid int env var, using default",
			"key", key, "value", v, "default", def)
		return def
	}
	return n
}
