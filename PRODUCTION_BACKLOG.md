# Beholdr production backlog

## Product decision

Beholdr is currently a lightweight Kubernetes resource observer with external
telemetry-provider integration. It is **not** a replacement for New Relic,
Grafana, or Prometheus, and per the roadmap decided in #24 it does not intend
to become one: modules *consume* existing Prometheus, Elasticsearch, and
OpenTelemetry data rather than Beholdr owning ingestion, storage, or query
evaluation itself. Kubernetes state still has about one hour of in-memory
history, while Prometheus supplies bounded per-service long-range charts.
Beholdr has no notification delivery, log/trace search, durable storage of its
own, built-in authentication, or arbitrary metric query capability — and per
#24's stated non-goals, unrestricted user-supplied query access and owning the
telemetry database are deliberately out of scope before v1.

This document tracks execution against track 1 below. The architecture,
runtime-module contract, and version roadmap for track 2 are decided in #24;
items here that touch that direction are scoped to match it rather than
re-litigating it.

1. Make the existing observer safe and dependable for production use.
2. Extend it into a modular observability platform along the roadmap and
   non-goals #24 already decided, not by having Beholdr replace the
   telemetry systems it integrates with.

Priorities: **P0** blocks a production release; **P1** belongs in the first
production-ready release; **P2** is planned follow-up.

## Release status

The published `v0.2.0` tag is a **prerelease/preview**, not a completed
milestone: it shipped before all of the [v0.2.0 milestone](https://github.com/TimLange31/Beholdr/milestone/1)'s
required items (#4-#7) and release checks were finished, which the release
notes now say explicitly (see #34). The tag is not being moved. Per #24, a
tag and GitHub Release are only created once all required milestone items
and release-qualification checks pass — that policy applies from here on;
a labeled preview may still ship early. A 2026-09-05 audit of this preview
found further defects, tracked as patch-level fixes in the
[v0.2.1 milestone](https://github.com/TimLange31/Beholdr/milestone/7) (#25-#33),
independent of finishing the v0.2.0 feature scope.

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

## P1 — platform capabilities beyond the .NET module roadmap (scoped by #24)

The architecture decision this section used to ask for — integrate with
existing telemetry systems versus have Beholdr own ingestion/storage/query —
is made: #24 commits to the integration path, with owning the telemetry
database and unrestricted user-supplied query access as explicit non-goals
before v1. The items below are scoped to that decision; they are not a
proposal to revisit it.

- [ ] **Resolve what #24's roadmap leaves open within the integration
  decision.** Tenancy, data model for cross-provider correlation, retention
  policy for Beholdr's own operational state (see "Persist history" above),
  cost model, availability SLOs, and the migration path as more runtime
  modules and providers are added.

- [ ] **Adopt OpenTelemetry as an integration contract alongside Prometheus.**
  Support OTLP for traces, metrics, and logs from existing OpenTelemetry
  pipelines; propagate service/resource attributes; provide Kubernetes
  enrichment; and publish language/platform onboarding examples. Beholdr
  consumes this data from the systems that already store it — it does not
  stand up its own ingestion or storage path.

- [ ] **Extend metric consumption from Prometheus and OpenTelemetry.**
  Fixed, bounded, cached service-range queries with configurable metric/label
  profiles are delivered (see "Delivered foundations"); #4 covers finishing
  the configurable ASP.NET Core schema's remaining validation defects and #7
  covers managed Prometheus identity/token refresh. Remaining here: broader
  label/dimension support for future runtime modules and recording-rule
  guidance for keeping Beholdr's own queries cheap — not metric ingestion,
  storage, or a general query language, which stay out of scope per #24.

- [ ] **Build a dashboard and exploration layer.** Add composable dashboards,
  panels, variables, annotations, sharing/export, provisioning as code,
  permissions, and a metric/log/trace explorer scoped to the fixed queries
  Beholdr already knows how to run safely — not ad-hoc or arbitrary backend
  queries, which #24 lists as a non-goal before v1.

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
