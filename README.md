# Beholdr

A lightweight Kubernetes observability control plane. One tiny static Go binary
(with the UI embedded) gives you a click-and-deploy dashboard for cluster
history, node and pod performance, pods-per-node, and per-microservice
scaling/autoscaling. Optional integrations discover the health of Prometheus,
Elasticsearch, and an OpenTelemetry Collector without copying telemetry into
Beholdr. The Go collector does the Kubernetes aggregation; the SvelteKit UI just
renders it.

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
- **Observability** — connection state for the Prometheus metrics source,
  Elasticsearch logs/traces source, and OpenTelemetry ingestion gateway.

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
GET /live                                 liveness: process can serve HTTP (no cluster dependency)
GET /ready                                readiness: 200 once a recent collection has succeeded, 503 otherwise
GET /api/health                           rich status for the UI (always 200): ready, last_success, last_error, metrics_available
GET /api/integrations                     configured/reachable state for external telemetry systems
GET /api/cluster                          cluster totals + history
GET /api/nodes                            all nodes
GET /api/nodes/{name}                     node detail + pods + history
GET /api/microservices                    all workloads
GET /api/microservices/{ns}/{name}        workload detail + pods + history
GET /api/pods                             all pods
```

`/api/*` data endpoints return `503` until the first collection succeeds, and
keep serving the last known-good snapshot after that even if later
collections fail — `/ready` and `/api/health` are what tell you the data
might be stale.

## Configuration (env vars)

| Var | Default | Meaning |
|-----|---------|---------|
| `BEHOLDR_ADDR` | `:8000` | Listen address |
| `BEHOLDR_POLL_INTERVAL` | `15` | Seconds between cluster polls |
| `BEHOLDR_HISTORY_SIZE` | `240` | Samples kept per series (240×15s ≈ 1h) |
| `BEHOLDR_NAMESPACES` | *(all)* | Comma-separated namespaces to watch |
| `BEHOLDR_KUBE_MODE` | `auto` | `auto` \| `in-cluster` \| `kubeconfig` |
| `KUBECONFIG` | *(default)* | kubeconfig path when not in-cluster |
| `BEHOLDR_CORS_ORIGINS` | *(empty — disabled)* | Comma-separated allowlist of origins permitted to call the API cross-origin. Empty disables CORS entirely. `*` allows any origin — local development only. |
| `BEHOLDR_PROMETHEUS_URL` | *(empty — disabled)* | Prometheus-compatible API base URL |
| `BEHOLDR_PROMETHEUS_BEARER_TOKEN` | *(empty)* | Optional bearer token; supply from a Kubernetes Secret |
| `BEHOLDR_ELASTICSEARCH_URL` | *(empty — disabled)* | Elasticsearch API base URL |
| `BEHOLDR_ELASTICSEARCH_API_KEY` | *(empty)* | Optional Elasticsearch API key; supply from a Kubernetes Secret |
| `BEHOLDR_OTEL_COLLECTOR_HEALTH_URL` | *(empty — disabled)* | URL exposed by the Collector `health_check` extension, commonly port 13133 |
| `BEHOLDR_INTEGRATION_CHECK_INTERVAL` | `30` | Seconds between integration health checks |
| `BEHOLDR_INTEGRATION_REQUEST_TIMEOUT` | `5` | Per-request integration timeout in seconds |

## Security model

Beholdr has **no built-in authentication** and does not terminate TLS itself.
Both are expected to be provided by whatever sits in front of it:

- **TLS** — terminate at the Ingress (or load balancer / service mesh). The
  Terraform module refuses to create an Ingress without a `tls_secret_name`
  unless you explicitly set `insecure_http = true`; the plain manifest in
  `deploy/k8s/beholdr.yaml` ships a cert-manager-annotated example.
- **Authentication** — put an OIDC-aware auth proxy in front of the Ingress,
  e.g. [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/) fronting
  your IdP, wired up via `nginx.ingress.kubernetes.io/auth-url` /
  `auth-signin` (or your ingress controller's equivalent). The Terraform
  module refuses to create an Ingress without `auth_annotations` unless you
  explicitly set `insecure_no_auth = true`.
- **CORS** — off by default (see `BEHOLDR_CORS_ORIGINS` above). Only needed
  if you host the UI separately from the API.

Beholdr exposes cluster topology, pod names, and resource usage — treat it
like any other cluster-admin-adjacent read path.

Integration credentials are outbound-only configuration. Beholdr never returns
their values or upstream response bodies through its API. Put them in Kubernetes
Secrets and expose them to the Beholdr container with `secretKeyRef`; do not put
credentials in endpoint URLs or Terraform state.

## Local development

```bash
# backend — uses your current kube context
go run ./cmd/beholdr # :8000

# frontend (separate shell) — proxies /api to :8000
cd web && npm ci && npm run dev   # :5173
```

Or run the full image against your current context:

```bash
docker compose up --build              # http://localhost:8000
```

`go.sum` and `web/package-lock.json` are committed, so `go build`/`go test`
and `npm ci` resolve the exact same dependency graph everywhere — locally, in
CI, and in the Docker build. Run `go mod tidy` after changing `go.mod`, or
`npm install` after changing `web/package.json`, and commit the result.

## Testing

```bash
go test ./... -race -cover
cd web && npm run check   # svelte-check (types)
```

`.github/workflows/ci.yml` runs both, plus a frontend production build and a
container build, on every pull request.

## Build & push

```bash
docker build -t registry.example.com/beholdr:0.1.0 .
docker push registry.example.com/beholdr:0.1.0
```

The build compiles the SvelteKit UI with `npm ci` and the Go binary against
the committed `go.sum` (no dependency resolution happens inside the build),
embeds the UI, and ships a distroless image (typically ~15–25 MB).

## Deploy with Terraform

```bash
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # set image + ingress_host
terraform init
terraform apply
```

Creates the namespace, a read-only ServiceAccount + ClusterRole (pods, nodes,
deployments, HPAs, metrics), the Deployment, a Service, and a configurable
Ingress (`ingress_class` / `ingress_host` / `ingress_annotations`). The
Ingress requires `tls_secret_name` and `auth_annotations` — `terraform apply`
fails with a clear error if either is missing, unless you explicitly opt out
via `insecure_http` / `insecure_no_auth` for a non-production environment.
See `terraform.tfvars.example` and [Security model](#security-model) above.
Prefer raw manifests? See `deploy/k8s/beholdr.yaml`.

## Notes & limits

History is in-memory, so it resets on pod restart and isn't shared across
replicas — run a single replica (the default). If you later want durable,
long-range history or alerting, configure external telemetry systems. The
current integration slice checks backend connectivity only; querying and
correlating their telemetry will be added behind the same provider boundary.
