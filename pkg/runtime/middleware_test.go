package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestWithTimeoutExceeded(t *testing.T) {
	h := withTimeout(50*time.Millisecond, func(ctx context.Context, _ *Request) (*Response, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return Text(http.StatusOK, "late"), nil
		}
	})

	resp, err := h(context.Background(), &Request{})
	if resp != nil {
		t.Fatalf("want nil response on timeout, got %v", resp)
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusGatewayTimeout {
		t.Fatalf("want 504 HTTPError, got %v", err)
	}
}

func TestWithTimeoutFastPath(t *testing.T) {
	h := withTimeout(time.Second, func(_ context.Context, _ *Request) (*Response, error) {
		return Text(http.StatusOK, "ok"), nil
	})
	resp, err := h(context.Background(), &Request{})
	if err != nil || resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("fast handler: resp=%v err=%v", resp, err)
	}
}

func TestWithTimeoutDisabledPassesThrough(t *testing.T) {
	want := Text(http.StatusOK, "ok")
	h := withTimeout(0, func(_ context.Context, _ *Request) (*Response, error) {
		return want, nil
	})
	resp, err := h(context.Background(), &Request{})
	if err != nil || resp != want {
		t.Fatalf("disabled timeout should pass through: resp=%v err=%v", resp, err)
	}
}

func TestWithRecoverCatchesPanic(t *testing.T) {
	h := withRecover(discardLogger, func(_ context.Context, _ *Request) (*Response, error) {
		panic("boom")
	})
	resp, err := h(context.Background(), &Request{})
	if resp != nil {
		t.Fatalf("want nil response on panic, got %v", resp)
	}
	if err == nil {
		t.Fatal("want error from recovered panic")
	}
}

// A panic inside a handler running under withTimeout must be recovered in the spawned
// goroutine; if the composition order were wrong, this test binary would crash.
func TestTimeoutWithPanicDoesNotCrash(t *testing.T) {
	h := withTimeout(time.Second, withRecover(discardLogger, func(_ context.Context, _ *Request) (*Response, error) {
		panic("boom")
	}))
	if _, err := h(context.Background(), &Request{}); err == nil {
		t.Fatal("want error from recovered panic under timeout")
	}
}

// End-to-end through the mux: a panicking handler yields a masked 500 and the server
// keeps serving (a follow-up request still succeeds on the same mux).
func TestPanicYields500AndServerSurvives(t *testing.T) {
	var panicNext bool
	h := withTimeout(0, withRecover(discardLogger, func(_ context.Context, _ *Request) (*Response, error) {
		if panicNext {
			panic("boom")
		}
		return Text(http.StatusOK, "ok"), nil
	}))
	hc := &health{}
	hc.setReady(true)
	mux := newMux(h, hc, newMetrics(config{}), config{}, discardLogger)

	panicNext = true
	resp := do(t, mux, http.MethodPost, "/", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic request = %d, want 500", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if got := string(b); got != "internal server error" {
		t.Fatalf("body = %q, want masked message", got)
	}

	panicNext = false
	if resp := do(t, mux, http.MethodPost, "/", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("follow-up request = %d, want 200 (server should survive)", resp.StatusCode)
	}
}

func TestLimitConcurrencySheds429(t *testing.T) {
	entered := make(chan struct{}, 1) // buffered: handler must never block signaling entry
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	limited := limitConcurrency(1, next)

	// Occupy the single slot with one in-flight request.
	firstDone := make(chan struct{})
	go func() {
		limited.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
		close(firstDone)
	}()
	<-entered

	// A second concurrent request is shed immediately.
	rec := httptest.NewRecorder()
	limited.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", rec.Code)
	}

	// Release the first and wait for it to free the slot before retrying.
	close(release)
	<-firstDone
	rec2 := httptest.NewRecorder()
	limited.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("post-release request = %d, want 200", rec2.Code)
	}
}

func TestLimitConcurrencyUnlimitedIsPassthrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if got := limitConcurrency(0, next); got == nil {
		t.Fatal("unlimited should return a usable handler")
	}
}

// drain must flip readiness off and wait for in-flight requests before returning.
func TestDrainWaitsForInflight(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		close(finished)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	hc := &health{}
	hc.setReady(true)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started // ensure the request is in-flight before draining

	if err := drain(srv, &http.Server{}, hc, 2*time.Second, discardLogger); err != nil {
		t.Fatalf("drain returned error: %v", err)
	}
	if hc.ready.Load() {
		t.Fatal("readiness should be false after drain")
	}
	select {
	case <-finished:
	default:
		t.Fatal("drain returned before the in-flight request completed")
	}
}
