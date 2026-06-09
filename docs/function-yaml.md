# `function.yaml` reference

`function.yaml` is the deploy manifest for a function — the input to `kfn render`,
`build`, `push` and `apply`. Only `name` and `image` are required; everything else is
defaulted. **Unknown keys are rejected**, so a typo surfaces immediately instead of being
silently ignored.

## Minimal

```yaml
name: hello
image: harbor.lan/kfn/hello:0.2.0
```

## Full example

```yaml
name: hello                       # required — DNS-1123 label
image: harbor.lan/kfn/hello:0.2.0 # required — full registry reference + tag
port: 8080                        # default 8080
replicas: 2                       # default 1
namespace: kfn                    # default kfn
nodeSelector:                     # default {role: workload}
  role: workload
resources:
  requests: { cpu: 50m,  memory: 64Mi }    # defaults
  limits:   { cpu: 250m, memory: 128Mi }   # defaults
shutdownGrace: 15s                # default 15s
env:
  - { name: LOG_LEVEL,      value: info }
  - { name: INVOKE_TIMEOUT, value: 30s }
  - { name: MAX_CONCURRENCY, value: "50" }
ingress:
  enabled: true                   # default false → ClusterIP only
  host: hello.kfn.lan             # default <name>.kfn.lan
monitoring:
  enabled: true                   # default true
```

---

## Top-level fields

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `name` | string | — **required** | Object name; must be a valid DNS-1123 label (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`). Injected into the pod as `FUNCTION_NAME`. |
| `image` | string | — **required** | Full image reference incl. registry and tag, e.g. `harbor.lan/kfn/hello:0.2.0`. Used by `build`/`push`/the Deployment. |
| `port` | int | `8080` | Function listen port. Must be `1–65535`. Injected as `PORT`. |
| `replicas` | int | `1` | Initial replica count. Must be `>= 0`. No HPA is created — your autoscaler (or `kubectl scale`) drives it afterward. |
| `namespace` | string | `kfn` | Target namespace. `apply` creates it if missing; `-n` overrides this. |
| `nodeSelector` | map | `{role: workload}` | Pins pods to nodes. Set explicitly to schedule elsewhere. |
| `resources.requests.cpu` | string | `50m` | Kubernetes CPU quantity. |
| `resources.requests.memory` | string | `64Mi` | Kubernetes memory quantity. |
| `resources.limits.cpu` | string | `250m` | |
| `resources.limits.memory` | string | `128Mi` | |
| `shutdownGrace` | duration | `15s` | The runtime's drain window (`SHUTDOWN_GRACE`). Drives `terminationGracePeriodSeconds = ceil(grace) + 5s`, so the kubelet never `SIGKILL`s a still-draining pod. |
| `env` | list of `{name, value}` | _(none)_ | Literal env vars added to the container. This is how you set `INVOKE_TIMEOUT`, `MAX_CONCURRENCY`, `LOG_LEVEL`, etc. (see [writing-functions.md](writing-functions.md#configuration-environment-variables)). |
| `ingress` | block | _(off)_ | See below. |
| `monitoring` | block | _(on)_ | See below. |

> Numeric env **values must be quoted** in YAML (`value: "50"`), because the value field
> is a string.

---

## `ingress:` — exposure + TLS (optional, off by default)

When `enabled: true`, `kfn` renders an extra `Ingress` that routes traffic to the function
through ingress-nginx, with TLS issued by cert-manager. Off by default → the function is
`ClusterIP`-only (reachable in-cluster, not from outside).

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `enabled` | bool | `false` | Master switch. |
| `host` | string | `<name>.kfn.lan` | The external hostname. Must be a valid DNS subdomain. **You own DNS** — point it at the ingress-nginx LoadBalancer IP. |
| `tls` | bool | `true` (when enabled) | Request a cert-manager certificate. |
| `clusterIssuer` | string | `cm-lab-ca` | cert-manager `ClusterIssuer` (required when `tls: true`). |
| `className` | string | `nginx` | `IngressClass` name (required when enabled). |
| `secretName` | string | `<name>-tls` | TLS secret the cert is issued into. |
| `annotations` | map | _(derived)_ | Your entries are **merged over** the derived nginx annotations and win on conflict. |

### Derived nginx annotations

`kfn` derives the nginx annotations from the runtime contract so the edge agrees with the
pod — you don't set these yourself:

- `nginx.ingress.kubernetes.io/proxy-body-size: 1m` — matches the runtime's 1 MiB body cap.
- `nginx.ingress.kubernetes.io/proxy-read-timeout` / `proxy-send-timeout` — set to
  **`INVOKE_TIMEOUT + 30s`** (reading `INVOKE_TIMEOUT` from your `env:`, default `30s`),
  so the runtime returns its own `504` before nginx severs the connection.
- when `tls: true`: `cert-manager.io/cluster-issuer: <clusterIssuer>` and
  `nginx.ingress.kubernetes.io/ssl-redirect: "true"`.

```yaml
ingress:
  enabled: true
  host: api.kfn.lan
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: 10m   # override the derived 1m
```

---

## `monitoring:` — Prometheus scraping (on by default)

Metrics are **on by default**. When on, the Deployment and Service gain a `metrics` port
and `kfn` renders a `ServiceMonitor` that the prometheus-operator discovers and scrapes.
Set `enabled: false` to drop the metrics port and the ServiceMonitor entirely.

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `enabled` | bool | `true` | Master switch. |
| `port` | int | `9090` | Metrics port; must match the runtime's `METRICS_PORT` and **differ from `port`**. Range `1–65535`. |
| `path` | string | `/metrics` | Scrape path. |
| `interval` | string | `30s` | Scrape interval. |
| `releaseLabel` | string | `kps` | The `release:` label the ServiceMonitor carries. The prometheus-operator selects ServiceMonitors by this label — it **must** match your operator's selector or nothing gets scraped. |

```yaml
monitoring:
  enabled: true
  interval: 15s
  releaseLabel: kps     # kube-prometheus-stack's default Helm release label
```

See [observability.md](observability.md) for the metrics themselves and PromQL recipes.

---

## Validation rules

`kfn` rejects a spec (with a `manifest: …` error) when:

- `name` is missing or not a valid DNS-1123 label.
- `image` is missing.
- `port` is outside `1–65535`.
- `replicas` is negative.
- the file contains an **unknown key**.
- `ingress.enabled` and: `host` isn't a valid DNS subdomain; `className` is empty; or
  `tls` is on but `clusterIssuer` is empty.
- monitoring is on and: `port` is outside `1–65535`; or `port` equals the function `port`.
