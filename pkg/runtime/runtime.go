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

	// Bound and protect each invocation. Order matters: recover must run inside the
	// goroutine withTimeout spawns, so it is the inner wrapper.
	h = withTimeout(cfg.InvokeTimeout, withRecover(logger, h))

	hc := &health{}
	mtr := newMetrics(cfg)
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: newMux(h, hc, mtr, cfg, logger),
		// WriteTimeout is intentionally unset: response timing is governed by
		// INVOKE_TIMEOUT in the handler path, so a hard WriteTimeout below it would
		// truncate legitimately slow responses. ReadTimeout guards slow request reads
		// (body size is separately capped at maxBodyBytes).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Serve /metrics on a dedicated port so it is never reachable through the function's
	// public Ingress (which only routes the function port).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", mtr.handler())
	metricsSrv := &http.Server{
		Addr:              ":" + cfg.MetricsPort,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := run(srv, metricsSrv, hc, cfg, logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run starts both listeners (the function server and the dedicated metrics server) and
// handles graceful shutdown on SIGINT/SIGTERM.
func run(srv, metricsSrv *http.Server, hc *health, cfg config, logger *slog.Logger) error {
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
	go func() {
		logger.Info("metrics server listening", "addr", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining", "grace", cfg.ShutdownGrace)
	}

	return drain(srv, metricsSrv, hc, cfg.ShutdownGrace, logger)
}

// drain performs a readiness-gated graceful shutdown: it stops advertising readiness
// so Kubernetes removes the pod from Service endpoints, then waits up to grace for
// in-flight requests to finish before closing the function server. The metrics server,
// which holds no business traffic, is closed alongside it.
func drain(srv, metricsSrv *http.Server, hc *health, grace time.Duration, logger *slog.Logger) error {
	hc.setReady(false)
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	_ = metricsSrv.Shutdown(ctx)
	logger.Info("shutdown complete")
	return nil
}

// newMux reserves the probe paths for the runtime and forwards everything else to the
// function. /healthz and /readyz are registered without a method, so the runtime owns
// them for any verb; the catch-all "/" then receives every other path and method,
// letting the function handle GET, POST, PUT, … itself (it sees req.Method). Go's
// ServeMux resolves the more specific probe patterns ahead of "/". The invocation chain
// is request-id → logging → metrics → concurrency limit → invoke: request-id is
// outermost so logs and the response header carry it, and metrics sit outside the
// concurrency limit so shed (429) and timed-out (504) requests are still counted. Probe
// traffic is intentionally neither logged nor measured. /metrics is served on a separate
// port (see Start), not here.
func newMux(h Handler, hc *health, mtr *metrics, cfg config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", hc.liveness)
	mux.HandleFunc("/readyz", hc.readiness)
	mux.Handle("/", withRequestID(logging(logger, mtr.middleware(limitConcurrency(cfg.MaxConcurrency, invoke(h, logger))))))
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
