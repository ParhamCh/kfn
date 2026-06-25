package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// withRecover wraps a Handler so a panic in user code is caught, counted, logged with its
// stack, and converted to a generic error (which writeError renders as a masked 500). The
// process keeps serving. recover must run in the same goroutine as the user call, so this
// is composed *inside* withTimeout (see Start). panics may be nil (it is incremented only
// when set), which keeps the function usable in tests.
func withRecover(logger *slog.Logger, panics prometheus.Counter, h Handler) Handler {
	return func(ctx context.Context, req *Request) (resp *Response, err error) {
		defer func() {
			if r := recover(); r != nil {
				if panics != nil {
					panics.Inc()
				}
				logger.Error("handler panicked", "panic", r, "stack", string(debug.Stack()))
				resp = nil
				err = fmt.Errorf("handler panic: %v", r)
			}
		}()
		return h(ctx, req)
	}
}

// withTimeout bounds a single invocation to d. The handler runs in its own goroutine;
// if the deadline fires first the caller gets a 504 and the handler's context is
// cancelled to signal cooperative handlers to stop. The result channel is buffered so
// a late-finishing handler never blocks on send — but Go cannot force-kill it, so a
// handler that ignores ctx keeps running until it returns. d <= 0 disables the bound.
func withTimeout(d time.Duration, h Handler) Handler {
	if d <= 0 {
		return h
	}
	return func(ctx context.Context, req *Request) (*Response, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()

		type result struct {
			resp *Response
			err  error
		}
		done := make(chan result, 1)
		go func() {
			resp, err := h(ctx, req)
			done <- result{resp, err}
		}()

		select {
		case <-ctx.Done():
			return nil, Errorf(http.StatusGatewayTimeout, "function timed out")
		case r := <-done:
			return r.resp, r.err
		}
	}
}

// limitConcurrency caps simultaneous in-flight invocations at n using a buffered
// channel as a semaphore. Acquisition is non-blocking: when the pod is already at n,
// the request is shed immediately with 429 rather than queued, giving an autoscaler a
// prompt saturation signal. n <= 0 means unlimited (the handler is returned unwrapped).
func limitConcurrency(n int, next http.Handler) http.Handler {
	if n <= 0 {
		return next
	}
	sem := make(chan struct{}, n)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		default:
			writePlain(w, http.StatusTooManyRequests, "too many concurrent requests")
		}
	})
}
