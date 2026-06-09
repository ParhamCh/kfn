# kfn

A lightweight **function runtime** for Kubernetes: import the runtime, register one
handler, and your function becomes a hardened, long-lived HTTP service ready to run as
a pod. A companion CLI builds the image and generates the Kubernetes manifests to deploy it.

> Status: **M5 — ingress + TLS** complete (opt-in `<name>.kfn.lan` with cert-manager).
> The runtime (M1–M2), manifest generator (M3), image build/deploy (M4) and HTTPS
> exposure are in place. See [`DESIGN.md`](DESIGN.md) for the full design and milestone
> plan.

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

## Deploying to Kubernetes — the `kfn` CLI

Each function is its **own Deployment**, scaled independently. The `kfn` CLI turns a
`function.yaml` into a Deployment + Service and applies it.

```bash
go build -o bin/kfn ./cmd/kfn

# Inspect the generated manifests
bin/kfn render -f examples/hello/function.yaml          # → stdout
bin/kfn render -f examples/hello/function.yaml -o out.yaml

# Apply to the cluster (creates the target namespace if missing)
bin/kfn apply -f examples/hello/function.yaml
bin/kfn apply -f examples/hello/function.yaml -n staging # override namespace
```

`function.yaml` (only `name` and `image` are required; everything else is defaulted):

```yaml
name: hello
image: harbor.lan/kfn/hello:0.1.0
port: 8080            # default 8080
replicas: 2           # default 1
# namespace: kfn               (default)
# nodeSelector: {role: workload} (default — pins to workload nodes)
resources:
  requests: { cpu: 50m, memory: 64Mi }
  limits:   { cpu: 250m, memory: 128Mi }
env:
  - { name: LOG_LEVEL, value: info }
```

The generated Deployment wires the runtime's `/healthz`/`/readyz` probes, injects
`FUNCTION_NAME`, pins to `role=workload` nodes, sets resource requests, and runs as a
non-root, read-only-rootfs container. **No HorizontalPodAutoscaler is created** — scale
with `kubectl scale deploy/<name> -n kfn --replicas=N` (or your own autoscaler).
A `ServiceMonitor` for Prometheus arrives in M6 with `/metrics`.

### Exposing a function (Ingress + TLS)

A function is `ClusterIP`-only unless it opts in. Add an `ingress:` block and `kfn`
also renders an `Ingress` that routes `https://<name>.kfn.lan` through ingress-nginx,
with TLS issued by cert-manager:

```yaml
ingress:
  enabled: true              # default false → ClusterIP only
  host: hello.kfn.lan        # default <name>.kfn.lan
  # tls: true                  (default; cert issued into <name>-tls)
  # clusterIssuer: cm-lab-ca   (default)
  # className: nginx           (default)
  # secretName: hello-tls      (default <name>-tls)
  # annotations: {...}         (override/extend the derived nginx annotations)
```

`kfn` derives the nginx annotations from the runtime contract so the edge agrees with
the pod: `proxy-body-size: 1m` (the runtime's 1 MiB body cap) and
`proxy-read/send-timeout` set above `INVOKE_TIMEOUT` (so the runtime returns its own
`504` first), plus `ssl-redirect` and the `cert-manager.io/cluster-issuer` annotation
when TLS is on. Your own `annotations:` win over the derived ones.

You own DNS: point `<name>.kfn.lan` at the ingress-nginx LoadBalancer IP
(`10.10.0.240` here) via your resolver or `/etc/hosts`.

## Building & shipping the image

The same reference [`build/Dockerfile`](build/Dockerfile) builds any function: the
author's `main.go` plus the kfn runtime compile into one static binary on top of
`distroless/static:nonroot` (runs as uid 65532, matching the Deployment). The image
reference comes from `function.yaml`; `kfn build`/`push` shell out to `docker` (or
`podman` — override with `KFN_CONTAINER_ENGINE`).

```bash
# One-time: authenticate to the registry and create a (public) project for the images.
docker login harbor.lan

# Build the example. --func selects the package to compile (default ".", i.e. the
# function's own repo root); the bundled example lives in ./examples/hello.
bin/kfn build -f examples/hello/function.yaml --func ./examples/hello
bin/kfn push  -f examples/hello/function.yaml      # → harbor.lan/kfn/hello:0.1.0

# Deploy, then scale by hand (each function is its own Deployment).
bin/kfn apply -f examples/hello/function.yaml
kubectl -n kfn get pods -o wide                    # pods land on role=workload nodes
kubectl -n kfn scale deploy/hello --replicas=5     # the autoscaler's eventual lever
```

## Contributing & releases

This repository follows **Git Flow**, **Conventional Commits** and **SemVer**. See
[`docs/git-workflow.md`](docs/git-workflow.md) before opening a branch.
