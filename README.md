# Beholdr

A hyper-lightweight Kubernetes observer. One tiny static Go binary (with the UI
embedded) gives you a click-and-deploy dashboard for cluster history, node and
pod performance, pods-per-node, and per-microservice scaling/autoscaling — no
PromQL, no external time-series database. The Go collector does all the
aggregation; the SvelteKit UI just renders it.

## Stack

- **Backend** — Go 1.22, standard-library `net/http` (method+pattern routing),
  `client-go` + the official `metrics.k8s.io` clientset, structured logging with
  `log/slog`. A background collector polls the cluster on an interval and keeps a
  rolling in-memory history behind an `RWMutex`.
- **Frontend** — SvelteKit (Svelte 5 runes) + Tailwind CSS v4, built as a static
  SPA and **embedded into the Go binary** via `embed`. Charts are hand-rolled SVG
  (zero chart dependencies).
- **Image** — multi-stage build to a `distroless/static` runtime: a single
  stripped, non-root, static binary. No interpreter, no node_modules at runtime.

## What it shows

- **Cluster** — nodes ready, pod counts by phase, cluster-wide CPU/memory with a
  rolling utilization history and running-pod trend.
- **Nodes** — per-node CPU/mem vs capacity, pods-per-node, which microservices
  run where, and per-node history.
- **Microservices** — every workload (Deployments, plus StatefulSets/DaemonSets)
  with replica counts, HPA range/target, summed CPU/mem, request utilization,
  node spread and restarts. Drill in for scaling history and the pod list.

> Prerequisite: **metrics-server** must be installed for live CPU/memory. Without
> it, topology and replica data still work but usage reads 0 (the UI shows a banner).

## Layout

```
cmd/beholdr/            main: wiring, graceful shutdown
internal/config/        env-driven configuration
internal/k8s/           client-go wrapper (millicores + bytes, no quantity math elsewhere)
internal/collect/       the monitor: collector, history ring buffers, JSON models
internal/api/           net/http server, routes, SPA/static handler
internal/webui/         go:embed of the built SvelteKit app (dist/)
web/                    SvelteKit + Tailwind source
deploy/terraform/       namespace, RBAC, Deployment, Service, Ingress
deploy/k8s/             plain-manifest equivalent
```

## API

```
GET /api/cluster                          cluster totals + history
GET /api/nodes                            all nodes
GET /api/nodes/{name}                     node detail + pods + history
GET /api/microservices                    all workloads
GET /api/microservices/{ns}/{name}        workload detail + pods + history
GET /api/pods                             all pods
GET /api/health                           liveness/readiness
```

## Configuration (env vars)

| Var | Default | Meaning |
|-----|---------|---------|
| `BEHOLDR_ADDR` | `:8000` | Listen address |
| `BEHOLDR_POLL_INTERVAL` | `15` | Seconds between cluster polls |
| `BEHOLDR_HISTORY_SIZE` | `240` | Samples kept per series (240×15s ≈ 1h) |
| `BEHOLDR_NAMESPACES` | *(all)* | Comma-separated namespaces to watch |
| `BEHOLDR_KUBE_MODE` | `auto` | `auto` \| `in-cluster` \| `kubeconfig` |
| `KUBECONFIG` | *(default)* | kubeconfig path when not in-cluster |
| `BEHOLDR_CORS` | `true` | Permissive CORS (handy for local dev) |

## Local development

```bash
# backend — uses your current kube context
go mod tidy          # once: resolves deps + writes go.sum
go run ./cmd/beholdr # :8000

# frontend (separate shell) — proxies /api to :8000
cd web && npm install && npm run dev   # :5173
```

Or run the full image against your current context:

```bash
docker compose up --build              # http://localhost:8000
```

## Build & push

```bash
docker build -t registry.example.com/beholdr:0.1.0 .
docker push registry.example.com/beholdr:0.1.0
```

The build compiles the SvelteKit UI, embeds it in the Go binary, and ships a
distroless image (typically ~15–25 MB).

## Deploy with Terraform

```bash
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # set image + ingress_host
terraform init
terraform apply
```

Creates the namespace, a read-only ServiceAccount + ClusterRole (pods, nodes,
deployments, HPAs, metrics), the Deployment, a Service, and a configurable
Ingress (`ingress_class` / `ingress_host` / `ingress_annotations` — point TLS or
basic-auth annotations there). Prefer raw manifests? See `deploy/k8s/beholdr.yaml`.

## Notes & limits

History is in-memory, so it resets on pod restart and isn't shared across
replicas — run a single replica (the default). If you later want durable,
long-range history or alerting, the same collector could write to a TSDB; the
current design deliberately trades that away for a zero-dependency single binary.
