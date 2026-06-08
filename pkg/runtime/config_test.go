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
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("FUNCTION_NAME", "func1")
	t.Setenv("PORT", "9090")
	t.Setenv("SHUTDOWN_GRACE", "30s")
	t.Setenv("LOG_LEVEL", "debug")

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
}

func TestLoadConfigInvalidValuesFallBack(t *testing.T) {
	t.Setenv("SHUTDOWN_GRACE", "not-a-duration")
	t.Setenv("LOG_LEVEL", "verbose")

	cfg := loadConfig()
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("invalid grace should fall back to 15s, got %s", cfg.ShutdownGrace)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("invalid level should fall back to info, got %s", cfg.LogLevel)
	}
}
