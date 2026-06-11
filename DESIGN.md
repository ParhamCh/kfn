# kfn — FaaS Function Runtime: Design

## 1. Goal & scope

A lightweight Go **function runtime**: a wrapper that takes a single user-supplied
function, runs it as a long-lived HTTP service inside a container, and ships the
**Kubernetes manifests** needed to deploy that container.

This is *not* a full FaaS control plane (no autoscaler, no event router, no
multi-tenant API). It is the per-function unit that such a platform would schedule.
Scope is deliberately one function == one container image == one Deployment.

**In scope**
- A runtime library that user code imports to register a handler.
- An HTTP server wrapping that handler, with the operational concerns a k8s pod
  needs: health/readiness probes, graceful shutdown, request timeouts, concurrency
  limiting, panic recovery, structured logging.
- A manifest generator that emits Deployment + Service YAML from function metadata,
  and can optionally `kubectl apply` them.
- A reference Dockerfile producing a minimal image.

**Out of scope (for now)**
- Scale-to-zero / cold-start optimization (pods stay warm).
- Event sources beyond HTTP (queues, cron, pub/sub).
- A build service that compiles arbitrary user source — the user builds their own
  image using our runtime as a library.

## 2. The function contract

User code imports the runtime and registers exactly one handler:

```go
package main

import (
    "context"
    "github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
    runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
        return runtime.Text(200, "hello "+req.Query.Get("name")), nil
    })
}
```

Types (kept intentionally small and transport-agnostic so we can add non-HTTP
triggers later without changing handler signatures):

```go
type Handler func(ctx context.Context, req *Request) (*Response, error)

type Request struct {
    Method  string
    Path    string
    Headers http.Header
    Query   url.Values
    Body    []byte
}

type Response struct {
    Status  int
    Headers http.Header
    Body    []byte
}
```

Helpers: `Text(status, s)`, `JSON(status, v)`, `Bytes(status, ct, b)`. A returned
`error` maps to 500 (with the message logged, not leaked to the client unless it's a
typed `runtime.HTTPError`).

## 3. Runtime behavior (the operational wrapper)

This is the part that earns the project its keep — the boilerplate every function
would otherwise reimplement.

| Concern            | Behavior |
|--------------------|----------|
| Routes             | Catch-all `/` (any method) → handler; `/healthz` liveness and `/readyz` readiness reserved for the runtime across all methods. `/metrics` is served on a **separate port** (see Observability), not the function port. |
| Config             | Env vars: `FUNCTION_NAME` (unset), `PORT` (8080), `METRICS_PORT` (9090), `INVOKE_TIMEOUT` (30s), `MAX_CONCURRENCY` (0=unlimited), `SHUTDOWN_GRACE` (15s), `LOG_LEVEL` (info) |
| Timeouts           | Per-invocation `context.WithTimeout` → `504`; server read/idle timeouts set (`WriteTimeout` left unset so `INVOKE_TIMEOUT` governs response timing) |
| Concurrency        | Optional per-pod semaphore; over-limit → `429` |
| Panic recovery     | Handler panics recovered → `500`, stack logged, process stays up |
| Graceful shutdown  | SIGTERM → `/readyz` flips to NotReady → drain in-flight up to grace → `server.Shutdown` (both the function and metrics servers) |
| Logging            | Structured (`log/slog`), one line per invocation: `function`, method, path, status, duration, `request_id` |
| Request-id         | Honor inbound `X-Request-Id` or generate one; echo on the response, add to the log line, expose via `runtime.RequestID(ctx)` |
| Observability      | Prometheus `/metrics` on a dedicated `METRICS_PORT`, every series carrying a constant `function` label: `kfn_requests_total{method,code}` (incl. `429`/`504`), `kfn_request_duration_seconds{method}`, `kfn_in_flight_requests`, plus `go_*`/`process_*` |

Readiness is the subtle bit: on SIGTERM we fail `/readyz` *before* draining so k8s
stops routing new traffic while we finish in-flight requests. Metrics live on their own
port so they are never reachable through the function's public Ingress.

## 4. Manifest generation

A small CLI (`cmd/kfn`) takes function metadata and renders manifests. Metadata
source: flags and/or a `function.yaml` in the function repo.

```yaml
# function.yaml
name: hello
image: registry.example.com/hello:v1
port: 8080
replicas: 2
resources:
  requests: { cpu: 50m, memory: 64Mi }
  limits:   { cpu: 250m, memory: 128Mi }
env:
  - { name: LOG_LEVEL, value: info }
```

```
kfn render -f function.yaml            # print Deployment+Service YAML to stdout
kfn render -f function.yaml -o out.yaml
kfn apply  -f function.yaml            # render then `kubectl apply -f -`
```

Generated objects:
- **Deployment**: the standard liveness (`/healthz`) + readiness (`/readyz`) probes
  wired to the runtime, resource requests/limits, env, `terminationGracePeriodSeconds`
  aligned to `SHUTDOWN_GRACE`, sane securityContext (non-root, read-only rootfs).
- **Service**: ClusterIP exposing the port.
- **Ingress** *(optional, off by default — M5)*: an `ingress:` block opts a function in
  to `https://<name>.kfn.lan` via ingress-nginx + cert-manager, with nginx annotations
  derived from the runtime contract.
