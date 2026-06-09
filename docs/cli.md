# `kfn` CLI reference

`kfn` is the deploy-side companion to the runtime library. It turns a function's
`function.yaml` into Kubernetes manifests, builds and pushes its image, and applies it to
a cluster.

```
kfn render -f function.yaml [-o out.yaml] [-n namespace]
kfn build  -f function.yaml [--func .] [--dockerfile build/Dockerfile] [--context .]
kfn push   -f function.yaml
kfn apply  -f function.yaml [-n namespace]
kfn version
```

Build it once:

```bash
go build -o bin/kfn ./cmd/kfn
```

Every command that takes `-f` accepts `-` to read `function.yaml` from **stdin**. On any
error `kfn` prints `kfn: <error>` to stderr and exits non-zero (`2` for usage errors,
`1` for runtime failures). See [function-yaml.md](function-yaml.md) for the file schema.

---

## `kfn render`

Render the manifests and print them (or write them to a file). Pure templating — it does
not touch the cluster, so it's the command to eyeball output or pipe elsewhere.

| Flag | Default | Meaning |
|------|---------|---------|
| `-f` | _(required)_ | Path to `function.yaml`, or `-` for stdin. |
| `-o` | stdout | Write the YAML to this file instead of stdout. |
| `-n` | _(from file)_ | Override the target namespace. |

```bash
bin/kfn render -f examples/hello/function.yaml              # → stdout
bin/kfn render -f examples/hello/function.yaml -o out.yaml
bin/kfn render -f examples/hello/function.yaml -n staging   # override namespace

# Quick sanity check: how many documents, what kinds?
bin/kfn render -f examples/hello/function.yaml | grep '^kind:'
```

The output is a multi-document stream: **Deployment** + **Service** always, then an
**Ingress** if `ingress.enabled: true`, then a **ServiceMonitor** if monitoring is on
(the default). Validate it against a live cluster without applying:

```bash
bin/kfn render -f examples/hello/function.yaml | kubectl apply --dry-run=server -f -
```

---

## `kfn build`

Build the function's container image using the reference Dockerfile. The image **tag**
comes from `image:` in `function.yaml`; the flags control *what* and *how* to compile.

| Flag | Default | Meaning |
|------|---------|---------|
| `-f` | _(required)_ | Path to `function.yaml` (supplies the image reference). |
| `--func` | `.` | Go package to compile into the image (passed as the `FUNC_PKG` build arg). |
| `--dockerfile` | `build/Dockerfile` | Path to the Dockerfile. |
| `--context` | `.` | Build context directory. |

From a function's own repo the defaults are right (`--func .`). This monorepo's bundled
example lives in a subpackage, so it overrides `--func`:

```bash
bin/kfn build -f examples/hello/function.yaml --func ./examples/hello
```

The equivalent raw engine command is:

```bash
docker build --build-arg FUNC_PKG=<func> -t <image> -f <dockerfile> <context>
```

See [Container engine](#container-engine) for `docker` vs `podman` selection.

---

## `kfn push`

Push the built image to the registry named in `image:`.

| Flag | Default | Meaning |
|------|---------|---------|
| `-f` | _(required)_ | Path to `function.yaml` (supplies the image reference). |

```bash
docker login harbor.lan                         # one-time, per registry
bin/kfn push -f examples/hello/function.yaml     # → docker push harbor.lan/kfn/hello:0.2.0
```

> **Tag caching gotcha.** Nodes pull by tag under the default `imagePullPolicy:
> IfNotPresent`. If you rebuild and push the *same* tag, already-running nodes keep the
> cached image. Bump the tag in `function.yaml` for every change you want rolled out.

---

## `kfn apply`

Render the manifests and apply them with `kubectl apply -f -`. Creates the target
namespace first if it doesn't exist (idempotent).

| Flag | Default | Meaning |
|------|---------|---------|
| `-f` | _(required)_ | Path to `function.yaml`, or `-` for stdin. |
| `-n` | _(from file)_ | Override the target namespace. |

```bash
bin/kfn apply -f examples/hello/function.yaml
bin/kfn apply -f examples/hello/function.yaml -n staging
```

Requires a working `kubectl` on `PATH` pointed at your cluster (`kubectl config
current-context`).

> **`apply` never prunes.** It only creates/updates the objects in the stream. If you
> turn `ingress.enabled` or `monitoring.enabled` off and re-apply, the previously created
> Ingress / ServiceMonitor is **not** deleted — remove it by hand:
> `kubectl -n <ns> delete ingress,servicemonitor <name>`.

---

## `kfn version`

```bash
bin/kfn version        # kfn dev   (or the value baked in via -ldflags)
```

The version is `dev` unless set at build time:

```bash
go build -ldflags "-X main.version=v0.6.0" -o bin/kfn ./cmd/kfn
```

---

## Container engine

`build` and `push` shell out to a container CLI, chosen in this order:

1. `KFN_CONTAINER_ENGINE` if set (must be on `PATH`, else it's an error).
2. otherwise `docker`, then `podman` — the first found on `PATH`.

```bash
KFN_CONTAINER_ENGINE=podman bin/kfn build -f function.yaml
```

On hosts where `docker` is a wrapper around `podman`, the default `docker` preference is
correct. If neither is installed `kfn` errors with a clear message.
