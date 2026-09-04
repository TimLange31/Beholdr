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
  Elasticsearch logs/traces source, and OpenTelemetry ingestion gateway,
  including whether each one calls itself healthy and whether its certificate
  is actually being verified.
- **Service health** — bounded Prometheus-backed charts from one hour through
  21 days for HTTP 5xx rate (with the week-before series), CPU/request usage,
  memory/limit usage, and failing pods.

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
GET /api/microservices/{ns}/{name}/metrics?range=24h
                                         fixed service signals; range: 1h|6h|24h|7d|21d
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
| `BEHOLDR_PROMETHEUS_URL` | *(empty — disabled)* | Prometheus **server root**, e.g. `http://prometheus.monitoring.svc:9090` — the check calls `/-/ready` beneath it, so this is not the `/api/v1` query path |
| `BEHOLDR_PROMETHEUS_BEARER_TOKEN` | *(empty)* | Optional bearer token; supply from a Kubernetes Secret |
| `BEHOLDR_PROMETHEUS_CA_FILE` | *(system roots)* | PEM bundle that signs Prometheus's certificate, appended to the system trust store |
| `BEHOLDR_PROMETHEUS_TLS_INSECURE` | `false` | Skip certificate verification. Last resort — logged at WARN, shown as unverified in the UI |
| `BEHOLDR_ELASTICSEARCH_URL` | *(empty — disabled)* | Elasticsearch base URL — the check calls `/_cluster/health?local=true` beneath it |
| `BEHOLDR_ELASTICSEARCH_API_KEY` | *(empty)* | Optional Elasticsearch API key; supply from a Kubernetes Secret |
| `BEHOLDR_ELASTICSEARCH_CA_FILE` | *(system roots)* | PEM bundle that signs Elasticsearch's certificate (for ECK, the `ca.crt` in `<cluster>-es-http-certs-public`) |
| `BEHOLDR_ELASTICSEARCH_TLS_INSECURE` | `false` | Skip certificate verification. Last resort — logged at WARN, shown as unverified in the UI |
| `BEHOLDR_OTEL_COLLECTOR_HEALTH_URL` | *(empty — disabled)* | Full URL exposed by the Collector `health_check` extension, commonly port 13133 (requested as given, not a base) |
| `BEHOLDR_OTEL_COLLECTOR_CA_FILE` | *(system roots)* | PEM bundle that signs the Collector's certificate |
| `BEHOLDR_OTEL_COLLECTOR_TLS_INSECURE` | `false` | Skip certificate verification. Last resort — logged at WARN, shown as unverified in the UI |
| `BEHOLDR_INTEGRATION_CHECK_INTERVAL` | `30` | Seconds between integration health checks |
| `BEHOLDR_INTEGRATION_REQUEST_TIMEOUT` | `5` | Per-request integration timeout in seconds |

## Security model

Beholdr has **no built-in authentication** and does not terminate TLS itself.
Both are expected to be provided by whatever sits in front of it:

