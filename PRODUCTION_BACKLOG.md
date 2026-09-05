# Beholdr production backlog

## Product decision

Beholdr is currently a lightweight Kubernetes resource observer with external
telemetry-provider integration. It is **not** yet a replacement for New Relic,
Grafana, or Prometheus: Kubernetes state still has about one hour of in-memory
history, while Prometheus supplies bounded per-service long-range charts.
Beholdr has no notification delivery, log/trace search, durable storage of its
own, built-in authentication, or arbitrary metric query capability.

The recommended release path is therefore two tracks:

1. Make the existing observer safe and dependable for production use.
2. Design and build an observability platform deliberately, instead of trying
   to extend the polling loop into a Prometheus/New Relic substitute.

Priorities: **P0** blocks a production release; **P1** belongs in the first
production-ready release; **P2** is planned follow-up.

## Delivered foundations

- [x] Correct liveness/readiness semantics and stale-data reporting.
- [x] Secure deployment boundary: CORS disabled by default, explicit TLS mode,
  and required ingress authentication unless deliberately overridden.
- [x] Deterministic Go/frontend dependency locks, PR tests, frontend build, and
  container-build quality gates.
- [x] Read-only Prometheus, Elasticsearch, and OpenTelemetry Collector health
  integrations with provider-scoped TLS and secret-backed credentials.
- [x] Fixed, bounded per-service PromQL queries and charts for HTTP error
  rate/week comparison, CPU, memory, and failing pods — configurable metric and
  label profile, thresholds validated at startup, severity scored from an
  instant query so it does not vary with the selected chart window, and reports
  cached, de-duplicated and concurrency-bounded so the UI cannot amplify load
  onto Prometheus.
- [x] Dependency and toolchain vulnerability triage against the actual
  release build (see below), with the Go build toolchain and frontend build
  toolchain deliberately upgraded rather than accepting `npm audit fix
  --force`'s downgrade suggestions.

## Dependency and toolchain vulnerability triage (2026-09-05)

- **Go toolchain.** `govulncheck` found 28 reachable advisories, all in the
  Go standard library shipped inside the compiled binary (`crypto/tls`,
  `crypto/x509`, `net/http`, `encoding/asn1`, etc.) plus two in
  `golang.org/x/net` (GO-2026-4918, GO-2026-5026). The release binary is a
  static Go build, so the compiler's own standard library is part of its
  vulnerability surface — this was not caught by dependency scanning alone.
  Fixed by pinning the release Docker build image to a current patch release
  (`golang:1.25.14-alpine`, replacing a floating, unsupported
  `golang:1.22-alpine`) and bumping `golang.org/x/net`/`x/oauth2`/`x/sys`/
  `x/term`/`x/text`/`x/time` to current versions. Re-running `govulncheck`
  against the updated graph reports zero reachable module vulnerabilities.
