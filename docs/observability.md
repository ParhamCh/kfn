# Observability

Every function exposes Prometheus metrics and propagates a request id. The metrics are the
intended input for a per-function autoscaler: each series carries a constant `function`
label so a control loop can scope its query to one function.

## The `/metrics` endpoint

Metrics are served on a **dedicated port** (`METRICS_PORT`, default `9090`) at `/metrics` —
deliberately separate from the function port, so they are never reachable through the
function's public Ingress (which only routes the function port).

```bash
# Local
curl -s http://localhost:9090/metrics | grep '^kfn_'

# In-cluster
kubectl -n kfn port-forward svc/hello 9090:9090 &
curl -s http://localhost:9090/metrics | grep '^kfn_'
```

## Metrics reference

| Metric | Type | Labels | Use |
|--------|------|--------|-----|
| `kfn_requests_total` | counter | `function`, `method`, `code` | request rate, error rate, `429`/`504` saturation |
| `kfn_request_duration_seconds` | histogram | `function`, `method` | latency / SLOs (default Prometheus buckets) |
| `kfn_in_flight_requests` | gauge | `function` | live concurrency — a direct scale signal |

The standard `go_*` and `process_*` collectors are exported too, each also carrying the
`function` label. `code` includes the runtime's own `429` (shed) and `504` (timeout), so
saturation and timeout rates are visible without extra instrumentation. The request **path
is not a label** — that's deliberate, to keep cardinality bounded.

## Wiring into Prometheus (ServiceMonitor)

When `monitoring` is on (the default), `kfn` renders a `ServiceMonitor` and adds a
`metrics` port to the Deployment and Service. The prometheus-operator discovers the
ServiceMonitor and scrapes the function automatically — **if** the labels line up.

> **The one thing that must match: `releaseLabel`.** The operator selects ServiceMonitors
> by a label, conventionally `release: <helm-release>`. kfn defaults to **`release: kps`**
> (kube-prometheus-stack's default). If your operator uses a different release name,
> nothing gets scraped until you set `monitoring.releaseLabel` to match it.

Find your operator's selector:

```bash
kubectl -n monitoring get prometheus -o jsonpath='{.items[0].spec.serviceMonitorSelector}'; echo
# e.g. {"matchLabels":{"release":"kps"}}  → releaseLabel: kps
```

The rendered ServiceMonitor:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  labels:
    release: kps                 # <- operator discovery hinges on this
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: hello
  namespaceSelector:
    matchNames: [kfn]
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
```

## Confirming it's scraped

```bash
kubectl -n kfn get servicemonitor,endpoints hello

# Port-forward the operator's Prometheus and check the target is up:
kubectl -n monitoring port-forward svc/kps-prometheus 9090:9090 &
curl -s 'http://localhost:9090/api/v1/targets?state=active' \
  | grep -o '"namespace":"kfn"[^}]*"health":"[a-z]*"'

# Then query a per-function signal — non-empty means discovery worked:
curl -s 'http://localhost:9090/api/v1/query?query=kfn_requests_total%7Bfunction%3D%22hello%22%7D'
```

## Generating load to watch

To make these signals move on demand, deploy a load generator from
[`examples/`](../examples/). [`examples/sleep`](../examples/sleep) holds requests open for a
configurable duration, so driving it concurrently pushes `kfn_in_flight_requests` up and
fills the latency histogram — a controllable source for testing dashboards and (eventually)
the autoscaler:

```bash
# with examples/sleep deployed and svc/load-sleep port-forwarded on :8080
for i in $(seq 20); do curl -s 'localhost:8080/?duration=2s' >/dev/null & done
```

[`examples/cpu`](../examples/cpu) burns CPU across N workers to drive
`process_cpu_seconds_total` (and to provoke CPU throttling against a pod's limit):

```bash
# cores consumed by the cpu generator
sum(rate(process_cpu_seconds_total{function="load-cpu"}[1m]))
```

[`examples/ram`](../examples/ram) allocates and holds resident memory to drive
`process_resident_memory_bytes` (rising during a hold, falling after release):

```bash
# resident memory of the ram generator, in MiB
process_resident_memory_bytes{function="load-ram"} / 1024 / 1024
```

### Real-time dashboard

[`grafana/kfn-load-dashboard.json`](grafana/kfn-load-dashboard.json) is a minimal Grafana
dashboard for watching the load functions live (5s refresh, 15-minute window): in-flight
concurrency, request rate by status code, CPU cores, memory RSS, p95 latency, and pods
scraped per function. Import it via *Dashboards → New → Import → Upload JSON file*. Note
that data still advances at the ServiceMonitor scrape interval (30s by default; set
`monitoring.interval` lower for snappier graphs).

## PromQL recipes

Per-function signals an autoscaler (or a dashboard) can use:

```promql
# Request rate
sum(rate(kfn_requests_total{function="hello"}[1m]))

# Error rate (5xx share)
sum(rate(kfn_requests_total{function="hello", code=~"5.."}[5m]))
  / sum(rate(kfn_requests_total{function="hello"}[5m]))

# Saturation: shed (429) rate — the cleanest "scale me up" signal
sum(rate(kfn_requests_total{function="hello", code="429"}[1m]))

# Live concurrency across replicas
sum(kfn_in_flight_requests{function="hello"})

# p95 latency
histogram_quantile(0.95,
  sum(rate(kfn_request_duration_seconds_bucket{function="hello"}[5m])) by (le))
```

Because the `function` label is constant per deployment, the same query works for any
function by swapping the label value — which is exactly what a generic autoscaler control
loop does: read `sum(kfn_in_flight_requests{function="X"})` (or the `429` rate), compare to
a target, and set `deploy/X`'s replicas.

## Request-id tracing

Every invocation has an `X-Request-Id`: the runtime honors an inbound one or generates a
128-bit hex id, echoes it on the response, and logs it as `request_id`. Use it to stitch a
single request across the client, the access log, and your handler's own logs.

```bash
# Echoed back on the response:
curl -is http://localhost:8080/ | grep -i x-request-id

# Supply your own to correlate with an upstream trace:
curl -is -H 'X-Request-Id: trace-abc-123' http://localhost:8080/ | grep -i x-request-id
```

In a handler:

```go
slog.Info("handling", "request_id", runtime.RequestID(ctx))
```

Find a request across all replicas by its id:

```bash
kubectl -n kfn logs deploy/hello | grep '"request_id":"trace-abc-123"'
```

## Turning metrics off

```yaml
monitoring:
  enabled: false     # drops the metrics port, the /metrics scrape target and the ServiceMonitor
```

Re-applying with this set **does not** delete an already-created ServiceMonitor (`kfn
apply` never prunes) — remove it by hand:
`kubectl -n kfn delete servicemonitor hello`.
