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
| Routes             | `POST /` (or `/invoke`) → handler; `GET /healthz` liveness; `GET /readyz` readiness; `GET /metrics` (optional) |
| Config             | Env vars: `PORT` (8080), `INVOKE_TIMEOUT` (30s), `MAX_CONCURRENCY` (0=unlimited), `SHUTDOWN_GRACE` (15s), `LOG_LEVEL` |
| Timeouts           | Per-invocation `context.WithTimeout`; server read/write/idle timeouts set |
| Concurrency        | Optional weighted semaphore; over-limit → 429 |
| Panic recovery     | Handler panics recovered → 500, stack logged, process stays up |
| Graceful shutdown  | SIGTERM → `/readyz` flips to NotReady → drain in-flight up to grace → `server.Shutdown` |
| Logging            | Structured (`log/slog`), one line per request: method, path, status, duration |
| Observability      | `/metrics`: invocation count, in-flight, duration histogram, error count |

Readiness is the subtle bit: on SIGTERM we fail `/readyz` *before* draining so k8s
stops routing new traffic while we finish in-flight requests.

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
├── DESIGN.md
├── pkg/
│   └── runtime/
│       ├── runtime.go      # Start(), config, server wiring
│       ├── request.go      # Request/Response types + helpers
│       ├── middleware.go   # recovery, timeout, concurrency, logging
│       └── health.go       # liveness/readiness state + graceful shutdown
├── cmd/
│   └── kfn/
│       ├── main.go         # CLI: render / apply
│       └── render.go       # function.yaml → manifest templates
├── internal/
│   └── manifest/
│       ├── manifest.go     # metadata struct, defaults, validation
│       └── templates/      # deployment.yaml.tmpl, service.yaml.tmpl
├── examples/
│   └── hello/
│       ├── main.go
│       └── function.yaml
└── build/
    └── Dockerfile
```

## 7. Build order (milestones)

1. **M1 — Runtime core.** `pkg/runtime`: types, `Start()`, routing, config from env,
   logging. Outcome: `examples/hello` runs locally, `curl localhost:8080` works.
2. **M2 — Operational hardening.** Timeouts, panic recovery, concurrency limit,
   readiness-gated graceful shutdown. Outcome: SIGTERM drains cleanly; tests cover it.
3. **M3 — Manifest generator.** `internal/manifest` + `cmd/kfn render`. Outcome:
   `function.yaml` → valid Deployment+Service YAML (validated with `kubectl --dry-run`).
4. **M4 — Apply + image.** ✅ `kfn build`/`push`, the reference Dockerfile, and a
   first end-to-end run on the cluster (function on `role=workload` nodes, scaled by hand).
5. **M5 — Ingress + TLS.** ✅ Optional `ingress:` block → an `Ingress` exposing
   `https://<name>.kfn.lan` via ingress-nginx + cert-manager (`cm-lab-ca`).
6. **M6 — Observability.** `/metrics`, ServiceMonitor, request-id propagation, polish.

## 8. Key decisions to confirm

- **Module path**: I assumed `github.com/ParhamCh/kfn`. What should it be?
- **Trigger route**: `POST /` vs `POST /invoke`. I lean `/` (simpler) with health on
  subpaths.
- **Metrics**: Prometheus client (`/metrics`) now, or defer to M5?
- **`apply` strategy**: shell out to `kubectl` (assumed) keeps deps minimal; the
  alternative is client-go server-side apply if you'd rather not depend on a kubectl
  binary being present.

---
*Next step: confirm the four decisions in §8, then I start M1.*
