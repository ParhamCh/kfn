# `cpu` — CPU-burn load generator

Burns CPU for a configurable wall-clock window across N parallel workers, optionally at a
fractional duty cycle. Each worker runs a tight SHA-256 loop (real, non-elidable work) and
the response reports the throughput achieved. Use it to drive `process_cpu_seconds_total`,
saturate cores, and observe CPU throttling against a pod's limit — the input for CPU-based
autoscaling.

## Parameters

All are query-string params; the env vars below set per-deployment defaults.

| Param | Default | Meaning |
|-------|---------|---------|
| `duration` | `CPU_DEFAULT` (`500ms`) | Burn window, a Go duration (`2s`, `500ms`). Clamped to `CPU_MAX`. |
| `workers` | `1` | Parallel goroutines burning CPU. Clamped to `[1, CPU_MAX_WORKERS]` (default `GOMAXPROCS`). |
| `load` | `1.0` | Duty cycle `0..1`: fraction of each ~100ms tick spent burning. `0.5` ≈ half a core per worker. |

`workers` is how you saturate multiple cores; `load` is how you hold a steady *fractional*
CPU level. Effective parallelism is bounded by the pod's CPU limit — ask for more workers
than the limit allows and you'll see throttling rather than more throughput.

The burn is **cooperatively cancellable**: if the runtime's `INVOKE_TIMEOUT` fires or the
client disconnects, every worker stops within ~a tick instead of running the cores hot for
a response nobody will read (`"canceled": true`).

### Environment

| Var | Default | Meaning |
|-----|---------|---------|
| `CPU_DEFAULT` | `500ms` | Burn window when `duration` is omitted. |
| `CPU_MAX` | `60s` | Hard ceiling on a single burn. |
| `CPU_MAX_WORKERS` | `GOMAXPROCS` | Cap on parallel workers. |

> Keep `INVOKE_TIMEOUT` **above** `CPU_MAX`, or long burns get cut off with a `504`. The
> shipped `function.yaml` sets `INVOKE_TIMEOUT: 90s`.

## Run locally

```bash
go run ./examples/cpu
```

```bash
curl -s 'localhost:8080/?duration=2s'              # burn one core ~2s
curl -s 'localhost:8080/?duration=2s&workers=4'    # burn up to 4 cores
curl -s 'localhost:8080/?duration=5s&load=0.5'     # ~half a core, steady
curl -s 'localhost:8080/?duration=999s'            # clamped to CPU_MAX
```

Response:

```json
{"duration_ms":2000,"workers":4,"load":1,"total_ops":18412160,"ops_per_sec":9200000,"gomaxprocs":8,"canceled":false,"request_id":"…"}
```

Watch CPU usage climb while you drive it:

```bash
curl -s 'localhost:8080/?duration=10s&workers=2' >/dev/null &
watch -n0.5 "curl -s localhost:9090/metrics | grep '^process_cpu_seconds_total'"
# or just: top -p "$(pgrep -f 'go run')"
```

## Deploy

```bash
kfn build -f examples/cpu/function.yaml --func ./examples/cpu
kfn push  -f examples/cpu/function.yaml
kfn apply -f examples/cpu/function.yaml
```

The shipped `function.yaml` exposes it at `https://load-cpu.kfn.lan` (point that host at
your ingress-nginx LoadBalancer IP). Drive load from outside the cluster:

```bash
curl -sk 'https://load-cpu.kfn.lan/?duration=5s&workers=2'
```

## Signals it drives

| Metric | What it shows |
|--------|---------------|
| `process_cpu_seconds_total{function="load-cpu"}` | container CPU time — `rate()` it for cores in use |
| `kfn_request_duration_seconds{function="load-cpu"}` | how long each burn ran |

```promql
# cores consumed
sum(rate(process_cpu_seconds_total{function="load-cpu"}[1m]))
```

See [`../../docs/observability.md`](../../docs/observability.md) for more PromQL recipes.
