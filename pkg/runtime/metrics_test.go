package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scrape renders the metrics exposition for the given runtime metrics.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	b, _ := io.ReadAll(rec.Result().Body)
	return string(b)
}

func TestMetricsRecordInvocations(t *testing.T) {
	hc := &health{}
	hc.setReady(true)
	m := newMetrics(config{Name: "hello"})
	mux := newMux(func(_ context.Context, _ *Request) (*Response, error) {
		return Text(http.StatusOK, "ok"), nil
	}, hc, m, config{}, discardLogger)

	for range 3 {
		do(t, mux, http.MethodGet, "/", nil)
	}

	out := scrape(t, m)
	// Counter incremented with the right labels, including the constant function label.
	if !strings.Contains(out, `kfn_requests_total{code="200",function="hello",method="get"} 3`) {
		t.Errorf("kfn_requests_total not as expected; got:\n%s", out)
	}
	for _, want := range []string{
		"kfn_request_duration_seconds", // histogram present
		"kfn_in_flight_requests",       // gauge present
		"go_goroutines",                // Go collector present
		`function="hello"`,             // constant label applied across series
	} {
		if !strings.Contains(out, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestMetricsCountSheddingAndTimeouts(t *testing.T) {
	// A 429 (shed) must still be counted: metrics sits outside limitConcurrency.
	hc := &health{}
	hc.setReady(true)
	m := newMetrics(config{Name: "hello"})
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	h := func(_ context.Context, _ *Request) (*Response, error) {
		entered <- struct{}{}
		<-block
		return Text(http.StatusOK, "ok"), nil
	}
	mux := newMux(h, hc, m, config{MaxConcurrency: 1}, discardLogger)

	// Hold the one slot, then fire a second request that must be shed with 429.
	go do(t, mux, http.MethodPost, "/", nil)
	<-entered
	resp := do(t, mux, http.MethodPost, "/", nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", resp.StatusCode)
	}
	close(block)

	if out := scrape(t, m); !strings.Contains(out, `code="429"`) {
		t.Errorf("shed request not counted with code=429; got:\n%s", out)
	}
}
