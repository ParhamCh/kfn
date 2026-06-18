# kfn examples

Runnable kfn functions. [`hello`](hello) is the minimal reference; the rest are a
**load-generator suite** — functions that deliberately produce a chosen kind of load so you
can exercise the runtime and (soon) a per-function autoscaler.

## The load-generator suite

| Example | Load it makes | Drives this signal | Key params |
|---------|---------------|--------------------|------------|
| [`sleep`](sleep) | latency / in-flight concurrency (no CPU) | `kfn_in_flight_requests`, request-duration histogram | `duration`, `dist` (fixed/uniform/exp), `jitter` |
| [`cpu`](cpu) | CPU burn across N workers | `process_cpu_seconds_total` | `duration`, `workers`, `load` (duty cycle) |
| [`ram`](ram) | allocate + hold resident memory | `process_resident_memory_bytes` | `mb`, `hold`, `ramp` |
| [`mixed`](mixed) | **all of the above at once**, in one call | all three signals together | the union of the above; query string or JSON body |

Each is a self-contained Go program (copy a folder into your own repo and it builds). They
share the same conventions: query-string-first params over env defaults, **hard caps** so a
request can't overrun the pod, cooperative `ctx` cancellation, an honest JSON summary, and a
harmonized `function.yaml` (ingress/TLS at `load-<name>.kfn.lan`, metrics scraped every 10s).

## Run one locally

```bash
go run ./examples/sleep        # or cpu / ram / mixed / hello
# in another shell:
curl -s 'localhost:8080/?duration=2s'
curl -s localhost:9090/metrics | grep '^kfn_'   # metrics on the dedicated port
```

## Deploy one to a cluster

```bash
kfn build -f examples/<name>/function.yaml --func ./examples/<name>
kfn push  -f examples/<name>/function.yaml
kfn apply -f examples/<name>/function.yaml
```

See each example's own `README.md` for its parameters and a worked example, and
[`../docs/deploying.md`](../docs/deploying.md) for the end-to-end flow.

## Watch the load

Import [`../docs/grafana/kfn-load-dashboard.json`](../docs/grafana/kfn-load-dashboard.json)
— a real-time dashboard (5s refresh) with a panel per signal: in-flight concurrency,
request rate by status code, CPU cores, memory RSS, p95 latency, and pods scraped. Drive
any example and watch its panel move; drive `mixed` and watch them all move at once. PromQL
recipes are in [`../docs/observability.md`](../docs/observability.md).
