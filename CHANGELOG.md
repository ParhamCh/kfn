# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.0] - 2026-06-09

### Added
- **M6 observability**:
  - Prometheus **`/metrics`** served on a dedicated port (`METRICS_PORT`, default `9090`)
    — off the function port, so metrics are never exposed through the public Ingress.
    Per-function metrics (constant `function` label): `kfn_requests_total{method,code}`
    (includes `429`/`504`), `kfn_request_duration_seconds{method}`, and
    `kfn_in_flight_requests`, plus the standard `go_*`/`process_*` collectors.
  - **request-id propagation**: every invocation honors an inbound `X-Request-Id` or
    generates one, echoes it on the response, adds `request_id` to the access log, and
    exposes it to handlers via `runtime.RequestID(ctx)`.
  - **ServiceMonitor** generation (on by default) via a `monitoring:` block in
    `function.yaml`; the Deployment and Service gain a `metrics` port. The ServiceMonitor
    is labelled `release: kps` so the kube-prometheus-stack operator discovers it
    (overridable via `monitoring.releaseLabel`). Toggle with `monitoring.enabled`.
- New runtime dependency: `github.com/prometheus/client_golang`.

## [0.5.0] - 2026-06-09

### Added
- **M5 ingress + TLS** — `function.yaml` gains an optional `ingress:` block. When
  enabled, `kfn render`/`apply` emit a third object, an `Ingress` that routes
  `https://<name>.kfn.lan` through ingress-nginx with TLS issued by cert-manager:
  - Defaults: `host: <name>.kfn.lan`, `tls: true` (issued by `cm-lab-ca` into
    `<name>-tls`), `className: nginx`. All overridable.
  - nginx annotations are **derived from the runtime contract**: `proxy-body-size: 1m`
    (the runtime's 1 MiB body cap) and `proxy-read/send-timeout` set above
    `INVOKE_TIMEOUT` (so the runtime emits its own `504` before nginx cuts the
    connection), plus `ssl-redirect` and `cert-manager.io/cluster-issuer` when TLS is on.
    User-supplied `annotations:` override the derived ones.
- Exposure is **opt-in**: a function is `ClusterIP`-only unless `ingress.enabled: true`.

## [0.4.0] - 2026-06-09

### Added
- **M4 image + end-to-end deploy**:
  - Reference multi-stage `build/Dockerfile` — compiles any function (the author's
    `main.go` + the kfn runtime) into a static binary on `distroless/static:nonroot`
    (uid 65532, matching the generated Deployment). `FUNC_PKG` build arg selects the
    package (default `.`; the bundled example is `./examples/hello`).
  - `kfn build -f function.yaml [--func .] [--dockerfile build/Dockerfile] [--context .]`
    builds the image (reference from `function.yaml`) via `docker`/`podman`.
  - `kfn push -f function.yaml` pushes it to its registry.
  - Container engine is auto-detected (`docker` preferred, then `podman`); override with
    `KFN_CONTAINER_ENGINE`.
- The reference function now runs on the cluster and scales by hand
  (`kubectl scale deploy/<name> -n kfn --replicas=N`) — the lever the user's autoscaler
  will drive.

### Changed
- `examples/hello/function.yaml` image reference is now `harbor.lan/kfn/hello:0.1.0`
  (real registry/project) instead of the `harbor.example.com` placeholder.

## [0.3.0] - 2026-06-08

### Added
- **M3 manifest generator** — the `kfn` CLI:
  - `kfn render -f function.yaml [-o out.yaml] [-n namespace]` turns a function's
    `function.yaml` into a Deployment + Service (no client-go; plain templated YAML).
  - `kfn apply -f function.yaml [-n namespace]` renders and applies via `kubectl`,
    creating the target namespace if missing.
  - `kfn version`.
  - Generated Deployment: one per function, pinned to `role=workload` nodes, resource
    requests/limits, `/healthz`+`/readyz` probes, injected `FUNCTION_NAME`, non-root /
    read-only-rootfs container, `terminationGracePeriodSeconds` aligned to the drain
    window. Defaults: namespace `kfn`, port `8080`, replicas `1`. **No HPA** (the user
    runs their own autoscaler). ServiceMonitor deferred to M5.
- New dependency: `gopkg.in/yaml.v3` (parsing only).

### Changed
- Routing: the function now receives **all HTTP methods** on any path (previously only
  `POST`; other methods returned `405`). It sees `req.Method` and decides.
- `/healthz` and `/readyz` are now **reserved for the runtime across all methods** (not
  just `GET`), so a request can no longer fall through to the function on those paths.

## [0.2.0] - 2026-06-08

### Added
- **M2 operational hardening** (`pkg/runtime`):
  - Per-invocation timeout (`INVOKE_TIMEOUT`, default `30s`): a slow handler is
    abandoned with `504` and its context cancelled. `0` disables.
  - Panic recovery: a handler panic is caught, logged with its stack, and returned as a
    masked `500`; the process keeps serving.
  - Per-pod concurrency limit (`MAX_CONCURRENCY`, default `0` = unlimited): excess
    in-flight requests are shed immediately with `429` — a saturation signal a
    per-function autoscaler can scale on.
- Per-function identity: `FUNCTION_NAME` is read from the environment and attached as
  a `function` attribute on every log line, so per-function workloads are attributable
  in logs (and, from M5, metrics). Foundation for independent per-function autoscaling.

### Changed
- Renamed the project `loadgen-go` → `kfn`; the Go module path is now
  `github.com/ParhamCh/kfn` (**breaking** for importers).
- HTTP server now sets `ReadTimeout`; `WriteTimeout` is intentionally left unset so
  `INVOKE_TIMEOUT` governs response timing.

## [0.1.0] - 2026-06-08

### Added
- **Runtime core** (`pkg/runtime`): `runtime.Start` wraps a single user handler as a
  long-lived HTTP service.
- Transport-agnostic `Request`/`Response` types with `Text`, `JSON`, `Bytes` helpers
  and a typed `HTTPError` for controlling failure status codes.
- HTTP surface: `POST /` invocation route plus `GET /healthz` and `GET /readyz` probes.
- Environment-based configuration: `PORT`, `SHUTDOWN_GRACE`, `LOG_LEVEL`.
- Graceful shutdown: SIGTERM marks the instance not-ready, then drains in-flight
  requests within the grace window.
- Structured JSON access logging (one line per invocation).
- `examples/hello` reference function and accompanying `function.yaml`.

[Unreleased]: https://github.com/ParhamCh/kfn/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/ParhamCh/kfn/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/ParhamCh/kfn/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/ParhamCh/kfn/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ParhamCh/kfn/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ParhamCh/kfn/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ParhamCh/kfn/releases/tag/v0.1.0
