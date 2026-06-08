package runtime

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Ensure a clean environment for the fields under test.
	t.Setenv("FUNCTION_NAME", "")
	t.Setenv("PORT", "")
	t.Setenv("SHUTDOWN_GRACE", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("INVOKE_TIMEOUT", "")
	t.Setenv("MAX_CONCURRENCY", "")

	cfg := loadConfig()
	if cfg.Name != "" {
		t.Errorf("Name default = %q, want empty", cfg.Name)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port default = %q, want 8080", cfg.Port)
	}
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("ShutdownGrace default = %s, want 15s", cfg.ShutdownGrace)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel default = %s, want info", cfg.LogLevel)
	}
	if cfg.InvokeTimeout != 30*time.Second {
		t.Errorf("InvokeTimeout default = %s, want 30s", cfg.InvokeTimeout)
	}
	if cfg.MaxConcurrency != 0 {
		t.Errorf("MaxConcurrency default = %d, want 0", cfg.MaxConcurrency)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("FUNCTION_NAME", "func1")
	t.Setenv("PORT", "9090")
	t.Setenv("SHUTDOWN_GRACE", "30s")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("INVOKE_TIMEOUT", "5s")
	t.Setenv("MAX_CONCURRENCY", "10")

	cfg := loadConfig()
	if cfg.Name != "func1" {
		t.Errorf("Name = %q, want func1", cfg.Name)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.ShutdownGrace != 30*time.Second {
		t.Errorf("ShutdownGrace = %s, want 30s", cfg.ShutdownGrace)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %s, want debug", cfg.LogLevel)
	}
	if cfg.InvokeTimeout != 5*time.Second {
		t.Errorf("InvokeTimeout = %s, want 5s", cfg.InvokeTimeout)
	}
	if cfg.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", cfg.MaxConcurrency)
	}
}

func TestLoadConfigInvalidValuesFallBack(t *testing.T) {
	t.Setenv("SHUTDOWN_GRACE", "not-a-duration")
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("MAX_CONCURRENCY", "lots")

	cfg := loadConfig()
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("invalid grace should fall back to 15s, got %s", cfg.ShutdownGrace)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("invalid level should fall back to info, got %s", cfg.LogLevel)
	}
	if cfg.MaxConcurrency != 0 {
		t.Errorf("invalid concurrency should fall back to 0, got %d", cfg.MaxConcurrency)
	}
}
