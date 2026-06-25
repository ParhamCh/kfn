package runtime

import (
	"net/http"
	"runtime/debug"

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
	panics   prometheus.Counter
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

	buckets := cfg.LatencyBuckets
	if len(buckets) == 0 {
		buckets = prometheus.DefBuckets
	}

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
			Buckets: buckets,
		}, []string{"method"}),
		panics: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kfn_panics_total",
			Help: "Total handler panics recovered by the runtime (a health signal, not a scale signal).",
		}),
	}

	// Capacity signal: the per-pod in-flight ceiling, so an autoscaler can compute
	// saturation = kfn_in_flight_requests / kfn_max_concurrency. 0 means unlimited.
	maxConcurrency := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kfn_max_concurrency",
		Help: "Configured MAX_CONCURRENCY (per-pod in-flight ceiling); 0 means unlimited.",
	})
	maxConcurrency.Set(float64(cfg.MaxConcurrency))

	// Build info: a constant 1 carrying the runtime and Go versions in labels, so you can
	// confirm which code a function is actually running.
	kfnVersion, goVersion := buildVersions()
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kfn_build_info",
		Help: "Runtime build info; constant 1 with kfn_version and go_version labels.",
	}, []string{"kfn_version", "go_version"})
	buildInfo.WithLabelValues(kfnVersion, goVersion).Set(1)

	r.MustRegister(m.inFlight, m.requests, m.duration, m.panics, maxConcurrency, buildInfo)
	return m
}

// buildVersions reports the kfn module version and Go toolchain version from the embedded
// build info, for the kfn_build_info metric.
func buildVersions() (kfnVersion, goVersion string) {
	kfnVersion, goVersion = "devel", "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if bi.GoVersion != "" {
		goVersion = bi.GoVersion
	}
	// When kfn is the main module (its own examples), Main carries the version; when it is a
	// dependency of a user's function, it appears in Deps.
	if bi.Main.Path == "github.com/ParhamCh/kfn" && bi.Main.Version != "" {
		kfnVersion = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/ParhamCh/kfn" && d.Version != "" {
			kfnVersion = d.Version
		}
	}
	return
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
