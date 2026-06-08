// Package runtime turns a single user-supplied function into a long-lived HTTP
// service suitable for running as a Kubernetes pod. User code registers one Handler
// and calls Start; the runtime owns the server, routing, configuration, logging and
// shutdown.
//
//	func main() {
//	    runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
//	        return runtime.Text(200, "hello "+req.Query.Get("name")), nil
//	    })
//	}
package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// maxBodyBytes caps the request body the runtime will read into a Request.Body.
const maxBodyBytes = 1 << 20 // 1 MiB

// Start loads configuration from the environment, builds the HTTP server around h,
// and blocks until the process receives SIGINT or SIGTERM, after which it drains and
// exits. It is the single entry point a function's main() calls.
func Start(h Handler) {
	cfg := loadConfig()
	logger := newLogger(cfg)
	slog.SetDefault(logger)

	if h == nil {
		logger.Error("no handler provided to runtime.Start")
		os.Exit(1)
	}

	hc := &health{}
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           newMux(h, hc, logger),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if err := run(srv, hc, cfg, logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run starts the listener and handles graceful shutdown on SIGINT/SIGTERM.
func run(srv *http.Server, hc *health, cfg config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("function runtime listening", "addr", srv.Addr)
		hc.setReady(true)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining", "grace", cfg.ShutdownGrace)
	}

	// Stop advertising readiness so the load balancer drains us, then give in-flight
	// requests up to the grace period to complete.
	hc.setReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

// newMux wires the invocation route (POST /) and the health probes. Go's ServeMux
// resolves the more specific health patterns ahead of the catch-all POST /.
func newMux(h Handler, hc *health, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", hc.liveness)
	mux.HandleFunc("GET /readyz", hc.readiness)
	mux.Handle("POST /", logging(logger, invoke(h, logger)))
	return mux
}

// invoke adapts an HTTP request to a Handler call and writes the Response back.
func invoke(h Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writePlain(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		req := &Request{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header,
			Query:   r.URL.Query(),
			Body:    body,
		}

		resp, err := h(r.Context(), req)
		if err != nil {
			writeError(w, err, logger)
			return
		}
		writeResponse(w, resp)
	})
}

// writeResponse renders a Handler's Response. A nil Response is 204 No Content.
func writeResponse(w http.ResponseWriter, resp *Response) {
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for k, vs := range resp.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}

// writeError maps a Handler error to a status. *HTTPError carries its own status and
// client-visible message; any other error is a 500 with a generic message so internal
// details are not leaked. The full error is always logged.
func writeError(w http.ResponseWriter, err error, logger *slog.Logger) {
	var he *HTTPError
	if errors.As(err, &he) {
		logger.Warn("handler returned http error", "status", he.Status, "err", err)
		writePlain(w, he.Status, he.Message)
		return
	}
	logger.Error("handler failed", "err", err)
	writePlain(w, http.StatusInternalServerError, "internal server error")
}