- **TLS** — terminated either in the cluster or in front of it; see
  [TLS termination](#tls-termination) below. The Terraform module refuses to
  create an Ingress that is not covered by one of those, and the plain manifest
  in `deploy/k8s/beholdr.yaml` carries both variants.
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

Outbound checks never follow redirects, so a redirected health endpoint can
never replay a token to another host, and failures are reported through a fixed
vocabulary (`connection refused`, `DNS lookup failed`, `TLS certificate signed
by an unknown authority — set the CA bundle`, …) that interpolates nothing from
the endpoint, the credential, or the upstream response. The full error goes to
the pod log.

## Service health queries

Beholdr owns the PromQL templates and only exposes the five bounded windows
`1h`, `6h`, `24h`, `7d`, and `21d`. The browser cannot submit arbitrary PromQL.
Each request is limited to 2,000 evaluation points and a 16 MiB response.

| Signal | Calculation | Warning | Critical |
|--------|-------------|---------|----------|
| HTTP error rate | HTTP 5xx request rate / all HTTP request rate | 1% | 5% |
| Error-rate increase | Current rate minus the aligned `offset 1w` series | +0.5 percentage points | +2 percentage points |
| CPU | Container CPU usage / Kubernetes CPU requests | 80% | 95% |
| Memory | Container working set / Kubernetes memory limits | 80% | 95% |
| Failing pods | Crash/image/config waiting pods plus Failed/Unknown pods | ≥10% (minimum 1) | ≥25% (minimum 2; 1 for a single-replica service) |

These thresholds currently produce health states in the API and UI; they do
not send notifications yet. Missing metrics produce `unknown`, never a green
status. The initial NLZIET metric profile uses
`aspnetcore_requests_duration_seconds_count` with `code=~"5.."`, divided by
the same request counter without the status filter and scoped by
`kubernetes_namespace` and `app_kubernetes_io_name`. Kubernetes signals use
cAdvisor/kube-state-metrics with their separate `namespace` and `pod` labels.
Deployment infrastructure series are selected by their stable pod-name pattern
so historical pods remain visible across rollouts.

A complete week-before overlay across the full 21-day window requires 28 days
of Prometheus retention. With exactly 21 days retained, the current series is
complete but the comparison series is absent for the earliest seven days.

### TLS termination

Beholdr never serves TLS itself. Where it is terminated is a deployment choice,
and the Terraform module makes it explicit with `tls_mode`:

| `tls_mode` | Who holds the certificate | When |
|-----------|---------------------------|------|
| `cluster` *(default)* | The Ingress, via `tls_secret_name` | cert-manager, your own PKI, or a **Cloudflare Origin CA** certificate for Cloudflare Full / Full (strict) |
| `edge` | Something in front of the cluster | Cloudflare Flexible SSL, a Cloudflare Tunnel, an external load balancer — **no certificate or key exists in the cluster at all** |

In `edge` mode nothing in the cluster needs a cert or key: the module emits no
`tls` block, requests no certificate, and mounts no key material. Two things
matter in that mode, and the module handles both:

1. **The redirect must be switched off, not merely left unset.** The edge
   already rewrites `http` to `https` for the public hostname, and then reaches
   the origin over plain HTTP. If ingress-nginx also redirects to `https`, the
   edge re-requests over HTTP and the two bounce off each other —
   `ERR_TOO_MANY_REDIRECTS`. `edge` mode sets `ssl-redirect` and
   `force-ssl-redirect` to `"false"` for you.
2. **The origin now answers plain HTTP.** Anything that can route to the
   Ingress bypasses the edge and whatever authentication sits there. Set
   `edge_source_ranges` to the [Cloudflare IP ranges](https://www.cloudflare.com/ips/)
   (or use a Cloudflare Tunnel, which exposes no origin at all).

`edge` mode also requires `edge_tls_terminator` — a name like `"cloudflare"`,
recorded in state as the reason this Ingress carries no certificate. That is
deliberate: an Ingress without TLS should never be reachable by accident, only
by a decision someone wrote down. `insecure_http` remains for local and
throwaway environments, where TLS exists nowhere at all.

The same distinction applies in the other direction. When a backend presents a
certificate the system trust store does not know — an ECK-issued Elasticsearch
cert, a Cloudflare Origin CA cert, a corporate PKI — mount its CA bundle
(`integration_ca_secret_name` plus the per-provider `*_ca_secret_key`) rather
than reaching for `*_tls_insecure`. Verification stays on, and each provider
gets its own HTTP client, so one backend's trust settings never apply to
another. If verification really must be disabled, Beholdr logs it at WARN on
startup and the Observability page renders that provider as unverified, so the
decision stays visible instead of ageing into the cluster.

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
Ingress requires TLS and `auth_annotations` — `terraform apply` fails with a
clear error if either is missing. TLS is satisfied by `tls_secret_name`
(`tls_mode = "cluster"`) or by naming what terminates it in front of the
cluster (`tls_mode = "edge"` + `edge_tls_terminator`); authentication can be
opted out of with `insecure_no_auth`, and TLS entirely with `insecure_http`,
for non-production environments.
See `terraform.tfvars.example` and [Security model](#security-model) above.
Prefer raw manifests? See `deploy/k8s/beholdr.yaml`.

## Notes & limits

Kubernetes collector history is still in-memory, so it resets on pod restart
and isn't shared across replicas — run a single replica (the default).
Prometheus-backed service-health charts are read directly from the external
store and therefore support the configured backend's durable history. Log and
trace querying and cross-signal correlation are still to be added behind the
same provider boundary.
