# loadgen-go

A lightweight **function runtime** for Kubernetes: import the runtime, register one
handler, and your function becomes a hardened, long-lived HTTP service ready to run as
a pod. A companion CLI (coming in M3) generates the Kubernetes manifests to deploy it.

> Status: **M1 — runtime core** complete. See [`DESIGN.md`](DESIGN.md) for the full
> design and milestone plan.

## The contract

```go
package main

import (
	"context"

	"github.com/ParhamCh/loadgen-go/pkg/runtime"
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

| Route          | Purpose                                            |
|----------------|----------------------------------------------------|
| `POST /`       | Invoke the function                                |
| `GET /healthz` | Liveness probe                                     |
| `GET /readyz`  | Readiness probe (flips to 503 while draining)      |

A returned `error` becomes `500` (message hidden); return `runtime.Errorf(status, ...)`
to control the status and client-visible message. A `nil` response is `204 No Content`.

## Configuration (environment)

| Variable         | Default | Meaning                                  |
|------------------|---------|------------------------------------------|
| `PORT`           | `8080`  | Listen port                              |
| `SHUTDOWN_GRACE` | `15s`   | Drain window after SIGTERM               |
| `LOG_LEVEL`      | `info`  | `debug` / `info` / `warn` / `error`      |

Per-invocation timeouts, concurrency limits and `/metrics` arrive in later milestones.

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
