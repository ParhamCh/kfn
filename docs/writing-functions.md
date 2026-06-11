# Writing functions

A kfn function is an ordinary Go `main` package that imports the runtime, registers
**one** handler, and calls `runtime.Start`. The runtime owns the HTTP server, routing,
configuration, logging, metrics, timeouts, concurrency limiting, panic recovery and
graceful shutdown — you write only the handler.

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

`runtime.Start(h Handler)` blocks until the process receives `SIGINT`/`SIGTERM`, then
drains and returns. Pass `nil` and it logs an error and exits `1`.

## The handler contract

```go
type Handler func(ctx context.Context, req *Request) (*Response, error)
```

- **One handler per process.** kfn is one-function-per-Deployment; there is no router for
  you to register multiple routes on. Branch on `req.Method` / `req.Path` inside the
  handler if you need sub-behaviors.
- The `ctx` carries the per-invocation deadline (`INVOKE_TIMEOUT`) and is cancelled when
  the client disconnects. Pass it down to every blocking call (DB, HTTP, etc.) so slow
  work is abandoned promptly.

### Request

```go
type Request struct {
	Method  string      // GET, POST, …
	Path    string      // URL path, e.g. "/users/42"
	Headers http.Header // canonicalized request headers
	Query   url.Values  // parsed query string
	Body    []byte      // request body, capped at 1 MiB (see below)
}
```

The type is transport-agnostic on purpose, so non-HTTP triggers (queues, cron) can be
added later without changing your handler signature.

### Response

```go
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}
```

Don't build this by hand — use the helpers:

| Helper | Returns | Use |
|--------|---------|-----|
| `runtime.Text(status, s string)` | `*Response` | `text/plain` body |
| `runtime.JSON(status, v any)` | `(*Response, error)` | marshals `v` to `application/json` |
| `runtime.Bytes(status, contentType string, b []byte)` | `*Response` | explicit content type |

`JSON` returns **two values** because marshalling can fail, so it slots straight into a
`return`:

```go
return runtime.JSON(200, payload)          // JSON already returns (*Response, error)
return runtime.Text(200, "pong"), nil      // Text/Bytes return one value — add nil
```

Returning `nil, nil` produces **`204 No Content`**.

## Errors

A returned `error` becomes a **masked `500`** — the message is logged but never sent to
the client, so internal details can't leak. To control the client-facing status and
message, return a typed `*HTTPError` via `runtime.Errorf`:

```go
if name == "" {
	return nil, runtime.Errorf(400, "query param 'name' is required")
}
```

```go
type HTTPError struct {
	Status  int
	Message string
}
func Errorf(status int, format string, args ...any) *HTTPError
```

`Errorf` is variadic and formats like `fmt.Errorf`. Any non-`HTTPError` error → `500`.

## What the runtime does around your handler

These are automatic — you don't wire them, but you should know they exist:

| Behavior | Trigger | Client sees |
|----------|---------|-------------|
| Body cap | request body > 1 MiB | `413 request body too large` |
| Timeout | invocation exceeds `INVOKE_TIMEOUT` | `504 function timed out`; `ctx` cancelled |
| Concurrency shed | in-flight invocations > `MAX_CONCURRENCY` | `429 too many concurrent requests` |
| Panic recovery | handler panics | `500` (masked); stack logged; process keeps serving |

A caveat on timeouts: Go can't force-kill a goroutine. When the deadline fires the client
gets its `504` immediately, but a handler that ignores `ctx` keeps running in the
background until it returns. **Always honor `ctx`.**

## Request IDs

Every invocation has an `X-Request-Id`: the runtime honors an inbound one or generates a
128-bit hex id, echoes it on the response, and adds `request_id` to the access log. Read
it in your handler to correlate your own logs with the runtime's:

```go
slog.Info("processing", "request_id", runtime.RequestID(ctx), "name", name)
```

`runtime.RequestID(ctx)` returns `""` if none is set (e.g. in a unit test that doesn't go
through the middleware).

## Configuration (environment variables)

The runtime reads its config from the environment at startup. Locally you set these in
your shell; in the cluster the manifest generator injects them (see
[function-yaml.md](function-yaml.md)).

| Variable | Default | Meaning |
|----------|---------|---------|
| `FUNCTION_NAME` | _(unset)_ | Function identity; the `function` label on every log line and metric. Injected from the function's name. |
| `PORT` | `8080` | Function listen port. |
| `METRICS_PORT` | `9090` | Dedicated Prometheus `/metrics` port, kept off the function port. |
| `INVOKE_TIMEOUT` | `30s` | Per-invocation deadline → `504`. `0` disables. |
| `MAX_CONCURRENCY` | `0` | Max simultaneous invocations per pod → `429`. `0` = unlimited. |
| `SHUTDOWN_GRACE` | `15s` | Drain window after `SIGTERM`. |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |

Durations use Go syntax (`500ms`, `30s`, `2m`). An invalid value logs a warning and falls
back to the default rather than crashing.

## Running locally

```bash
# From your function's repo (or this repo, for the bundled example):
go run ./examples/hello

# In another shell:
curl 'http://localhost:8080/?name=parham'      # {"message":"hello parham"}
curl -i http://localhost:8080/healthz          # 200 ok
curl -i http://localhost:8080/readyz           # 200 ready
curl -s http://localhost:9090/metrics | head   # metrics on the dedicated port

# Try the guard rails:
MAX_CONCURRENCY=1 INVOKE_TIMEOUT=2s LOG_LEVEL=debug go run ./examples/hello
```

## Structuring your own function repo

A real function lives in its own repository:

```
my-func/
├── go.mod                 # requires github.com/ParhamCh/kfn
├── main.go                # imports runtime, calls runtime.Start
└── function.yaml          # deploy metadata (see function-yaml.md)
```

```bash
go mod init github.com/you/my-func
go get github.com/ParhamCh/kfn/pkg/runtime
```

The reference [`build/Dockerfile`](../build/Dockerfile) compiles your `main.go` + the kfn
runtime into one static binary. Because it runs `go mod download`, your repo needs a
committed `go.mod`/`go.sum`. From your own repo the build package is `.` (the default);
this monorepo's example overrides it with `--func ./examples/hello`. See
[cli.md](cli.md) and [deploying.md](deploying.md).

## See also

- [`function-yaml.md`](function-yaml.md) — the deploy manifest schema.
- [`cli.md`](cli.md) — the `kfn` command reference.
- [`deploying.md`](deploying.md) — build → push → deploy → expose, end to end.
- [`observability.md`](observability.md) — metrics, ServiceMonitor and tracing.