- **npm/frontend.** `npm audit` reported 7 findings (1 high, 3 moderate, 3
  low), all rooted in the Vite 5.x dev-server toolchain (path traversal and
  request-handling issues in Vite/esbuild's dev server) — not reachable in
  the shipped app, since production serves a prebuilt static SPA from the Go
  binary and never runs the Vite dev server, but still worth fixing since
  `npm run dev` is used locally. Fixed deliberately: Vite 5→6,
  `@sveltejs/vite-plugin-svelte` 4→5, and `@sveltejs/kit`/
  `@sveltejs/adapter-static`/`svelte-check`/`typescript` bumped to their
  latest compatible releases, verified with a clean `npm run check` and
  `npm run build`.
- **Remaining, accepted.** One low-severity finding (`cookie` < 0.7.0,
  GHSA-pxg6-pf52-xh8x) is pinned by `@sveltejs/kit`@2.70.3, the latest
  stable release — the fix ships only in SvelteKit's 3.0.0 prerelease line.
  Beholdr uses `adapter-static` (a prerendered SPA with no SvelteKit server
  runtime or cookie handling in production), so this finding is not
  reachable in the shipped app. Revisit once SvelteKit 3 stabilizes.
- **Proposed severity policy** (needs explicit sign-off before it gates
  releases): block a release on any *reachable* high/critical finding in
  the built binary/image or the production frontend bundle; track
  dev-toolchain-only and non-reachable findings here instead of blocking on
  them. Continuous enforcement (running `govulncheck`/`npm audit` in CI) is
  still open — see "Finish automated quality gates" below.

## P0 — remaining release blockers

- [ ] **Make collector health and metric availability correct under failure.**
  Synchronize client state or return availability/errors as part of each
  collection result; recover to “available” after a later successful metrics
  read; show partial-data and stale-data states in the UI.  
  _Why:_ `MetricsAvailable` is read and written from concurrent goroutines,
  becomes permanently false after one error, and current zero values are
  indistinguishable from missing metrics.

- [ ] **Finish supply-chain hardening.** Dependency locks and deterministic
  builds are in place. Pin base images and GitHub Actions by digest/immutable
  revision, generate an SBOM, and scan image/dependencies in CI.

- [ ] **Finish automated quality gates.** Backend/frontend tests and builds plus
  the container build run on pull requests. Add IaC validation, vulnerability
  and license checks, publish coverage, and fail on coverage regressions.

- [ ] **Fix documented-versus-actual workload coverage.** Either implement
  StatefulSet and DaemonSet discovery with their real desired/ready status, or
  remove the claim that they are first-class microservices. Handle Jobs and
  CronJobs explicitly rather than grouping them as “Other”.

- [ ] **Run a security and scale validation against a representative cluster.**
  Validate least-privilege RBAC, namespace isolation, API response size,
  collection duration, API-server request rate, memory growth, and UI behavior
  at the target pod/node/workload counts. Define and test a supported scale
  envelope before launch.

## P1 — production-ready observer

- [ ] **Persist history and define retention.** Add a pluggable durable store,
  migrations, retention/downsampling, backups/restores, and a graceful
  degradation path. Keep an in-memory option for local/demo use.

- [ ] **Provide high availability.** Make collectors horizontally safe via
  leader election/sharding and shared storage, add a PodDisruptionBudget, and
  document recovery and upgrade behavior. A single in-memory replica must not
  be the source of truth for operational data.

- [ ] **Use Kubernetes watches/informers.** Replace repeated full object lists
  with shared informers and cache state; retain periodic reconciliation as a
  correctness backstop. Bound concurrency and expose collection latency/error
  metrics.

- [ ] **Improve resource accuracy.** Calculate scheduling pressure from
  allocatable (not raw capacity), expose requests *and* limits, account for
  init containers/pod overhead, distinguish usage samples from instantaneous
  utilization, and surface missing or stale metrics explicitly.

- [ ] **Cover core Kubernetes operational signals.** Add pod/container state
  reasons, OOMKills, CrashLoopBackOff, restart rate, image/version, owner
  hierarchy, rollout progress, unschedulable pods, node conditions/pressure,
  PVC status, and Kubernetes Events with correlation to each workload.

- [ ] **Add filtering and safe API contracts.** Implement pagination,
  namespace/label/status filters, sorting, server-side time-range queries,
  stable versioned API schemas, validation and encoded path handling, request
  limits/timeouts, compression, and a generated OpenAPI contract.

- [ ] **Improve the operator experience.** Add global search, a unified
  workload/pod detail route, time-range selection and zoom, table sorting,
  empty/error/stale states, saved URL filters, responsive/mobile layout, and
  accessibility and keyboard checks.

- [ ] **Operate Beholdr itself.** Expose Prometheus-format self-metrics,
  structured request logs, traces, profiling, audit logs, dashboards, and an
  operational runbook (install, upgrade, rollback, recovery, and incident
  triage).

- [ ] **Harden deployment defaults.** Add security context (read-only root
  filesystem, dropped capabilities, seccomp), NetworkPolicy guidance, resource
  sizing, image pull policy, PDB, pod anti-affinity/topology spread where HA is
  enabled, and a Helm chart with values/schema validation. Keep raw manifest
  and Terraform outputs aligned.

- [ ] **Document supported environments and failure modes.** Include
  metrics-server requirements, Kubernetes version matrix, RBAC modes,
  namespace-scoped deployment option, capacity limits, data-retention
  guarantees, and troubleshooting. Remove or add the missing Terraform README
  referenced by the root README.

## P1 — platform architecture decision (required before claiming replacement)

- [ ] **Write an observability architecture RFC.** Decide whether Beholdr will
  integrate with Prometheus-compatible/OpenTelemetry systems or own ingestion,
  storage, query, and alert evaluation. Define tenancy, data model, retention,
  cardinality limits, cost model, availability SLOs, and a migration path.

- [ ] **Adopt OpenTelemetry as the integration contract.** Support OTLP for
  traces, metrics, and logs; propagate service/resource attributes; provide
  Kubernetes enrichment; and publish language/platform onboarding examples.

- [ ] **Build a metrics platform.** Ingest application, Kubernetes, host,
  network, and custom metrics; support labels, dimensional aggregation,
  recording rules, remote write/read or an equivalent durable protocol,
  cardinality controls, and a query language/API. The Kubernetes metrics API
  alone cannot supply this. Fixed service range queries are now present; still
  add managed Prometheus identity/token refresh, recording rules,
  query caching/concurrency limits, and configurable metric-schema profiles for
  ASP.NET Core runtime metrics and future OpenTelemetry metrics.

- [ ] **Build a dashboard and exploration layer.** Add composable dashboards,
  panels, variables, annotations, ad-hoc queries, sharing/export, provisioning
  as code, permissions, and a metric/log/trace explorer. This is the minimum
  Grafana-like workflow, not just fixed pages.

- [ ] **Build alerting and incident response.** Add alert rules, evaluation and
  deduplication, silences, routing/escalation, notification integrations,
  alert history, runbook links, and SLO/error-budget alerts. Test delivery and
  failure behavior end to end.

- [ ] **Build logs and trace correlation.** Add indexed log ingestion/search
  with retention controls, distributed trace storage/search, service maps,
  error analytics, and links between deploys, events, metrics, logs, and
  traces. Add browser RUM and synthetics only after the core backend signals.

- [ ] **Design identity and tenancy.** Implement SSO (OIDC/SAML as required),
  RBAC, teams/projects, per-tenant quotas and retention, audit trails, data
  isolation, and secret-management policies before accepting multiple users or
  clusters.

- [ ] **Support multi-cluster and lifecycle management.** Provide a secure
  agent/collector deployment model, central control plane or federation,
  upgrades, version compatibility, configuration-as-code, health reporting,
  and cost/usage accounting.

## P2 — high-value differentiators

- [ ] **Kubernetes cost and capacity intelligence:** rightsizing from usage
  history, request/limit recommendations, bin-packing visibility, idle
  workloads, and cost allocation by team/namespace/label.
- [ ] **Change intelligence:** correlate deploys, configuration changes,
  autoscaling, events, and incidents; add rollback-risk and anomaly views.
- [ ] **Complete service health views:** the first error/CPU/memory/pod signals
  now exist; add dependency/service maps, latency, traffic, SLO scorecards,
  error-budget forecasting, and release-health comparison.
- [ ] **Integrations:** Alertmanager-compatible webhooks, Slack/PagerDuty/email,
  GitHub/GitOps annotations, cloud-provider metrics, databases, and managed
  Kubernetes distributions.

## Suggested release gates

Do not market Beholdr as a replacement until the platform-architecture items
are implemented and independently load/security tested. For the near-term
observer release, all P0 items must be complete, P1 observer work must have an
explicitly accepted scope, and the release must pass a representative-cluster
test with a documented supported scale and recovery exercise.
