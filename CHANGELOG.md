# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ParhamCh/loadgen-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ParhamCh/loadgen-go/releases/tag/v0.1.0
