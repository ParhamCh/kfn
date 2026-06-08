# kfn

A lightweight **function runtime** for Kubernetes: import the runtime, register one
handler, and your function becomes a hardened, long-lived HTTP service ready to run as
a pod. A companion CLI (coming in M3) generates the Kubernetes manifests to deploy it.

> Status: **M2 — operational hardening** complete (timeouts, panic recovery,
> concurrency limiting, graceful drain). See [`DESIGN.md`](DESIGN.md) for the full
> design and milestone plan.

## The contract

```go
package main

import (
	"context"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		name := req.Query.Get("name")
		if name == "" {
			name = "world"
		}
		return runtime.JSON(200, map[string]string{"message": "hello " + name})
	})
}
```

`runtime.Start` owns the HTTP server, routing, configuration, structured logging and
graceful shutdown. You only write the handler.

## HTTP surface

| Route                | Purpose                                                  |
|----------------------|----------------------------------------------------------|
| `/healthz`           | Liveness probe (reserved; any method)                    |
| `/readyz`            | Readiness probe (reserved; flips to 503 while draining)  |
| any other path/method| Invoke the function — it receives the method, path, query, headers and body |

A returned `error` becomes `500` (message hidden); return `runtime.Errorf(status, ...)`
to control the status and client-visible message. A `nil` response is `204 No Content`.
A handler **panic** is recovered into a `500` (the process keeps serving), an invocation
exceeding `INVOKE_TIMEOUT` returns `504`, and a request beyond `MAX_CONCURRENCY` is shed
with `429`. The `429`/`504` rates are clean per-pod saturation signals an autoscaler can
scale on.

## Configuration (environment)

| Variable          | Default   | Meaning                                          |
|-------------------|-----------|--------------------------------------------------|
| `FUNCTION_NAME`   | _(unset)_ | Function identity; tags every log line (and metrics from M5) so a per-function autoscaler can scope its signal. Injected by the manifest generator. |
| `PORT`            | `8080`    | Listen port                                      |
| `INVOKE_TIMEOUT`  | `30s`     | Max time for one invocation before `504`; `0` disables |
| `MAX_CONCURRENCY` | `0`       | Max simultaneous invocations per pod before `429`; `0` = unlimited |
| `SHUTDOWN_GRACE`  | `15s`     | Drain window after SIGTERM                        |
| `LOG_LEVEL`       | `info`    | `debug` / `info` / `warn` / `error`              |

Prometheus `/metrics` arrives in M5.

## Quickstart

```bash
# Run the reference function
go run ./examples/hello
# In another shell
curl -XPOST 'http://localhost:8080/?name=parham'   # {"message":"hello parham"}
curl http://localhost:8080/healthz                 # ok

# Test & vet
go test ./...
go vet ./...
```

## Contributing & releases

This repository follows **Git Flow**, **Conventional Commits** and **SemVer**. See
[`docs/git-workflow.md`](docs/git-workflow.md) before opening a branch.
