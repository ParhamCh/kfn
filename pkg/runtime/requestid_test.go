package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	var seenByHandler string
	mux := testMux(func(ctx context.Context, _ *Request) (*Response, error) {
		seenByHandler = RequestID(ctx)
		return Text(http.StatusOK, "ok"), nil
	})

	resp := do(t, mux, http.MethodGet, "/", nil)
	got := resp.Header.Get("X-Request-Id")
	if got == "" {
		t.Fatal("response missing X-Request-Id header")
	}
	if seenByHandler != got {
		t.Errorf("handler saw RequestID %q, response header was %q; want equal", seenByHandler, got)
	}
	if len(got) != 32 { // 16 random bytes, hex-encoded
		t.Errorf("generated id %q is %d chars, want 32", got, len(got))
	}
}

func TestRequestIDHonorsInbound(t *testing.T) {
	var seenByHandler string
	mux := testMux(func(ctx context.Context, _ *Request) (*Response, error) {
		seenByHandler = RequestID(ctx)
		return Text(http.StatusOK, "ok"), nil
	})

	const want = "trace-abc-123"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", want)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	resp := rec.Result()
	if seenByHandler != want {
		t.Errorf("handler saw %q, want inbound id %q", seenByHandler, want)
	}
	if got := resp.Header.Get("X-Request-Id"); got != want {
		t.Errorf("response echoed %q, want %q", got, want)
	}
}