- **ServiceMonitor** *(optional, on by default — M6)*: a `monitoring:` block wires the
  `/metrics` port into the kube-prometheus-stack operator (discovered via a `release`
  label); the Deployment and Service gain a `metrics` port.

No `HorizontalPodAutoscaler` is ever generated — scaling is the job of the user's own
autoscaler driving the Deployment's scale subresource.

Rendering uses Go `text/template` over a struct (no client-go dependency for the
render path — keeps the binary tiny). `apply` shells out to `kubectl apply -f -`,
streaming the rendered YAML to stdin. This matches your "generate manifests" choice
and avoids pulling in the full k8s API machinery.

## 5. Container image

Multi-stage Dockerfile: build static binary in `golang` stage, copy into
`gcr.io/distroless/static` (or `scratch`). Runs as non-root, no shell, ~10–15 MB.
The function author's `main.go` + our runtime lib compile into a single binary; the
same Dockerfile works for any function.

## 6. Proposed layout

```
kfn/
├── go.mod
├── DESIGN.md · README.md · CHANGELOG.md
├── pkg/
│   └── runtime/
│       ├── runtime.go      # Start(), server wiring, the invocation mux
│       ├── config.go       # env-sourced config + parsing helpers
│       ├── request.go      # Request/Response types + Text/JSON/Bytes/HTTPError
│       ├── middleware.go   # recovery, timeout, concurrency limiting
│       ├── logging.go      # structured access log + statusRecorder
│       ├── metrics.go      # Prometheus registry, instrumentation, /metrics handler
│       ├── requestid.go    # X-Request-Id propagation + RequestID(ctx)
│       └── health.go       # liveness/readiness state + graceful shutdown
├── cmd/
│   └── kfn/
│       ├── main.go         # CLI entrypoint + version
│       ├── render.go       # render: function.yaml → manifests
│       ├── apply.go        # apply: render then `kubectl apply -f -`
│       ├── build.go        # build: image from build/Dockerfile
│       ├── push.go         # push: image to its registry
│       └── engine.go       # container-engine detection (docker/podman)
├── internal/
│   └── manifest/
│       ├── manifest.go     # FunctionSpec, defaults, validation, Render
│       └── templates/      # deployment / service / ingress / servicemonitor .yaml.tmpl
├── examples/
│   └── hello/
│       ├── main.go
│       └── function.yaml
├── docs/
│   └── git-workflow.md
└── build/
    └── Dockerfile
```

## 7. Build order

All of the following have shipped (releases v0.1.0–v0.7.0):

1. **Runtime core** — `pkg/runtime`: types, `Start()`, routing, config from env, logging.
2. **Operational hardening** — per-invocation timeouts, panic recovery, concurrency limit,
   readiness-gated graceful shutdown.
3. **Manifest generator** — `internal/manifest` + `cmd/kfn render`: `function.yaml` → a
   valid Deployment + Service.
4. **Apply + image** — `kfn build`/`push`, the reference Dockerfile, end-to-end on the
   cluster (functions on `role=workload` nodes, scaled by hand).
5. **Ingress + TLS** — optional `ingress:` block → an `Ingress` exposing
   `https://<name>.kfn.lan` via ingress-nginx + cert-manager (`cm-lab-ca`).
6. **Observability** — Prometheus `/metrics` on a dedicated port, per-function metrics +
   ServiceMonitor (operator-discovered via the `release` label), request-id propagation.
7. **Load-generator examples** — runnable functions that generate controllable load to
   exercise the runtime and the upcoming autoscaler: `sleep` (latency / concurrency) and
   `cpu` (CPU burn), with `ram` and `mixed` planned.

Next: the autoscaler (§9).

## 8. Key decisions (resolved)

- **Module path**: `github.com/ParhamCh/kfn` (renamed from `loadgen-go` at v0.2.0, since
  that name implied a load generator).
- **Trigger route**: the catch-all `/` handles every method (the handler reads
  `req.Method`); `/healthz` and `/readyz` are reserved on subpaths.
- **Metrics**: Prometheus client (`prometheus/client_golang`), landed in M6 on a
  dedicated port rather than the function port — kept off the public Ingress.
- **`apply` strategy**: shell out to `kubectl apply -f -` (streaming rendered YAML to
  stdin). Keeps the binary free of client-go; the trade-off is a `kubectl` dependency on
  the operator's machine. Note `apply` never prunes — disabling a previously-applied
  `ingress:`/`monitoring:` block leaves the old object behind; delete it explicitly.

## 9. Next: the autoscaler

With per-function scrape signals in place (M6), the next unit is the user's own
**autoscaler**: a Go control loop that reads a per-function signal from Prometheus
(e.g. `kfn_in_flight_requests` or `rate(kfn_requests_total[1m])`) and sets each
Deployment's `.spec.replicas` through the scale subresource. HPA/KEDA are deliberately
not used — this is the platform's own scaling brain.

---
*Status: the runtime, CLI, ingress/TLS, observability and load-generator examples have
shipped (latest v0.7.0). Next: the autoscaler (§9).*
