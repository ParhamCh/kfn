package runtime

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

// newLogger builds the structured JSON logger used across the runtime. JSON output
// is chosen so cluster log collectors (Fluent Bit, Loki, etc.) can parse fields
// without a regex.
func newLogger(cfg config) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
}

// statusRecorder captures the response status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logging emits one structured line per invocation with method, path, status and
// duration. It wraps only the invocation route; probe traffic is intentionally not
// logged to keep the signal clean.
func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("invocation",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
