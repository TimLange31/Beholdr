# Beholdr

**See what's happening in your Kubernetes cluster, and know where to look next.**

Beholdr is a lightweight, read-only dashboard for the people running applications
on Kubernetes. It brings cluster health, resource usage, workload replicas, and
service health into one place so you can spot problems and investigate them
without piecing together every answer by hand.

## The goal

Beholdr aims to make everyday Kubernetes troubleshooting easier: **what is
running, what is struggling, and what has changed?** Start with the cluster,
follow a problem to a node or microservice, and use its pods and health history
to understand where to investigate next.

The longer-term goal is to connect infrastructure, metrics, logs, and traces in
one clear workflow, using the telemetry systems you already run. Today, Beholdr
provides cluster visibility and Prometheus-backed service health; log and trace
search and cross-signal correlation are still planned.

## What you can do today

- **Check the cluster at a glance.** See node readiness, pod status, CPU and
  memory usage, and recent trends.
- **Find resource pressure.** Explore individual nodes, the pods running on
  them, and how their usage changes over time.
- **Understand your workloads.** Inspect Deployment replicas, autoscaling
  settings, restarts, resource usage, and how pods are spread across nodes.
  Other workloads appear as pod-derived groups with more limited replica data.
- **Investigate service health.** Connect Prometheus for error rates, CPU,
  memory, and failing-pod charts over windows from one hour to 21 days,
  depending on available metrics and retention.
- **Check your telemetry connections.** See connectivity and health status for
  Prometheus, Elasticsearch, and an OpenTelemetry Collector.

Beholdr observes your cluster; it does not change workloads or scaling settings.
It runs as a single small container with the dashboard included.

## Try it locally

You need **Go 1.22 or newer**, **Node.js 20 with npm**, and a working kubeconfig
with permission to read nodes, pods, Deployments, HPAs, and metrics. Beholdr uses
your current Kubernetes context. Install **metrics-server** in the cluster for
live CPU and memory usage; topology and replica information work without it.

From the repository root, start the backend:

```sh
go run ./cmd/beholdr
```

In a second terminal, start the dashboard:

```sh
cd web
npm ci
npm run dev
```

Open [localhost:5173](http://localhost:5173). The dashboard forwards API requests
to the backend on port 8000. Prometheus and the other integrations are optional;
the cluster overview works without them.

For a container setup, see the [Docker instructions](docs/technical-guide.md#local-development).

## Run it in your cluster

Build your container image, then deploy using the included
[Terraform example](deploy/terraform/terraform.tfvars.example) or
[Kubernetes manifest](deploy/k8s/beholdr.yaml). The
[deployment guide](docs/technical-guide.md#deploy-with-terraform) covers the steps.

Beholdr has no built-in login or HTTPS server. Configure authentication and TLS
in front of it before exposing the dashboard. Run one replica: the recent
cluster history is held in memory and resets on restart. Longer service-health
history comes from your Prometheus instance.

## Where the project is heading

The next steps are to make the current observer dependable for production, then
add log and trace exploration and connect those signals to service health.
Notifications, built-in authentication, and Beholdr's own durable storage are
not available today. See the [production backlog](PRODUCTION_BACKLOG.md) for
remaining work and priorities.

## Working on Beholdr

The app uses Go and SvelteKit. Setup details, configuration options, API routes,
service-health metrics, and build instructions live in the
[technical guide](docs/technical-guide.md).

To run the existing checks:

```sh
go test ./... -race -cover
cd web
npm run check
npm run build
```
