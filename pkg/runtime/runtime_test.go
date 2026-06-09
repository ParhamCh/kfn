package runtime

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testMux(h Handler) http.Handler {
	hc := &health{}
	hc.setReady(true)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newMux(h, hc, newMetrics(config{}), config{}, logger)
}

func do(t *testing.T, mux http.Handler, method, target string, body io.Reader) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Result()
}

func TestInvokeJSON(t *testing.T) {
	mux := testMux(func(_ context.Context, req *Request) (*Response, error) {
		return JSON(http.StatusOK, map[string]string{"echo": req.Query.Get("name")})
	})

	resp := do(t, mux, http.MethodPost, "/?name=ada", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if got, want := strings.TrimSpace(string(b)), `{"echo":"ada"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestNilResponseIsNoContent(t *testing.T) {
	mux := testMux(func(_ context.Context, _ *Request) (*Response, error) {
		return nil, nil
	})
	resp := do(t, mux, http.MethodPost, "/", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHTTPErrorStatus(t *testing.T) {
	mux := testMux(func(_ context.Context, _ *Request) (*Response, error) {
		return nil, Errorf(http.StatusBadRequest, "missing field")
	})
	resp := do(t, mux, http.MethodPost, "/", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if got := strings.TrimSpace(string(b)); got != "missing field" {
		t.Fatalf("body = %q, want %q", got, "missing field")
	}
}

func TestGenericErrorIsMasked(t *testing.T) {
	mux := testMux(func(_ context.Context, _ *Request) (*Response, error) {
		return nil, io.ErrUnexpectedEOF // a non-HTTPError
	})
	resp := do(t, mux, http.MethodPost, "/", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if got := strings.TrimSpace(string(b)); got != "internal server error" {
		t.Fatalf("body = %q, want generic message, got %q", got, got)
	}
}

func TestHealthProbes(t *testing.T) {
	mux := testMux(func(_ context.Context, _ *Request) (*Response, error) {
		return Text(http.StatusOK, "ok"), nil
	})

	if resp := do(t, mux, http.MethodGet, "/healthz", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", resp.StatusCode)
	}
	if resp := do(t, mux, http.MethodGet, "/readyz", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz = %d, want 200", resp.StatusCode)
	}
}

func TestFunctionReceivesAllMethods(t *testing.T) {
	mux := testMux(func(_ context.Context, req *Request) (*Response, error) {
		return Text(http.StatusOK, "fn:"+req.Method), nil
	})
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		resp := do(t, mux, m, "/", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s / = %d, want 200 (function should handle any method)", m, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if got, want := string(b), "fn:"+m; got != want {
			t.Errorf("%s / body = %q, want %q", m, got, want)
		}
	}
}

func TestProbesReservedFromFunction(t *testing.T) {
	mux := testMux(func(_ context.Context, _ *Request) (*Response, error) {
		return Text(http.StatusOK, "function"), nil
	})
	// Even non-GET verbs on the probe paths must hit the runtime, not the function.
	for _, p := range []string{"/healthz", "/readyz"} {
		resp := do(t, mux, http.MethodPost, p, nil)
		b, _ := io.ReadAll(resp.Body)
		if string(b) == "function" {
			t.Errorf("POST %s reached the function; the probe path must be reserved", p)
		}
	}
}

func TestReadinessReflectsState(t *testing.T) {
	hc := &health{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := newMux(func(_ context.Context, _ *Request) (*Response, error) {
		return Text(http.StatusOK, "ok"), nil
	}, hc, newMetrics(config{}), config{}, logger)

	// Not ready until set.
	if resp := do(t, mux, http.MethodGet, "/readyz", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before ready = %d, want 503", resp.StatusCode)
	}
	hc.setReady(true)
	if resp := do(t, mux, http.MethodGet, "/readyz", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz after ready = %d, want 200", resp.StatusCode)
	}
}
