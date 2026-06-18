# Deploying a function, end to end

This walks a function from source to a running, exposed, scraped Deployment. It uses the
bundled `examples/hello`; for your own function, drop the `--func ./examples/hello`
override and run the commands from your function's repo.

Prerequisites: a Go toolchain, a container engine (`docker` or `podman`), `kubectl`
pointed at your cluster, and push access to your registry.

```bash
go build -o bin/kfn ./cmd/kfn      # build the CLI once
```

## 1. Write the function and its manifest

See [writing-functions.md](writing-functions.md) for the handler, and
[function-yaml.md](function-yaml.md) for the manifest. The example ships both already:

```yaml
# examples/hello/function.yaml
name: hello
image: harbor.lan/kfn/hello:0.2.1
replicas: 2
ingress:
  enabled: true
  host: hello.kfn.lan
# monitoring is on by default
```

## 2. Eyeball the rendered manifests

Always render before applying — it's pure templating, no cluster contact:

```bash
bin/kfn render -f examples/hello/function.yaml | grep '^kind:'
# Deployment
# Service
# Ingress         (because ingress.enabled: true)
# ServiceMonitor  (monitoring on by default)
```

Validate against the live API without changing anything:

```bash
bin/kfn render -f examples/hello/function.yaml | kubectl apply --dry-run=server -f -
```

## 3. Build and push the image

```bash
docker login harbor.lan                                            # one-time per registry
bin/kfn build -f examples/hello/function.yaml --func ./examples/hello
bin/kfn push  -f examples/hello/function.yaml                      # → harbor.lan/kfn/hello:0.2.1
```

The image tag comes from `image:` in `function.yaml`. **Bump that tag for every change you
ship** — nodes cache by tag under `imagePullPolicy: IfNotPresent`, so re-pushing the same
tag won't roll out to already-running nodes.

If your registry project is private, create an `imagePullSecret`; the bundled example uses
a public project so none is needed.

## 4. Apply to the cluster

```bash
bin/kfn apply -f examples/hello/function.yaml      # creates the namespace if missing
```

Verify the rollout:

```bash
kubectl -n kfn get deploy,pods,svc -o wide         # pods land on role=workload nodes
kubectl -n kfn rollout status deploy/hello
kubectl -n kfn logs deploy/hello -f                # structured JSON access logs
```

Hit it in-cluster:

```bash
kubectl -n kfn port-forward svc/hello 8080:8080 &
curl 'http://localhost:8080/?name=cluster'         # {"message":"hello cluster"}
curl -i http://localhost:8080/ | grep -i x-request-id
```

## 5. Expose it (Ingress + TLS)

With `ingress.enabled: true` (step 1), `kfn` already rendered an `Ingress`. Two things are
your responsibility:

1. **DNS.** Point the host at the ingress-nginx LoadBalancer IP — via your resolver or, for
   a quick local test, `/etc/hosts`:

   ```bash
   echo '10.10.0.240 hello.kfn.lan' | sudo tee -a /etc/hosts
   ```

2. **Wait for the certificate** (cert-manager issues it from the `ClusterIssuer`):

   ```bash
   kubectl -n kfn get ingress hello
   kubectl -n kfn get certificate hello-tls          # READY should flip to True
   ```

Then:

```bash
curl -I https://hello.kfn.lan/                       # HTTP/2 200
curl -I http://hello.kfn.lan/                        # 308 → https (ssl-redirect)
```

If you trust a private CA (e.g. `cm-lab-ca`), add `--cacert` or `-k` for a quick check.

## 6. Scale

No HorizontalPodAutoscaler is created — scaling is deliberately left to you (and,
eventually, your own autoscaler driving the scale subresource):

```bash
kubectl -n kfn scale deploy/hello --replicas=5
kubectl -n kfn scale deploy/hello --replicas=2
```

## 7. Observe

Metrics are on by default. Confirm Prometheus discovered the function and query its
signals — see [observability.md](observability.md). In short:

```bash
kubectl -n kfn get servicemonitor hello
kubectl -n kfn port-forward svc/hello 9090:9090 &
curl -s http://localhost:9090/metrics | grep '^kfn_'
```

---

## Updating a deployed function

```bash
# 1. bump the tag in function.yaml, e.g. 0.2.0 → 0.2.1
# 2. rebuild, push, re-apply
bin/kfn build -f examples/hello/function.yaml --func ./examples/hello
bin/kfn push  -f examples/hello/function.yaml
bin/kfn apply -f examples/hello/function.yaml
kubectl -n kfn rollout status deploy/hello
```

## Tearing things down

`kfn apply` **never prunes**. Turning off `ingress`/`monitoring` and re-applying leaves the
old objects behind — and there's no `kfn delete`. Remove objects with `kubectl`:

```bash
kubectl -n kfn delete deploy,svc,ingress,servicemonitor hello
# the TLS Certificate is owned by the Ingress and is cleaned up with it
```

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| Pods `Pending` | No node matches `nodeSelector` (default `role=workload`), or resource requests don't fit. `kubectl describe pod`. |
| `ImagePullBackOff` | Tag not pushed, wrong registry, or missing `imagePullSecret` for a private project. |
| New code not running | Re-pushed the **same tag**; nodes served the cached image. Bump the tag. |
| `404` from the Ingress | DNS not pointed at the LB IP, or `host` mismatch. |
| Cert never `Ready` | `ClusterIssuer` wrong/not ready, or the ACME/CA challenge can't resolve the host. `kubectl describe certificate <name>-tls`. |
| Function not scraped | ServiceMonitor `releaseLabel` doesn't match the operator's selector. See [observability.md](observability.md). |
| Disabled Ingress still serving | `apply` doesn't prune — delete it by hand. |
