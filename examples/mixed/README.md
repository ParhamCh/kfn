# `mixed` — all-in-one load generator

Accepts **every parameter** from the [`sleep`](../sleep), [`cpu`](../cpu) and
[`ram`](../ram) examples and runs all of those loads **at once, in a single request** — so
one call can make the pod CPU-heavy, memory-heavy and slow at the same time. There are no
presets: you send exactly the load you want, as a query string or a JSON file.

## Parameters (the union of the three examples)

A load type runs **only if its trigger param is set**, so you opt into exactly what you want.

| Group | Params | Trigger | From |
|-------|--------|---------|------|
| **CPU** | `cpu` (burn duration), `workers`, `load` (duty cycle 0..1) | `cpu` > 0 | [cpu](../cpu) |
| **Memory** | `mb`, `hold`, `ramp` | `mb` > 0 | [ram](../ram) |
| **Latency** | `sleep` (duration), `dist` (`fixed`/`uniform`/`exp`), `jitter` | `sleep` > 0 | [sleep](../sleep) |

The loads run **concurrently** — memory is held while CPU burns and the sleep elapses — and
the request returns when the longest finishes. Every param is clamped to a cap
(`MIXED_MAX_CPU`, `MIXED_MAX_SLEEP`, `MIXED_MAX_MB`, …); malformed input is a `400`. The
whole thing is cooperatively cancellable (`INVOKE_TIMEOUT`/disconnect frees memory and stops
the burn, reporting `"canceled": true`).

The shipped manifest defaults to **heavy single loads**: `cpu`/`sleep`/`hold` up to `120s`
and `mb` up to `200`, with `MAX_CONCURRENCY=1` (one request at a time) so a big allocation
can't OOM the 256Mi pod. A single curl like `?cpu=90s&workers=2&mb=180&hold=90s` paints a
clear ~90-second plateau on the CPU and memory dashboards.

When you set `mb` without `hold`, the memory is held for the whole request (it spans the CPU
and sleep work) so RSS doesn't dip mid-flight.

## Two ways to send the load

**Query string** — quick and ad-hoc:

```bash
curl -s 'localhost:8080/?cpu=2s&workers=2&mb=64&sleep=500ms&dist=exp'
```

**JSON file** — keep your load definitions in files and replay them. POST the file as the
body:

```bash
cat > web.json <<'EOF'
{ "cpu": "200ms", "workers": 1, "mb": 32, "sleep": "300ms", "dist": "exp" }
EOF
curl -s --data-binary @web.json localhost:8080/
```

Both accept the same fields. (Numbers may be JSON numbers — `"mb": 64` — or strings.)

## Response (only the loads you ran appear)

```json
{
  "cpu":   {"duration_ms": 2000, "workers": 2, "load": 1, "total_ops": 8123456},
  "ram":   {"requested_mb": 64, "allocated_mb": 64, "rss_peak_mb": 78},
  "sleep": {"requested_ms": 500, "distribution": "exp", "slept_ms": 612},
  "total_ms": 2010,
  "canceled": false,
  "request_id": "…"
}
```

## Run locally

```bash
go run ./examples/mixed
```

```bash
curl -s 'localhost:8080/?cpu=1s&workers=2&mb=64&sleep=300ms'   # all three at once
curl -s 'localhost:8080/?sleep=500ms&dist=exp'                 # latency only
curl -s 'localhost:8080/?mb=128&hold=10s'                      # memory only
curl -s 'localhost:8080/?cpu=abc'                              # 400
```

## Deploy

```bash
kfn build -f examples/mixed/function.yaml --func ./examples/mixed
kfn push  -f examples/mixed/function.yaml
kfn apply -f examples/mixed/function.yaml
```

Exposed at `https://load-mixed.kfn.lan` (point that host at your ingress-nginx LB IP):

```bash
curl -sk 'https://load-mixed.kfn.lan/?cpu=2s&workers=2&mb=64&sleep=500ms'
curl -sk --data-binary @web.json https://load-mixed.kfn.lan/
```

## Signals it drives

A single `mixed` call moves **all three** at once — the multi-dimensional load a real
autoscaler must cope with:

| Metric | From |
|--------|------|
| `process_cpu_seconds_total{function="load-mixed"}` | the CPU phase |
| `process_resident_memory_bytes{function="load-mixed"}` | the memory phase |
| `kfn_in_flight_requests{function="load-mixed"}` | concurrency / the sleep phase |

Watch them together on the [Grafana dashboard](../../docs/grafana/kfn-load-dashboard.json).
See [`../../docs/observability.md`](../../docs/observability.md) for PromQL recipes.
