# `ram` — memory allocation load generator

Allocates a configurable amount of **resident** memory, holds it, then releases it back to
the OS. Each megabyte is touched page-by-page so it truly commits to RSS (not lazy virtual
memory). Use it to drive `process_resident_memory_bytes` up and down — the input for
memory-based autoscaling.

**Safe by default:** `mb` is hard-capped (`RAM_MAX_MB`) *below* the pod's memory limit, and
`MAX_CONCURRENCY=1` serializes requests, so a request can **never OOM-kill the pod**. RSS
climbs visibly in Grafana, the pod stays healthy. (Provoking an OOM is opt-in — see below.)

## Parameters

All are query-string params; the env vars below set per-deployment defaults.

| Param | Default | Meaning |
|-------|---------|---------|
| `mb` | `RAM_DEFAULT_MB` (`64`) | Megabytes to allocate. Clamped to `RAM_MAX_MB`. |
| `hold` | `5s` | How long to hold the allocation before releasing. Clamped to `RAM_MAX_HOLD`. |
| `ramp` | `0` (instant) | Allocate gradually over this window instead of all at once — emulates a slow leak. Clamped to `RAM_MAX_RAMP`. |

The allocation is **cooperatively cancellable**: if the runtime's `INVOKE_TIMEOUT` fires or
the client disconnects, the memory is freed immediately (`"canceled": true`) instead of
being held.

### Environment

| Var | Default | Meaning |
|-----|---------|---------|
| `RAM_DEFAULT_MB` | `64` | Allocation when `mb` is omitted. |
| `RAM_MAX_MB` | `160` | Hard ceiling on a single allocation (kept below the pod limit). |
| `RAM_MAX_HOLD` | `60s` | Max hold window. |
| `RAM_MAX_RAMP` | `30s` | Max ramp window. |

> Keep `INVOKE_TIMEOUT` **above** `RAM_MAX_HOLD + RAM_MAX_RAMP`, or long holds get cut off
> with a `504`. The shipped `function.yaml` raises the caps for longer experiments —
> `RAM_MAX_HOLD: 180s`, `RAM_MAX_RAMP: 120s`, `INVOKE_TIMEOUT: 320s` — so a full
> ramp+hold (up to 300s) always fits inside the timeout.

## Run locally

```bash
go run ./examples/ram
```

```bash
curl -s 'localhost:8080/?mb=64&hold=3s'          # allocate 64 MiB, hold 3s, release
curl -s 'localhost:8080/?mb=128&ramp=2s&hold=2s' # grow to 128 MiB over 2s, hold 2s
curl -s 'localhost:8080/?mb=99999'               # clamped to RAM_MAX_MB
```

Response:

```json
{"requested_mb":64,"allocated_mb":64,"ramp_ms":12,"held_ms":3000,"rss_before_mb":9,"rss_peak_mb":74,"rss_after_mb":10,"canceled":false,"request_id":"…"}
```

Note `rss_peak_mb` rising during the hold and `rss_after_mb` dropping back once it's
released.

## Deploy

```bash
kfn build -f examples/ram/function.yaml --func ./examples/ram
kfn push  -f examples/ram/function.yaml
kfn apply -f examples/ram/function.yaml
```

The shipped `function.yaml` exposes it at `https://load-ram.kfn.lan` (point that host at
your ingress-nginx LoadBalancer IP):

```bash
curl -sk 'https://load-ram.kfn.lan/?mb=128&hold=20s'
```

## Signals it drives

| Metric | What it shows |
|--------|---------------|
| `process_resident_memory_bytes{function="load-ram"}` | container RSS — rises during a hold, falls after release |

```promql
# resident memory in MiB
process_resident_memory_bytes{function="load-ram"} / 1024 / 1024
```

## Opt-in: trigger an OOMKilled (only if you want to see it)

By default this can't happen. To deliberately observe an OOM kill + pod restart, give the
allocation room to exceed the limit — either lower the limit or raise the cap, then ask for
a big `mb`:

```bash
# raise the per-request cap above the 256Mi pod limit, then over-allocate
kubectl -n kfn set env deploy/load-ram RAM_MAX_MB=512
curl -sk 'https://load-ram.kfn.lan/?mb=400&hold=10s'      # connection drops — pod OOMKilled
kubectl -n kfn get pod -l app.kubernetes.io/name=load-ram # RESTARTS climbs, last state OOMKilled
# revert:
kubectl -n kfn set env deploy/load-ram RAM_MAX_MB=160
```

See [`../../docs/observability.md`](../../docs/observability.md) for more PromQL recipes.
