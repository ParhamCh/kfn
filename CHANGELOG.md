# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ParhamCh/kfn/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ParhamCh/kfn/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ParhamCh/kfn/releases/tag/v0.1.0
