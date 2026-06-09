package runtime

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics holds the per-function Prometheus instruments and the registry they live in.
// Every series carries a constant `function` label so a per-function autoscaler can
// scope its query (e.g. rate(kfn_requests_total{function="hello"}[1m])).
type metrics struct {
	reg      *prometheus.Registry
	inFlight prometheus.Gauge
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// newMetrics builds a fresh registry (not the global default, so tests stay isolated),
// scopes it to the function name, and registers the runtime metrics alongside the Go and
// process collectors.
func newMetrics(cfg config) *metrics {
	reg := prometheus.NewRegistry()
	// Constant label on every metric, including the go_*/process_* collectors.
	r := prometheus.WrapRegistererWith(prometheus.Labels{"function": cfg.Name}, reg)
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &metrics{
		reg: reg,
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kfn_in_flight_requests",
			Help: "Number of invocations currently being served.",
		}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kfn_requests_total",
			Help: "Total invocations by HTTP method and response code (includes 429 shed and 504 timeout).",
		}, []string{"method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kfn_request_duration_seconds",
			Help:    "Invocation latency in seconds by HTTP method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
	}
	r.MustRegister(m.inFlight, m.requests, m.duration)
	return m
}

// middleware instruments the invocation chain: in-flight gauge, request count by
// method+code, and latency. The promhttp wrappers capture the status code themselves, so
// it sees 429 (shed) and 504 (timeout) when composed outside those layers.
func (m *metrics) middleware(next http.Handler) http.Handler {
	var h http.Handler = promhttp.InstrumentHandlerCounter(m.requests, next)
	h = promhttp.InstrumentHandlerDuration(m.duration, h)
	h = promhttp.InstrumentHandlerInFlight(m.inFlight, h)
	return h
}

// handler serves the Prometheus exposition for this function's registry.
func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
