package runtime

import (
	"log/slog"
	"os"
	"time"
)

// config holds runtime settings sourced from the environment. M1 wires the values
// needed to serve and shut down cleanly; per-invocation timeouts, concurrency limits
// and metrics are introduced in later milestones (see DESIGN.md §3).
type config struct {
	// Name identifies the function. It is attached to every log line (and, from M5,
	// every metric as the `function` label) so a per-function autoscaler can query
	// signals scoped to a single function. The manifest generator injects it as
	// FUNCTION_NAME from the function's name; locally it may be left unset.
	Name          string
	Port          string
	ShutdownGrace time.Duration
	LogLevel      slog.Level
}

func loadConfig() config {
	return config{
		Name:          envString("FUNCTION_NAME", ""),
		Port:          envString("PORT", "8080"),
		ShutdownGrace: envDuration("SHUTDOWN_GRACE", 15*time.Second),
		LogLevel:      envLevel("LOG_LEVEL", slog.LevelInfo),
	}
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
