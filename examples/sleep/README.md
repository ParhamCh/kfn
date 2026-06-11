# `sleep` — latency / concurrency load generator

Holds each request open for a configurable duration **without burning CPU**. It's a pure
in-flight-concurrency generator: a stand-in for a handler that's blocked on a slow
downstream (DB, upstream API). Use it to drive `kfn_in_flight_requests`, fill the latency
histogram, and exercise the runtime's `MAX_CONCURRENCY` load shedding.

## Parameters

All are query-string params; the env vars below set per-deployment defaults.

| Param | Default | Meaning |
|-------|---------|---------|
| `duration` | `SLEEP_DEFAULT` (`200ms`) | Base sleep, a Go duration (`250ms`, `2s`). Clamped to `SLEEP_MAX`. |
| `dist` | `fixed` | Latency distribution: `fixed`, `uniform`, or `exp`. |
| `jitter` | `0.25` | For `uniform`: spread as a fraction of `duration` (`0..1`). |

The distributions:

- **`fixed`** — sleep exactly `duration`.
- **`uniform`** — uniform in `duration × (1 ± jitter)`; a flat band of latencies.
- **`exp`** — exponential with mean `duration`: mostly short, with the occasional long
  sleep. Models a realistic latency tail (p99 spikes). Always clamped to `SLEEP_MAX`.

Every computed sleep is hard-capped at `SLEEP_MAX`, so one request can never pin a worker
longer than the operator allows. The sleep is **cooperatively cancellable** — if the
runtime's `INVOKE_TIMEOUT` fires or the client disconnects, it returns immediately
(`"canceled": true`) instead of holding the slot.

### Environment

| Var | Default | Meaning |
|-----|---------|---------|
| `SLEEP_DEFAULT` | `200ms` | Base sleep when `duration` is omitted. |
| `SLEEP_MAX` | `60s` | Hard ceiling on any single sleep. |

> Keep the runtime's `INVOKE_TIMEOUT` **above** `SLEEP_MAX`, or long sleeps get cut off
> with a `504`. The shipped `function.yaml` sets `INVOKE_TIMEOUT: 90s`.

## Run locally

```bash
go run ./examples/sleep
```

```bash
curl -s 'localhost:8080/?duration=300ms'                       # ~300ms
curl -s 'localhost:8080/?duration=200ms&dist=uniform&jitter=0.5'  # 100–300ms
curl -s 'localhost:8080/?duration=200ms&dist=exp'              # exponential tail
curl -s 'localhost:8080/?duration=999s'                        # clamped to SLEEP_MAX
```

Response:

```json
{"requested_ms":300,"distribution":"fixed","jitter":0.25,"slept_ms":301,"canceled":false,"request_id":"…"}
```

Watch concurrency climb while you hammer it:

```bash
# 50 concurrent 2s sleeps in one shell …
seq 50 | xargs -P50 -I_ curl -s 'localhost:8080/?duration=2s' >/dev/null &
# … and watch the gauge in another:
watch -n0.2 "curl -s localhost:9090/metrics | grep kfn_in_flight_requests"
```

## Deploy

```bash
kfn build -f examples/sleep/function.yaml --func ./examples/sleep
kfn push  -f examples/sleep/function.yaml
kfn apply -f examples/sleep/function.yaml
```

The shipped `function.yaml` exposes it at `https://load-sleep.kfn.lan` (point that host at
your ingress-nginx LoadBalancer IP). Drive load from outside the cluster:

```bash
curl -sk 'https://load-sleep.kfn.lan/?duration=2s&dist=exp'
```

## Signals it drives

| Metric | What it shows |
|--------|---------------|
| `kfn_in_flight_requests{function="load-sleep"}` | live concurrency — the direct scale signal |
| `kfn_request_duration_seconds{function="load-sleep"}` | the latency distribution you dialed in |
| `kfn_requests_total{function="load-sleep",code="429"}` | shed requests, if `MAX_CONCURRENCY` is set |

See [`../../docs/observability.md`](../../docs/observability.md) for PromQL recipes.
