# Autoscaling signals

The data a per-function autoscaler reads, how to **aggregate** it, and how to **trust** it.
Everything is per-function (constant `function` label) and aggregatable across replicas.

## 1. Raw runtime metrics (per function, per pod)

Served on `METRICS_PORT` (default `9090`), scraped by the generated ServiceMonitor.

| Metric | Type | Use |
|--------|------|-----|
| `kfn_requests_total{method,code}` | counter | request rate, error rate, `429`/`504` rates |
| `kfn_request_duration_seconds{method}` | histogram | latency percentiles (buckets tunable via `METRICS_BUCKETS`) |
| `kfn_in_flight_requests` | gauge | live concurrency (only meaningful for long-lived requests — see trust) |
| `kfn_max_concurrency` | gauge | the per-pod `MAX_CONCURRENCY` ceiling (`0` = unlimited) |
| `kfn_panics_total` | counter | recovered panics — a health gate, not a scale signal |
| `kfn_build_info{kfn_version,go_version}` | gauge `1` | which code is running |
| `process_cpu_seconds_total`, `process_resident_memory_bytes` | counter/gauge | CPU / memory |

## 2. The canonical signals (recording rules)

Apply once — these define the `kfn:function:*` series the autoscaler and dashboard read
(never ad-hoc PromQL):

```bash
kubectl apply -f deploy/kfn-recording-rules.yaml
```

| Recording rule | What it tells the autoscaler |
|----------------|------------------------------|
| **`kfn:function:request_rate`** (`1m`), `…_30s` | **demand (RPS)** — the primary signal |
| **`kfn:function:cpu_utilization`** | CPU load vs the pod limit |
| `kfn:function:concurrency_saturation` | in-flight ÷ capacity — *only for blocking workloads* |
| `kfn:function:shed_rate` (429) | hard "scale now" — already shedding |
| `kfn:function:error_ratio` (5xx) | health gate — don't scale a broken function |
| `kfn:function:latency_p95` / `_p99` | SLO-based scaling |
| `kfn:function:cpu_throttle_ratio` | CPU starvation (leading indicator) |
| `kfn:function:memory_utilization` | memory headroom / OOM risk |
| `kfn:function:replicas` | current scale (the lever's position) |

The CPU/memory-limit and throttle rules join kube-state-metrics / cAdvisor (which carry
`pod` but not `function`) onto a `pod → function` map built from the runtime's own metrics
(`kfn_build_info`) — because kube-state-metrics doesn't export pod labels by default.

Import the dashboard at [`grafana/kfn-autoscaling-dashboard.json`](grafana/kfn-autoscaling-dashboard.json)
to see all of this per function (RPS-first, per-replica breakdown).

## 3. How to trust the data

- **RPS never drops a request — gauges do.** `kfn_requests_total` is a cumulative counter
  incremented in the app on every request; Prometheus reads the running total each scrape,
  so even a burst of sub-millisecond requests is fully counted. The `kfn_in_flight_requests`
  *gauge*, by contrast, is a point-in-time sample: a request that starts and ends between two
  scrapes is invisible to it. So **use RPS (and CPU) for fast workloads**; concurrency
  saturation is only meaningful for *long-lived/blocking* functions (like `sleep`).
- **RPS is a per-second average over a window, not a count.** `rate(kfn_requests_total[w])`
  = (increase over `w`) ÷ (`w` seconds). One isolated request reads `1/w` (≈0.03 for `[30s]`),
  **not** `1`. To read RPS = N you must sustain N requests/second.
- **The rate window must be ≥ 2× the scrape interval.** `rate()` needs at least two samples
  in the window. With a 10s scrape, `[30s]` is the practical floor; a 30s scrape would make a
  `[30s]` window return *nothing*. This is why all the example functions scrape at `10s`
  (`monitoring.interval: 10s`).
- **Counter resets are handled.** `rate()`/`increase()` account for the counter restarting at
  a pod restart — always use them, never raw counter deltas.
- **Cardinality is bounded.** The request labels are `method` and `code` only; the request
  *path* is deliberately excluded, so series count stays small and the data stays cheap.

## 4. Testing RPS

RPS = sustained requests **per second**, averaged over the window. To verify it, drive a
known *rate* (not a one-off count) and read it back.

```bash
# Drive ~80 requests/second at a function for ~45s (80 fast requests each second):
kubectl -n kfn port-forward deploy/load-sleep 18080:8080 &
( while true; do for i in $(seq 80); do curl -s 'localhost:18080/?duration=1ms' >/dev/null & done; sleep 1; done )
# Ctrl-C to stop.
```

Then (port-forward `kps-prometheus:9090`) you should see **RPS ≈ 80**:

```promql
sum(rate(kfn_requests_total{function="load-sleep"}[30s]))     # ≈ 80 req/s
sum(increase(kfn_requests_total{function="load-sleep"}[30s])) # ≈ 2400  (= 80 × 30s)
```

The two are the same fact: `increase` is the **count** over the window, `rate` is that count
**÷ the window seconds**. (Measured live: ~80/s drive → `rate[30s]` = 76 req/s, `increase[30s]`
= 2280 ≈ 76 × 30.)

> "Send 80 requests once and see 80 RPS" does **not** hold — 80 requests spread over a 30s
> window read `80/30 ≈ 2.7` req/s. RPS measures a *rate*; to see 80 you sustain 80/second.
> If you want the literal count, use `increase(kfn_requests_total[1m])`.

## 5. Recommended autoscaler inputs

- **Primary:** `kfn:function:request_rate` (RPS) and/or `kfn:function:cpu_utilization`.
- **Hard trigger:** `kfn:function:shed_rate` (429s) — already over capacity, scale up now.
- **Health gate:** `kfn:function:error_ratio` — don't scale a function that's failing.
- **SLO guard:** `kfn:function:latency_p95`.
- **Blocking workloads only:** `kfn:function:concurrency_saturation`.
